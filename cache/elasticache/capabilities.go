package elasticache

import (
	"context"
	"sync"
	"time"
)

// flightCall coordinates a single in-flight load for a given key so that
// concurrent GetOrLoad callers share one call into the caller-supplied
// load function (thundering-herd protection).
type flightCall struct {
	wg  sync.WaitGroup
	val []byte
	err error
}

// Sweep is a no-op on the Redis backend.
//
// Redis expires keys eagerly on access and lazily via its own background
// sampler — this sweep is a no-op for API compatibility (satisfies
// cache.Sweeper) and always returns 0.
//
// A "real" sweep against Redis would require SCAN + TTL per key across
// every shard, which is expensive at any non-trivial cache size and does
// not remove entries any sooner than Redis's own expiry machinery would.
// Callers that need active TTL cleanup should rely on Redis itself.
func (c *Client) Sweep() int {
	return 0
}

// GetOrLoad returns the cached value for key. On a miss, load is invoked
// exactly once per concurrent miss (via an in-process single-flight lock)
// and the result is cached under the supplied ttl. Other concurrent
// callers for the same key wait for the single load and share its result.
//
// Semantics:
//   - Fast path: a cache hit returns the cached value without calling load.
//   - Miss path: the first arriving goroutine executes load; concurrent
//     callers for the same key block on it and receive the same (value,
//     error) pair.
//   - Errors from load propagate unwrapped; nothing is cached on error.
//   - A ttl of cache.NoExpiry (zero) stores the loaded value without
//     expiry, matching cache.Cache.SetWithTTL semantics.
//
// The single-flight lock is process-local. It coalesces requests inside
// this Client only; concurrent misses across processes / nodes will each
// call load. Cross-process herd protection is out of scope here — layer a
// distributed lock (e.g. SETNX with a random token + Lua release script)
// at the application level if you need it.
func (c *Client) GetOrLoad(
	ctx context.Context,
	key string,
	ttl time.Duration,
	load func(context.Context) ([]byte, error),
) ([]byte, error) {
	// Fast path — direct cache hit skips the flight bookkeeping entirely.
	if v, ok := c.Get(ctx, key); ok {
		return v, nil
	}

	// Register or attach to the in-flight load for this key.
	c.flightMu.Lock()
	if c.flights == nil {
		// Lazy init in case the Client was constructed by a zero-value
		// path (tests, embedding); the New() constructor also initializes.
		c.flights = make(map[string]*flightCall)
	}
	if fc, ok := c.flights[key]; ok {
		c.flightMu.Unlock()
		fc.wg.Wait()
		return fc.val, fc.err
	}
	fc := &flightCall{}
	fc.wg.Add(1)
	c.flights[key] = fc
	c.flightMu.Unlock()

	// Clean up the flight entry once we finish (success or error) so a
	// subsequent miss for the same key can start a fresh load.
	defer func() {
		c.flightMu.Lock()
		delete(c.flights, key)
		c.flightMu.Unlock()
		fc.wg.Done()
	}()

	// Re-check the cache under the flight — another goroutine may have
	// populated it between our fast-path check and our flight registration.
	if v, ok := c.Get(ctx, key); ok {
		fc.val = v
		return v, nil
	}

	v, err := load(ctx)
	if err != nil {
		fc.err = err
		return nil, err
	}

	if setErr := c.SetWithTTL(ctx, key, v, ttl); setErr != nil {
		// The load succeeded; surface the caching failure but still hand
		// the value back so callers aren't forced to redo the work.
		fc.val = v
		fc.err = setErr
		return v, setErr
	}
	fc.val = v
	return v, nil
}

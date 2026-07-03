package elasticache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"oss.nandlabs.io/golly/cache"
)

// Compile-time assertion (also exists in elasticache.go; double-checked
// here to surface as a test failure if the interface ever changes).
func TestClient_SatisfiesCacheInterface(t *testing.T) {
	var _ cache.Cache[string, []byte] = (*Client)(nil)
}

func TestClient_NilConfig(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected ErrInvalidConfig for nil config")
	}
	if _, err := New(&Config{}); err == nil {
		t.Fatal("expected ErrInvalidConfig for empty Addrs")
	}
}

// withMiniRedis spins up an in-process Redis emulator and returns a Client
// pointed at it. TLS is disabled because miniredis is plaintext-only.
func withMiniRedis(t *testing.T) (*Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := New(&Config{
		Addrs:      []string{mr.Addr()},
		DisableTLS: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, mr
}

func TestClient_SetGet(t *testing.T) {
	c, _ := withMiniRedis(t)
	ctx := context.Background()

	if err := c.Set(ctx, "k", []byte("v")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok := c.Get(ctx, "k")
	if !ok || string(got) != "v" {
		t.Fatalf("Get(k) = (%q, %v), want (\"v\", true)", got, ok)
	}
	if _, ok := c.Get(ctx, "missing"); ok {
		t.Fatalf("Get(missing) should report ok=false")
	}
}

func TestClient_TTLExpiry(t *testing.T) {
	c, mr := withMiniRedis(t)
	ctx := context.Background()

	if err := c.SetWithTTL(ctx, "tkey", []byte("tv"), 100*time.Millisecond); err != nil {
		t.Fatalf("SetWithTTL: %v", err)
	}
	if _, ok := c.Get(ctx, "tkey"); !ok {
		t.Fatalf("expected key present within TTL")
	}
	// miniredis exposes a deterministic clock — advance it past the TTL.
	mr.FastForward(200 * time.Millisecond)
	if _, ok := c.Get(ctx, "tkey"); ok {
		t.Fatalf("expected key absent past TTL")
	}
}

func TestClient_NoExpiry(t *testing.T) {
	c, mr := withMiniRedis(t)
	ctx := context.Background()
	_ = c.SetWithTTL(ctx, "k", []byte("v"), cache.NoExpiry)
	mr.FastForward(24 * time.Hour)
	if _, ok := c.Get(ctx, "k"); !ok {
		t.Fatalf("NoExpiry key should outlive any FastForward")
	}
}

func TestClient_DeleteAndHas(t *testing.T) {
	c, _ := withMiniRedis(t)
	ctx := context.Background()
	_ = c.Set(ctx, "x", []byte("1"))
	if !c.Has(ctx, "x") {
		t.Fatalf("Has(x) should be true after Set")
	}
	if !c.Delete(ctx, "x") {
		t.Fatalf("Delete(x) should report true on hit")
	}
	if c.Delete(ctx, "x") {
		t.Fatalf("Delete(x) should report false after key gone")
	}
	if c.Has(ctx, "x") {
		t.Fatalf("Has(x) should be false after Delete")
	}
}

func TestClient_ClearAndLen(t *testing.T) {
	c, _ := withMiniRedis(t)
	ctx := context.Background()
	for i, k := range []string{"a", "b", "c"} {
		_ = c.Set(ctx, k, []byte{byte('0' + i)})
	}
	if got := c.Len(ctx); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}
	if err := c.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got := c.Len(ctx); got != 0 {
		t.Fatalf("Len after Clear = %d, want 0", got)
	}
}

func TestClient_PingValidatesConnection(t *testing.T) {
	c, _ := withMiniRedis(t)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// --- Sweeper capability -----------------------------------------------------

func TestSweep_ReturnsZero(t *testing.T) {
	c, _ := withMiniRedis(t)
	// Seed some keys — Redis handles TTL server-side so Sweep is a no-op
	// regardless of contents.
	_ = c.SetWithTTL(context.Background(), "expiring", []byte("v"), time.Millisecond)
	_ = c.Set(context.Background(), "persistent", []byte("v"))
	if n := c.Sweep(); n != 0 {
		t.Fatalf("Sweep() = %d, want 0 (Redis handles expiry itself)", n)
	}
}

// --- Loader capability ------------------------------------------------------

func TestGetOrLoad_MissThenLoads(t *testing.T) {
	c, _ := withMiniRedis(t)
	ctx := context.Background()

	var called int32
	got, err := c.GetOrLoad(ctx, "k", time.Minute, func(ctx context.Context) ([]byte, error) {
		atomic.AddInt32(&called, 1)
		return []byte("loaded"), nil
	})
	if err != nil {
		t.Fatalf("GetOrLoad: %v", err)
	}
	if string(got) != "loaded" {
		t.Fatalf("GetOrLoad value = %q, want %q", got, "loaded")
	}
	if n := atomic.LoadInt32(&called); n != 1 {
		t.Fatalf("load invoked %d times, want 1", n)
	}
	// Result must have been cached.
	cached, ok := c.Get(ctx, "k")
	if !ok || string(cached) != "loaded" {
		t.Fatalf("cache after GetOrLoad = (%q, %v), want (%q, true)", cached, ok, "loaded")
	}
}

func TestGetOrLoad_HitSkipsLoad(t *testing.T) {
	c, _ := withMiniRedis(t)
	ctx := context.Background()

	if err := c.Set(ctx, "k", []byte("prewarmed")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	var called int32
	got, err := c.GetOrLoad(ctx, "k", time.Minute, func(ctx context.Context) ([]byte, error) {
		atomic.AddInt32(&called, 1)
		return []byte("SHOULD-NOT-RUN"), nil
	})
	if err != nil {
		t.Fatalf("GetOrLoad: %v", err)
	}
	if string(got) != "prewarmed" {
		t.Fatalf("value = %q, want %q (load must not run on hit)", got, "prewarmed")
	}
	if n := atomic.LoadInt32(&called); n != 0 {
		t.Fatalf("load invoked %d times on cache hit, want 0", n)
	}
}

func TestGetOrLoad_LoadErrorPropagates(t *testing.T) {
	c, _ := withMiniRedis(t)
	ctx := context.Background()

	sentinel := errors.New("upstream boom")
	got, err := c.GetOrLoad(ctx, "err-key", time.Minute, func(ctx context.Context) ([]byte, error) {
		return nil, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want (Is) %v — must propagate unwrapped", err, sentinel)
	}
	if got != nil {
		t.Fatalf("value on load error = %q, want nil", got)
	}
	if c.Has(ctx, "err-key") {
		t.Fatalf("key should not be cached when load errors")
	}
}

func TestGetOrLoad_SingleFlight_Concurrent(t *testing.T) {
	c, _ := withMiniRedis(t)
	ctx := context.Background()

	const N = 32
	var called int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([][]byte, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			v, err := c.GetOrLoad(ctx, "herd", time.Minute, func(ctx context.Context) ([]byte, error) {
				atomic.AddInt32(&called, 1)
				// Widen the race window so concurrent callers pile up on
				// the same flight.
				time.Sleep(30 * time.Millisecond)
				return []byte("winner"), nil
			})
			if err != nil {
				t.Errorf("GetOrLoad: %v", err)
				return
			}
			results[i] = v
		}(i)
	}
	close(start)
	wg.Wait()

	if n := atomic.LoadInt32(&called); n != 1 {
		t.Fatalf("load invoked %d times under concurrent misses, want exactly 1", n)
	}
	for i, r := range results {
		if string(r) != "winner" {
			t.Fatalf("goroutine %d saw %q, want %q", i, r, "winner")
		}
	}
}

func TestClient_CredentialsProviderInvoked(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireUserAuth("appuser", "rotating-token-v1")

	var calls int
	c, err := New(&Config{
		Addrs:      []string{mr.Addr()},
		Username:   "appuser",
		DisableTLS: true,
		CredentialsProvider: func(ctx context.Context) (string, error) {
			calls++
			return "rotating-token-v1", nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if calls == 0 {
		t.Fatalf("CredentialsProvider was never called")
	}
}

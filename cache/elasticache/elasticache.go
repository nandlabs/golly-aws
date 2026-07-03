// Package elasticache provides an oss.nandlabs.io/golly/cache.Cache
// backend for AWS ElastiCache for Redis. It wraps go-redis/v9's universal
// client so the same code works against:
//
//   - A single-instance ElastiCache cluster (one configuration endpoint).
//   - A cluster-mode-enabled ElastiCache cluster (configuration endpoint
//     resolves to multiple shards).
//
// Both AUTH-token and IAM auth are supported; pair the IAM helper in
// iam.go with Config.CredentialsProvider for short-lived rotating
// tokens.
package elasticache

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"oss.nandlabs.io/golly/cache"
)

// Config configures an ElastiCache connection.
//
// Addrs accepts the configuration endpoint of an ElastiCache cluster.
// For cluster-mode-enabled clusters, list the configuration endpoint once;
// go-redis discovers shards automatically via the CLUSTER SLOTS command.
type Config struct {
	// Addrs is the list of ElastiCache endpoints (host:port). A single
	// entry is treated as a primary endpoint; multiple entries trigger
	// go-redis's cluster-aware client.
	Addrs []string

	// Username is the RBAC user (Redis 6+). Leave empty when using a
	// classic AUTH token or anonymous access.
	Username string

	// Password is a static AUTH token. Mutually exclusive with
	// CredentialsProvider — if both are set, CredentialsProvider wins.
	Password string

	// CredentialsProvider returns a fresh AUTH token on every connection
	// attempt. Use IAMAuthProvider for ElastiCache for Redis 7.x IAM
	// authentication; the token rotates every 15 minutes.
	CredentialsProvider func(ctx context.Context) (string, error)

	// TLSConfig controls TLS in transit. The zero value enables TLS with
	// MinVersion = TLS 1.2 (ElastiCache best practice). Set to a custom
	// *tls.Config to override (e.g. for a private CA or InsecureSkipVerify
	// in tests). Pass DisableTLS = true to send unencrypted traffic.
	TLSConfig *tls.Config

	// DisableTLS forces plaintext. Default false — TLS is on by default.
	// Use only against test endpoints (miniredis, local fakes).
	DisableTLS bool

	// PoolSize, ReadTimeout, WriteTimeout, DialTimeout — passthroughs to
	// go-redis's UniversalOptions. Zero values use go-redis defaults.
	PoolSize     int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	DialTimeout  time.Duration
}

// Client is a cache.Cache[string, []byte] backed by ElastiCache for Redis.
// Values are stored as raw bytes; pair with a typed wrapper (or codec)
// at the call site for richer values.
//
// Client also satisfies the optional cache.Sweeper and cache.Loader
// capability interfaces (see capabilities.go).
type Client struct {
	rdb redis.UniversalClient

	// flightMu guards flights. In-process single-flight coordination for
	// GetOrLoad; see capabilities.go.
	flightMu sync.Mutex
	flights  map[string]*flightCall
}

// Compile-time check that Client satisfies cache.Cache[string, []byte]
// and the optional Sweeper + Loader capability interfaces.
var (
	_ cache.Cache[string, []byte]  = (*Client)(nil)
	_ cache.Sweeper                = (*Client)(nil)
	_ cache.Loader[string, []byte] = (*Client)(nil)
)

// ErrInvalidConfig is returned by New when Config is unusable.
var ErrInvalidConfig = errors.New("elasticache: invalid config")

// New returns a ready-to-use Client. Returns ErrInvalidConfig if Addrs is
// empty. The underlying redis client connects lazily — New itself does
// not perform I/O. Call Client.Ping(ctx) to validate connectivity.
func New(cfg *Config) (*Client, error) {
	if cfg == nil || len(cfg.Addrs) == 0 {
		return nil, fmt.Errorf("%w: at least one address required", ErrInvalidConfig)
	}

	opts := &redis.UniversalOptions{
		Addrs:        cfg.Addrs,
		Username:     cfg.Username,
		Password:     cfg.Password,
		PoolSize:     cfg.PoolSize,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		DialTimeout:  cfg.DialTimeout,
	}

	// Dynamic credentials (e.g. IAM tokens) take precedence over the
	// static Password.
	if cfg.CredentialsProvider != nil {
		username := cfg.Username
		opts.CredentialsProviderContext = func(ctx context.Context) (string, string, error) {
			token, err := cfg.CredentialsProvider(ctx)
			if err != nil {
				return "", "", fmt.Errorf("elasticache: credentials provider: %w", err)
			}
			return username, token, nil
		}
	}

	if !cfg.DisableTLS {
		if cfg.TLSConfig != nil {
			opts.TLSConfig = cfg.TLSConfig
		} else {
			opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
	}

	return &Client{
		rdb:     redis.NewUniversalClient(opts),
		flights: make(map[string]*flightCall),
	}, nil
}

// Ping verifies connectivity by issuing a Redis PING. Useful right after
// New to surface auth/TLS misconfiguration up front.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// Get returns the value for key. Missing keys return (nil, false).
func (c *Client) Get(ctx context.Context, key string) ([]byte, bool) {
	v, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false
	}
	if err != nil {
		return nil, false
	}
	return v, true
}

// Set stores value under key with no expiry.
func (c *Client) Set(ctx context.Context, key string, value []byte) error {
	return c.rdb.Set(ctx, key, value, 0).Err()
}

// SetWithTTL stores value under key with the given TTL. A ttl of
// cache.NoExpiry (zero) means the entry never expires.
func (c *Client) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

// Delete removes the key. Returns true when the key existed.
func (c *Client) Delete(ctx context.Context, key string) bool {
	n, err := c.rdb.Del(ctx, key).Result()
	if err != nil {
		return false
	}
	return n > 0
}

// Has reports whether key is present (and not expired server-side).
func (c *Client) Has(ctx context.Context, key string) bool {
	n, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false
	}
	return n > 0
}

// Clear empties the cache via FLUSHDB. This is a destructive admin
// operation — only call against caches that exclusively belong to this
// application.
//
// For cluster-mode-enabled deployments, the underlying redis client runs
// FLUSHDB on every shard.
func (c *Client) Clear(ctx context.Context) error {
	switch v := c.rdb.(type) {
	case *redis.ClusterClient:
		return v.ForEachMaster(ctx, func(ctx context.Context, m *redis.Client) error {
			return m.FlushDB(ctx).Err()
		})
	default:
		return c.rdb.FlushDB(ctx).Err()
	}
}

// Len returns the total number of keys via DBSIZE. For cluster deployments
// it sums DBSIZE across masters. Approximate under concurrent writes.
func (c *Client) Len(ctx context.Context) int {
	switch v := c.rdb.(type) {
	case *redis.ClusterClient:
		total := int64(0)
		_ = v.ForEachMaster(ctx, func(ctx context.Context, m *redis.Client) error {
			n, err := m.DBSize(ctx).Result()
			if err == nil {
				total += n
			}
			return nil
		})
		return int(total)
	default:
		n, err := c.rdb.DBSize(ctx).Result()
		if err != nil {
			return 0
		}
		return int(n)
	}
}

// Close releases the underlying redis client and its connection pool.
// Safe to call multiple times.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Package cache provides a thin wrapper around a Redis client for the
// url-shortener's read-through cache.
//
// The wrapper exposes only the surface the rest of the codebase needs
// (Get/Set/Del/Ping), which keeps the call sites stable if we later swap
// implementations or add observability. A cache miss is signaled by the
// (found=false, err=nil) tuple from Get -- never as an error -- so callers
// don't need to know about the underlying redis.Nil sentinel.
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client is the cache facade.
type Client struct {
	rdb *redis.Client
}

// New parses redisURL, dials, and pings to verify connectivity. The caller
// must Close it when done.
func New(ctx context.Context, redisURL string) (*Client, error) {
	if redisURL == "" {
		return nil, errors.New("cache: redis url is empty")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("cache: parse url: %w", err)
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("cache: ping: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// Close releases the underlying connection pool. Safe to call on a nil
// receiver to simplify cleanup paths.
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// Ping verifies the connection. Used as a /readyz check.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// Get returns (value, true, nil) on a hit, ("", false, nil) on a miss, and
// ("", false, err) on a real error.
func (c *Client) Get(ctx context.Context, key string) (string, bool, error) {
	v, err := c.rdb.Get(ctx, key).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("cache: get: %w", err)
	}
	return v, true, nil
}

// Set stores value under key with the given TTL. A zero TTL means "no
// expiry"; in that case use a long explicit duration if you actually want
// short-lived data.
func (c *Client) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("cache: set: %w", err)
	}
	return nil
}

// Del removes one or more keys. Missing keys are not errors.
func (c *Client) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("cache: del: %w", err)
	}
	return nil
}

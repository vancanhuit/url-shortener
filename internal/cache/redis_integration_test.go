//go:build integration

// Integration tests for the Redis cache wrapper. Run with:
//
//	just test-integration
//
// Skipped automatically when URL_SHORTENER_TEST_REDIS_URL is unset.

package cache_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/vancanhuit/url-shortener/internal/cache"
)

func newClient(t *testing.T) *cache.Client {
	t.Helper()
	url := os.Getenv("URL_SHORTENER_TEST_REDIS_URL")
	if url == "" {
		t.Fatal("URL_SHORTENER_TEST_REDIS_URL must be set to run integration tests")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	c, err := cache.New(ctx, url)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// uniqueKey returns a key that does not collide with concurrent test runs.
func uniqueKey(t *testing.T, label string) string {
	t.Helper()
	return "test:" + label + ":" + time.Now().UTC().Format("150405.000000")
}

func TestSetAndGet(t *testing.T) {
	c := newClient(t)
	ctx := t.Context()
	key := uniqueKey(t, "setget")

	if err := c.Set(ctx, key, "hello", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	t.Cleanup(func() { _ = c.Del(context.Background(), key) })

	v, ok, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatalf("Get returned miss for key set just above")
	}
	if v != "hello" {
		t.Errorf("v = %q, want \"hello\"", v)
	}
}

func TestGet_MissReturnsFoundFalseNoError(t *testing.T) {
	c := newClient(t)
	ctx := t.Context()

	v, ok, err := c.Get(ctx, uniqueKey(t, "miss"))
	if err != nil {
		t.Fatalf("Get on missing key returned error: %v", err)
	}
	if ok {
		t.Errorf("expected miss, got value %q", v)
	}
}

func TestSet_ExpiryRemovesEntry(t *testing.T) {
	c := newClient(t)
	ctx := t.Context()
	key := uniqueKey(t, "ttl")

	if err := c.Set(ctx, key, "soon-gone", 50*time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	_, ok, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Errorf("entry still present after TTL")
	}
}

func TestDel(t *testing.T) {
	c := newClient(t)
	ctx := t.Context()
	key := uniqueKey(t, "del")

	if err := c.Set(ctx, key, "v", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := c.Del(ctx, key); err != nil {
		t.Fatalf("Del: %v", err)
	}
	_, ok, _ := c.Get(ctx, key)
	if ok {
		t.Errorf("key still present after Del")
	}
}

func TestPing(t *testing.T) {
	c := newClient(t)
	if err := c.Ping(t.Context()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

// TestRateLimit_FixedWindowBehavior verifies the core contract of
// RateLimit: the first `limit` calls for a key are allowed, the next
// are denied, and the counter resets after the window expires.
func TestRateLimit_FixedWindowBehavior(t *testing.T) {
	c := newClient(t)
	ctx := t.Context()
	key := uniqueKey(t, "rl")

	const limit = 3
	const window = 200 * time.Millisecond

	t.Cleanup(func() { _ = c.Del(context.Background(), key) })

	// First `limit` calls must be allowed.
	for i := range limit {
		allowed, remaining, err := c.RateLimit(ctx, key, limit, window)
		if err != nil {
			t.Fatalf("call %d: RateLimit: %v", i+1, err)
		}
		if !allowed {
			t.Errorf("call %d: allowed = false, want true", i+1)
		}
		want := limit - (i + 1)
		if remaining != want {
			t.Errorf("call %d: remaining = %d, want %d", i+1, remaining, want)
		}
	}

	// (limit+1)-th call must be denied.
	allowed, _, err := c.RateLimit(ctx, key, limit, window)
	if err != nil {
		t.Fatalf("over-limit call: RateLimit: %v", err)
	}
	if allowed {
		t.Errorf("over-limit call: allowed = true, want false")
	}

	// After the window expires the counter resets.
	time.Sleep(window + 50*time.Millisecond)

	allowed, _, err = c.RateLimit(ctx, key, limit, window)
	if err != nil {
		t.Fatalf("post-reset call: RateLimit: %v", err)
	}
	if !allowed {
		t.Errorf("post-reset call: allowed = false, want true (window should have reset)")
	}
}

// TestRateLimit_PerKeyIsolation: distinct keys (different IPs) each
// get independent counters.
func TestRateLimit_PerKeyIsolation(t *testing.T) {
	c := newClient(t)
	ctx := t.Context()

	keyA := uniqueKey(t, "rl-a")
	keyB := uniqueKey(t, "rl-b")
	t.Cleanup(func() {
		_ = c.Del(context.Background(), keyA, keyB)
	})

	const limit = 1
	const window = time.Second

	// Exhaust key B.
	if _, _, err := c.RateLimit(ctx, keyB, limit, window); err != nil {
		t.Fatalf("B first: %v", err)
	}
	allowed, _, err := c.RateLimit(ctx, keyB, limit, window)
	if err != nil {
		t.Fatalf("B second: %v", err)
	}
	if allowed {
		t.Fatalf("B second: allowed = true, want false")
	}

	// Key A must still be within budget.
	allowed, _, err = c.RateLimit(ctx, keyA, limit, window)
	if err != nil {
		t.Fatalf("A first: %v", err)
	}
	if !allowed {
		t.Errorf("A first: allowed = false, want true (keys must be isolated)")
	}
}

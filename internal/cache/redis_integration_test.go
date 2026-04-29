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
		t.Skip("URL_SHORTENER_TEST_REDIS_URL not set; skipping integration test")
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

package cache

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestCachedResponseSerialization tests that CachedResponse can be cleanly JSON marshaled and unmarshaled.
func TestCachedResponseSerialization(t *testing.T) {
	orig := &CachedResponse{
		StatusCode: 200,
		Headers: map[string][]string{
			"Content-Type": {"application/json"},
			"X-Custom":     {"foo", "bar"},
		},
		Body:     []byte(`{"message":"hello world"}`),
		CachedAt: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded CachedResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.StatusCode != orig.StatusCode {
		t.Errorf("expected status %d, got %d", orig.StatusCode, decoded.StatusCode)
	}
	if string(decoded.Body) != string(orig.Body) {
		t.Errorf("expected body %s, got %s", orig.Body, decoded.Body)
	}
	if len(decoded.Headers["X-Custom"]) != 2 {
		t.Errorf("expected 2 custom headers, got %d", len(decoded.Headers["X-Custom"]))
	}
}

// isRedisAvailable checks whether a local Redis instance is reachable.
func isRedisAvailable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func TestRedisCache_LiveIntegration(t *testing.T) {
	addr := "localhost:6379"
	if !isRedisAvailable(addr) {
		t.Skip("skipping Redis integration test: no local Redis server running on", addr)
	}

	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skip("skipping Redis integration test: ping failed:", err)
	}

	c := NewRedisCache(client, "test:proxy:cache:", nil)
	defer c.Flush()

	// 1. Miss on empty key
	_, ok := c.Get("missing-key")
	if ok {
		t.Fatalf("expected miss on missing-key")
	}

	// 2. Set and Get
	resp := &CachedResponse{
		StatusCode: 200,
		Headers: map[string][]string{
			"Content-Type": {"text/plain"},
		},
		Body:     []byte("redis cached body"),
		CachedAt: time.Now(),
	}

	if err := c.Set("item-1", resp, 2*time.Second); err != nil {
		t.Fatalf("failed to set in redis: %v", err)
	}

	got, ok := c.Get("item-1")
	if !ok {
		t.Fatalf("expected hit on item-1")
	}
	if got.StatusCode != 200 || string(got.Body) != "redis cached body" {
		t.Errorf("unexpected value from redis: %+v", got)
	}

	// 3. Delete
	if err := c.Delete("item-1"); err != nil {
		t.Fatalf("failed to delete key: %v", err)
	}

	_, ok = c.Get("item-1")
	if ok {
		t.Fatalf("expected miss after delete")
	}

	// 4. Stats
	s := c.Stats()
	if s.Hits < 1 || s.Misses < 1 {
		t.Errorf("unexpected stats: %+v", s)
	}
}

// TestRedisCache_UnreachableFallback verifies that if Redis is unreachable,
// Get returns (nil, false) gracefully without crashing.
func TestRedisCache_UnreachableFallback(t *testing.T) {
	// Point to an unused port to simulate Redis being down
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:54321",
		DialTimeout: 50 * time.Millisecond,
		ReadTimeout: 50 * time.Millisecond,
	})
	defer client.Close()

	c := NewRedisCache(client, "test:", nil)

	// Get should return false without panic or error
	val, ok := c.Get("some-key")
	if ok || val != nil {
		t.Errorf("expected miss on unreachable redis, got hit")
	}

	if c.Stats().Misses != 1 {
		t.Errorf("expected 1 miss, got %d", c.Stats().Misses)
	}
}

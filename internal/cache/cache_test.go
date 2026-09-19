package cache

import (
	"sync"
	"testing"
	"time"
)

// TestIsCacheable verifies idempotency and credential rules.
func TestIsCacheable(t *testing.T) {
	tests := []struct {
		method  string
		headers map[string][]string
		want    bool
	}{
		{"GET", nil, true},
		{"HEAD", nil, true},
		{"POST", nil, false},
		{"PUT", nil, false},
		{"PATCH", nil, false},
		{"DELETE", nil, false},
		{"GET", map[string][]string{"Authorization": {"Bearer token"}}, false},
		{"GET", map[string][]string{"Cookie": {"session=abc"}}, false},
		{"HEAD", map[string][]string{"X-Custom": {"value"}}, true},
	}

	for _, tt := range tests {
		got := IsCacheable(tt.method, tt.headers)
		if got != tt.want {
			t.Errorf("IsCacheable(%q, %v) = %v, want %v", tt.method, tt.headers, got, tt.want)
		}
	}
}

// TestBuildKey verifies the canonical key construction.
func TestBuildKey(t *testing.T) {
	key := BuildKey("GET", "http://example.com/api/data?q=1")
	if key != "GET|http://example.com/api/data?q=1" {
		t.Errorf("unexpected key: %q", key)
	}
}

// TestMemoryCache_SetAndGet validates basic set/get behaviour.
func TestMemoryCache_SetAndGet(t *testing.T) {
	c := NewMemoryCache(100)

	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"ok":true}`),
		CachedAt:   time.Now(),
	}

	if err := c.Set("key1", resp, time.Minute); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}

	got, ok := c.Get("key1")
	if !ok {
		t.Fatal("expected cache hit, got miss")
	}
	if got.StatusCode != 200 {
		t.Errorf("expected 200, got %d", got.StatusCode)
	}
	if string(got.Body) != `{"ok":true}` {
		t.Errorf("unexpected body: %s", got.Body)
	}
}

// TestMemoryCache_Miss validates that missing keys return false.
func TestMemoryCache_Miss(t *testing.T) {
	c := NewMemoryCache(100)
	_, ok := c.Get("nonexistent")
	if ok {
		t.Fatal("expected cache miss, got hit")
	}
	if c.Stats().Misses != 1 {
		t.Errorf("expected 1 miss, got %d", c.Stats().Misses)
	}
}

// TestMemoryCache_TTLExpiry verifies that entries expire after TTL.
func TestMemoryCache_TTLExpiry(t *testing.T) {
	c := NewMemoryCache(100)

	resp := &CachedResponse{StatusCode: 200, Body: []byte("data")}
	if err := c.Set("expired-key", resp, 30*time.Millisecond); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}

	// Should be a hit before TTL
	if _, ok := c.Get("expired-key"); !ok {
		t.Fatal("expected cache hit before TTL expiry")
	}

	time.Sleep(50 * time.Millisecond)

	// Should be a miss after TTL
	if _, ok := c.Get("expired-key"); ok {
		t.Fatal("expected cache miss after TTL expiry")
	}
}

// TestMemoryCache_Delete verifies explicit invalidation.
func TestMemoryCache_Delete(t *testing.T) {
	c := NewMemoryCache(100)
	resp := &CachedResponse{StatusCode: 200, Body: []byte("value")}

	c.Set("del-key", resp, time.Minute)
	if _, ok := c.Get("del-key"); !ok {
		t.Fatal("expected hit before delete")
	}

	c.Delete("del-key")
	if _, ok := c.Get("del-key"); ok {
		t.Fatal("expected miss after delete")
	}
}

// TestMemoryCache_Flush removes all entries.
func TestMemoryCache_Flush(t *testing.T) {
	c := NewMemoryCache(100)
	for i := 0; i < 10; i++ {
		c.Set(string(rune('a'+i)), &CachedResponse{StatusCode: 200}, time.Minute)
	}
	if c.Stats().Size != 10 {
		t.Fatalf("expected 10 entries, got %d", c.Stats().Size)
	}

	c.Flush()
	if c.Stats().Size != 0 {
		t.Errorf("expected 0 entries after flush, got %d", c.Stats().Size)
	}
}

// TestMemoryCache_LRUEviction verifies that the LRU entry is evicted when full.
func TestMemoryCache_LRUEviction(t *testing.T) {
	c := NewMemoryCache(3)

	c.Set("a", &CachedResponse{StatusCode: 200}, time.Minute)
	c.Set("b", &CachedResponse{StatusCode: 200}, time.Minute)
	c.Set("c", &CachedResponse{StatusCode: 200}, time.Minute)

	// Access "a" so it becomes most-recently-used; "b" is now LRU
	c.Get("a")

	// Adding "d" should evict "b" (the LRU)
	c.Set("d", &CachedResponse{StatusCode: 200}, time.Minute)

	if _, ok := c.Get("b"); ok {
		t.Fatal("expected 'b' to be evicted as LRU entry")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected 'a' to still be in cache")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("expected 'c' to still be in cache")
	}
	if _, ok := c.Get("d"); !ok {
		t.Fatal("expected 'd' to still be in cache")
	}

	if c.Stats().Evictions != 1 {
		t.Errorf("expected 1 eviction, got %d", c.Stats().Evictions)
	}
}

// TestMemoryCache_SizeLimit verifies the cache size is bounded.
func TestMemoryCache_SizeLimit(t *testing.T) {
	maxSize := 5
	c := NewMemoryCache(maxSize)

	for i := 0; i < 20; i++ {
		key := string(rune('a' + i))
		c.Set(key, &CachedResponse{StatusCode: 200}, time.Minute)
	}

	if c.Stats().Size > int64(maxSize) {
		t.Errorf("cache size %d exceeds maxSize %d", c.Stats().Size, maxSize)
	}
}

// TestMemoryCache_Stats verifies hit and miss counters.
func TestMemoryCache_Stats(t *testing.T) {
	c := NewMemoryCache(100)

	c.Set("x", &CachedResponse{StatusCode: 200}, time.Minute)
	c.Get("x") // hit
	c.Get("x") // hit
	c.Get("y") // miss
	c.Get("z") // miss

	s := c.Stats()
	if s.Hits != 2 {
		t.Errorf("expected 2 hits, got %d", s.Hits)
	}
	if s.Misses != 2 {
		t.Errorf("expected 2 misses, got %d", s.Misses)
	}
}

// TestMemoryCache_Concurrency verifies thread-safety under concurrent load.
func TestMemoryCache_Concurrency(t *testing.T) {
	c := NewMemoryCache(50)

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := string(rune('a' + (i % 26)))
			c.Set(key, &CachedResponse{StatusCode: 200, Body: []byte("data")}, time.Minute)
			c.Get(key)
			c.Delete(key)
		}(i)
	}
	wg.Wait()
}

// TestMemoryCache_NoTTL verifies that entries without TTL never expire.
func TestMemoryCache_NoTTL(t *testing.T) {
	c := NewMemoryCache(100)
	resp := &CachedResponse{StatusCode: 200, Body: []byte("permanent")}

	c.Set("no-ttl", resp, 0) // 0 = no expiry

	time.Sleep(10 * time.Millisecond)

	if _, ok := c.Get("no-ttl"); !ok {
		t.Fatal("expected entry with no TTL to be present")
	}
}

// BenchmarkMemoryCache_Get benchmarks cache read throughput.
func BenchmarkMemoryCache_Get(b *testing.B) {
	c := NewMemoryCache(1000)
	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       make([]byte, 1024), // 1KB body
	}

	for i := 0; i < 100; i++ {
		c.Set(string(rune('a'+i%26))+string(rune('0'+i/26)), resp, time.Hour)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := string(rune('a'+i%26)) + string(rune('0'+i/26%10))
			c.Get(key)
			i++
		}
	})
}

// BenchmarkMemoryCache_Set benchmarks cache write throughput.
func BenchmarkMemoryCache_Set(b *testing.B) {
	c := NewMemoryCache(1000)
	resp := &CachedResponse{
		StatusCode: 200,
		Body:       make([]byte, 1024),
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := string(rune('a'+i%26)) + string(rune('0'+i/26%10))
			c.Set(key, resp, time.Hour)
			i++
		}
	})
}

package cache

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// BenchmarkCache_Disabled measures latency and allocations when caching is bypassed,
// forcing every request to execute the backend handler.
func BenchmarkCache_Disabled(b *testing.B) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","items":[1,2,3,4,5]}`))
	}))
	defer backend.Close()

	client := backend.Client()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(backend.URL + "/api/products")
		if err != nil {
			b.Fatalf("request failed: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

// BenchmarkCache_Enabled measures latency and allocations when caching is enabled,
// serving requests directly from memory cache on hit.
func BenchmarkCache_Enabled(b *testing.B) {
	c := NewMemoryCache(1000)
	key := BuildKey("GET", "/api/products")
	resp := &CachedResponse{
		StatusCode: http.StatusOK,
		Headers: map[string][]string{
			"Content-Type": {"application/json"},
		},
		Body:     []byte(`{"status":"ok","items":[1,2,3,4,5]}`),
		CachedAt: time.Now(),
	}
	_ = c.Set(key, resp, time.Hour)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cached, ok := c.Get(key)
		if !ok || cached == nil {
			b.Fatalf("cache hit expected")
		}
	}
}

// BenchmarkCache_Enabled_Parallel measures concurrent cache read throughput across multiple CPU cores.
func BenchmarkCache_Enabled_Parallel(b *testing.B) {
	c := NewMemoryCache(1000)
	key := BuildKey("GET", "/api/products")
	resp := &CachedResponse{
		StatusCode: http.StatusOK,
		Headers: map[string][]string{
			"Content-Type": {"application/json"},
		},
		Body:     []byte(`{"status":"ok","items":[1,2,3,4,5]}`),
		CachedAt: time.Now(),
	}
	_ = c.Set(key, resp, time.Hour)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cached, ok := c.Get(key)
			if !ok || cached == nil {
				b.Fatalf("cache hit expected")
			}
		}
	})
}

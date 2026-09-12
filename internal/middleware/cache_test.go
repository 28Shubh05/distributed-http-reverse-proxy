package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/cache"
)

func TestCachingMiddleware_HitAndMiss(t *testing.T) {
	memCache := cache.NewMemoryCache(100)
	backendCalls := atomic.Int32{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":"value"}`))
	})

	cachedHandler := Caching(memCache, CacheConfig{
		TTL: 1 * time.Minute,
	})(handler)

	// First request: Cache Miss
	req1 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	rr1 := httptest.NewRecorder()
	cachedHandler.ServeHTTP(rr1, req1)

	if rr1.Header().Get("X-Cache") != "MISS" {
		t.Errorf("expected X-Cache MISS on first request, got %q", rr1.Header().Get("X-Cache"))
	}
	if backendCalls.Load() != 1 {
		t.Errorf("expected 1 backend call on miss, got %d", backendCalls.Load())
	}
	body1, _ := io.ReadAll(rr1.Body)
	if string(body1) != `{"data":"value"}` {
		t.Errorf("unexpected body on miss: %s", body1)
	}

	// Second request: Cache Hit
	req2 := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	rr2 := httptest.NewRecorder()
	cachedHandler.ServeHTTP(rr2, req2)

	if rr2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("expected X-Cache HIT on second request, got %q", rr2.Header().Get("X-Cache"))
	}
	if backendCalls.Load() != 1 {
		t.Errorf("expected backend NOT to be called on hit, but call count is %d", backendCalls.Load())
	}
	body2, _ := io.ReadAll(rr2.Body)
	if string(body2) != `{"data":"value"}` {
		t.Errorf("unexpected body on hit: %s", body2)
	}
}

func TestCachingMiddleware_InvalidateOnMutate(t *testing.T) {
	memCache := cache.NewMemoryCache(100)
	backendValue := "initial"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			backendValue = "updated"
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("updated"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(backendValue))
	})

	cachedHandler := Caching(memCache, CacheConfig{
		TTL:                1 * time.Minute,
		InvalidateOnMutate: true,
	})(handler)

	// 1. GET to populate cache
	req1 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	rr1 := httptest.NewRecorder()
	cachedHandler.ServeHTTP(rr1, req1)
	if rr1.Body.String() != "initial" {
		t.Fatalf("expected initial, got %s", rr1.Body.String())
	}

	// 2. GET again should hit cache
	req2 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	rr2 := httptest.NewRecorder()
	cachedHandler.ServeHTTP(rr2, req2)
	if rr2.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("expected HIT on second GET")
	}

	// 3. POST to /resource to mutate state -> should invalidate cache
	postReq := httptest.NewRequest(http.MethodPost, "/resource", nil)
	postRR := httptest.NewRecorder()
	cachedHandler.ServeHTTP(postRR, postReq)
	if postRR.Code != http.StatusOK {
		t.Fatalf("POST failed with code %d", postRR.Code)
	}

	// 4. GET again should MISS and fetch new backendValue "updated"
	req3 := httptest.NewRequest(http.MethodGet, "/resource", nil)
	rr3 := httptest.NewRecorder()
	cachedHandler.ServeHTTP(rr3, req3)
	if rr3.Header().Get("X-Cache") != "MISS" {
		t.Errorf("expected MISS after invalidation, got %q", rr3.Header().Get("X-Cache"))
	}
	if rr3.Body.String() != "updated" {
		t.Errorf("expected updated body, got %s", rr3.Body.String())
	}
}

func TestCachingMiddleware_Bypass(t *testing.T) {
	memCache := cache.NewMemoryCache(100)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("response"))
	})

	cachedHandler := Caching(memCache, DefaultCacheConfig())(handler)

	// Auth header should bypass
	reqAuth := httptest.NewRequest(http.MethodGet, "/secure", nil)
	reqAuth.Header.Set("Authorization", "Bearer xyz")
	rrAuth := httptest.NewRecorder()
	cachedHandler.ServeHTTP(rrAuth, reqAuth)
	if rrAuth.Header().Get("X-Cache") != "BYPASS" {
		t.Errorf("expected BYPASS on auth request, got %q", rrAuth.Header().Get("X-Cache"))
	}

	// Cache-Control: no-cache should bypass
	reqNoCache := httptest.NewRequest(http.MethodGet, "/data", nil)
	reqNoCache.Header.Set("Cache-Control", "no-cache")
	rrNoCache := httptest.NewRecorder()
	cachedHandler.ServeHTTP(rrNoCache, reqNoCache)
	if rrNoCache.Header().Get("X-Cache") != "BYPASS" {
		t.Errorf("expected BYPASS on Cache-Control: no-cache, got %q", rrNoCache.Header().Get("X-Cache"))
	}
}

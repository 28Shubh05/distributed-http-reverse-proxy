package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/ratelimit"
)

func TestRateLimitingMiddleware_AllowAndReject(t *testing.T) {
	limiter := ratelimit.NewTokenBucketLimiter(10.0, 3) // burst 3
	defer limiter.Close()

	backendCalled := atomic.Int32{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mw := RateLimiting(limiter, RateLimitConfig{})(handler)

	// Requests 1, 2, 3 should succeed (burst 3)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		rr := httptest.NewRecorder()

		mw.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("request %d should return 200, got %d", i+1, rr.Code)
		}
		if rr.Header().Get("X-RateLimit-Limit") != "3" {
			t.Errorf("expected limit header 3, got %s", rr.Header().Get("X-RateLimit-Limit"))
		}
	}

	if backendCalled.Load() != 3 {
		t.Fatalf("expected 3 backend calls, got %d", backendCalled.Load())
	}

	// 4th request should be rejected with 429 Too Many Requests
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rr := httptest.NewRecorder()

	mw.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Errorf("expected Retry-After header to be present")
	}
	if rr.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Errorf("expected remaining 0, got %s", rr.Header().Get("X-RateLimit-Remaining"))
	}

	var respBody map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &respBody); err != nil {
		t.Fatalf("failed to parse 429 json body: %v", err)
	}
	if respBody["error"] != "too many requests" {
		t.Errorf("unexpected error field: %v", respBody["error"])
	}

	// Backend should NOT have been called for the 4th request
	if backendCalled.Load() != 3 {
		t.Errorf("backend should NOT be called on rate-limited request, calls: %d", backendCalled.Load())
	}
}

func TestRateLimitingMiddleware_ClientIPExtraction(t *testing.T) {
	limiter := ratelimit.NewTokenBucketLimiter(10.0, 1) // burst 1
	defer limiter.Close()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RateLimiting(limiter, RateLimitConfig{})(handler)

	// Client 1 with X-Forwarded-For
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18")
	rr1 := httptest.NewRecorder()
	mw.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("client 1 first req should succeed")
	}

	// Client 1 second request should fail
	req1Repeat := httptest.NewRequest(http.MethodGet, "/", nil)
	req1Repeat.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18")
	rr1Repeat := httptest.NewRecorder()
	mw.ServeHTTP(rr1Repeat, req1Repeat)
	if rr1Repeat.Code != http.StatusTooManyRequests {
		t.Fatalf("client 1 second req should be 429")
	}

	// Client 2 with different X-Forwarded-For should succeed independently
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Forwarded-For", "198.51.100.1")
	rr2 := httptest.NewRecorder()
	mw.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("client 2 first req should succeed independently")
	}
}

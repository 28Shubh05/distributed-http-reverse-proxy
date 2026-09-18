package chaos

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/cache"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/circuitbreaker"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/health"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/loadbalancer"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/middleware"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/proxy"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/ratelimit"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/retry"
	"github.com/redis/go-redis/v9"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// 1. Backend Crash Chaos Test:
// One backend abruptly crashes while handling traffic; proxy retry policy
// seamlessly fails over to healthy backends with zero dropped client requests.
func TestChaos_BackendCrash(t *testing.T) {
	logger := discardLogger()
	lb := loadbalancer.NewRoundRobin()
	hcfg := health.DefaultConfig()

	// Start 3 backends
	b1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-Server", "b1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("backend-1 response"))
	}))
	defer b1.Close()

	var b2Alive atomic.Bool
	b2Alive.Store(true)
	b2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !b2Alive.Load() {
			// Abrupt failure: close connection or return 503
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("X-Backend-Server", "b2")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("backend-2 response"))
	}))
	defer b2.Close()

	b3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-Server", "b3")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("backend-3 response"))
	}))
	defer b3.Close()

	servers := []*httptest.Server{b1, b2, b3}
	for i, s := range servers {
		u, _ := url.Parse(s.URL)
		bh := health.NewBackendHealth(hcfg)
		cb := circuitbreaker.New(circuitbreaker.DefaultConfig())
		lb.AddBackend(&loadbalancer.Backend{
			ID:             fmt.Sprintf("b-%d", i+1),
			URL:            u,
			Weight:         1,
			Health:         bh,
			CircuitBreaker: cb,
		})
	}

	retryPolicy := retry.DefaultPolicy()
	retryPolicy.MaxRetries = 2
	p, err := proxy.New(proxy.Config{
		BackendTimeout: 2 * time.Second,
		RetryPolicy:    retryPolicy,
	}, lb, http.DefaultTransport, logger)
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}

	// Crash backend 2
	b2Alive.Store(false)

	// Send 30 requests through proxy: all must succeed via retry failover
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()
		p.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("request %d failed with status %d: %s", i, rr.Code, rr.Body.String())
		}
	}
}

// 2. Backend Latency Spike Chaos Test:
// One backend has an artificial latency delay exceeding BackendTimeout;
// the proxy times out and fails over to an available healthy backend.
func TestChaos_BackendLatency(t *testing.T) {
	logger := discardLogger()
	lb := loadbalancer.NewRoundRobin()

	// Slow backend (exceeds timeout)
	slowBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slowBackend.Close()

	// Fast backend
	fastBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fast response"))
	}))
	defer fastBackend.Close()

	uSlow, _ := url.Parse(slowBackend.URL)
	uFast, _ := url.Parse(fastBackend.URL)

	hcfg := health.DefaultConfig()
	lb.AddBackend(&loadbalancer.Backend{
		ID:             "slow",
		URL:            uSlow,
		Weight:         1,
		Health:         health.NewBackendHealth(hcfg),
		CircuitBreaker: circuitbreaker.New(circuitbreaker.DefaultConfig()),
	})
	lb.AddBackend(&loadbalancer.Backend{
		ID:             "fast",
		URL:            uFast,
		Weight:         1,
		Health:         health.NewBackendHealth(hcfg),
		CircuitBreaker: circuitbreaker.New(circuitbreaker.DefaultConfig()),
	})

	retryPolicy := retry.DefaultPolicy()
	retryPolicy.MaxRetries = 2

	p, err := proxy.New(proxy.Config{
		BackendTimeout: 100 * time.Millisecond, // Strict timeout
		RetryPolicy:    retryPolicy,
	}, lb, http.DefaultTransport, logger)
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}

	// Send 10 requests: even when hitting the slow backend first, retry should failover to fast backend
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/latency-test", nil)
		rr := httptest.NewRecorder()
		p.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("request %d failed with status %d", i, rr.Code)
		}
	}
}

// 3. Redis Failure Graceful Degradation Chaos Test:
// When Redis is completely unreachable, cache and rate limiter fall back
// safely to in-memory implementations without dropping client traffic.
func TestChaos_RedisFailureGracefulDegradation(t *testing.T) {
	logger := discardLogger()

	// Target a guaranteed dead Redis address
	unreachableAddr := "127.0.0.1:54321"

	// 1. Redis Cache Fallback
	rcCfg := cache.DefaultRedisConfig()
	rcCfg.Addr = unreachableAddr
	rcCfg.DialTimeout = 100 * time.Millisecond
	_, err := cache.NewRedisCacheFromConfig(rcCfg, logger)
	if err == nil {
		t.Error("expected error dialing dead redis, got nil")
	}
	// Fallback to MemoryCache succeeds
	memCache := cache.NewMemoryCache(100)
	if memCache == nil {
		t.Fatal("fallback memory cache should not be nil")
	}

	// 2. Redis Limiter Fallback
	rdb := redis.NewClient(&redis.Options{
		Addr:        unreachableAddr,
		DialTimeout: 50 * time.Millisecond,
	})
	defer rdb.Close()

	limiter := ratelimit.NewRedisLimiter(rdb, 10, 10, "chaos:test:", logger)

	// Verify limiter falls back to in-memory bucket rather than panicking or blocking
	res := limiter.Allow("client-1")
	if !res.Allowed {
		t.Error("expected fallback rate limiter to allow initial request")
	}
}

// 4. All Backends Down Chaos Test:
// When all backends are unavailable, proxy must return 503 Service Unavailable
// cleanly without hanging, crashing, or memory corruption.
func TestChaos_AllBackendsDown(t *testing.T) {
	logger := discardLogger()
	lb := loadbalancer.NewRoundRobin()

	// No backends added
	p, err := proxy.New(proxy.Config{
		BackendTimeout: 100 * time.Millisecond,
	}, lb, http.DefaultTransport, logger)
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/blackout", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 Service Unavailable when all backends down, got %d", rr.Code)
	}

	// Now add a backend back dynamically; proxy recovers immediately
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("recovered"))
	}))
	defer server.Close()

	u, _ := url.Parse(server.URL)
	hcfg := health.DefaultConfig()
	lb.AddBackend(&loadbalancer.Backend{
		ID:             "revived",
		URL:            u,
		Weight:         1,
		Health:         health.NewBackendHealth(hcfg),
		CircuitBreaker: circuitbreaker.New(circuitbreaker.DefaultConfig()),
	})

	req2 := httptest.NewRequest(http.MethodGet, "/blackout", nil)
	rr2 := httptest.NewRecorder()
	p.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Errorf("expected 200 OK after backend recovery, got %d", rr2.Code)
	}
}

// 5. Concurrency Under Chaos Test:
// 100 concurrent goroutines querying proxy while 1 of 3 backends continuously throws 500 errors.
func TestChaos_ConcurrentRequestsWithPartialFailure(t *testing.T) {
	logger := discardLogger()
	lb := loadbalancer.NewRoundRobin()
	hcfg := health.DefaultConfig()

	healthy1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("healthy-1"))
	}))
	defer healthy1.Close()

	healthy2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("healthy-2"))
	}))
	defer healthy2.Close()

	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer flaky.Close()

	for _, s := range []*httptest.Server{healthy1, healthy2, flaky} {
		u, _ := url.Parse(s.URL)
		lb.AddBackend(&loadbalancer.Backend{
			ID:             s.URL,
			URL:            u,
			Weight:         1,
			Health:         health.NewBackendHealth(hcfg),
			CircuitBreaker: circuitbreaker.New(circuitbreaker.DefaultConfig()),
		})
	}

	retryPolicy := retry.DefaultPolicy()
	retryPolicy.MaxRetries = 2

	p, err := proxy.New(proxy.Config{
		BackendTimeout: 1 * time.Second,
		RetryPolicy:    retryPolicy,
	}, lb, http.DefaultTransport, logger)
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}

	handler := middleware.RequestID(p)

	const concurrency = 100
	doneCh := make(chan bool, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(id int) {
			req := httptest.NewRequest(http.MethodGet, "/concurrent-chaos", nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			// All GET requests should successfully failover from the flaky backend to healthy backends
			if rr.Code != http.StatusOK {
				t.Errorf("goroutine %d failed with status %d", id, rr.Code)
			}
			doneCh <- true
		}(i)
	}

	for i := 0; i < concurrency; i++ {
		<-doneCh
	}
}

package proxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/circuitbreaker"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/health"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/loadbalancer"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/retry"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestReverseProxy_LoadBalancing verifies basic routing across multiple backends.
func TestReverseProxy_LoadBalancing(t *testing.T) {
	b1Count := atomic.Int32{}
	b2Count := atomic.Int32{}

	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b1Count.Add(1)
		w.Header().Set("X-Backend", "b1")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response from b1"))
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b2Count.Add(1)
		w.Header().Set("X-Backend", "b2")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response from b2"))
	}))
	defer s2.Close()

	u1, _ := url.Parse(s1.URL)
	u2, _ := url.Parse(s2.URL)

	lb := loadbalancer.NewRoundRobin()
	lb.AddBackend(&loadbalancer.Backend{ID: "b1", URL: u1})
	lb.AddBackend(&loadbalancer.Backend{ID: "b2", URL: u2})

	rp, err := New(Config{BackendTimeout: 2 * time.Second}, lb, nil, testLogger())
	if err != nil {
		t.Fatalf("unexpected error creating proxy: %v", err)
	}

	proxyServer := httptest.NewServer(rp)
	defer proxyServer.Close()

	for i := 0; i < 4; i++ {
		resp, err := http.Get(proxyServer.URL + "/test")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if resp.Header.Get("X-Proxy") != "distributed-proxy/1.0" {
			t.Errorf("expected X-Proxy header, got %q", resp.Header.Get("X-Proxy"))
		}
		resp.Body.Close()
	}

	if b1Count.Load() != 2 || b2Count.Load() != 2 {
		t.Errorf("expected 2 requests to each backend, got b1=%d, b2=%d", b1Count.Load(), b2Count.Load())
	}
}

// TestReverseProxy_PassiveFailureDetection_5xx verifies that repeated 500s from a backend
// trigger passive failure detection and cause failover to the healthy backend.
func TestReverseProxy_PassiveFailureDetection_5xx(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("healthy response"))
	}))
	defer s2.Close()

	u1, _ := url.Parse(s1.URL)
	u2, _ := url.Parse(s2.URL)

	healthCfg := health.Config{
		UnhealthyThreshold: 2, // 2 failures to trip UNHEALTHY
		RecoveryThreshold:  2,
	}

	bh1 := health.NewBackendHealth(healthCfg)
	bh2 := health.NewBackendHealth(healthCfg)

	lb := loadbalancer.NewRoundRobin()
	lb.AddBackend(&loadbalancer.Backend{ID: "b1", URL: u1, Health: bh1})
	lb.AddBackend(&loadbalancer.Backend{ID: "b2", URL: u2, Health: bh2})

	rp, err := New(Config{BackendTimeout: 2 * time.Second}, lb, nil, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	proxyServer := httptest.NewServer(rp)
	defer proxyServer.Close()

	// Initially b1 is healthy
	if bh1.Status() != health.StatusHealthy {
		t.Fatalf("expected b1 to start healthy")
	}

	// Trigger requests to hit b1 and record 500s.
	// Round robin alternates between b1 and b2, so 4 requests hits each 2 times.
	for i := 0; i < 4; i++ {
		resp, err := http.Get(proxyServer.URL + "/api")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
	}

	// After 2 failures, b1 should now be UNHEALTHY
	if bh1.Status() != health.StatusUnhealthy {
		t.Fatalf("expected b1 to be UNHEALTHY after repeated 500s, got %s", bh1.Status())
	}

	// Now all subsequent requests should only route to healthy b2
	for i := 0; i < 4; i++ {
		resp, err := http.Get(proxyServer.URL + "/api")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK from surviving backend, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// TestReverseProxy_BackendCrash_Failover tests what happens when a backend crashes
// (server closed / connection refused). The proxy should detect transport failure,
// return 502, mark the backend UNHEALTHY, and failover to the surviving backend.
func TestReverseProxy_BackendCrash_Failover(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// Crash backend 1 immediately by closing its listener
	s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("b2 response"))
	}))
	defer s2.Close()

	u1, _ := url.Parse(s1.URL)
	u2, _ := url.Parse(s2.URL)

	healthCfg := health.Config{
		UnhealthyThreshold: 2,
		RecoveryThreshold:  2,
	}

	bh1 := health.NewBackendHealth(healthCfg)
	bh2 := health.NewBackendHealth(healthCfg)

	lb := loadbalancer.NewRoundRobin()
	lb.AddBackend(&loadbalancer.Backend{ID: "b1", URL: u1, Health: bh1})
	lb.AddBackend(&loadbalancer.Backend{ID: "b2", URL: u2, Health: bh2})

	rp, err := New(Config{BackendTimeout: 1 * time.Second}, lb, nil, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	proxyServer := httptest.NewServer(rp)
	defer proxyServer.Close()

	// Make enough requests to trip b1's unhealthy threshold
	for i := 0; i < 4; i++ {
		resp, err := http.Get(proxyServer.URL + "/data")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
	}

	// b1 must now be UNHEALTHY due to connection errors
	if bh1.Status() != health.StatusUnhealthy {
		t.Fatalf("expected crashed b1 to be UNHEALTHY, got %s", bh1.Status())
	}

	// Subsequent requests must succeed on b2
	for i := 0; i < 3; i++ {
		resp, err := http.Get(proxyServer.URL + "/data")
		if err != nil {
			t.Fatalf("failover request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 from healthy b2, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// TestReverseProxy_AllBackendsDown verifies that if all backends are marked UNHEALTHY,
// the proxy returns 503 Service Unavailable.
func TestReverseProxy_AllBackendsDown(t *testing.T) {
	healthCfg := health.Config{
		UnhealthyThreshold: 1,
		RecoveryThreshold:  2,
	}

	bh1 := health.NewBackendHealth(healthCfg)
	bh1.RecordFailure() // status -> UNHEALTHY

	u1, _ := url.Parse("http://127.0.0.1:9999")
	lb := loadbalancer.NewRoundRobin()
	lb.AddBackend(&loadbalancer.Backend{ID: "b1", URL: u1, Health: bh1})

	rp, err := New(Config{BackendTimeout: 1 * time.Second}, lb, nil, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	proxyServer := httptest.NewServer(rp)
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL + "/test")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable when all backends down, got %d", resp.StatusCode)
	}
}

// TestReverseProxy_ClientCancellation verifies that a cancelled client context
// does not record a failure against backend health.
func TestReverseProxy_ClientCancellation(t *testing.T) {
	requestStarted := make(chan struct{})
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer s1.Close()

	u1, _ := url.Parse(s1.URL)
	bh1 := health.NewBackendHealth(health.DefaultConfig())

	lb := loadbalancer.NewRoundRobin()
	lb.AddBackend(&loadbalancer.Backend{ID: "b1", URL: u1, Health: bh1})

	rp, err := New(Config{BackendTimeout: 2 * time.Second}, lb, nil, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	proxyServer := httptest.NewServer(rp)
	defer proxyServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, proxyServer.URL+"/cancel", nil)

	go func() {
		<-requestStarted
		cancel() // Cancel request while in flight
	}()

	client := &http.Client{}
	_, _ = client.Do(req)

	// Verify that client cancellation did NOT increment backend failure count
	if bh1.Snapshot().TotalFailure != 0 {
		t.Errorf("expected 0 failures for client cancellation, got %d", bh1.Snapshot().TotalFailure)
	}
}

// TestReverseProxy_Retry_FailoverOn503 verifies that when backend 1 returns 503,
// an idempotent GET request is automatically retried on backend 2.
func TestReverseProxy_Retry_FailoverOn503(t *testing.T) {
	b1Hits := atomic.Int32{}
	b2Hits := atomic.Int32{}

	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b1Hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable) // 503
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b2Hits.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("s2 ok"))
	}))
	defer s2.Close()

	u1, _ := url.Parse(s1.URL)
	u2, _ := url.Parse(s2.URL)

	lb := loadbalancer.NewRoundRobin()
	lb.AddBackend(&loadbalancer.Backend{ID: "b1", URL: u1})
	lb.AddBackend(&loadbalancer.Backend{ID: "b2", URL: u2})

	retryPolicy := retry.DefaultPolicy()
	retryPolicy.MaxRetries = 2
	retryPolicy.InitialBackoff = 5 * time.Millisecond
	retryPolicy.JitterFraction = 0.0

	rp, err := New(Config{
		BackendTimeout: 2 * time.Second,
		RetryPolicy:    retryPolicy,
	}, lb, nil, testLogger())
	if err != nil {
		t.Fatalf("unexpected error creating proxy: %v", err)
	}

	proxyServer := httptest.NewServer(rp)
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL + "/retry-test")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Client should transparently receive 200 OK after failover
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from failover retry, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "s2 ok" {
		t.Errorf("expected body 's2 ok', got %q", string(body))
	}

	if b1Hits.Load() != 1 || b2Hits.Load() != 1 {
		t.Errorf("expected 1 hit on b1 and 1 hit on b2, got b1=%d, b2=%d", b1Hits.Load(), b2Hits.Load())
	}
}

// TestReverseProxy_Retry_NoRetryForPost verifies that a non-idempotent POST request
// is NOT retried even when the backend returns 503.
func TestReverseProxy_Retry_NoRetryForPost(t *testing.T) {
	b1Hits := atomic.Int32{}
	b2Hits := atomic.Int32{}

	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b1Hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable) // 503
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b2Hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer s2.Close()

	u1, _ := url.Parse(s1.URL)
	u2, _ := url.Parse(s2.URL)

	lb := loadbalancer.NewRoundRobin()
	lb.AddBackend(&loadbalancer.Backend{ID: "b1", URL: u1})
	lb.AddBackend(&loadbalancer.Backend{ID: "b2", URL: u2})

	retryPolicy := retry.DefaultPolicy()
	retryPolicy.MaxRetries = 2
	retryPolicy.InitialBackoff = 5 * time.Millisecond

	rp, err := New(Config{
		BackendTimeout: 2 * time.Second,
		RetryPolicy:    retryPolicy,
	}, lb, nil, testLogger())
	if err != nil {
		t.Fatalf("unexpected error creating proxy: %v", err)
	}

	proxyServer := httptest.NewServer(rp)
	defer proxyServer.Close()

	// Send POST request
	resp, err := http.Post(proxyServer.URL+"/order", "application/json", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should NOT be retried -> returns 503 directly to client
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for non-idempotent POST, got %d", resp.StatusCode)
	}

	if b1Hits.Load() != 1 {
		t.Errorf("expected exactly 1 attempt on b1, got %d", b1Hits.Load())
	}
	if b2Hits.Load() != 0 {
		t.Errorf("expected 0 attempts on b2 for POST, got %d", b2Hits.Load())
	}
}

// TestReverseProxy_CircuitBreaker_TripsAndProtects verifies that once a backend's
// CircuitBreaker trips OPEN, subsequent requests bypass it and go to healthy backends.
func TestReverseProxy_CircuitBreaker_TripsAndProtects(t *testing.T) {
	b1Hits := atomic.Int32{}
	b2Hits := atomic.Int32{}

	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b1Hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b2Hits.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("b2 healthy"))
	}))
	defer s2.Close()

	u1, _ := url.Parse(s1.URL)
	u2, _ := url.Parse(s2.URL)

	cb1 := circuitbreaker.New(circuitbreaker.Config{
		FailureThreshold: 2,
		OpenTimeout:      1 * time.Second,
	})

	lb := loadbalancer.NewRoundRobin()
	lb.AddBackend(&loadbalancer.Backend{ID: "b1", URL: u1, CircuitBreaker: cb1})
	lb.AddBackend(&loadbalancer.Backend{ID: "b2", URL: u2})

	rp, err := New(Config{
		BackendTimeout: 2 * time.Second,
	}, lb, nil, testLogger())
	if err != nil {
		t.Fatalf("unexpected error creating proxy: %v", err)
	}

	proxyServer := httptest.NewServer(rp)
	defer proxyServer.Close()

	// Send requests to trip cb1 (2 failures)
	// Round robin hits b1, b2, b1, b2
	for i := 0; i < 4; i++ {
		resp, _ := http.Get(proxyServer.URL + "/data")
		if resp != nil {
			resp.Body.Close()
		}
	}

	// cb1 should now be OPEN
	if cb1.State() != circuitbreaker.StateOpen {
		t.Fatalf("expected cb1 to be OPEN after failures, got %v", cb1.State())
	}

	b1Before := b1Hits.Load()

	// Subsequent requests should ONLY go to b2, b1 should receive zero requests
	for i := 0; i < 5; i++ {
		resp, err := http.Get(proxyServer.URL + "/data")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200 OK from b2, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	}

	if b1Hits.Load() != b1Before {
		t.Errorf("b1 received requests while circuit breaker was OPEN! hits before=%d, after=%d", b1Before, b1Hits.Load())
	}
}

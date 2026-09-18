package load

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/circuitbreaker"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/connectionpool"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/health"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/loadbalancer"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/proxy"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/retry"
)

func setupTestProxy(b *testing.B, backendCount int) (*proxy.ReverseProxy, []*httptest.Server) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	lb := loadbalancer.NewRoundRobin()
	hcfg := health.DefaultConfig()

	var servers []*httptest.Server
	for i := 0; i < backendCount; i++ {
		idx := i + 1
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{"status":"ok","backend":%d}`+"\n", idx)
		}))
		servers = append(servers, s)

		u, _ := url.Parse(s.URL)
		bh := health.NewBackendHealth(hcfg)
		cb := circuitbreaker.New(circuitbreaker.DefaultConfig())
		lb.AddBackend(&loadbalancer.Backend{
			ID:             fmt.Sprintf("backend-%d", idx),
			URL:            u,
			Weight:         1,
			Health:         bh,
			CircuitBreaker: cb,
		})
	}

	pcfg := connectionpool.Default()
	pcfg.MaxIdleConns = 1000
	pcfg.MaxIdleConnsPerHost = 500
	poolTransport := connectionpool.New(pcfg)
	p, err := proxy.New(proxy.Config{
		BackendTimeout: 5 * time.Second,
		RetryPolicy:    retry.DefaultPolicy(),
	}, lb, poolTransport, logger)
	if err != nil {
		b.Fatalf("failed to create proxy: %v", err)
	}

	return p, servers
}

func BenchmarkProxy_Serial(b *testing.B) {
	p, servers := setupTestProxy(b, 3)
	defer func() {
		for _, s := range servers {
			s.Close()
		}
	}()

	req := httptest.NewRequest(http.MethodGet, "/bench", nil)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		p.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			b.Fatalf("unexpected code: %d", rr.Code)
		}
	}
}

func BenchmarkProxy_Parallel(b *testing.B) {
	p, servers := setupTestProxy(b, 3)
	defer func() {
		for _, s := range servers {
			s.Close()
		}
	}()

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, "/bench", nil)
		for pb.Next() {
			rr := httptest.NewRecorder()
			p.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				b.Fatalf("unexpected code: %d", rr.Code)
			}
		}
	})
}

func BenchmarkProxy_HighConcurrency(b *testing.B) {
	p, servers := setupTestProxy(b, 3)
	defer func() {
		for _, s := range servers {
			s.Close()
		}
	}()

	b.SetParallelism(4)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, "/bench", nil)
		for pb.Next() {
			rr := httptest.NewRecorder()
			p.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				b.Fatalf("unexpected code: %d", rr.Code)
			}
		}
	})
}

func TestLoad_BurstConcurrency(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	lb := loadbalancer.NewRoundRobin()
	hcfg := health.DefaultConfig()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer s.Close()

	u, _ := url.Parse(s.URL)
	lb.AddBackend(&loadbalancer.Backend{
		ID:             "b1",
		URL:            u,
		Weight:         1,
		Health:         health.NewBackendHealth(hcfg),
		CircuitBreaker: circuitbreaker.New(circuitbreaker.DefaultConfig()),
	})

	pcfg := connectionpool.Default()
	pcfg.MaxIdleConns = 1000
	pcfg.MaxIdleConnsPerHost = 500
	poolTransport := connectionpool.New(pcfg)
	p, err := proxy.New(proxy.Config{
		BackendTimeout: 5 * time.Second,
		RetryPolicy:    retry.DefaultPolicy(),
	}, lb, poolTransport, logger)
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}

	// 100 concurrent goroutines burst
	const concurrency = 100
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/burst", nil)
			rr := httptest.NewRecorder()
			p.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", rr.Code)
			}
		}()
	}

	wg.Wait()
}

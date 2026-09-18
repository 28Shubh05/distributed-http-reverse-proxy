package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/cache"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/loadbalancer"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/storage"
)

func TestAdminAPI_Authentication(t *testing.T) {
	secretToken := "my-secret-admin-token"
	server := NewServer(Config{
		Token: secretToken,
		Store: storage.NewMemoryStore(),
	})

	handler := server.Handler()

	// 1. Missing auth
	req1 := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized without token, got %d", rr1.Code)
	}

	// 2. Bad token
	req2 := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	req2.Header.Set("Authorization", "Bearer wrong-token")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong token, got %d", rr2.Code)
	}

	// 3. Valid Bearer token
	req3 := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	req3.Header.Set("Authorization", "Bearer "+secretToken)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Errorf("expected 200 with valid bearer token, got %d", rr3.Code)
	}

	// 4. Valid X-Admin-Token
	req4 := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	req4.Header.Set("X-Admin-Token", secretToken)
	rr4 := httptest.NewRecorder()
	handler.ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusOK {
		t.Errorf("expected 200 with X-Admin-Token header, got %d", rr4.Code)
	}
}

func TestAdminAPI_Backends(t *testing.T) {
	store := storage.NewMemoryStore()
	lb := loadbalancer.NewRoundRobin()
	server := NewServer(Config{
		Store:        store,
		LoadBalancer: lb,
	})
	handler := server.Handler()

	// 1. POST /admin/backends
	payload := []byte(`{"id":"b-99","url":"http://127.0.0.1:9099","weight":3}`)
	req1 := httptest.NewRequest(http.MethodPost, "/admin/backends", bytes.NewReader(payload))
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on POST /admin/backends, got %d (body: %s)", rr1.Code, rr1.Body.String())
	}

	// Check that backend is present in load balancer
	b := lb.SelectBackend()
	if b == nil || b.ID != "b-99" {
		t.Fatalf("expected b-99 to be added to load balancer, got %v", b)
	}

	// 2. GET /admin/backends
	req2 := httptest.NewRequest(http.MethodGet, "/admin/backends", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on GET /admin/backends, got %d", rr2.Code)
	}
	var getResp struct {
		Backends []storage.BackendRecord `json:"backends"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("failed to decode GET backends response: %v", err)
	}
	if len(getResp.Backends) != 1 || getResp.Backends[0].ID != "b-99" {
		t.Errorf("unexpected backends in list: %+v", getResp.Backends)
	}

	// 3. DELETE /admin/backends/b-99
	req3 := httptest.NewRequest(http.MethodDelete, "/admin/backends/b-99", nil)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)

	if rr3.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on DELETE /admin/backends/b-99, got %d", rr3.Code)
	}

	// Verify backend removed from load balancer
	if remaining := lb.SelectBackend(); remaining != nil {
		t.Errorf("expected load balancer to be empty after delete, got %v", remaining)
	}
}

func TestAdminAPI_ConfigAndReload(t *testing.T) {
	store := storage.NewMemoryStore()
	reloaded := atomic.Bool{}
	server := NewServer(Config{
		Store: store,
		ReloadFunc: func(ctx context.Context) error {
			reloaded.Store(true)
			return nil
		},
	})
	handler := server.Handler()

	// 1. POST /admin/config
	setPayload := []byte(`{"key":"cache_ttl","value":"300s"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/admin/config", bytes.NewReader(setPayload))
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("expected 200 on set config, got %d", rr1.Code)
	}

	// 2. GET /admin/config
	req2 := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 on get config, got %d", rr2.Code)
	}
	var cfgResp struct {
		Configs map[string]string `json:"configs"`
	}
	_ = json.Unmarshal(rr2.Body.Bytes(), &cfgResp)
	if cfgResp.Configs["cache_ttl"] != "300s" {
		t.Errorf("expected cache_ttl=300s, got %v", cfgResp.Configs)
	}

	// 3. POST /admin/reload
	req3 := httptest.NewRequest(http.MethodPost, "/admin/reload", nil)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("expected 200 on reload, got %d", rr3.Code)
	}
	if !reloaded.Load() {
		t.Errorf("expected reload callback to be executed")
	}
}

func TestAdminAPI_StatsAndEvents(t *testing.T) {
	store := storage.NewMemoryStore()
	memCache := cache.NewMemoryCache(100)
	_ = memCache.Set("k1", &cache.CachedResponse{StatusCode: 200}, 0)
	_, _ = memCache.Get("k1") // hit

	_ = store.RecordHealthEvent(context.Background(), storage.HealthEvent{
		BackendID: "b-1",
		Status:    "HEALTHY",
	})

	server := NewServer(Config{
		Store: store,
		Cache: memCache,
	})
	handler := server.Handler()

	// GET /admin/stats
	req1 := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Errorf("expected 200 on stats, got %d", rr1.Code)
	}

	// GET /admin/events
	req2 := httptest.NewRequest(http.MethodGet, "/admin/events?limit=10", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("expected 200 on events, got %d", rr2.Code)
	}
}

func TestAdminAPI_MetricsEndpoint(t *testing.T) {
	server := NewServer(Config{
		Token: "secret-token-123",
		Store: storage.NewMemoryStore(),
	})
	handler := server.Handler()

	// Scrape /metrics without token should succeed (200 OK)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /metrics without token, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !bytes.Contains([]byte(body), []byte("go_info")) && !bytes.Contains([]byte(body), []byte("proxy_requests_total")) {
		t.Errorf("expected prometheus metrics in response, got:\n%s", body)
	}
}

func TestAdminAPI_PprofEndpoints(t *testing.T) {
	server := NewServer(Config{
		Token: "secret-token-123",
		Store: storage.NewMemoryStore(),
	})
	handler := server.Handler()

	// Access /debug/pprof/ without token should succeed (200 OK)
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /debug/pprof/, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !bytes.Contains([]byte(body), []byte("Types of profiles available")) {
		t.Errorf("expected pprof index page, got:\n%s", body)
	}
}

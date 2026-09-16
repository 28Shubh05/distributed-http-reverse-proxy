package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestMetrics_RegistrationAndCollection(t *testing.T) {
	m, reg := NewTestMetrics()

	// 1. Record proxy requests
	m.RecordProxyRequest("GET", "200", 25*time.Millisecond)
	m.RecordProxyRequest("POST", "500", 120*time.Millisecond)
	m.RecordProxyError("timeout")

	// 2. Record backend requests & failures
	m.RecordBackendRequest("backend-1", "200")
	m.RecordBackendFailure("backend-1", "dial_timeout")
	m.SetBackendHealth("backend-1", true)
	m.SetBackendHealth("backend-2", false)

	// 3. Record load balancer selections
	m.RecordLoadBalancerSelection("backend-1", "round_robin")

	// 4. Record cache events
	m.RecordCacheHit()
	m.RecordCacheMiss()

	// 5. Rate limit and circuit breaker
	m.RecordRateLimitRejection("192.168.1.1")
	m.SetCircuitBreakerState("backend-1", 0)
	m.RecordCircuitBreakerOpen("backend-2")

	// 6. Connection metrics
	m.ActiveConnections.Set(12)
	m.ConnectionPoolActive.Set(4)
	m.ConnectionPoolIdle.Set(16)

	// Gather metrics from test registry
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	found := make(map[string]bool)
	for _, fam := range families {
		found[fam.GetName()] = true
	}

	expectedMetrics := []string{
		"proxy_requests_total",
		"proxy_request_duration_seconds",
		"proxy_request_errors_total",
		"backend_requests_total",
		"backend_failures_total",
		"backend_health_status",
		"load_balancer_requests_total",
		"cache_hits_total",
		"cache_misses_total",
		"rate_limit_rejections_total",
		"circuit_breaker_state",
		"circuit_breaker_open_total",
		"active_connections",
		"connection_pool_active",
		"connection_pool_idle",
	}

	for _, name := range expectedMetrics {
		if !found[name] {
			t.Errorf("expected metric %s to be registered and gathered, but was not found", name)
		}
	}
}

func TestMetrics_NilSafety(t *testing.T) {
	var m *Metrics
	// Ensure no panic when m is nil
	m.RecordProxyRequest("GET", "200", time.Second)
	m.RecordProxyError("err")
	m.RecordBackendRequest("b1", "200")
	m.RecordBackendFailure("b1", "err")
	m.SetBackendHealth("b1", true)
	m.RecordLoadBalancerSelection("b1", "rr")
	m.RecordCacheHit()
	m.RecordCacheMiss()
	m.RecordRateLimitRejection("ip")
	m.SetCircuitBreakerState("b1", 1)
	m.RecordCircuitBreakerOpen("b1")
}

func getCounterValue(counter prometheus.Counter) float64 {
	var m dto.Metric
	if err := counter.Write(&m); err != nil {
		return 0
	}
	return m.GetCounter().GetValue()
}

func TestMetrics_Counters(t *testing.T) {
	m, _ := NewTestMetrics()

	m.RecordCacheHit()
	m.RecordCacheHit()
	m.RecordCacheMiss()

	if val := getCounterValue(m.CacheHitsTotal); val != 2 {
		t.Errorf("expected cache hits 2, got %v", val)
	}
	if val := getCounterValue(m.CacheMissesTotal); val != 1 {
		t.Errorf("expected cache misses 1, got %v", val)
	}
}

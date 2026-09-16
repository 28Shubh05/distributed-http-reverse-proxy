// Package metrics provides Prometheus instrumentation for the reverse proxy,
// tracking throughput, latency distributions, backend health, errors, connection pools, and cache rates.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics holds the registered Prometheus collectors for the reverse proxy.
type Metrics struct {
	// Request counters and histograms
	ProxyRequestsTotal         *prometheus.CounterVec
	ProxyRequestDurationSeconds *prometheus.HistogramVec
	ProxyRequestErrorsTotal     *prometheus.CounterVec

	// Upstream backend metrics
	BackendRequestsTotal *prometheus.CounterVec
	BackendFailuresTotal *prometheus.CounterVec
	BackendHealthStatus  *prometheus.GaugeVec

	// Load balancer metrics
	LoadBalancerRequestsTotal *prometheus.CounterVec

	// Cache metrics
	CacheHitsTotal   prometheus.Counter
	CacheMissesTotal prometheus.Counter

	// Rate limiter metrics
	RateLimitRejectionsTotal *prometheus.CounterVec

	// Circuit breaker metrics
	CircuitBreakerState     *prometheus.GaugeVec
	CircuitBreakerOpenTotal *prometheus.CounterVec

	// Connection metrics
	ActiveConnections    prometheus.Gauge
	ConnectionPoolActive prometheus.Gauge
	ConnectionPoolIdle   prometheus.Gauge

	reg prometheus.Registerer
}

// NewMetrics initializes and registers all Prometheus collectors.
// If reg is nil, prometheus.DefaultRegisterer is used.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	m := &Metrics{
		reg: reg,
		ProxyRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "proxy_requests_total",
				Help: "Total number of HTTP requests processed by the reverse proxy.",
			},
			[]string{"method", "code"},
		),
		ProxyRequestDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "proxy_request_duration_seconds",
				Help:    "HTTP request latency distributions in seconds.",
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			},
			[]string{"method", "code"},
		),
		ProxyRequestErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "proxy_request_errors_total",
				Help: "Total number of HTTP request errors encountered by the proxy.",
			},
			[]string{"type"},
		),
		BackendRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "backend_requests_total",
				Help: "Total number of HTTP requests forwarded to upstream backends.",
			},
			[]string{"backend", "code"},
		),
		BackendFailuresTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "backend_failures_total",
				Help: "Total number of failed attempts to connect or receive response from backends.",
			},
			[]string{"backend", "type"},
		),
		BackendHealthStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "backend_health_status",
				Help: "Current health status of backends (1 = HEALTHY/RECOVERING, 0 = UNHEALTHY).",
			},
			[]string{"backend"},
		),
		LoadBalancerRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "load_balancer_requests_total",
				Help: "Total number of routing selections made by load balancers.",
			},
			[]string{"backend", "algorithm"},
		),
		CacheHitsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "cache_hits_total",
				Help: "Total number of cache hits in the reverse proxy.",
			},
		),
		CacheMissesTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "cache_misses_total",
				Help: "Total number of cache misses in the reverse proxy.",
			},
		),
		RateLimitRejectionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "rate_limit_rejections_total",
				Help: "Total number of requests rejected by the rate limiter (HTTP 429).",
			},
			[]string{"client_ip"},
		),
		CircuitBreakerState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "circuit_breaker_state",
				Help: "Current state of circuit breakers (0 = Closed, 1 = HalfOpen, 2 = Open).",
			},
			[]string{"backend"},
		),
		CircuitBreakerOpenTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "circuit_breaker_open_total",
				Help: "Total count of circuit breaker state transitions to Open.",
			},
			[]string{"backend"},
		),
		ActiveConnections: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "active_connections",
				Help: "Current count of active in-flight requests handled by the proxy.",
			},
		),
		ConnectionPoolActive: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "connection_pool_active",
				Help: "Number of active connections currently leased from the connection pool.",
			},
		),
		ConnectionPoolIdle: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "connection_pool_idle",
				Help: "Number of idle keep-alive connections residing in the connection pool.",
			},
		),
	}

	reg.MustRegister(
		m.ProxyRequestsTotal,
		m.ProxyRequestDurationSeconds,
		m.ProxyRequestErrorsTotal,
		m.BackendRequestsTotal,
		m.BackendFailuresTotal,
		m.BackendHealthStatus,
		m.LoadBalancerRequestsTotal,
		m.CacheHitsTotal,
		m.CacheMissesTotal,
		m.RateLimitRejectionsTotal,
		m.CircuitBreakerState,
		m.CircuitBreakerOpenTotal,
		m.ActiveConnections,
		m.ConnectionPoolActive,
		m.ConnectionPoolIdle,
	)

	return m
}

// NewTestMetrics creates an isolated Metrics instance with an isolated registry for unit testing.
func NewTestMetrics() (*Metrics, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	return NewMetrics(reg), reg
}

// Default holds the default package-level metrics instance.
var Default = NewMetrics(prometheus.DefaultRegisterer)

// RecordProxyRequest tracks overall request volume and latency.
func (m *Metrics) RecordProxyRequest(method, code string, duration time.Duration) {
	if m == nil {
		return
	}
	m.ProxyRequestsTotal.WithLabelValues(method, code).Inc()
	m.ProxyRequestDurationSeconds.WithLabelValues(method, code).Observe(duration.Seconds())
}

// RecordProxyError tracks error categories (e.g. "no_backends", "timeout", "client_canceled").
func (m *Metrics) RecordProxyError(errType string) {
	if m == nil {
		return
	}
	m.ProxyRequestErrorsTotal.WithLabelValues(errType).Inc()
}

// RecordBackendRequest tracks requests forwarded to a specific upstream backend.
func (m *Metrics) RecordBackendRequest(backend, code string) {
	if m == nil {
		return
	}
	m.BackendRequestsTotal.WithLabelValues(backend, code).Inc()
}

// RecordBackendFailure tracks backend network failures or 5xx responses.
func (m *Metrics) RecordBackendFailure(backend, failureType string) {
	if m == nil {
		return
	}
	m.BackendFailuresTotal.WithLabelValues(backend, failureType).Inc()
}

// SetBackendHealth sets the health gauge for a backend (1 = healthy, 0 = unhealthy).
func (m *Metrics) SetBackendHealth(backend string, healthy bool) {
	if m == nil {
		return
	}
	val := 0.0
	if healthy {
		val = 1.0
	}
	m.BackendHealthStatus.WithLabelValues(backend).Set(val)
}

// RecordLoadBalancerSelection tracks which backend was chosen by what algorithm.
func (m *Metrics) RecordLoadBalancerSelection(backend, algorithm string) {
	if m == nil {
		return
	}
	m.LoadBalancerRequestsTotal.WithLabelValues(backend, algorithm).Inc()
}

// RecordCacheHit increments the cache hit counter.
func (m *Metrics) RecordCacheHit() {
	if m == nil {
		return
	}
	m.CacheHitsTotal.Inc()
}

// RecordCacheMiss increments the cache miss counter.
func (m *Metrics) RecordCacheMiss() {
	if m == nil {
		return
	}
	m.CacheMissesTotal.Inc()
}

// RecordRateLimitRejection tracks rate limit 429 events.
func (m *Metrics) RecordRateLimitRejection(clientIP string) {
	if m == nil {
		return
	}
	m.RateLimitRejectionsTotal.WithLabelValues(clientIP).Inc()
}

// SetCircuitBreakerState updates the state gauge (0 = closed, 1 = half-open, 2 = open).
func (m *Metrics) SetCircuitBreakerState(backend string, state int) {
	if m == nil {
		return
	}
	m.CircuitBreakerState.WithLabelValues(backend).Set(float64(state))
}

// RecordCircuitBreakerOpen tracks transitions to Open state.
func (m *Metrics) RecordCircuitBreakerOpen(backend string) {
	if m == nil {
		return
	}
	m.CircuitBreakerOpenTotal.WithLabelValues(backend).Inc()
}

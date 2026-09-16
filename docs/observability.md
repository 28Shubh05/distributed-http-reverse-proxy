# Observability: Prometheus Metrics + Grafana Dashboard

This document describes the observability stack built into the distributed HTTP reverse proxy — covering Prometheus metrics instrumentation, the `/metrics` endpoint, structured JSON logging, and the Grafana dashboard.

---

## 1. Architecture Overview

```
                    ┌──────────────────────────────────────┐
                    │   Distributed HTTP Reverse Proxy     │
                    │                                      │
                    │  Data Plane (:8080)                  │
                    │  ├─ MetricsMiddleware (active conn)  │
                    │  ├─ RateLimitMiddleware (429 count)  │
                    │  ├─ CacheMiddleware (hit/miss)       │
                    │  └─ retryTransport (backend req/err) │
                    │                                      │
                    │  Control Plane (:8081)               │
                    │  └─ GET /metrics ──────────────────────────► Prometheus
                    └──────────────────────────────────────┘         │
                                                                     │ scrape 5s
                                                              ┌──────▼──────┐
                                                              │ Prometheus  │
                                                              │   :9090     │
                                                              └──────┬──────┘
                                                                     │ datasource
                                                              ┌──────▼──────┐
                                                              │   Grafana   │
                                                              │   :3000     │
                                                              └─────────────┘
```

---

## 2. Prometheus Metrics (`internal/metrics`)

All counters, histograms, and gauges are registered in `metrics.go` with an isolated `prometheus.Registerer` for testability.

### Request Throughput & Latency

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `proxy_requests_total` | Counter | `method`, `code` | Total requests handled by the proxy |
| `proxy_request_duration_seconds` | Histogram | `method`, `code` | Request latency with 12 buckets (1ms → 10s) |
| `proxy_request_errors_total` | Counter | `type` | Errors by category (`server_error`, `client_error`, `no_healthy_backends`, `client_canceled`, `timeout`) |

### Backend Fleet

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `backend_requests_total` | Counter | `backend`, `code` | Requests forwarded to each upstream backend |
| `backend_failures_total` | Counter | `backend`, `type` | Backend failures by type (`transport_error`, `status_error`, `server_error`) |
| `backend_health_status` | Gauge | `backend` | Health state: `1` = UP (Healthy/Recovering), `0` = DOWN (Unhealthy) |
| `load_balancer_requests_total` | Counter | `backend`, `algorithm` | Load balancer routing selections |

### Cache

| Metric | Type | Description |
|--------|------|-------------|
| `cache_hits_total` | Counter | Total cache hits (served from LRU/Redis without backend hit) |
| `cache_misses_total` | Counter | Total cache misses (forwarded to backend) |

### Rate Limiting

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `rate_limit_rejections_total` | Counter | `client_ip` | Requests rejected with HTTP 429 |

### Circuit Breaker

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `circuit_breaker_state` | Gauge | `backend` | CB state: `0`=Closed, `1`=HalfOpen, `2`=Open |
| `circuit_breaker_open_total` | Counter | `backend` | Circuit breaker trips to Open state |

### Connections

| Metric | Type | Description |
|--------|------|-------------|
| `active_connections` | Gauge | Current in-flight request count tracked by the Metrics middleware |
| `connection_pool_active` | Gauge | Active leased connections in the HTTP transport pool |
| `connection_pool_idle` | Gauge | Idle keep-alive connections in the transport pool |

---

## 3. Metrics Endpoint

The Prometheus scrape endpoint is served by the **Admin API** server on `:8081`:

```
GET http://localhost:8081/metrics
```

> [!NOTE]
> `/metrics` is intentionally **exempt from bearer token authentication** to allow Prometheus to scrape it without credentials. Ensure `:8081` is only reachable from your internal network or monitoring subnet.

### Sample Output

```
# HELP proxy_requests_total Total number of HTTP requests processed by the reverse proxy.
# TYPE proxy_requests_total counter
proxy_requests_total{code="200",method="GET"} 14200
proxy_requests_total{code="429",method="GET"} 84
proxy_requests_total{code="503",method="GET"} 12

# HELP proxy_request_duration_seconds HTTP request latency distributions in seconds.
# TYPE proxy_request_duration_seconds histogram
proxy_request_duration_seconds_bucket{code="200",method="GET",le="0.01"} 8100
...

# HELP backend_health_status Current health status of backends (1 = HEALTHY/RECOVERING, 0 = UNHEALTHY).
# TYPE backend_health_status gauge
backend_health_status{backend="backend-1"} 1
backend_health_status{backend="backend-2"} 0
```

---

## 4. Structured JSON Logging

Every HTTP request through the proxy emits a structured `slog` JSON log line with the following fields:

| Field | Example | Description |
|-------|---------|-------------|
| `timestamp` | `2026-09-16T15:20:00.000000Z` | RFC3339Nano UTC timestamp |
| `request_id` | `4a6b8c2d...` | X-Request-ID propagated to backend |
| `method` | `GET` | HTTP method |
| `path` | `/api/users` | Request path |
| `backend` | `http://localhost:9001` | Upstream backend that served the response |
| `status` | `200` | HTTP response status code |
| `latency` | `0.0251` | Latency in seconds (float) |
| `latency_ms` | `25` | Latency in milliseconds (integer) |
| `retry_count` | `1` | Number of backend retries performed |
| `cache_hit` | `true` | Whether response was served from cache |
| `error` | `""` | Error message for 4xx/5xx responses |
| `remote_addr` | `192.168.1.50:55400` | Client remote address |
| `user_agent` | `curl/8.0.1` | Client User-Agent |

### Example JSON Log Line

```json
{
  "time": "2026-09-16T15:20:01.123456789Z",
  "level": "INFO",
  "msg": "request handled",
  "timestamp": "2026-09-16T15:20:01.123456789Z",
  "request_id": "4a6b8c2d7ef893aa",
  "method": "GET",
  "path": "/api/products",
  "backend": "http://localhost:9002",
  "status": 200,
  "latency": 0.0251,
  "latency_ms": 25,
  "retry_count": "0",
  "cache_hit": false,
  "error": "",
  "remote_addr": "192.168.1.50:55400",
  "user_agent": "curl/8.0.1"
}
```

---

## 5. Grafana Dashboard

The pre-built dashboard is auto-provisioned at `deployments/grafana/dashboards/proxy-overview.json`.

### Panels

| Panel | Query | Description |
|-------|-------|-------------|
| Request Rate (RPS) | `sum(rate(proxy_requests_total[1m])) by (code)` | RPS breakdown by HTTP status |
| Latency P50/P95/P99 | `histogram_quantile(0.99, ...)` | Tail latency distributions |
| Backend Health | `backend_health_status` | UP/DOWN per backend (stat panel) |
| Backend Forwarding Rate | `sum(rate(backend_requests_total[1m])) by (backend)` | Traffic to each upstream |
| Backend Failures | `sum(rate(backend_failures_total[1m])) by (backend, type)` | Failure type breakdown |
| Cache Hit Ratio | `hits / (hits + misses)` | Gauge 0→100% |
| Cache Hits vs Misses | `rate(cache_hits_total[1m])` | Timeseries |
| Rate Limit Rejections | `sum(rate(rate_limit_rejections_total[1m])) by (client_ip)` | 429s per client |
| Active & Pool Connections | `active_connections`, `connection_pool_active/idle` | Concurrency profile |

---

## 6. Prometheus Configuration

Prometheus is pre-configured in `deployments/prometheus/prometheus.yml`:

```yaml
global:
  scrape_interval: 5s

scrape_configs:
  - job_name: "reverse-proxy"
    static_configs:
      - targets: ["localhost:8081", "proxy:8081"]
```

---

## 7. Running the Observability Stack

```bash
# Run the reverse proxy with admin/metrics on :8081
./bin/proxy.exe \
  --port 8080 \
  --admin-port 8081 \
  --admin-token secret \
  --backends http://localhost:9001,http://localhost:9002 \
  --enable-cache \
  --enable-rate-limit

# Scrape metrics manually
curl http://localhost:8081/metrics

# Run Prometheus (point to prometheus.yml)
prometheus --config.file=deployments/prometheus/prometheus.yml

# Open Grafana at http://localhost:3000
# Dashboard auto-loaded from deployments/grafana/dashboards/
```

# System Architecture

This document provides a comprehensive architectural overview of the distributed HTTP reverse proxy — covering the system topology, component interactions, data flow, design decisions, and engineering trade-offs.

---

## 1. High-Level System Topology

```
                    ┌─────────────────────────────────────────────┐
                    │               CLIENT REQUESTS               │
                    └─────────────────┬───────────────────────────┘
                                      │
                    ┌─────────────────▼───────────────────────────┐
                    │         Layer 4 / DNS Load Balancing         │
                    │  (nginx, HAProxy, AWS ALB, or round-robin)   │
                    └─────────────────┬───────────────────────────┘
                                      │
           ┌──────────────────────────┼──────────────────────────┐
           │                          │                          │
     ┌─────▼──────┐             ┌─────▼──────┐            (more proxy
     │  proxy-1   │             │  proxy-2   │             instances)
     │  :8080     │             │  :8082     │
     │  :8081     │             │  :8083     │
     └─────┬──────┘             └─────┬──────┘
           │                          │
           │         Middleware Chain (applied per-request)
           │  ┌────────────────────────────────────────────┐
           │  │  1. RequestID — generate/propagate trace ID │
           │  │  2. Metrics   — track latency, status codes │
           │  │  3. RateLimit — token bucket per client IP  │
           │  │  4. Logging   — structured JSON access log  │
           │  │  5. Cache     — serve from LRU / Redis      │
           │  │  6. Proxy     — forward + retry + failover  │
           │  └────────────────────────────────────────────┘
           │
           │       Service Discovery (etcd watch)
     ┌─────▼──────────────────────────────────┐
     │               Load Balancer             │
     │  Round-Robin / Weighted / LeastConn     │
     └──────┬───────────────┬─────────────────┘
            │               │                  └──────────────────┐
     ┌──────▼──────┐  ┌─────▼───────┐   ┌──────────────┐        │
     │  backend-1  │  │  backend-2  │   │  backend-3   │       ...
     │  :9001      │  │  :9002      │   │  :9003       │
     └─────────────┘  └─────────────┘   └──────────────┘
```

---

## 2. Component Architecture

### 2.1 Proxy Data Plane (`cmd/proxy`, port `:8080`)

The proxy data plane receives all client traffic and is the performance-critical path:

```
HTTP Request
     │
     ▼
[RequestID middleware]        → Generate or propagate X-Request-ID header
     │
     ▼
[Metrics middleware]          → Increment active_connections gauge
     │
     ▼
[RateLimit middleware]        → Token Bucket check (per client IP)
     │  ← HTTP 429 if exhausted
     ▼
[Logging middleware]          → Record start time; emit JSON log on exit
     │
     ▼
[Cache middleware]            → Check LRU / Redis for cached response
     │  ← HTTP 200 from cache (cache hit path exits here)
     ▼
[ReverseProxy]
     ├── Load Balancer.SelectBackend()
     │       ├── Round-Robin (stateful atomic counter)
     │       ├── Weighted Round-Robin
     │       └── Least-Connections (atomic per-backend counter)
     │
     ├── CircuitBreaker.Allow() → OPEN circuit short-circuits immediately
     │
     ├── retryTransport.RoundTrip()
     │       ├── Forward to backend over pooled HTTP/1.1
     │       ├── On transport error or 502/503/504:
     │       │       └── Retry up to MaxRetries with exponential backoff + jitter
     │       │           (selecting a different backend on each attempt)
     │       └── Return response to client
     │
     └── PassiveHealthCheck → Mark backend Unhealthy on repeated 5xx
```

### 2.2 Proxy Control Plane (`cmd/proxy`, port `:8081`)

The admin control plane is isolated on a separate HTTP server and port:

| Endpoint | Method | Auth Required | Purpose |
|----------|--------|:---:|---------|
| `/admin/backends` | `GET` | ✓ | List registered backends |
| `/admin/backends` | `POST` | ✓ | Register a new backend |
| `/admin/backends/{id}` | `DELETE` | ✓ | Deregister a backend |
| `/admin/config` | `GET/POST` | ✓ | Inspect or update runtime configuration |
| `/admin/reload` | `POST` | ✓ | Reload backend pool without downtime |
| `/admin/stats` | `GET` | ✓ | Request counters, uptime, cache metrics |
| `/admin/health` | `GET` | ✓ | Per-backend health status |
| `/admin/events` | `GET` | ✓ | Stream of health state change events |
| `/metrics` | `GET` | ✗ | Prometheus metrics scrape endpoint |
| `/debug/pprof/*` | `GET` | ✗ | Go pprof CPU, memory, goroutine profiles |

### 2.3 Health Checking (`internal/health`)

Two complementary detection mechanisms work in tandem:

**Active Health Checking** (background goroutine per backend):
```
Every 5 seconds:
  Send GET /health to backend:
    200 OK  → bh.RecordSuccess()  → state: Healthy / Recovering
    Timeout → bh.RecordFailure()  → state: Unhealthy after N failures
```

**Passive Failure Detection** (in the request hot path):
```
Backend returns 5xx:
  → proxy.go: bh.RecordFailure()
  → If consecutive failures >= threshold:
      → backend excluded from load-balancer pool
```

**State Machine**:
```
             failure               N failures
[Healthy] ──────────────► [Recovering] ─────────────► [Unhealthy]
    ▲                          ▲                            │
    │                          │   probe success            │
    └──────────────────────────┘                            │
                                  probe success x3          │
         ◄──────────────────────────────────────────────────┘
```

### 2.4 Circuit Breaker (`internal/circuitbreaker`)

Protects backends from thundering herd during outages:

```
         failure_count >= threshold
[CLOSED] ──────────────────────────► [OPEN]
  ▲                                    │
  │                                    │ after reset_timeout
  │  success on probes                 ▼
  └──────────────────────── [HALF-OPEN]
```

- **CLOSED**: All requests pass through normally.
- **OPEN**: All requests are immediately short-circuited with HTTP 503.
- **HALF-OPEN**: One probe request is allowed; success → CLOSED, failure → OPEN.

### 2.5 Retry Policy (`internal/retry`)

```
Attempt 1 → backend-1 (fails with 503)
               └── sleep: 100ms × 2^0 ± jitter
Attempt 2 → backend-2 (round-robin selects next)
               └── sleep: 100ms × 2^1 ± jitter
Attempt 3 → backend-3
               └── Success → HTTP 200 returned to client
```

- Only **idempotent** methods are retried: `GET`, `HEAD`, `OPTIONS`.
- Exponential backoff is capped at `MaxBackoff` (default: 2s).
- Context cancellation aborts retry sleep immediately.

---

## 3. State & Storage Layer

```
┌──────────────────────────────────────────────────────────────┐
│                         Proxy Instances                       │
│                                                              │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐   │
│  │   proxy-1    │    │   proxy-2    │    │   proxy-N    │   │
│  └──────┬───────┘    └──────┬───────┘    └──────┬───────┘   │
└─────────┼───────────────────┼───────────────────┼───────────┘
          │                   │                   │
          ▼                   ▼                   ▼
    ┌─────────────────────────────────────────────────┐
    │               Shared Distributed State           │
    │                                                 │
    │  Redis  ─── Distributed LRU cache               │
    │         ─── Distributed Token Bucket limits      │
    │                                                 │
    │  etcd   ─── Backend service registry             │
    │         ─── Backend metadata + weights           │
    │                                                 │
    │  PostgreSQL ─ Admin backend/config persistence   │
    │             ─ Routing policy store               │
    │             ─ Health event history               │
    └─────────────────────────────────────────────────┘
```

---

## 4. Request Flow — Cache Miss Path (Full Sequence)

```
Client                  Proxy                   Backend          Redis / etcd
  │                       │                        │                   │
  │── GET /api/users ─────►                         │                   │
  │                       │── Redis GET cache key ─────────────────────►
  │                       │                        │◄── MISS ───────────
  │                       │── lb.SelectBackend() ──►                    │
  │                       │── CB.Allow() ──────────►                    │
  │                       │── HTTP GET /api/users ──►                   │
  │                       │◄─ 200 OK body ──────────                    │
  │                       │── Redis SET (cache TTL) ────────────────────►
  │◄── 200 OK ────────────│                        │                   │
  │   X-Request-ID: ...   │                        │                   │
  │   X-Backend-Server: b2│                        │                   │
```

---

## 5. Directory Structure

```
distributed-http-reverse-proxy/
├── cmd/
│   ├── proxy/          → Main reverse proxy server
│   ├── backend/        → HTTP upstream backend server
│   ├── admin/          → Admin CLI (control plane tool)
│   ├── loadgen/        → Concurrent HTTP load generator
│   ├── tcpserver/      → Day 1 TCP echo server (foundations)
│   └── tcpclient/      → Day 1 TCP client
│
├── internal/
│   ├── admin/          → Admin HTTP API & control plane
│   ├── cache/          → LRU memory cache & Redis distributed cache
│   ├── circuitbreaker/ → CLOSED/OPEN/HALF-OPEN state machine
│   ├── connectionpool/ → Tunable http.Transport pool
│   ├── discovery/      → etcd v3 registry + watcher
│   ├── health/         → Active & passive health checker
│   ├── loadbalancer/   → Round-Robin, Weighted RR, Least-Connections
│   ├── metrics/        → Prometheus collector registry
│   ├── middleware/      → RequestID, Logging, Metrics, RateLimit, Cache
│   ├── proxy/          → httputil.ReverseProxy with retry transport
│   ├── ratelimit/      → Token Bucket + Redis Lua distributed limiter
│   ├── retry/          → Exponential backoff + jitter retry policy
│   └── storage/        → Store interface, MemoryStore, PostgresStore
│
├── test/
│   ├── chaos/          → Automated chaos engineering test suite
│   └── load/           → High-concurrency proxy benchmarks
│
├── deployments/
│   ├── prometheus/     → Scrape configuration
│   └── grafana/        → Dashboard JSON + provisioning configs
│
├── docs/               → Documentation per major subsystem
├── .github/workflows/  → GitHub Actions CI/CD (lint, test, build, docker)
├── Dockerfile.proxy    → Multi-stage container for proxy
├── Dockerfile.backend  → Multi-stage container for backend
├── Dockerfile.admin    → Container for admin CLI
├── docker-compose.yml  → 10-node distributed cluster definition
└── Makefile            → Build, test, bench, docker targets
```

---

## 6. Concurrency Model

| Component | Goroutines Used | Synchronization |
|-----------|----------------|-----------------|
| Proxy HTTP Server | `GOMAXPROCS` workers + per-conn goroutines | `net/http` handles internally |
| Active Health Checker | 1 goroutine per backend | `context.Context` cancellation |
| etcd Watcher | 1 goroutine per watcher | `context.Context` cancellation |
| Load Balancer (RoundRobin) | Concurrent reads/writes | `sync.RWMutex` + `atomic.Uint64` |
| Circuit Breaker | Concurrent reads | `sync.RWMutex` |
| Metrics | Concurrent increments | Prometheus atomic counters |
| Rate Limiter | Per-client bucket map | `sync.Mutex` per bucket |

---

## 7. Security Considerations

> [!IMPORTANT]
> This is a learning / portfolio project. Do not expose admin endpoints or `/debug/pprof/` on a public internet-facing network.

| Surface | Controls |
|---------|----------|
| Admin API | Bearer token + `X-Admin-Token` header authentication |
| Metrics & pprof | No auth (intended for internal Prometheus scraping) — restrict at network level |
| Backend requests | Configurable `BackendTimeout` prevents slow-loris attacks |
| Request size | `ReadTimeout` limits how long clients can stream request bodies |
| Rate limiting | Per-client-IP token bucket prevents abuse bursts |

---

## 8. Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Standard library `net/http`** | No framework overhead, full control over connection lifecycle and middleware |
| **`httputil.ReverseProxy`** | Battle-tested HTTP/1.1 proxying; wrapping it with custom transport gives full retry control |
| **Middleware chain as function slice** | Simple, type-safe, easy to add/remove at startup; no reflection |
| **Prometheus over custom metrics** | Industry standard; native Grafana support; battle-tested concurrency |
| **etcd for service discovery** | Strong consistency guarantees; native lease keepalive; watch streaming |
| **Redis for distributed state** | Cache and rate-limit state is shared across all proxy instances without coordination |
| **PostgreSQL for admin persistence** | ACID guarantees for backend registration and routing policies |
| **Interface-based storage** | `Store` interface enables seamless switch between memory and Postgres |
| **Multi-stage Docker builds** | Static binary + Alpine runtime → ~25MB images; minimal attack surface |

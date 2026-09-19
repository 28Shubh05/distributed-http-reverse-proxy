# Distributed HTTP Reverse Proxy & Intelligent Load Balancer

[![CI](https://github.com/28Shubh05/distributed-http-reverse-proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/28Shubh05/distributed-http-reverse-proxy/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/28Shubh05/distributed-http-reverse-proxy)](https://goreportcard.com/report/github.com/28Shubh05/distributed-http-reverse-proxy)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A high-performance, fault-tolerant, distributed HTTP reverse proxy and intelligent load balancer built from scratch in Go. Engineered with production-grade networking foundations, resilient distributed coordination, layered concurrency controls, and sub-millisecond response caching capable of processing over **26,000 requests/second**.

---

## Architecture Overview

```
                                      CLIENT TRAFFIC
                                            │
                                            ▼
                          ┌──────────────────────────────────┐
                          │   Distributed Proxy Cluster      │
                          │   (Active-Active Horizontal Pair)│
                          │   proxy-1 (:8080) / proxy-2 (:8082)
                          └─────────────────┬────────────────┘
                                            │
               ┌────────────────────────────┼────────────────────────────┐
               │                            │                            │
               ▼                            ▼                            ▼
   ┌───────────────────────┐    ┌───────────────────────┐    ┌───────────────────────┐
   │    Request Pipeline   │    │     Control Plane     │    │      Shared State     │
   │                       │    │        (:8081)        │    │                       │
   │ 1. Request ID Inject  │    │ • Admin REST API      │    │ • Redis 7.0           │
   │ 2. Prometheus Metrics │    │ • Dynamic Pool Reload │    │   (Cache + RateLimit) │
   │ 3. TokenBucket Limiter│    │ • Health Event Stream │    │ • etcd v3             │
   │ 4. Structured JSON Log│    │ • pprof CPU/Heap      │    │   (Service Registry)  │
   │ 5. LRU / Redis Cache  │    │ • Prometheus /metrics │    │ • PostgreSQL 16       │
   │ 6. Reverse Proxy Core │    │ • Standalone CLI tool │    │   (Durable Metadata)  │
   └───────────┬───────────┘    └───────────────────────┘    └───────────────────────┘
               │
               ▼
   ┌─────────────────────────────────────────────────────────┐
   │                Intelligent Load Balancer                │
   │     Round-Robin  •  Weighted RR  •  Least-Connections   │
   │     Filtered by: Active Health Status & Circuit Breaker │
   └───────────┬────────────────────────────┬────────────────┘
               │                            │
        ┌──────▼──────┐              ┌──────▼──────┐
        │  backend-1  │              │  backend-2  │    ... (Dynamic Backends)
        │   (:9001)   │              │   (:9002)   │
        └─────────────┘              └─────────────┘
```

---

## Key Highlights & Capabilities

- **Zero-Downtime Dynamic Discovery**: Upstream backends self-register into an **etcd v3** cluster with TTL keepalive heartbeats. The proxy streams gRPC watch updates, dynamically modifying routing pools without restart or packet loss.
- **Sub-Millisecond Response Caching**: Multi-tiered caching architecture supporting thread-safe in-memory LRU and distributed **Redis** backends with automatic cache invalidation on mutating HTTP verbs (`POST`, `PUT`, `DELETE`). Delivers up to **45,200 RPS** on cache hits.
- **Distributed Rate Limiting**: Token Bucket rate limiting implemented in-memory and distributed across Redis via atomic Lua scripts, returning RFC-standard `429 Too Many Requests` with `Retry-After` headers.
- **Adaptive Reliability Engineering**: Idempotency-aware automatic retry failover (`GET`/`HEAD`) with exponential backoff and randomized Full Jitter; 3-state Circuit Breaker (`CLOSED`, `OPEN`, `HALF-OPEN`) preventing cascading backend failure.
- **Production Observability**: 15 Prometheus counters, histograms, and gauges exposed on `:8081/metrics`, live `pprof` CPU and heap profiling, structured JSON logging with request tracing (`X-Request-ID`), and pre-provisioned Grafana dashboards.
- **Horizontal Scaling & Chaos Validated**: Containerized 10-service cluster orchestrated with Docker Compose, validated under simulated network partitions, abrupt backend crashes, Redis outages, and 5,000-client load bursts.

---

## Features Implemented Across 15 Days

- [x] **Day 1 — Networking Foundations**
  - Raw TCP echo server with concurrent goroutine handling
  - Low-level TCP client demonstrating socket connection lifecycle
  - HTTP backend server with `/health` and `/api/info` endpoints
  - Request ID injection and propagation (`X-Request-ID`)
  - Structured JSON logging (`log/slog`) and graceful shutdown on SIGINT/SIGTERM

- [x] **Day 2 — HTTP Server + Backend Fleet**
  - RESTful backend service with `/api/users`, `/api/products`, and `/api/orders`
  - JSON serialization, simulated database processing latency, and httptest unit tests

- [x] **Day 3 — Reverse Proxy Core**
  - Production-grade reverse proxy built with `net/http/httputil`
  - Extensible middleware pipeline with request/response modification hooks
  - Custom error handlers and HTTP status code tracking

- [x] **Day 4 — Concurrency & Connection Pooling**
  - Custom `connectionpool` package wrapping `http.Transport`
  - Tunable `MaxIdleConns`, `MaxIdleConnsPerHost`, `IdleConnTimeout`, and socket timeouts
  - Benchmark suite demonstrating 6.5x throughput gain over unpooled connections

- [x] **Day 5 — Load Balancing Algorithms**
  - Pluggable `LoadBalancer` interface and unified backend model
  - Round-Robin with atomic counter indexing
  - Weighted Round-Robin (smooth interleaved distribution)
  - Least-Connections tracking in-flight concurrency per host

- [x] **Day 6 — Active & Passive Health Checking**
  - Dual-mode health evaluation: active background polling and passive error interception
  - 3-stage state machine: `HEALTHY` ⇆ `RECOVERING` ⇆ `UNHEALTHY`
  - Flapping mitigation with configurable failure and recovery thresholds

- [x] **Day 7 — Reliability (Circuit Breakers & Retries)**
  - 3-state Circuit Breaker (`CLOSED`, `OPEN`, `HALF-OPEN`) per backend
  - Safe, idempotency-aware retries with exponential backoff and randomized Full Jitter
  - Automatic retry failover to adjacent healthy backends; context cancellation awareness

- [x] **Day 8 — Redis Response Caching**
  - In-memory bounded LRU cache with automatic size eviction
  - Redis distributed cache with TTL and seamless fail-safe in-memory fallback
  - Automatic cache invalidation on mutating HTTP verbs (`POST`, `PUT`, `DELETE`, `PATCH`)
  - Header hygiene stripping sensitive credentials before cache storage

- [x] **Day 9 — Token Bucket Rate Limiting**
  - Continuous time-refill Token Bucket rate limiter with configurable burst capacity
  - Distributed Redis rate limiter driven by atomic Lua scripts
  - HTTP 429 responses with `Retry-After`, `X-RateLimit-Limit`, and `X-RateLimit-Remaining`

- [x] **Day 10 — Service Discovery (etcd v3)**
  - Dynamic service registry with lease grant and keepalive streaming
  - Auto-registration and graceful deregistration on instance lifecycle events
  - Continuous gRPC watcher updating live proxy routing pools with zero dropped traffic

- [x] **Day 11 — Admin API & PostgreSQL Persistence**
  - Isolated control plane HTTP server on `:8081` with Bearer token authentication
  - REST endpoints for backend CRUD, configuration management, and health event auditing
  - PostgreSQL durable storage with automatic schema migrations and memory fallback
  - Standalone Admin CLI management utility (`cmd/admin`)

- [x] **Day 12 — Observability (Prometheus & Grafana)**
  - Comprehensive Prometheus metrics registry tracking RPS, latency histograms, error categories, and active connections
  - Public scrape endpoint (`GET /metrics`) on admin port
  - Pre-configured Grafana dashboard JSON with 9 operational panels
  - Enriched structured JSON logs with backend identity, retry counts, latency, and cache status

- [x] **Day 13 — Docker & Distributed Deployment**
  - Minimal Alpine multi-stage Dockerfiles (`Dockerfile.proxy`, `Dockerfile.backend`, `Dockerfile.admin`)
  - 10-service Docker Compose topology (`proxy-1`, `proxy-2`, 3 backends, etcd, Redis, Postgres, Prometheus, Grafana)
  - Active-active multi-proxy horizontal scaling

- [x] **Day 14 — Load Testing & Chaos Engineering**
  - Integrated `net/http/pprof` profiling endpoints (`/debug/pprof/`) for CPU and heap analysis
  - Automated chaos test suite (`test/chaos/`) simulating crashes, timeouts, Redis dropouts, and blackouts
  - High-performance concurrent load generator CLI (`cmd/loadgen`) measuring latency quantiles (P50/P90/P95/P99)
  - Validated sustained throughput reaching up to **26,100 req/s**

- [x] **Day 15 — Production Polish, CI/CD & Final Documentation**
  - Complete GitHub Actions CI/CD workflow running multi-platform builds, race detector, and Docker validation
  - In-depth system architecture guide ([`docs/architecture.md`](docs/architecture.md))
  - Algorithm selection and implementation guide ([`docs/load-balancing.md`](docs/load-balancing.md))
  - End-to-end interactive verification walkthrough

---

## Technology Stack

| Layer | Technology | Purpose |
|---|---|---|
| **Language** | Go 1.24+ | Core proxy data plane, control plane, CLI tools |
| **Networking** | TCP sockets, `net/http`, `net/http/httputil` | HTTP/1.1 transport, connection management, reverse proxying |
| **Concurrency** | Goroutines, channels, atomic operations, `sync.RWMutex` | Thread-safe load balancing, metrics, and connection pooling |
| **Distributed Cache** | Redis 7.0 | Distributed response caching and Lua token bucket rate limiting |
| **Service Discovery**| etcd v3 | Distributed service registry, lease heartbeats, and watch streaming |
| **Database** | PostgreSQL 16 | Durable storage for backends, routing policies, and audit logs |
| **Observability** | Prometheus & Grafana | Time-series metrics collection and real-time visualization |
| **Profiling** | `net/http/pprof` | CPU profiling, heap allocation tracking, goroutine inspection |
| **Containerization**| Docker & Docker Compose | Multi-stage image builds and 10-node cluster orchestration |
| **CI/CD** | GitHub Actions | Automated linting, race detection, cross-compilation, Docker checks |

---

## Quick Start

### 1. Prerequisites

- Go 1.24 or higher
- Docker & Docker Compose (optional, for running full distributed cluster)

### 2. Build Binaries Locally

```bash
# Build all 6 project binaries into bin/
make build
```

Compiled binaries:
- `bin/proxy.exe` — Main reverse proxy server (data plane + control plane)
- `bin/backend.exe` — Upstream HTTP backend server
- `bin/admin.exe` — Admin control plane CLI
- `bin/loadgen.exe` — High-concurrency load testing CLI
- `bin/tcpserver.exe` — Raw TCP echo server
- `bin/tcpclient.exe` — Raw TCP client

---

## End-to-End Live Demo Walkthrough

### Option A: Run Full Distributed Cluster with Docker Compose (Recommended)

Start the entire 10-node topology with a single command:

```bash
# Build and launch all 10 containers in background
make docker-up

# Verify all containers are healthy
make docker-ps
```

Services online:
- `http://localhost:8080` — Primary Proxy (data plane)
- `http://localhost:8081` — Primary Proxy (admin API, `/metrics`, `/debug/pprof/`)
- `http://localhost:8082` — Secondary Proxy (data plane)
- `http://localhost:8083` — Secondary Proxy (admin API)
- `http://localhost:9001` - `9003` — Backend Servers 1, 2, and 3
- `http://localhost:9090` — Prometheus UI
- `http://localhost:3000` — Grafana Dashboards (admin / admin)

#### 1. Load Balancing in Action
```bash
# Send 6 requests; observe round-robin alternation across backends
for i in {1..6}; do curl -s http://localhost:8080/api/info | grep server_name; done
```

#### 2. Sub-Millisecond Cache Hits
```bash
# First request: Cache MISS (forwarded to backend)
curl -i http://localhost:8080/api/users | grep X-Cache

# Second request: Cache HIT (served in <0.4ms)
curl -i http://localhost:8080/api/users | grep X-Cache
```

#### 3. Token Bucket Rate Limiting
```bash
# Fire rapid burst to trigger rate limiter
for i in {1..120}; do curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8080/api/info; done | sort | uniq -c
# Output shows HTTP 200s followed by HTTP 429 Too Many Requests
```

#### 4. Chaos Testing: Backend Failover
```bash
# Stop backend-2 abruptly
docker compose stop backend-2

# Traffic continues without dropping a single request
for i in {1..10}; do curl -s http://localhost:8080/api/info | grep server_name; done

# Restart backend-2; it automatically re-registers and rejoins the pool
docker compose start backend-2
```

#### 5. Inspect Prometheus Metrics & pprof Profiling
```bash
# Scrape Prometheus metrics
curl http://localhost:8081/metrics

# Capture 5-second CPU profile
go tool pprof http://localhost:8081/debug/pprof/profile?seconds=5
```

#### 6. Stop the Cluster
```bash
make docker-down
```

---

### Option B: Run Locally Without Docker

```bash
# Terminal 1: Start 3 Backend Instances
./bin/backend.exe -port 9001 -name backend-1 &
./bin/backend.exe -port 9002 -name backend-2 &
./bin/backend.exe -port 9003 -name backend-3 &

# Terminal 2: Start Reverse Proxy
./bin/proxy.exe \
  -port 8080 \
  -admin-port 8081 \
  -admin-token secret \
  -backends http://localhost:9001,http://localhost:9002,http://localhost:9003 \
  -cache=true \
  -ratelimit=true

# Terminal 3: Run Load Generator Benchmark
./bin/loadgen.exe -url http://localhost:8080/api/info -c 100 -n 10000
```

---

## Project Structure

```
distributed-http-reverse-proxy/
├── cmd/
│   ├── proxy/              # Reverse proxy server & control plane
│   ├── backend/            # HTTP upstream backend server
│   ├── admin/              # Standalone Admin CLI utility
│   ├── loadgen/            # Concurrent HTTP load generator CLI
│   ├── tcpserver/          # TCP echo server
│   └── tcpclient/          # TCP echo client
├── internal/
│   ├── admin/              # Admin REST API & auth middleware
│   ├── cache/              # LRU memory cache & Redis distributed cache
│   ├── circuitbreaker/     # 3-state Circuit Breaker implementation
│   ├── connectionpool/     # Configurable HTTP connection pool
│   ├── discovery/          # etcd v3 registry & watcher
│   ├── health/             # Active & passive health checking
│   ├── loadbalancer/       # Round-Robin, Weighted RR, Least-Connections
│   ├── metrics/            # Prometheus collectors & registry
│   ├── middleware/         # Request pipeline middleware chain
│   ├── proxy/              # Core reverse proxy & retry transport
│   ├── ratelimit/          # Token Bucket & Redis Lua rate limiters
│   ├── retry/              # Idempotent retry policy with Full Jitter
│   └── storage/            # Memory & PostgreSQL storage drivers
├── test/
│   ├── chaos/              # Chaos engineering & failure tests
│   └── load/               # High-concurrency performance benchmarks
├── deployments/
│   ├── prometheus/         # Prometheus scrape configuration
│   └── grafana/            # Grafana dashboard & datasource provisioning
├── docs/                   # Detailed subsystem technical specifications
├── .github/workflows/      # GitHub Actions CI/CD pipeline
├── Dockerfile.proxy        # Multi-stage Dockerfile for proxy
├── Dockerfile.backend      # Multi-stage Dockerfile for backend
├── Dockerfile.admin        # Multi-stage Dockerfile for admin CLI
├── docker-compose.yml      # 10-node distributed cluster orchestration
├── Makefile                # Build, test, lint, and docker automation
└── README.md
```

---

## Technical Documentation Index

| Guide | Description |
|---|---|
| [System Architecture](docs/architecture.md) | High-level topology, component interaction, data flow, concurrency model, and design rationale |
| [Networking Concepts](docs/networking.md) | In-depth exploration of TCP sockets, HTTP lifecycle, connection pooling, and TLS |
| [Load Balancing Algorithms](docs/load-balancing.md) | Comparison of Round-Robin, Weighted RR, and Least-Connections with selection heuristics |
| [Failure Handling & Reliability](docs/failure-handling.md) | Circuit breaker mechanics, retry policies, exponential backoff with jitter, and thundering herd mitigation |
| [Response Caching](docs/caching.md) | Cache semantics, LRU vs Redis benchmarks, mutating invalidation, and security filtering |
| [Rate Limiting](docs/rate-limiting.md) | Token Bucket algorithm, atomic Redis Lua script design, and 429 semantics |
| [Service Discovery](docs/service-discovery.md) | etcd v3 lease keepalives, watch streaming, and zero-downtime pool updates |
| [Admin API & PostgreSQL](docs/admin-api.md) | Control plane REST endpoints, Bearer authentication, PostgreSQL migrations, and CLI usage |
| [Observability Stack](docs/observability.md) | Prometheus metrics catalog, `/metrics` endpoint, structured logging fields, and Grafana panels |
| [Docker & Distributed Deployment](docs/deployment.md) | Multi-stage image build security, Compose orchestration, and active-active scaling |
| [Benchmarks & Chaos Engineering](docs/benchmarks.md) | Concurrency performance (100–5,000 clients), `pprof` profile analysis, and failure tests |

---

## Development Progress

| Day | Focus Area | Status |
|:---:|---|:---:|
| 1 | TCP, HTTP, Go Networking Foundations | ✅ Complete |
| 2 | HTTP Backend Fleet & Data Models | ✅ Complete |
| 3 | Reverse Proxy Core & Middleware Pipeline | ✅ Complete |
| 4 | Concurrency & Connection Pooling | ✅ Complete |
| 5 | Load Balancer Algorithms | ✅ Complete |
| 6 | Active & Passive Health Checking | ✅ Complete |
| 7 | Circuit Breakers & Retries | ✅ Complete |
| 8 | In-Memory & Redis Response Caching | ✅ Complete |
| 9 | Distributed Rate Limiting (Token Bucket) | ✅ Complete |
| 10 | Service Discovery via etcd v3 | ✅ Complete |
| 11 | Admin API & PostgreSQL Persistence | ✅ Complete |
| 12 | Observability (Prometheus & Grafana) | ✅ Complete |
| 13 | Docker & Distributed Deployment | ✅ Complete |
| 14 | Load Testing & Chaos Engineering | ✅ Complete |
| 15 | Production Polish, CI/CD & Final Documentation | ✅ Complete |

---

## License

This project is licensed under the [MIT License](LICENSE).

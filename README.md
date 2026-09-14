# Distributed HTTP Reverse Proxy & Intelligent Load Balancer

A production-style distributed reverse proxy and intelligent load balancer built from scratch in Go. This project demonstrates deep understanding of computer networking, concurrent programming, distributed systems, and reliability engineering.

## Architecture

```
              Client
                |
                | HTTP
                v
  +-----------------------------+
  |    Reverse Proxy / LB       |
  |                             |
  |  HTTP Server                |
  |  Router                     |
  |  Load Balancer              |
  |  Rate Limiter               |
  |  Circuit Breaker            |
  |  Cache                      |
  |  Connection Pool            |
  +-------------+---------------+
                |
      +---------+---------+
      |         |         |
      v         v         v
   Backend   Backend   Backend
      1         2         3

  Service Registry (etcd)
  Cache / Rate Limit (Redis)
  Metadata (PostgreSQL)
  Observability (Prometheus → Grafana)
```

## Features

### Implemented

- [x] **Day 1 — Networking Foundations**
  - Raw TCP echo server with concurrent client handling
  - Raw TCP client demonstrating connection lifecycle
  - HTTP backend server with health check and info endpoints
  - Request ID generation and propagation (X-Request-ID)
  - Structured JSON logging (log/slog)
  - Graceful shutdown (SIGINT/SIGTERM handling)
  - HTTP server timeout configuration

- [x] **Day 2 — HTTP Server + Backend**
  - Configurable HTTP backend service with REST endpoints
  - Data models and JSON encoding/decoding
  - `GET /api/users` and `GET /api/products` endpoints
  - `POST /api/orders` endpoint with simulated database latency
  - Unit testing with `net/http/httptest`
  - Multi-instance configuration (Makefile targets)

- [x] **Day 3 — Reverse Proxy**
  - Standard httputil.ReverseProxy implementation
  - Shared middleware architecture
  - Request ID propagation and tracking
  - Latency and status code logging

- [x] **Day 4 — Concurrency + Connection Management**
  - `internal/connectionpool` package wrapping `http.Transport`
  - Configurable pool: `MaxIdleConns`, `MaxIdleConnsPerHost`, `IdleConnTimeout`, dial/response timeouts
  - `NoPool` config for benchmark comparison
  - Per-request backend timeout via `context.WithTimeout`
  - Client disconnect detection (context cancellation propagation)
  - `transport.CloseIdleConnections()` on graceful shutdown
  - Benchmark suite: `BenchmarkPooled`, `BenchmarkNoPool`, parallel variants
  - `go test -race ./...` passes clean

- [x] **Day 5 — Load Balancer**
  - `LoadBalancer` interface and `Backend` model
  - Round Robin implementation (thread-safe, atomic operations)
  - Weighted Round Robin implementation (LVS algorithm)
  - Least Connections implementation
  - Independent testing of all algorithms

- [x] **Day 6 — Health Checking**
  - Active background polling (`/health`) with dedicated HTTP client
  - Passive failure detection (hooks into 5xx and transport errors)
  - `HEALTHY` / `UNHEALTHY` / `RECOVERING` state machine
  - Configurable failure and recovery thresholds to prevent flapping
  - Load balancer integration (skips unavailable backends)

- [x] **Day 7 — Reliability (Circuit Breakers + Retries)**
  - Circuit Breaker with `CLOSED`, `OPEN`, and `HALF_OPEN` states
  - Rate-limited probe requests in `HALF_OPEN` (thundering herd prevention)
  - Safe, idempotency-aware retries (GET/HEAD enabled, POST disabled)
  - Exponential backoff with randomized Full Jitter
  - Automatic retry failover to surviving healthy backends
  - Context cancellation support (promptly aborts backoff sleep)

- [x] **Day 8 — Redis + Caching**
  - High-performance In-Memory LRU Cache with size bounding and eviction
  - Redis-backed distributed cache with TTL and fail-safe fallback
  - Caching middleware with `X-Cache: HIT / MISS / BYPASS` observability
  - Automatic cache invalidation on mutating requests (POST, PUT, DELETE, PATCH)
  - Security header hygiene (excludes Auth/Cookie requests to prevent leaks)
  - Benchmarked ~2,190x speedup with 0 memory allocations on cache hits

- [x] **Day 9 — Rate Limiting (Token Bucket + Redis)**
  - In-Memory Token Bucket rate limiter with continuous time-based refill
  - Burst capacity handling and per-client IP isolation
  - Distributed Redis rate limiter using atomic Lua scripts
  - Fail-safe fallback to in-memory limiter when Redis is unreachable
  - HTTP `429 Too Many Requests` responses with `Retry-After` header
  - Standard headers: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`

- [x] **Day 10 — Service Discovery (etcd)**
  - etcd v3 service discovery registry with lease grant and keepalive heartbeats
  - Automatic backend self-registration on startup and graceful deregistration
  - Dynamic discovery watcher streaming changes via etcd gRPC watch API
  - Zero-downtime hot updates to load balancer pool and health checker without restart
  - Tested dynamic lifecycle: backend start, backend stop, and backend restart

### Planned

- [ ] Admin API with authentication
- [ ] PostgreSQL integration for metadata/configuration
- [ ] Prometheus metrics + Grafana dashboard
- [ ] Docker + Docker Compose deployment
- [ ] Multi-proxy horizontal scaling
- [ ] Load testing + chaos testing + benchmarks
- [ ] CI/CD with GitHub Actions

## Technology Stack

| Layer | Technology |
|-------|-----------|
| Language | Go 1.26 |
| Networking | TCP sockets, net/http, HTTP/1.1, TLS |
| Concurrency | Goroutines, channels, sync.Mutex, context.Context |
| Cache / Rate Limit | Redis |
| Service Discovery | etcd |
| Database | PostgreSQL |
| Observability | Prometheus, Grafana, structured logging |
| Infrastructure | Docker, Docker Compose, GitHub Actions |
| Testing | Go testing, benchmarks, race detector, hey/wrk/k6 |

## Quick Start

### Prerequisites

- Go 1.26+

### Build

```bash
# Build all binaries
make build

# Or build individually
go build -o bin/tcpserver.exe ./cmd/tcpserver/
go build -o bin/tcpclient.exe ./cmd/tcpclient/
go build -o bin/backend.exe ./cmd/backend/
```

### Run

#### TCP Echo Server + Client

```bash
# Terminal 1: Start TCP server
go run ./cmd/tcpserver/ -port 9000

# Terminal 2: Connect with TCP client
go run ./cmd/tcpclient/ -host localhost -port 9000
# Type messages and see them echoed back
```

#### HTTP Backend Server

```bash
# Start backend
go run ./cmd/backend/ -port 9001 -name backend-1

# Test health endpoint
curl http://localhost:9001/health

# Test info endpoint
curl http://localhost:9001/api/info
```

### Test

```bash
make test          # Run all tests
make test-race     # Run tests with race detector
make vet           # Run go vet
make fmt           # Format code
```

## Project Structure

```
distributed-http-reverse-proxy/
├── cmd/
│   ├── tcpserver/         # Raw TCP echo server
│   │   └── main.go
│   ├── tcpclient/         # Raw TCP client
│   │   └── main.go
│   └── backend/           # HTTP backend server
│       └── main.go
├── configs/
│   └── proxy.yaml         # Configuration
├── docs/
│   └── networking.md      # Networking concepts documentation
├── .gitignore
├── go.mod
├── Makefile
└── README.md
```

## Documentation

- [Networking Concepts](docs/networking.md) — TCP, HTTP, sockets, DNS, connection pooling, TLS, reverse/forward proxy
- [Failure Handling & Reliability](docs/failure-handling.md) — Circuit breakers, idempotency, retries, exponential backoff, jitter, retry storms
- [Response Caching](docs/caching.md) — HTTP caching semantics, LRU vs Redis, invalidation, security rules, benchmarks
- [Rate Limiting](docs/rate-limiting.md) — Token Bucket algorithm, distributed Redis Lua scripts, 429 semantics, burst handling
- [Service Discovery](docs/service-discovery.md) — etcd v3 registry, lease keepalive, watch streaming, dynamic pool updates

## Development Progress

| Day | Focus | Status |
|-----|-------|--------|
| 1 | TCP, HTTP, Go Networking Foundations | ✅ Complete |
| 2 | HTTP Backend Service | ✅ Complete |
| 3 | Reverse Proxy | ✅ Complete |
| 4 | Concurrency + Connection Pooling | ✅ Complete |
| 5 | Load Balancer Algorithms | ✅ Complete |
| 6 | Health Checking | ✅ Complete |
| 7 | Circuit Breaker + Retries | ✅ Complete |
| 8 | Redis Caching | ✅ Complete |
| 9 | Rate Limiting | ✅ Complete |
| 10 | Service Discovery (etcd) | ✅ Complete |
| 11 | Admin API + PostgreSQL | 🔜 Next |
| 12 | Observability (Prometheus + Grafana) | ⬜ |
| 13 | Docker + Distributed Deployment | ⬜ |
| 14 | Load Testing + Chaos Testing | ⬜ |
| 15 | Production Polish + Documentation | ⬜ |

## License

MIT

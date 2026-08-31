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

### Planned

- [ ] Configurable HTTP backend service with REST endpoints
- [ ] Reverse proxy with request/response forwarding
- [ ] Connection pooling with benchmarks
- [ ] Load balancing (Round Robin, Weighted Round Robin, Least Connections, EWMA)
- [ ] Active health checking + passive failure detection
- [ ] Circuit breaker with CLOSED/OPEN/HALF_OPEN states
- [ ] Retry policy with exponential backoff and jitter
- [ ] Redis-based HTTP response caching
- [ ] Token Bucket rate limiting + Redis distributed rate limiter
- [ ] etcd service discovery with dynamic backend registration
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

## Development Progress

| Day | Focus | Status |
|-----|-------|--------|
| 1 | TCP, HTTP, Go Networking Foundations | ✅ Complete |
| 2 | HTTP Backend Service | 🔜 Next |
| 3 | Reverse Proxy | ⬜ |
| 4 | Concurrency + Connection Pooling | ⬜ |
| 5 | Load Balancer Algorithms | ⬜ |
| 6 | Health Checking | ⬜ |
| 7 | Circuit Breaker + Retries | ⬜ |
| 8 | Redis Caching | ⬜ |
| 9 | Rate Limiting | ⬜ |
| 10 | Service Discovery (etcd) | ⬜ |
| 11 | Admin API + PostgreSQL | ⬜ |
| 12 | Observability (Prometheus + Grafana) | ⬜ |
| 13 | Docker + Distributed Deployment | ⬜ |
| 14 | Load Testing + Chaos Testing | ⬜ |
| 15 | Production Polish + Documentation | ⬜ |

## License

MIT

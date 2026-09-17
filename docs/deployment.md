# Docker & Distributed Deployment

This document describes the containerized multi-node architecture, multi-stage Docker builds, Docker Compose cluster orchestration, dynamic service discovery across container networks, and failure simulation procedures for the distributed HTTP reverse proxy.

---

## 1. Distributed Topology Overview

The cluster consists of **10 orchestrated services** running within an isolated Docker bridge network (`proxy-net`):

```
                                  ┌────────────────────────┐
                                  │      Client Traffic    │
                                  └───────────┬────────────┘
                                              │
                      ┌───────────────────────┴───────────────────────┐
                      │                                               │
                      ▼ :8080 / :8081                                 ▼ :8082 / :8083
           ┌──────────────────────┐                       ┌──────────────────────┐
           │       proxy-1        │                       │       proxy-2        │
           │  (Active-Active Pair)│                       │  (Active-Active Pair)│
           └──────┬────────┬──────┘                       └──────┬────────┬──────┘
                  │        │                                     │        │
      ┌───────────┘        └───────────┐             ┌───────────┘        └───────────┐
      │                                │             │                                │
      ▼                                ▼             ▼                                ▼
┌───────────┐                    ┌───────────┐ ┌───────────┐                    ┌───────────┐
│ backend-1 │                    │ backend-2 │ │ backend-3 │                    │   etcd    │
│  (:9001)  │                    │  (:9002)  │ │  (:9003)  │                    │  (:2379)  │
└─────┬─────┘                    └─────┬─────┘ └─────┬─────┘                    └─────┬─────┘
      │                                │             │                                │
      └────────────────────────────────┴──────┬──────┴────────────────────────────────┘
                                              │ Auto-registration via etcd lease
                                              ▼
                        ┌───────────────────────────────────────────┐
                        │        Shared State & Cache               │
                        │ • redis:6379 (distributed rate & cache)   │
                        │ • postgres:5432 (durable control plane)   │
                        └─────────────────────┬─────────────────────┘
                                              │
                                              ▼
                        ┌───────────────────────────────────────────┐
                        │        Observability Infrastructure       │
                        │ • prometheus:9090 (scrapes proxy-1/2)     │
                        │ • grafana:3000 (pre-provisioned dashboard)│
                        └───────────────────────────────────────────┘
```

---

## 2. Multi-Stage Docker Builds

Each Go service utilizes a multi-stage Dockerfile:

1. **Build Stage (`golang:1.24-alpine`)**:
   - Downloads dependencies (`go mod download`).
   - Compiles static Go binaries with CGO disabled: `CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w"`.
2. **Runtime Stage (`alpine:3.20`)**:
   - Contains only the static binary and essential CA certificates, `tzdata`, and `curl` for container health checks.
   - Resulting image size is minimal (~25MB), drastically reducing attack surface and container startup latency.
3. **Container Healthchecks**:
   - Proxies: `curl -fs http://localhost:8081/admin/health || exit 1`
   - Backends: `curl -fs http://localhost:9001/health || exit 1`

---

## 3. Orchestrated Services (`docker-compose.yml`)

| Service | Image / Build | Internal Port | Host Port | Purpose |
|---------|---------------|---------------|-----------|---------|
| `proxy-1` | `Dockerfile.proxy` | `8080`, `8081` | `8080:8080`, `8081:8081` | Primary reverse proxy (data plane + admin/metrics) |
| `proxy-2` | `Dockerfile.proxy` | `8080`, `8081` | `8082:8080`, `8083:8081` | Secondary proxy (horizontal active-active scaling) |
| `backend-1` | `Dockerfile.backend` | `9001` | `9001:9001` | HTTP upstream backend 1 |
| `backend-2` | `Dockerfile.backend` | `9002` | `9002:9002` | HTTP upstream backend 2 |
| `backend-3` | `Dockerfile.backend` | `9003` | `9003:9003` | HTTP upstream backend 3 |
| `etcd` | `quay.io/coreos/etcd:v3.5.15` | `2379` | `2379:2379` | Distributed service discovery registry |
| `redis` | `redis:7-alpine` | `6379` | `6379:6379` | Distributed cache & token bucket rate limiter |
| `postgres` | `postgres:16-alpine` | `5432` | `5432:5432` | Durable storage for backends, policies, events |
| `prometheus`| `prom/prometheus:latest`| `9090` | `9090:9090` | Time-series scraper (5s interval) |
| `grafana` | `grafana/grafana:latest` | `3000` | `3000:3000` | Real-time monitoring dashboards |

---

## 4. Quick Start & Cluster Management

### Using Makefile Targets

```bash
# Build all container images
make docker-build

# Start all 10 cluster services in background
make docker-up

# Verify service health and running status
make docker-ps

# Follow aggregate logs
make docker-logs

# Shut down and remove containers and network
make docker-down
```

### Direct Docker Compose Commands

```bash
# Build and start in detached mode
docker compose up --build -d

# Check container statuses
docker compose ps
```

---

## 5. Dynamic Service Discovery in Container Networks

When backend instances boot:
1. Each backend is given `-advertise-url http://backend-<N>:<port>` and `-etcd-endpoints etcd:2379`.
2. The backend connects to etcd, acquires a 5-second lease, and writes its registration key:
   `/services/backends/backend-<N> -> {"id":"backend-<N>","url":"http://backend-<N>:<port>","weight":1}`
3. Both `proxy-1` and `proxy-2` run an active etcd watcher that streams dynamic updates.
4. If a new backend is added or scaled, both proxies dynamically update their round-robin pool without reload or restart.

---

## 6. Fault Tolerance & Chaos Testing Demonstration

### Step 1: Send Traffic Through Proxy
```bash
curl -i http://localhost:8080/api/info
curl -i http://localhost:8082/api/info
```
Requests alternate between `backend-1`, `backend-2`, and `backend-3`.

### Step 2: Kill a Backend Node
Simulate an abrupt backend outage:
```bash
docker compose stop backend-2
```

### Step 3: Verify Uninterrupted Traffic Flow
```bash
for i in {1..10}; do curl -s http://localhost:8080/api/info | grep server_name; done
```
**Outcome**:
- The active health checker and etcd watcher immediately detect `backend-2` is down.
- All subsequent client traffic is transparently routed between `backend-1` and `backend-3`.
- Zero client requests fail (`0% error rate`).

### Step 4: Revive the Backend
```bash
docker compose start backend-2
```
**Outcome**:
- `backend-2` starts, re-registers its lease with `etcd:2379`.
- Both proxies receive the etcd PUT event and re-insert `backend-2` into their active routing pools within 1-2 seconds.

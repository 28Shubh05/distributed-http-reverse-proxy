# Admin API & PostgreSQL Control Plane Architecture

This document describes the administrative control plane, authentication mechanism, PostgreSQL persistence schema, runtime configuration management, and zero-downtime reload capabilities of the distributed HTTP reverse proxy.

---

## 1. Overview & Architectural Separation

To ensure security and operational stability, the reverse proxy maintains strict separation between:

- **Data Plane (`:8080`)**: High-performance HTTP reverse proxy routing client requests to upstream backends with connection pooling, caching, rate limiting, and circuit breaking.
- **Control Plane / Admin API (`:8081`)**: Dedicated management endpoints for operators to inspect health, query cache statistics, dynamically register/deregister backends, update configurations, and trigger zero-downtime pool reloads.

```
                    ┌────────────────────────────┐
                    │     Operator / DevOps      │
                    │   curl / CLI (cmd/admin)   │
                    └─────────────┬──────────────┘
                                  │ Authorization: Bearer <admin-token>
                                  ▼
                   ┌─────────────────────────────┐
                   │    Control Plane (:8081)    │
                   │ • Authentication Middleware │
                   │ • Backend Management API    │
                   │ • Configuration & Reload    │
                   └──────┬───────────────┬──────┘
                          │               │
            ┌─────────────┴──────┐ ┌──────┴──────────────┐
            ▼                    │ │                     ▼
┌───────────────────────┐        │ │       ┌──────────────────────────┐
│   PostgreSQL Storage  │        │ │       │    Reverse Proxy Data    │
│ • backends            │◄───────┘ └──────►│       Plane (:8080)      │
│ • configs             │                  │ • Load Balancer Pool     │
│ • routing_policies    │                  │ • Active Health Checker  │
│ • health_events       │                  │ • In-Memory/Redis Cache  │
└───────────────────────┘                  └──────────────────────────┘
```

---

## 2. Authentication & Security

Every request to the Admin API is guarded by authentication middleware:

1. **Token Transport**: Clients authenticate via standard header `Authorization: Bearer <token>` or custom header `X-Admin-Token: <token>`.
2. **Rejection**: Missing or invalid tokens immediately yield `401 Unauthorized`:
   ```json
   {
     "error": "unauthorized"
   }
   ```
3. **Network Isolation**: The admin port must not be exposed to the public internet. In production/Docker deployments, `:8081` is bound to internal management networks or localhost only.

---

## 3. Database Schema (PostgreSQL)

When `--postgres-url` is supplied, the proxy persists state across four tables (with automatic schema initialization):

### 1. `backends`
Stores upstream servers and traffic weights:
```sql
CREATE TABLE IF NOT EXISTS backends (
    id TEXT PRIMARY KEY,
    url TEXT NOT NULL,
    weight INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 2. `configs`
Stores dynamic runtime key-value parameters:
```sql
CREATE TABLE IF NOT EXISTS configs (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 3. `routing_policies`
Stores per-path routing preferences (e.g. round robin vs least connections):
```sql
CREATE TABLE IF NOT EXISTS routing_policies (
    path_prefix TEXT PRIMARY KEY,
    algorithm TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 4. `health_events`
Audit log of backend health transitions:
```sql
CREATE TABLE IF NOT EXISTS health_events (
    id BIGSERIAL PRIMARY KEY,
    backend_id TEXT NOT NULL,
    status TEXT NOT NULL,
    event_time TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    details TEXT NOT NULL DEFAULT ''
);
```

*Note: If PostgreSQL is unreachable or not specified, the system gracefully operates on a thread-safe in-memory store (`storage.MemoryStore`).*

---

## 4. API Reference

All requests require `Authorization: Bearer <token>`.

### Backends Management

#### `GET /admin/backends`
Lists all registered backends.
```http
HTTP/1.1 200 OK
Content-Type: application/json

{
  "backends": [
    {
      "id": "backend-1",
      "url": "http://localhost:9001",
      "weight": 1,
      "created_at": "2026-09-15T10:00:00Z",
      "updated_at": "2026-09-15T10:00:00Z"
    }
  ]
}
```

#### `POST /admin/backends`
Dynamically registers a backend node and adds it to the routing pool immediately.
```http
POST /admin/backends HTTP/1.1
Content-Type: application/json

{
  "id": "backend-3",
  "url": "http://localhost:9003",
  "weight": 2
}

HTTP/1.1 201 Created
```

#### `DELETE /admin/backends/{id}`
Removes a backend from persistence, the load balancer pool, and the active health checker.
```http
DELETE /admin/backends/backend-3 HTTP/1.1

HTTP/1.1 200 OK
{
  "status": "deleted",
  "id": "backend-3"
}
```

---

### Observability & Runtime

#### `GET /admin/stats`
Returns cache statistics (hits, misses, size, evictions) and timestamp.
```http
HTTP/1.1 200 OK
{
  "timestamp": "2026-09-15T12:00:00Z",
  "cache": {
    "hits": 1420,
    "misses": 88,
    "size": 75,
    "evictions": 0
  }
}
```

#### `GET /admin/health`
Returns backend health status summary.
```http
HTTP/1.1 200 OK
{
  "status": "UP",
  "backends": [
    {"id": "backend-1", "url": "http://localhost:9001", "weight": 1, "status": "HEALTHY"}
  ]
}
```

#### `GET /admin/events?limit=50`
Returns audit history of health check state changes.

---

### Configuration & Dynamic Reload

#### `GET /admin/config`
Retrieves runtime configuration entries.

#### `POST /admin/config`
Updates a runtime configuration setting.
```http
POST /admin/config HTTP/1.1
Content-Type: application/json

{
  "key": "cache_ttl",
  "value": "120s"
}
```

#### `POST /admin/reload`
Triggers an immediate reload of backend servers and configurations from storage into the live routing pool without restarting the proxy.
```http
POST /admin/reload HTTP/1.1

HTTP/1.1 200 OK
{
  "status": "reloaded",
  "message": "configuration and backend routing pool reloaded"
}
```

---

## 5. Standalone Admin CLI Tool (`cmd/admin`)

A dedicated CLI binary is provided for operators:

```bash
# Query proxy stats
./bin/admin.exe -mode client -admin-url http://localhost:8081 -token secret-admin-token -cmd stats

# Trigger dynamic reload
./bin/admin.exe -mode client -admin-url http://localhost:8081 -token secret-admin-token -cmd reload

# Query registered backends
./bin/admin.exe -mode client -admin-url http://localhost:8081 -token secret-admin-token -cmd backends
```

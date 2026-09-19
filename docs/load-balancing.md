# Load Balancing Algorithms

This document describes the three load balancing algorithms implemented in `internal/loadbalancer`, their selection trade-offs, consistent behaviour under backend failures, and interface design.

---

## 1. LoadBalancer Interface

All algorithms implement the same interface, making them interchangeable without touching the proxy core:

```go
type LoadBalancer interface {
    // SelectBackend picks the next backend for a request.
    // Returns nil if no healthy backend is currently available.
    SelectBackend() *Backend

    // AddBackend registers a new upstream backend in the pool.
    AddBackend(b *Backend)

    // RemoveBackend removes a backend from the pool by ID.
    RemoveBackend(id string)
}
```

A `Backend` carries the URL, health tracker, circuit breaker, and weight:

```go
type Backend struct {
    ID             string
    URL            *url.URL
    Weight         int
    Health         *health.BackendHealth
    CircuitBreaker *circuitbreaker.CircuitBreaker
}
```

`SelectBackend()` **filters** backends on every call:
- Skips backends marked **Unhealthy** by the health checker.
- Skips backends whose **circuit breaker is OPEN**.

---

## 2. Round-Robin

**How it works**: Routes requests evenly across backends in order, cycling back to the start.

```
Backends: [A, B, C]

Request 1 → A
Request 2 → B
Request 3 → C
Request 4 → A   ← wraps around
Request 5 → B
```

**Implementation**: An atomic uint64 counter is incremented on every request. The backend is selected as `counter % len(healthy_backends)`.

```go
idx := lb.counter.Add(1) % uint64(len(backends))
```

**Characteristics**:

| Property | Value |
|----------|-------|
| Distribution | Equal across all healthy backends |
| Complexity | O(1) selection |
| State | Per-instance atomic counter |
| Best for | Backends of identical capacity and performance |

---

## 3. Weighted Round-Robin

**How it works**: Backends receive requests in proportion to their `Weight` field.

```
Backends: [A(weight=3), B(weight=2), C(weight=1)]
Effective sequence: [A, A, A, B, B, C, A, A, A, B, B, C, ...]
```

**Implementation**: At construction time, an expanded slot list is built by repeating each backend `weight` times. Round-robin runs over this expanded list.

```go
// Weight=3 backend → [backend, backend, backend] in slot list
for i := 0; i < b.Weight; i++ {
    lb.slots = append(lb.slots, b)
}
```

**Characteristics**:

| Property | Value |
|----------|-------|
| Distribution | Proportional to backend weights |
| Complexity | O(1) selection, O(N×W) build |
| State | Atomic counter + slot list |
| Best for | Heterogeneous backends (different CPU/RAM capacity) |
| Limitation | Adding/removing backends requires slot list rebuild |

---

## 4. Least Connections

**How it works**: Routes each request to the backend currently handling the fewest active requests.

```
Before request:
  A: 10 active connections
  B:  3 active connections
  C:  7 active connections

→ Routes to B (fewest)

After request routed to B:
  B: 4 active connections
```

**Implementation**: Each backend has an atomic `activeConns` counter. On `SelectBackend`, iterate all healthy backends and pick the minimum. On response completion, decrement the counter.

```go
b.activeConns.Add(1)  // before forwarding
defer b.activeConns.Add(^uint64(0))  // after forwarding completes
```

**Characteristics**:

| Property | Value |
|----------|-------|
| Distribution | Inversely proportional to load |
| Complexity | O(N) selection |
| State | Per-backend atomic counter |
| Best for | Backends with variable latency or mixed request types |
| Advantage over RR | Prevents queuing on slow backends |

---

## 5. Health Filtering — Critical for Production

All three algorithms call the same filtering logic before selection:

```go
func isAvailable(b *Backend) bool {
    status := b.Health.Status()
    if status == health.StatusUnhealthy {
        return false
    }
    if b.CircuitBreaker.State() == circuitbreaker.Open {
        return false
    }
    return true
}
```

If **all** backends are unavailable, `SelectBackend()` returns `nil` and the proxy responds with **HTTP 503 Service Unavailable**.

---

## 6. Algorithm Selection Guide

| Scenario | Recommended Algorithm |
|----------|-----------------------|
| Homogeneous fleet, equal capacity | **Round-Robin** — simplest, no tuning required |
| Mixed capacity (e.g., 8-core vs 4-core) | **Weighted Round-Robin** — assign weights proportionally |
| Variable latency / long-running requests | **Least Connections** — avoids request queuing on slow nodes |
| Single backend | Any — all degrade gracefully |

---

## 7. Adding a Custom Algorithm

Implement the `LoadBalancer` interface and wire it in `cmd/proxy/main.go`:

```go
type RandomBalancer struct {
    mu       sync.RWMutex
    backends []*Backend
}

func (r *RandomBalancer) SelectBackend() *Backend {
    r.mu.RLock()
    defer r.mu.RUnlock()
    healthy := filterHealthy(r.backends)
    if len(healthy) == 0 {
        return nil
    }
    return healthy[rand.Intn(len(healthy))]
}
```

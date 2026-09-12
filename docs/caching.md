# HTTP Response Caching & Distributed Cache Architecture

This document describes the caching architecture, eviction algorithms, distributed storage strategies, and performance characteristics implemented in the distributed reverse proxy.

---

## 1. Overview & Architecture

Reverse proxy caching intercepts HTTP requests at the gateway tier, storing origin responses to serve subsequent identical requests without contacting upstream backend servers.

```
Client Request
      │
      ▼
┌───────────────────────────────────────────────┐
│ Caching Middleware                            │
│  1. Is method GET / HEAD?                     │
│  2. Are Auth/Cookie headers absent?           │
│  3. Cache-Control != no-cache?                │
└──────┬────────────────────────────────────────┘
       │
       ├─── [Cache Hit: X-Cache: HIT] ───────────────► Immediate Return (0.05 µs)
       │
       └─── [Cache Miss: X-Cache: MISS]
              │
              ▼
       ┌───────────────────────────────┐
       │ Forward to Backend via Pool   │
       └──────────────┬────────────────┘
                      │ Response 200 OK
                      ▼
       ┌───────────────────────────────┐
       │ Store in Cache (TTL applied)  │
       └───────────────────────────────┘
```

---

## 2. Caching Policies & HTTP Semantics

### Safe vs Non-Idempotent Methods

In accordance with RFC 7234 and RFC 9111:
- **Eligible for Caching**: `GET` and `HEAD` requests.
- **Strictly Ineligible**: `POST`, `PUT`, `PATCH`, and `DELETE`.
  - Mutating methods change state on the origin server. Caching them would result in stale data and duplicate mutations.

### Security & Header Hygiene

1. **Credential-Bearing Requests**:
   - Requests containing `Authorization` or `Cookie` headers are automatically bypassed (`X-Cache: BYPASS`). This prevents sensitive user-specific data from being cached and served to other users.
2. **Hop-by-Hop Header Stripping**:
   - Hop-by-hop headers (`Connection`, `Keep-Alive`, `Proxy-Authenticate`, `Transfer-Encoding`, `Upgrade`) pertain to a single transport connection and are stripped prior to caching.
3. **Cache-Control Respect**:
   - Requests carrying `Cache-Control: no-cache` or `Pragma: no-cache` bypass the cache, forcing fresh data retrieval from the origin.

---

## 3. Storage Implementations

The proxy provides two swappable implementations under the unified `cache.Cache` interface:

### 1. In-Memory LRU Cache (`MemoryCache`)

- Built using a doubly linked list (`container/list`) and hash map for $O(1)$ reads, writes, and evictions.
- **LRU Eviction**: When `cache-size` is reached, the least-recently-used entry is evicted to prevent memory exhaustion.
- **Thread Safety**: Protected with a `sync.Mutex` and atomic counters for lock-free metrics reading.
- **Ideal For**: Single-instance proxy setups, edge nodes, and testing without Redis dependencies.

### 2. Distributed Redis Cache (`RedisCache`)

- Backed by Redis using `GET`, `SET` with TTL, and `SCAN`/`DEL` for invalidation.
- **Shared State**: Enables horizontally scaled proxies to share a single unified cache tier. If Proxy A caches a response, Proxy B immediately benefits from the cache hit.
- **Fail-Safe Fallback**: If Redis becomes unreachable or times out, the cache safely records a miss and falls through to the origin backend rather than returning an error to the client.

---

## 4. Cache Invalidation Strategies

Stale data is the primary challenge in caching systems. The proxy employs three invalidation mechanisms:

1. **Time-To-Live (TTL)**:
   - Configurable TTL (default: 60s). Entries automatically expire when their TTL elapses.
2. **Mutation-Based Invalidation**:
   - When a successful mutating request (`POST`, `PUT`, `DELETE`, or `PATCH`) completes for a URI (e.g. `POST /api/products`), the proxy automatically invalidates the cached `GET` and `HEAD` entries for that URI.
3. **Manual / Programmatic Invalidation**:
   - `c.Delete(key)` and `c.Flush()` methods allow administrative purge via APIs or cache clearing hooks.

---

## 5. Performance Benchmarks

Benchmarks were performed comparing direct backend execution (cache disabled) versus serving cached responses (cache enabled):

```
cpu: 12th Gen Intel(R) Core(TM) i5-12450HX
BenchmarkCache_Disabled-12               21,051    108,633 ns/op    6,012 B/op    70 allocs/op
BenchmarkCache_Enabled-12            61,941,237      49.54 ns/op        0 B/op     0 allocs/op
BenchmarkCache_Enabled_Parallel-12   23,605,922     103.30 ns/op        0 B/op     0 allocs/op
```

### Key Findings

| Metric | Cache Disabled (Direct Backend) | Cache Enabled (Memory Hit) | Improvement |
| :--- | :--- | :--- | :--- |
| **Latency per Request** | `108.6 µs` | `0.049 µs` (49.5 ns) | **~2,190x faster** |
| **Throughput (Parallel)** | ~9,200 req/sec | ~9,700,000 req/sec | **~1,050x increase** |
| **Memory Allocations** | `70 allocs/op` (6 KB) | **`0 allocs/op` (0 B)** | **Zero allocation** |

---

## 6. Observability & Headers

Every response processed by the proxy includes observability headers:
- `X-Cache: HIT`: Request was served directly from cache without hitting any backend.
- `X-Cache: MISS`: Request was not in cache; fetched from backend and stored.
- `X-Cache: BYPASS`: Request had mutating method, auth headers, or `no-cache` directive.
- Proxy logs aggregate cache performance on graceful shutdown:
  ```json
  {"level":"INFO","msg":"cache metrics on shutdown","hits":24589,"misses":1102,"size":450,"evictions":12}
  ```

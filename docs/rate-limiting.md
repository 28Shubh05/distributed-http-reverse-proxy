# Rate Limiting & Distributed Token Bucket Architecture

This document details the rate limiting algorithms, in-memory and distributed Redis implementations, HTTP semantics, and operational design patterns implemented in the distributed reverse proxy.

---

## 1. Overview & Motivation

Rate limiting is an essential defense mechanism at the gateway tier. Without rate limiting, a reverse proxy and its upstream backend services are vulnerable to:
- **Denial of Service (DoS)** attacks and brute-force credential stuffing.
- **Cascading Failures**: A traffic surge can overwhelm connection pools and crash backend services.
- **Noisy Neighbor Problems**: A single aggressive or misconfigured client consumes disproportionate resources, degrading performance for legitimate users.

---

## 2. Algorithm Comparison

| Algorithm | Burst Handling | Memory Overhead | Time Accuracy | Best Use Case |
| :--- | :--- | :--- | :--- | :--- |
| **Token Bucket (Selected)** | **Excellent (up to capacity)** | **$O(1)$ per client** | **High (sub-millisecond)** | **APIs & Web Gateways** |
| **Leaky Bucket** | Poor (fixed output rate) | $O(1)$ per client | High | Packet smoothing / queues |
| **Fixed Window Counter** | Dangerous ($2\times$ burst at window edge) | $O(1)$ per client | Low | Simple hourly/daily quotas |
| **Sliding Window Log** | Good | $O(N)$ requests (High) | Precise | Low-volume sensitive actions |
| **Sliding Window Counter** | Good | $O(1)$ per client | Approximate | General high-throughput APIs |

### Why Token Bucket?

The **Token Bucket** algorithm was selected because:
1. **Bursts with Bound**: Clients can burst up to the bucket's `burst` (capacity) instantly, which is common for legitimate web browsing and page asset loading.
2. **Deterministic Long-Term Rate**: Sustained throughput is strictly capped at `rate` tokens per second.
3. **Constant Time & Memory**: State requires only two numbers per client: `tokens` (float64) and `last_updated` (timestamp), giving $O(1)$ space and $O(1)$ compute complexity.

---

## 3. Mathematical Model & Refill Mechanics

Each client is assigned a token bucket governed by:
- $C$: Maximum capacity (burst size).
- $R$: Refill rate in tokens per second.
- $t_{\text{last}}$: Timestamp of the previous request.
- $t_{\text{now}}$: Timestamp of the current request.
- $\Delta t = t_{\text{now}} - t_{\text{last}}$: Elapsed time in seconds.

### Token Calculation
$$\text{tokens}_{\text{current}} = \min(C, \, \text{tokens}_{\text{previous}} + \Delta t \times R)$$

### Decision Logic
- If $\text{tokens}_{\text{current}} \ge 1$:
  $$\text{tokens}_{\text{remaining}} = \text{tokens}_{\text{current}} - 1$$
  Request is **allowed**.
- If $\text{tokens}_{\text{current}} < 1$:
  $$\text{Retry-After} = \left\lceil \frac{1 - \text{tokens}_{\text{current}}}{R} \right\rceil \text{ seconds}$$
  Request is **rejected** with HTTP `429 Too Many Requests`.

---

## 4. Architectural Implementations

The proxy provides two implementations under the unified `ratelimit.Limiter` interface:

```
                  Incoming Request
                         │
                         ▼
             ┌───────────────────────┐
             │ Client Key Extractor  │
             │ (X-Forwarded-For, IP) │
             └───────────┬───────────┘
                         │
           ┌─────────────┴─────────────┐
           ▼                           ▼
  [In-Memory Limiter]        [Redis Distributed Limiter]
  • Per-instance map         • Shared across proxy cluster
  • Zero external dependency • Atomic Lua script
  • Auto-reclaiming GC       • Automatic in-memory fallback
           │                           │
           └─────────────┬─────────────┘
                         │
        ┌────────────────┴────────────────┐
        │                                 │
 [Allowed: Tokens >= 1]         [Rejected: Tokens < 1]
        │                                 │
        ▼                                 ▼
 Set X-RateLimit-* headers        Set 429 Status Code
 Forward to Cache / Proxy         Set Retry-After header
                                  Return JSON Error Body
```

### 1. In-Memory Token Bucket (`TokenBucketLimiter`)
- Uses a thread-safe map protected by a `sync.RWMutex` with fine-grained per-bucket `sync.Mutex` locks.
- **Garbage Collection**: A background loop sweeps client buckets that have been idle for $> 5$ minutes, preventing memory leaks caused by transient or bot IP addresses.

### 2. Distributed Redis Rate Limiter (`RedisLimiter`)
- Allows multiple proxy instances in a cluster to share the exact same rate limit counter for each client IP.
- **Atomic Lua Script**:
  - Executes token refill, deduction, and update directly in Redis memory in a single atomic operation.
  - Eliminates network round-trips and distributed race conditions.
  - Automatically sets key TTL to $2 \times (C / R)$ so inactive client keys expire automatically from Redis.
- **Fail-Safe Fallback**: If Redis becomes unreachable, the proxy automatically delegates to an in-memory `TokenBucketLimiter`, ensuring the proxy never crashes or blocks traffic due to Redis network outages.

---

## 5. HTTP Protocol & Header Standards

### Standard Observability Headers

Every request processed by the rate limiting middleware receives standard rate limit headers:

| Header | Description | Example |
| :--- | :--- | :--- |
| `X-RateLimit-Limit` | Maximum allowed burst capacity (tokens) | `100` |
| `X-RateLimit-Remaining` | Remaining tokens in the bucket after this request | `42` |
| `X-RateLimit-Reset` | Approximate seconds until the bucket is completely full | `2` |
| `Retry-After` *(on 429 only)* | Number of seconds the client must wait before retrying | `1` |

### HTTP 429 Response Format
```http
HTTP/1.1 429 Too Many Requests
Content-Type: application/json
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 2
Retry-After: 1

{"error":"too many requests","retry_after":1}
```

---

## 6. Client Identification & Security

Client keys are extracted in order of priority:
1. `X-Forwarded-For`: Parses the first (origin client) IP address when running behind trusted upstream load balancers / CDNs.
2. `X-Real-IP`: Uses upstream gateway-provided client IP if present.
3. `RemoteAddr`: Falls back to the TCP socket peer IP (stripping the port number).

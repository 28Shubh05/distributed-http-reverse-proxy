# Reliability & Failure Handling in Distributed Reverse Proxies

This document details the reliability architecture, fault tolerance mechanisms, and failure handling strategies implemented in the distributed reverse proxy and intelligent load balancer.

---

## 1. Overview of Defense-in-Depth Architecture

When upstream backend services degrade, crash, or slow down, naive proxying leads to cascading failure across the entire system. To achieve high availability and fault isolation, the proxy employs a multi-tiered defense:

```
Incoming Request
       │
       ▼
┌───────────────────────────────────────────────┐
│ 1. Load Balancer Gatekeeper                   │
│    - Active Health Checks (proactive poll)    │
│    - Circuit Breakers (per-backend tripwire)  │
│    - Connection Pool limits                   │
└──────────────────────┬────────────────────────┘
                       │ Selects healthy backend
                       ▼
┌───────────────────────────────────────────────┐
│ 2. Execution & Observability                  │
│    - Per-request backend timeout              │
│    - Passive Failure Detection (5xx / errors) │
└──────────────────────┬────────────────────────┘
                       │ Fails?
                       ▼
┌───────────────────────────────────────────────┐
│ 3. Intelligent Retries                        │
│    - Idempotency validation (GET/HEAD only)   │
│    - Exponential backoff + Full Jitter        │
│    - Failover to alternate healthy backend    │
│    - Bounded attempt ceilings                 │
└───────────────────────────────────────────────┘
```

---

## 2. Circuit Breaker Pattern

A **Circuit Breaker** isolates individual degraded backends to fail fast, preserving proxy goroutines, sockets, and CPU cycles, while giving the struggling backend breathing room to recover.

### States & Transitions

```
              ┌───────────────────────────┐
              │          CLOSED           │ ◄─────────────────────────┐
              │  (Traffic flows normally) │                           │
              └─────────────┬─────────────┘                           │
                            │                                         │
                            │ consecutiveFailures >= FailureThreshold │
                            ▼                                         │
              ┌───────────────────────────┐                           │
              │           OPEN            │                           │
              │ (Fail fast, drop requests)│                           │
              └─────────────┬─────────────┘                           │
                            │                                         │
                            │ OpenTimeout elapsed                     │
                            ▼                                         │
              ┌───────────────────────────┐                           │
              │         HALF-OPEN         │                           │
              │  (Rate-limited probes)    │ ── successes >= Threshold ┘
              └─────────────┬─────────────┘
                            │
                            │ any trial failure
                            ▼
                         (Back to OPEN)
```

1. **`CLOSED`**:
   - All requests are permitted to pass to the backend.
   - Successful requests reset consecutive failure counts.
   - If consecutive transport errors or 5xx responses reach `FailureThreshold`, the breaker trips to `OPEN`.

2. **`OPEN`**:
   - The backend is deemed unhealthy. All requests are immediately rejected without attempting network I/O (`Allow() == false`).
   - The load balancer skips this backend during selection.
   - After `OpenTimeout` has elapsed, the breaker transitions to `HALF_OPEN`.

3. **`HALF_OPEN` (Thundering Herd Prevention)**:
   - Rather than allowing all queued requests to stampede the recovering backend simultaneously, the breaker only permits up to `HalfOpenMaxRequests` concurrent trial requests.
   - Any excess requests are rejected or routed to alternative backends.
   - If **any** trial request fails, the breaker immediately trips back to `OPEN` for another timeout cycle.
   - If consecutive trials reach `SuccessThreshold`, the backend is declared healthy and transitions back to `CLOSED`.

---

## 3. Idempotency-Aware Safe Retries

Retrying requests blindly can cause severe bugs (such as duplicate payments or database corruptions) and amplify outages.

### Idempotency Principles (RFC 7231 / RFC 9110)

- **Idempotent Methods (`GET`, `HEAD`, `OPTIONS`, `TRACE`)**:
  - Making $N$ identical requests produces the exact same side-effects on the server as making 1 request.
  - These methods are **safe to retry** automatically on transient failures.
- **Non-Idempotent Methods (`POST`, `PATCH`)**:
  - Executing a `POST /api/orders` request twice could create two separate orders or double-charge a customer.
  - The proxy **strictly disallows retries** on `POST` and `PATCH` requests by default.
- **Conditionally Idempotent Methods (`PUT`, `DELETE`)**:
  - `PUT` and `DELETE` are semantically idempotent in REST specifications, but in real-world systems can have non-idempotent side effects. Configured off by default and enabled only when upstream APIs guarantee idempotency.

### Retriable Failure Criteria

The proxy only retries under transient circumstances:
1. **Transport Errors**: Connection refused, dial timeout, connection reset by peer, or broken pipes.
2. **Transient Upstream HTTP Status Codes**:
   - `502 Bad Gateway`
   - `503 Service Unavailable`
   - `504 Gateway Timeout`
3. **Non-Retriable Scenarios**:
   - Client errors (`400 Bad Request`, `401 Unauthorized`, `404 Not Found`, `422 Unprocessable Entity`).
   - Server business logic errors (e.g. `500 Internal Server Error` caused by invalid application state).
   - Client cancellations (`context.Canceled`).

---

## 4. Why Retries Can Worsen Outages ("Retry Storms")

In distributed systems, naive retries are often the primary cause of catastrophic, system-wide collapse:

```
High Traffic / Database Hiccup
       │
       ▼
Backend Latency Spikes (e.g. from 20ms to 2000ms)
       │
       ▼
Proxies Time Out at 1000ms & Immediately Retry
       │
       ▼
Backend Traffic Multiplies by 2x – 4x
       │
       ▼
Backend Exhausts Connection Pool / Threads / CPU
       │
       ▼
COMPLETE CASCADING OUTAGE (System cannot recover without manual traffic shedding)
```

### Mitigations Implemented in the Proxy

1. **Exponential Backoff**:
   - Delays between retries grow exponentially:
     $$d_k = \min(\text{MaxBackoff}, \text{InitialBackoff} \times \text{Multiplier}^k)$$
   - Prevents pounding the upstream server with immediate retries.

2. **Full Randomized Jitter**:
   - If hundreds of clients experience a timeout simultaneously, fixed exponential backoff would cause all clients to retry at the exact same synchronized intervals (harmonic spikes).
   - By applying randomized jitter ($\pm 25\%$ variation), retry attempts are decorrelated and spread evenly across the time domain:
     $$\text{delay} = d_k \times (1.0 + \text{rand}(-1, 1) \times \text{JitterFraction})$$

3. **Strict Bounded Ceilings**:
   - Maximum retry attempts are capped (default: 2 retries).
   - Maximum backoff duration is capped (default: 2 seconds).

4. **Context Cancellation Propagation**:
   - If a client closes their browser tab or cancels their HTTP connection, the proxy immediately aborts backoff sleep and drops the request, avoiding wasted upstream computation.

---

## 5. Summary of Reliability Layers

| Layer | Trigger | Scope | Action |
| :--- | :--- | :--- | :--- |
| **Active Health Check** | Periodic timer (`GET /health`) | Out-of-band | Marks backend `UNHEALTHY` or `RECOVERING`. |
| **Passive Failure Detection** | Real request 5xx or transport error | Per-request | Increments failure streaks; trips backend health. |
| **Circuit Breaker** | Consecutive request failures | Per-backend | Trips `OPEN` to fail fast; limits concurrency in `HALF_OPEN`. |
| **Retry Policy** | Idempotent request failure | Per-request | Backoff + Jitter retry on an alternate healthy backend. |
| **Connection Pool** | Socket idle timeout / max conns | TCP Transport | Reuses persistent TCP connections; cleans stale sockets. |

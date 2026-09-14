# Service Discovery with etcd & Dynamic Backend Pool Architecture

This document describes the etcd-backed service discovery system, dynamic backend registration, lease keepalive mechanism, watch streaming, and zero-downtime pool updates implemented in the distributed reverse proxy.

---

## 1. Overview & Motivation

In traditional static reverse proxy setups, backend server addresses are hardcoded into configuration files or CLI flags. Adding, removing, or replacing backend servers requires editing configuration and restarting the proxy.

In dynamic cloud-native environments (Kubernetes, AWS Auto Scaling, Docker Swarm), backends constantly scale up, scale down, crash, or redeploy with new ephemeral IP addresses.

**Service discovery** solves this by establishing a decentralized registry where:
- Backends automatically announce their presence upon starting up.
- Backends maintain a heartbeat lease while running.
- Proxies watch the registry in real time and adapt their routing pools instantaneously **without restarting or dropping in-flight requests**.

---

## 2. Architecture & Components

```
                    ┌────────────────────────────┐
                    │     etcd Consensus Tier    │
                    │   /proxy/backends/<id>     │
                    └─────────────┬──────────────┘
                                  │
         ┌────────────────────────┼────────────────────────┐
         │ Put (Lease TTL: 15s)   │ Persistent gRPC Watch  │ Delete on Stop
         │ Keepalive Heartbeat    │ EventStream            │ Lease Expiry on Crash
         ▼                        ▼                        ▼
 ┌───────────────┐        ┌───────────────┐        ┌───────────────┐
 │   Backend 1   │        │ Reverse Proxy │        │   Backend 2   │
 │  (Registers)  │        │ (Watcher/Sync)│        │ (Deregisters) │
 └───────────────┘        └───────┬───────┘        └───────────────┘
                                  │
                                  ▼
                   ┌─────────────────────────────┐
                   │    LoadBalancer Pool        │
                   │ • AddBackend(backend)       │
                   │ • RemoveBackend(backendId)  │
                   │ • Zero-Downtime Hot Updates │
                   └─────────────────────────────┘
```

The system comprises three core layers:

1. **Registry (`internal/discovery/registry.go`)**:
   - Client wrapper over `go.etcd.io/etcd/client/v3`.
   - Handles `Register` with lease grant and background keepalive loop.
   - Handles `Deregister` and key deletion.
   - Provides prefix-based `List` and gRPC streaming `Watch`.

2. **Watcher (`internal/discovery/watcher.go`)**:
   - Bridge connecting the discovery registry with the proxy's `LoadBalancer` and active `health.Checker`.
   - Executes an initial synchronization (`List`), populating the load balancer pool.
   - Continuously consumes `EventAdded` and `EventRemoved` notifications, dynamically updating routing tables.

3. **Backend Agent (`cmd/backend/main.go`)**:
   - Self-registers its ID, reachable URL, and weight with etcd on startup.
   - Renews lease heartbeats every few seconds.
   - Gracefully deregisters from etcd upon receiving SIGINT / SIGTERM before shutting down HTTP listeners.

---

## 3. Data Model & Keyspace Schema

Backends are registered under a hierarchical prefix:
```
/proxy/backends/<backend-id>
```

Each key stores a serialized JSON `BackendEntry`:
```json
{
  "id": "backend-1",
  "url": "http://10.0.0.1:9001",
  "weight": 5
}
```

- `id`: Unique identifier for the instance.
- `url`: Network address the proxy will forward requests to.
- `weight`: Traffic weight utilized by weighted load balancing algorithms.

---

## 4. Lease & Heartbeat Mechanics (Dead Man's Switch)

Backends register with a time-to-live lease (default: 15 seconds):

1. **Active Heartbeats**: While healthy, the backend's keepalive goroutine streams periodic heartbeats to etcd, refreshing the lease.
2. **Graceful Deregistration**: On planned shutdown, the backend immediately calls `Deregister`, deleting its key and triggering an immediate `EventRemoved` event.
3. **Crash Protection (Dead Man's Switch)**: If a backend node crashes or experiences a network partition, heartbeats stop. After 15 seconds, etcd automatically expires the lease and purges the key. The proxy receives an `EventRemoved` event and stops routing traffic to the dead backend.

---

## 5. Zero-Downtime Dynamic Updates

When the proxy receives discovery events:

- **`EventAdded`**:
  1. Parses and validates the backend URL.
  2. Allocates a dedicated `circuitbreaker.CircuitBreaker` and `health.BackendHealth`.
  3. Registers the backend with the active `health.Checker`.
  4. Invokes `lb.AddBackend()`, making the node immediately eligible for load balancing.
- **`EventRemoved`**:
  1. Invokes `lb.RemoveBackend(id)`, atomically removing the backend from the routing slice.
  2. Deregisters the backend from the active `health.Checker`.
  3. Existing in-flight requests complete normally; new requests skip the removed node.

---

## 6. Verification & Lifecycle Testing

The system is tested for full lifecycle transitions:

- **Start Backend**: Dynamic registration triggers `EventAdded`, immediately adding the node to the load balancer pool.
- **Stop Backend**: Deregistration triggers `EventRemoved`, safely isolating traffic to surviving nodes.
- **Restart Backend**: Node re-registers with etcd, triggers `EventAdded`, and is restored to active rotation seamlessly.

---

## 7. Configuration & CLI Flags

### Reverse Proxy
```bash
# Run proxy with dynamic etcd discovery
./bin/proxy.exe -port 8080 -etcd-endpoints localhost:2379
```

### Backend Servers
```bash
# Backend 1 auto-registers with etcd
./bin/backend.exe -port 9001 -name backend-1 -etcd-endpoints localhost:2379 -weight 2

# Backend 2 auto-registers with etcd
./bin/backend.exe -port 9002 -name backend-2 -etcd-endpoints localhost:2379 -weight 1
```

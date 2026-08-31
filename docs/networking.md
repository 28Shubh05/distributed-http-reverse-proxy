# Comprehensive Networking Concepts for Distributed HTTP Reverse Proxy

> **Target Audience:** SDE / Systems Engineering Candidates building a production-grade Distributed Reverse Proxy & Intelligent Load Balancer in Go.  
> **Document Scope:** Foundational through advanced computer networking principles, transport mechanics, application-layer protocols, and system-level performance optimizations required to design, implement, and debug high-performance reverse proxies.

---

## Table of Contents

1. [OSI Model & Layer Decomposition](#1-osi-model--layer-decomposition)
2. [TCP/IP Stack & Transport Foundations](#2-tcpip-stack--transport-foundations)
3. [TCP 3-Way Handshake & Connection Establishment](#3-tcp-3-way-handshake--connection-establishment)
4. [TCP Connection Termination & The TIME_WAIT State](#4-tcp-connection-termination--the-time_wait-state)
5. [Ports, Sockets, and File Descriptors](#5-ports-sockets-and-file-descriptors)
6. [Domain Name System (DNS) & Service Discovery](#6-domain-name-system-dns--service-discovery)
7. [HTTP/1.1 Protocol Architecture](#7-http11-protocol-architecture)
8. [Essential HTTP Headers in Reverse Proxying](#8-essential-http-headers-in-reverse-proxying)
9. [HTTP Keep-Alive & Persistent Connections](#9-http-keep-alive--persistent-connections)
10. [Connection Pooling & Pool Lifecycle Management](#10-connection-pooling--pool-lifecycle-management)
11. [Network Timeouts & Failure Modes](#11-network-timeouts--failure-modes)
12. [TLS/SSL & Edge Termination Architecture](#12-tlsssl--edge-termination-architecture)
13. [Reverse Proxy vs. Forward Proxy Architecture](#13-reverse-proxy-vs-forward-proxy-architecture)
14. [L4 vs. L7 Load Balancing](#14-l4-vs-l7-load-balancing)
15. [Network Address Translation (NAT) & Container Networking](#15-network-address-translation-nat--container-networking)
16. [Connection Reuse: Combining Keep-Alive & Pooling for Maximum Throughput](#16-connection-reuse-combining-keep-alive--pooling-for-maximum-throughput)
17. [SDE Interview Rapid-Fire Cheat Sheet](#17-sde-interview-rapid-fire-cheat-sheet)

---

## 1. OSI Model & Layer Decomposition

### What It Is

The **Open Systems Interconnection (OSI)** model is a conceptual 7-layer framework standardized by ISO that standardizes communication functions in telecommunication and computing systems. Rather than viewing network communication as an indivisible black box, the model decomposes data flow into distinct layers where each layer serves the layer above it and is served by the layer below it through well-defined interfaces (encapsulation and decapsulation).

```
+-------------------------------------------------------------------------+
|                        OSI 7-LAYER REFERENCE MODEL                      |
+---+----------------------+-----------------------------+----------------+
| # | Layer Name           | Primary Responsibility      | Data Unit (PDU)|
+---+----------------------+-----------------------------+----------------+
| 7 | Application Layer    | HTTP, gRPC, DNS, TLS        | Message / Data |
| 6 | Presentation Layer   | Data formats, Encryption    | Message / Data |
| 5 | Session Layer        | Session mgmt, RPC sockets   | Message / Data |
| 4 | Transport Layer      | TCP, UDP (Port Addressing)  | Segment / DG   |
| 3 | Network Layer        | IPv4, IPv6 (IP Addressing)  | Packet         |
| 2 | Data Link Layer      | Ethernet, MAC addressing    | Frame          |
| 1 | Physical Layer       | Bits over copper/fiber/air  | Bit            |
+---+----------------------+-----------------------------+----------------+
```

In modern networking and cloud architectures, layers 5, 6, and 7 are collapsed into the **Application Layer**, while our reverse proxy operates primarily across three critical layers:

```
                  PACKET ENCAPSULATION / DECAPSULATION
                  
      Client                                                 Reverse Proxy
+------------------+                                      +------------------+
| Layer 7: HTTP    |  [HTTP Payload: GET /api/v1/users]   | Layer 7: HTTP    |
| (Application)    |                                      | (Parser/Router)  |
+------------------+                                      +------------------+
        | Encapsulate                                              ^ Decapsulate
        v                                                          |
+------------------+                                      +------------------+
| Layer 4: TCP     |  [TCP Header | Port 443 -> 52314]    | Layer 4: TCP     |
| (Transport)      |  [ + HTTP Payload              ]    | (Socket Buffer)  |
+------------------+                                      +------------------+
        | Encapsulate                                              ^ Decapsulate
        v                                                          |
+------------------+                                      +------------------+
| Layer 3: IP      |  [IP Header  | 192.0.2.1 -> 10.0.0.1]| Layer 3: IP      |
| (Network)        |  [ + TCP Header + HTTP Payload ]    | (Kernel Routing) |
+------------------+                                      +------------------+
```

### The Three Critical Layers for Reverse Proxies

1. **Layer 3 (Network Layer - IP):**
   * **Unit:** Packet.
   * **Function:** Logical addressing (IPv4/IPv6) and end-to-end packet routing across intermediate routers.
   * **Proxy Relevance:** The proxy reads the client's source IP for security policies (IP whitelisting/blacklisting, geographic routing, rate limiting per IP).
2. **Layer 4 (Transport Layer - TCP/UDP):**
   * **Unit:** Segment (TCP) / Datagram (UDP).
   * **Function:** Process-to-process communication via **ports**, multiplexing, stream segmentation, reliable transmission, flow control, and congestion control.
   * **Proxy Relevance:** The proxy establishes and terminates TCP sockets, manages socket read/write buffers, and implements connection pooling to upstream servers.
3. **Layer 7 (Application Layer - HTTP/HTTPS/gRPC):**
   * **Unit:** Message (Request/Response stream).
   * **Function:** Application-level semantic understanding: HTTP methods, URI paths, query parameters, headers, cookies, chunked bodies, and status codes.
   * **Proxy Relevance:** The reverse proxy completely parses incoming HTTP streams, evaluates routing rules (e.g., path `/api/v1/payments` -> Payment Service), modifies headers (`X-Forwarded-For`), performs caching, and handles TLS encryption/decryption.

### Why It Matters For This Project Specifically

Our reverse proxy is an **L7 Application Proxy**. It does not merely forward raw IP packets or TCP segments at the kernel level (like an L4 router or Linux IPVS). Instead, it:
* Accepts an incoming L4 TCP connection on port `80` or `443`.
* Decapsulates the TCP stream into L7 HTTP request bytes.
* Parses and inspects the HTTP headers and path.
* Rewrites headers, verifies authentication tokens, checks rate limits.
* Establishes a *second, distinct* L4 TCP connection (or reuses an existing pooled one) to an upstream backend server.
* Encapsulates a newly synthesized HTTP request and transmits it upstream.

### Key Interview Points

* **Encapsulation Overhead:** Every layer prepends metadata (headers). A standard Ethernet frame adds 14 bytes, IPv4 adds 20 bytes, TCP adds 20-60 bytes.
* **L4 vs. L7 Boundary:** L4 proxies cannot inspect URL paths or cookies because those exist strictly inside the L7 payload. L7 proxies must pay the CPU cost of stream assembly, parsing, and re-serialization.
* **Where does TLS live?** Conceptually at Layer 6 (Presentation), but in the TCP/IP stack it runs inside the Application Layer, wrapping Layer 7 protocols like HTTP (creating HTTPS on L4 port 443).

---

## 2. TCP/IP Stack & Transport Foundations

### What It Is

The **TCP/IP Model** (Internet Protocol Suite) is the practical 4-layer standard that powers the global internet, mapping directly to real-world networking implementations.

```
+--------------------+------------------------------------+--------------------+
| TCP/IP Layer       | Equivalent OSI Layers              | Core Protocols     |
+--------------------+------------------------------------+--------------------+
| 4. Application     | Application, Presentation, Session | HTTP, gRPC, DNS,TLS|
| 3. Transport       | Transport                          | TCP, UDP, QUIC     |
| 2. Internet        | Network                            | IPv4, IPv6, ICMP   |
| 1. Network Access  | Data Link, Physical                | Ethernet, Wi-Fi    |
+--------------------+------------------------------------+--------------------+
```

### Why TCP Matters for HTTP

HTTP/1.1 and HTTP/2 rely exclusively on **TCP (Transmission Control Protocol)** because web traffic requires strict data integrity and ordered delivery. A dropped byte in an HTML file, JSON response, or binary image would corrupt the application state.

```
+-----------------------------------------------------------------------------+
|                               TCP HEADER FORMAT                             |
+------------------------------------+----------------------------------------+
| 0               15                 | 16                                  31 |
+------------------------------------+----------------------------------------+
|       Source Port (16 bits)        |      Destination Port (16 bits)        |
+------------------------------------+----------------------------------------+
|                        Sequence Number (32 bits)                            |
+-----------------------------------------------------------------------------+
|                     Acknowledgment Number (32 bits)                         |
+------+-------+---------------------+----------------------------------------+
|Offset| Rsrvd |Flags:S|A|P|R|F|...  |          Window Size (16 bits)         |
+------+-------+---------------------+----------------------------------------+
|       Checksum (16 bits)           |        Urgent Pointer (16 bits)        |
+------------------------------------+----------------------------------------+
|                    Options (Variable 0 - 320 bits)                          |
+-----------------------------------------------------------------------------+
```

### Core TCP Guarantees

1. **Reliability (ACKs & Retransmission):** Every transmitted segment must be acknowledged by the receiver. If an ACK is not received before the retransmission timeout (RTO), TCP re-sends the packet.
2. **In-Order Byte Stream Delivery:** TCP tracks bytes via 32-bit `Sequence Numbers`. Out-of-order packets arriving at the network interface are held in the TCP receive buffer until missing packets arrive.
3. **Flow Control (Sliding Window):** The receiver advertises its available buffer space (`Window Size`). The sender never transmits more bytes than the receiver can store, preventing fast producers from overwhelming slow consumers.
4. **Congestion Control (AIMD / CUBIC / BBR):** Senders dynamically detect network congestion (packet drops or rising RTT) and adjust their `Congestion Window` (`cwnd`) to avoid collapsing the shared network infrastructure.
5. **Connection-Oriented:** A logical point-to-point circuit is established before any data transfer begins (via the 3-Way Handshake) and cleanly torn down (via the 4-Way Handshake).

### Why It Matters For This Project Specifically

In Go, the `net.Listener` and `net.Conn` interfaces wrap kernel TCP sockets. Because our reverse proxy bridges **two separate TCP connections** (Client <-> Proxy and Proxy <-> Backend), TCP behavior directly dictates proxy throughput and latency:
* **Nagle's Algorithm (`TCP_NODELAY`):** By default, Nagle's algorithm buffers small outgoing packets to combine them into full MSS (Maximum Segment Size) frames. In interactive HTTP proxies, this introduces artificial 40ms delays. Go disables Nagle's algorithm by default (`TCP_NODELAY=true`) on `net.TCPConn` to minimize request latency.
* **Head-of-Line (HoL) Blocking:** If one TCP packet drops between the proxy and backend, the entire TCP stream stalls until that packet is retransmitted—affecting all multiplexed or pipelined requests on that connection.

### Key Interview Points

* **Byte Stream vs. Message Stream:** TCP has no concept of "records" or "HTTP messages". It delivers a continuous, boundary-less stream of raw bytes. The application layer (HTTP parser) is strictly responsible for finding where one request ends and the next begins (using `Content-Length` or `\r\n\r\n`).
* **MTU vs. MSS:** Maximum Transmission Unit (MTU) is typically 1500 bytes on standard Ethernet. Subtracting 20 bytes of IP header and 20 bytes of TCP header leaves the Maximum Segment Size (MSS) of **1460 bytes** for payload data.

---

## 3. TCP 3-Way Handshake & Connection Establishment

### What It Is

Before any HTTP request can be transmitted over TCP, the client and server must establish synchronization, exchange initial sequence numbers (ISN), and negotiate TCP options (like Window Scaling and MSS). This is done through the **3-Way Handshake**.

```
       CLIENT                                          SERVER
 (State: CLOSED)                                  (State: LISTEN)
        |                                                |
        |  1. SYN (Seq = X, Flags=[SYN])                 |
        |----------------------------------------------->|  Server allocates TCB,
        |                                                |  places in SYN Queue
        |                                                |  (State: SYN_RCVD)
        |  2. SYN-ACK (Seq = Y, Ack = X+1, Flags=[SYN,ACK])
        |<-----------------------------------------------|
        |                                                |
Client allocates TCB,                                    |
(State: ESTABLISHED)                                     |
        |  3. ACK (Seq = X+1, Ack = Y+1, Flags=[ACK])    |
        |----------------------------------------------->|  Server moves to
        |                                                |  Accept Queue
        |                                                |  (State: ESTABLISHED)
        |                                                |
        | === 3-Way Handshake Complete: 1 RTT Latency == |
        |                                                |
        |  4. HTTP Request (GET /api/v1/resource)        |
        |----------------------------------------------->|
```

### Handshake Progression Step-by-Step

1. **SYN (Synchronize):** The client selects a random 32-bit Initial Sequence Number ($X$) and transmits a segment with the `SYN` control bit set. Server state: `SYN_RCVD`.
2. **SYN-ACK:** The server acknowledges receipt of $X$ by setting `Ack = X + 1`, picks its own random Initial Sequence Number ($Y$), sets the `SYN` and `ACK` bits, and replies.
3. **ACK:** The client acknowledges the server's sequence number by setting `Ack = Y + 1`. Client state: `ESTABLISHED`. At this point, the client can begin sending application data (HTTP payload) immediately inside or following the ACK packet.

### Latency Cost of the Handshake

The 3-way handshake requires **1 full Round-Trip Time (RTT)** before a single byte of HTTP payload can be sent. If the client is 50ms away from the proxy, and the proxy is 30ms away from the backend:
* **Without Connection Pooling:** Establishing a new TCP connection on every request adds $50\text{ms} + 30\text{ms} = 80\text{ms}$ of fixed latency overhead *per request*.
* **With TLS 1.2:** Adds another 2 RTTs (total 3 RTTs).
* **With TLS 1.3:** Adds another 1 RTT (total 2 RTTs).

```
LATENCY WATERFALL: NEW CONNECTION VS POOLED CONNECTION

Unpooled (Cold Connection):
|--- TCP Handshake (1 RTT) ---|--- TLS Handshake (1-2 RTT) ---|--- HTTP Req/Resp (1 RTT) ---|
Total = 3 to 4 RTTs before data returns!

Pooled (Warm Connection):
|--- HTTP Req/Resp (1 RTT) ---|
Total = 1 RTT! (Up to 75% latency reduction)
```

### Why It Matters For This Project Specifically

In high-throughput reverse proxies:
1. **Upstream Connection Pooling is Mandatory:** The proxy must maintain long-lived, pre-established TCP connections to backend services. When 10,000 requests arrive per second, opening 10,000 new TCP connections/sec creates severe CPU overhead, kernel lock contention, and latency degradation.
2. **Kernel Backlog Queues:**
   * **SYN Queue (Half-Open Sockets):** Stores connections where SYN was received but final ACK is pending.
   * **Accept Queue (Fully Established):** Stores connections ready for Go's `listener.Accept()` to consume. If Go goroutines fail to accept fast enough, the accept queue overflows, causing dropped packets or connection refusals (`ECONNREFUSED`).

### Key Interview Points

* **SYN Flood Attack:** An attacker sends thousands of SYN packets with spoofed source IPs and never sends the final ACK. The server's SYN queue fills up, denying service to legitimate users.
  * *Defense:* **SYN Cookies** (encoding connection state cryptographically into the server's sequence number $Y$, allowing state-less handshake without allocating kernel memory until the final ACK arrives).
* **TCP Fast Open (TFO):** An extension (RFC 7413) that allows data transmission inside the initial SYN packet using a cryptographic cookie obtained from a previous connection, reducing handshake latency to 0 RTT for repeat connections.

---

## 4. TCP Connection Termination & The TIME_WAIT State

### What It Is

TCP connection teardown is a graceful **4-Way Handshake** because TCP connections are **full-duplex** (data can flow in both directions independently). Each direction must be shut down independently with a `FIN` (Finish) and `ACK` exchange.

```
       ACTIVE CLOSER                                   PASSIVE CLOSER
   (e.g., Reverse Proxy)                             (e.g., Upstream Server)
    (State: ESTABLISHED)                              (State: ESTABLISHED)
             |                                                 |
             |  1. FIN (Seq = U)                               |
             |------------------------------------------------>| (State: CLOSE_WAIT)
(State: FIN_WAIT_1)                                            |
             |  2. ACK (Ack = U + 1)                           | Application notified
             |<------------------------------------------------| (Half-closed)
(State: FIN_WAIT_2)                                            |
             |                                                 | Application finishes
             |                                                 | sending remaining data,
             |  3. FIN (Seq = V)                               | then calls close()
             |<------------------------------------------------| (State: LAST_ACK)
             |                                                 |
             |  4. ACK (Ack = V + 1)                           |
             |------------------------------------------------>| (State: CLOSED)
(State: TIME_WAIT)                                             |
 [Timer: 2 * MSL]                                              |
 [typically 60s ]                                              |
        |                                                      |
 (State: CLOSED)                                               |
```

### The Crucial `TIME_WAIT` State

The endpoint that **initiates the active close** (sends the first `FIN`) is forced by the TCP specification to transition into the `TIME_WAIT` state upon sending the final `ACK`. It must remain in `TIME_WAIT` for **$2 \times \text{MSL}$ (Maximum Segment Lifetime)**, typically configured as 60 seconds in modern Linux kernels.

#### Why `TIME_WAIT` Exists (Two Critical Purposes):
1. **Reliable Final ACK Delivery:** If the final `ACK` (Step 4) is lost in transit, the passive closer retransmits its `FIN` (Step 3). If the active closer had immediately destroyed its socket and closed, it would reply with an `RST` (Reset), causing an unnatural error (`connection reset by peer`) on the remote application.
2. **Draining Delayed / Wandering Segments:** Old duplicated packets lingering in internet routing queues must die before a new connection using the exact same 4-tuple `(src_ip, src_port, dst_ip, dst_port)` is permitted to open.

```
                  THE TIME_WAIT PORT EXHAUSTION DISASTER
                  
  Clients (10,000/s)           Reverse Proxy                    Backend Servers
       |                            |                                 |
       |  GET /api                  |                                 |
       |--------------------------->|  Opens new TCP connection       |
       |                            |-------------------------------->|
       |                            |  Closes connection proactively  |
       |                            |  (Sends FIN -> Active Closer)   |
       |                            |-------------------------------->|
       |                            |                                 |
       |                            | * Socket enters TIME_WAIT *     |
       |                            | * Port locked for 60 seconds *  |
       |                            |                                 |
       |                            | 60s x 1,000 closes/sec =        |
       |                            | 60,000 sockets in TIME_WAIT!    |
       |                            |                                 |
       |                            | Linux Ephemeral Range: ~28,000  |
       |                            | Result: EADDRNOTAVAIL (CRASH)   |
```

### Why It Matters For This Project Specifically

If our reverse proxy opens an ephemeral outbound connection to a backend for an HTTP request and closes it after receiving the response, the **proxy becomes the active closer**.
* At 1,000 requests/second, in 60 seconds the proxy will accumulate **60,000 sockets in `TIME_WAIT`**.
* The Linux default ephemeral port range (`ip_local_port_range`) is `32768` to `60999` (roughly 28,232 ports).
* **The proxy completely runs out of source ports!** Subsequent connection attempts to that backend fail with `dial tcp: bind: cannot assign requested address` (`EADDRNOTAVAIL`).

#### Mitigation Strategies:
1. **Use Persistent Connection Pooling (Keep-Alive):** Keep backend sockets open permanently so teardown never occurs under normal operation.
2. **Let the Upstream Server Close:** If closing is unavoidable, configure the backend server to initiate the close so `TIME_WAIT` state lives on the backend, distributing the socket load across multiple servers rather than concentrating it all on the single reverse proxy.
3. **Kernel Tuning:** Enable `net.ipv4.tcp_tw_reuse = 1` in Linux (allows the kernel to safely reuse `TIME_WAIT` sockets for outgoing connections when timestamps are present).

### Key Interview Points

* **`CLOSE_WAIT` vs. `TIME_WAIT`:**
  * `TIME_WAIT` is normal and healthy on the active closer.
  * `CLOSE_WAIT` is on the passive receiver. If a server has thousands of sockets stuck in `CLOSE_WAIT`, it indicates an **application-level bug**: the remote client closed the connection, but the local application code forgot to call `conn.Close()` or `resp.Body.Close()`.
* **`RST` (Reset) vs. `FIN`:** `FIN` is an orderly, polite shutdown ("I have no more data to send"). `RST` is an abrupt termination ("Connection aborted immediately, discard all buffers").

---

## 5. Ports, Sockets, and File Descriptors

### What It Is

* **Port:** A 16-bit unsigned integer ($0$ to $65,535$) used by the transport layer to deliver network segments to specific software processes.
* **Socket:** An operating system abstraction representing an endpoint for communication. A live TCP connection is globally and uniquely identified by a **5-Tuple**:

$$\text{Connection 5-Tuple} = \big(\text{Protocol}, \text{Source IP}, \text{Source Port}, \text{Destination IP}, \text{Destination Port}\big)$$

```
                       THE TCP 5-TUPLE ARCHITECTURE
                       
       Client Machine                                Reverse Proxy Server
    (IP: 198.51.100.42)                              (IP: 203.0.113.10)
+-------------------------+                       +-------------------------+
| Browser Process         |                       | Proxy Process (Go)      |
| Socket FD: 14           |                       | Socket FD: 102          |
| (Src Port: 54321)       |                       | (Listening Port: 443)   |
+-------------------------+                       +-------------------------+
             \                                                 /
              \_______________________________________________/
                               TCP CONNECTION 5-TUPLE:
             Protocol: TCP (6)
             Source IP: 198.51.100.42       | Source Port: 54321
             Destination IP: 203.0.113.10   | Destination Port: 443
```

### Port Categories

```
+-------------------+--------------------+---------------------------------------+
| Category          | Range              | Description / Standard Uses           |
+-------------------+--------------------+---------------------------------------+
| Well-Known Ports  | 0 - 1023           | System/Privileged (Root needed)       |
|                   |                    | 80 (HTTP), 443 (HTTPS), 22 (SSH),     |
|                   |                    | 53 (DNS)                              |
+-------------------+--------------------+---------------------------------------+
| Registered Ports  | 1024 - 49151       | Assigned to user processes & services |
|                   |                    | 8080 (Proxy Alt), 2379 (etcd),        |
|                   |                    | 6379 (Redis), 5432 (Postgres)         |
+-------------------+--------------------+---------------------------------------+
| Dynamic /         | 49152 - 65535      | Temporary outbound ports allocated by |
| Ephemeral Ports   | (Linux default:    | kernel for client-side connections    |
|                   | 32768 - 60999)     |                                       |
+-------------------+--------------------+---------------------------------------+
```

### Sockets as File Descriptors (FDs) in Unix/Linux

In Unix philosophy, **everything is a file**. When Go creates a listener or establishes an outbound connection, the OS kernel allocates an integer **File Descriptor (FD)** pointing to an in-kernel `struct socket` with receive/send buffers and protocol state.

```
                      PROCESS FILE DESCRIPTOR TABLE
                      
  Linux Kernel Process (PID: 4082 - Reverse Proxy)
  +--------+-----------------------------------------------------+
  | FD #   | Type / Destination Target                           |
  +--------+-----------------------------------------------------+
  | 0      | standard input (stdin)                              |
  | 1      | standard output (stdout)                            |
  | 2      | standard error (stderr)                             |
  | 3      | epoll instance fd (Go Netpoller)                    |
  | 4      | TCP Listening Socket (0.0.0.0:80)                   |
  | 5      | TCP Listening Socket (0.0.0.0:443)                  |
  | 6      | TCP Client Conn (198.51.100.42:54321 -> :443)       |
  | 7      | TCP Upstream Conn (:51234 -> 10.0.1.5:8080)         |
  | ...    | ...                                                 |
  | 1023   | * OS Limit Reached: "Too many open files" *         |
  +--------+-----------------------------------------------------+
```

### Why It Matters For This Project Specifically

1. **The Maximum Connection Limit (`ulimit -n`):** Each connected client uses at least 1 FD. Each connection to a backend uses 1 FD. Servicing 10,000 concurrent client requests proxying to backends requires **at least 20,000 open file descriptors**. If the system `ulimit -n` is set to the default of 1024, the proxy will crash with `accept: too many open files` (`EMFILE`). Production reverse proxies must tune `nofile` to `65535` or `1048576`.
2. **Go's Epoll Netpoller:** Traditional servers spawned 1 OS thread per socket (which fails beyond ~5,000 threads due to stack memory and context switching). Go's runtime uses **I/O Multiplexing** (`epoll` on Linux, `kqueue` on macOS). Thousands of Go goroutines block on `net.Conn.Read()`, while a single runtime thread monitors all socket FDs asynchronously via `epoll_wait`.

### Key Interview Points

* **Can multiple sockets listen on the same port?** Yes, via `SO_REUSEPORT` (Linux 3.9+). Multiple processes or worker threads can bind to the exact same IP:port; the kernel automatically load balances incoming SYN packets across the listeners.
* **5-Tuple Scaling Myth:** "A server can only handle 65,535 connections because there are 65,535 ports." **False!** A server listening on port 443 can accept millions of concurrent connections because each connection has a unique *client IP + client port* combination. The 65,535 limit only applies to outbound connections from *one IP* to a single destination *IP:Port*.

---

## 6. Domain Name System (DNS) & Service Discovery

### What It Is

The **Domain Name System (DNS)** is a distributed, hierarchical naming database that translates human-readable hostnames (e.g., `api.example.com`) into routable IP addresses (e.g., `203.0.113.5` or `2001:db8::1`).

```
                          DNS RESOLUTION HIERARCHY
                          
                              [ Root Servers ]
                              ( . ) (13 clusters)
                                      |
                                      v
                              [ TLD Servers ]
                                  ( .com )
                                      |
                                      v
                         [ Authoritative Nameservers ]
                              ( ns1.example.com )
                                      |
                                      v
  Client / Reverse Proxy ---> [ Local Recursive Resolver ] ---> Upstream Record
                               (8.8.8.8 / 1.1.1.1)             (A: 203.0.113.5)
```

### Core DNS Record Types

* **`A` Record:** Maps a hostname to an IPv4 address (32-bit).
* **`AAAA` Record:** Maps a hostname to an IPv6 address (128-bit).
* **`CNAME` Record:** Canonical Name (alias) mapping one hostname to another.
* **`SRV` Record:** Service locator record specifying hostname **and port** (widely used in service discovery like Consul).
* **`TXT` Record:** Arbitrary text metadata (used for domain verification, SPF/DKIM).

### DNS Caching and TTL (Time-To-Live)

Every DNS response contains a `TTL` (in seconds) defining how long intermediate resolvers and clients may cache the answer before issuing a fresh query.

```
       DNS RESOLUTION FLOW FOR REVERSE PROXY SERVICE DISCOVERY
       
Reverse Proxy                   Local Resolver / CoreDNS         Authoritative DNS
     |                                     |                             |
     | 1. Query: "backend-payment.local"   |                             |
     |------------------------------------>|                             |
     |                                     | 2. Recursive Lookup         |
     |                                     |---------------------------->|
     |                                     | 3. Response: IP=10.0.1.20   |
     |                                     |    (TTL = 5 seconds)        |
     |                                     |<----------------------------|
     | 4. Cached Result Returned           |                             |
     |<------------------------------------|                             |
     |                                                                   |
     | * Next 5 seconds: Proxy sends traffic to 10.0.1.20 without DNS *  |
     | * After 5 seconds: TTL expires -> New query issued *              |
```

### Relevance to Service Discovery in Our Reverse Proxy

In modern microservices environments (Docker Swarm, Kubernetes, etcd/Consul), backend instances are ephemeral—containers spin up, crash, and scale dynamically with changing private IP addresses.
* **DNS-Based Discovery:** The reverse proxy resolves `payment-service.default.svc.cluster.local`. CoreDNS returns the list of current healthy pod IPs.
* **The DNS Caching Pitfall:** If the reverse proxy or Go standard library resolver caches DNS responses indefinitely, traffic will continue flowing to dead IPs long after containers are destroyed.

### Why It Matters For This Project Specifically

1. **Why DNS Alone is Insufficient for Reverse Proxy Load Balancing:**
   * Intermediate DNS caches ignore low TTLs (TTL clamping).
   * DNS cannot detect if a backend instance's port is hung or returning 500 Internal Server Errors in real time (DNS has no L7 health checking).
   * DNS client-side failover is slow (seconds to minutes).
2. **Our Architecture:** Our reverse proxy uses **active service discovery** via `etcd` / dynamic registry combined with real-time L7 active health checks (probing `/healthz` every 1 second), bypassing stale DNS limitations entirely.

### Key Interview Points

* **Recursive vs. Iterative DNS:**
  * *Recursive:* The client asks the local resolver to do all the work and return only the final answer.
  * *Iterative:* The server replies with the next server to query ("I don't know, ask the `.com` TLD server").
* **DNS Round Robin:** An authoritative server returns multiple IP addresses in rotating order. While primitive, it lacks health-awareness, session persistence, and weighted distribution—which is why an intelligent L7 reverse proxy is placed in front.

---

## 7. HTTP/1.1 Protocol Architecture

### What It Is

**HTTP/1.1 (Hypertext Transfer Protocol)** is a stateless, text-based application-layer request/response protocol standardized in RFC 7230-7235. It operates strictly over a reliable transport stream (TCP).

```
                      RAW HTTP/1.1 REQUEST ANATOMY
                      
Request Line ->  GET /api/v1/users?role=admin HTTP/1.1\r\n
                 Host: api.example.com\r\n
Headers ------>  User-Agent: curl/8.1.2\r\n
                 Accept: application/json\r\n
                 Connection: keep-alive\r\n
Empty Line --->  \r\n
Body --------->  (Optional Request Body if POST/PUT)
```

```
                     RAW HTTP/1.1 RESPONSE ANATOMY
                     
Status Line -->  HTTP/1.1 200 OK\r\n
                 Date: Mon, 31 Aug 2026 12:00:00 GMT\r\n
Headers ------>  Content-Type: application/json; charset=utf-8\r\n
                 Content-Length: 38\r\n
                 Connection: keep-alive\r\n
Empty Line --->  \r\n
Body --------->  {"status":"success","users":["alice"]}
```

> **CRLF Convention:** Every line in an HTTP/1.1 header block MUST terminate with Carriage Return + Line Feed (`\r\n` or `0x0D 0x0A`). An empty line `\r\n` strictly denotes the boundary between headers and the body.

### HTTP Methods & Idempotency Matrix

```
+---------+--------+------------+-----------------------------------------------+
| Method  | Safe?  | Idempotent?| Primary Semantic Use Case                     |
+---------+--------+------------+-----------------------------------------------+
| GET     | YES    | YES        | Retrieve resource state (No side effects)     |
| HEAD    | YES    | YES        | Retrieve headers only (Identical to GET)      |
| OPTIONS | YES    | YES        | Query server capabilities / CORS preflight    |
| POST    | NO     | NO         | Create new resource or execute arbitrary action|
| PUT     | NO     | YES        | Replace resource completely at target URI     |
| PATCH   | NO     | NO         | Apply partial modifications to a resource     |
| DELETE  | NO     | YES        | Remove resource at target URI                 |
+---------+--------+------------+-----------------------------------------------+
```

* **Safe:** Does not modify server state (read-only).
* **Idempotent:** Executing the request $N$ times consecutively ($N > 1$) results in the exact same server state as executing it once ($N = 1$). *Crucial for proxy retry logic: A proxy can automatically retry failed GET/PUT requests, but should NEVER blindly retry non-idempotent POST requests without special idempotency keys!*

### HTTP Status Code Taxonomy for Reverse Proxies

```
+-------+-----------------------+-------------------------------------------------------+
| Range | Classification        | Core Status Codes Handled by Reverse Proxies          |
+-------+-----------------------+-------------------------------------------------------+
| 1xx   | Informational         | 100 Continue, 101 Switching Protocols (WebSockets)    |
| 2xx   | Success               | 200 OK, 201 Created, 204 No Content                   |
| 3xx   | Redirection           | 301 Moved Permanently, 302 Found, 304 Not Modified    |
| 4xx   | Client Error          | 400 Bad Request, 401 Unauthorized, 403 Forbidden,     |
|       |                       | 404 Not Found, 429 Too Many Requests (Rate Limiter)   |
| 5xx   | Server / Proxy Error  | 500 Internal Error, 502 Bad Gateway,                  |
|       |                       | 503 Service Unavailable, 504 Gateway Timeout          |
+-------+-----------------------+-------------------------------------------------------+
```

### The Critical 5xx Distinction in Reverse Proxies

* **`502 Bad Gateway`:** The reverse proxy reached out to the backend server, but the backend returned an invalid response, closed the connection abruptly (TCP RST), or the proxy failed to establish a TCP handshake (`ECONNREFUSED`).
* **`503 Service Unavailable`:** The reverse proxy cannot forward the request because all backend instances are marked unhealthy by the circuit breaker/health checker, or the internal connection pool queue is completely full.
* **`504 Gateway Timeout`:** The reverse proxy established a connection to the backend, but the backend failed to return an HTTP response header before the proxy's configured `ResponseHeaderTimeout` expired.

### Chunked Transfer Encoding (`Transfer-Encoding: chunked`)

When an origin backend streams dynamic data whose total byte size is unknown in advance, it omits `Content-Length` and specifies `Transfer-Encoding: chunked`.
* The body is transmitted in a series of chunks.
* Each chunk starts with the chunk's size in **hexadecimal**, followed by `\r\n`, the raw chunk data, and `\r\n`.
* A final chunk of size `0\r\n\r\n` signals the end of the stream.

```
                  CHUNKED TRANSFER ENCODING STREAM
                  
HTTP/1.1 200 OK\r\n
Transfer-Encoding: chunked\r\n
Content-Type: text/plain\r\n
\r\n
7\r\n          <-- Hex 7 = 7 bytes
Golang \r\n
8\r\n          <-- Hex 8 = 8 bytes
Reverse \r\n
6\r\n          <-- Hex 6 = 6 bytes
Proxy!\r\n
0\r\n          <-- 0-size chunk: End of stream
\r\n
```

### Why It Matters For This Project Specifically

Our reverse proxy must stream request and response bodies using chunked encoding without buffering gigabytes of data into RAM. In Go, `io.Copy(clientWriter, backendReader)` streams chunks transparently with zero intermediate disk allocation.

---

## 8. Essential HTTP Headers in Reverse Proxying

### What It Is

HTTP headers are colon-separated key-value pairs that carry metadata about the request, response, connection state, or payload encoding.

```
+---------------------+-------------------+---------------------------------------------------+
| Header Name         | Header Category   | Function & Reverse Proxy Responsibility           |
+---------------------+-------------------+---------------------------------------------------+
| Host                | Request (End2End) | Specifies target domain (e.g. api.example.com).   |
|                     |                   | Proxy rewrites or passes through for vhosts.      |
+---------------------+-------------------+---------------------------------------------------+
| Content-Length      | Entity (End2End)  | Exact size of the message body in octets (bytes). |
+---------------------+-------------------+---------------------------------------------------+
| Content-Type        | Entity (End2End)  | Media type (MIME) of the body (application/json). |
+---------------------+-------------------+---------------------------------------------------+
| Connection          | Hop-by-Hop        | Controls connection persistence (keep-alive/close)|
|                     |                   | MUST be handled and stripped by proxy.            |
+---------------------+-------------------+---------------------------------------------------+
| X-Forwarded-For     | De-facto Proxy    | IP chain: client, proxy1, proxy2.                 |
+---------------------+-------------------+---------------------------------------------------+
| X-Forwarded-Proto   | De-facto Proxy    | Original scheme: http or https.                   |
+---------------------+-------------------+---------------------------------------------------+
| X-Forwarded-Host    | De-facto Proxy    | Original Host header sent by client.              |
+---------------------+-------------------+---------------------------------------------------+
| X-Request-ID        | Distributed Trace | Unique UUID for tracing request across backends.  |
+---------------------+-------------------+---------------------------------------------------+
```

### Hop-by-Hop vs. End-to-End Headers (RFC 7230 §6.1)

This is one of the most important architectural rules for reverse proxy implementers:

```
  Client                     Reverse Proxy                     Backend Server
    |                              |                                 |
    | GET / HTTP/1.1               | GET / HTTP/1.1                  |
    | Connection: close   -------->| Connection: keep-alive -------->|
    | Keep-Alive: timeout=5        | (Strip Hop-by-Hop Headers!)     |
    | X-Forwarded-For: 203.0.113.1 | X-Forwarded-For: 203.0.113.1    |
```

1. **End-to-End Headers:** MUST be transmitted to the final recipient intact (e.g., `Authorization`, `Accept`, `User-Agent`, `Cookie`).
2. **Hop-by-Hop Headers:** Meaningful ONLY for a single transport-level connection. A reverse proxy **MUST strip all hop-by-hop headers** before forwarding the request to the upstream backend!

#### The Standard Hop-by-Hop Header Blacklist:
* `Connection`
* `Keep-Alive`
* `Proxy-Authenticate`
* `Proxy-Authorization`
* `TE`
* `Trailers`
* `Transfer-Encoding` (if the proxy decodes the stream)
* `Upgrade` (unless proxying WebSockets)

### Forwarded Headers & Client IP Restoration

When traffic passes through a reverse proxy, the backend server's TCP socket sees the **proxy's IP address** as the remote address—completely hiding the original client IP!

```
                  RESTORING CLIENT IDENTITY VIA XFF
                  
Client (198.51.100.25)
   |
   v
Reverse Proxy (203.0.113.10)
   |  * Appends client IP to X-Forwarded-For *
   |  * Injects X-Forwarded-Proto: https *
   |  * Injects X-Request-ID: c4a8-92f1 *
   v
Backend Server (10.0.1.5)
   * Reads X-Forwarded-For -> Knows real client is 198.51.100.25 *
   * Reads X-Request-ID -> Logs trace ID in application logs *
```

### Security Alert: X-Forwarded-For Spoofing

If an attacker sends `X-Forwarded-For: 127.0.0.1` to bypass your admin IP whitelist:
* **Insecure Proxy:** Appends to the header: `X-Forwarded-For: 127.0.0.1, 198.51.100.25`. If the backend reads the first IP in the list, it gets fooled!
* **Secure Proxy:** Sanitizes the header at the edge: If the incoming request comes from an untrusted public internet client, strip/overwrite the incoming `X-Forwarded-For` with `client.RemoteAddr`. Only trust existing `XFF` headers if the peer is a known, trusted upstream proxy (like Cloudflare or AWS ALB).

---

## 9. HTTP Keep-Alive & Persistent Connections

### What It Is

In HTTP/1.0, every single request/response required opening a brand new TCP connection and terminating it immediately after the response was received. In **HTTP/1.1**, all connections are **persistent by default** (`Connection: keep-alive`).

```
                HTTP/1.0 (NON-PERSISTENT) VS HTTP/1.1 (KEEP-ALIVE)
                
HTTP/1.0: 3 Requests = 3 TCP Handshakes + 3 TCP Teardowns
Client                                              Server
  |--- SYN ---> | <--- SYN-ACK --- | --- ACK --->   | (Handshake 1)
  |--- GET /index.html --->                         |
  |<-- 200 OK (Body) ---                            |
  |--- FIN ---> | <--- ACK --- | --- FIN --->       | (Close 1)
  |                                                 |
  |--- SYN ---> | <--- SYN-ACK --- | --- ACK --->   | (Handshake 2)
  |--- GET /style.css --->                          |
  |<-- 200 OK (Body) ---                            |
  |--- FIN ---> | <--- ACK --- | --- FIN --->       | (Close 2)
  
HTTP/1.1 Keep-Alive: 3 Requests = 1 TCP Handshake
Client                                              Server
  |--- SYN ---> | <--- SYN-ACK --- | --- ACK --->   | (Handshake 1)
  |--- GET /index.html --->                         |
  |<-- 200 OK (Body) ---                            |
  |--- GET /style.css --->                          | (Reused Socket)
  |<-- 200 OK (Body) ---                            |
  |--- GET /app.js --->                             | (Reused Socket)
  |<-- 200 OK (Body) ---                            |
  |--- (Socket stays open for future requests) ---- |
```

### The Delimitation Problem

If a TCP connection remains open after a response is sent, how does the receiver know where the current HTTP message ends and the next one begins?
1. **`Content-Length` Header:** Explicitly states the exact byte size of the payload. The client reads exactly $K$ bytes after `\r\n\r\n` and immediately prepares to parse the next HTTP header line.
2. **`Transfer-Encoding: chunked`:** Used when size is dynamic. The stream ends when a zero-byte chunk (`0\r\n\r\n`) is read.
3. **If neither is present and connection is not closed:** The parser hangs indefinitely waiting for EOF -> **Gateway Timeout (504)**.

### Head-of-Line (HoL) Blocking in HTTP/1.1

While HTTP/1.1 persistent connections eliminate TCP handshakes, they enforce **Strict FIFO Ordering**:
* Request 2 cannot be answered until Request 1 finishes.
* If Request 1 triggers a slow 10-second database query, Requests 2, 3, and 4 queued on that same TCP connection are blocked for 10 seconds.
* *(Note: HTTP/2 solves this with binary frame multiplexing over a single connection).*

### Why It Matters For This Project Specifically

Our reverse proxy manages Keep-Alive on **two independent sides**:
1. **Client <-> Proxy Keep-Alive:** Allows web browsers to send multiple API calls without reconnecting.
2. **Proxy <-> Backend Keep-Alive:** Allows the proxy to keep warm sockets connected to internal backend services in its **Connection Pool**.

---

## 10. Connection Pooling & Pool Lifecycle Management

### What It Is

A **Connection Pool** is a software cache of pre-established, warm network connections kept alive and ready for immediate reuse across multiple concurrent requests.

```
                     CONNECTION POOL ARCHITECTURE
                     
                      +---------------------------------------+
                      |       Upstream Connection Pool        |
                      |   (Target: backend-1.internal:8080)   |
                      +---------------------------------------+
                                   |             |
           Active Connections      |             |     Idle Connections
           (In-Flight Requests)    |             |     (Waiting in Pool)
          +--------------------+   |             |   +--------------------+
          | [Conn 1] (Req #10) |   |             |   | [Conn 3] (Idle 2s) |
          | [Conn 2] (Req #11) |<--+             +-->| [Conn 4] (Idle 5s) |
          +--------------------+                     +--------------------+
```

### Connection Lifecycle States

```
                 +-------------------+
                 |    NON-EXISTENT   |
                 +-------------------+
                           |
                           | 1. DialContext() [TCP + TLS Handshake]
                           v
                     +-----------+
    +--------------->|   IDLE    |<--------------+
    |                +-----------+               |
    |                      |                     |
    | 4. ReturnToPool()    | 2. Acquire()        |
    | (Reset state)        v                     |
    |                +-----------+               |
    +----------------|  ACTIVE   |---------------+
                     +-----------+
                           |
                           | 3. Network Error / Idle Timeout / EOF
                           v
                 +-------------------+
                 |      CLOSED       |
                 +-------------------+
```

### Pool Parameters & Tuning

```
+--------------------------+-------------------------------------------------------------+
| Setting Parameter        | Architectural Meaning & Production Impact                   |
+--------------------------+-------------------------------------------------------------+
| MaxIdleConns             | Global limit on total idle connections across all hosts.    |
+--------------------------+-------------------------------------------------------------+
| MaxIdleConnsPerHost      | Limit on idle connections kept warm per target backend.     |
|                          | CRITICAL: Go's default is 2! (Must be tuned to 100-1000).   |
+--------------------------+-------------------------------------------------------------+
| MaxConnsPerHost          | Total limit (active + idle) per backend. 0 = unlimited.     |
|                          | Prevents proxy from overwhelming a fragile backend.         |
+--------------------------+-------------------------------------------------------------+
| IdleConnTimeout          | Max duration a connection can sit idle before being closed  |
|                          | and removed from the pool (e.g., 90 seconds).               |
+--------------------------+-------------------------------------------------------------+
```

### The Go `http.Transport` Default Trap

In Go's standard library `http.Transport`, `DefaultMaxIdleConnsPerHost` is set to **`2`**.
If your reverse proxy receives 1,000 req/sec to a backend, only 2 connections return to the idle pool—the other 998 connections are aggressively closed after every request! This causes massive TCP handshake storms, CPU spikes, and `TIME_WAIT` port exhaustion. **Always explicitly set `MaxIdleConnsPerHost` to a high number (e.g., 100-500)!**

```go
// Production-tuned Go HTTP Transport for Reverse Proxying
customTransport := &http.Transport{
    Proxy: http.ProxyFromEnvironment,
    DialContext: (&net.Dialer{
        Timeout:   5 * time.Second,  // Connect Timeout
        KeepAlive: 30 * time.Second, // TCP Keep-Alive Probe
    }).DialContext,
    ForceAttemptHTTP2:     true,
    MaxIdleConns:          1000,             // Global idle pool size
    MaxIdleConnsPerHost:   100,              // Upstream per-host idle size
    MaxConnsPerHost:       500,              // Cap maximum concurrency per backend
    IdleConnTimeout:       90 * time.Second, // Close idle sockets after 90s
    TLSHandshakeTimeout:   5 * time.Second,
    ExpectContinueTimeout: 1 * time.Second,
    ResponseHeaderTimeout: 10 * time.Second, // Backend read timeout
}
```

### Key Interview Points

* **Stale Connection Detection Race:** A backend server might close an idle connection due to its own timeout at the exact millisecond the proxy acquires it from the pool to send a request. The proxy must detect the resulting `EOF` or `broken pipe` and cleanly retry the request on a fresh connection.

---

## 11. Network Timeouts & Failure Modes

### What It Is

A **Network Timeout** is a bounded deadline enforced on a socket operation. Without timeouts, network calls block indefinitely when packets are dropped, intermediate routers fail, or remote processes deadlock.

```
                      END-TO-END TIMEOUT ANATOMY
                      
Client                              Reverse Proxy                            Backend
  |                                       |                                     |
  |--- [1. ReadHeaderTimeout] ----------->|                                     |
  |    (Client sending request headers)   |                                     |
  |                                       |--- [2. Dial / Connect Timeout] ---->|
  |                                       |    (3-Way Handshake to Backend)     |
  |                                       |                                     |
  |                                       |--- [3. TLS Handshake Timeout] ----->|
  |                                       |                                     |
  |                                       |--- [4. ResponseHeaderTimeout] ----->|
  |                                       |    (Backend processing query)       |
  |                                       |                                     |
  |<-- [5. WriteTimeout] -----------------|                                     |
       (Streaming response back to client)|                                     |
```

### The 5 Essential Timeouts for Reverse Proxies

```
+------------------------+-------------------+---------------------------------------------------+
| Timeout Type           | Where Enforced    | Protection Provided / Failure Consequence         |
+------------------------+-------------------+---------------------------------------------------+
| ReadHeaderTimeout      | Inbound (Server)  | Prevents Slowloris attacks (slow header attack).  |
+------------------------+-------------------+---------------------------------------------------+
| ReadTimeout            | Inbound (Server)  | Max time to read the entire client request body.  |
+------------------------+-------------------+---------------------------------------------------+
| WriteTimeout           | Inbound (Server)  | Max time to write the full response to the client.|
+------------------------+-------------------+---------------------------------------------------+
| Connect / Dial Timeout | Outbound (Client) | Fails fast when backend server is down/unreachable|
+------------------------+-------------------+---------------------------------------------------+
| ResponseHeaderTimeout  | Outbound (Client) | Prevents proxy hanging on slow backend DB queries.|
+------------------------+-------------------+---------------------------------------------------+
| IdleTimeout            | Inbound & Outbound| Cleans up inactive keep-alive sockets.            |
+------------------------+-------------------+---------------------------------------------------+
```

### The Slowloris Denial-of-Service Attack

In a **Slowloris** attack, an attacker opens hundreds of TCP connections to the reverse proxy and sends incomplete HTTP headers at agonizingly slow rates (e.g., 1 byte every 10 seconds: `X-Header: a...`).
* **Without `ReadHeaderTimeout`:** The proxy keeps worker goroutines and socket buffers open waiting for the terminal `\r\n\r\n`, exhausting all available file descriptors and thread memory.
* **With `ReadHeaderTimeout = 2s`:** The proxy enforces that all headers must arrive within 2 seconds, immediately severing malicious connections with a `408 Request Timeout`.

### Cascading Distributed Timeout Budgeting

```
      CORRECT TIMEOUT BUDGETING (STRICT HIERARCHICAL SHRINKING)
      
[ Client Timeout: 10s ]
      |
      +---> [ Reverse Proxy Upstream Timeout: 7s ]
                  |
                  +---> [ Backend Internal Service Timeout: 4s ]
                              |
                              +---> [ Database Query Timeout: 2s ]
```

> **Hierarchical Rule:** The upstream timeout on the reverse proxy MUST be shorter than the downstream client's timeout, and the backend's internal timeouts must be shorter than the proxy's timeout. If the proxy timeout is 10s and the database timeout is 30s, the proxy will timeout, return 504 to the user, while the database wastes 20 seconds executing an orphaned query!

---

## 12. TLS (Transport Layer Security) & Edge Termination

### What It Is

**Transport Layer Security (TLS)** provides end-to-end cryptographic confidentiality, integrity, and authentication over TCP.

```
                      TLS 1.3 1-RTT HANDSHAKE FLOW
                      
Client                                                          Reverse Proxy
  |                                                                   |
  | 1. ClientHello                                                    |
  |    + Supported TLS Versions (TLS 1.3)                             |
  |    + Cipher Suites (AES-256-GCM, ChaCha20)                        |
  |    + Key Share (Client Public Ephemeral Key: ECDHE)               |
  |    + SNI (Server Name Indication: "api.example.com")              |
  |------------------------------------------------------------------>|
  |                                                                   |
  | 2. ServerHello                                                    |
  |    + Selected Cipher Suite                                        |
  |    + Key Share (Server Public Ephemeral Key)                      |
  |    + EncryptedExtensions (ALPN: ["h2", "http/1.1"])               |
  |    + Certificate (X.509 Server Certificate)                       |
  |    + CertificateVerify (Digital Signature proving private key)    |
  |    + Finished (HMAC verifying handshake integrity)                |
  |<------------------------------------------------------------------|
  |                                                                   |
  | === Keys Derived Independently via ECDHE Shared Secret ===        |
  |                                                                   |
  | 3. Finished [Encrypted Application Data: GET /api/v1 HTTP/1.1]   |
  |------------------------------------------------------------------>|
  |                                                                   |
  | 4. 200 OK [Encrypted Application Data]                            |
  |<------------------------------------------------------------------|
```

### Essential TLS Concepts

* **Symmetric Encryption (AES-GCM, ChaCha20-Poly1305):** Fast encryption using a single shared secret key for streaming payload data.
* **Asymmetric Encryption (RSA, ECDSA):** Public/private key pairs used strictly during the handshake to authenticate identity and establish the shared symmetric key.
* **X.509 Certificates & Public Key Infrastructure (PKI):** Digital certificates signed by trusted Certificate Authorities (CAs) guaranteeing domain ownership.
* **SNI (Server Name Indication):** TLS extension where the client specifies the hostname it wants in the initial plaintext `ClientHello`. This allows a single reverse proxy IP to host multiple distinct TLS domains with different SSL certificates.
* **ALPN (Application-Layer Protocol Negotiation):** Allows client and proxy to negotiate the protocol (HTTP/1.1 vs. HTTP/2) *during* the TLS handshake without extra round trips.

### TLS Termination Architecture

```
                        TLS TERMINATION AT THE EDGE
                        
Public Internet (Untrusted)              Private VPC / Datacenter (Secure)
===========================              =================================

  Client (Browser/Mobile)
     |
     | HTTPS / TLS Encrypted (Port 443)
     | [High Asymmetric Crypto Overhead]
     v
+------------------------------------+
| REVERSE PROXY (TLS Edge Terminal)  |
|                                    |
| * Decrypts incoming TLS traffic    |
| * Inspects HTTP headers/paths      |
| * Applies WAF & Rate Limiting      |
| * Performs Response Caching        |
+------------------------------------+
     |
     | Plaintext HTTP (Port 8080) OR Lightweight mTLS
     | [Zero / Low Crypto Overhead on Backends]
     +-------------------+-------------------+
     |                   |                   |
     v                   v                   v
+------------+     +------------+     +------------+
| Backend 1  |     | Backend 2  |     | Backend 3  |
+------------+     +------------+     +------------+
```

### Why TLS Termination at the Reverse Proxy is Standard:
1. **Centralized Certificate Management:** SSL certificates are renewed in one place (e.g., Let's Encrypt / ACME on the proxy) rather than deployed across 500 individual backend microservices.
2. **CPU Offloading:** Elliptic-curve Diffie-Hellman handshakes consume substantial CPU cycles. Terminating at the proxy frees backend compute for business logic.
3. **Enables L7 Intelligence:** A proxy cannot inspect HTTP paths, cookies, or headers if the stream remains encrypted end-to-end to the backend.

---

## 13. Reverse Proxy vs. Forward Proxy Architecture

### What It Is

While both types of proxies sit between a client and a server to mediate network traffic, their **purpose, placement, visibility, and control** are exact opposites.

```
                            FORWARD PROXY TOPOLOGY
                            
  Private Corporate Network                  Public Internet
+---------------------------+              +-----------------+
|  Client A                 |              |                 |
|  Client B --+             |              |  Origin Web     |
|  Client C --+             |              |  Servers        |
|             v             |              |  (google.com,   |
|     [ FORWARD PROXY ]     | -----------> |   github.com)   |
|     (Acts on behalf       |              |                 |
|      of the CLIENTS)      |              |                 |
+---------------------------+              +-----------------+
```

```
                            REVERSE PROXY TOPOLOGY
                            
      Public Internet                      Private Cloud / Datacenter
+-------------------------+              +----------------------------+
|                         |              |     +---> [ Backend 1 ]    |
|  Public Clients         |              |     |                      |
|  (Mobile, Browsers)     | -----------> | [ REVERSE PROXY ]          |
|  (Unaware of backends)  |              | (Acts on behalf            |
|                         |              |  of the SERVERS)           |
|                         |              |     +---> [ Backend 2 ]    |
+-------------------------+              +----------------------------+
```

### Comprehensive Comparison Matrix

```
+--------------------+------------------------------------+------------------------------------+
| Dimension          | Forward Proxy                      | Reverse Proxy                      |
+--------------------+------------------------------------+------------------------------------+
| Who it represents  | The CLIENT                         | The SERVER (Origin)                |
+--------------------+------------------------------------+------------------------------------+
| Placement          | In front of clients (Edge of LAN)  | In front of servers (Edge of DC)   |
+--------------------+------------------------------------+------------------------------------+
| Client Awareness   | Explicit (Configured in OS/browser)| Transparent (Client thinks proxy   |
|                    | or transparent gateway             | IS the origin server)              |
+--------------------+------------------------------------+------------------------------------+
| Identity Hidden    | Hides Client IP from Origin Server | Hides Backend IPs from Public      |
+--------------------+------------------------------------+------------------------------------+
| DNS Resolution     | Client resolves target web domain  | Public DNS maps domain directly to |
|                    | through or via proxy               | the Reverse Proxy's public IP      |
+--------------------+------------------------------------+------------------------------------+
| Primary Functions  | - Content filtering & egress rules | - Load balancing & traffic routing |
|                    | - Anonymity & geo-unblocking       | - TLS termination & offloading     |
|                    | - Caching outbound web assets      | - Ingress rate limiting & WAF      |
|                    | - Corporate data loss prevention   | - Microservice API Gateway routing |
+--------------------+------------------------------------+------------------------------------+
| HTTPS Handling     | Uses HTTP CONNECT tunneling        | Decrypts & Terminates TLS with its |
|                    | (cannot inspect without MitM cert) | own domain certificate             |
+--------------------+------------------------------------+------------------------------------+
```

---

## 14. L4 vs. L7 Load Balancing

### What It Is

Load balancing is the distribution of network traffic across a pool of backend compute resources. Load balancers operate primarily at **Layer 4 (Transport)** or **Layer 7 (Application)**.

```
                          L4 LOAD BALANCING (FLOW-BASED)
                          
Client (TCP SYN) ----> [ L4 Load Balancer ] ---- (NAT / DNAT) ----> [ Backend 2 ]
                        * Inspects IP:Port only *
                        * Does NOT inspect HTTP *
                        * High throughput, dumb routing *
```

```
                        L7 LOAD BALANCING (APPLICATION-AWARE)
                        
Client (HTTP GET) ---> [ L7 Load Balancer ] ---> Parses Headers / Path:
                        * Full HTTP Parser *     - Path /api/users  -> User Pods
                        * Terminates TCP *       - Path /api/orders -> Order Pods
                        * Inspects Headers *     - Header Auth: VIP -> High-Prio Pods
```

### Technical Comparison Matrix

```
+----------------------+---------------------------------+---------------------------------+
| Feature              | Layer 4 (L4) Load Balancer      | Layer 7 (L7) Load Balancer      |
+----------------------+---------------------------------+---------------------------------+
| Protocol Awareness   | TCP, UDP                        | HTTP, HTTPS, HTTP/2, gRPC, WS   |
+----------------------+---------------------------------+---------------------------------+
| Packet Inspection    | IP header & TCP/UDP port only   | Full HTTP payload, headers, URI |
+----------------------+---------------------------------+---------------------------------+
| TCP Connections      | 1 Pass-through stream (or DNAT) | 2 Terminated TCP connections    |
|                      |                                 | (Client<->Proxy & Proxy<->Svr)  |
+----------------------+---------------------------------+---------------------------------+
| Routing Decision     | IP hash, Round-Robin, LeastConn | Path-based (/api), Header,      |
|                      | based on raw TCP connections    | Cookie, Query Param, Method     |
+----------------------+---------------------------------+---------------------------------+
| TLS Handling         | Passthrough (Cannot terminate)  | Terminates TLS, decrypts stream |
+----------------------+---------------------------------+---------------------------------+
| Modification Ability | Cannot alter payload or headers | Injects/modifies headers (XFF), |
|                      |                                 | rewrites URL paths, compresses  |
+----------------------+---------------------------------+---------------------------------+
| Throughput / CPU     | Extremely high (Gbps/million PPS| Moderate (CPU bound due to L7   |
|                      | with negligible CPU usage)      | parsing and TLS decryption)     |
+----------------------+---------------------------------+---------------------------------+
| Real-World Examples  | AWS NLB, Linux IPVS, HAProxy TCP| AWS ALB, NGINX, Envoy, Traefik  |
+----------------------+---------------------------------+---------------------------------+
```

### Why Our Project is an L7 Load Balancer

Our reverse proxy is explicitly designed as an **L7 Intelligent Load Balancer** because modern distributed systems require application semantics:
1. **Content-Based Routing:** Routing `/api/v1/auth` to the Auth service and `/api/v1/search` to the Search cluster.
2. **Resilience & Fault Tolerance:** Inspecting status codes (retrying on 502/503, opening circuit breakers after $N$ consecutive 500 errors).
3. **Security:** Rate limiting by API Key extracted from the `Authorization: Bearer <token>` header or client IP.
4. **Caching:** Storing responses in Redis based on HTTP Method (`GET`), URI, and `Cache-Control` headers.

---

## 15. Network Address Translation (NAT) & Container Networking

### What It Is

**Network Address Translation (NAT)** is a method of remapping an IP address space into another by modifying network address information in the IP header while packets are in transit.

```
                      DOCKER CONTAINER NAT IN ACTION
                      
Host Machine (Public / LAN IP: 192.168.1.100)
+-------------------------------------------------------------------------------+
|  iptables Rule: PREROUTING -p tcp --dport 8080 -j DNAT --to-destination 172.17.0.2:80
|                                                                               |
|  [ docker0 Virtual Bridge: 172.17.0.1 ]                                       |
|               |                                                               |
|               v (veth0 virtual ethernet pair)                                 |
|  +-------------------------------------+                                      |
|  | Container (Reverse Proxy)           |                                      |
|  | Container IP: 172.17.0.2            |                                      |
|  | Listening Port: 80                  |                                      |
|  +-------------------------------------+                                      |
+-------------------------------------------------------------------------------+
```

### Core Flavors of NAT

1. **DNAT (Destination NAT):** Modifies the destination IP address in the packet header.
   * *Docker Port Forwarding (`-p 8080:80`):* When a packet arrives at the host on port 8080, Linux `iptables` performs DNAT, rewriting the destination IP to the container's private IP (`172.17.0.2`) and destination port to `80`.
2. **SNAT (Source NAT / Masquerade):** Modifies the source IP address in the packet header.
   * *Outbound Container Traffic:* When a container initiates a request to the internet, the host rewrites the source IP from the private container IP (`172.17.0.2`) to the host's public IP (`192.168.1.100`).

### Relevance to Our Reverse Proxy Architecture

When deploying our Go reverse proxy and backend services in Docker:
* **The `127.0.0.1` Trap:** Inside a Docker container, `localhost` (`127.0.0.1`) refers **only to that specific container**, not the host machine or other containers. The proxy cannot reach a backend at `http://127.0.0.1:8081`.
* **Bridge & Overlay Networks:** In a Docker user-defined network, containers communicate directly using internal container DNS names (e.g., `http://backend-1:8080`) over un-NATted internal bridge interfaces.

### Key Interview Points

* **Conntrack Table Exhaustion:** Linux tracks every active NAT connection in the kernel `conntrack` table (`/proc/sys/net/netfilter/nf_conntrack_max`). High-connection reverse proxies running behind NAT can exhaust the conntrack table, dropping all incoming SYN packets with `nf_conntrack: table full, dropping packet`.

---

## 16. Connection Reuse: Combining Keep-Alive & Pooling for Maximum Throughput

### What It Is

**Connection Reuse** is the end-to-end architectural synthesis of HTTP/1.1 persistent connections (`Keep-Alive`) with upstream **Connection Pooling** to eliminate redundant TCP 3-way handshakes, TLS handshakes, and slow-start ramp-ups across the entire request lifecycle.

```
          END-TO-END CONNECTION REUSE LIFECYCLE TIMELINE
          
SCENARIO A: NO REUSE (COLD CONNECTIONS)
---------------------------------------------------------------------------------
Client                         Reverse Proxy                         Backend
  |--- 1. TCP Handshake (1 RTT) ---->|                                  |
  |--- 2. TLS Handshake (1 RTT) ---->|                                  |
  |--- 3. HTTP GET Request --------->|--- 4. TCP Handshake (1 RTT) ---->|
  |                                  |--- 5. HTTP GET Request --------->|
  |                                  |<-- 6. HTTP 200 Response ---------|
  |                                  |--- 7. TCP FIN Close ------------>|
  |<-- 8. HTTP 200 Response ---------|                                  |
  |--- 9. TCP FIN Close ------------>|                                  |
Total Latency: 4 Round Trips (RTTs) | Sockets Created: 2 | High CPU Churn

SCENARIO B: FULL CONNECTION REUSE (WARM CONNECTIONS)
---------------------------------------------------------------------------------
Client                         Reverse Proxy                         Backend
  | [Client Keep-Alive Active]       | [Warm Pool: Conn #4 Ready]       |
  |--- 1. HTTP GET Request --------->|                                  |
  |                                  |--- 2. HTTP GET (On Conn #4) ---->|
  |                                  |<-- 3. HTTP 200 Response ---------|
  |<-- 4. HTTP 200 Response ---------| [Conn #4 Returned to Pool]       |
Total Latency: 1 Round Trip (RTT)   | Sockets Created: 0 | Minimal CPU Overhead!
```

### Quantitative Performance Impact

Let $R_{\text{client}}$ be client-to-proxy RTT ($40\text{ms}$) and $R_{\text{backend}}$ be proxy-to-backend RTT ($10\text{ms}$):
* **Without Connection Reuse:**
$$\text{Latency} = (2 \times R_{\text{client}}) + (2 \times R_{\text{backend}}) = 80\text{ms} + 20\text{ms} = \mathbf{100\text{ms}}$$
* **With Connection Reuse:**
$$\text{Latency} = (1 \times R_{\text{client}}) + (1 \times R_{\text{backend}}) = 40\text{ms} + 10\text{ms} = \mathbf{50\text{ms}} \quad \mathbf{(50\%\text{ Latency Drop!})}$$

### The Cardinal Rule of Body Draining in Go

To ensure an HTTP connection is returned to the `http.Transport` idle pool rather than closed, the application **MUST read the response body to completion** and explicitly close it:

```go
// CRITICAL PATTERN: Ensuring Connection Reuse in Go Reverse Proxies
resp, err := transport.RoundTrip(outboundReq)
if err != nil {
    return nil, err
}
defer resp.Body.Close()

// Forward body to downstream client
_, err = io.Copy(clientResponseWriter, resp.Body)

// If not fully consumed, discard remaining bytes so socket can be reused!
io.Copy(io.Discard, resp.Body)
```

---

## 17. SDE Interview Rapid-Fire Cheat Sheet

```
+----+---------------------------------------------------+--------------------------------------------------------------------+
| #  | Common SDE Interview Question                     | High-Impact 60-Second Answer                                       |
+----+---------------------------------------------------+--------------------------------------------------------------------+
| 1  | What happens when you type a URL in a browser?    | 1. DNS resolves domain to Reverse Proxy IP via cache/recursive.    |
|    |                                                   | 2. TCP 3-Way Handshake establishes L4 socket with Proxy on :443.   |
|    |                                                   | 3. TLS 1.3 Handshake negotiates encryption & validates cert via SNI|
|    |                                                   | 4. Browser sends HTTP GET request over encrypted TLS stream.       |
|    |                                                   | 5. Reverse Proxy parses L7 headers, routes to backend pool via LB. |
|    |                                                   | 6. Proxy fetches upstream response, streams bytes back to client.  |
+----+---------------------------------------------------+--------------------------------------------------------------------+
| 2  | Why is your project an L7 proxy instead of L4?    | L4 only sees IP:Port; L7 parses HTTP semantics. We need L7 to route|
|    |                                                   | by URI path, inject X-Forwarded-For headers, perform SSL           |
|    |                                                   | termination, enforce rate limits by API Key, and cache responses.  |
+----+---------------------------------------------------+--------------------------------------------------------------------+
| 3  | How do you prevent TIME_WAIT port exhaustion?     | Maintain persistent upstream HTTP connection pools (Keep-Alive) to |
|    |                                                   | avoid active closes, tune MaxIdleConnsPerHost, and enable          |
|    |                                                   | net.ipv4.tcp_tw_reuse at the Linux kernel level.                   |
+----+---------------------------------------------------+--------------------------------------------------------------------+
| 4  | What is the difference between 502 and 504?       | 502 Bad Gateway means the upstream server rejected the connection  |
|    |                                                   | or returned invalid data. 504 Gateway Timeout means the upstream  |
|    |                                                   | accepted the connection but didn't respond within the timeout limit|
+----+---------------------------------------------------+--------------------------------------------------------------------+
| 5  | What are Hop-by-Hop headers?                      | Headers meaningful for only one transport link (e.g., Connection,  |
|    |                                                   | Keep-Alive, Upgrade). An RFC-compliant proxy MUST strip them before|
|    |                                                   | forwarding traffic upstream.                                       |
+----+---------------------------------------------------+--------------------------------------------------------------------+
| 6  | Why is TCP_NODELAY important for reverse proxies? | Nagle's algorithm buffers small packets to create full frames,     |
|    |                                                   | adding 40ms latency. TCP_NODELAY disables buffering, sending bytes|
|    |                                                   | immediately for real-time HTTP throughput.                         |
+----+---------------------------------------------------+--------------------------------------------------------------------+
| 7  | How do you defend against Slowloris attacks?      | Configure strict ReadHeaderTimeout on the HTTP server. If client   |
|    |                                                   | headers aren't received within 2-5 seconds, abort the connection.  |
+----+---------------------------------------------------+--------------------------------------------------------------------+
| 8  | Why is Go well-suited for high-concurrency proxies| Go's runtime uses an internal netpoller with epoll/kqueue. Each    |
|    |                                                   | connection runs in a lightweight Goroutine (2KB stack vs 2MB OS    |
|    |                                                   | thread), allowing 100,000+ concurrent connections effortlessly.   |
+----+---------------------------------------------------+--------------------------------------------------------------------+
| 9  | What is SNI and why is it needed for TLS?         | Server Name Indication sends the requested hostname in plaintext   |
|    |                                                   | inside ClientHello, allowing one proxy IP to host multiple certs.  |
+----+---------------------------------------------------+--------------------------------------------------------------------+
| 10 | What causes "Too many open files" errors?         | Sockets are file descriptors. If concurrent connections exceed the |
|    |                                                   | OS ulimit -n limit, new accept() calls fail. Solved by increasing  |
|    |                                                   | ulimit -n to 65535+ in production.                                 |
+----+---------------------------------------------------+--------------------------------------------------------------------+
```

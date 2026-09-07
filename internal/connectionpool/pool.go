// Package connectionpool manages HTTP backend connection pools.
//
// # Why connection pooling?
//
// Every TCP connection requires a 3-way handshake before any data can be sent.
// On a typical LAN this costs ~0.5ms; over the internet it can be 50-300ms.
//
// Without pooling:
//
//	Client → Proxy → [SYN] → [SYN-ACK] → [ACK] → Backend → response → [FIN] → done
//
// The proxy creates a fresh TCP connection for EVERY proxied request, paying
// the handshake cost every time and leaving thousands of sockets in TIME_WAIT.
//
// With pooling:
//
//	First request  : Proxy connects to Backend once (handshake paid here)
//	Second request : Proxy reuses the existing idle connection → no handshake
//
// Go's net/http.Transport is the standard mechanism for this. It:
//   - maintains a pool of idle keep-alive connections per host
//   - reuses them across goroutines safely
//   - handles connection expiry and broken pipes
//
// This package exposes Config so callers can tune pool behaviour, and
// New wraps an http.Transport with those settings.
package connectionpool

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// Config holds tunable parameters for the connection pool.
//
// Choosing values:
//
//	MaxIdleConns          — total idle connections kept across all hosts.
//	                        Set to 0 for unlimited (not recommended in prod).
//	MaxIdleConnsPerHost   — idle connections kept for one backend.
//	                        Tune to roughly: expected RPS × avg backend latency.
//	MaxConnsPerHost       — hard cap on concurrent connections to one backend.
//	                        0 = unlimited (OS-limited).
//	IdleConnTimeout       — how long an idle connection stays open.
//	                        Shorter = fewer resources; longer = more reuse.
//	DialTimeout           — time allowed to establish a TCP connection.
//	ResponseHeaderTimeout — time to wait for the backend's first response byte.
//	TLSHandshakeTimeout   — time allowed for the TLS handshake.
type Config struct {
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
	MaxConnsPerHost       int
	IdleConnTimeout       time.Duration
	DialTimeout           time.Duration
	ResponseHeaderTimeout time.Duration
	TLSHandshakeTimeout   time.Duration
}

// Default returns a sensible baseline config for a single-backend proxy.
func Default() Config {
	return Config{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       0, // unlimited
		IdleConnTimeout:       90 * time.Second,
		DialTimeout:           10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
	}
}

// NoPool returns a config that forces a new TCP connection per request.
// Used in benchmarks to demonstrate the overhead of connection establishment.
func NoPool() Config {
	return Config{
		MaxIdleConns:          0, // no keep-alive connections
		MaxIdleConnsPerHost:   0,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       1 * time.Nanosecond, // expire immediately
		DialTimeout:           10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
	}
}

// New builds an http.Transport tuned with the given Config.
//
// http.Transport is the heart of Go's HTTP client connection management.
// It is safe for concurrent use and should be shared across requests —
// that is exactly what the proxy does by holding a single instance.
func New(cfg Config) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   cfg.DialTimeout,
		KeepAlive: 30 * time.Second, // send TCP keep-alive probes every 30s
	}

	return &http.Transport{
		// DialContext is called when a new TCP connection is needed.
		// net.Dialer enforces the DialTimeout and KeepAlive probes.
		DialContext: dialer.DialContext,

		// TLS settings
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: false},
		TLSHandshakeTimeout: cfg.TLSHandshakeTimeout,

		// Idle connection pool settings
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:     cfg.MaxConnsPerHost,
		IdleConnTimeout:     cfg.IdleConnTimeout,

		// ResponseHeaderTimeout limits how long we wait for the backend
		// to send response headers. This is separate from the body timeout.
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,

		// DisableKeepAlives = false (default) — enable HTTP keep-alive.
		// Setting this to true disables connection reuse entirely (like NoPool).
		DisableKeepAlives: false,

		// ForceAttemptHTTP2 upgrades to HTTP/2 where the server supports it.
		ForceAttemptHTTP2: true,
	}
}

// Package main implements a minimal HTTP backend server that will evolve into
// the full backend service over subsequent days.
//
// This is the server that the reverse proxy will forward requests to.
//
// Day 1 scope:
//   - GET /health     → health check endpoint (used by health checker on Day 6)
//   - GET /api/info   → server metadata (demonstrates JSON responses)
//   - Structured JSON logging with request IDs
//   - Graceful shutdown
//   - Configurable via flags
//
// HTTP concepts demonstrated:
//   - HTTP/1.1 request/response cycle over TCP
//   - HTTP methods, status codes, headers
//   - JSON content negotiation (Content-Type: application/json)
//   - Request ID generation and propagation (X-Request-ID header)
//   - HTTP server timeouts (read, write, idle)
//   - Graceful shutdown with http.Server.Shutdown
//   - Middleware pattern (logging, request ID injection)
//
// Usage:
//
//	go run ./cmd/backend/ -port 9001 -name backend-1
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/discovery"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/middleware"
)

// startTime records when the server started, used for uptime reporting.
var startTime = time.Now()

func main() {
	port := flag.String("port", "9001", "HTTP server listen port")
	name := flag.String("name", "backend-1", "Server instance name (identifies this backend)")
	etcdEndpoints := flag.String("etcd-endpoints", "", "Comma-separated list of etcd endpoints for auto-registration (e.g. localhost:2379)")
	weight := flag.Int("weight", 1, "Load balancer weight for this backend")
	advertiseURL := flag.String("advertise-url", "", "Public URL for service discovery (defaults to http://localhost:<port>)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger, *port, *name, *etcdEndpoints, *advertiseURL, *weight); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

// run starts the HTTP server and blocks until shutdown is requested.
func run(ctx context.Context, logger *slog.Logger, port, name, etcdEndpoints, advertiseURL string, weight int) error {
	// --- Route registration ---
	// Go 1.22+ supports method-based routing directly in http.ServeMux.
	// "GET /health" only matches GET requests to /health (not POST, PUT, etc.).
	mux := http.NewServeMux()

	// requestCount tracks total requests handled (atomic for concurrent safety).
	var requestCount atomic.Uint64

	mux.HandleFunc("GET /health", handleHealth(name))
	mux.HandleFunc("GET /api/info", handleInfo(name))
	mux.HandleFunc("GET /api/users", handleGetUsers())
	mux.HandleFunc("GET /api/products", handleGetProducts())
	mux.HandleFunc("POST /api/orders", handleCreateOrder())

	// --- Middleware chain ---
	// Middleware wraps handlers to add cross-cutting concerns.
	// Order matters: outermost middleware runs first.
	//
	//   Request → requestID → logging → router → handler
	//   Response ← requestID ← logging ← router ← handler
	handler := middleware.Chain(
		mux,
		middleware.RequestID,
		middleware.Logging(logger, &requestCount),
	)

	// --- Server configuration ---
	// Each timeout protects against a different failure mode:
	//   ReadHeaderTimeout → slow client sending headers (Slowloris attack mitigation)
	//   ReadTimeout       → slow client sending body
	//   WriteTimeout      → slow response writing (prevents goroutine leaks)
	//   IdleTimeout       → keep-alive connection sitting idle (frees resources)
	//   MaxHeaderBytes    → prevents memory exhaustion from huge headers
	addr := ":" + port
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}

	// Start server in a goroutine. ListenAndServe creates a TCP listener
	// (same as net.Listen under the hood), then enters an accept loop that
	// spawns a goroutine per incoming HTTP connection.
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http backend server started",
			"addr", addr,
			"name", name,
			"pid", os.Getpid(),
		)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// --- Service Discovery Auto-Registration ---
	if etcdEndpoints != "" {
		endpoints := strings.Split(etcdEndpoints, ",")
		for i := range endpoints {
			endpoints[i] = strings.TrimSpace(endpoints[i])
		}
		reg, err := discovery.NewRegistry(endpoints, 3*time.Second, logger)
		if err != nil {
			logger.Warn("failed to connect to etcd for auto-registration", "err", err)
		} else {
			advURL := advertiseURL
			if advURL == "" {
				advURL = fmt.Sprintf("http://localhost:%s", port)
			}
			entry := discovery.BackendEntry{
				ID:     name,
				URL:    advURL,
				Weight: weight,
			}
			stopKA, err := reg.Register(ctx, entry, 15*time.Second)
			if err != nil {
				logger.Warn("failed to register with etcd", "err", err)
			} else {
				defer func() {
					stopKA()
					deregCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					_ = reg.Deregister(deregCtx, name)
					_ = reg.Close()
				}()
				logger.Info("backend registered in etcd service discovery", "id", name, "url", advURL)
			}
		}
	}

	// Wait for either a fatal server error or shutdown signal.
	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
	}

	// --- Graceful shutdown ---
	// server.Shutdown does the following:
	//   1. Closes the listener (stops accepting new connections)
	//   2. Closes idle keep-alive connections
	//   3. Waits for active requests to complete (up to the context deadline)
	//   4. Returns nil if all requests finished, or context error if timed out
	//
	// This is critical for production — it prevents dropping in-flight requests
	// during deployments or restarts.
	logger.Info("shutting down http server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	logger.Info("http server stopped gracefully",
		"total_requests", requestCount.Load(),
	)
	return nil
}

// --- Handlers are now in handlers.go ---

// --- Helpers ---

// writeJSON serializes data as JSON and writes it to the response.
// Sets Content-Type to application/json and the given status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		// If JSON encoding fails, we've already written the status header,
		// so we can only log the error. This should never happen with simple
		// map[string]any data, but we handle it anyway.
		slog.Error("json encode error", "error", err)
	}
}

// generateRequestID creates a random 16-byte hex string (32 chars).
// Uses crypto/rand for cryptographic randomness — no external UUID library needed.
//
// Example output: "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6"
func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read should never fail on a healthy OS.
		// If it does, fall back to timestamp-based ID.
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

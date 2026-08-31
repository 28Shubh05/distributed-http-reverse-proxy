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
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
)

// startTime records when the server started, used for uptime reporting.
var startTime = time.Now()

func main() {
	port := flag.String("port", "9001", "HTTP server listen port")
	name := flag.String("name", "backend-1", "Server instance name (identifies this backend)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger, *port, *name); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

// run starts the HTTP server and blocks until shutdown is requested.
func run(ctx context.Context, logger *slog.Logger, port, name string) error {
	// --- Route registration ---
	// Go 1.22+ supports method-based routing directly in http.ServeMux.
	// "GET /health" only matches GET requests to /health (not POST, PUT, etc.).
	mux := http.NewServeMux()

	// requestCount tracks total requests handled (atomic for concurrent safety).
	var requestCount atomic.Uint64

	mux.HandleFunc("GET /health", handleHealth(name))
	mux.HandleFunc("GET /api/info", handleInfo(name))

	// --- Middleware chain ---
	// Middleware wraps handlers to add cross-cutting concerns.
	// Order matters: outermost middleware runs first.
	//
	//   Request → requestID → logging → router → handler
	//   Response ← requestID ← logging ← router ← handler
	handler := chainMiddleware(
		mux,
		requestIDMiddleware,
		loggingMiddleware(logger, &requestCount),
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

// --- Handlers ---

// handleHealth returns a health check handler. This endpoint will be used by
// the proxy's health checker (Day 6) to determine if this backend is alive.
//
// Response:
//
//	200 OK
//	{"status": "ok", "server": "backend-1", "timestamp": "2024-..."}
func handleHealth(serverName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "ok",
			"server":    serverName,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// handleInfo returns server metadata. Useful for debugging which backend
// handled a request (important when load balancing across multiple instances).
//
// Response:
//
//	200 OK
//	{"server": "backend-1", "go_version": "go1.26.3", ...}
func handleInfo(serverName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"server":     serverName,
			"go_version": runtime.Version(),
			"os":         runtime.GOOS,
			"arch":       runtime.GOARCH,
			"goroutines": runtime.NumGoroutine(),
			"uptime":     time.Since(startTime).String(),
		})
	}
}

// --- Middleware ---

// middlewareFunc is a function that wraps an http.Handler.
type middlewareFunc func(http.Handler) http.Handler

// chainMiddleware applies middleware in order. The first middleware in the list
// is the outermost (runs first on request, last on response).
func chainMiddleware(handler http.Handler, middlewares ...middlewareFunc) http.Handler {
	// Apply in reverse so the first middleware listed is the outermost.
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// contextKey is an unexported type for context keys to avoid collisions.
type contextKey string

const requestIDKey contextKey = "request_id"

// requestIDMiddleware generates or propagates X-Request-ID.
//
// Request ID flow:
//  1. If the client sends X-Request-ID → use it (enables end-to-end tracing)
//  2. Otherwise → generate a new one (crypto/rand for uniqueness)
//  3. Store it in the request context (for use by handlers and other middleware)
//  4. Set it as a response header (so the client can reference it in bug reports)
//
// In production, request IDs are essential for correlating logs across services.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Set response header before calling next handler.
		w.Header().Set("X-Request-ID", requestID)

		// Store in context so handlers and downstream middleware can access it.
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// loggingMiddleware logs every request with structured fields.
//
// Fields logged:
//   - request_id  → correlates with X-Request-ID header
//   - method      → HTTP method (GET, POST, etc.)
//   - path        → request path
//   - status      → HTTP status code
//   - latency_ms  → request processing time in milliseconds
//   - remote_addr → client's IP:port
//
// This is the structured logging format we'll use throughout the project.
// JSON format enables log aggregation and querying (e.g., in Grafana Loki).
func loggingMiddleware(logger *slog.Logger, requestCount *atomic.Uint64) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			requestCount.Add(1)

			// Wrap ResponseWriter to capture the status code.
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(sw, r)

			// Extract request ID from context (set by requestIDMiddleware).
			requestID, _ := r.Context().Value(requestIDKey).(string)

			logger.Info("request handled",
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"latency_ms", time.Since(start).Milliseconds(),
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent(),
			)
		})
	}
}

// statusWriter wraps http.ResponseWriter to capture the status code.
// We need this because the standard ResponseWriter doesn't expose the
// status code after WriteHeader is called.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (sw *statusWriter) WriteHeader(code int) {
	if !sw.wroteHeader {
		sw.status = code
		sw.wroteHeader = true
	}
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.wroteHeader {
		sw.wroteHeader = true
	}
	return sw.ResponseWriter.Write(b)
}

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

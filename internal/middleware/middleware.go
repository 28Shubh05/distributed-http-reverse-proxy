package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// MiddlewareFunc is a function that wraps an http.Handler.
type MiddlewareFunc func(http.Handler) http.Handler

// Chain applies middleware in order.
func Chain(handler http.Handler, middlewares ...MiddlewareFunc) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

type contextKey string

const requestIDKey contextKey = "request_id"

// RequestID generates or propagates X-Request-ID.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Always ensure X-Request-ID is in the request headers so it's forwarded
		r.Header.Set("X-Request-ID", requestID)
		w.Header().Set("X-Request-ID", requestID)

		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Logging logs every request with structured fields.
func Logging(logger *slog.Logger, requestCount *atomic.Uint64) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			if requestCount != nil {
				requestCount.Add(1)
			}

			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)

			requestID, _ := r.Context().Value(requestIDKey).(string)
			backend := sw.Header().Get("X-Backend-Server")
			retryCount := sw.Header().Get("X-Retry-Count")
			cacheHit := sw.Header().Get("X-Cache") == "HIT"
			errMsg := sw.Header().Get("X-Error-Message")
			if errMsg == "" && sw.status >= 400 {
				errMsg = http.StatusText(sw.status)
			}
			latency := time.Since(start)

			logger.Info("request handled",
				"timestamp", time.Now().UTC().Format(time.RFC3339Nano),
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"backend", backend,
				"status", sw.status,
				"latency", latency.Seconds(),
				"latency_ms", latency.Milliseconds(),
				"retry_count", retryCount,
				"cache_hit", cacheHit,
				"error", errMsg,
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent(),
			)
		})
	}
}

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

func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/metrics"
)

// Metrics returns a middleware that records Prometheus request volume, latency distributions,
// active in-flight connections, and HTTP error counts.
func Metrics(m *metrics.Metrics) MiddlewareFunc {
	if m == nil {
		m = metrics.Default
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.ActiveConnections.Inc()
			defer m.ActiveConnections.Dec()

			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(sw, r)

			duration := time.Since(start)
			codeStr := fmt.Sprintf("%d", sw.status)

			m.RecordProxyRequest(r.Method, codeStr, duration)

			if sw.status >= 500 {
				m.RecordProxyError("server_error")
			} else if sw.status >= 400 {
				m.RecordProxyError("client_error")
			}
		})
	}
}

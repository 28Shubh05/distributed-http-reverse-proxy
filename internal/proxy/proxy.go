package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"sync/atomic"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/loadbalancer"
)

// Config holds proxy configuration.
type Config struct {
	// BackendTimeout is the maximum time to wait for the backend to respond.
	// This applies per request and is enforced via context.WithTimeout.
	// A zero value disables the timeout.
	BackendTimeout time.Duration
}

type contextKey string

const backendCtxKey contextKey = "backend"

// ReverseProxy forwards incoming HTTP requests to multiple backends using a LoadBalancer.
// It is safe for concurrent use across goroutines.
type ReverseProxy struct {
	proxy  *httputil.ReverseProxy
	cfg    Config
	lb     loadbalancer.LoadBalancer
	logger *slog.Logger
}

// New creates a ReverseProxy with a custom http.Transport and a LoadBalancer.
func New(cfg Config, lb loadbalancer.LoadBalancer, transport http.RoundTripper, logger *slog.Logger) (*ReverseProxy, error) {
	if transport == nil {
		transport = http.DefaultTransport
	}

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			b, ok := req.Context().Value(backendCtxKey).(*loadbalancer.Backend)
			if !ok || b == nil {
				return // Handled in ServeHTTP
			}

			req.URL.Scheme = b.URL.Scheme
			req.URL.Host = b.URL.Host
			// Preserve the original path and query string.
			// req.URL.Path is already set by the client.
			req.Host = b.URL.Host

			// Delete X-Forwarded-For so we can set it cleanly.
			// httputil.ReverseProxy will re-add it with the client IP.
			req.Header.Del("X-Forwarded-For")
		},

		Transport: transport,

		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Set("X-Proxy", "distributed-proxy/1.0")

			// Passive health checking based on HTTP status code.
			b, ok := resp.Request.Context().Value(backendCtxKey).(*loadbalancer.Backend)
			if ok && b != nil {
				if resp.StatusCode >= 500 {
					lb.RecordFailure(b.ID)
				} else {
					lb.RecordSuccess(b.ID)
				}
			}
			return nil
		},

		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			b, ok := req.Context().Value(backendCtxKey).(*loadbalancer.Backend)

			// Check if the error is because the client cancelled the request.
			// In that case we shouldn't write any response or penalize the backend's health.
			if req.Context().Err() != nil {
				logger.Info("client cancelled request",
					"path", req.URL.Path,
					"err", req.Context().Err(),
				)
				if ok && b != nil {
					val := atomic.LoadInt64(&b.ActiveConnections)
					for val > 0 {
						if atomic.CompareAndSwapInt64(&b.ActiveConnections, val, val-1) {
							break
						}
						val = atomic.LoadInt64(&b.ActiveConnections)
					}
				}
				return
			}

			// Record passive failure on transport-level error (connection refused, timeout).
			if ok && b != nil {
				lb.RecordFailure(b.ID)
			}

			backendHost := "unknown"
			if ok && b != nil {
				backendHost = b.URL.Host
			}

			logger.Error("backend error",
				"path", req.URL.Path,
				"backend", backendHost,
				"err", err,
			)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}

	return &ReverseProxy{
		proxy:  rp,
		cfg:    cfg,
		lb:     lb,
		logger: logger,
	}, nil
}

// ServeHTTP implements http.Handler.
func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b := rp.lb.SelectBackend()
	if b == nil {
		rp.logger.Warn("no healthy backends available", "path", r.URL.Path)
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Put the backend in the context so the Director and callbacks can access it.
	ctx := context.WithValue(r.Context(), backendCtxKey, b)

	if rp.cfg.BackendTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, rp.cfg.BackendTimeout)
		defer cancel()
	}
	r = r.WithContext(ctx)

	rp.proxy.ServeHTTP(w, r)
}

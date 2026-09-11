package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"sync/atomic"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/loadbalancer"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/retry"
)

// Config holds proxy configuration.
type Config struct {
	// BackendTimeout is the maximum time to wait for the backend to respond.
	// This applies per request and is enforced via context.WithTimeout.
	// A zero value disables the timeout.
	BackendTimeout time.Duration

	// RetryPolicy defines retry behavior (max retries, backoff, jitter, idempotency).
	// If nil, retries are disabled.
	RetryPolicy *retry.Policy
}

// ErrNoHealthyBackends is returned when all backends are unavailable.
var ErrNoHealthyBackends = errors.New("no healthy backends available")

// retryTransport wraps an http.RoundTripper with load balancing and safe retries.
type retryTransport struct {
	base        http.RoundTripper
	lb          loadbalancer.LoadBalancer
	retryPolicy *retry.Policy
	logger      *slog.Logger
}

func (rt *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	maxRetries := 0
	if rt.retryPolicy != nil {
		maxRetries = rt.retryPolicy.MaxRetries
	}

	var lastErr error
	var lastResp *http.Response

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// If retrying, wait with exponential backoff & jitter
		if attempt > 0 {
			if rt.retryPolicy != nil {
				if err := rt.retryPolicy.Sleep(req.Context(), attempt-1); err != nil {
					return nil, err
				}
			}
		}

		// Select next healthy backend (skips unhealthy and open circuit breakers)
		backend := rt.lb.SelectBackend()
		if backend == nil {
			if lastResp != nil {
				return lastResp, nil
			}
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, ErrNoHealthyBackends
		}

		// Configure target backend URL
		req.URL.Scheme = backend.URL.Scheme
		req.URL.Host = backend.URL.Host
		req.Host = backend.URL.Host

		// Reset request body for retries if body exists
		if attempt > 0 && req.GetBody != nil {
			rc, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("failed to reset request body: %w", err)
			}
			req.Body = rc
		}

		// Send request upstream
		resp, err := rt.base.RoundTrip(req)

		// 1. Transport-level error (timeout, connection refused, network failure)
		if err != nil {
			// Do not penalize backend if the client disconnected
			if req.Context().Err() != nil {
				decrementActiveConnections(backend)
				return nil, err
			}

			// Record failure on this backend
			rt.lb.RecordFailure(backend.ID)
			lastErr = err

			if rt.retryPolicy != nil && rt.retryPolicy.ShouldRetry(req.Method, 0, err, attempt) {
				rt.logger.Warn("backend transport error, retrying on next backend",
					"backend", backend.ID,
					"attempt", attempt+1,
					"err", err,
				)
				continue
			}
			return nil, err
		}

		// 2. HTTP response received
		// Check for transient retriable status codes (e.g. 502, 503, 504)
		if rt.retryPolicy != nil && rt.retryPolicy.ShouldRetry(req.Method, resp.StatusCode, nil, attempt) {
			rt.lb.RecordFailure(backend.ID)
			rt.logger.Warn("backend returned retriable status code, retrying on next backend",
				"backend", backend.ID,
				"attempt", attempt+1,
				"status", resp.StatusCode,
			)
			resp.Body.Close()
			lastResp = resp
			continue
		}

		// 3. Final response accepted
		if resp.StatusCode >= 500 {
			rt.lb.RecordFailure(backend.ID)
		} else {
			rt.lb.RecordSuccess(backend.ID)
		}

		return resp, nil
	}

	if lastResp != nil {
		return lastResp, nil
	}
	return nil, lastErr
}

func decrementActiveConnections(b *loadbalancer.Backend) {
	if b == nil {
		return
	}
	val := atomic.LoadInt64(&b.ActiveConnections)
	for val > 0 {
		if atomic.CompareAndSwapInt64(&b.ActiveConnections, val, val-1) {
			break
		}
		val = atomic.LoadInt64(&b.ActiveConnections)
	}
}

// ReverseProxy forwards incoming HTTP requests to multiple backends using a LoadBalancer.
// It is safe for concurrent use across goroutines.
type ReverseProxy struct {
	proxy  *httputil.ReverseProxy
	cfg    Config
	lb     loadbalancer.LoadBalancer
	logger *slog.Logger
}

// New creates a ReverseProxy with a custom http.Transport, LoadBalancer, and RetryPolicy.
func New(cfg Config, lb loadbalancer.LoadBalancer, transport http.RoundTripper, logger *slog.Logger) (*ReverseProxy, error) {
	if transport == nil {
		transport = http.DefaultTransport
	}

	rt := &retryTransport{
		base:        transport,
		lb:          lb,
		retryPolicy: cfg.RetryPolicy,
		logger:      logger,
	}

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// Delete X-Forwarded-For so we can set it cleanly.
			// httputil.ReverseProxy will re-add it with the client IP.
			req.Header.Del("X-Forwarded-For")
		},

		Transport: rt,

		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Set("X-Proxy", "distributed-proxy/1.0")
			return nil
		},

		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			if req.Context().Err() != nil {
				logger.Info("client cancelled request",
					"path", req.URL.Path,
					"err", req.Context().Err(),
				)
				return
			}

			if errors.Is(err, ErrNoHealthyBackends) {
				logger.Warn("no healthy backends available", "path", req.URL.Path)
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)
				return
			}

			logger.Error("proxy error",
				"path", req.URL.Path,
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
	// Enable request body replay if body is present
	if r.Body != nil && r.Body != http.NoBody && r.GetBody == nil {
		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10MB limit
		if err == nil {
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			r.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(bodyBytes)), nil
			}
		}
	}

	if rp.cfg.BackendTimeout > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), rp.cfg.BackendTimeout)
		defer cancel()
		r = r.WithContext(ctx)
	}

	rp.proxy.ServeHTTP(w, r)
}

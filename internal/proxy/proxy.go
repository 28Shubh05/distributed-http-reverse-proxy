// Package proxy implements the core HTTP reverse proxy logic.
//
// # Architecture
//
// The ReverseProxy sits between clients and backends:
//
//	Client → [ReverseProxy.ServeHTTP] → httputil.ReverseProxy → Backend
//
// httputil.ReverseProxy does the heavy lifting:
//   - copying the request (method, headers, body, query params)
//   - stripping hop-by-hop headers (Connection, Upgrade, etc.)
//   - adding X-Forwarded-For / X-Forwarded-Proto
//   - copying the response back to the client
//
// This package adds:
//   - a configurable http.Transport (connection pool)
//   - per-request backend timeout via context.WithTimeout
//   - structured error handling (502 Bad Gateway on backend failure)
//
// # Context cancellation
//
// Every proxied request carries a context derived from the incoming request.
// If the client disconnects mid-flight, r.Context() is cancelled, which:
//  1. cancels the backend HTTP request immediately
//  2. releases the backend connection back to the pool
//  3. stops any goroutine waiting on the response body
//
// This prevents goroutine leaks under client-disconnect scenarios.
package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// Config holds proxy configuration.
type Config struct {
	// BackendURL is the upstream server this proxy routes to.
	BackendURL string

	// BackendTimeout is the maximum time to wait for the backend to respond.
	// This applies per request and is enforced via context.WithTimeout.
	// A zero value disables the timeout.
	BackendTimeout time.Duration
}

// ReverseProxy forwards incoming HTTP requests to a single backend.
// It is safe for concurrent use across goroutines.
type ReverseProxy struct {
	proxy  *httputil.ReverseProxy
	cfg    Config
	logger *slog.Logger
}

// New creates a ReverseProxy with a custom http.Transport (connection pool).
//
// transport is typically built by connectionpool.New(cfg). Passing nil falls
// back to http.DefaultTransport (which has its own built-in pool but with
// less tuned defaults).
func New(cfg Config, transport http.RoundTripper, logger *slog.Logger) (*ReverseProxy, error) {
	target, err := url.Parse(cfg.BackendURL)
	if err != nil {
		return nil, fmt.Errorf("invalid backend URL %q: %w", cfg.BackendURL, err)
	}

	if transport == nil {
		transport = http.DefaultTransport
	}

	rp := &httputil.ReverseProxy{
		// Director rewrites the incoming request before it is forwarded.
		// Called in the goroutine that handles the upstream connection.
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			// Preserve the original path and query string.
			// req.URL.Path is already set by the client.
			req.Host = target.Host

			// Delete X-Forwarded-For so we can set it cleanly.
			// httputil.ReverseProxy will re-add it with the client IP.
			req.Header.Del("X-Forwarded-For")
		},

		// Transport is the connection pool. httputil.ReverseProxy calls
		// transport.RoundTrip(req) for every forwarded request.
		Transport: transport,

		// ModifyResponse runs after a successful backend response.
		// We could add/strip response headers here.
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Set("X-Proxy", "distributed-proxy/1.0")
			return nil
		},

		// ErrorHandler is called when the backend is unreachable or returns
		// a transport-level error (not a 5xx). We return 502 Bad Gateway.
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			// Check if the error is because the client cancelled the request.
			// In that case we shouldn't write any response.
			if req.Context().Err() != nil {
				logger.Info("client cancelled request",
					"path", req.URL.Path,
					"err", req.Context().Err(),
				)
				return
			}
			logger.Error("backend error",
				"path", req.URL.Path,
				"backend", req.URL.Host,
				"err", err,
			)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}

	return &ReverseProxy{
		proxy:  rp,
		cfg:    cfg,
		logger: logger,
	}, nil
}

// ServeHTTP implements http.Handler.
//
// If BackendTimeout is set, a child context is derived with that deadline.
// This ensures backend requests never hang indefinitely regardless of what
// the backend does.
func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if rp.cfg.BackendTimeout > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), rp.cfg.BackendTimeout)
		defer cancel()
		r = r.WithContext(ctx)
	}
	rp.proxy.ServeHTTP(w, r)
}

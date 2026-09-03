package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// ReverseProxy wraps httputil.ReverseProxy and adds our custom logic.
type ReverseProxy struct {
	proxy  *httputil.ReverseProxy
	logger *slog.Logger
}

// NewReverseProxy creates a new reverse proxy that routes to the given backend URL.
func NewReverseProxy(backendURL string, logger *slog.Logger) (*ReverseProxy, error) {
	target, err := url.Parse(backendURL)
	if err != nil {
		return nil, err
	}

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			// req.URL.Path stays the same to forward the path correctly
			req.Host = target.Host
			
			// Optional: add any extra headers, clear X-Forwarded-For if needed
			// For now httputil.ReverseProxy handles X-Forwarded-For automatically
		},
		ModifyResponse: func(resp *http.Response) error {
			// Can modify response here or log it if needed
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			logger.Error("proxy error", "error", err, "path", req.URL.Path)
			w.WriteHeader(http.StatusBadGateway)
		},
	}

	return &ReverseProxy{
		proxy:  rp,
		logger: logger,
	}, nil
}

func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	
	// Pass to standard reverse proxy
	rp.proxy.ServeHTTP(w, r)
	
	// Since standard httputil.ReverseProxy doesn't give us the status code directly easily 
	// unless we wrap ResponseWriter, we just log latency here as a basic level.
	// We will add more robust logging via middleware.
	rp.logger.Debug("proxied request", 
		"path", r.URL.Path, 
		"latency_ms", time.Since(start).Milliseconds(),
	)
}

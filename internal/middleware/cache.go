package middleware

import (
	"bytes"
	"net/http"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/cache"
)

// CacheConfig holds configuration parameters for the Caching middleware.
type CacheConfig struct {
	// TTL is the time-to-live for cached responses. Default: 60s.
	TTL time.Duration

	// InvalidateOnMutate automatically invalidates the cache for the requested URI
	// when a mutating method (POST, PUT, DELETE, PATCH) succeeds. Default: true.
	InvalidateOnMutate bool
}

// DefaultCacheConfig returns safe defaults for response caching.
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		TTL:                60 * time.Second,
		InvalidateOnMutate: true,
	}
}

// Caching wraps an http.Handler with HTTP response caching.
// It checks the cache for eligible requests (GET, HEAD), returning the cached
// response with "X-Cache: HIT" immediately. On cache miss, it captures the
// response, stores 200 OK responses with the configured TTL, and adds "X-Cache: MISS".
func Caching(c cache.Cache, cfg CacheConfig) MiddlewareFunc {
	if cfg.TTL <= 0 {
		cfg.TTL = 60 * time.Second
	}

	return func(next http.Handler) http.Handler {
		if c == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Invalidate cache for mutating requests (POST, PUT, DELETE, PATCH)
			if cfg.InvalidateOnMutate && (r.Method == http.MethodPost || r.Method == http.MethodPut ||
				r.Method == http.MethodDelete || r.Method == http.MethodPatch) {
				// We execute the request first
				rw := &responseCapture{ResponseWriter: w}
				next.ServeHTTP(rw, r)

				// If mutation succeeded (2xx), invalidate GET/HEAD cache for this URI
				if rw.statusCode >= 200 && rw.statusCode < 300 {
					_ = c.Delete(cache.BuildKey(http.MethodGet, r.URL.RequestURI()))
					_ = c.Delete(cache.BuildKey(http.MethodHead, r.URL.RequestURI()))
				}
				return
			}

			// Check if request is eligible for caching
			if !cache.IsCacheable(r.Method, r.Header) {
				w.Header().Set("X-Cache", "BYPASS")
				next.ServeHTTP(w, r)
				return
			}

			// Respect client Cache-Control: no-cache to force cache refresh
			if r.Header.Get("Cache-Control") == "no-cache" || r.Header.Get("Pragma") == "no-cache" {
				w.Header().Set("X-Cache", "BYPASS")
				next.ServeHTTP(w, r)
				return
			}

			cacheKey := cache.BuildKey(r.Method, r.URL.RequestURI())

			// 1. Check cache
			if cached, hit := c.Get(cacheKey); hit && cached != nil {
				w.Header().Set("X-Cache", "HIT")
				for k, vals := range cached.Headers {
					if cache.HopByHopHeaders[k] {
						continue
					}
					for _, v := range vals {
						w.Header().Add(k, v)
					}
				}
				w.WriteHeader(cached.StatusCode)
				if r.Method != http.MethodHead {
					_, _ = w.Write(cached.Body)
				}
				return
			}

			// 2. Cache miss: intercept response
			capture := &responseCapture{
				ResponseWriter: w,
				header:         make(http.Header),
				body:           &bytes.Buffer{},
			}
			w.Header().Set("X-Cache", "MISS")

			next.ServeHTTP(capture, r)

			// Store in cache only if response was 200 OK
			if capture.statusCode == http.StatusOK {
				headersCopy := make(map[string][]string)
				for k, v := range capture.header {
					if !cache.HopByHopHeaders[k] {
						headersCopy[k] = append([]string(nil), v...)
					}
				}

				cachedResp := &cache.CachedResponse{
					StatusCode: capture.statusCode,
					Headers:    headersCopy,
					Body:       capture.body.Bytes(),
					CachedAt:   time.Now(),
				}
				_ = c.Set(cacheKey, cachedResp, cfg.TTL)
			}
		})
	}
}

// responseCapture intercepts status code, headers, and body before sending to client.
type responseCapture struct {
	http.ResponseWriter
	statusCode  int
	header      http.Header
	body        *bytes.Buffer
	wroteHeader bool
}

func (rc *responseCapture) Header() http.Header {
	if rc.header != nil {
		return rc.header
	}
	return rc.ResponseWriter.Header()
}

func (rc *responseCapture) WriteHeader(code int) {
	if !rc.wroteHeader {
		rc.statusCode = code
		rc.wroteHeader = true

		if rc.header != nil {
			for k, v := range rc.header {
				for _, val := range v {
					rc.ResponseWriter.Header().Add(k, val)
				}
			}
		}
		rc.ResponseWriter.WriteHeader(code)
	}
}

func (rc *responseCapture) Write(b []byte) (int, error) {
	if !rc.wroteHeader {
		rc.WriteHeader(http.StatusOK)
	}
	if rc.body != nil {
		rc.body.Write(b)
	}
	return rc.ResponseWriter.Write(b)
}

package middleware

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/metrics"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/ratelimit"
)

// RateLimitConfig holds configuration for the RateLimiting middleware.
type RateLimitConfig struct {
	// KeyFunc extracts the rate limiting key from an incoming request.
	// If nil, ClientIPKey is used by default.
	KeyFunc func(r *http.Request) string
}

// ClientIPKey extracts the client IP address from X-Forwarded-For, X-Real-IP,
// or RemoteAddr.
func ClientIPKey(r *http.Request) string {
	// 1. Check X-Forwarded-For (client, proxy1, proxy2...)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}

	// 2. Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// 3. Fallback to RemoteAddr (strip port if present)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// RateLimiting returns a middleware that limits incoming request rates per client.
// When rate limits are exceeded, it immediately responds with 429 Too Many Requests
// and appropriate Retry-After and X-RateLimit-* headers.
func RateLimiting(limiter ratelimit.Limiter, cfg RateLimitConfig) MiddlewareFunc {
	keyFunc := cfg.KeyFunc
	if keyFunc == nil {
		keyFunc = ClientIPKey
	}

	return func(next http.Handler) http.Handler {
		if limiter == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientKey := keyFunc(r)
			res := limiter.Allow(clientKey)

			// Standard rate limiting observability headers
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", res.Limit))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", res.Remaining))
			resetSec := int64(math.Ceil(res.ResetIn.Seconds()))
			if resetSec <= 0 {
				resetSec = 1
			}
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetSec))

			if !res.Allowed {
				metrics.Default.RecordRateLimitRejection(clientKey)
				retryAfterSec := int64(math.Ceil(res.RetryAfter.Seconds()))
				if retryAfterSec <= 0 {
					retryAfterSec = 1
				}
				w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfterSec))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = fmt.Fprintf(w, `{"error":"too many requests","retry_after":%d}`+"\n", retryAfterSec)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Package cache provides a unified caching interface for HTTP response caching
// in the distributed reverse proxy.
//
// # Architecture
//
// The cache is designed around a simple key-value abstraction:
//
//	Cache key = HTTP method + URL (for safe, idempotent methods only)
//	Cache value = serialized HTTP response (status code + headers + body)
//
// # Caching Rules (HTTP Semantics)
//
// Only safe, idempotent GET and HEAD requests are cached. POST, PUT, PATCH,
// and DELETE requests are never cached because:
//
//   - POST is non-idempotent (duplicating it can create double orders, payments)
//   - PUT/PATCH/DELETE mutate server state and would produce stale reads
//
// Authorization-bearing requests (with "Authorization" or "Cookie" headers) are
// excluded from caching by default to prevent credential leakage between clients.
//
// # Implementations
//
// Two implementations are provided:
//
//   - MemoryCache: in-process LRU cache with size cap, suitable for single-instance proxies
//     and testing without a Redis dependency.
//   - RedisCache: Redis-backed cache using SET/GET with TTL, suitable for distributed
//     multi-proxy deployments where all proxy instances share the same cache.
package cache

import (
	"time"
)

// CachedResponse holds the data needed to reconstruct a full HTTP response
// from cached storage.
type CachedResponse struct {
	// StatusCode is the HTTP status code returned by the origin (e.g. 200).
	StatusCode int

	// Headers contains the response headers from the origin.
	// Hop-by-hop headers (Connection, Transfer-Encoding, etc.) are stripped
	// before storage.
	Headers map[string][]string

	// Body is the raw response body bytes.
	Body []byte

	// CachedAt is the time when the response was placed in the cache.
	CachedAt time.Time
}

// Cache is the interface that all cache backends must implement.
type Cache interface {
	// Get retrieves a cached response for the given key.
	// Returns (response, true) on cache hit or (nil, false) on miss.
	Get(key string) (*CachedResponse, bool)

	// Set stores a response in the cache with the given key and TTL.
	Set(key string, resp *CachedResponse, ttl time.Duration) error

	// Delete removes a cached entry by key. Used for manual invalidation.
	Delete(key string) error

	// Flush removes all entries from the cache. Primarily used in tests.
	Flush() error

	// Stats returns a snapshot of cache metrics.
	Stats() Stats
}

// Stats holds cache performance counters.
type Stats struct {
	// Hits is the total number of successful cache lookups.
	Hits int64

	// Misses is the total number of cache misses (key not found or expired).
	Misses int64

	// Evictions is the total number of entries evicted to make room for new ones.
	Evictions int64

	// Size is the current number of entries in the cache.
	Size int64
}

// IsCacheable reports whether a request is eligible for caching based on
// HTTP method and the presence of credential-bearing headers.
//
// Only GET and HEAD requests without Authorization/Cookie headers are cached.
// This prevents serving stale private data to different clients.
func IsCacheable(method string, headers map[string][]string) bool {
	if method != "GET" && method != "HEAD" {
		return false
	}
	// Never cache requests carrying session/auth data
	if _, hasAuth := headers["Authorization"]; hasAuth {
		return false
	}
	if _, hasCookie := headers["Cookie"]; hasCookie {
		return false
	}
	return true
}

// BuildKey constructs a canonical cache key from the HTTP method and URL string.
// The format is: "<METHOD>|<URL>"
func BuildKey(method, rawURL string) string {
	return method + "|" + rawURL
}

// HopByHopHeaders are headers that must not be forwarded or stored in a cache,
// as they apply to a single transport-level connection rather than end-to-end.
var HopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

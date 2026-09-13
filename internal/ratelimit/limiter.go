// Package ratelimit provides in-memory and distributed Token Bucket rate limiters
// to protect upstream backends from traffic spikes, denial-of-service, and abusive clients.
package ratelimit

import "time"

// Result contains the rate limiting decision and metadata for a client request.
type Result struct {
	// Allowed reports whether the request is within rate limits and permitted.
	Allowed bool

	// Limit is the maximum burst capacity (maximum tokens).
	Limit int

	// Remaining is the number of tokens left in the bucket after this request.
	Remaining int

	// RetryAfter is the duration until at least one token becomes available.
	// Zero if Allowed is true.
	RetryAfter time.Duration

	// ResetIn is the approximate duration until the bucket is completely refilled.
	ResetIn time.Duration
}

// Limiter defines the interface implemented by both in-memory and distributed rate limiters.
type Limiter interface {
	// Allow checks if 1 token is available for the given key and consumes it.
	Allow(key string) Result

	// AllowN checks if n tokens are available for the given key and consumes them.
	AllowN(key string, n int) Result
}

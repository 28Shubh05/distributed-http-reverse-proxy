// Package retry provides safe, idempotency-aware retry policies with
// exponential backoff and randomized jitter to mitigate retry storms.
//
// # Why Blind Retries Cause Outages ("Retry Storms")
//
// In distributed systems, naive retries during an upstream slowdown create a
// vicious cycle:
//  1. Backend slows down under load.
//  2. Clients or proxies time out and immediately retry.
//  3. Backend receives 2x-4x more load than normal, amplifying the degradation.
//  4. Cascading failure takes down the entire fleet.
//
// To prevent retry storms, this package implements:
//   - Strict idempotency checking (e.g. POST requests are never retried by default).
//   - Exponential backoff with random full jitter to desynchronize concurrent retries.
//   - Bounded maximum attempts and maximum backoff ceilings.
//   - Context cancellation awareness (aborts backoff sleep immediately if client disconnects).
package retry

import (
	"context"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// Policy defines parameters for deciding when and how long to wait before retrying.
type Policy struct {
	// MaxRetries is the maximum number of retry attempts (excluding the initial attempt).
	// Default: 2.
	MaxRetries int

	// InitialBackoff is the base backoff delay for the first retry.
	// Default: 100ms.
	InitialBackoff time.Duration

	// MaxBackoff is the maximum delay cap between attempts.
	// Default: 2s.
	MaxBackoff time.Duration

	// BackoffMultiplier is the factor by which delay increases each attempt.
	// Default: 2.0.
	BackoffMultiplier float64

	// JitterFraction is the fraction of delay subjected to randomization [0.0, 1.0].
	// Default: 0.25 (±25% variation).
	JitterFraction float64

	// RetryableMethods specifies which HTTP methods are safe to retry.
	// Default: GET, HEAD.
	RetryableMethods map[string]bool

	// RetryableStatusCodes specifies which HTTP status codes warrant a retry.
	// Default: 502 (Bad Gateway), 503 (Service Unavailable), 504 (Gateway Timeout).
	RetryableStatusCodes map[int]bool

	rngMu sync.Mutex
	rng   *rand.Rand
}

// DefaultPolicy returns a safe, production-oriented retry policy.
func DefaultPolicy() *Policy {
	return &Policy{
		MaxRetries:        2,
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        2 * time.Second,
		BackoffMultiplier: 2.0,
		JitterFraction:    0.25,
		RetryableMethods: map[string]bool{
			http.MethodGet:     true,
			http.MethodHead:    true,
			http.MethodOptions: true,
			http.MethodTrace:   true,
			http.MethodPut:     false, // PUT can be idempotent but off by default for safety
			http.MethodPost:    false, // non-idempotent
			http.MethodDelete:  false, // off by default
			http.MethodPatch:   false, // non-idempotent
		},
		RetryableStatusCodes: map[int]bool{
			http.StatusBadGateway:          true, // 502
			http.StatusServiceUnavailable: true, // 503
			http.StatusGatewayTimeout:      true, // 504
		},
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// IsIdempotent reports whether the specified HTTP method is considered safe/idempotent to retry.
func (p *Policy) IsIdempotent(method string) bool {
	if p == nil || p.RetryableMethods == nil {
		return method == http.MethodGet || method == http.MethodHead
	}
	return p.RetryableMethods[method]
}

// ShouldRetry decides whether an attempt should be retried based on method,
// response status, error, and current attempt count.
// attempt is 0-indexed: attempt 0 is the first retry after initial failure.
func (p *Policy) ShouldRetry(method string, statusCode int, err error, attempt int) bool {
	if p == nil || attempt >= p.MaxRetries {
		return false
	}

	// Never retry non-idempotent methods unless explicitly configured
	if !p.IsIdempotent(method) {
		return false
	}

	// Transport-level error (connection refused, network timeout, reset) -> retry
	if err != nil {
		return true
	}

	// Upstream returned a transient status code (502, 503, 504) -> retry
	if p.RetryableStatusCodes != nil && p.RetryableStatusCodes[statusCode] {
		return true
	}

	return false
}

// BackoffDuration calculates the backoff duration for a given attempt with exponential backoff and jitter.
// attempt is 0-indexed (attempt 0 is the delay before the 1st retry).
func (p *Policy) BackoffDuration(attempt int) time.Duration {
	if p == nil || p.InitialBackoff <= 0 {
		return 0
	}

	multiplier := p.BackoffMultiplier
	if multiplier <= 1.0 {
		multiplier = 2.0
	}

	// Calculate base exponential delay: InitialBackoff * (multiplier ^ attempt)
	delay := float64(p.InitialBackoff)
	for i := 0; i < attempt; i++ {
		delay *= multiplier
		if p.MaxBackoff > 0 && delay > float64(p.MaxBackoff) {
			delay = float64(p.MaxBackoff)
			break
		}
	}

	if p.MaxBackoff > 0 && delay > float64(p.MaxBackoff) {
		delay = float64(p.MaxBackoff)
	}

	// Apply randomized jitter: delay ± (delay * JitterFraction * rand[-1.0, 1.0])
	jitterFraction := p.JitterFraction
	if jitterFraction > 0 {
		if jitterFraction > 1.0 {
			jitterFraction = 1.0
		}
		p.rngMu.Lock()
		if p.rng == nil {
			p.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
		}
		factor := 1.0 + (p.rng.Float64()*2.0-1.0)*jitterFraction
		p.rngMu.Unlock()

		delay *= factor
	}

	if delay < 0 {
		delay = 0
	}
	return time.Duration(delay)
}

// Sleep pauses for the calculated backoff duration, or returns immediately if ctx is cancelled.
func (p *Policy) Sleep(ctx context.Context, attempt int) error {
	duration := p.BackoffDuration(attempt)
	if duration <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

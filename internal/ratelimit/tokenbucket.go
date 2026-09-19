package ratelimit

import (
	"math"
	"sync"
	"time"
)

// bucket tracks token count and last update time for a single client key.
type bucket struct {
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
}

// TokenBucketLimiter implements an in-memory Token Bucket rate limiter with per-client tracking.
//
// Algorithm:
//   - Each client key receives a bucket that holds up to `capacity` tokens.
//   - Tokens refill continuously at `rate` tokens per second.
//   - When a request arrives, if `tokens >= requested`, tokens are decremented and the request is allowed.
//   - If insufficient tokens remain, the request is rejected with a calculated RetryAfter duration.
type TokenBucketLimiter struct {
	mu       sync.RWMutex
	buckets  map[string]*bucket
	rate     float64 // tokens per second
	capacity int     // maximum bucket capacity (burst)
	ttl      time.Duration

	stopCleanup chan struct{}
}

// NewTokenBucketLimiter creates a new TokenBucketLimiter.
//   - rate: refill rate in tokens per second (e.g. 50.0).
//   - capacity: maximum burst capacity (e.g. 100).
func NewTokenBucketLimiter(rate float64, capacity int) *TokenBucketLimiter {
	if rate <= 0 {
		rate = 1.0
	}
	if capacity <= 0 {
		capacity = 1
	}

	tbl := &TokenBucketLimiter{
		buckets:     make(map[string]*bucket),
		rate:        rate,
		capacity:    capacity,
		ttl:         5 * time.Minute,
		stopCleanup: make(chan struct{}),
	}

	// Start background cleanup goroutine to prevent memory leaks from inactive clients
	go tbl.cleanupLoop()

	return tbl
}

// Close stops the background cleanup goroutine.
func (tbl *TokenBucketLimiter) Close() {
	select {
	case <-tbl.stopCleanup:
	default:
		close(tbl.stopCleanup)
	}
}

func (tbl *TokenBucketLimiter) getBucket(key string) *bucket {
	tbl.mu.RLock()
	b, ok := tbl.buckets[key]
	tbl.mu.RUnlock()
	if ok {
		return b
	}

	tbl.mu.Lock()
	defer tbl.mu.Unlock()

	// Double-check after acquiring write lock
	if b, ok = tbl.buckets[key]; ok {
		return b
	}

	b = &bucket{
		tokens:     float64(tbl.capacity),
		lastRefill: time.Now(),
	}
	tbl.buckets[key] = b
	return b
}

// Allow checks if 1 token is available for the given key and consumes it.
func (tbl *TokenBucketLimiter) Allow(key string) Result {
	return tbl.AllowN(key, 1)
}

// AllowN checks if n tokens are available for the given key and consumes them.
func (tbl *TokenBucketLimiter) AllowN(key string, n int) Result {
	b := tbl.getBucket(key)

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.lastRefill = now

	// Refill tokens: tokens = min(capacity, tokens + elapsed * rate)
	b.tokens = math.Min(float64(tbl.capacity), b.tokens+(elapsed*tbl.rate))

	req := float64(n)
	if b.tokens >= req {
		b.tokens -= req
		remaining := int(math.Floor(b.tokens))
		resetIn := time.Duration(float64(tbl.capacity-remaining)/tbl.rate) * time.Second

		return Result{
			Allowed:    true,
			Limit:      tbl.capacity,
			Remaining:  remaining,
			RetryAfter: 0,
			ResetIn:    resetIn,
		}
	}

	// Insufficient tokens: calculate time required for 1 token to become available
	missing := req - b.tokens
	retryAfter := time.Duration(math.Ceil((missing / tbl.rate) * float64(time.Second)))
	resetIn := time.Duration(float64(tbl.capacity)/tbl.rate) * time.Second

	return Result{
		Allowed:    false,
		Limit:      tbl.capacity,
		Remaining:  0,
		RetryAfter: retryAfter,
		ResetIn:    resetIn,
	}
}

// cleanupLoop periodically sweeps inactive client buckets to reclaim memory.
func (tbl *TokenBucketLimiter) cleanupLoop() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-tbl.stopCleanup:
			return
		case now := <-ticker.C:
			tbl.mu.Lock()
			for k, b := range tbl.buckets {
				b.mu.Lock()
				idle := now.Sub(b.lastRefill)
				b.mu.Unlock()

				if idle > tbl.ttl {
					delete(tbl.buckets, k)
				}
			}
			tbl.mu.Unlock()
		}
	}
}

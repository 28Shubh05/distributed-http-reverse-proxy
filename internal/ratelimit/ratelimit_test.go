package ratelimit

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestTokenBucket_BurstCapacity(t *testing.T) {
	limiter := NewTokenBucketLimiter(10.0, 5) // 10 tokens/sec, burst 5
	defer limiter.Close()

	// 5 requests in burst should all be allowed
	for i := 0; i < 5; i++ {
		res := limiter.Allow("client-1")
		if !res.Allowed {
			t.Fatalf("request %d should be allowed within burst capacity", i+1)
		}
		if res.Limit != 5 {
			t.Errorf("expected limit 5, got %d", res.Limit)
		}
	}

	// 6th request immediately after burst should be rejected
	res := limiter.Allow("client-1")
	if res.Allowed {
		t.Fatalf("6th request should be rejected after burst exhaustion")
	}
	if res.RetryAfter <= 0 {
		t.Errorf("expected positive RetryAfter, got %v", res.RetryAfter)
	}
}

func TestTokenBucket_RefillRate_50ReqSec(t *testing.T) {
	// 50 tokens/sec -> 1 token every 20ms
	limiter := NewTokenBucketLimiter(50.0, 2)
	defer limiter.Close()

	// Exhaust burst
	limiter.Allow("c1")
	limiter.Allow("c1")
	if limiter.Allow("c1").Allowed {
		t.Fatalf("expected exhaustion")
	}

	// Wait 50ms -> should refill ~2.5 tokens (at least 2 tokens)
	time.Sleep(50 * time.Millisecond)

	res := limiter.Allow("c1")
	if !res.Allowed {
		t.Fatalf("expected request to be allowed after 50ms refill")
	}
}

func TestTokenBucket_RefillRate_100ReqSec(t *testing.T) {
	// 100 tokens/sec -> 1 token every 10ms
	limiter := NewTokenBucketLimiter(100.0, 5)
	defer limiter.Close()

	// Exhaust
	for i := 0; i < 5; i++ {
		limiter.Allow("c1")
	}
	if limiter.Allow("c1").Allowed {
		t.Fatalf("expected exhaustion")
	}

	// Wait 25ms -> should refill at least 2 tokens
	time.Sleep(25 * time.Millisecond)

	res1 := limiter.Allow("c1")
	if !res1.Allowed {
		t.Fatalf("expected token 1 to be refilled after 25ms")
	}
	res2 := limiter.Allow("c1")
	if !res2.Allowed {
		t.Fatalf("expected token 2 to be refilled after 25ms")
	}
}

func TestTokenBucket_PerClientIsolation(t *testing.T) {
	limiter := NewTokenBucketLimiter(10.0, 2)
	defer limiter.Close()

	// Exhaust client-A
	limiter.Allow("client-A")
	limiter.Allow("client-A")
	if limiter.Allow("client-A").Allowed {
		t.Fatalf("expected client-A to be exhausted")
	}

	// Client-B should have independent tokens and be allowed
	resB := limiter.Allow("client-B")
	if !resB.Allowed {
		t.Fatalf("client-B should be allowed independently of client-A")
	}
}

func TestTokenBucket_Concurrency(t *testing.T) {
	limiter := NewTokenBucketLimiter(100.0, 50)
	defer limiter.Close()

	var wg sync.WaitGroup
	var allowedCount int32
	var mu sync.Mutex

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := "client-shared"
			res := limiter.Allow(key)
			if res.Allowed {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	// Initial burst is 50, so allowedCount should be at least 50 and bounded
	if allowedCount < 50 || allowedCount > 60 {
		t.Errorf("expected approximately 50 allowed requests, got %d", allowedCount)
	}
}

func isRedisAvailable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func TestRedisLimiter_FallbackOnUnreachable(t *testing.T) {
	// Dead redis client on unused port
	rdb := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:54321",
		DialTimeout: 50 * time.Millisecond,
		ReadTimeout: 50 * time.Millisecond,
	})
	defer rdb.Close()

	// Rate of 0.5 tokens/sec (1 token every 2 seconds) with capacity 3
	limiter := NewRedisLimiter(rdb, 0.5, 3, "test_rl:", nil)
	defer limiter.Close()

	// Fallback in-memory should allow up to 3 burst requests
	for i := 0; i < 3; i++ {
		res := limiter.Allow("fb-client")
		if !res.Allowed {
			t.Fatalf("fallback should allow request %d", i+1)
		}
	}

	// 4th request within 1s should be rate-limited by fallback (refill needs 2s)
	res := limiter.Allow("fb-client")
	if res.Allowed {
		t.Fatalf("fallback should rate-limit 4th request")
	}
}

func TestRedisLimiter_LiveIntegration(t *testing.T) {
	addr := "localhost:6379"
	if !isRedisAvailable(addr) {
		t.Skip("skipping Redis rate limiter test: no local Redis server running on", addr)
	}

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("skipping Redis rate limiter test: ping failed:", err)
	}

	limiter := NewRedisLimiter(rdb, 10.0, 3, "test_live_rl:", nil)
	defer limiter.Close()

	key := "live_client"
	_ = rdb.Del(ctx, "test_live_rl:"+key).Err()

	// Burst 3 allowed
	for i := 0; i < 3; i++ {
		res := limiter.Allow(key)
		if !res.Allowed {
			t.Fatalf("live redis: request %d should be allowed", i+1)
		}
	}

	// 4th rejected
	res := limiter.Allow(key)
	if res.Allowed {
		t.Fatalf("live redis: 4th request should be rate limited")
	}
	if res.RetryAfter <= 0 {
		t.Errorf("live redis: expected positive RetryAfter, got %v", res.RetryAfter)
	}
}

package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// tokenBucketLuaScript performs an atomic token bucket refill and deduction inside Redis.
//
// KEYS[1]: rate limit key, e.g. "ratelimit:<client_ip>"
// ARGV[1]: capacity (number of tokens maximum)
// ARGV[2]: refill_rate (tokens per second)
// ARGV[3]: now (Unix timestamp in seconds with fractional precision)
// ARGV[4]: requested tokens (e.g. 1)
const tokenBucketLuaScript = `
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])

-- Retrieve current tokens and last update timestamp
local data = redis.call('HMGET', key, 'tokens', 'last_updated')
local tokens = tonumber(data[1])
local last_updated = tonumber(data[2])

if not tokens then
    tokens = capacity
    last_updated = now
else
    local elapsed = math.max(0, now - last_updated)
    tokens = math.min(capacity, tokens + (elapsed * refill_rate))
    last_updated = now
end

local allowed = 0
local remaining = 0
local retry_after = 0

if tokens >= requested then
    allowed = 1
    tokens = tokens - requested
    remaining = math.floor(tokens)
else
    allowed = 0
    remaining = 0
    retry_after = (requested - tokens) / refill_rate
end

redis.call('HMSET', key, 'tokens', tostring(tokens), 'last_updated', tostring(last_updated))

-- Auto-expire key after time needed to fully refill * 2 (min 60s) to reclaim memory
local ttl = math.ceil(capacity / refill_rate) * 2
if ttl < 60 then ttl = 60 end
redis.call('EXPIRE', key, ttl)

return { allowed, remaining, tostring(retry_after) }
`

// RedisLimiter implements Limiter using Redis and an atomic Lua script,
// allowing multiple distributed proxy instances to share rate limit state.
type RedisLimiter struct {
	client   redis.Cmdable
	rate     float64
	capacity int
	prefix   string
	fallback *TokenBucketLimiter
	logger   *slog.Logger
	script   *redis.Script
}

// NewRedisLimiter creates a new Redis-backed rate limiter.
// If Redis fails, it seamlessly falls back to an in-memory TokenBucketLimiter.
func NewRedisLimiter(client redis.Cmdable, rate float64, capacity int, prefix string, logger *slog.Logger) *RedisLimiter {
	if rate <= 0 {
		rate = 1.0
	}
	if capacity <= 0 {
		capacity = 1
	}
	if prefix == "" {
		prefix = "ratelimit:"
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &RedisLimiter{
		client:   client,
		rate:     rate,
		capacity: capacity,
		prefix:   prefix,
		fallback: NewTokenBucketLimiter(rate, capacity),
		logger:   logger,
		script:   redis.NewScript(tokenBucketLuaScript),
	}
}

// Allow checks if 1 token is available for key and consumes it.
func (rl *RedisLimiter) Allow(key string) Result {
	return rl.AllowN(key, 1)
}

// AllowN checks if n tokens are available for key and consumes them.
func (rl *RedisLimiter) AllowN(key string, n int) Result {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	nowSec := float64(time.Now().UnixNano()) / 1e9
	redisKey := rl.prefix + key

	res, err := rl.script.Run(ctx, rl.client, []string{redisKey},
		rl.capacity,
		rl.rate,
		fmt.Sprintf("%.6f", nowSec),
		n,
	).Slice()

	if err != nil {
		rl.logger.Warn("redis rate limiter error, using in-memory fallback", "key", key, "err", err)
		return rl.fallback.AllowN(key, n)
	}

	allowedInt, _ := res[0].(int64)
	remainingInt, _ := res[1].(int64)
	retryAfterStr, _ := res[2].(string)

	retryAfterSec, _ := strconv.ParseFloat(retryAfterStr, 64)
	retryAfter := time.Duration(retryAfterSec * float64(time.Second))
	resetIn := time.Duration(float64(rl.capacity-int(remainingInt))/rl.rate) * time.Second

	return Result{
		Allowed:    allowedInt == 1,
		Limit:      rl.capacity,
		Remaining:  int(remainingInt),
		RetryAfter: retryAfter,
		ResetIn:    resetIn,
	}
}

// Close closes the in-memory fallback limiter.
func (rl *RedisLimiter) Close() {
	if rl.fallback != nil {
		rl.fallback.Close()
	}
}

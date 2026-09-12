package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisConfig holds configuration for the Redis cache client.
type RedisConfig struct {
	// Addr is the Redis server address, e.g. "localhost:6379".
	Addr string

	// Password for Redis authentication (optional).
	Password string

	// DB is the Redis database index (default: 0).
	DB int

	// KeyPrefix is prepended to all cache keys to avoid namespace collisions.
	// Default: "proxy:cache:"
	KeyPrefix string

	// DialTimeout is the timeout for establishing connections to Redis.
	DialTimeout time.Duration

	// ReadTimeout is the timeout for socket reads.
	ReadTimeout time.Duration

	// WriteTimeout is the timeout for socket writes.
	WriteTimeout time.Duration
}

// DefaultRedisConfig provides sensible production defaults for Redis.
func DefaultRedisConfig() RedisConfig {
	return RedisConfig{
		Addr:         "localhost:6379",
		Password:     "",
		DB:           0,
		KeyPrefix:    "proxy:cache:",
		DialTimeout:  2 * time.Second,
		ReadTimeout:  1 * time.Second,
		WriteTimeout: 1 * time.Second,
	}
}

// RedisCache is a distributed cache implementation backed by Redis.
// It is safe for concurrent use across multiple proxy instances.
type RedisCache struct {
	client redis.Cmdable
	prefix string
	logger *slog.Logger

	hits   atomic.Int64
	misses atomic.Int64
}

// NewRedisCache creates a new RedisCache with the given Redis client.
func NewRedisCache(client redis.Cmdable, prefix string, logger *slog.Logger) *RedisCache {
	if prefix == "" {
		prefix = "proxy:cache:"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisCache{
		client: client,
		prefix: prefix,
		logger: logger,
	}
}

// NewRedisCacheFromConfig initializes a redis.Client and returns a RedisCache.
func NewRedisCacheFromConfig(cfg RedisConfig, logger *slog.Logger) (*RedisCache, error) {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 2 * time.Second
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 1 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 1 * time.Second
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	// Quick ping to check connection availability
	ctx, cancel := context.WithTimeout(context.Background(), cfg.DialTimeout)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis at %s: %w", cfg.Addr, err)
	}

	return NewRedisCache(rdb, cfg.KeyPrefix, logger), nil
}

func (r *RedisCache) formatKey(key string) string {
	return r.prefix + key
}

// Get retrieves a response from Redis.
// If Redis is unreachable or the key does not exist, it safely records a miss
// and returns (nil, false) without causing proxy failure.
func (r *RedisCache) Get(key string) (*CachedResponse, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	val, err := r.client.Get(ctx, r.formatKey(key)).Bytes()
	if err != nil {
		r.misses.Add(1)
		if !errors.Is(err, redis.Nil) {
			r.logger.Warn("redis cache read error", "key", key, "err", err)
		}
		return nil, false
	}

	var resp CachedResponse
	if err := json.Unmarshal(val, &resp); err != nil {
		r.misses.Add(1)
		r.logger.Warn("redis cache unmarshal error", "key", key, "err", err)
		return nil, false
	}

	r.hits.Add(1)
	return &resp, true
}

// Set stores a response in Redis with the given TTL.
func (r *RedisCache) Set(key string, resp *CachedResponse, ttl time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal cached response: %w", err)
	}

	if err := r.client.Set(ctx, r.formatKey(key), data, ttl).Err(); err != nil {
		r.logger.Warn("redis cache write error", "key", key, "err", err)
		return err
	}

	return nil
}

// Delete removes an entry from Redis by key.
func (r *RedisCache) Delete(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	return r.client.Del(ctx, r.formatKey(key)).Err()
}

// Flush removes all cache keys matching the prefix.
func (r *RedisCache) Flush() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var cursor uint64
	match := r.prefix + "*"

	for {
		keys, nextCursor, err := r.client.Scan(ctx, cursor, match, 100).Result()
		if err != nil {
			return err
		}

		if len(keys) > 0 {
			if err := r.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return nil
}

// Stats returns a snapshot of cache metrics.
func (r *RedisCache) Stats() Stats {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var size int64
	dbsize, err := r.client.DBSize(ctx).Result()
	if err == nil {
		size = dbsize
	}

	return Stats{
		Hits:   r.hits.Load(),
		Misses: r.misses.Load(),
		Size:   size,
	}
}

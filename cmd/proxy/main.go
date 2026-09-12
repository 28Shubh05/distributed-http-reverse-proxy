// cmd/proxy is the entry point for the reverse proxy server.
//
// Day 6 additions:
//   - Multiple backends (comma separated)
//   - Round Robin load balancer
//   - Active health checker
//   - Passive failure detection
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/cache"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/circuitbreaker"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/connectionpool"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/health"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/loadbalancer"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/middleware"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/proxy"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/retry"
)

func main() {
	port := flag.String("port", "8080", "HTTP proxy listen port")
	backendsFlag := flag.String("backends", "http://localhost:9001", "Comma-separated list of backend URLs")
	backendTimeout := flag.Duration("backend-timeout", 30*time.Second, "Max time to wait for backend response")
	maxRetries := flag.Int("max-retries", 2, "Max retry attempts on failover for idempotent requests")
	enableCache := flag.Bool("cache", true, "Enable HTTP response caching")
	cacheTTL := flag.Duration("cache-ttl", 60*time.Second, "TTL for cached HTTP responses")
	cacheSize := flag.Int("cache-size", 1000, "Maximum entries in in-memory LRU cache")
	redisAddr := flag.String("redis-addr", "", "Redis address (e.g. localhost:6379), leave empty for in-memory cache")
	maxIdleConns := flag.Int("max-idle-conns", 100, "Max idle connections in pool")
	maxIdlePerHost := flag.Int("max-idle-per-host", 20, "Max idle connections per backend host")
	noPool := flag.Bool("no-pool", false, "Disable connection pooling (for benchmarking)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger, runConfig{
		port:           *port,
		backendsFlag:   *backendsFlag,
		backendTimeout: *backendTimeout,
		maxRetries:     *maxRetries,
		enableCache:    *enableCache,
		cacheTTL:       *cacheTTL,
		cacheSize:      *cacheSize,
		redisAddr:      *redisAddr,
		maxIdleConns:   *maxIdleConns,
		maxIdlePerHost: *maxIdlePerHost,
		noPool:         *noPool,
	}); err != nil {
		logger.Error("proxy failed", "error", err)
		os.Exit(1)
	}
}

type runConfig struct {
	port           string
	backendsFlag   string
	backendTimeout time.Duration
	maxRetries     int
	enableCache    bool
	cacheTTL       time.Duration
	cacheSize      int
	redisAddr      string
	maxIdleConns   int
	maxIdlePerHost int
	noPool         bool
}

func run(ctx context.Context, logger *slog.Logger, cfg runConfig) error {
	// --- Connection pool ---
	var poolCfg connectionpool.Config
	if cfg.noPool {
		poolCfg = connectionpool.NoPool()
		logger.Info("connection pooling disabled (benchmark mode)")
	} else {
		poolCfg = connectionpool.Default()
		poolCfg.MaxIdleConns = cfg.maxIdleConns
		poolCfg.MaxIdleConnsPerHost = cfg.maxIdlePerHost
	}
	transport := connectionpool.New(poolCfg)

	// --- Load Balancer & Health Checker ---
	lb := loadbalancer.NewRoundRobin()
	healthCfg := health.DefaultConfig()
	healthChecker := health.NewChecker(healthCfg, logger)

	backendURLs := strings.Split(cfg.backendsFlag, ",")
	for i, bURL := range backendURLs {
		bURL = strings.TrimSpace(bURL)
		if bURL == "" {
			continue
		}
		
		parsedURL, err := url.Parse(bURL)
		if err != nil {
			return fmt.Errorf("invalid backend URL %q: %w", bURL, err)
		}

		id := fmt.Sprintf("backend-%d", i+1)
		bh := health.NewBackendHealth(healthCfg)
		cb := circuitbreaker.New(circuitbreaker.DefaultConfig())
		
		backend := &loadbalancer.Backend{
			ID:             id,
			URL:            parsedURL,
			Weight:         1,
			Health:         bh,
			CircuitBreaker: cb,
		}
		
		lb.AddBackend(backend)
		healthChecker.Register(id, bURL, bh)
	}

	// Start health checker background goroutines
	healthChecker.Start(ctx)

	// --- Retry Policy ---
	retryPolicy := retry.DefaultPolicy()
	retryPolicy.MaxRetries = cfg.maxRetries

	// --- Proxy ---
	rp, err := proxy.New(proxy.Config{
		BackendTimeout: cfg.backendTimeout,
		RetryPolicy:    retryPolicy,
	}, lb, transport, logger)
	if err != nil {
		return fmt.Errorf("failed to create reverse proxy: %w", err)
	}

	// --- Response Cache ---
	var cacheBackend cache.Cache
	if cfg.enableCache {
		if cfg.redisAddr != "" {
			redisCfg := cache.DefaultRedisConfig()
			redisCfg.Addr = cfg.redisAddr
			rc, err := cache.NewRedisCacheFromConfig(redisCfg, logger)
			if err != nil {
				logger.Warn("failed to connect to redis, falling back to in-memory cache", "err", err)
				cacheBackend = cache.NewMemoryCache(cfg.cacheSize)
			} else {
				cacheBackend = rc
				logger.Info("connected to redis distributed cache", "addr", cfg.redisAddr)
			}
		} else {
			cacheBackend = cache.NewMemoryCache(cfg.cacheSize)
			logger.Info("using in-memory LRU response cache", "max_size", cfg.cacheSize)
		}
	}

	// --- Middleware ---
	var requestCount atomic.Uint64
	middlewares := []middleware.MiddlewareFunc{
		middleware.RequestID,
		middleware.Logging(logger, &requestCount),
	}
	if cacheBackend != nil {
		middlewares = append(middlewares, middleware.Caching(cacheBackend, middleware.CacheConfig{
			TTL:                cfg.cacheTTL,
			InvalidateOnMutate: true,
		}))
	}
	handler := middleware.Chain(rp, middlewares...)

	// --- HTTP server ---
	addr := ":" + cfg.port
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
		// Client-facing timeouts (how long we wait for the client)
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second, // must be >= BackendTimeout
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("reverse proxy started",
			"addr", addr,
			"backends", cfg.backendsFlag,
			"backend_timeout", cfg.backendTimeout,
			"cache_enabled", cfg.enableCache,
			"cache_ttl", cfg.cacheTTL,
			"pool_max_idle", poolCfg.MaxIdleConns,
			"pool_max_idle_per_host", poolCfg.MaxIdleConnsPerHost,
			"pid", os.Getpid(),
		)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
	}

	// --- Graceful shutdown ---
	logger.Info("shutting down proxy...",
		"total_requests", requestCount.Load(),
	)
	if cacheBackend != nil {
		stats := cacheBackend.Stats()
		logger.Info("cache metrics on shutdown",
			"hits", stats.Hits,
			"misses", stats.Misses,
			"size", stats.Size,
			"evictions", stats.Evictions,
		)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	// Close idle connections in the transport pool.
	transport.CloseIdleConnections()
	logger.Info("proxy stopped gracefully")
	return nil
}

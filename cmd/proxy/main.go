// cmd/proxy is the entry point for the reverse proxy server.
//
// Day 4 additions:
//   - Connection pool via internal/connectionpool
//   - Per-request backend timeout flag
//   - Pool stats logged on shutdown
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/connectionpool"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/middleware"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/proxy"
)

func main() {
	port := flag.String("port", "8080", "HTTP proxy listen port")
	backendURL := flag.String("backend", "http://localhost:9001", "Backend URL to proxy to")
	backendTimeout := flag.Duration("backend-timeout", 30*time.Second, "Max time to wait for backend response")
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
		backendURL:     *backendURL,
		backendTimeout: *backendTimeout,
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
	backendURL     string
	backendTimeout time.Duration
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

	// --- Proxy ---
	rp, err := proxy.New(proxy.Config{
		BackendURL:     cfg.backendURL,
		BackendTimeout: cfg.backendTimeout,
	}, transport, logger)
	if err != nil {
		return fmt.Errorf("failed to create reverse proxy: %w", err)
	}

	// --- Middleware ---
	var requestCount atomic.Uint64
	handler := middleware.Chain(
		rp,
		middleware.RequestID,
		middleware.Logging(logger, &requestCount),
	)

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
			"backend", cfg.backendURL,
			"backend_timeout", cfg.backendTimeout,
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

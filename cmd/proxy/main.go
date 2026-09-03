package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/proxy"
)

func main() {
	port := flag.String("port", "8080", "HTTP proxy listen port")
	backendURL := flag.String("backend", "http://localhost:9001", "Backend URL to proxy to")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger, *port, *backendURL); err != nil {
		logger.Error("proxy failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, port, backendURL string) error {
	rp, err := proxy.NewReverseProxy(backendURL, logger)
	if err != nil {
		return fmt.Errorf("failed to create reverse proxy: %w", err)
	}

	addr := ":" + port
	server := &http.Server{
		Addr:              addr,
		Handler:           rp,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("reverse proxy started",
			"addr", addr,
			"backend", backendURL,
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

	logger.Info("shutting down proxy server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	logger.Info("proxy server stopped gracefully")
	return nil
}

// Package main implements a standalone Admin CLI tool and control plane server
// for inspecting backends, triggering dynamic configuration reloads, and managing routing policies.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/admin"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/storage"
)

func main() {
	mode := flag.String("mode", "server", "Mode of operation: 'server' or 'client'")
	port := flag.String("port", "8081", "Admin server HTTP port (in server mode)")
	token := flag.String("token", "secret-admin-token", "Admin authentication token")
	postgresURL := flag.String("postgres-url", "", "PostgreSQL connection string (optional)")
	adminURL := flag.String("admin-url", "http://localhost:8081", "Target admin API URL (in client mode)")
	command := flag.String("cmd", "stats", "Command in client mode: stats, backends, health, config, reload")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *mode == "client" {
		runClient(ctx, *adminURL, *token, *command)
		return
	}

	if err := runServer(ctx, logger, *port, *token, *postgresURL); err != nil {
		logger.Error("admin server failed", "err", err)
		os.Exit(1)
	}
}

func runClient(ctx context.Context, adminURL, token, cmd string) {
	path := "/admin/" + cmd
	reqURL := adminURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if cmd == "reload" {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating request: %v\n", err)
		os.Exit(1)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("[%d %s]\n%s\n", resp.StatusCode, resp.Status, string(body))
}

func runServer(ctx context.Context, logger *slog.Logger, port, token, postgresURL string) error {
	var store storage.Store
	if postgresURL != "" {
		ps, err := storage.NewPostgresStore(postgresURL)
		if err != nil {
			logger.Warn("failed to connect to postgresql, falling back to in-memory store", "err", err)
			store = storage.NewMemoryStore()
		} else {
			store = ps
			logger.Info("connected to postgresql storage backend")
		}
	} else {
		store = storage.NewMemoryStore()
		logger.Info("using in-memory storage backend")
	}
	defer store.Close()

	adminServer := admin.NewServer(admin.Config{
		Token:  token,
		Store:  store,
		Logger: logger,
	})

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           adminServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("admin server started", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

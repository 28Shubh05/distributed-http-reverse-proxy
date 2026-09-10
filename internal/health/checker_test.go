package health

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// testLogger returns a no-op structured logger for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestChecker_ActiveHealthCheck_Healthy verifies that the checker marks a backend
// HEALTHY when the health endpoint returns 200.
func TestChecker_ActiveHealthCheck_Healthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := Config{
		Interval:           50 * time.Millisecond,
		Timeout:            1 * time.Second,
		HealthPath:         "/health",
		UnhealthyThreshold: 3,
		RecoveryThreshold:  2,
	}
	bh := NewBackendHealth(cfg)
	checker := NewChecker(cfg, testLogger())
	checker.Register("test-backend", server.URL, bh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker.Start(ctx)

	// After a short time the backend should still be HEALTHY.
	time.Sleep(200 * time.Millisecond)
	if bh.Status() != StatusHealthy {
		t.Fatalf("expected HEALTHY, got %s", bh.Status())
	}
}

// TestChecker_ActiveHealthCheck_Unhealthy verifies that repeated 500 responses
// drive the backend to UNHEALTHY.
func TestChecker_ActiveHealthCheck_Unhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := Config{
		Interval:           30 * time.Millisecond,
		Timeout:            1 * time.Second,
		HealthPath:         "/health",
		UnhealthyThreshold: 3,
		RecoveryThreshold:  2,
	}
	bh := NewBackendHealth(cfg)
	checker := NewChecker(cfg, testLogger())
	checker.Register("test-backend", server.URL, bh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker.Start(ctx)

	// Wait long enough for 3 failures (3 * 30ms + buffer).
	time.Sleep(300 * time.Millisecond)
	if bh.Status() != StatusUnhealthy {
		t.Fatalf("expected UNHEALTHY after repeated 500s, got %s", bh.Status())
	}
	if bh.IsAvailable() {
		t.Fatal("UNHEALTHY backend should not be available")
	}
}

// TestChecker_Recovery verifies that after going UNHEALTHY the checker
// transitions back to HEALTHY once the backend returns 200.
func TestChecker_Recovery(t *testing.T) {
	var returnError atomic.Bool
	returnError.Store(true) // start failing

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if returnError.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	cfg := Config{
		Interval:           30 * time.Millisecond,
		Timeout:            1 * time.Second,
		HealthPath:         "/health",
		UnhealthyThreshold: 3,
		RecoveryThreshold:  2,
	}
	bh := NewBackendHealth(cfg)
	checker := NewChecker(cfg, testLogger())
	checker.Register("test-backend", server.URL, bh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker.Start(ctx)

	// Let it go UNHEALTHY.
	time.Sleep(200 * time.Millisecond)
	if bh.Status() != StatusUnhealthy {
		t.Fatalf("expected UNHEALTHY, got %s", bh.Status())
	}

	// Flip the backend to healthy responses.
	returnError.Store(false)

	// Wait for recovery (2 successes * 30ms + buffer).
	time.Sleep(300 * time.Millisecond)
	if bh.Status() != StatusHealthy {
		t.Fatalf("expected HEALTHY after recovery, got %s (snapshot: %+v)", bh.Status(), bh.Snapshot())
	}
}

// TestChecker_ConnectionRefused verifies that a backend that refuses connections
// is correctly marked UNHEALTHY.
func TestChecker_ConnectionRefused(t *testing.T) {
	cfg := Config{
		Interval:           30 * time.Millisecond,
		Timeout:            200 * time.Millisecond,
		HealthPath:         "/health",
		UnhealthyThreshold: 3,
		RecoveryThreshold:  2,
	}
	bh := NewBackendHealth(cfg)
	checker := NewChecker(cfg, testLogger())
	// Use a port that is (almost certainly) not listening.
	checker.Register("dead-backend", "http://127.0.0.1:1", bh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker.Start(ctx)

	// 3 failures * (30ms interval + 200ms timeout) + buffer.
	time.Sleep(800 * time.Millisecond)
	if bh.Status() != StatusUnhealthy {
		t.Fatalf("expected UNHEALTHY for refused connection, got %s", bh.Status())
	}
}

// TestChecker_HealthURL verifies the URL helper.
func TestChecker_HealthURL(t *testing.T) {
	cfg := DefaultConfig()
	checker := NewChecker(cfg, testLogger())
	bh := NewBackendHealth(cfg)
	checker.Register("b1", "http://localhost:9001", bh)

	url, err := checker.HealthURL("b1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := fmt.Sprintf("http://localhost:9001%s", cfg.HealthPath)
	if url != want {
		t.Fatalf("expected %q, got %q", want, url)
	}

	_, err = checker.HealthURL("nonexistent")
	if err == nil {
		t.Fatal("expected error for unregistered backend, got nil")
	}
}

// TestChecker_Deregister verifies that deregistering a backend stops future
// registration lookup.
func TestChecker_Deregister(t *testing.T) {
	cfg := DefaultConfig()
	checker := NewChecker(cfg, testLogger())
	bh := NewBackendHealth(cfg)
	checker.Register("b1", "http://localhost:9001", bh)
	checker.Deregister("b1")

	_, err := checker.HealthURL("b1")
	if err == nil {
		t.Fatal("expected error after deregister, got nil")
	}
}

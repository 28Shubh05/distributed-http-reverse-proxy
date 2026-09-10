package health

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// entry holds a registered backend inside the Checker.
type entry struct {
	id         string
	healthURL  string // e.g. "http://backend-1:9001/health"
	bh         *BackendHealth
}

// Checker runs active health checks against registered backends.
// It fires a goroutine per backend on a configurable interval and updates the
// BackendHealth state accordingly.
//
// Usage:
//
//	checker := health.NewChecker(cfg, logger)
//	checker.Register("backend-1", "http://localhost:9001", bh1)
//	checker.Register("backend-2", "http://localhost:9002", bh2)
//
//	ctx, cancel := context.WithCancel(context.Background())
//	checker.Start(ctx)
//	// ...
//	cancel() // graceful shutdown
type Checker struct {
	mu       sync.RWMutex
	entries  map[string]*entry
	cfg      Config
	client   *http.Client
	logger   *slog.Logger
}

// NewChecker creates a Checker with the given config and logger.
// A dedicated http.Client is used so health-check traffic does not share the
// proxy's connection pool.
func NewChecker(cfg Config, logger *slog.Logger) *Checker {
	return &Checker{
		entries: make(map[string]*entry),
		cfg:     cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		logger: logger,
	}
}

// Register adds a backend to the active health checker.
// backendBaseURL is the root URL of the backend (e.g. "http://localhost:9001").
// The health check path from cfg.HealthPath is appended automatically.
// bh is the BackendHealth instance that will be updated on every check.
func (c *Checker) Register(id, backendBaseURL string, bh *BackendHealth) {
	healthURL := backendBaseURL + c.cfg.HealthPath

	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[id] = &entry{
		id:        id,
		healthURL: healthURL,
		bh:        bh,
	}
	c.logger.Info("registered backend for health checking",
		"backend", id,
		"health_url", healthURL,
	)
}

// Deregister removes a backend from the health checker.
func (c *Checker) Deregister(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
	c.logger.Info("deregistered backend from health checker", "backend", id)
}

// Start launches a background goroutine for each registered backend and runs
// until ctx is cancelled.
// Newly registered backends (via Register) after Start is called will NOT
// automatically get their own goroutine. Call Start again or use a watcher.
// This is sufficient for a static backend list (Day 5/6); dynamic registration
// (etcd) is added in Day 10.
func (c *Checker) Start(ctx context.Context) {
	c.mu.RLock()
	entries := make([]*entry, 0, len(c.entries))
	for _, e := range c.entries {
		entries = append(entries, e)
	}
	c.mu.RUnlock()

	for _, e := range entries {
		go c.runLoop(ctx, e)
	}
}

// runLoop polls one backend at the configured interval until ctx is cancelled.
func (c *Checker) runLoop(ctx context.Context, e *entry) {
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	c.logger.Info("starting active health check loop",
		"backend", e.id,
		"interval", c.cfg.Interval,
	)

	// Run an immediate check on startup so we don't wait a full interval.
	c.check(ctx, e)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("health check loop stopped", "backend", e.id)
			return
		case <-ticker.C:
			c.check(ctx, e)
		}
	}
}

// check performs a single active health check for backend e.
func (c *Checker) check(ctx context.Context, e *entry) {
	reqCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, e.healthURL, nil)
	if err != nil {
		// Malformed URL — treat as permanent failure.
		c.logger.Error("failed to build health check request",
			"backend", e.id,
			"err", err,
		)
		e.bh.RecordFailure()
		return
	}

	resp, err := c.client.Do(req)
	if err != nil {
		e.bh.RecordFailure()
		c.logger.Warn("active health check failed",
			"backend", e.id,
			"health_url", e.healthURL,
			"err", err,
			"status", e.bh.Status().String(),
		)
		return
	}
	resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		e.bh.RecordFailure()
		c.logger.Warn("active health check returned non-2xx",
			"backend", e.id,
			"http_status", resp.StatusCode,
			"status", e.bh.Status().String(),
		)
		return
	}

	prevStatus := e.bh.Status()
	e.bh.RecordSuccess()
	newStatus := e.bh.Status()

	if prevStatus != newStatus {
		c.logger.Info("backend health status changed",
			"backend", e.id,
			"from", prevStatus.String(),
			"to", newStatus.String(),
		)
	} else {
		c.logger.Debug("active health check passed",
			"backend", e.id,
			"http_status", resp.StatusCode,
		)
	}
}

// HealthURL returns the full health check URL for a registered backend.
// Returns an error if the backend is not registered.
func (c *Checker) HealthURL(id string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[id]
	if !ok {
		return "", fmt.Errorf("backend %q not registered", id)
	}
	return e.healthURL, nil
}

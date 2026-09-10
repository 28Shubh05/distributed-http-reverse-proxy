// Package health implements active health checking and passive failure detection
// for backend servers.
//
// # Active vs Passive Health Checking
//
// Active health checking means the proxy proactively sends periodic HTTP requests
// (e.g. GET /health) to every backend, regardless of real traffic. This lets the
// proxy detect failures quickly, even when the backend is idle. The downside is
// that it consumes resources and can produce false positives if the /health
// endpoint does not reflect real backend capacity.
//
// Passive failure detection (also called passive health checking) means the proxy
// observes real request outcomes. If an actual proxied request fails (connection
// refused, timeout, 5xx), the proxy records that failure against the backend.
// Passive detection is "free" — it piggybacks on traffic — but it can only detect
// failures when requests are flowing. A backend that receives no traffic will not
// be marked unhealthy through passive detection alone.
//
// This package implements both mechanisms:
//   - [Checker] runs active health checks in the background.
//   - [BackendHealth.RecordFailure] / [BackendHealth.RecordSuccess] are called by
//     the proxy layer for passive detection.
//
// # State machine
//
//	HEALTHY  ──(threshold failures)──► UNHEALTHY
//	UNHEALTHY──(threshold successes)──► RECOVERING
//	RECOVERING──(threshold successes)──► HEALTHY
//
// The RECOVERING state is an intermediate state where the backend must sustain
// a consecutive run of successful health checks before being fully promoted back
// to HEALTHY. This avoids flapping when a backend oscillates around its failure
// threshold.
package health

import (
	"sync"
	"sync/atomic"
	"time"
)

// Status represents the health state of a backend.
type Status int32

const (
	// StatusHealthy means the backend is operating normally and can receive traffic.
	StatusHealthy Status = iota

	// StatusUnhealthy means the backend has exceeded the failure threshold.
	// No traffic is routed to it.
	StatusUnhealthy

	// StatusRecovering means the backend is responding again but has not yet
	// sustained enough consecutive successes to be considered fully healthy.
	StatusRecovering
)

// String returns a human-readable name for the status.
func (s Status) String() string {
	switch s {
	case StatusHealthy:
		return "HEALTHY"
	case StatusUnhealthy:
		return "UNHEALTHY"
	case StatusRecovering:
		return "RECOVERING"
	default:
		return "UNKNOWN"
	}
}

// Config holds the thresholds and timing configuration for a health checker.
type Config struct {
	// Interval is how often active health checks are performed.
	// Default: 10 seconds.
	Interval time.Duration

	// Timeout is the HTTP timeout for each health check request.
	// Default: 5 seconds.
	Timeout time.Duration

	// HealthPath is the HTTP path to GET for the health check.
	// Default: "/health".
	HealthPath string

	// UnhealthyThreshold is the number of consecutive failures before a
	// HEALTHY backend is marked UNHEALTHY.
	// Default: 3.
	UnhealthyThreshold int

	// RecoveryThreshold is the number of consecutive successes a backend must
	// sustain while in UNHEALTHY or RECOVERING state before being promoted to
	// HEALTHY.
	// Default: 2.
	RecoveryThreshold int
}

// DefaultConfig returns a Config populated with safe defaults.
func DefaultConfig() Config {
	return Config{
		Interval:           10 * time.Second,
		Timeout:            5 * time.Second,
		HealthPath:         "/health",
		UnhealthyThreshold: 3,
		RecoveryThreshold:  2,
	}
}

// BackendHealth tracks the health state and statistics for a single backend.
// All exported methods are safe for concurrent use.
type BackendHealth struct {
	mu sync.Mutex

	// status is stored as int32 so atomic reads are cheap for the hot path.
	status atomic.Int32

	consecutiveFailures  int
	consecutiveSuccesses int

	LastCheck   time.Time
	LastSuccess time.Time
	LastFailure time.Time

	// TotalChecks is the total number of health checks (active + passive) performed.
	TotalChecks  int64
	TotalSuccess int64
	TotalFailure int64

	cfg Config
}

// NewBackendHealth creates a BackendHealth starting in the HEALTHY state.
func NewBackendHealth(cfg Config) *BackendHealth {
	bh := &BackendHealth{cfg: cfg}
	bh.status.Store(int32(StatusHealthy))
	return bh
}

// Status returns the current health status.
// This is a lock-free atomic read for use on the hot path.
func (bh *BackendHealth) Status() Status {
	return Status(bh.status.Load())
}

// IsAvailable returns true if the backend can receive traffic.
// HEALTHY and RECOVERING backends are considered available.
// UNHEALTHY backends are excluded from routing.
func (bh *BackendHealth) IsAvailable() bool {
	s := bh.Status()
	return s == StatusHealthy || s == StatusRecovering
}

// RecordSuccess records one successful health check (active or passive) and
// transitions state if the recovery threshold is met.
func (bh *BackendHealth) RecordSuccess() {
	bh.mu.Lock()
	defer bh.mu.Unlock()

	now := time.Now()
	bh.LastCheck = now
	bh.LastSuccess = now
	bh.TotalChecks++
	bh.TotalSuccess++

	bh.consecutiveFailures = 0
	bh.consecutiveSuccesses++

	current := bh.Status()

	switch current {
	case StatusUnhealthy, StatusRecovering:
		if bh.consecutiveSuccesses >= bh.cfg.RecoveryThreshold {
			bh.status.Store(int32(StatusHealthy))
			bh.consecutiveSuccesses = 0
		} else {
			// Transition to RECOVERING if not already there.
			if current == StatusUnhealthy {
				bh.status.Store(int32(StatusRecovering))
			}
		}
	case StatusHealthy:
		// Already healthy — nothing to do.
	}
}

// RecordFailure records one failed health check (active or passive) and
// transitions state if the failure threshold is met.
func (bh *BackendHealth) RecordFailure() {
	bh.mu.Lock()
	defer bh.mu.Unlock()

	now := time.Now()
	bh.LastCheck = now
	bh.LastFailure = now
	bh.TotalChecks++
	bh.TotalFailure++

	bh.consecutiveSuccesses = 0
	bh.consecutiveFailures++

	current := bh.Status()

	switch current {
	case StatusHealthy, StatusRecovering:
		if bh.consecutiveFailures >= bh.cfg.UnhealthyThreshold {
			bh.status.Store(int32(StatusUnhealthy))
			bh.consecutiveFailures = 0
		}
	case StatusUnhealthy:
		// Already unhealthy — keep counting but don't change state.
	}
}

// Stats is a point-in-time snapshot of health metrics.
type Stats struct {
	Status               Status
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	LastCheck            time.Time
	LastSuccess          time.Time
	LastFailure          time.Time
	TotalChecks          int64
	TotalSuccess         int64
	TotalFailure         int64
}

// Snapshot returns a consistent point-in-time copy of the health stats.
func (bh *BackendHealth) Snapshot() Stats {
	bh.mu.Lock()
	defer bh.mu.Unlock()
	return Stats{
		Status:               Status(bh.status.Load()),
		ConsecutiveFailures:  bh.consecutiveFailures,
		ConsecutiveSuccesses: bh.consecutiveSuccesses,
		LastCheck:            bh.LastCheck,
		LastSuccess:          bh.LastSuccess,
		LastFailure:          bh.LastFailure,
		TotalChecks:          bh.TotalChecks,
		TotalSuccess:         bh.TotalSuccess,
		TotalFailure:         bh.TotalFailure,
	}
}

// Package circuitbreaker implements a thread-safe Circuit Breaker pattern
// to protect upstream backends and fail fast when an upstream service degrades.
//
// # Circuit Breaker States
//
//	  CLOSED (normal traffic)
//	    │
//	    │ repeated failures >= FailureThreshold
//	    ▼
//	   OPEN (fail fast, all requests rejected)
//	    │
//	    │ OpenTimeout elapsed
//	    ▼
//	HALF-OPEN (rate-limited trial probes)
//	    ├── trial request failure ──► OPEN
//	    └── trial requests success >= SuccessThreshold ──► CLOSED
//
// # Thundering Herd Prevention
//
// In the HALF-OPEN state, rather than allowing all incoming traffic to stampede
// an already-struggling backend, the breaker only permits up to HalfOpenMaxRequests
// concurrent trial requests. All other requests are immediately rejected until
// the outcome of the trials is determined.
package circuitbreaker

import (
	"sync"
	"time"
)

// State represents the current lifecycle phase of the circuit breaker.
type State int32

const (
	// StateClosed allows all requests through normally.
	StateClosed State = iota

	// StateOpen rejects all requests immediately to prevent cascading overload.
	StateOpen

	// StateHalfOpen allows a restricted number of trial requests to test recovery.
	StateHalfOpen
)

// String returns a human-readable representation of State.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// Config holds configuration parameters for the circuit breaker.
type Config struct {
	// FailureThreshold is the number of consecutive failures in CLOSED state
	// before the breaker trips OPEN. Default: 5.
	FailureThreshold int

	// OpenTimeout is the duration the breaker remains OPEN before transitioning
	// to HALF_OPEN to trial upstream health. Default: 10s.
	OpenTimeout time.Duration

	// HalfOpenMaxRequests is the maximum number of concurrent trial requests
	// allowed when in HALF_OPEN state (prevents thundering herd). Default: 2.
	HalfOpenMaxRequests int

	// SuccessThreshold is the number of consecutive successful trial requests
	// in HALF_OPEN state required to fully close the breaker. Default: 2.
	SuccessThreshold int
}

// DefaultConfig provides safe, production-oriented defaults.
func DefaultConfig() Config {
	return Config{
		FailureThreshold:    5,
		OpenTimeout:         10 * time.Second,
		HalfOpenMaxRequests: 2,
		SuccessThreshold:    2,
	}
}

// CircuitBreaker manages state transitions and gatekeeping for a single backend.
// All methods are safe for concurrent use.
type CircuitBreaker struct {
	mu  sync.Mutex
	cfg Config

	state State

	consecutiveFailures  int
	consecutiveSuccesses int
	halfOpenInFlight     int

	lastStateChange time.Time
}

// New creates a new CircuitBreaker with the provided configuration.
func New(cfg Config) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = 10 * time.Second
	}
	if cfg.HalfOpenMaxRequests <= 0 {
		cfg.HalfOpenMaxRequests = 2
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 2
	}

	return &CircuitBreaker{
		cfg:             cfg,
		state:           StateClosed,
		lastStateChange: time.Now(),
	}
}

// State returns the current state of the circuit breaker, evaluating any
// timeout-based transition from OPEN to HALF_OPEN.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.evaluateStateLocked(time.Now())
	return cb.state
}

// Allow reports whether a request is permitted to proceed to the backend.
//
// Rules:
//   - CLOSED: Always true.
//   - OPEN: True only if OpenTimeout has elapsed (transitions to HALF_OPEN and claims 1 trial slot).
//     Otherwise false (fails fast).
//   - HALF_OPEN: True only if currently active trial requests < HalfOpenMaxRequests.
//     Otherwise false (thundering herd protection).
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	cb.evaluateStateLocked(now)

	switch cb.state {
	case StateClosed:
		return true

	case StateOpen:
		return false

	case StateHalfOpen:
		if cb.halfOpenInFlight < cb.cfg.HalfOpenMaxRequests {
			cb.halfOpenInFlight++
			return true
		}
		return false

	default:
		return false
	}
}

// RecordSuccess records a successful execution for this backend.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	cb.evaluateStateLocked(now)

	switch cb.state {
	case StateHalfOpen:
		if cb.halfOpenInFlight > 0 {
			cb.halfOpenInFlight--
		}
		cb.consecutiveSuccesses++
		if cb.consecutiveSuccesses >= cb.cfg.SuccessThreshold {
			// Backend has proven healthy; transition to CLOSED
			cb.transitionLocked(StateClosed, now)
		}

	case StateClosed:
		cb.consecutiveFailures = 0

	case StateOpen:
		// Stale response from previous request; ignore.
	}
}

// RecordFailure records a failed execution for this backend.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	cb.evaluateStateLocked(now)

	switch cb.state {
	case StateClosed:
		cb.consecutiveFailures++
		if cb.consecutiveFailures >= cb.cfg.FailureThreshold {
			cb.transitionLocked(StateOpen, now)
		}

	case StateHalfOpen:
		if cb.halfOpenInFlight > 0 {
			cb.halfOpenInFlight--
		}
		// Any failure during trial immediately trips the breaker back to OPEN
		cb.transitionLocked(StateOpen, now)

	case StateOpen:
		// Already open; update timestamp to prevent premature half-open if desired
		cb.lastStateChange = now
	}
}

// evaluateStateLocked checks if OpenTimeout has expired for OPEN state.
func (cb *CircuitBreaker) evaluateStateLocked(now time.Time) {
	if cb.state == StateOpen && now.Sub(cb.lastStateChange) >= cb.cfg.OpenTimeout {
		cb.transitionLocked(StateHalfOpen, now)
	}
}

// transitionLocked updates state and resets counters.
func (cb *CircuitBreaker) transitionLocked(next State, now time.Time) {
	cb.state = next
	cb.lastStateChange = now
	cb.consecutiveFailures = 0
	cb.consecutiveSuccesses = 0
	cb.halfOpenInFlight = 0
}

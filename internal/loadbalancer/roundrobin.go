package loadbalancer

import (
	"sync"
	"sync/atomic"
)

// RoundRobin implements a simple round-robin load balancer.
type RoundRobin struct {
	mu       sync.RWMutex
	backends []*Backend
	current  uint64
}

// NewRoundRobin creates a new RoundRobin load balancer.
func NewRoundRobin() *RoundRobin {
	return &RoundRobin{
		backends: make([]*Backend, 0),
	}
}

// SelectBackend chooses the next backend in a round-robin fashion, skipping unhealthy ones.
func (rr *RoundRobin) SelectBackend() *Backend {
	rr.mu.RLock()
	backends := rr.backends
	rr.mu.RUnlock()

	n := len(backends)
	if n == 0 {
		return nil
	}

	for i := 0; i < n; i++ {
		idx := (atomic.AddUint64(&rr.current, 1) - 1) % uint64(n)
		b := backends[idx]
		if b.IsAvailable() {
			return b
		}
	}

	// All backends are unavailable
	return nil
}

// AddBackend adds a new backend to the pool.
func (rr *RoundRobin) AddBackend(b *Backend) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	// Ensure we don't add duplicates based on ID
	for _, existing := range rr.backends {
		if existing.ID == b.ID {
			return
		}
	}
	rr.backends = append(rr.backends, b)
}

// RemoveBackend removes a backend from the pool by its ID.
func (rr *RoundRobin) RemoveBackend(id string) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	for i, b := range rr.backends {
		if b.ID == id {
			rr.backends = append(rr.backends[:i], rr.backends[i+1:]...)
			return
		}
	}
}

// RecordSuccess records a success for health checking and circuit breaking.
func (rr *RoundRobin) RecordSuccess(id string) {
	rr.mu.RLock()
	defer rr.mu.RUnlock()
	for _, b := range rr.backends {
		if b.ID == id {
			if b.Health != nil {
				b.Health.RecordSuccess()
			}
			if b.CircuitBreaker != nil {
				b.CircuitBreaker.RecordSuccess()
			}
			return
		}
	}
}

// RecordFailure records a failure for health checking and circuit breaking.
func (rr *RoundRobin) RecordFailure(id string) {
	rr.mu.RLock()
	defer rr.mu.RUnlock()
	for _, b := range rr.backends {
		if b.ID == id {
			if b.Health != nil {
				b.Health.RecordFailure()
			}
			if b.CircuitBreaker != nil {
				b.CircuitBreaker.RecordFailure()
			}
			return
		}
	}
}

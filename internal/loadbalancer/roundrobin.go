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

// SelectBackend chooses the next backend in a round-robin fashion.
func (rr *RoundRobin) SelectBackend() *Backend {
	rr.mu.RLock()
	backends := rr.backends
	rr.mu.RUnlock()

	n := len(backends)
	if n == 0 {
		return nil
	}

	// Atomically increment the counter to ensure thread-safety
	// Note: atomic.AddUint64 returns the new value, so the first selection is index 1.
	// This is fine as it still distributes evenly.
	idx := atomic.AddUint64(&rr.current, 1) % uint64(n)
	return backends[idx]
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

// RecordSuccess is a no-op for basic round robin.
func (rr *RoundRobin) RecordSuccess(id string) {}

// RecordFailure is a no-op for basic round robin.
func (rr *RoundRobin) RecordFailure(id string) {}

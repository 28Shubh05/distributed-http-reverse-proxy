package loadbalancer

import (
	"sync"
	"sync/atomic"
)

// LeastConnections implements the least-connections load balancing algorithm.
type LeastConnections struct {
	mu       sync.RWMutex
	backends []*Backend
}

// NewLeastConnections creates a new LeastConnections load balancer.
func NewLeastConnections() *LeastConnections {
	return &LeastConnections{
		backends: make([]*Backend, 0),
	}
}

// SelectBackend chooses the healthy backend with the fewest active connections.
func (lc *LeastConnections) SelectBackend() *Backend {
	lc.mu.RLock()
	backends := lc.backends
	lc.mu.RUnlock()

	n := len(backends)
	if n == 0 {
		return nil
	}

	var selected *Backend
	minConns := int64(-1)

	for _, b := range backends {
		if !b.IsAvailable() {
			continue
		}
		conns := atomic.LoadInt64(&b.ActiveConnections)
		if minConns == -1 || conns < minConns {
			minConns = conns
			selected = b
		}
	}

	if selected != nil {
		// Implicitly assume a request is starting when a backend is selected.
		atomic.AddInt64(&selected.ActiveConnections, 1)
	}

	return selected
}

// AddBackend adds a new backend to the pool.
func (lc *LeastConnections) AddBackend(b *Backend) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	for _, existing := range lc.backends {
		if existing.ID == b.ID {
			return
		}
	}
	lc.backends = append(lc.backends, b)
}

// RemoveBackend removes a backend from the pool by its ID.
func (lc *LeastConnections) RemoveBackend(id string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	for i, b := range lc.backends {
		if b.ID == id {
			lc.backends = append(lc.backends[:i], lc.backends[i+1:]...)
			return
		}
	}
}

func (lc *LeastConnections) decrementConnection(id string) *Backend {
	lc.mu.RLock()
	backends := lc.backends
	lc.mu.RUnlock()

	for _, b := range backends {
		if b.ID == id {
			// Ensure we don't go below zero
			val := atomic.LoadInt64(&b.ActiveConnections)
			for val > 0 {
				if atomic.CompareAndSwapInt64(&b.ActiveConnections, val, val-1) {
					break
				}
				val = atomic.LoadInt64(&b.ActiveConnections)
			}
			return b
		}
	}
	return nil
}

// RecordSuccess decrements the active connection count and records success for health and circuit breaking.
func (lc *LeastConnections) RecordSuccess(id string) {
	b := lc.decrementConnection(id)
	if b != nil {
		if b.Health != nil {
			b.Health.RecordSuccess()
		}
		if b.CircuitBreaker != nil {
			b.CircuitBreaker.RecordSuccess()
		}
	}
}

// RecordFailure decrements the active connection count and records failure for health and circuit breaking.
func (lc *LeastConnections) RecordFailure(id string) {
	b := lc.decrementConnection(id)
	if b != nil {
		if b.Health != nil {
			b.Health.RecordFailure()
		}
		if b.CircuitBreaker != nil {
			b.CircuitBreaker.RecordFailure()
		}
	}
}

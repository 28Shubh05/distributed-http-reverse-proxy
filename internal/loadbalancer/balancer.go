package loadbalancer

import (
	"net/url"
	
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/circuitbreaker"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/health"
)

// Backend represents an upstream server that can handle requests.
type Backend struct {
	ID     string   // Unique identifier for the backend
	URL    *url.URL // Parsed URL of the backend
	Weight int      // Weight for Weighted Round Robin
	
	// ActiveConnections tracks the number of currently active requests to this backend.
	// This should be updated atomically.
	ActiveConnections int64

	// Health tracks the health state of this backend.
	Health *health.BackendHealth

	// CircuitBreaker guards this backend against cascading failure.
	CircuitBreaker *circuitbreaker.CircuitBreaker
}

// IsAvailable reports whether the backend is ready to receive requests,
// checking both its health status and circuit breaker gate.
func (b *Backend) IsAvailable() bool {
	if b.Health != nil && !b.Health.IsAvailable() {
		return false
	}
	if b.CircuitBreaker != nil && !b.CircuitBreaker.Allow() {
		return false
	}
	return true
}

// LoadBalancer defines the interface for selecting a backend server.
type LoadBalancer interface {
	// SelectBackend chooses the next available backend to route the request to.
	// It returns nil if no backend is available.
	SelectBackend() *Backend

	// AddBackend adds a new backend to the load balancer pool.
	AddBackend(b *Backend)

	// RemoveBackend removes a backend from the pool by its ID.
	RemoveBackend(id string)

	// RecordSuccess records a successful request for a backend.
	RecordSuccess(id string)

	// RecordFailure records a failed request for a backend.
	RecordFailure(id string)
}

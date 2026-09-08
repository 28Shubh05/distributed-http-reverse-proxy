package loadbalancer

import "net/url"

// Backend represents an upstream server that can handle requests.
type Backend struct {
	ID     string   // Unique identifier for the backend
	URL    *url.URL // Parsed URL of the backend
	Weight int      // Weight for Weighted Round Robin
	
	// Future fields for health checking and active connections will be added here.
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

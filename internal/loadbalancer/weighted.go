package loadbalancer

import (
	"sync"
)

// WeightedRoundRobin implements the weighted round-robin load balancing algorithm.
// It uses an algorithm similar to LVS (Linux Virtual Server) to distribute traffic.
type WeightedRoundRobin struct {
	mu            sync.Mutex
	backends      []*Backend
	currentIndex  int
	currentWeight int
	maxWeight     int
	gcdWeight     int
}

// NewWeightedRoundRobin creates a new WeightedRoundRobin load balancer.
func NewWeightedRoundRobin() *WeightedRoundRobin {
	return &WeightedRoundRobin{
		backends:      make([]*Backend, 0),
		currentIndex:  -1,
		currentWeight: 0,
	}
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func (wrr *WeightedRoundRobin) recalculate() {
	if len(wrr.backends) == 0 {
		wrr.maxWeight = 0
		wrr.gcdWeight = 0
		wrr.currentWeight = 0
		wrr.currentIndex = -1
		return
	}

	max := 0
	gcdVal := wrr.backends[0].Weight
	for _, b := range wrr.backends {
		if b.Weight > max {
			max = b.Weight
		}
		gcdVal = gcd(gcdVal, b.Weight)
	}

	wrr.maxWeight = max
	wrr.gcdWeight = gcdVal

	// Reset state slightly to ensure we re-evaluate correctly
	wrr.currentIndex = -1
	wrr.currentWeight = 0
}

// SelectBackend chooses the next backend considering their weights and health.
func (wrr *WeightedRoundRobin) SelectBackend() *Backend {
	wrr.mu.Lock()
	defer wrr.mu.Unlock()

	n := len(wrr.backends)
	if n == 0 || wrr.maxWeight == 0 {
		return nil
	}

	// We need to avoid infinite loops if all backends are unhealthy or have 0 weight.
	// Since the algorithm cycles, if we check `n` elements at `currentWeight == 1`
	// without finding a match, no healthy backends exist.
	// To simplify, we'll keep a counter of iterations without finding a backend.
	// Max iterations is roughly n * (maxWeight / gcdWeight). Let's use a simpler bound.
	maxIterations := n * wrr.maxWeight
	if maxIterations == 0 {
		return nil
	}

	for i := 0; i < maxIterations; i++ {
		wrr.currentIndex = (wrr.currentIndex + 1) % n
		if wrr.currentIndex == 0 {
			wrr.currentWeight = wrr.currentWeight - wrr.gcdWeight
			if wrr.currentWeight <= 0 {
				wrr.currentWeight = wrr.maxWeight
				if wrr.currentWeight == 0 {
					return nil // All weights are zero
				}
			}
		}

		b := wrr.backends[wrr.currentIndex]
		if b.Weight >= wrr.currentWeight && b.IsAvailable() {
			return b
		}
	}

	return nil
}

// AddBackend adds a new backend to the pool.
func (wrr *WeightedRoundRobin) AddBackend(b *Backend) {
	wrr.mu.Lock()
	defer wrr.mu.Unlock()

	for _, existing := range wrr.backends {
		if existing.ID == b.ID {
			// If it exists, update the weight and recalculate
			existing.Weight = b.Weight
			wrr.recalculate()
			return
		}
	}
	wrr.backends = append(wrr.backends, b)
	wrr.recalculate()
}

// RemoveBackend removes a backend from the pool by its ID.
func (wrr *WeightedRoundRobin) RemoveBackend(id string) {
	wrr.mu.Lock()
	defer wrr.mu.Unlock()

	for i, b := range wrr.backends {
		if b.ID == id {
			wrr.backends = append(wrr.backends[:i], wrr.backends[i+1:]...)
			wrr.recalculate()
			return
		}
	}
}

// RecordSuccess records a success for health checking and circuit breaking.
func (wrr *WeightedRoundRobin) RecordSuccess(id string) {
	wrr.mu.Lock()
	defer wrr.mu.Unlock()
	for _, b := range wrr.backends {
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
func (wrr *WeightedRoundRobin) RecordFailure(id string) {
	wrr.mu.Lock()
	defer wrr.mu.Unlock()
	for _, b := range wrr.backends {
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

package loadbalancer

import (
	"math"
	"testing"
)

func TestWeightedRoundRobin_Distribution(t *testing.T) {
	wrr := NewWeightedRoundRobin()

	b1 := &Backend{ID: "A", Weight: 5}
	b2 := &Backend{ID: "B", Weight: 3}
	b3 := &Backend{ID: "C", Weight: 1}

	wrr.AddBackend(b1)
	wrr.AddBackend(b2)
	wrr.AddBackend(b3)

	totalRequests := 9000
	results := make(map[string]int)

	for i := 0; i < totalRequests; i++ {
		b := wrr.SelectBackend()
		if b == nil {
			t.Fatal("expected backend, got nil")
		}
		results[b.ID]++
	}

	// Expected distribution
	// A: 5/9 * 9000 = 5000
	// B: 3/9 * 9000 = 3000
	// C: 1/9 * 9000 = 1000
	expected := map[string]int{
		"A": 5000,
		"B": 3000,
		"C": 1000,
	}

	for id, expectedCount := range expected {
		count := results[id]
		// Allow some small deviation, although the LVS algorithm is deterministic and exact
		// over the cycle length (9). So it should be perfectly exact.
		if math.Abs(float64(count-expectedCount)) > float64(totalRequests)*0.01 {
			t.Errorf("backend %s expected ~%d, got %d", id, expectedCount, count)
		}
	}
}

func TestWeightedRoundRobin_ZeroWeight(t *testing.T) {
	wrr := NewWeightedRoundRobin()

	b1 := &Backend{ID: "A", Weight: 0}
	b2 := &Backend{ID: "B", Weight: 0}

	wrr.AddBackend(b1)
	wrr.AddBackend(b2)

	if b := wrr.SelectBackend(); b != nil {
		t.Fatalf("expected nil backend when all weights are 0, got %v", b)
	}
}

func TestWeightedRoundRobin_AddRemove(t *testing.T) {
	wrr := NewWeightedRoundRobin()
	b1 := &Backend{ID: "A", Weight: 5}
	wrr.AddBackend(b1)

	b := wrr.SelectBackend()
	if b == nil || b.ID != "A" {
		t.Fatalf("expected A, got %v", b)
	}

	wrr.RemoveBackend("A")
	if wrr.SelectBackend() != nil {
		t.Fatal("expected nil after removing the only backend")
	}
}

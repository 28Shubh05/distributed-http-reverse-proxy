package loadbalancer

import (
	"net/url"
	"testing"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/health"
)

func TestRoundRobin_HealthFiltering(t *testing.T) {
	rr := NewRoundRobin()

	bh1 := health.NewBackendHealth(health.DefaultConfig())
	bh2 := health.NewBackendHealth(health.DefaultConfig())

	b1 := &Backend{ID: "b1", URL: &url.URL{Host: "localhost:8081"}, Health: bh1}
	b2 := &Backend{ID: "b2", URL: &url.URL{Host: "localhost:8082"}, Health: bh2}

	rr.AddBackend(b1)
	rr.AddBackend(b2)

	// Both healthy -> alternates
	selected := make(map[string]int)
	for i := 0; i < 4; i++ {
		b := rr.SelectBackend()
		selected[b.ID]++
	}
	if selected["b1"] != 2 || selected["b2"] != 2 {
		t.Fatalf("expected 2 of each, got b1=%d, b2=%d", selected["b1"], selected["b2"])
	}

	// Mark b1 UNHEALTHY via RecordFailure threshold
	for i := 0; i < health.DefaultConfig().UnhealthyThreshold; i++ {
		rr.RecordFailure("b1")
	}
	if bh1.Status() != health.StatusUnhealthy {
		t.Fatalf("expected b1 to be UNHEALTHY, got %s", bh1.Status())
	}

	// Now only b2 should be selected
	for i := 0; i < 5; i++ {
		b := rr.SelectBackend()
		if b == nil || b.ID != "b2" {
			t.Fatalf("expected b2, got %v", b)
		}
	}

	// Mark b2 UNHEALTHY too
	for i := 0; i < health.DefaultConfig().UnhealthyThreshold; i++ {
		rr.RecordFailure("b2")
	}

	// Now both are UNHEALTHY -> SelectBackend should return nil
	if b := rr.SelectBackend(); b != nil {
		t.Fatalf("expected nil when all backends unhealthy, got %v", b)
	}

	// Recover b1 via RecordSuccess threshold
	for i := 0; i < health.DefaultConfig().RecoveryThreshold; i++ {
		rr.RecordSuccess("b1")
	}
	if bh1.Status() != health.StatusHealthy {
		t.Fatalf("expected b1 to recover to HEALTHY, got %s", bh1.Status())
	}

	// b1 should be selected again
	if b := rr.SelectBackend(); b == nil || b.ID != "b1" {
		t.Fatalf("expected b1 after recovery, got %v", b)
	}
}

func TestWeightedRoundRobin_HealthFiltering(t *testing.T) {
	wrr := NewWeightedRoundRobin()

	bh1 := health.NewBackendHealth(health.DefaultConfig())
	bh2 := health.NewBackendHealth(health.DefaultConfig())

	b1 := &Backend{ID: "b1", Weight: 5, Health: bh1}
	b2 := &Backend{ID: "b2", Weight: 1, Health: bh2}

	wrr.AddBackend(b1)
	wrr.AddBackend(b2)

	// Mark b1 UNHEALTHY
	for i := 0; i < health.DefaultConfig().UnhealthyThreshold; i++ {
		wrr.RecordFailure("b1")
	}

	// Even though b1 has weight 5, it's UNHEALTHY so all traffic must go to b2
	for i := 0; i < 5; i++ {
		b := wrr.SelectBackend()
		if b == nil || b.ID != "b2" {
			t.Fatalf("expected b2, got %v", b)
		}
	}

	// Mark b2 UNHEALTHY
	for i := 0; i < health.DefaultConfig().UnhealthyThreshold; i++ {
		wrr.RecordFailure("b2")
	}

	// Both UNHEALTHY -> nil
	if b := wrr.SelectBackend(); b != nil {
		t.Fatalf("expected nil when all unhealthy, got %v", b)
	}
}

func TestLeastConnections_HealthFiltering(t *testing.T) {
	lc := NewLeastConnections()

	bh1 := health.NewBackendHealth(health.DefaultConfig())
	bh2 := health.NewBackendHealth(health.DefaultConfig())

	b1 := &Backend{ID: "b1", Health: bh1}
	b2 := &Backend{ID: "b2", Health: bh2}

	lc.AddBackend(b1)
	lc.AddBackend(b2)

	// Mark b1 UNHEALTHY
	for i := 0; i < health.DefaultConfig().UnhealthyThreshold; i++ {
		bh1.RecordFailure()
	}

	// b1 has 0 connections, but is UNHEALTHY.
	// b2 should be picked even as its connections increase.
	b := lc.SelectBackend()
	if b == nil || b.ID != "b2" {
		t.Fatalf("expected b2, got %v", b)
	}

	// Mark b2 UNHEALTHY
	for i := 0; i < health.DefaultConfig().UnhealthyThreshold; i++ {
		bh2.RecordFailure()
	}

	// Both UNHEALTHY -> nil
	if b := lc.SelectBackend(); b != nil {
		t.Fatalf("expected nil when all unhealthy, got %v", b)
	}
}

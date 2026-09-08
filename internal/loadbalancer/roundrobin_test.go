package loadbalancer

import (
	"net/url"
	"sync"
	"testing"
)

func TestRoundRobin_SelectBackend(t *testing.T) {
	rr := NewRoundRobin()

	if b := rr.SelectBackend(); b != nil {
		t.Fatalf("expected nil backend, got %v", b)
	}

	b1 := &Backend{ID: "backend-1", URL: &url.URL{Host: "localhost:8081"}}
	b2 := &Backend{ID: "backend-2", URL: &url.URL{Host: "localhost:8082"}}
	b3 := &Backend{ID: "backend-3", URL: &url.URL{Host: "localhost:8083"}}

	rr.AddBackend(b1)
	rr.AddBackend(b2)
	rr.AddBackend(b3)

	selected := make(map[string]int)
	for i := 0; i < 3; i++ {
		b := rr.SelectBackend()
		selected[b.ID]++
	}

	if len(selected) != 3 {
		t.Fatalf("expected 3 unique backends selected, got %d", len(selected))
	}

	for id, count := range selected {
		if count != 1 {
			t.Errorf("expected backend %s to be selected 1 time, got %d", id, count)
		}
	}
}

func TestRoundRobin_AddRemove(t *testing.T) {
	rr := NewRoundRobin()
	b1 := &Backend{ID: "b1"}
	rr.AddBackend(b1)
	rr.AddBackend(b1) // duplicate, should be ignored

	if len(rr.backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(rr.backends))
	}

	rr.RemoveBackend("b1")
	if len(rr.backends) != 0 {
		t.Fatalf("expected 0 backends, got %d", len(rr.backends))
	}
	
	if rr.SelectBackend() != nil {
		t.Fatal("expected nil after removing only backend")
	}
}

func TestRoundRobin_Concurrency(t *testing.T) {
	rr := NewRoundRobin()
	b1 := &Backend{ID: "backend-1"}
	b2 := &Backend{ID: "backend-2"}
	rr.AddBackend(b1)
	rr.AddBackend(b2)

	var wg sync.WaitGroup
	// Run concurrent selections to test thread-safety
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr.SelectBackend()
		}()
	}
	wg.Wait()
}

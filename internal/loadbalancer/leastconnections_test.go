package loadbalancer

import (
	"sync"
	"testing"
)

func TestLeastConnections_SelectBackend(t *testing.T) {
	lc := NewLeastConnections()

	b1 := &Backend{ID: "backend-1"}
	b2 := &Backend{ID: "backend-2"}
	b3 := &Backend{ID: "backend-3"}

	lc.AddBackend(b1)
	lc.AddBackend(b2)
	lc.AddBackend(b3)

	// Select first backend, should be b1 (or any, but assuming order)
	sel1 := lc.SelectBackend()
	if sel1 == nil {
		t.Fatal("expected backend, got nil")
	}

	// Because sel1 has 1 active connection now, next select should return b2 or b3
	sel2 := lc.SelectBackend()
	if sel2.ID == sel1.ID {
		t.Fatalf("expected different backend, got same %s", sel2.ID)
	}

	sel3 := lc.SelectBackend()
	if sel3.ID == sel1.ID || sel3.ID == sel2.ID {
		t.Fatalf("expected 3rd backend, got %s", sel3.ID)
	}

	// Now all have 1 connection.
	// Next select should wrap around and pick the first one again.
	sel4 := lc.SelectBackend()
	if sel4.ID != sel1.ID {
		t.Fatalf("expected %s to be selected again, got %s", sel1.ID, sel4.ID)
	}

	// Mark sel1's request as finished.
	lc.RecordSuccess(sel1.ID)

	// Since sel1 just finished a request, it has 1 connection while others have 1 or 2 (sel4 incremented sel1).
	// Wait, at this point:
	// sel1 has 2 connections (initial + sel4) - 1 (success) = 1
	// sel2 has 1
	// sel3 has 1
	// Next select will see all have 1 connection, and pick the first one.
	sel5 := lc.SelectBackend()
	if sel5 == nil {
		t.Fatal("expected backend, got nil")
	}
}

func TestLeastConnections_Decrement(t *testing.T) {
	lc := NewLeastConnections()
	b1 := &Backend{ID: "backend-1"}
	lc.AddBackend(b1)

	lc.SelectBackend() // +1
	lc.SelectBackend() // +1
	
	if b1.ActiveConnections != 2 {
		t.Fatalf("expected 2 active connections, got %d", b1.ActiveConnections)
	}

	lc.RecordSuccess(b1.ID) // -1
	if b1.ActiveConnections != 1 {
		t.Fatalf("expected 1 active connection, got %d", b1.ActiveConnections)
	}

	lc.RecordFailure(b1.ID) // -1
	if b1.ActiveConnections != 0 {
		t.Fatalf("expected 0 active connections, got %d", b1.ActiveConnections)
	}

	// Should not go below zero
	lc.RecordSuccess(b1.ID)
	if b1.ActiveConnections != 0 {
		t.Fatalf("expected 0 active connections, got %d", b1.ActiveConnections)
	}
}

func TestLeastConnections_Concurrency(t *testing.T) {
	lc := NewLeastConnections()
	b1 := &Backend{ID: "backend-1"}
	b2 := &Backend{ID: "backend-2"}
	lc.AddBackend(b1)
	lc.AddBackend(b2)

	var wg sync.WaitGroup
	// 1000 concurrent selects
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := lc.SelectBackend()
			// immediately record success
			if b != nil {
				lc.RecordSuccess(b.ID)
			}
		}()
	}
	wg.Wait()
}

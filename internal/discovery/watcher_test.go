package discovery

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/circuitbreaker"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/health"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/loadbalancer"
)

// mockDiscoverer implements Discoverer for testing dynamic updates.
type mockDiscoverer struct {
	mu       sync.Mutex
	backends []BackendEntry
	eventCh  chan Event
}

func newMockDiscoverer(initial []BackendEntry) *mockDiscoverer {
	return &mockDiscoverer{
		backends: initial,
		eventCh:  make(chan Event, 16),
	}
}

func (m *mockDiscoverer) List(ctx context.Context) ([]BackendEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]BackendEntry, len(m.backends))
	copy(copied, m.backends)
	return copied, nil
}

func (m *mockDiscoverer) Watch(ctx context.Context) <-chan Event {
	return m.eventCh
}

func (m *mockDiscoverer) emit(ev Event) {
	m.eventCh <- ev
}

func TestWatcher_InitialSync(t *testing.T) {
	initialBackends := []BackendEntry{
		{ID: "backend-1", URL: "http://127.0.0.1:9001", Weight: 1},
		{ID: "backend-2", URL: "http://127.0.0.1:9002", Weight: 2},
	}
	disc := newMockDiscoverer(initialBackends)
	lb := loadbalancer.NewRoundRobin()
	hc := health.NewChecker(health.DefaultConfig(), nil)

	watcher := NewWatcher(disc, lb, hc, circuitbreaker.DefaultConfig(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = watcher.Sync(ctx)
	}()

	// Allow sync to process
	time.Sleep(50 * time.Millisecond)

	b1 := lb.SelectBackend()
	if b1 == nil {
		t.Fatalf("expected backend to be selected from initial sync, got nil")
	}

	b2 := lb.SelectBackend()
	if b2 == nil {
		t.Fatalf("expected second backend to be selected, got nil")
	}

	if b1.ID == b2.ID {
		t.Errorf("expected round robin between two backends, got same: %s", b1.ID)
	}
}

func TestWatcher_DynamicAddRemoveAndRestart(t *testing.T) {
	disc := newMockDiscoverer(nil)
	lb := loadbalancer.NewRoundRobin()
	hc := health.NewChecker(health.DefaultConfig(), nil)

	watcher := NewWatcher(disc, lb, hc, circuitbreaker.DefaultConfig(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = watcher.Sync(ctx)
	}()

	time.Sleep(20 * time.Millisecond)

	// Initially pool is empty
	if b := lb.SelectBackend(); b != nil {
		t.Fatalf("expected empty pool, got %v", b)
	}

	// 1. Dynamic Start/Register backend-1
	disc.emit(Event{
		Type: EventAdded,
		Backend: BackendEntry{
			ID:     "backend-1",
			URL:    "http://127.0.0.1:9001",
			Weight: 1,
		},
	})
	time.Sleep(30 * time.Millisecond)

	b := lb.SelectBackend()
	if b == nil || b.ID != "backend-1" {
		t.Fatalf("expected backend-1 to be available after dynamic add, got %v", b)
	}

	// 2. Dynamic Start/Register backend-2
	disc.emit(Event{
		Type: EventAdded,
		Backend: BackendEntry{
			ID:     "backend-2",
			URL:    "http://127.0.0.1:9002",
			Weight: 1,
		},
	})
	time.Sleep(30 * time.Millisecond)

	// Both backends should be chosen
	seen := make(map[string]bool)
	for i := 0; i < 4; i++ {
		selected := lb.SelectBackend()
		if selected != nil {
			seen[selected.ID] = true
		}
	}
	if !seen["backend-1"] || !seen["backend-2"] {
		t.Errorf("expected both backends to be active in load balancer, got seen: %v", seen)
	}

	// 3. Stop / Remove backend-1
	disc.emit(Event{
		Type: EventRemoved,
		Backend: BackendEntry{
			ID: "backend-1",
		},
	})
	time.Sleep(30 * time.Millisecond)

	for i := 0; i < 4; i++ {
		selected := lb.SelectBackend()
		if selected == nil || selected.ID != "backend-2" {
			t.Errorf("expected only backend-2 after backend-1 removal, got %v", selected)
		}
	}

	// 4. Restart backend-1 (add back again)
	disc.emit(Event{
		Type: EventAdded,
		Backend: BackendEntry{
			ID:     "backend-1",
			URL:    "http://127.0.0.1:9001",
			Weight: 1,
		},
	})
	time.Sleep(30 * time.Millisecond)

	seenAfterRestart := make(map[string]bool)
	for i := 0; i < 4; i++ {
		selected := lb.SelectBackend()
		if selected != nil {
			seenAfterRestart[selected.ID] = true
		}
	}
	if !seenAfterRestart["backend-1"] {
		t.Errorf("expected backend-1 to be restored after restart, got %v", seenAfterRestart)
	}
}

func TestWatcher_InvalidURLIgnored(t *testing.T) {
	disc := newMockDiscoverer(nil)
	lb := loadbalancer.NewRoundRobin()

	watcher := NewWatcher(disc, lb, nil, circuitbreaker.DefaultConfig(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = watcher.Sync(ctx)
	}()

	time.Sleep(20 * time.Millisecond)

	// Emit invalid URL
	disc.emit(Event{
		Type: EventAdded,
		Backend: BackendEntry{
			ID:  "bad-backend",
			URL: "http://[::1]:namedport",
		},
	})
	time.Sleep(30 * time.Millisecond)

	if b := lb.SelectBackend(); b != nil {
		t.Errorf("expected invalid URL backend to be ignored, got %v", b)
	}
}

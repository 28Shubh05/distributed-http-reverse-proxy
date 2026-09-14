package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sync"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/circuitbreaker"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/health"
	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/loadbalancer"
)

// Discoverer provides discovery listing and event watching.
type Discoverer interface {
	List(ctx context.Context) ([]BackendEntry, error)
	Watch(ctx context.Context) <-chan Event
}

// Watcher subscribes to discovery events and keeps a LoadBalancer's backend
// pool updated dynamically — no proxy restart needed.
type Watcher struct {
	discoverer    Discoverer
	lb            loadbalancer.LoadBalancer
	healthChecker *health.Checker
	cbConfig      circuitbreaker.Config
	logger        *slog.Logger

	mu      sync.Mutex
	watched map[string]struct{} // backend IDs currently tracked
}

// NewWatcher creates a Watcher that will sync lb with discovery events.
func NewWatcher(discoverer Discoverer, lb loadbalancer.LoadBalancer, hc *health.Checker, cbCfg circuitbreaker.Config, logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Watcher{
		discoverer:    discoverer,
		lb:            lb,
		healthChecker: hc,
		cbConfig:      cbCfg,
		logger:        logger,
		watched:       make(map[string]struct{}),
	}
}

// Sync does an initial load from etcd and then starts watching for changes.
// It blocks until ctx is cancelled. Run in a goroutine.
func (w *Watcher) Sync(ctx context.Context) error {
	// Initial list
	backends, err := w.discoverer.List(ctx)
	if err != nil {
		return fmt.Errorf("initial list from etcd: %w", err)
	}
	for _, b := range backends {
		w.add(b)
	}
	w.logger.Info("service discovery: initial backend list loaded", "count", len(backends))

	// Watch for changes
	events := w.discoverer.Watch(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			switch ev.Type {
			case EventAdded:
				w.add(ev.Backend)
			case EventRemoved:
				w.remove(ev.Backend.ID)
			}
		}
	}
}

func (w *Watcher) add(entry BackendEntry) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.watched[entry.ID]; exists {
		w.logger.Info("service discovery: backend already tracked", "id", entry.ID)
		return
	}

	u, err := url.Parse(entry.URL)
	if err != nil {
		w.logger.Error("service discovery: invalid backend URL", "id", entry.ID, "url", entry.URL, "err", err)
		return
	}

	weight := entry.Weight
	if weight <= 0 {
		weight = 1
	}

	bh := health.NewBackendHealth(health.DefaultConfig())
	cb := circuitbreaker.New(w.cbConfig)
	backend := &loadbalancer.Backend{
		ID:     entry.ID,
		URL:    u,
		Weight: weight,
		Health: bh,
		CircuitBreaker: cb,
	}

	w.lb.AddBackend(backend)
	if w.healthChecker != nil {
		w.healthChecker.Register(entry.ID, entry.URL, bh)
	}

	w.watched[entry.ID] = struct{}{}
	w.logger.Info("service discovery: backend added", "id", entry.ID, "url", entry.URL)
}

func (w *Watcher) remove(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.watched[id]; !exists {
		return
	}

	w.lb.RemoveBackend(id)
	if w.healthChecker != nil {
		w.healthChecker.Deregister(id)
	}

	delete(w.watched, id)
	w.logger.Info("service discovery: backend removed", "id", id)
}

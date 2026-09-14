// Package discovery implements etcd-backed service discovery for dynamic
// backend pool management without proxy restarts.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

const defaultPrefix = "/proxy/backends/"

// BackendEntry is the data stored per backend in etcd.
type BackendEntry struct {
	// ID is a unique identifier for the backend (e.g. "backend-1").
	ID string `json:"id"`

	// URL is the full HTTP URL of the backend (e.g. "http://10.0.0.1:9001").
	URL string `json:"url"`

	// Weight is the traffic weight for weighted round-robin (default: 1).
	Weight int `json:"weight"`
}

// EventType classifies a discovery event.
type EventType int

const (
	EventAdded   EventType = iota // backend registered or updated
	EventRemoved                  // backend deregistered / lease expired
)

// Event carries a single discovery change notification.
type Event struct {
	Type    EventType
	Backend BackendEntry
}

// Registry manages backend registration and watching in etcd.
type Registry struct {
	client *clientv3.Client
	prefix string
	logger *slog.Logger
}

// NewRegistry creates a Registry connected to the given etcd endpoints.
func NewRegistry(endpoints []string, dialTimeout time.Duration, logger *slog.Logger) (*Registry, error) {
	if dialTimeout <= 0 {
		dialTimeout = 3 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}

	return &Registry{
		client: cli,
		prefix: defaultPrefix,
		logger: logger,
	}, nil
}

// NewRegistryFromClient creates a Registry from an existing etcd client.
// Useful for tests that inject a fake/mock client.
func NewRegistryFromClient(cli *clientv3.Client, prefix string, logger *slog.Logger) *Registry {
	if prefix == "" {
		prefix = defaultPrefix
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{client: cli, prefix: prefix, logger: logger}
}

// Close closes the underlying etcd client connection.
func (r *Registry) Close() error {
	return r.client.Close()
}

// Register writes a backend entry into etcd with a lease TTL.
// When the TTL expires (e.g. process dies), etcd removes the key automatically.
// Returns a keepalive context cancel func that must be called to stop keepalives.
func (r *Registry) Register(ctx context.Context, entry BackendEntry, ttl time.Duration) (func(), error) {
	if ttl <= 0 {
		ttl = 15 * time.Second
	}

	// Create a lease
	leaseResp, err := r.client.Grant(ctx, int64(ttl.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("etcd grant lease: %w", err)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("marshal backend entry: %w", err)
	}

	key := r.keyFor(entry.ID)
	_, err = r.client.Put(ctx, key, string(data), clientv3.WithLease(leaseResp.ID))
	if err != nil {
		return nil, fmt.Errorf("etcd put backend: %w", err)
	}

	r.logger.Info("backend registered", "id", entry.ID, "url", entry.URL, "key", key, "ttl", ttl)

	// Start automatic keepalive to renew the lease while alive
	keepAliveCh, err := r.client.KeepAlive(ctx, leaseResp.ID)
	if err != nil {
		return nil, fmt.Errorf("etcd keepalive: %w", err)
	}

	kaCtx, kaCancel := context.WithCancel(ctx)

	go func() {
		for {
			select {
			case <-kaCtx.Done():
				return
			case _, ok := <-keepAliveCh:
				if !ok {
					r.logger.Warn("keepalive channel closed", "id", entry.ID)
					return
				}
			}
		}
	}()

	return kaCancel, nil
}

// Deregister removes a backend's key from etcd immediately.
func (r *Registry) Deregister(ctx context.Context, id string) error {
	key := r.keyFor(id)
	_, err := r.client.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("etcd delete backend %s: %w", id, err)
	}
	r.logger.Info("backend deregistered", "id", id)
	return nil
}

// List returns all currently registered backends.
func (r *Registry) List(ctx context.Context) ([]BackendEntry, error) {
	resp, err := r.client.Get(ctx, r.prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("etcd list backends: %w", err)
	}

	var backends []BackendEntry
	for _, kv := range resp.Kvs {
		var entry BackendEntry
		if err := json.Unmarshal(kv.Value, &entry); err != nil {
			r.logger.Warn("skipping malformed backend entry", "key", string(kv.Key), "err", err)
			continue
		}
		backends = append(backends, entry)
	}
	return backends, nil
}

// Watch streams backend discovery events to the returned channel.
// The channel is closed when ctx is cancelled.
func (r *Registry) Watch(ctx context.Context) <-chan Event {
	ch := make(chan Event, 32)

	go func() {
		defer close(ch)
		watchCh := r.client.Watch(ctx, r.prefix, clientv3.WithPrefix())

		for {
			select {
			case <-ctx.Done():
				return
			case resp, ok := <-watchCh:
				if !ok {
					return
				}
				if resp.Err() != nil {
					r.logger.Warn("etcd watch error", "err", resp.Err())
					continue
				}
				for _, ev := range resp.Events {
					r.handleEvent(ch, ev)
				}
			}
		}
	}()

	return ch
}

func (r *Registry) handleEvent(ch chan<- Event, ev *clientv3.Event) {
	switch ev.Type {
	case clientv3.EventTypePut:
		var entry BackendEntry
		if err := json.Unmarshal(ev.Kv.Value, &entry); err != nil {
			r.logger.Warn("malformed put event", "key", string(ev.Kv.Key))
			return
		}
		ch <- Event{Type: EventAdded, Backend: entry}

	case clientv3.EventTypeDelete:
		// Extract backend ID from key path
		id := path.Base(string(ev.Kv.Key))
		ch <- Event{Type: EventRemoved, Backend: BackendEntry{ID: id}}
	}
}

func (r *Registry) keyFor(id string) string {
	return r.prefix + id
}

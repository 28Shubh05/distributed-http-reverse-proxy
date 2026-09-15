package storage

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is an in-memory implementation of Store, safe for concurrent use.
type MemoryStore struct {
	mu sync.RWMutex

	backends map[string]BackendRecord
	configs  map[string]ConfigRecord
	policies map[string]RoutingPolicy
	events   []HealthEvent
	eventSeq int64
}

// NewMemoryStore creates a new in-memory store initialized with empty tables.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		backends: make(map[string]BackendRecord),
		configs:  make(map[string]ConfigRecord),
		policies: make(map[string]RoutingPolicy),
		events:   make([]HealthEvent, 0),
	}
}

func (m *MemoryStore) ListBackends(ctx context.Context) ([]BackendRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]BackendRecord, 0, len(m.backends))
	for _, b := range m.backends {
		res = append(res, b)
	}
	return res, nil
}

func (m *MemoryStore) GetBackend(ctx context.Context, id string) (*BackendRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	b, ok := m.backends[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &b, nil
}

func (m *MemoryStore) UpsertBackend(ctx context.Context, b BackendRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	existing, ok := m.backends[b.ID]
	if ok {
		b.CreatedAt = existing.CreatedAt
	} else if b.CreatedAt.IsZero() {
		b.CreatedAt = now
	}
	b.UpdatedAt = now

	m.backends[b.ID] = b
	return nil
}

func (m *MemoryStore) DeleteBackend(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.backends[id]; !ok {
		return ErrNotFound
	}
	delete(m.backends, id)
	return nil
}

func (m *MemoryStore) GetConfig(ctx context.Context, key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cfg, ok := m.configs[key]
	if !ok {
		return "", ErrNotFound
	}
	return cfg.Value, nil
}

func (m *MemoryStore) SetConfig(ctx context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.configs[key] = ConfigRecord{
		Key:       key,
		Value:     value,
		UpdatedAt: time.Now(),
	}
	return nil
}

func (m *MemoryStore) GetAllConfig(ctx context.Context) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make(map[string]string, len(m.configs))
	for k, v := range m.configs {
		res[k] = v.Value
	}
	return res, nil
}

func (m *MemoryStore) ListRoutingPolicies(ctx context.Context) ([]RoutingPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]RoutingPolicy, 0, len(m.policies))
	for _, p := range m.policies {
		res = append(res, p)
	}
	return res, nil
}

func (m *MemoryStore) SetRoutingPolicy(ctx context.Context, p RoutingPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p.UpdatedAt = time.Now()
	m.policies[p.PathPrefix] = p
	return nil
}

func (m *MemoryStore) RecordHealthEvent(ctx context.Context, event HealthEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.eventSeq++
	event.ID = m.eventSeq
	if event.EventTime.IsZero() {
		event.EventTime = time.Now()
	}

	m.events = append(m.events, event)
	return nil
}

func (m *MemoryStore) ListHealthEvents(ctx context.Context, limit int) ([]HealthEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 || limit > len(m.events) {
		limit = len(m.events)
	}

	start := len(m.events) - limit
	res := make([]HealthEvent, limit)
	copy(res, m.events[start:])
	return res, nil
}

func (m *MemoryStore) Close() error {
	return nil
}

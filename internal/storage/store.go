// Package storage provides persistence abstractions and PostgreSQL implementations
// for storing backend metadata, runtime configuration, routing policies, and health events.
package storage

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when an entity is not found in the store.
	ErrNotFound = errors.New("record not found")

	// ErrAlreadyExists is returned when attempting to create a record with an existing ID.
	ErrAlreadyExists = errors.New("record already exists")
)

// BackendRecord represents a persistent backend server entry.
type BackendRecord struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Weight    int       `json:"weight"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ConfigRecord represents a dynamic runtime configuration key-value pair.
type ConfigRecord struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RoutingPolicy defines algorithm and routing preferences for a path prefix.
type RoutingPolicy struct {
	PathPrefix string    `json:"path_prefix"`
	Algorithm  string    `json:"algorithm"` // round_robin, weighted, least_connections
	UpdatedAt  time.Time `json:"updated_at"`
}

// HealthEvent logs state transitions and health check failures for auditing.
type HealthEvent struct {
	ID        int64     `json:"id"`
	BackendID string    `json:"backend_id"`
	Status    string    `json:"status"` // HEALTHY, UNHEALTHY, RECOVERING
	EventTime time.Time `json:"event_time"`
	Details   string    `json:"details"`
}

// Store defines the persistence interface implemented by database backends.
type Store interface {
	// Backends
	ListBackends(ctx context.Context) ([]BackendRecord, error)
	GetBackend(ctx context.Context, id string) (*BackendRecord, error)
	UpsertBackend(ctx context.Context, b BackendRecord) error
	DeleteBackend(ctx context.Context, id string) error

	// Configuration
	GetConfig(ctx context.Context, key string) (string, error)
	SetConfig(ctx context.Context, key, value string) error
	GetAllConfig(ctx context.Context) (map[string]string, error)

	// Routing policies
	ListRoutingPolicies(ctx context.Context) ([]RoutingPolicy, error)
	SetRoutingPolicy(ctx context.Context, p RoutingPolicy) error

	// Health events
	RecordHealthEvent(ctx context.Context, event HealthEvent) error
	ListHealthEvents(ctx context.Context, limit int) ([]HealthEvent, error)

	// Lifecycle
	Close() error
}

package storage

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

func TestMemoryStore_Backends(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	// 1. Initial list empty
	list, err := s.ListBackends(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("expected empty list, got %v, err: %v", list, err)
	}

	// 2. Upsert
	b1 := BackendRecord{ID: "b-1", URL: "http://localhost:9001", Weight: 2}
	if err := s.UpsertBackend(ctx, b1); err != nil {
		t.Fatalf("failed to upsert backend: %v", err)
	}

	// 3. Get
	got, err := s.GetBackend(ctx, "b-1")
	if err != nil {
		t.Fatalf("failed to get backend: %v", err)
	}
	if got.URL != "http://localhost:9001" || got.Weight != 2 {
		t.Errorf("unexpected backend data: %+v", got)
	}

	// 4. Update
	b1Updated := BackendRecord{ID: "b-1", URL: "http://localhost:9001", Weight: 5}
	if err := s.UpsertBackend(ctx, b1Updated); err != nil {
		t.Fatalf("failed to update backend: %v", err)
	}
	gotUpdated, _ := s.GetBackend(ctx, "b-1")
	if gotUpdated.Weight != 5 {
		t.Errorf("expected weight 5, got %d", gotUpdated.Weight)
	}

	// 5. Delete
	if err := s.DeleteBackend(ctx, "b-1"); err != nil {
		t.Fatalf("failed to delete backend: %v", err)
	}
	_, err = s.GetBackend(ctx, "b-1")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStore_Configs(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	if err := s.SetConfig(ctx, "cache_ttl", "120s"); err != nil {
		t.Fatalf("failed to set config: %v", err)
	}
	if err := s.SetConfig(ctx, "rate_limit_rate", "100"); err != nil {
		t.Fatalf("failed to set config: %v", err)
	}

	val, err := s.GetConfig(ctx, "cache_ttl")
	if err != nil || val != "120s" {
		t.Errorf("expected 120s, got %s, err: %v", val, err)
	}

	all, err := s.GetAllConfig(ctx)
	if err != nil || len(all) != 2 {
		t.Errorf("expected 2 configs, got %v", all)
	}

	_, err = s.GetConfig(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for missing config, got %v", err)
	}
}

func TestMemoryStore_RoutingPolicies(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	p1 := RoutingPolicy{PathPrefix: "/api/users", Algorithm: "least_connections"}
	p2 := RoutingPolicy{PathPrefix: "/api/products", Algorithm: "weighted"}

	_ = s.SetRoutingPolicy(ctx, p1)
	_ = s.SetRoutingPolicy(ctx, p2)

	policies, err := s.ListRoutingPolicies(ctx)
	if err != nil || len(policies) != 2 {
		t.Fatalf("expected 2 policies, got %v", policies)
	}
}

func TestMemoryStore_HealthEvents(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	_ = s.RecordHealthEvent(ctx, HealthEvent{
		BackendID: "backend-1",
		Status:    "UNHEALTHY",
		Details:   "connection refused",
	})
	_ = s.RecordHealthEvent(ctx, HealthEvent{
		BackendID: "backend-1",
		Status:    "HEALTHY",
		Details:   "recovered after 2 checks",
	})

	events, err := s.ListHealthEvents(ctx, 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("expected 2 health events, got %v", events)
	}
	if events[1].Status != "HEALTHY" {
		t.Errorf("expected last event HEALTHY, got %s", events[1].Status)
	}
}

func TestMemoryStore_Concurrency(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			b := BackendRecord{
				ID:     string(rune('a' + (id % 26))),
				URL:    "http://127.0.0.1:9000",
				Weight: id,
			}
			_ = s.UpsertBackend(ctx, b)
			_, _ = s.GetBackend(ctx, b.ID)
			_ = s.SetConfig(ctx, "key", "val")
			_, _ = s.GetConfig(ctx, "key")
		}(i)
	}
	wg.Wait()
}

func isPostgresAvailable(connStr string) bool {
	conn, err := net.DialTimeout("tcp", "localhost:5432", 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func TestPostgresStore_LiveIntegration(t *testing.T) {
	connStr := "postgres://postgres:postgres@localhost:5432/proxy_db?sslmode=disable"
	if !isPostgresAvailable(connStr) {
		t.Skip("skipping Postgres integration test: no local Postgres server on localhost:5432")
	}

	store, err := NewPostgresStore(connStr)
	if err != nil {
		t.Skip("cannot connect to postgres:", err)
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	b := BackendRecord{ID: "pg-backend-1", URL: "http://127.0.0.1:9001", Weight: 3}
	if err := store.UpsertBackend(ctx, b); err != nil {
		t.Fatalf("failed to upsert in postgres: %v", err)
	}

	got, err := store.GetBackend(ctx, "pg-backend-1")
	if err != nil {
		t.Fatalf("failed to get from postgres: %v", err)
	}
	if got.Weight != 3 {
		t.Errorf("expected weight 3, got %d", got.Weight)
	}

	_ = store.DeleteBackend(ctx, "pg-backend-1")
}

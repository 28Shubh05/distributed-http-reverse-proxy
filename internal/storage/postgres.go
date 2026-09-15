package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// PostgresStore implements Store backed by a PostgreSQL database.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgresStore, validates connectivity,
// and ensures required tables are created.
func NewPostgresStore(connStr string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	s := &PostgresStore{db: db}
	if err := s.initSchema(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to initialize postgres schema: %w", err)
	}

	return s, nil
}

func (s *PostgresStore) initSchema(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS backends (
		id TEXT PRIMARY KEY,
		url TEXT NOT NULL,
		weight INT NOT NULL DEFAULT 1,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS configs (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS routing_policies (
		path_prefix TEXT PRIMARY KEY,
		algorithm TEXT NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS health_events (
		id BIGSERIAL PRIMARY KEY,
		backend_id TEXT NOT NULL,
		status TEXT NOT NULL,
		event_time TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		details TEXT NOT NULL DEFAULT ''
	);
	`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func (s *PostgresStore) ListBackends(ctx context.Context) ([]BackendRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, url, weight, created_at, updated_at FROM backends ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var backends []BackendRecord
	for rows.Next() {
		var b BackendRecord
		if err := rows.Scan(&b.ID, &b.URL, &b.Weight, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		backends = append(backends, b)
	}
	return backends, rows.Err()
}

func (s *PostgresStore) GetBackend(ctx context.Context, id string) (*BackendRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, url, weight, created_at, updated_at FROM backends WHERE id = $1`, id)
	var b BackendRecord
	if err := row.Scan(&b.ID, &b.URL, &b.Weight, &b.CreatedAt, &b.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &b, nil
}

func (s *PostgresStore) UpsertBackend(ctx context.Context, b BackendRecord) error {
	query := `
	INSERT INTO backends (id, url, weight, created_at, updated_at)
	VALUES ($1, $2, $3, NOW(), NOW())
	ON CONFLICT (id) DO UPDATE
	SET url = EXCLUDED.url,
	    weight = EXCLUDED.weight,
	    updated_at = NOW();
	`
	_, err := s.db.ExecContext(ctx, query, b.ID, b.URL, b.Weight)
	return err
}

func (s *PostgresStore) DeleteBackend(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM backends WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) GetConfig(ctx context.Context, key string) (string, error) {
	row := s.db.QueryRowContext(ctx, `SELECT value FROM configs WHERE key = $1`, key)
	var val string
	if err := row.Scan(&val); err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNotFound
		}
		return "", err
	}
	return val, nil
}

func (s *PostgresStore) SetConfig(ctx context.Context, key, value string) error {
	query := `
	INSERT INTO configs (key, value, updated_at)
	VALUES ($1, $2, NOW())
	ON CONFLICT (key) DO UPDATE
	SET value = EXCLUDED.value,
	    updated_at = NOW();
	`
	_, err := s.db.ExecContext(ctx, query, key, value)
	return err
}

func (s *PostgresStore) GetAllConfig(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM configs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		res[k] = v
	}
	return res, rows.Err()
}

func (s *PostgresStore) ListRoutingPolicies(ctx context.Context) ([]RoutingPolicy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path_prefix, algorithm, updated_at FROM routing_policies ORDER BY path_prefix ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []RoutingPolicy
	for rows.Next() {
		var p RoutingPolicy
		if err := rows.Scan(&p.PathPrefix, &p.Algorithm, &p.UpdatedAt); err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	return policies, rows.Err()
}

func (s *PostgresStore) SetRoutingPolicy(ctx context.Context, p RoutingPolicy) error {
	query := `
	INSERT INTO routing_policies (path_prefix, algorithm, updated_at)
	VALUES ($1, $2, NOW())
	ON CONFLICT (path_prefix) DO UPDATE
	SET algorithm = EXCLUDED.algorithm,
	    updated_at = NOW();
	`
	_, err := s.db.ExecContext(ctx, query, p.PathPrefix, p.Algorithm)
	return err
}

func (s *PostgresStore) RecordHealthEvent(ctx context.Context, event HealthEvent) error {
	query := `
	INSERT INTO health_events (backend_id, status, event_time, details)
	VALUES ($1, $2, NOW(), $3);
	`
	_, err := s.db.ExecContext(ctx, query, event.BackendID, event.Status, event.Details)
	return err
}

func (s *PostgresStore) ListHealthEvents(ctx context.Context, limit int) ([]HealthEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, backend_id, status, event_time, details FROM health_events ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []HealthEvent
	for rows.Next() {
		var ev HealthEvent
		if err := rows.Scan(&ev.ID, &ev.BackendID, &ev.Status, &ev.EventTime, &ev.Details); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}

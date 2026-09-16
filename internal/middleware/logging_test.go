package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLoggingMiddleware_StructuredJSONFields(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	var reqCount atomic.Uint64

	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-Server", "http://localhost:9001")
		w.Header().Set("X-Retry-Count", "1")
		w.Header().Set("X-Cache", "HIT")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mw := Logging(logger, &reqCount)
	wrapped := mw(dummyHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if reqCount.Load() != 1 {
		t.Fatalf("expected request count 1, got %d", reqCount.Load())
	}

	var logEntry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse structured JSON log: %v (raw: %s)", err, buf.String())
	}

	requiredFields := []string{
		"timestamp",
		"method",
		"path",
		"backend",
		"status",
		"latency",
		"latency_ms",
		"retry_count",
		"cache_hit",
	}

	for _, field := range requiredFields {
		if _, ok := logEntry[field]; !ok {
			t.Errorf("expected field %q in structured log, but was missing: %v", field, logEntry)
		}
	}

	if logEntry["method"] != "GET" {
		t.Errorf("expected method GET, got %v", logEntry["method"])
	}
	if logEntry["path"] != "/api/users" {
		t.Errorf("expected path /api/users, got %v", logEntry["path"])
	}
	if logEntry["backend"] != "http://localhost:9001" {
		t.Errorf("expected backend http://localhost:9001, got %v", logEntry["backend"])
	}
	if logEntry["cache_hit"] != true {
		t.Errorf("expected cache_hit true, got %v", logEntry["cache_hit"])
	}
}

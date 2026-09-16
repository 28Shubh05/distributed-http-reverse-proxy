package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/28Shubh05/distributed-http-reverse-proxy/internal/metrics"
)

func TestMetricsMiddleware(t *testing.T) {
	m, reg := metrics.NewTestMetrics()

	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		case "/bad":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		case "/error":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		}
	})

	mw := Metrics(m)
	wrapped := mw(dummyHandler)

	// Send /ok
	req1 := httptest.NewRequest(http.MethodGet, "/ok", nil)
	rr1 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr1.Code)
	}

	// Send /bad
	req2 := httptest.NewRequest(http.MethodPost, "/bad", nil)
	rr2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr2.Code)
	}

	// Send /error
	req3 := httptest.NewRequest(http.MethodGet, "/error", nil)
	rr3 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr3.Code)
	}

	// Gather metrics
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	foundReqTotal := false
	foundErrors := false
	for _, fam := range families {
		if fam.GetName() == "proxy_requests_total" {
			foundReqTotal = true
			if len(fam.Metric) != 3 {
				t.Errorf("expected 3 distinct (method, code) metrics, got %d", len(fam.Metric))
			}
		}
		if fam.GetName() == "proxy_request_errors_total" {
			foundErrors = true
			if len(fam.Metric) != 2 {
				t.Errorf("expected 2 error metrics (client_error, server_error), got %d", len(fam.Metric))
			}
		}
	}

	if !foundReqTotal {
		t.Errorf("proxy_requests_total not found in registry")
	}
	if !foundErrors {
		t.Errorf("proxy_request_errors_total not found in registry")
	}
}

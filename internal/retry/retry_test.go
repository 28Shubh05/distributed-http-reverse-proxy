package retry

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRetry_Idempotency(t *testing.T) {
	p := DefaultPolicy()

	if !p.IsIdempotent(http.MethodGet) {
		t.Errorf("GET should be idempotent")
	}
	if !p.IsIdempotent(http.MethodHead) {
		t.Errorf("HEAD should be idempotent")
	}
	if p.IsIdempotent(http.MethodPost) {
		t.Errorf("POST should NOT be idempotent by default")
	}
	if p.IsIdempotent(http.MethodPatch) {
		t.Errorf("PATCH should NOT be idempotent by default")
	}
}

func TestRetry_ShouldRetry(t *testing.T) {
	p := DefaultPolicy()
	p.MaxRetries = 2

	// 1. Idempotent GET with transport error -> retry attempt 0, 1
	if !p.ShouldRetry(http.MethodGet, 0, errors.New("connection refused"), 0) {
		t.Errorf("expected ShouldRetry to be true on transport error attempt 0")
	}
	if !p.ShouldRetry(http.MethodGet, 0, errors.New("connection reset"), 1) {
		t.Errorf("expected ShouldRetry to be true on transport error attempt 1")
	}
	// Exceeded MaxRetries -> false
	if p.ShouldRetry(http.MethodGet, 0, errors.New("connection reset"), 2) {
		t.Errorf("expected ShouldRetry to be false when attempt >= MaxRetries")
	}

	// 2. Idempotent GET with status codes
	if !p.ShouldRetry(http.MethodGet, http.StatusBadGateway, nil, 0) {
		t.Errorf("expected retry on 502 Bad Gateway")
	}
	if !p.ShouldRetry(http.MethodGet, http.StatusServiceUnavailable, nil, 0) {
		t.Errorf("expected retry on 503 Service Unavailable")
	}
	if !p.ShouldRetry(http.MethodGet, http.StatusGatewayTimeout, nil, 0) {
		t.Errorf("expected retry on 504 Gateway Timeout")
	}
	// 4xx errors should not be retried
	if p.ShouldRetry(http.MethodGet, http.StatusBadRequest, nil, 0) {
		t.Errorf("should not retry on 400 Bad Request")
	}
	if p.ShouldRetry(http.MethodGet, http.StatusNotFound, nil, 0) {
		t.Errorf("should not retry on 404 Not Found")
	}

	// 3. Non-idempotent POST should never be retried even on 503 or error
	if p.ShouldRetry(http.MethodPost, http.StatusServiceUnavailable, nil, 0) {
		t.Errorf("POST must not be retried on 503")
	}
	if p.ShouldRetry(http.MethodPost, 0, errors.New("connection reset"), 0) {
		t.Errorf("POST must not be retried on transport error")
	}
}

func TestRetry_BackoffExponentialAndCapped(t *testing.T) {
	p := &Policy{
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        1 * time.Second,
		BackoffMultiplier: 2.0,
		JitterFraction:    0.0, // disable jitter for deterministic math
	}

	// attempt 0: 100ms
	d0 := p.BackoffDuration(0)
	if d0 != 100*time.Millisecond {
		t.Errorf("attempt 0 expected 100ms, got %v", d0)
	}

	// attempt 1: 200ms
	d1 := p.BackoffDuration(1)
	if d1 != 200*time.Millisecond {
		t.Errorf("attempt 1 expected 200ms, got %v", d1)
	}

	// attempt 2: 400ms
	d2 := p.BackoffDuration(2)
	if d2 != 400*time.Millisecond {
		t.Errorf("attempt 2 expected 400ms, got %v", d2)
	}

	// attempt 5: 3200ms capped at 1000ms
	d5 := p.BackoffDuration(5)
	if d5 != 1*time.Second {
		t.Errorf("attempt 5 expected cap at 1s, got %v", d5)
	}
}

func TestRetry_Jitter(t *testing.T) {
	p := &Policy{
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        1 * time.Second,
		BackoffMultiplier: 2.0,
		JitterFraction:    0.25, // ±25%
	}

	// Sample multiple times to verify jitter varies within [75ms, 125ms]
	hasVariation := false
	first := p.BackoffDuration(0)

	for i := 0; i < 20; i++ {
		d := p.BackoffDuration(0)
		if d < 75*time.Millisecond || d > 125*time.Millisecond {
			t.Errorf("jittered delay %v out of [75ms, 125ms] range", d)
		}
		if d != first {
			hasVariation = true
		}
	}

	if !hasVariation {
		t.Errorf("expected jitter to produce varied backoff durations")
	}
}

func TestRetry_SleepContextCancellation(t *testing.T) {
	p := &Policy{
		InitialBackoff: 500 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
		JitterFraction: 0.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := p.Sleep(ctx, 0)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if elapsed >= 200*time.Millisecond {
		t.Errorf("Sleep took too long (%v), did not abort promptly on cancel", elapsed)
	}
}

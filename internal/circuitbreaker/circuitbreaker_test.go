package circuitbreaker

import (
	"sync"
	"testing"
	"time"
)

func TestCircuitBreaker_InitialState(t *testing.T) {
	cb := New(DefaultConfig())
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed, got %v", cb.State())
	}
	if !cb.Allow() {
		t.Fatalf("expected Allow() to be true in StateClosed")
	}
}

func TestCircuitBreaker_TripsOpenOnFailures(t *testing.T) {
	cfg := Config{
		FailureThreshold: 3,
		OpenTimeout:      1 * time.Second,
	}
	cb := New(cfg)

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed after 2 failures, got %v", cb.State())
	}

	cb.RecordFailure() // 3rd failure reaches threshold
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen after 3 failures, got %v", cb.State())
	}

	if cb.Allow() {
		t.Fatalf("expected Allow() to be false when StateOpen")
	}
}

func TestCircuitBreaker_SuccessResetsFailuresInClosed(t *testing.T) {
	cfg := Config{
		FailureThreshold: 3,
		OpenTimeout:      1 * time.Second,
	}
	cb := New(cfg)

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess() // should reset consecutive failures count

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed because success reset the streak, got %v", cb.State())
	}
}

func TestCircuitBreaker_TransitionsToHalfOpen(t *testing.T) {
	cfg := Config{
		FailureThreshold: 2,
		OpenTimeout:      50 * time.Millisecond,
	}
	cb := New(cfg)

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen, got %v", cb.State())
	}

	time.Sleep(70 * time.Millisecond)

	if cb.State() != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen after timeout, got %v", cb.State())
	}
}

func TestCircuitBreaker_ThunderingHerdProtectionInHalfOpen(t *testing.T) {
	cfg := Config{
		FailureThreshold:    1,
		OpenTimeout:         30 * time.Millisecond,
		HalfOpenMaxRequests: 2,
		SuccessThreshold:    2,
	}
	cb := New(cfg)

	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen")
	}

	time.Sleep(50 * time.Millisecond) // transitions to HalfOpen

	// First two trial requests should be allowed
	if !cb.Allow() {
		t.Fatalf("trial request 1 should be allowed")
	}
	if !cb.Allow() {
		t.Fatalf("trial request 2 should be allowed")
	}

	// Third request should be blocked to prevent thundering herd
	if cb.Allow() {
		t.Fatalf("trial request 3 should be blocked (thundering herd protection)")
	}
}

func TestCircuitBreaker_HalfOpenFailureTripsOpen(t *testing.T) {
	cfg := Config{
		FailureThreshold:    1,
		OpenTimeout:         30 * time.Millisecond,
		HalfOpenMaxRequests: 2,
		SuccessThreshold:    2,
	}
	cb := New(cfg)

	cb.RecordFailure()
	time.Sleep(50 * time.Millisecond)

	if !cb.Allow() {
		t.Fatalf("expected trial request to be allowed")
	}

	// Fail the trial request
	cb.RecordFailure()

	// Should immediately trip back to OPEN
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen after trial failure, got %v", cb.State())
	}
	if cb.Allow() {
		t.Fatalf("expected Allow() to be false immediately after trial failure")
	}
}

func TestCircuitBreaker_HalfOpenRecoveryToClosed(t *testing.T) {
	cfg := Config{
		FailureThreshold:    1,
		OpenTimeout:         30 * time.Millisecond,
		HalfOpenMaxRequests: 2,
		SuccessThreshold:    2,
	}
	cb := New(cfg)

	cb.RecordFailure()
	time.Sleep(50 * time.Millisecond)

	if !cb.Allow() {
		t.Fatalf("expected trial 1 to be allowed")
	}
	cb.RecordSuccess()

	if cb.State() != StateHalfOpen {
		t.Fatalf("expected still StateHalfOpen after 1 success, got %v", cb.State())
	}

	if !cb.Allow() {
		t.Fatalf("expected trial 2 to be allowed")
	}
	cb.RecordSuccess()

	// Reached SuccessThreshold -> CLOSED
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed after reaching success threshold, got %v", cb.State())
	}
	if !cb.Allow() {
		t.Fatalf("expected Allow() to be true in StateClosed")
	}
}

func TestCircuitBreaker_Concurrency(t *testing.T) {
	cfg := Config{
		FailureThreshold:    5,
		OpenTimeout:         10 * time.Millisecond,
		HalfOpenMaxRequests: 3,
		SuccessThreshold:    3,
	}
	cb := New(cfg)

	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if cb.Allow() {
				if id%3 == 0 {
					cb.RecordFailure()
				} else {
					cb.RecordSuccess()
				}
			}
			_ = cb.State()
		}(i)
	}
	wg.Wait()
}

func TestStateString(t *testing.T) {
	if StateClosed.String() != "CLOSED" {
		t.Errorf("expected CLOSED, got %s", StateClosed.String())
	}
	if StateOpen.String() != "OPEN" {
		t.Errorf("expected OPEN, got %s", StateOpen.String())
	}
	if StateHalfOpen.String() != "HALF_OPEN" {
		t.Errorf("expected HALF_OPEN, got %s", StateHalfOpen.String())
	}
	if State(99).String() != "UNKNOWN" {
		t.Errorf("expected UNKNOWN, got %s", State(99).String())
	}
}

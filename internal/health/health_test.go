package health

import (
	"testing"
	"time"
)

func makeHealth(unhealthy, recovery int) *BackendHealth {
	cfg := DefaultConfig()
	cfg.UnhealthyThreshold = unhealthy
	cfg.RecoveryThreshold = recovery
	return NewBackendHealth(cfg)
}

// TestInitialState verifies a new BackendHealth starts HEALTHY and available.
func TestInitialState(t *testing.T) {
	bh := makeHealth(3, 2)
	if bh.Status() != StatusHealthy {
		t.Fatalf("expected HEALTHY, got %s", bh.Status())
	}
	if !bh.IsAvailable() {
		t.Fatal("expected backend to be available initially")
	}
}

// TestHealthyToUnhealthy verifies transition after reaching the failure threshold.
func TestHealthyToUnhealthy(t *testing.T) {
	bh := makeHealth(3, 2)

	bh.RecordFailure()
	if bh.Status() != StatusHealthy {
		t.Fatalf("expected HEALTHY after 1 failure, got %s", bh.Status())
	}

	bh.RecordFailure()
	if bh.Status() != StatusHealthy {
		t.Fatalf("expected HEALTHY after 2 failures, got %s", bh.Status())
	}

	bh.RecordFailure() // 3rd failure hits threshold
	if bh.Status() != StatusUnhealthy {
		t.Fatalf("expected UNHEALTHY after %d failures, got %s", 3, bh.Status())
	}
	if bh.IsAvailable() {
		t.Fatal("expected unavailable backend when UNHEALTHY")
	}
}

// TestUnhealthyToRecoveringToHealthy verifies the full recovery state machine.
func TestUnhealthyToRecoveringToHealthy(t *testing.T) {
	bh := makeHealth(3, 2)

	// Drive to UNHEALTHY
	bh.RecordFailure()
	bh.RecordFailure()
	bh.RecordFailure()
	if bh.Status() != StatusUnhealthy {
		t.Fatalf("expected UNHEALTHY, got %s", bh.Status())
	}

	// First success → RECOVERING
	bh.RecordSuccess()
	if bh.Status() != StatusRecovering {
		t.Fatalf("expected RECOVERING after 1 success, got %s", bh.Status())
	}
	if !bh.IsAvailable() {
		t.Fatal("RECOVERING backend should be available for traffic")
	}

	// Second success → HEALTHY (recovery threshold = 2)
	bh.RecordSuccess()
	if bh.Status() != StatusHealthy {
		t.Fatalf("expected HEALTHY after recovery, got %s", bh.Status())
	}
}

// TestRecoveringReset verifies that a failure during RECOVERING resets to UNHEALTHY.
func TestRecoveringReset(t *testing.T) {
	bh := makeHealth(3, 3) // recovery threshold = 3

	// Drive to UNHEALTHY
	bh.RecordFailure()
	bh.RecordFailure()
	bh.RecordFailure()

	// 2 successes → RECOVERING (not yet at threshold)
	bh.RecordSuccess()
	bh.RecordSuccess()
	if bh.Status() != StatusRecovering {
		t.Fatalf("expected RECOVERING, got %s", bh.Status())
	}

	// A failure during RECOVERING resets the consecutive success counter and
	// if failure threshold hit → back to UNHEALTHY.
	bh.RecordFailure()
	bh.RecordFailure()
	bh.RecordFailure()
	if bh.Status() != StatusUnhealthy {
		t.Fatalf("expected UNHEALTHY after failure during recovery, got %s", bh.Status())
	}
}

// TestSuccessResetsFailureCounter verifies a success breaks a failure streak.
func TestSuccessResetsFailureCounter(t *testing.T) {
	bh := makeHealth(3, 2)

	bh.RecordFailure()
	bh.RecordFailure()
	// One success should reset the consecutive failure count
	bh.RecordSuccess()
	// Next failure should NOT immediately cause UNHEALTHY
	bh.RecordFailure()
	bh.RecordFailure()
	if bh.Status() != StatusHealthy {
		t.Fatalf("expected HEALTHY, got %s", bh.Status())
	}
	// Third failure after the reset should now cause UNHEALTHY
	bh.RecordFailure()
	if bh.Status() != StatusUnhealthy {
		t.Fatalf("expected UNHEALTHY, got %s", bh.Status())
	}
}

// TestSnapshot verifies stats are captured correctly.
func TestSnapshot(t *testing.T) {
	bh := makeHealth(3, 2)

	before := time.Now()
	bh.RecordSuccess()
	bh.RecordFailure()
	after := time.Now()

	s := bh.Snapshot()
	if s.TotalChecks != 2 {
		t.Fatalf("expected 2 total checks, got %d", s.TotalChecks)
	}
	if s.TotalSuccess != 1 {
		t.Fatalf("expected 1 success, got %d", s.TotalSuccess)
	}
	if s.TotalFailure != 1 {
		t.Fatalf("expected 1 failure, got %d", s.TotalFailure)
	}
	if s.LastCheck.Before(before) || s.LastCheck.After(after) {
		t.Fatalf("LastCheck out of expected range")
	}
}

// TestStatusString verifies the string representation.
func TestStatusString(t *testing.T) {
	cases := []struct {
		s    Status
		want string
	}{
		{StatusHealthy, "HEALTHY"},
		{StatusUnhealthy, "UNHEALTHY"},
		{StatusRecovering, "RECOVERING"},
		{Status(99), "UNKNOWN"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("Status(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}

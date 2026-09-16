package health

import "testing"

func TestReadinessRequiresEveryGate(t *testing.T) {
	readiness := NewReadiness("database", "migrations")
	readiness.Set("database", true)
	if ready, _ := readiness.Snapshot(); ready {
		t.Fatal("readiness passed with a closed gate")
	}
	readiness.Set("migrations", true)
	if ready, _ := readiness.Snapshot(); !ready {
		t.Fatal("readiness failed after all gates opened")
	}
}

package trade

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestW4ADisabledProductionCompositionFailsClosed(t *testing.T) {
	store := canaryStore(t)
	composition, err := NewProductionComposition(store, ProductionModeDisabled)
	if err != nil {
		t.Fatal(err)
	}
	composition.now = func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }
	if err = composition.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	readiness := composition.Readiness()
	if readiness.CanaryAdmissionReady || readiness.SubmissionSendReady || readiness.RecoveryReady {
		t.Fatalf("W4-A must remain disabled: %#v", readiness)
	}
	if readiness.ReasonCode != "W4A_DISABLED_RECOVERY_NOT_WIRED" {
		t.Fatalf("unexpected readiness reason: %#v", readiness)
	}

	var audits, permits, submissions int
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='W4_PRODUCTION_STARTUP' AND reason_code='PRODUCTION_WIRING_DISABLED'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("durable disabled startup audit = %d, err = %v", audits, err)
	}
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM canary_send_permits WHERE state='CONSUMED'`).Scan(&permits); err != nil || permits != 0 {
		t.Fatalf("consumed permits = %d, err = %v", permits, err)
	}
	if err = store.db.QueryRow(`SELECT COUNT(*) FROM transaction_submissions`).Scan(&submissions); err != nil || submissions != 0 {
		t.Fatalf("submission rows = %d, err = %v", submissions, err)
	}

	var metrics bytes.Buffer
	if err = composition.WriteMetrics(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	for _, metric := range []string{"rbh_canary_admission_ready 0", "rbh_submission_send_ready 0", "rbh_recovery_ready 0"} {
		if !strings.Contains(metrics.String(), metric) {
			t.Fatalf("missing metric %q in %q", metric, metrics.String())
		}
	}
}

func TestW4ARejectsAnyProductionEnablement(t *testing.T) {
	store := canaryStore(t)
	for _, mode := range []string{"", "CONTROLLED_CANARY", "UNRESTRICTED_LIVE"} {
		if _, err := NewProductionComposition(store, mode); err == nil {
			t.Fatalf("mode %q must fail closed", mode)
		}
	}
}

func TestW4AProductionCompositionHasNoEconomicDependencies(t *testing.T) {
	typ := reflect.TypeOf(ProductionComposition{})
	want := map[string]bool{"store": true, "mode": true, "now": true}
	if typ.NumField() != len(want) {
		t.Fatalf("unexpected production dependencies: %d fields", typ.NumField())
	}
	for i := 0; i < typ.NumField(); i++ {
		if !want[typ.Field(i).Name] {
			t.Fatalf("W4-A dependency %q is not allowed", typ.Field(i).Name)
		}
	}
}

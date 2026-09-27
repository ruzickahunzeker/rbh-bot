package trade

import (
	"context"
	"testing"
	"time"
)

func newTransitionAuthorization(t *testing.T, id string) (*Store, RuntimeAuthorization, time.Time) {
	t.Helper()
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	a, err := s.CreateRuntimeAuthorization(context.Background(), runtimeAuthorization(id, now), now)
	if err != nil {
		t.Fatal(err)
	}
	return s, a, now
}

func transitionAuditCount(t *testing.T, s *Store, id string) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE authorization_id=? AND event_type='AUTHORIZATION_STATE_CHANGED'`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestDirectSQLAuthorizationTransitionCreatesExactlyOneAudit(t *testing.T) {
	s, a, now := newTransitionAuthorization(t, "direct-sql-arm")
	result, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED',updated_at=? WHERE id=? AND state='PENDING'`, now.Format(time.RFC3339Nano), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		t.Fatalf("changed=%d", changed)
	}
	if count := transitionAuditCount(t, s, a.ID); count != 1 {
		t.Fatalf("transition audits=%d", count)
	}
}

func TestDirectSQLAuthorizationTransitionRollsBackWhenAuditFails(t *testing.T) {
	s, a, now := newTransitionAuthorization(t, "direct-sql-audit-failure")
	if _, err := s.db.Exec(`CREATE TRIGGER test_force_transition_audit_failure BEFORE INSERT ON canary_runtime_audit WHEN NEW.event_type='AUTHORIZATION_STATE_CHANGED' BEGIN SELECT RAISE(ABORT,'forced audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED',updated_at=? WHERE id=? AND state='PENDING'`, now.Format(time.RFC3339Nano), a.ID); err == nil {
		t.Fatal("transition survived audit failure")
	}
	var state string
	if err := s.db.QueryRow(`SELECT state FROM canary_runtime_authorizations WHERE id=?`, a.ID).Scan(&state); err != nil || state != "PENDING" {
		t.Fatalf("state=%s err=%v", state, err)
	}
	if count := transitionAuditCount(t, s, a.ID); count != 0 {
		t.Fatalf("transition audits=%d", count)
	}
}

func TestStoreAuthorizationTransitionCreatesExactlyOneAudit(t *testing.T) {
	s, a, now := newTransitionAuthorization(t, "store-api-arm")
	if err := s.TransitionRuntimeAuthorization(context.Background(), a.ID, "PENDING", "ARMED", now); err != nil {
		t.Fatal(err)
	}
	if count := transitionAuditCount(t, s, a.ID); count != 1 {
		t.Fatalf("transition audits=%d", count)
	}
}

func TestArmedTerminalTransitionsCreateExactlyOneAudit(t *testing.T) {
	for _, terminal := range []string{"REVOKED", "EXPIRED", "EXHAUSTED"} {
		t.Run(terminal, func(t *testing.T) {
			s, a, now := newTransitionAuthorization(t, "terminal-"+terminal)
			if err := s.TransitionRuntimeAuthorization(context.Background(), a.ID, "PENDING", "ARMED", now); err != nil {
				t.Fatal(err)
			}
			before := transitionAuditCount(t, s, a.ID)
			if err := s.TransitionRuntimeAuthorization(context.Background(), a.ID, "ARMED", terminal, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if after := transitionAuditCount(t, s, a.ID); after-before != 1 || after != 2 {
				t.Fatalf("before=%d after=%d", before, after)
			}
		})
	}
}

func TestInvalidAuthorizationTransitionHasNoAudit(t *testing.T) {
	s, a, now := newTransitionAuthorization(t, "invalid-transition")
	if _, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET state='EXHAUSTED',updated_at=? WHERE id=?`, now.Format(time.RFC3339Nano), a.ID); err == nil {
		t.Fatal("invalid transition accepted")
	}
	if count := transitionAuditCount(t, s, a.ID); count != 0 {
		t.Fatalf("transition audits=%d", count)
	}
}

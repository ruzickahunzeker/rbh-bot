package trade

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestRuntimeAuditAppendOnlyAndAuthorizationTransitionAtomic(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	a, err := s.CreateRuntimeAuthorization(context.Background(), runtimeAuthorization("audit-auth", now), now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.db.Exec(`UPDATE canary_runtime_audit SET reason_code='tampered'`); err == nil {
		t.Fatal("direct runtime audit UPDATE accepted")
	}
	if _, err = s.db.Exec(`DELETE FROM canary_runtime_audit`); err == nil {
		t.Fatal("direct runtime audit DELETE accepted")
	}

	if _, err = s.db.Exec(`CREATE TRIGGER test_reject_state_audit BEFORE INSERT ON canary_runtime_audit WHEN NEW.event_type='AUTHORIZATION_STATE_CHANGED' BEGIN SELECT RAISE(ABORT,'test audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.TransitionRuntimeAuthorization(context.Background(), a.ID, "PENDING", "ARMED", now); err == nil {
		t.Fatal("transition committed without its durable audit")
	}
	var state string
	if err = s.db.QueryRow(`SELECT state FROM canary_runtime_authorizations WHERE id=?`, a.ID).Scan(&state); err != nil || state != "PENDING" {
		t.Fatalf("state=%s err=%v", state, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER test_reject_state_audit`); err != nil {
		t.Fatal(err)
	}
	if err = s.TransitionRuntimeAuthorization(context.Background(), a.ID, "PENDING", "ARMED", now); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE authorization_id=? AND event_type='AUTHORIZATION_STATE_CHANGED'`, a.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("transition audits=%d err=%v", audits, err)
	}
}

func TestRuntimeBudgetConcurrentMaxOperationsConservation(t *testing.T) {
	runRuntimeBudgetConcurrency(t, "max-operations", 2, "1000", "1", 100, 2, "2")
}

func TestRuntimeBudgetConcurrentMaxTotalInputConservation(t *testing.T) {
	runRuntimeBudgetConcurrency(t, "max-total-input", 100, "10", "3", 100, 3, "9")
}

func runRuntimeBudgetConcurrency(t *testing.T, name string, maxOperations uint64, maxTotal, amount string, requests, wantAccepted int, wantTotal string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), name+".db")
	database, err := storage.Open(ctx, storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(database)
	if err != nil {
		t.Fatal(err)
	}
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	a := runtimeAuthorization("concurrent-"+name, now)
	a.MaxOperations, a.MaxTotalInput = maxOperations, maxTotal
	a, err = s.CreateRuntimeAuthorization(ctx, a, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.TransitionRuntimeAuthorization(ctx, a.ID, "PENDING", "ARMED", now); err != nil {
		t.Fatal(err)
	}

	operationIDs := make([]string, requests)
	for i := range requests {
		r := canaryRequest(fmt.Sprintf("%s-%03d", name, i), now)
		persistSource(t, s, r)
		operationIDs[i] = r.OperationID
	}
	var accepted, rejected, unexplained atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, operationID := range operationIDs {
		wg.Add(1)
		go func(operation string) {
			defer wg.Done()
			<-start
			duplicate, reserveErr := s.ReserveRuntimeAuthorizationBudget(ctx, a.ID, operation, amount, now)
			switch {
			case reserveErr == nil && !duplicate:
				accepted.Add(1)
			case errors.Is(reserveErr, ErrCanaryRuntimeRejected):
				rejected.Add(1)
			default:
				unexplained.Add(1)
			}
		}(operationID)
	}
	close(start)
	wg.Wait()
	if got := int(accepted.Load()); got != wantAccepted {
		t.Fatalf("accepted=%d want=%d", got, wantAccepted)
	}
	if got := int(rejected.Load()); got != requests-wantAccepted {
		t.Fatalf("rejected=%d", got)
	}
	if unexplained.Load() != 0 {
		t.Fatalf("unexplained accepts/errors=%d", unexplained.Load())
	}

	var count, rows int
	var total, rowTotal string
	if err = s.db.QueryRow(`SELECT operation_count,total_input FROM canary_authorization_usage WHERE authorization_id=?`, a.ID).Scan(&count, &total); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(CAST(amount AS INTEGER)),0) FROM canary_authorization_operation_usage WHERE authorization_id=?`, a.ID).Scan(&rows, &rowTotal); err != nil {
		t.Fatal(err)
	}
	if count != wantAccepted || rows != wantAccepted || total != wantTotal || rowTotal != wantTotal {
		t.Fatalf("usage count=%d rows=%d total=%s row_total=%s", count, rows, total, rowTotal)
	}

	acceptedOperation := operationIDs[0]
	if _, err = s.db.Exec(`SELECT 1`); err != nil {
		t.Fatal(err)
	}
	var storedAmount string
	if err = s.db.QueryRow(`SELECT operation_id,amount FROM canary_authorization_operation_usage WHERE authorization_id=? LIMIT 1`, a.ID).Scan(&acceptedOperation, &storedAmount); err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.ReserveRuntimeAuthorizationBudget(ctx, a.ID, acceptedOperation, storedAmount, now)
	if err != nil || !duplicate {
		t.Fatalf("duplicate=%v err=%v", duplicate, err)
	}

	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = storage.Open(ctx, storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(database)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT operation_count,total_input FROM canary_authorization_usage WHERE authorization_id=?`, a.ID).Scan(&count, &total); err != nil || count != wantAccepted || total != wantTotal {
		t.Fatalf("restart usage count=%d total=%s err=%v", count, total, err)
	}
	duplicate, err = s.ReserveRuntimeAuthorizationBudget(ctx, a.ID, acceptedOperation, storedAmount, now)
	if err != nil || !duplicate {
		t.Fatalf("restart duplicate=%v err=%v", duplicate, err)
	}
}

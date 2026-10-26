package tradeapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestHTTPABCLifecycleCursorPaginationAndReopen(t *testing.T) {
	ctx := context.Background()
	database, store, original, path := fixture(t)
	h := intakeAPI(t, store, original)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "lifecycle-cursor", encode(buy())))
	var first uint64
	if err := store.db.QueryRow(`SELECT sequence FROM manual_http_order_sources WHERE id=?`, r.RequestID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	initial, err := store.Events(ctx, h.identity, r.RequestID, 0)
	if err != nil || initial.Scope != "MANUAL_ORDER_LIFECYCLE" || len(initial.Events) != 1 || initial.NextCursor != first || initial.Events[0].Type != "MANUAL_HTTP_ORDER_RECORDED" {
		t.Fatalf("intake cursor compatibility: %+v %v", initial, err)
	}
	// Test-only audit observations exercise journal pagination, not recovery
	// state transitions. Equal timestamps cannot reorder the durable cursor.
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 130; i++ {
		_, err := tx.ExecContext(ctx, `INSERT INTO canary_runtime_audit(id,event_type,operation_id,reason_code,details_json,created_at) VALUES(?,'MANUAL_LIFECYCLE_TEST',?,'TEST_OBSERVATION','{}',?)`, fmt.Sprintf("lifecycle-test-%03d", i), *r.OperationID, r.CreatedAt)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	page, err := store.Events(ctx, h.identity, r.RequestID, initial.NextCursor)
	if err != nil || len(page.Events) != manualLifecyclePageSize || page.NextCursor != first+100 {
		t.Fatalf("first bounded page: %+v %v", page, err)
	}
	for i, e := range page.Events {
		if e.Cursor != first+uint64(i)+1 {
			t.Fatal("noncontiguous journal cursor", e.Cursor)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(ctx, storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	fresh, err := NewStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, fresh.db)
	tail, err := fresh.Events(ctx, h.identity, r.RequestID, page.NextCursor)
	if err != nil || len(tail.Events) != 30 || tail.NextCursor != first+130 {
		t.Fatalf("reopened continuation: %+v %v", tail, err)
	}
	empty, err := fresh.Events(ctx, h.identity, r.RequestID, tail.NextCursor)
	if err != nil || len(empty.Events) != 0 || empty.NextCursor != tail.NextCursor || admittedDatabaseSnapshot(t, fresh.db) != before {
		t.Fatal("empty page advanced cursor or history read mutated state", empty, err)
	}
}

func TestHTTPABCLifecycleAuditAndJournalAreAtomicAndImmutable(t *testing.T) {
	_, store, original, _ := fixture(t)
	h := intakeAPI(t, store, original)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "lifecycle-fault", encode(buy())))
	if _, err := store.db.Exec(`CREATE TRIGGER lifecycle_test_fault BEFORE INSERT ON manual_lifecycle_events WHEN NEW.event_type='MANUAL_LIFECYCLE_TEST_FAILURE' BEGIN SELECT RAISE(ABORT,'test journal insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	beforeAudits, beforeEvents := count(t, store.db, "canary_runtime_audit"), count(t, store.db, "manual_lifecycle_events")
	if _, err := store.db.Exec(`INSERT INTO canary_runtime_audit(id,event_type,operation_id,reason_code,details_json,created_at) VALUES('lifecycle-failed-audit','MANUAL_LIFECYCLE_TEST_FAILURE',?,'TEST_FAILURE','{}',?)`, *r.OperationID, r.CreatedAt); err == nil {
		t.Fatal("journal failure did not reject audit statement")
	}
	if count(t, store.db, "canary_runtime_audit") != beforeAudits || count(t, store.db, "manual_lifecycle_events") != beforeEvents {
		t.Fatal("partial audit/journal committed after insert failure")
	}
	for _, statement := range []string{
		`UPDATE manual_lifecycle_events SET cursor=cursor+1`,
		`UPDATE manual_lifecycle_events SET reason_code='changed'`,
		`DELETE FROM manual_lifecycle_events`,
		`INSERT OR REPLACE INTO manual_lifecycle_events SELECT * FROM manual_lifecycle_events`,
	} {
		if _, err := store.db.Exec(statement); err == nil {
			t.Fatal("journal mutation accepted", statement)
		}
	}
	if _, err := store.Events(context.Background(), h.identity, r.RequestID, 0); err != nil {
		t.Fatal("rejected mutations damaged journal", err)
	}
}

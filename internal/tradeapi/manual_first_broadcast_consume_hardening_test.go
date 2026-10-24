package tradeapi

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestHTTPM4B24ThreeSidesConsumeMetadataOnly(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			f := manualB24FixtureForSide(t, side)
			before := manualB24EconomicSnapshot(t, f.s)
			if reason, err := f.trade.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, f.permit.ID, f.raw, f.permit.IssuedAt.Add(time.Nanosecond)); err != nil {
				t.Fatalf("exact %s permit rejected: %s: %v", side, reason, err)
			}
			if manualB24EconomicSnapshot(t, f.s) != before {
				t.Fatal("metadata consume changed original artifact/nonce/accounting")
			}
			for _, table := range []string{"transaction_submissions", "receipt_observations", "position_effects", "manual_first_broadcast_consume_scopes", "manual_first_broadcast_consume_checks"} {
				if count(t, f.s.db, table) != 0 {
					t.Fatal("metadata consume created downstream state or retained transient scope", table)
				}
			}
			if countEvent(t, f.s.db, "MANUAL_FIRST_BROADCAST_CONSUMED") != 1 {
				t.Fatal("missing exact one-shot consumption audit")
			}
		})
	}
}

func TestHTTPM4B24EmergencyStopPrecedesPermitIdentityAndDisabledMode(t *testing.T) {
	f := manualB24Fixture(t)
	if _, err := f.s.db.Exec(`UPDATE canary_control_state SET mode='DISABLED',emergency_stopped=1`); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, f.s.db)
	if reason, err := f.trade.ConsumeManualFirstBroadcastPermit(context.Background(), f.r, "missing-permit", "invalid", f.permit.IssuedAt.Add(time.Nanosecond)); err == nil || reason != "EMERGENCY_STOPPED" {
		t.Fatalf("emergency stop did not have priority: %s: %v", reason, err)
	}
	if admittedDatabaseSnapshot(t, f.s.db) != before {
		t.Fatal("stopped consumer mutated durable rows")
	}
}

func TestHTTPM4B24ReplayPurposeRemainsClosed(t *testing.T) {
	f := manualB24Fixture(t)
	before := admittedDatabaseSnapshot(t, f.s.db)
	if _, err := f.s.db.Exec(`INSERT INTO canary_send_permits(id,operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,query_evidence_hash,queried_at,state,issued_at,expires_at)
	 SELECT 'b24-replay',operation_id,attempt_id,authorization_id,authorization_epoch,'UNKNOWN_REPLAY',artifact_hash,tx_hash,?,issued_at,'ISSUED',issued_at,expires_at FROM canary_send_permits WHERE id=?`, strings.Repeat("a", 64), f.permit.ID); err == nil {
		t.Fatal("MANUAL UNKNOWN_REPLAY permit became issuable")
	}
	if _, err := f.s.db.Exec(`UPDATE canary_send_permits SET purpose='UNKNOWN_REPLAY',query_evidence_hash=?,queried_at=issued_at WHERE id=?`, strings.Repeat("a", 64), f.permit.ID); err == nil {
		t.Fatal("FIRST_BROADCAST permit became a replay permit")
	}
	if admittedDatabaseSnapshot(t, f.s.db) != before {
		t.Fatal("rejected replay mutation changed original permit or phase")
	}
}

func TestHTTPM4B24DirectSQLFailedAuditCannotCommitPartialConsume(t *testing.T) {
	for _, table := range []string{"canary_runtime_audit", "canary_alert_outbox"} {
		t.Run(table, func(t *testing.T) {
			f := manualB24Fixture(t)
			when := `NEW.event_type='MANUAL_FIRST_BROADCAST_CONSUMED'`
			if table == "canary_alert_outbox" {
				when = `NEW.audit_id='manual-first-permit-consumed-audit:` + f.permit.ID + `'`
			}
			if _, err := f.s.db.Exec(`CREATE TRIGGER fail_b24_sql_witness BEFORE INSERT ON ` + table + ` WHEN ` + when + ` BEGIN SELECT RAISE(FAIL,'forced direct SQL witness failure'); END`); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, f.s.db)
			conn, err := f.s.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := conn.ExecContext(context.Background(), `BEGIN`); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(context.Background(), `ROLLBACK`)
			stamp := f.permit.IssuedAt.Add(time.Nanosecond).Format(time.RFC3339Nano)
			if _, err := conn.ExecContext(context.Background(), `INSERT INTO manual_first_broadcast_consume_scopes(permit_id,operation_id,attempt_id,authorization_id,authorization_epoch,artifact_hash,opened_at) VALUES(?,?,?,?,?,?,?)`, f.permit.ID, f.r.OperationID, f.r.AttemptID, f.r.AuthorizationID, f.r.AuthorizationEpoch, f.raw, stamp); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(context.Background(), `UPDATE canary_send_permits SET state='CONSUMED',consumed_at=? WHERE id=?`, stamp, f.permit.ID); err == nil {
				t.Fatal("direct SQL witness failure accepted")
			}
			if _, err := conn.ExecContext(context.Background(), `COMMIT`); err == nil {
				t.Fatal("partial direct SQL consume committed")
			}
			if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			if admittedDatabaseSnapshot(t, f.s.db) != before {
				t.Fatal("rollback changed original permit or durable witnesses")
			}
		})
	}
}

func TestHTTPM4B24ControlCommitAfterRecheckRejectsConsume(t *testing.T) {
	for _, kind := range []string{"stop", "revoke", "lease-takeover"} {
		t.Run(kind, func(t *testing.T) {
			f := manualB24Fixture(t)
			other, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			db, err := other.SQLDB(storage.TradeOwner)
			if err != nil {
				t.Fatal(err)
			}
			rechecked, resume := make(chan struct{}), make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(resume) })
			f.trade.SetCanaryHookForTest(func(stage string) error {
				if stage == "manual_consume_rechecked" {
					close(rechecked)
					<-resume
				}
				return nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				_, err := f.trade.ConsumeManualFirstBroadcastPermit(ctx, f.r, f.permit.ID, f.raw, f.permit.IssuedAt.Add(time.Nanosecond))
				finished <- err
			}()
			select {
			case <-rechecked:
			case <-ctx.Done():
				t.Fatal("consume did not reach final recheck", ctx.Err())
			}
			// This mutation COMMITs after the old reader's checks and before
			// its attempted write. WAL must reject the stale writer snapshot.
			switch kind {
			case "stop":
				_, err = db.ExecContext(ctx, `UPDATE canary_control_state SET emergency_stopped=1`)
			case "revoke":
				_, err = db.ExecContext(ctx, `UPDATE canary_runtime_authorizations SET state='REVOKED' WHERE id=?`, f.r.AuthorizationID)
			case "lease-takeover":
				_, err = db.ExecContext(ctx, `UPDATE canary_worker_leases SET holder_id='new-owner',lease_epoch=lease_epoch+1 WHERE role='SUBMISSION'`)
			}
			if err != nil {
				t.Fatal("competing control update did not commit", err)
			}
			before := admittedDatabaseSnapshot(t, db)
			release.Do(func() { close(resume) })
			select {
			case err := <-finished:
				if err == nil {
					t.Fatal("stale control snapshot consumed permit")
				}
			case <-ctx.Done():
				t.Fatal("stale consumer did not terminate", ctx.Err())
			}
			if admittedDatabaseSnapshot(t, db) != before {
				t.Fatal("rejected stale consume changed control, permit or witness rows")
			}
		})
	}
}

func TestHTTPM4B24DirectSQLWitnessCannotBypassFinalControls(t *testing.T) {
	for _, kind := range []string{"stop", "revoke", "lease-takeover", "timestamp-mismatch", "expiry"} {
		t.Run(kind, func(t *testing.T) {
			f := manualB24Fixture(t)
			now := f.permit.IssuedAt.Add(time.Nanosecond)
			before := admittedDatabaseSnapshot(t, f.s.db)
			tx, err := f.s.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if kind == "expiry" {
				now = f.permit.ExpiresAt
			}
			_, err = tx.Exec(`INSERT INTO manual_first_broadcast_consume_scopes(permit_id,operation_id,attempt_id,authorization_id,authorization_epoch,artifact_hash,opened_at) VALUES(?,?,?,?,?,?,?)`, f.permit.ID, f.r.OperationID, f.r.AttemptID, f.r.AuthorizationID, f.r.AuthorizationEpoch, f.raw, now.Format(time.RFC3339Nano))
			if kind == "expiry" {
				if err == nil {
					t.Fatal("direct SQL opened witness at expiry")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "stop":
					_, err = tx.Exec(`UPDATE canary_control_state SET emergency_stopped=1`)
				case "revoke":
					_, err = tx.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED' WHERE id=?`, f.r.AuthorizationID)
				case "lease-takeover":
					_, err = tx.Exec(`UPDATE canary_worker_leases SET holder_id='new-owner',lease_epoch=lease_epoch+1 WHERE role='SUBMISSION'`)
				case "timestamp-mismatch":
					now = now.Add(time.Nanosecond)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(`UPDATE canary_send_permits SET state='CONSUMED',consumed_at=? WHERE id=?`, now.Format(time.RFC3339Nano), f.permit.ID); err == nil {
					t.Fatal("direct SQL bypassed current controls or witness timestamp")
				}
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if admittedDatabaseSnapshot(t, f.s.db) != before {
				t.Fatal("rejected SQL transition left partial data")
			}
		})
	}
}

func TestHTTPM4B24ConsumerHasNoProductionCallerOrTransportDependency(t *testing.T) {
	path := filepath.Join("..", "trade", "manual_first_broadcast_consume.go")
	tree, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range tree.Imports {
		if imported.Path.Value != `"context"` && imported.Path.Value != `"strings"` && imported.Path.Value != `"time"` {
			t.Error("consumer gained dependency", imported.Path.Value)
		}
	}
	allowedCalls := map[string]bool{"BeginTx": true, "Rollback": true, "QueryRowContext": true, "Scan": true, "EqualFold": true, "ToLower": true, "UTC": true, "Format": true, "IsZero": true, "ExecContext": true, "RowsAffected": true, "Commit": true, "canaryFault": true}
	ast.Inspect(tree, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !allowedCalls[method.Sel.Name] {
				t.Errorf("consumer gained unreviewed call at %v", call.Pos())
			}
		}
		return true
	})
	root := filepath.Join("..", "..")
	for _, dir := range []string{"cmd", "internal"} {
		if err := filepath.WalkDir(filepath.Join(root, dir), func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") || filepath.Base(file) == "manual_first_broadcast_consume.go" {
				return nil
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if strings.Contains(string(raw), "ConsumeManualFirstBroadcastPermit") {
				t.Error("private metadata consumer gained production caller", file)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

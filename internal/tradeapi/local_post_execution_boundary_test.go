package tradeapi

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalPostExecutionNoProductionRegistrationAndNoArm(t *testing.T) {
	// Lexical guard, not an arbitrary whole-program call graph proof. Product
	// files cannot refer to this test-only composition/transport or add a method
	// with its private name. Existing executor dependency tests remain unchanged.
	symbols := map[string]bool{"localPostHarness": true, "localPostTransport": true, "executeOriginal": true}
	root := filepath.Join("..", "..")
	for _, directory := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && symbols[id.Name] {
					t.Errorf("test-only execution became product reachable: %s %s", path, id.Name)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	f := newLocalPostFixture(t, "buy")
	before := admittedDatabaseSnapshot(t, f.store.db)
	if _, err := f.store.db.Exec(`UPDATE canary_runtime_authorizations SET state='ARMED' WHERE id=?`, f.h.scope.AuthorizationID); err == nil || !strings.Contains(err.Error(), "manual atomic admission not connected") {
		t.Fatal("synthetic grant became operator authorization", err)
	}
	if before != admittedDatabaseSnapshot(t, f.store.db) {
		t.Fatal("no-arm rejection changed authorization")
	}
}

func TestLocalPostExecutionClaimAuditAlertAndCommitRollback(t *testing.T) {
	for _, fault := range []string{"audit", "alert", "commit"} {
		t.Run(fault, func(t *testing.T) {
			f := newLocalPostFixture(t, "buy")
			var before string
			f.h.checkpoint = func(at string) error {
				if at != "after_permit" {
					return nil
				}
				switch fault {
				case "audit":
					_, err := f.store.db.Exec(`CREATE TRIGGER local_claim_fault BEFORE INSERT ON canary_runtime_audit WHEN NEW.event_type='MANUAL_FIRST_SUBMISSION_CLAIMED' BEGIN SELECT RAISE(ABORT,'controlled audit failure'); END`)
					if err != nil {
						return err
					}
				case "alert":
					_, err := f.store.db.Exec(`CREATE TRIGGER local_claim_fault BEFORE INSERT ON canary_alert_outbox WHEN NEW.id LIKE 'manual-first-submission-claim-alert:%' BEGIN SELECT RAISE(ABORT,'controlled alert failure'); END`)
					if err != nil {
						return err
					}
				case "commit":
					f.h.executor.store.SetCanaryHookForTest(func(stage string) error {
						if stage == "manual_submission_claim_before_commit" {
							return errors.New("controlled commit failure")
						}
						return nil
					})
				}
				before = foundationEconomicSnapshot(t, f.store)
				return nil
			}
			w := call(f.h, "POST", "/v1/trades", "claim-fault", encode(f.request))
			if w.Code != 503 || before == "" || foundationEconomicSnapshot(t, f.store) != before || f.signer.calls.Load() != 1 || f.transport.calls.Load() != 0 {
				t.Fatal("claim fault retained partial economics", fault, w.Code, w.Body.String())
			}
			for _, table := range []string{"transaction_submissions", "manual_first_submission_bindings", "manual_first_submission_scopes", "manual_first_broadcast_consume_scopes"} {
				if count(t, f.store.db, table) != 0 {
					t.Fatal("partial claim retained", table)
				}
			}
			var permit string
			if err := f.store.db.QueryRow(`SELECT state FROM canary_send_permits`).Scan(&permit); err != nil || permit != "ISSUED" {
				t.Fatal("failed claim consumed permit", permit, err)
			}
			beforeAll := admittedDatabaseSnapshot(t, f.store.db)
			got := localPostRecord(t, call(f.h, "POST", "/v1/trades", "claim-fault", encode(f.request)), 202)
			if !got.Duplicate || admittedDatabaseSnapshot(t, f.store.db) != beforeAll || f.transport.calls.Load() != 0 || f.signer.calls.Load() != 1 {
				t.Fatal("duplicate retried failed claim")
			}
		})
	}
}

func TestLocalPostExecutionCanceledBeforeIntakeHasNoNewWork(t *testing.T) {
	f := newLocalPostFixture(t, "buy")
	before := admittedDatabaseSnapshot(t, f.store.db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/v1/trades", strings.NewReader(encode(f.request))).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+testCredential)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "already-canceled")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 503 || before != admittedDatabaseSnapshot(t, f.store.db) || f.signer.calls.Load() != 0 || f.transport.calls.Load() != 0 {
		t.Fatal("canceled request created untracked work", w.Code)
	}
}

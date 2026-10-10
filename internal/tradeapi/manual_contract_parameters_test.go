package tradeapi

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

// This prerequisite does not add an admitted lifecycle/duplicate API. Synthetic
// statuses below are disposable test SQL showing why status alone is never a
// permission. Schema-complete command transitions are still future B3 work.
func TestHTTPC3B3ParametersHistoricalVerificationDoesNotWeakenPublicReader(t *testing.T) {
	for _, state := range []string{"manual_validation_succeeded", "manual_admission_succeeded", "submitted", "broadcast_unknown", "manual_resolution"} {
		t.Run(state, func(t *testing.T) {
			_, s, h, b, _ := validationAPI(t)
			r := manualRecord(t, call(h, "POST", "/v1/trades", "immutable-history", encode(buy())))
			prior, err := contractReader(t, s).Load(context.Background(), h.identity, r.RequestID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE operations SET status=? WHERE id=?`, state, *r.OperationID); err != nil {
				t.Fatal(err)
			}
			step := "manual_admitted"
			if state == "manual_validation_succeeded" {
				step = "validation_succeeded"
			}
			if _, err = s.db.Exec(`UPDATE execution_steps SET status=? WHERE id=?`, step, prior.StepID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`INSERT INTO execution_wallet_lanes(wallet_id,address,state,updated_at) VALUES(?,?,'frozen',?)`, h.identity.WalletID, h.identity.WalletAddress, testNow.Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
				var before, after int64
				if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
					t.Fatal(err)
				}
				got, err := verifyManualContractParametersTx(ctx, conn, h.identity, r.RequestID)
				if err != nil || encode(got) != encode(prior) || got.AdmissionAuthorized || got.SendAuthorized || got.Status != "PARAMETER_CONTRACT_NOT_ADMITTED" {
					t.Fatal("immutable history changed or acquired permission", err)
				}
				if err = conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil || after != before {
					t.Fatal("parameter verifier mutated durable state", err, before, after)
				}
				// Same caller transaction remains open. No permission/dedupe
				// decision is implied by being able to verify immutable history.
				if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
					t.Fatal(err)
				}
			})
			a := contractReader(t, s)
			if got, err := a.Load(context.Background(), h.identity, r.RequestID); err == nil || got.BindingHash != "" {
				t.Fatal("public C3-A weakened lifecycle/busy-wallet gate")
			}
			a.now = func() time.Time { expiry, _ := time.Parse(time.RFC3339Nano, r.ExpiresAt); return expiry }
			if got, err := a.Load(context.Background(), h.identity, r.RequestID); err == nil || got.BindingHash != "" {
				t.Fatal("expired public contract accepted")
			}
			if b.resolves.Load() != 1 || b.completes.Load() != 1 || count(t, s.db, "canary_runtime_audit") != 3 || count(t, s.db, "canary_alert_outbox") != 3 {
				t.Fatal("immutable verification invoked backend or audit mutation")
			}
			// The fixture intentionally inserted ONE frozen lane. Verify it
			// remained frozen, and all actual economic/output tables stayed empty.
			var lane string
			if err = s.db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id=?`, h.identity.WalletID).Scan(&lane); err != nil || lane != "frozen" || count(t, s.db, "execution_wallet_lanes") != 1 {
				t.Fatal("historical verification changed fixture lane", lane, err)
			}
			for _, table := range []string{"canary_risk_reservations", "canary_authorization_operation_usage", "canary_authorization_usage", "canary_admission_decisions", "canary_admission_attempts", "execution_reservations", "transaction_attempts", "transaction_submissions", "canary_send_permits", "canary_runtime_gate_snapshots"} {
				if count(t, s.db, table) != 0 {
					t.Fatal("historical verifier created economic work", table)
				}
			}
		})
	}
}

func TestHTTPC3B3ParametersRejectCorruptHistoryAndScopedIdentity(t *testing.T) {
	for _, statements := range [][]string{
		{`UPDATE execution_steps SET parameters_json='{}'`},
		{`DROP TRIGGER manual_validation_no_update`, `UPDATE manual_trade_validations SET result_hash='` + strings.Repeat("0", 64) + `'`},
		{`DROP TRIGGER manual_resolution_no_update`, `UPDATE manual_trade_resolutions SET resolution_hash='` + strings.Repeat("0", 64) + `'`},
		{`DROP TRIGGER manual_http_source_no_update`, `UPDATE manual_http_order_sources SET api_policy_hash='` + strings.Repeat("0", 64) + `'`},
		{`DROP TRIGGER manual_http_source_no_update`, `UPDATE manual_http_order_sources SET expires_at='2026-10-07T12:00:30+00:00'`},
		{`DROP TRIGGER manual_resolved_step_snapshot`, `UPDATE execution_steps SET quote_block_hash='` + strings.Repeat("0", 66) + `'`},
	} {
		_, s, h, _, _ := validationAPI(t)
		r := manualRecord(t, call(h, "POST", "/v1/trades", "corrupt-immutable-history", encode(buy())))
		for _, statement := range statements {
			if _, err := s.db.Exec(statement); err != nil {
				t.Fatal(statement, err)
			}
		}
		withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
			got, err := verifyManualContractParametersTx(ctx, conn, h.identity, r.RequestID)
			if err == nil || got.BindingHash != "" {
				t.Fatal("corrupt immutable binding returned contract")
			}
		})
		noIntakeEconomicWork(t, s)
	}
	_, s, h, _, _ := validationAPI(t)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "scoped-immutable-history", encode(buy())))
	for _, id := range []Identity{
		{PrincipalID: "other", WalletID: h.identity.WalletID, WalletAddress: testWallet},
		{PrincipalID: h.identity.PrincipalID, WalletID: "other", WalletAddress: testWallet},
		{PrincipalID: h.identity.PrincipalID, WalletID: h.identity.WalletID, WalletAddress: testToken},
	} {
		withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
			if got, err := verifyManualContractParametersTx(ctx, conn, id, r.RequestID); err == nil || got.BindingHash != "" {
				t.Fatal("immutable verifier accepted another principal/wallet")
			}
		})
	}
	// Hex address case must retain the public reader's existing identity semantics.
	for _, address := range []string{"0x" + strings.ToUpper(h.identity.WalletAddress[2:]), h.identity.WalletAddress[2:]} {
		id := h.identity
		id.WalletAddress = address
		withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
			if _, err := verifyManualContractParametersTx(ctx, conn, id, r.RequestID); err != nil {
				t.Fatal("equivalent wallet address case/prefix rejected", err)
			}
		})
		if _, err := contractReader(t, s).Load(context.Background(), id, r.RequestID); err != nil {
			t.Fatal("public identity semantics changed", err)
		}
	}
}

func TestHTTPC3B3ParametersManualLifecycleExcludedFromLegacyRecovery(t *testing.T) {
	d, s, h, _, _ := validationAPI(t)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "legacy-exclusion", encode(buy())))
	legacy, err := trade.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"manual_validation_succeeded", "manual_admission_succeeded"} {
		if _, err := s.db.Exec(`UPDATE operations SET status=? WHERE id=?`, state, *r.OperationID); err != nil {
			t.Fatal(err)
		}
		items, err := legacy.Recoverable(context.Background())
		if err != nil || len(items) != 0 {
			t.Fatal("MANUAL lifecycle exposed to copy recovery engine", state, len(items), err)
		}
	}
	noIntakeEconomicWork(t, s)
}

func TestHTTPC3B3ParametersVerifierHasNoMutatingOrEconomicDependencies(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "manual_execution_contract.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"context": true, "database/sql": true, "encoding/json": true, "math/big": true, "time": true, "github.com/ethereum/go-ethereum/common": true, "github.com/ruzickahunzeker/rbh-bot/internal/chainquery": true}
	for _, im := range f.Imports {
		if !allowed[strings.Trim(im.Path.Value, `"`)] {
			t.Fatal("unexpected contract dependency", im.Path.Value)
		}
	}
	var found bool
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "verifyManualContractParametersTx" {
			continue
		}
		found = true
		ast.Inspect(fn, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				for _, forbidden := range []string{"ExecContext", "BeginTx", "Commit", "Rollback", "Sign", "Submit", "ReplayUnknown", "SendRawTransaction", "ReserveNonce", "Resolve", "Complete"} {
					if sel.Sel.Name == forbidden {
						t.Error("immutable verifier acquired side effect", forbidden)
					}
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("missing immutable verifier")
	}
}

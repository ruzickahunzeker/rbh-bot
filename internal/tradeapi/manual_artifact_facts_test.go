package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
)

func TestHTTPM3B3AExactOriginalClaimProjectionNoWrites(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, side)
			if _, err := nonceWriter(s, &localNonceObserver{nonce: "9223372036854775808"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			q, err := readManualNonceClaim(context.Background(), s.db, r.RequestID)
			if err != nil {
				t.Fatal(err)
			}
			b, err := scanManualExecutionBinding(s.db.QueryRow(`SELECT ` + manualExecutionBindingColumns + ` FROM manual_execution_bindings`))
			if err != nil {
				t.Fatal(err)
			}
			cmd, err := scanManualCommand(s.db.QueryRow(`SELECT ` + manualCommandColumns + ` FROM manual_admission_commands`))
			if err != nil {
				t.Fatal(err)
			}
			c, err := verifyManualCommandTx(context.Background(), s.db, cmd)
			if err != nil {
				t.Fatal(err)
			}
			out := r.Validation.Output
			want := manualartifact.Facts{Version: 1, OperationID: *r.OperationID, RequestID: r.RequestID, ExecutionStepID: b.ExecutionStep, AttemptID: q.Attempt, WalletID: h.identity.WalletID, ReservationID: cmd.Reservation,
				ContractHash: c.BindingHash, ChainID: 4663, From: out.From, To: out.To, Value: out.Value, Data: out.Data, Nonce: "9223372036854775808", GasLimit: out.GasLimit, GasFeeCap: out.GasFeeCap, GasTipCap: "0",
				PolicyVersion: r.Validation.PolicyVersion, PolicyHash: r.Validation.PolicyHash, QuoteBlockNumber: out.Resolution.Token.Block.Number, QuoteBlockHash: out.Resolution.Token.Block.Hash, RuntimeCodeHash: out.Resolution.Token.CurveRuntimeCodeHash,
				ExpiresAt: r.ExpiresAt, ClaimedAt: q.CheckedAt, ClaimObservationHash: q.ObservationHash, DeadlineCapability: "APPLICATION_TTL_ONLY", ContractDeadline: false,
				AuthorizationID: scope.AuthorizationID, AuthorizationEpoch: scope.AuthorizationEpoch, ManualPolicyID: scope.ManualPolicyID, ManualPolicyHash: b.PolicyHash, GateEvidenceSetHash: b.GateHash,
				Environment: scope.Environment, BuildSHA: scope.BuildSHA, ReleaseID: scope.ReleaseID, DeploymentID: scope.DeploymentID}
			before := admittedDatabaseSnapshot(t, s.db)
			withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
				var first, last int64
				if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&first); err != nil {
					t.Fatal(err)
				}
				got, err := loadManualArtifactFacts(ctx, conn, h.identity, r.RequestID, scope)
				if err != nil || got != want {
					t.Fatal("same transaction original facts", err)
				}
				if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&last); err != nil || first != last {
					t.Fatal("projection wrote or ended transaction", err)
				}
			})
			for i := 0; i < 3; i++ {
				got, err := loadManualArtifactFacts(context.Background(), s.db, h.identity, r.RequestID, scope)
				if err != nil || got != want {
					t.Fatal("original claim projection drift", err, got)
				}
				tx, err := got.Transaction()
				if err != nil || tx.Nonce() != 9223372036854775808 || tx.GasTipCap().Sign() != 0 || tx.GasFeeCap().String() != out.GasFeeCap {
					t.Fatal("unsigned transaction projection", err)
				}
				if admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("projection wrote, consumed or refreshed")
				}
			}
		})
	}
}

func TestHTTPM3B3AProjectionHistoryAfterExpiryRevokeReopen(t *testing.T) {
	d, s, h, r, scope, path := boundSigningFixture(t, "buy")
	o := &localNonceObserver{nonce: "23"}
	if _, err := nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	original, err := loadManualArtifactFacts(context.Background(), s.db, h.identity, r.RequestID, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'; UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_authorization_operation_usage SET state='FROZEN'`); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	_, s = bindingReopen(t, d, path)
	got, err := loadManualArtifactFacts(context.Background(), s.db, h.identity, r.RequestID, scope)
	if err != nil || got != original || got.ExpiresAt != r.ExpiresAt || admittedDatabaseSnapshot(t, s.db) != before || o.calls.Load() != 1 {
		t.Fatal("history treated as new action", err)
	}
	expiry, _ := time.Parse(time.RFC3339Nano, got.ExpiresAt)
	if !expiry.Before(testNow.Add(time.Hour)) {
		t.Fatal("fixture must be expired at hypothetical read time")
	}
	// There is deliberately no current-time argument, permission flag or signer
	// on this historical primitive. Fresh signing gates are NOT implemented.
}

func TestHTTPM3B3AProjectionInvalidIdentityAndCancellationZeroOutput(t *testing.T) {
	_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
	before := admittedDatabaseSnapshot(t, s.db)
	if v, err := loadManualArtifactFacts(context.Background(), s.db, h.identity, r.RequestID, scope); err == nil || v != (manualartifact.Facts{}) || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("unclaimed input accepted")
	}
	if _, err := nonceWriter(s, &localNonceObserver{}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	before = admittedDatabaseSnapshot(t, s.db)
	for _, kind := range []string{"principal", "wallet", "scope", "request", "cancel", "nil_context", "empty_scope"} {
		t.Run(kind, func(t *testing.T) {
			id, expected, request, ctx := h.identity, scope, r.RequestID, context.Background()
			switch kind {
			case "principal":
				id.PrincipalID = "other"
			case "wallet":
				id.WalletAddress = "0x3000000000000000000000000000000000000003"
			case "scope":
				expected.AuthorizationEpoch++
			case "request":
				request = "other"
			case "nil_context":
				ctx = nil
			case "empty_scope":
				expected = manualAdmissionScope{}
			case "cancel":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			v, err := loadManualArtifactFacts(ctx, s.db, id, request, expected)
			if err == nil || !reflect.DeepEqual(v, manualartifact.Facts{}) || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("invalid/canceled read exposed facts", v, err)
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation cause lost", err)
			}
		})
	}
}

func TestHTTPM3B3ADisconnectedSourceNoSigningAndNoSchema(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "manual_artifact_facts.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		for _, bad := range []string{"/trade\"", "manualsign", "ethclient", "/rpc\"", "crypto/ecdsa"} {
			if strings.Contains(imp.Path.Value, bad) {
				t.Fatal("economic dependency", imp.Path.Value)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			switch s.Sel.Name {
			case "Exec", "ExecContext", "SignTransaction", "Encrypt", "Decrypt", "Submit", "ReplayUnknown", "SendRawTransaction", "PendingNonce", "ReserveExecution":
				t.Error("prohibited projection call", s.Sel.Name)
			}
		}
		return true
	})
	_, s, _, _ := fixture(t)
	var latest int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&latest); err != nil || latest != 36 {
		t.Fatal("unexpected schema checkpoint", latest, err)
	}
	for _, name := range []string{"manual_b1_no_arm", "manual_b1_no_permit", "manual_b1_no_pass_snapshot", "manual_execution_no_attempt_update", "manual_nonce_no_submission_insert"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Fatal("closed guard changed", name, err)
		}
	}
}

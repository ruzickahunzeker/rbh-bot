package tradeapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

func recoveryNewAPI(t *testing.T, f manualSubmissionFixture, validation bool) (*API, *validationBackend) {
	t.Helper()
	backend := &validationBackend{wallet: f.id.WalletAddress}
	options := Options{ManualIntake: true}
	if validation {
		options.ValidationBackend, options.ValidationPolicyVersion = backend, 1
	}
	h, err := NewWithOptions(f.e.verifier.store, f.id, testCredential, DefaultPolicy(), nil, options)
	if err != nil {
		t.Fatal(err)
	}
	h.now = func() time.Time { return testNow.Add(5 * time.Minute) }
	if h.validator != nil {
		h.validator.now = h.now
	}
	return h, backend
}

func TestManualRecoveryNoWalletReuseBeforeMultiGenerationRiskModel(t *testing.T) {
	f, recovery, _, lease := manualRecoveryFixture(t, "submitted", "buy")
	if err := recovery.ReconcileWithLease(context.Background(), f.permit.OperationID, func() trade.RecoveryLeaseFence { return lease }); err != nil {
		t.Fatal(err)
	}
	s := f.e.verifier.store
	h, backend := recoveryNewAPI(t, f, true)
	request := buy()
	request.Fee.TipGwei = "0"
	w := call(h, "POST", "/v1/trades", "reuse-validation-rejected", encode(request))
	if w.Code != 422 || backend.resolves.Load() != 0 || backend.completes.Load() != 0 {
		t.Fatal("settled wallet reused by validation/RPC", w.Code, w.Body.String(), backend.resolves.Load(), backend.completes.Load())
	}
	if err := checkManualWalletReusable(context.Background(), s.db, f.id); inputFailureCode(err) != "MANUAL_WALLET_REUSE_UNSUPPORTED" {
		t.Fatal("unstable retained-wallet rejection reason", err)
	}
	alias := f.id
	alias.WalletID, alias.WalletAddress = "another-wallet-id", strings.TrimPrefix(strings.ToUpper(f.id.WalletAddress), "0X")
	if err := checkManualWalletReusable(context.Background(), s.db, alias); inputFailureCode(err) != "MANUAL_WALLET_REUSE_UNSUPPORTED" {
		t.Fatal("normalized address alias bypassed retained claim", err)
	}
	other := f.id
	other.WalletID, other.WalletAddress = "unrelated-wallet", "0x9000000000000000000000000000000000000009"
	if err := checkManualWalletReusable(context.Background(), s.db, other); err != nil {
		t.Fatal("unrelated wallet incorrectly blocked", err)
	}

	// Emulate a valid resolution/validation that completed BEFORE the retained
	// nonce boundary. 017 still accepts its immutable facts; 045 must not rely
	// only on the Go preflight to protect an already validated request.
	intake, _ := recoveryNewAPI(t, f, false)
	second := manualRecord(t, call(intake, "POST", "/v1/trades", "prevalidated-reuse", encode(request)))
	stamp := intake.now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`INSERT INTO manual_trade_resolutions(operation_id,resolution_json,resolution_hash,policy_version,policy_hash,policy_json,expires_at,created_at)
 SELECT ?,resolution_json,resolution_hash,policy_version,policy_hash,policy_json,?,? FROM manual_trade_resolutions WHERE operation_id=?`, *second.OperationID, second.ExpiresAt, stamp, f.permit.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO manual_trade_validations(operation_id,result_json,result_hash,created_at,send_authorized)
 SELECT ?,result_json,result_hash,?,0 FROM manual_trade_validations WHERE operation_id=?`, *second.OperationID, stamp, f.permit.OperationID); err != nil {
		t.Fatal(err)
	}
	writer := atomicWriter(s)
	writer.now = intake.now
	scope := f.e.verifier.profile.process.scope
	beforeRisk, beforeUsage := count(t, s.db, "canary_risk_reservations"), count(t, s.db, "canary_authorization_operation_usage")
	if _, err := writer.admit(context.Background(), f.id, second.RequestID, scope); inputFailureCode(err) != "MANUAL_WALLET_REUSE_UNSUPPORTED" {
		t.Fatal("prevalidated order bypassed admission writer reuse boundary", err)
	}
	c, err := verifyManualContractParametersTx(context.Background(), s.db, f.id, second.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	original, err := scanManualCommand(s.db.QueryRow(`SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE operation_id=?`, f.permit.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	cmd := manualAdmissionCommand{Operation: c.OperationID, Request: c.RequestID, Step: c.StepID, Reservation: "manual-risk:" + c.OperationID,
		Scope: scope, PolicyHash: original.PolicyHash, GateHash: original.GateHash, ContractRaw: encode(c), ContractHash: c.BindingHash, CheckedAt: stamp}
	if _, err := s.db.Exec(`INSERT INTO manual_admission_commands(`+manualCommandColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, cmd.arguments()...); err == nil || !strings.Contains(err.Error(), "MANUAL_WALLET_REUSE_UNSUPPORTED") {
		t.Fatal("direct SQL bypassed same-wallet schema guard", err)
	}
	if count(t, s.db, "canary_risk_reservations") != beforeRisk || count(t, s.db, "canary_authorization_operation_usage") != beforeUsage {
		t.Fatal("rejected reuse consumed risk/authorization budget")
	}
	// Negative control: remove ONLY the newly introduced guard in a rolled-back
	// test transaction. The old admission path really accepts this order and
	// reserves the conflicting second risk row. No C07 unique index is removed.
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(context.Background(), `ROLLBACK`)
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `DROP TRIGGER manual_recovery_no_wallet_reuse`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `INSERT INTO manual_admission_commands(`+manualCommandColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, cmd.arguments()...); err != nil {
		t.Fatal("negative control did not reproduce real prior admission", err)
	}
	if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if count(t, s.db, "canary_risk_reservations") != beforeRisk || count(t, s.db, "canary_authorization_operation_usage") != beforeUsage {
		t.Fatal("negative control leaked financial changes")
	}
	// Historical duplicate admission is still history, not reuse permission.
	if result, err := writer.admit(context.Background(), f.id, f.request, scope); err != nil || !result.Duplicate || result.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" {
		t.Fatal("reuse restriction broke the original duplicate", result, err)
	}
	old, err := f.e.store.ListCanonicalReceiptObservations(context.Background(), f.permit.AttemptID)
	if err != nil || len(old) != 1 {
		t.Fatal(old, err)
	}
	if err := f.e.store.OrphanFenced(context.Background(), mustManualArtifact(t, f), old[0], time.Now(), &lease); err != nil {
		t.Fatal("rejected second admission still blocked the original reorg rollback", err)
	}
	assertManualRecoveryStates(t, f, "orphaned", "rolled_back", "frozen", "FROZEN", "frozen")
	if f.fake.calls != 1 {
		t.Fatal("wallet reuse closure invoked a second transport action")
	}
}

package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

// These local primitives have receipt/header/effect methods only. The fake
// first submission is fixture setup; all recovery assertions use its counters
// as a baseline and never construct a transport, signer or nonce allocator.
type manualRecoveryBackend struct {
	receipt *types.Receipt
	header  *types.Header
	fail    error
}

func (b *manualRecoveryBackend) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return b.receipt, b.fail
}
func (b *manualRecoveryBackend) HeaderByNumber(_ context.Context, n *big.Int) (*types.Header, error) {
	if b.fail != nil {
		return nil, b.fail
	}
	if n == nil {
		return &types.Header{Number: new(big.Int).Add(b.header.Number, big.NewInt(2))}, nil
	}
	return b.header, nil
}

type manualRecoveryEffect struct{ asset, delta string }

func (e manualRecoveryEffect) ResolvePositionEffect(context.Context, trade.SignedArtifact, *types.Receipt) (trade.PositionEffect, error) {
	return trade.PositionEffect{Asset: e.asset, Delta: e.delta}, nil
}

type manualRecoveryQuerier struct {
	evidence trade.ControlledRecoveryEvidence
	calls    int
}

func (q *manualRecoveryQuerier) QueryRecovery(context.Context, trade.ControlledRecoveryQuery) (trade.ControlledRecoveryEvidence, error) {
	q.calls++
	return q.evidence, nil
}

func manualRecoveryFixture(t *testing.T, state, side string) (manualSubmissionFixture, *trade.RecoveryService, *manualRecoveryBackend, trade.RecoveryLeaseFence) {
	t.Helper()
	f := newManualSubmissionFixture(t, side)
	if state == "broadcast_unknown" {
		f.fake.result, f.fake.err = manualTransportResult{}, errors.New("ambiguous fixture transport")
	}
	sub, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID)
	if sub.State != state {
		t.Fatalf("fixture outcome %s: %v", sub.State, err)
	}
	var found int
	if err := f.e.verifier.store.db.QueryRow(`SELECT COUNT(*) FROM manual_recovery_source WHERE operation_id=?`, f.permit.OperationID).Scan(&found); err != nil || found != 1 {
		t.Fatalf("retained recovery source count=%d: %v", found, err)
	}
	now := time.Now().UTC()
	lease := trade.RecoveryLeaseFence{Environment: f.e.verifier.profile.process.scope.Environment, HolderID: "manual-recovery-owner", Epoch: 1}
	if err := f.e.store.AcquireCanaryWorkerLease(context.Background(), "RECOVERY", lease.Environment, lease.HolderID, lease.Epoch, now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	header := &types.Header{Number: big.NewInt(45), Extra: []byte("canonical-manual")}
	b := &manualRecoveryBackend{header: header, receipt: &types.Receipt{TxHash: common.HexToHash(f.permit.TxHash), BlockHash: header.Hash(), BlockNumber: header.Number, Status: 1}}
	var asset, delta string
	if err := f.e.verifier.store.db.QueryRow(`SELECT json_extract(contract_json,'$.output.resolution.token.address'),
 CASE WHEN json_extract(contract_json,'$.output.resolution.side')='sell' THEN '-'||json_extract(contract_json,'$.input.units')
 ELSE json_extract(contract_json,'$.minimum_output.units') END FROM manual_admission_commands WHERE operation_id=?`, f.permit.OperationID).Scan(&asset, &delta); err != nil {
		t.Fatal(err)
	}
	r, err := trade.NewRecoveryServiceWithCipher(f.e.store, manualArtifactReadCipher{f.e.verifier.decryptor}, b, manualRecoveryEffect{asset, delta}, trade.CanonicalPolicy{Confirmations: 1})
	if err != nil {
		t.Fatal(err)
	}
	return f, r, b, lease
}

func TestManualRecoverySubmittedCanonicalReorgReapply(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			f, recovery, backend, lease := manualRecoveryFixture(t, "submitted", side)
			db := f.e.verifier.store.db
			var originalNonce, originalHash, originalBudget string
			if err := db.QueryRow(`SELECT q.nonce,artifact_hash,usage.total_input FROM manual_nonce_claims q
 JOIN manual_first_submission_bindings first ON first.operation_id=q.operation_id
 JOIN canary_authorization_usage usage ON usage.authorization_id=first.authorization_id`).Scan(&originalNonce, &originalHash, &originalBudget); err != nil {
				t.Fatal(err)
			}
			// Revocation, emergency stop, disabled mode and expired application TTL
			// do not revoke authority to reconcile an already propagated artifact.
			if _, err := db.Exec(`UPDATE canary_control_state SET mode='DISABLED',emergency_stopped=1;
 UPDATE canary_runtime_authorizations SET state='REVOKED' WHERE state='ARMED'`); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := recovery.ReconcileWithLease(context.Background(), f.permit.OperationID, func() trade.RecoveryLeaseFence { return lease }); err != nil {
					t.Fatal("canonical recovery", err)
				}
			}
			assertManualRecoveryStates(t, f, "canonical_success", "active", "committed", "CONSUMED", "idle")
			originalHeader := backend.header
			backend.header = &types.Header{Number: originalHeader.Number, Extra: []byte("orphaned-manual")}
			observations, err := f.e.store.ListCanonicalReceiptObservations(context.Background(), f.permit.AttemptID)
			if err != nil || len(observations) != 1 {
				t.Fatal(observations, err)
			}
			for i := 0; i < 2; i++ {
				if err := recovery.CheckReorgWithLease(context.Background(), f.permit.OperationID, observations[0], func() trade.RecoveryLeaseFence { return lease }); err != nil {
					t.Fatal("reorg recovery", err)
				}
			}
			assertManualRecoveryStates(t, f, "orphaned", "rolled_back", "frozen", "FROZEN", "frozen")
			backend.header = originalHeader
			for i := 0; i < 2; i++ {
				if err := recovery.ReconcileWithLease(context.Background(), f.permit.OperationID, func() trade.RecoveryLeaseFence { return lease }); err != nil {
					t.Fatal("reapply recovery", err)
				}
			}
			assertManualRecoveryStates(t, f, "canonical_success", "active", "committed", "CONSUMED", "idle")
			var commands, audits, alerts, events, histories, legacy int
			if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM manual_recovery_commands),
 (SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type LIKE 'MANUAL_RECOVERY_%'),
 (SELECT COUNT(*) FROM canary_alert_outbox WHERE id LIKE 'manual-recovery-alert:%'),
 (SELECT COUNT(*) FROM manual_lifecycle_events WHERE event_type LIKE 'MANUAL_RECOVERY_%'),
 (SELECT COUNT(*) FROM position_effect_history),(SELECT COUNT(*) FROM execution_reservations)`).Scan(&commands, &audits, &alerts, &events, &histories, &legacy); err != nil || commands != 4 || audits != 4 || alerts != 4 || events != 4 || histories != 3 || legacy != 0 {
				t.Fatal("transition conservation", commands, audits, alerts, events, histories, legacy, err)
			}
			var nonce, hash, budget string
			var uses int
			if err := db.QueryRow(`SELECT q.nonce,first.artifact_hash,usage.total_input,usage.operation_count FROM manual_nonce_claims q
 JOIN manual_first_submission_bindings first ON first.operation_id=q.operation_id
 JOIN canary_authorization_usage usage ON usage.authorization_id=first.authorization_id`).Scan(&nonce, &hash, &budget, &uses); err != nil || nonce != originalNonce || hash != originalHash || budget != originalBudget || uses != 1 || f.fake.calls != 1 {
				t.Fatal("recovery changed artifact/nonce/allowance/transport baseline", nonce, hash, budget, uses, f.fake.calls, err)
			}
		})
	}
}

func assertManualRecoveryStates(t *testing.T, f manualSubmissionFixture, receipt, effect, risk, usage, lane string) {
	t.Helper()
	var gotReceipt, gotEffect, gotRisk, gotUsage, gotLane string
	err := f.e.verifier.store.db.QueryRow(`SELECT current.canonical_state,current.effect_state,r.state,u.state,l.state
 FROM manual_recovery_current current JOIN manual_nonce_claims q ON q.attempt_id=current.attempt_id
 JOIN canary_risk_reservations r ON r.id=q.reservation_id JOIN canary_authorization_operation_usage u ON u.operation_id=q.operation_id
 JOIN execution_wallet_lanes l ON l.wallet_id=q.wallet_id WHERE q.operation_id=?`, f.permit.OperationID).Scan(&gotReceipt, &gotEffect, &gotRisk, &gotUsage, &gotLane)
	if err != nil || gotReceipt != receipt || gotEffect != effect || gotRisk != risk || gotUsage != usage || gotLane != lane {
		t.Fatalf("states got %q/%q/%q/%q/%q want %q/%q/%q/%q/%q: %v", gotReceipt, gotEffect, gotRisk, gotUsage, gotLane, receipt, effect, risk, usage, lane, err)
	}
}

func TestManualRecoveryUnknownStoppedQueryOnlyThenCanonical(t *testing.T) {
	f, recovery, backend, lease := manualRecoveryFixture(t, "broadcast_unknown", "buy")
	db := f.e.verifier.store.db
	if _, err := db.Exec(`UPDATE canary_control_state SET mode='DISABLED',emergency_stopped=1;
 UPDATE canary_runtime_authorizations SET state='REVOKED' WHERE state='ARMED'`); err != nil {
		t.Fatal(err)
	}
	q := &manualRecoveryQuerier{evidence: trade.ControlledRecoveryEvidence{NonceState: "RESERVED_UNRESOLVED", EvidenceHash: strings.Repeat("a", 64), ObservedAt: time.Now()}}
	w, err := trade.NewControlledRecoveryWorker(f.e.store, recovery, q, lease.Environment, lease.HolderID, lease.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := w.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var lane, risk, usage string
	if err := db.QueryRow(`SELECT l.state,r.state,u.state FROM execution_wallet_lanes l
 JOIN canary_risk_reservations r ON r.operation_id=l.operation_id
 JOIN canary_authorization_operation_usage u ON u.operation_id=l.operation_id`).Scan(&lane, &risk, &usage); err != nil || lane != "frozen" || risk != "frozen" || usage != "FROZEN" || q.calls != 2 || f.fake.calls != 1 || count(t, db, "receipt_observations") != 0 {
		t.Fatal("ambiguous recovery released or performed an action", lane, risk, usage, q.calls, f.fake.calls, err)
	}
	q.evidence.TxFound, q.evidence.ReceiptFound, q.evidence.EvidenceHash = true, true, strings.Repeat("b", 64)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertManualRecoveryStates(t, f, "canonical_success", "active", "committed", "CONSUMED", "idle")
	if backend.receipt == nil || f.fake.calls != 1 {
		t.Fatal("unknown recovery acquired a send capability")
	}
}

func TestManualRecoveryReceiptPendingMismatchAndRevert(t *testing.T) {
	f, recovery, backend, lease := manualRecoveryFixture(t, "submitted", "buy")
	receipt := backend.receipt
	backend.receipt = nil
	if err := recovery.ReconcileWithLease(context.Background(), f.permit.OperationID, func() trade.RecoveryLeaseFence { return lease }); !errors.Is(err, trade.ErrReceiptPending) {
		t.Fatal("pending", err)
	}
	if count(t, f.e.verifier.store.db, "manual_recovery_commands") != 0 {
		t.Fatal("pending receipt produced a mutation")
	}
	backend.receipt = receipt
	backend.receipt.Status = 0
	original := backend.header
	backend.header = &types.Header{Number: original.Number, Extra: []byte("not-canonical")}
	if err := recovery.ReconcileWithLease(context.Background(), f.permit.OperationID, func() trade.RecoveryLeaseFence { return lease }); !errors.Is(err, trade.ErrNotCanonical) {
		t.Fatal("mismatching canonical block", err)
	}
	if count(t, f.e.verifier.store.db, "position_effects") != 0 {
		t.Fatal("uncanonical receipt created an effect")
	}
	backend.header = original
	for i := 0; i < 2; i++ {
		if err := recovery.ReconcileWithLease(context.Background(), f.permit.OperationID, func() trade.RecoveryLeaseFence { return lease }); err != nil {
			t.Fatal(err)
		}
	}
	assertManualRecoveryStates(t, f, "canonical_revert", "", "released", "CONSUMED", "idle")
	if count(t, f.e.verifier.store.db, "position_effects") != 0 || f.fake.calls != 1 {
		t.Fatal("revert created an economic effect")
	}
}

func TestManualRecoveryNewBlockUsesNewExactOutput(t *testing.T) {
	f, recovery, backend, lease := manualRecoveryFixture(t, "submitted", "buy")
	if err := recovery.ReconcileWithLease(context.Background(), f.permit.OperationID, func() trade.RecoveryLeaseFence { return lease }); err != nil {
		t.Fatal(err)
	}
	observations, err := f.e.store.ListCanonicalReceiptObservations(context.Background(), f.permit.AttemptID)
	if err != nil || len(observations) != 1 {
		t.Fatal(observations, err)
	}
	backend.header = &types.Header{Number: backend.header.Number, Extra: []byte("old-block-reorged")}
	if err := recovery.CheckReorgWithLease(context.Background(), f.permit.OperationID, observations[0], func() trade.RecoveryLeaseFence { return lease }); err != nil {
		t.Fatal(err)
	}
	var asset, oldDelta string
	if err := f.e.verifier.store.db.QueryRow(`SELECT asset,delta FROM position_effects`).Scan(&asset, &oldDelta); err != nil {
		t.Fatal(err)
	}
	delta, _ := new(big.Int).SetString(oldDelta, 10)
	delta.Add(delta, big.NewInt(19))
	backend.header = &types.Header{Number: big.NewInt(46), Extra: []byte("different-canonical-block")}
	backend.receipt.BlockNumber, backend.receipt.BlockHash = backend.header.Number, backend.header.Hash()
	newRecovery, err := trade.NewRecoveryServiceWithCipher(f.e.store, manualArtifactReadCipher{f.e.verifier.decryptor}, backend, manualRecoveryEffect{asset, delta.String()}, trade.CanonicalPolicy{Confirmations: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := newRecovery.ReconcileWithLease(context.Background(), f.permit.OperationID, func() trade.RecoveryLeaseFence { return lease }); err != nil {
			t.Fatal(err)
		}
	}
	var gotDelta, retainedDelta string
	if err := f.e.verifier.store.db.QueryRow(`SELECT p.delta,(SELECT effect_delta FROM manual_recovery_commands
 WHERE kind='CANONICAL' ORDER BY sequence LIMIT 1) FROM position_effects p`).Scan(&gotDelta, &retainedDelta); err != nil || gotDelta != delta.String() || retainedDelta != oldDelta {
		t.Fatal("new canonical output must replace rolled-back output, not historical evidence", gotDelta, retainedDelta, err)
	}
	assertManualRecoveryStates(t, f, "canonical_success", "active", "committed", "CONSUMED", "idle")
}

func manualRecoveryCanonicalSQL(ctx context.Context, tx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, operation string, lease trade.RecoveryLeaseFence, asset, delta string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO manual_recovery_commands(id,operation_id,kind,receipt_id,tx_hash,block_number,block_hash,receipt_status,
 prior_version,effect_asset,effect_delta,environment,holder_id,lease_epoch,checked_at)
 SELECT 'manual-recovery:'||r.id||':'||r.reconciliation_version,src.operation_id,'CANONICAL',r.id,r.tx_hash,r.block_number,r.block_hash,r.receipt_status,
 r.reconciliation_version,?,?,src.environment,?,?,? FROM manual_recovery_source src
 JOIN receipt_observations r ON r.attempt_id=src.attempt_id WHERE src.operation_id=?`, asset, delta, lease.HolderID, lease.Epoch, time.Now().UTC().Format(time.RFC3339Nano), operation)
	return err
}

func TestManualRecoveryDirectSQLFaultCannotCommitPartialEffects(t *testing.T) {
	for _, fault := range []string{"audit", "alert", "event"} {
		t.Run(fault, func(t *testing.T) {
			f, recovery, backend, lease := manualRecoveryFixture(t, "submitted", "buy")
			original := backend.header
			backend.header = &types.Header{Number: original.Number, Extra: []byte("observe-without-finalizing")}
			if err := recovery.ReconcileWithLease(context.Background(), f.permit.OperationID, func() trade.RecoveryLeaseFence { return lease }); !errors.Is(err, trade.ErrNotCanonical) {
				t.Fatal(err)
			}
			db := f.e.verifier.store.db
			var asset, delta string
			if err := db.QueryRow(`SELECT json_extract(contract_json,'$.output.resolution.token.address'),json_extract(contract_json,'$.minimum_output.units') FROM manual_admission_commands`).Scan(&asset, &delta); err != nil {
				t.Fatal(err)
			}
			table, when := "canary_runtime_audit", "NEW.event_type='MANUAL_RECOVERY_CANONICAL'"
			if fault == "alert" {
				table, when = "canary_alert_outbox", "NEW.id LIKE 'manual-recovery-alert:%'"
			} else if fault == "event" {
				table, when = "manual_lifecycle_events", "NEW.event_type='MANUAL_RECOVERY_CANONICAL'"
			}
			if _, err := db.Exec(`CREATE TRIGGER forced_manual_recovery_fault BEFORE INSERT ON ` + table + ` WHEN ` + when + ` BEGIN SELECT RAISE(FAIL,'forced witness fault'); END`); err != nil {
				t.Fatal(err)
			}
			conn, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := conn.ExecContext(context.Background(), `BEGIN`); err != nil {
				t.Fatal(err)
			}
			if err := manualRecoveryCanonicalSQL(context.Background(), conn, f.permit.OperationID, lease, asset, delta); err == nil {
				t.Fatal("fault injection did not fail")
			}
			if _, err := conn.ExecContext(context.Background(), `COMMIT`); err == nil {
				t.Fatal("partial recovery committed after caller ignored statement failure")
			}
			if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			if count(t, db, "manual_recovery_commands") != 1 || count(t, db, "manual_recovery_scopes") != 0 || count(t, db, "position_effects") != 0 {
				t.Fatal("fault partially changed canonical economics")
			}
			assertManualRecoveryStates(t, f, "observed", "", "reserved", "RESERVED", "signed")
		})
	}
}

func TestManualRecoveryCloseReopenAndLeaseTakeover(t *testing.T) {
	f, recovery, _, lease := manualRecoveryFixture(t, "submitted", "buy")
	if err := recovery.ReconcileWithLease(context.Background(), f.permit.OperationID, func() trade.RecoveryLeaseFence { return lease }); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := storage.Open(context.Background(), storage.TradeOwner, f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	db, err := fresh.SQLDB(storage.TradeOwner)
	if err != nil {
		t.Fatal(err)
	}
	store, err := trade.NewStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.ListControlledRecoveryItems(context.Background())
	if err != nil || len(items) != 1 || items[0].AttemptID != f.permit.AttemptID || items[0].TxHash != f.permit.TxHash {
		t.Fatal("restart lost exact durable work", items, err)
	}
	if _, err := db.Exec(`UPDATE canary_worker_leases SET holder_id='new-owner',lease_epoch=2 WHERE role='RECOVERY'`); err != nil {
		t.Fatal(err)
	}
	var observation trade.ReceiptObservation
	if err := db.QueryRow(`SELECT id,attempt_id,tx_hash,block_number,block_hash,receipt_status,canonical_state,reconciliation_version FROM receipt_observations`).Scan(&observation.ID, &observation.AttemptID, &observation.TxHash, &observation.BlockNumber, &observation.BlockHash, &observation.Status, &observation.CanonicalState, &observation.Version); err != nil {
		t.Fatal(err)
	}
	stored, found, err := store.LoadEncryptedArtifact(context.Background(), f.permit.OperationID)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if err := store.OrphanFenced(context.Background(), stored.SignedArtifact, observation, time.Now(), &lease); !errors.Is(err, trade.ErrCanaryRecoveryLeaseLost) {
		t.Fatal("stale recovery owner mutated a reopened database", err)
	}
	if count(t, db, "manual_recovery_commands") != 2 || f.fake.calls != 1 {
		t.Fatal("restart duplicated an action")
	}
}

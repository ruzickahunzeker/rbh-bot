package tradeapi

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

type localPostRecoveryBackend struct {
	*httpRecoveryReceiptBackend
	pending bool
	failure error
}

func (b *localPostRecoveryBackend) TransactionReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	if b.failure != nil {
		return nil, b.failure
	}
	if b.pending {
		return nil, nil
	}
	return b.httpRecoveryReceiptBackend.TransactionReceipt(ctx, hash)
}

type localPostRecovery struct {
	service *trade.RecoveryService
	worker  *trade.ControlledRecoveryWorker
	backend *localPostRecoveryBackend
	query   *manualRecoveryQuerier
	cipher  *httpRecoveryReadCipher
	lease   func() trade.RecoveryLeaseFence
}

func newLocalPostRecovery(t *testing.T, f localPostFixture, r Record, status uint64) localPostRecovery {
	t.Helper()
	store := f.h.executor.store
	canonical := &types.Header{Number: big.NewInt(100), Extra: []byte("post-canonical")}
	receipt := &types.Receipt{TxHash: common.HexToHash(r.Execution.TxHash), Status: status, BlockNumber: canonical.Number, BlockHash: canonical.Hash()}
	if status == types.ReceiptStatusSuccessful {
		out := r.Validation.Output
		input, inputOK := new(big.Int).SetString(out.Resolution.AmountIn, 10)
		output, outputOK := new(big.Int).SetString(out.QuotedOut, 10)
		if !inputOK || !outputOK {
			t.Fatal("invalid original receipt fixture amounts")
		}
		word := func(v *big.Int) []byte { return v.FillBytes(make([]byte, 32)) }
		data := append(append(append(word(input), word(output)...), word(big.NewInt(10))...), word(big.NewInt(0))...)
		topic := parser.TopicPonsCurveBuy
		if out.Resolution.Side == "sell" {
			topic = parser.TopicPonsCurveSell
		}
		wallet := common.HexToAddress(f.h.api.identity.WalletAddress)
		owner := common.BytesToHash(common.LeftPadBytes(wallet.Bytes(), 32))
		receipt.Logs = []*types.Log{{Address: common.HexToAddress(out.Resolution.Token.Curve), Topics: []common.Hash{topic, owner, owner}, Data: data, TxHash: receipt.TxHash, BlockHash: receipt.BlockHash, BlockNumber: 100}}
	}
	backend := &localPostRecoveryBackend{httpRecoveryReceiptBackend: &httpRecoveryReceiptBackend{receipt: receipt, canonical: canonical, latest: &types.Header{Number: big.NewInt(102)}}}
	resolver, err := trade.NewPonsCurveEffectResolver(store)
	if err != nil {
		t.Fatal(err)
	}
	cipher := &httpRecoveryReadCipher{base: f.h.verifier.decryptor}
	service, err := trade.NewRecoveryServiceWithCipher(store, cipher, backend, resolver, trade.CanonicalPolicy{Confirmations: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	lease := trade.RecoveryLeaseFence{Environment: f.h.scope.Environment, HolderID: "local-post-recovery-only", Epoch: 1}
	if err := store.AcquireCanaryWorkerLease(context.Background(), "RECOVERY", lease.Environment, lease.HolderID, lease.Epoch, now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	q := &manualRecoveryQuerier{evidence: trade.ControlledRecoveryEvidence{TxFound: true, ReceiptFound: true, NonceState: "RESERVED_UNRESOLVED", EvidenceHash: strings.Repeat("a", 64), ObservedAt: now}}
	worker, err := trade.NewControlledRecoveryWorker(store, service, q, lease.Environment, lease.HolderID, lease.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	return localPostRecovery{service, worker, backend, q, cipher, func() trade.RecoveryLeaseFence { return lease }}
}

func localPostEvents(t *testing.T, h http.Handler, request string, after uint64) Events {
	t.Helper()
	w := call(h, "GET", "/v1/trades/"+request+"/events?after="+strconv.FormatUint(after, 10), "", "")
	var page Events
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Scope != "MANUAL_ORDER_LIFECYCLE" {
		t.Fatal("invalid lifecycle page", w.Code, w.Body.String())
	}
	last := after
	for _, event := range page.Events {
		if event.Cursor <= last {
			t.Fatal("nonmonotonic lifecycle cursor")
		}
		last = event.Cursor
	}
	if len(page.Events) > 100 || page.NextCursor != last {
		t.Fatal("unstable lifecycle pagination")
	}
	assertExecutionPublicJSON(t, w.Body.String())
	return page
}

func TestLocalPostExecutionRecoveryCanonicalReorgReapply(t *testing.T) {
	for _, tc := range []struct{ side, outcome string }{{"buy", "submitted"}, {"sell_amount", "submitted"}, {"sell_percent", "submitted"}, {"buy", "unknown"}} {
		t.Run(tc.side+"_"+tc.outcome, func(t *testing.T) {
			f := newLocalPostFixture(t, tc.side)
			f.transport.outcome = tc.outcome
			w := call(f.h, "POST", "/v1/trades", "recover", encode(f.request))
			code := 202
			if tc.outcome == "unknown" {
				code = 503
			}
			if w.Code != code {
				t.Fatal("unexpected first POST", w.Code, w.Body.String())
			}
			r := localPostRecord(t, call(f.h, "GET", w.Header().Get("Location"), "", ""), 200)
			f.assertOne(t, r, 1)
			var attempt string
			if err := f.store.db.QueryRow(`SELECT attempt_id FROM manual_nonce_claims WHERE operation_id=?`, *r.OperationID).Scan(&attempt); err != nil {
				t.Fatal(err)
			}
			identity := httpRecoveryArtifactIdentity(t, f.store, attempt)
			start := localPostEvents(t, f.h, r.RequestID, 0)
			recovery := newLocalPostRecovery(t, f, r, types.ReceiptStatusSuccessful)
			if _, err := f.store.db.Exec(`UPDATE canary_control_state SET mode='DISABLED',emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
				t.Fatal(err)
			}
			// Explicit expired HTTP clock; query/recovery does not request new
			// deployment or action permission. No signing/transport dependency
			// is added to the recovery worker or production resolver.
			expires, _ := canonicalManualTime(r.ExpiresAt)
			f.h.api.now = func() time.Time { return expires.Add(time.Hour) }
			for i := 0; i < 2; i++ {
				if err := recovery.worker.RunOnce(context.Background()); err != nil {
					t.Fatal("stopped/revoked/expired recovery failed", err)
				}
			}
			got := localPostRecord(t, call(f.h, "GET", "/v1/trades/"+r.RequestID, "", ""), 200)
			if got.Execution.ReceiptState != "canonical_success" || got.Execution.EffectState != "active" {
				t.Fatal("canonical effect missing", encode(got.Execution))
			}
			original := recovery.backend.canonical
			recovery.backend.canonical = &types.Header{Number: original.Number, Extra: []byte("post-reorg")}
			for i := 0; i < 2; i++ {
				if err := recovery.worker.RunOnce(context.Background()); err != nil {
					t.Fatal("reorg failed", err)
				}
			}
			got = localPostRecord(t, call(f.h, "GET", "/v1/trades/"+r.RequestID, "", ""), 200)
			if got.Execution.ReceiptState != "orphaned" || got.Execution.EffectState != "rolled_back" {
				t.Fatal("rollback missing", encode(got.Execution))
			}
			recovery.backend.canonical = original
			for i := 0; i < 2; i++ {
				if err := recovery.worker.RunOnce(context.Background()); err != nil {
					t.Fatal("reapply failed", err)
				}
			}
			got = localPostRecord(t, call(f.h, "GET", "/v1/trades/"+r.RequestID, "", ""), 200)
			if got.Execution.ReceiptState != "canonical_success" || got.Execution.EffectState != "active" || got.ExpiresAt != r.ExpiresAt {
				t.Fatal("reapply/expiry drift")
			}
			for transition := range map[string]bool{"apply": true, "rollback": true, "reapply": true} {
				var n int
				if err := f.store.db.QueryRow(`SELECT COUNT(*) FROM position_effect_history WHERE transition=?`, transition).Scan(&n); err != nil || n != 1 {
					t.Fatal("position effect not exactly once", transition, n, err)
				}
			}
			var delta string
			if err := f.store.db.QueryRow(`SELECT delta FROM position_effects WHERE attempt_id=?`, attempt).Scan(&delta); err != nil {
				t.Fatal(err)
			}
			want := r.Validation.Output.QuotedOut
			if tc.side != "buy" {
				want = "-" + r.Validation.Output.Resolution.AmountIn
			}
			if delta != want {
				t.Fatal("production resolver applied wrong unit/direction", delta, want)
			}
			if httpRecoveryArtifactIdentity(t, f.store, attempt) != identity || recovery.cipher.encryptCalls != 0 {
				t.Fatal("recovery mutated/encrypted original artifact")
			}
			page := localPostEvents(t, f.h, r.RequestID, start.NextCursor)
			if len(page.Events) == 0 {
				t.Fatal("lifecycle updates not visible")
			}
			empty := localPostEvents(t, f.h, r.RequestID, page.NextCursor)
			if len(empty.Events) != 0 || empty.NextCursor != page.NextCursor {
				t.Fatal("empty cursor skipped work")
			}
			// Real file-backed close/reopen; a fresh read-only HTTP composition
			// exposes the original current history with no execution primitives.
			d, s := bindingReopen(t, f.database, f.path)
			fresh, err := NewWithOptions(s, f.h.api.identity, testCredential, DefaultPolicy(), nil, Options{ManualIntake: true})
			if err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			duplicate := localPostRecord(t, call(fresh, "POST", "/v1/trades", "recover", encode(f.request)), 202)
			if !duplicate.Duplicate || duplicate.Execution.EffectState != "active" || duplicate.Execution.TxHash != r.Execution.TxHash || duplicate.ExpiresAt != r.ExpiresAt || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("restarted duplicate lost history or changed work")
			}
			if localPostEvents(t, fresh, r.RequestID, page.NextCursor).NextCursor != page.NextCursor || f.signer.calls.Load() != 1 || f.transport.calls.Load() != 1 {
				t.Fatal("restart reexecuted or cursor drift")
			}
			// A new worker/store/resolver discovers the retained submission from
			// reopened SQLite, not an HTTP queue. The existing independent
			// RECOVERY lease survives this restart; no new send lease is acquired.
			freshStore, err := trade.NewStore(d)
			if err != nil {
				t.Fatal(err)
			}
			items, err := freshStore.ListControlledRecoveryItems(context.Background())
			if err != nil || len(items) != 1 || items[0].OperationID != *r.OperationID || items[0].AttemptID != attempt {
				t.Fatal("reopen lost durable recovery work", items, err)
			}
			resolver, err := trade.NewPonsCurveEffectResolver(freshStore)
			if err != nil {
				t.Fatal(err)
			}
			freshRecovery, err := trade.NewRecoveryServiceWithCipher(freshStore, recovery.cipher, recovery.backend, resolver, trade.CanonicalPolicy{Confirmations: 1})
			if err != nil {
				t.Fatal(err)
			}
			lease := recovery.lease()
			worker, err := trade.NewControlledRecoveryWorker(freshStore, freshRecovery, recovery.query, lease.Environment, lease.HolderID, lease.Epoch)
			if err != nil {
				t.Fatal(err)
			}
			economics := foundationEconomicSnapshot(t, s)
			if err := worker.RunOnce(context.Background()); err != nil {
				t.Fatal("fresh worker failed durable rediscovery", err)
			}
			if foundationEconomicSnapshot(t, s) != economics || f.signer.calls.Load() != 1 || f.transport.calls.Load() != 1 || recovery.cipher.encryptCalls != 0 {
				t.Fatal("restarted worker duplicated effects or economic actions")
			}
		})
	}
}

func TestLocalPostExecutionUnknownQueryFrozenAndRecoveryFailure(t *testing.T) {
	f := newLocalPostFixture(t, "buy")
	f.transport.outcome = "unknown"
	w := call(f.h, "POST", "/v1/trades", "unknown-query", encode(f.request))
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	r := localPostRecord(t, call(f.h, "GET", w.Header().Get("Location"), "", ""), 200)
	recovery := newLocalPostRecovery(t, f, r, types.ReceiptStatusSuccessful)
	recovery.query.evidence.TxFound, recovery.query.evidence.ReceiptFound = false, false
	if _, err := f.store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
		t.Fatal(err)
	}
	for _, nonce := range []string{"RESERVED_UNRESOLVED", "UNSAFE"} {
		recovery.query.evidence.NonceState = nonce
		if err := recovery.worker.RunOnce(context.Background()); err != nil {
			t.Fatal("unknown query blocked", err)
		}
		var lane, risk string
		if err := f.store.db.QueryRow(`SELECT l.state,r.state FROM execution_wallet_lanes l JOIN canary_risk_reservations r ON r.operation_id=l.operation_id WHERE l.operation_id=?`, *r.OperationID).Scan(&lane, &risk); err != nil || lane != "frozen" || risk != "frozen" {
			t.Fatal("query released ambiguity", lane, risk, err)
		}
	}
	if recovery.query.calls != 2 || recovery.backend.receiptCalls != 0 {
		t.Fatal("query-only recovery tried canonicalization without propagation")
	}
	recovery.query.evidence.TxFound, recovery.query.evidence.ReceiptFound = true, true
	recovery.backend.failure = errors.New("controlled receipt RPC unavailable")
	beforeAudits, beforeAlerts := count(t, f.store.db, "canary_runtime_audit"), count(t, f.store.db, "canary_alert_outbox")
	if err := recovery.worker.RunOnce(context.Background()); err == nil {
		t.Fatal("RPC failure accepted")
	}
	if count(t, f.store.db, "canary_runtime_audit") <= beforeAudits || count(t, f.store.db, "canary_alert_outbox") <= beforeAlerts || count(t, f.store.db, "position_effects") != 0 {
		t.Fatal("RPC failure lost audit/alert or applied effect")
	}
	recovery.backend.failure = nil
	if err := recovery.worker.RunOnce(context.Background()); err != nil {
		t.Fatal("recovery not available after RPC recovered", err)
	}
	f.assertOne(t, r, 1)
	if recovery.cipher.encryptCalls != 0 {
		t.Fatal("recovery attempted encryption")
	}
}

func TestLocalPostExecutionRevertAndPending(t *testing.T) {
	f := newLocalPostFixture(t, "buy")
	r := localPostRecord(t, call(f.h, "POST", "/v1/trades", "revert", encode(f.request)), 202)
	recovery := newLocalPostRecovery(t, f, r, types.ReceiptStatusFailed)
	recovery.backend.pending = true
	if err := recovery.worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count(t, f.store.db, "receipt_observations") != 0 || count(t, f.store.db, "position_effects") != 0 {
		t.Fatal("missing receipt finalized")
	}
	recovery.backend.pending = false
	for i := 0; i < 2; i++ {
		if err := recovery.worker.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got := localPostRecord(t, call(f.h, "GET", "/v1/trades/"+r.RequestID, "", ""), 200)
	if got.Execution.ReceiptState != "canonical_revert" || got.Execution.EffectState != "" || count(t, f.store.db, "position_effects") != 0 {
		t.Fatal("revert applied position effect")
	}
	var risk, usage string
	if err := f.store.db.QueryRow(`SELECT r.state,u.state FROM canary_risk_reservations r JOIN canary_authorization_operation_usage u ON u.operation_id=r.operation_id WHERE r.operation_id=?`, *r.OperationID).Scan(&risk, &usage); err != nil || risk != "released" || usage != "CONSUMED" {
		t.Fatal("revert refunded lifetime use or retained economic risk", risk, usage, err)
	}
	if w := call(f.h, "POST", "/v1/trades", "second-after-revert", encode(f.request)); w.Code != 422 || !strings.Contains(w.Body.String(), "MANUAL_WALLET_REUSE_UNSUPPORTED") {
		t.Fatal("revert became wallet reuse permission", w.Code)
	}
	f.assertOne(t, r, 1)
}

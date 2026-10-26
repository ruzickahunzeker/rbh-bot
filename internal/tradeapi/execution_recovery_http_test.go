package tradeapi

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

// This test-only recovery adapter has no signer or encryption capability.
type httpRecoveryReadCipher struct {
	base         manualArtifactDecryptor
	encryptCalls int
}

func (c *httpRecoveryReadCipher) KeyVersion() string { return c.base.KeyVersion() }
func (c *httpRecoveryReadCipher) Decrypt(version string, ciphertext, nonce, aad []byte) ([]byte, error) {
	return c.base.Decrypt(version, ciphertext, nonce, aad)
}
func (c *httpRecoveryReadCipher) Encrypt([]byte, []byte) ([]byte, []byte, error) {
	c.encryptCalls++
	return nil, nil, errors.New("HTTP recovery fixture forbids encryption")
}

// Read-only fake chain observations, not a transport or fake effect resolver.
type httpRecoveryReceiptBackend struct {
	receipt                   *types.Receipt
	canonical, latest         *types.Header
	receiptCalls, headerCalls int
}

func (b *httpRecoveryReceiptBackend) TransactionReceipt(_ context.Context, hash common.Hash) (*types.Receipt, error) {
	b.receiptCalls++
	if hash != b.receipt.TxHash {
		return nil, trade.ErrArtifactIntegrity
	}
	return b.receipt, nil
}
func (b *httpRecoveryReceiptBackend) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	b.headerCalls++
	if number == nil {
		return b.latest, nil
	}
	if number.Cmp(b.canonical.Number) != 0 {
		return nil, trade.ErrArtifactIntegrity
	}
	return b.canonical, nil
}

type httpExecutionRecoveryFixture struct {
	submission manualSubmissionFixture
	h          *API
	backend    *httpRecoveryReceiptBackend
	service    *trade.RecoveryService
	cipher     *httpRecoveryReadCipher
	lease      func() trade.RecoveryLeaseFence
	key        string
	request    Request
	expires    string
	identity   string
	asset      string
}

func newHTTPExecutionRecoveryFixture(t *testing.T, outcome string, receiptStatus uint64) httpExecutionRecoveryFixture {
	t.Helper()
	f := newManualSubmissionFixture(t, "buy")
	if outcome == "broadcast_unknown" {
		f.fake.result, f.fake.err = manualTransportResult{}, errors.New("local ambiguous transport")
	}
	sub, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID)
	if sub.State != outcome || f.fake.calls != 1 {
		t.Fatalf("A local submission fixture: state=%s calls=%d err=%v", sub.State, f.fake.calls, err)
	}
	store := f.e.verifier.store
	h, err := NewWithOptions(store, f.id, testCredential, DefaultPolicy(), nil, Options{ManualIntake: true})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(context.Background(), f.id, f.request, false)
	if err != nil {
		t.Fatal(err)
	}
	var key, environment, curve, asset, input string
	if err := store.db.QueryRow(`SELECT src.idempotency_key,b.environment,
 json_extract(c.contract_json,'$.output.resolution.token.curve'),json_extract(c.contract_json,'$.output.resolution.token.address'),
 json_extract(c.contract_json,'$.output.resolution.amount_in')
 FROM manual_http_order_sources src JOIN manual_execution_bindings b ON b.operation_id=src.operation_id
 JOIN manual_admission_commands c ON c.operation_id=src.operation_id WHERE src.id=?`, f.request).Scan(&key, &environment, &curve, &asset, &input); err != nil {
		t.Fatal(err)
	}
	canonical := &types.Header{Number: big.NewInt(100), Extra: []byte("http-canonical")}
	receipt := &types.Receipt{TxHash: common.HexToHash(sub.TxHash), Status: receiptStatus, BlockNumber: big.NewInt(100), BlockHash: canonical.Hash()}
	if receiptStatus == types.ReceiptStatusSuccessful {
		amountIn, ok := new(big.Int).SetString(input, 10)
		if !ok {
			t.Fatal("invalid durable input amount")
		}
		word := func(value *big.Int) []byte { return value.FillBytes(make([]byte, 32)) }
		data := append(append(append(word(amountIn), word(big.NewInt(1000000))...), word(big.NewInt(10))...), word(big.NewInt(0))...)
		wallet := common.HexToAddress(f.id.WalletAddress)
		walletTopic := common.BytesToHash(common.LeftPadBytes(wallet.Bytes(), 32))
		receipt.Logs = []*types.Log{{Address: common.HexToAddress(curve), Topics: []common.Hash{parser.TopicPonsCurveBuy, walletTopic, walletTopic},
			Data: data, TxHash: receipt.TxHash, BlockHash: receipt.BlockHash, BlockNumber: 100}}
	}
	backend := &httpRecoveryReceiptBackend{receipt: receipt, canonical: canonical, latest: &types.Header{Number: big.NewInt(102)}}
	resolver, err := trade.NewPonsCurveEffectResolver(f.e.store)
	if err != nil {
		t.Fatal(err)
	}
	cipher := &httpRecoveryReadCipher{base: f.e.verifier.decryptor}
	service, err := trade.NewRecoveryServiceWithCipher(f.e.store, cipher, backend, resolver, trade.CanonicalPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := f.e.store.AcquireCanaryWorkerLease(context.Background(), "RECOVERY", environment, "http-recovery-reader", 1, now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	lease := func() trade.RecoveryLeaseFence {
		return trade.RecoveryLeaseFence{Environment: environment, HolderID: "http-recovery-reader", Epoch: 1}
	}
	if _, err := store.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
		t.Fatal(err)
	}
	expires, err := time.Parse(time.RFC3339Nano, stored.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	h.now = func() time.Time { return expires.Add(time.Hour) }
	return httpExecutionRecoveryFixture{submission: f, h: h, backend: backend, service: service, cipher: cipher, lease: lease, key: key,
		request: stored.Request, expires: stored.ExpiresAt, identity: httpRecoveryArtifactIdentity(t, store, f.permit.AttemptID), asset: asset}
}

func httpRecoveryArtifactIdentity(t *testing.T, store *Store, attempt string) string {
	t.Helper()
	var raw string
	if err := store.db.QueryRow(`SELECT json_object('attempt',actual.attempt_json,'claim_hash',q.observation_hash,'artifact',m.envelope_json)
 FROM manual_nonce_actual_attempt actual JOIN manual_nonce_claims q ON q.attempt_id=actual.id
 JOIN manual_signed_artifact_commands m ON m.operation_id=q.operation_id WHERE actual.id=?`, attempt).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return hash([]byte(raw))
}

func (f httpExecutionRecoveryFixture) assertHTTPFacts(t *testing.T, receipt, effect string) {
	t.Helper()
	store := f.submission.e.verifier.store
	before := admittedDatabaseSnapshot(t, store.db)
	queries := f.backend.receiptCalls + f.backend.headerCalls
	var expected *ExecutionSnapshot
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/trades/" + f.submission.request, ""},
		{http.MethodGet, "/v1/trades/by-idempotency-key", ""},
		{http.MethodPost, "/v1/trades", encode(f.request)},
	} {
		response := call(f.h, request.method, request.path, f.key, request.body)
		code := http.StatusOK
		if request.method == http.MethodPost {
			code = http.StatusAccepted
		}
		got := progressResponse(t, response, code, "ADMISSION_RECORDED")
		if got.Execution == nil || got.Execution.TxHash != f.submission.permit.TxHash || got.Execution.ReceiptState != receipt || got.Execution.EffectState != effect || got.ExpiresAt != f.expires || got.Status != "RECORDED" || got.Reason != "VALIDATION_NOT_CONNECTED" {
			t.Fatalf("HTTP lost current verified receipt/effect facts: %+v", got.Execution)
		}
		if request.method == http.MethodPost && !got.Duplicate {
			t.Fatal("historical duplicate created a new request")
		}
		if expected != nil && !reflect.DeepEqual(expected, got.Execution) {
			t.Fatal("GET and default-off duplicate disagree about current execution")
		}
		expected = got.Execution
		assertExecutionPublicJSON(t, response.Body.String())
	}
	if admittedDatabaseSnapshot(t, store.db) != before || queries != f.backend.receiptCalls+f.backend.headerCalls {
		t.Fatal("HTTP history itself mutated durable state or queried recovery")
	}
	f.assertNoNewEconomicAction(t)
}

func (f httpExecutionRecoveryFixture) assertNoNewEconomicAction(t *testing.T) {
	t.Helper()
	store := f.submission.e.verifier.store
	if f.submission.fake.calls != 1 || f.cipher.encryptCalls != 0 || httpRecoveryArtifactIdentity(t, store, f.submission.permit.AttemptID) != f.identity {
		t.Fatal("recovery/HTTP changed the original artifact/nonce or invoked transport/encryption")
	}
	for _, table := range []string{"transaction_attempts", "transaction_submissions", "manual_nonce_claims", "manual_signed_artifact_commands", "canary_send_permits"} {
		if count(t, store.db, table) != 1 {
			t.Fatal("recovery created a second economic artifact or permit", table)
		}
	}
}

func (f httpExecutionRecoveryFixture) events(t *testing.T, after uint64) Events {
	t.Helper()
	store := f.submission.e.verifier.store
	before := admittedDatabaseSnapshot(t, store.db)
	response := call(f.h, http.MethodGet, "/v1/trades/"+f.submission.request+"/events?after="+strconv.FormatUint(after, 10), "", "")
	var page Events
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Scope != "MANUAL_ORDER_LIFECYCLE" || len(page.Events) > 100 {
		t.Fatalf("invalid lifecycle page: code=%d scope=%s", response.Code, page.Scope)
	}
	previous := after
	for _, event := range page.Events {
		if event.Cursor <= previous {
			t.Fatal("lifecycle cursor did not increase strictly")
		}
		previous = event.Cursor
	}
	if page.NextCursor != previous || admittedDatabaseSnapshot(t, store.db) != before {
		t.Fatal("event polling advanced over unseen work or mutated state")
	}
	assertExecutionPublicJSON(t, response.Body.String())
	return page
}

func (f httpExecutionRecoveryFixture) assertEffects(t *testing.T, active string, apply, rollback, reapply int) {
	t.Helper()
	store := f.submission.e.verifier.store
	var state, asset, delta string
	if err := store.db.QueryRow(`SELECT state,asset,delta FROM position_effects WHERE attempt_id=?`, f.submission.permit.AttemptID).Scan(&state, &asset, &delta); err != nil || state != active || common.HexToAddress(asset) != common.HexToAddress(f.asset) || delta != "1000000" {
		t.Fatal("position effect mismatch", state, asset, delta, err)
	}
	for transition, want := range map[string]int{"apply": apply, "rollback": rollback, "reapply": reapply} {
		var got int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM position_effect_history h JOIN position_effects p ON p.effect_id=h.effect_id WHERE p.attempt_id=? AND h.transition=?`, f.submission.permit.AttemptID, transition).Scan(&got); err != nil || got != want {
			t.Fatal("duplicate/lost position transition", transition, got, want, err)
		}
	}
}

func TestHTTPCExecutionRecoveryCanonicalRollbackReapply(t *testing.T) {
	for _, outcome := range []string{"submitted", "broadcast_unknown"} {
		t.Run(outcome, func(t *testing.T) {
			f := newHTTPExecutionRecoveryFixture(t, outcome, types.ReceiptStatusSuccessful)
			f.assertHTTPFacts(t, "", "")
			page := f.events(t, 0)
			var intake uint64
			if err := f.submission.e.verifier.store.db.QueryRow(`SELECT sequence FROM manual_http_order_sources WHERE id=?`, f.submission.request).Scan(&intake); err != nil || len(page.Events) == 0 || page.Events[0].Cursor != intake || page.Events[0].Type != "MANUAL_HTTP_ORDER_RECORDED" {
				t.Fatal("original intake cursor lost", err)
			}
			cursor := page.NextCursor
			if empty := f.events(t, cursor); len(empty.Events) != 0 || empty.NextCursor != cursor {
				t.Fatal("empty poll skipped later lifecycle work")
			}
			for range 2 {
				if err := f.service.ReconcileWithLease(context.Background(), f.submission.permit.OperationID, f.lease); err != nil {
					t.Fatal("source-bound canonical recovery", err)
				}
			}
			f.assertHTTPFacts(t, "canonical_success", "active")
			f.assertEffects(t, "active", 1, 0, 0)
			page = f.events(t, cursor)
			if len(page.Events) != 2 || page.Events[0].Type != "MANUAL_RECOVERY_OBSERVE" || page.Events[1].Type != "MANUAL_RECOVERY_CANONICAL" {
				t.Fatal("canonical recovery missing atomic journal events", page.Events)
			}
			cursor = page.NextCursor
			var observed trade.ReceiptObservation
			if err := f.submission.e.verifier.store.db.QueryRow(`SELECT id,attempt_id,tx_hash,block_number,block_hash,receipt_status,canonical_state,reconciliation_version FROM receipt_observations WHERE attempt_id=?`, f.submission.permit.AttemptID).
				Scan(&observed.ID, &observed.AttemptID, &observed.TxHash, &observed.BlockNumber, &observed.BlockHash, &observed.Status, &observed.CanonicalState, &observed.Version); err != nil {
				t.Fatal(err)
			}
			original := f.backend.canonical
			f.backend.canonical = &types.Header{Number: big.NewInt(100), Extra: []byte("http-orphaned")}
			for range 2 {
				if err := f.service.CheckReorgWithLease(context.Background(), f.submission.permit.OperationID, observed, f.lease); err != nil {
					t.Fatal("source-bound reorg rollback", err)
				}
			}
			f.assertHTTPFacts(t, "orphaned", "rolled_back")
			f.assertEffects(t, "rolled_back", 1, 1, 0)
			var lane, reservation string
			if err := f.submission.e.verifier.store.db.QueryRow(`SELECT l.state,r.state FROM execution_wallet_lanes l JOIN canary_risk_reservations r ON r.operation_id=l.operation_id WHERE l.operation_id=?`, f.submission.permit.OperationID).Scan(&lane, &reservation); err != nil || lane != "frozen" || reservation != "frozen" {
				t.Fatal("reorg ownership did not freeze", lane, reservation, err)
			}
			page = f.events(t, cursor)
			if len(page.Events) != 1 || page.Events[0].Type != "MANUAL_RECOVERY_ORPHAN" {
				t.Fatal("rollback event missing or duplicated", page.Events)
			}
			cursor = page.NextCursor
			f.backend.canonical = original
			for range 2 {
				if err := f.service.ReconcileWithLease(context.Background(), f.submission.permit.OperationID, f.lease); err != nil {
					t.Fatal("source-bound re-canonical recovery", err)
				}
			}
			f.assertHTTPFacts(t, "canonical_success", "active")
			f.assertEffects(t, "active", 1, 1, 1)
			page = f.events(t, cursor)
			if len(page.Events) != 1 || page.Events[0].Type != "MANUAL_RECOVERY_CANONICAL" {
				t.Fatal("reapply event missing or duplicated", page.Events)
			}
			if empty := f.events(t, page.NextCursor); len(empty.Events) != 0 || empty.NextCursor != page.NextCursor {
				t.Fatal("final empty page changed cursor")
			}
			f.assertNoNewEconomicAction(t)
		})
	}
}

func TestHTTPCExecutionRecoveryCanonicalRevertHasNoPositionEffect(t *testing.T) {
	f := newHTTPExecutionRecoveryFixture(t, "broadcast_unknown", types.ReceiptStatusFailed)
	for range 2 {
		if err := f.service.ReconcileWithLease(context.Background(), f.submission.permit.OperationID, f.lease); err != nil {
			t.Fatal("source-bound canonical revert", err)
		}
	}
	f.assertHTTPFacts(t, "canonical_revert", "")
	store := f.submission.e.verifier.store
	if count(t, store.db, "position_effects") != 0 || count(t, store.db, "position_effect_history") != 0 {
		t.Fatal("canonical revert created position effect")
	}
	page := f.events(t, 0)
	canonical := 0
	for _, event := range page.Events {
		if event.Type == "MANUAL_RECOVERY_CANONICAL" {
			canonical++
		}
	}
	if canonical != 1 {
		t.Fatal("revert canonical event absent or duplicated", canonical)
	}
	f.assertNoNewEconomicAction(t)
}

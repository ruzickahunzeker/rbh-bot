package tradeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

// This entire composition is test-only: no product constructor, Options field,
// startup, network listener or broadcaster can instantiate it. The underlying
// authenticated API still performs only intake/validation/admission in product.
type localPostHarness struct {
	api        *API
	scope      manualAdmissionScope
	binding    *manualExecutionBindingWriter
	nonce      *manualNonceClaimWriter
	signing    *manualLocalSigningWriter
	verifier   *manualRuntimeVerifier
	executor   *manualSubmissionExecutor
	waitBudget time.Duration
	checkpoint func(string) error
	onFailure  func(error)
}

func (h *localPostHarness) at(stage string) error {
	if h.checkpoint != nil {
		return h.checkpoint(stage)
	}
	return nil
}

func (h *localPostHarness) executeOriginal(ctx context.Context, request string) error {
	id := h.api.identity // constructor-owned, never decoded from client fields
	if err := h.at("before_binding"); err != nil {
		return err
	}
	b, err := h.binding.bind(ctx, id, request, h.scope)
	if err != nil {
		return err
	}
	if b.Duplicate { // history cannot recreate a lost continuation
		return ErrConflict
	}
	if err := h.at("after_binding"); err != nil {
		return err
	}
	n, err := h.nonce.claim(ctx, id, request, h.scope)
	if err != nil {
		return err
	}
	if n.Duplicate {
		return ErrConflict
	}
	if err := h.at("after_nonce"); err != nil {
		return err
	}
	signed, err := h.signing.sign(ctx, id, request, h.scope)
	if err != nil {
		return err
	}
	if signed.Duplicate || signed.Status != "LOCAL_ARTIFACT_STORED_NOT_PERMISSION" {
		return ErrConflict
	}
	if err := h.at("after_artifact"); err != nil {
		return err
	}
	// Derive structural facts from the original durable projection, not POST or
	// a fixture's handcrafted signed artifact. Triggers own atomic witnesses.
	var binding, digest, created string
	if err := h.api.store.db.QueryRowContext(ctx, `SELECT binding_json,manual_artifact_bytes_sha256(hex(binding_json)),artifact_completed_at
 FROM manual_submission_foundation_expected WHERE operation_id=?`, signed.OperationID).Scan(&binding, &digest, &created); err != nil {
		return err
	}
	if _, err := h.api.store.db.ExecContext(ctx, `INSERT INTO manual_submission_foundations(operation_id,binding_json,binding_hash,created_at) VALUES(?,?,?,?)`, signed.OperationID, binding, digest, created); err != nil {
		return err
	}
	if err := h.at("after_foundation"); err != nil {
		return err
	}
	if _, err := h.verifier.capture(ctx, id, request); err != nil {
		return err
	}
	p, err := (&manualFirstBroadcastIssuer{verifier: h.verifier}).issue(ctx, id, request, signed.Artifact.AttemptID, signed.Artifact.RawSHA256, signed.Artifact.TxHash)
	if err != nil {
		return err
	}
	if err := h.at("after_permit"); err != nil {
		return err
	}
	if _, err := h.executor.execute(ctx, id, request, p.ID); err != nil {
		return err
	}
	return h.at("after_execution")
}

func copyLocalHTTPHeader(to, from http.Header) {
	for k, values := range from {
		to[k] = append([]string(nil), values...)
	}
}

func (h *localPostHarness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	accepted := httptest.NewRecorder()
	h.api.ServeHTTP(accepted, r) // auth, strict JSON, durable dedupe, current admission
	copyLocalHTTPHeader(w.Header(), accepted.Header())
	var record Record
	if r.Method != http.MethodPost || r.URL.Path != "/v1/trades" || accepted.Code != http.StatusAccepted ||
		json.Unmarshal(accepted.Body.Bytes(), &record) != nil || record.Admission == nil ||
		record.Admission.Status != "ADMITTED_NOT_SIGNED_NOT_SUBMITTED" {
		w.WriteHeader(accepted.Code)
		_, _ = w.Write(accepted.Body.Bytes())
		return
	}
	// Only the unique FIRST ADMISSION commit's uninterrupted stack owns this
	// local attempt. Intake and admission winners can differ under concurrency;
	// the composite public Duplicate flag cannot identify continuation ownership.
	// Historical admission/restart never enqueues or resumes unstarted work.
	if h.waitBudget <= 0 || h.waitBudget > 5*time.Minute {
		writeError(w, r, ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.waitBudget)
	defer cancel()
	if err := h.executeOriginal(ctx, record.RequestID); err != nil {
		if h.onFailure != nil {
			h.onFailure(err)
		}
		writeError(w, r, err) // Location survives even if intake/claim already committed
		return
	}
	if err := h.at("before_response"); err != nil {
		writeError(w, r, err)
		return
	}
	current, err := h.api.store.Get(ctx, h.api.identity, record.RequestID, false)
	if err != nil {
		writeError(w, r, err)
		return
	}
	current.Duplicate = record.Duplicate // preserve intake/admission history truth
	// 202 still means durable acceptance, never receipt/canonical success or
	// a production send grant. execution carries the local historical outcome.
	writeJSON(w, http.StatusAccepted, current)
}

// Implements ONLY the private fake synchronous transport. Decoding the exact
// borrowed signed bytes yields the hash; no network or real RPC send exists.
type localPostTransport struct {
	calls    atomic.Int64
	mu       sync.Mutex
	borrowed []byte
	rawHash  string
	outcome  string
	during   func(context.Context)
}

func (f *localPostTransport) submitExact(ctx context.Context, raw []byte) (manualTransportResult, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.borrowed, f.rawHash = raw, hash(raw)
	f.mu.Unlock()
	if f.during != nil {
		f.during(ctx)
	}
	switch f.outcome {
	case "unknown":
		return manualTransportResult{}, errors.New("controlled ambiguous outcome")
	case "known_unsent":
		return manualTransportResult{deterministicUnsent: true}, nil
	case "contradiction":
		return manualTransportResult{txHash: "wrong-controlled-hash"}, nil
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return manualTransportResult{}, err
	}
	return manualTransportResult{txHash: tx.Hash().Hex()}, nil
}

type localPostFixture struct {
	database  *storage.Database
	store     *Store
	path      string
	h         *localPostHarness
	request   Request
	signer    *localCountingSigner
	transport *localPostTransport
	clock     *runtimeTestClock
	backend   *validationBackend
}

func newLocalPostFixture(t *testing.T, side string) localPostFixture {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
	// One unrelated validated-only source seeds existing local policy/config.
	// The target POST below has not been recorded/admitted/claimed/signed/sent.
	d, s, original, seed, scope, path := manualInputsFixtureBudgetWallet(t, side, 10, "1000000000000000000", wallet)
	// The inherited offline fixture seeds a SYNTHETIC grant. Restore the exact
	// original no-arm guard before ALL HTTP/action acceptance, not after it.
	raw, err := os.ReadFile("../storage/migrations/trade/018_manual_risk_units.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, guard, ok := strings.Cut(string(raw), "CREATE TRIGGER manual_b1_no_arm ")
	if !ok {
		t.Fatal("missing no-arm guard")
	}
	body, _, ok := strings.Cut(guard, "END;")
	if !ok {
		t.Fatal("missing no-arm terminator")
	}
	definition := "CREATE TRIGGER manual_b1_no_arm " + body + "END;"
	if _, err = s.db.Exec(definition); err != nil {
		t.Fatal(err)
	}
	var restored string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='manual_b1_no_arm'`).Scan(&restored); err != nil || restored+";" != definition {
		t.Fatal("no-arm guard not exactly restored before actions", err)
	}
	baseSigner, err := trade.NewLocalSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	signer := &localCountingSigner{signer: baseSigner}
	cipher, err := trade.NewAESGCMCipher("local-post-only", bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	backend := &validationBackend{wallet: wallet}
	api := admissionHTTP(t, s, original, scope, backend)
	clock := &runtimeTestClock{wall: testNow, change: func(c *runtimeTestClock) { c.wall = c.wall.Add(time.Nanosecond) }}
	if err := initializeManualRuntimeClock(context.Background(), s, scope.Environment, clock, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO canary_worker_leases VALUES('SUBMISSION',?,'local-post-owner',1,?,?)`, scope.Environment, testNow.Add(time.Minute).Format(time.RFC3339Nano), testNow.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	process := manualRuntimeProcess{scope: scope, binaryHash: strings.Repeat("1", 64), networkProfile: "local-post-fixture", holder: "local-post-owner", provider: "trusted-fixture", leaseEpoch: 1,
		networkCheckpoint: seed.Validation.Output.Resolution.Token.Block}
	process.manifestHash = runtimeManifestDigest(process)
	verifier := &manualRuntimeVerifier{store: s, profile: manualRuntimeProfile{process: process, maximumError: time.Second, queryTimeout: time.Second, maxAge: time.Minute}, timeSource: clock,
		identity: &runtimeTestIdentity{process: process}, decryptor: cipher, observer: &runtimeTestObserver{environment: scope.Environment, profile: process.networkProfile}}
	tradeStore, err := trade.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	transport := &localPostTransport{}
	h := &localPostHarness{api: api, scope: scope, waitBudget: 5 * time.Minute, binding: bindingWriter(s), nonce: nonceWriter(s, &localNonceObserver{nonce: "23"}),
		signing:  &manualLocalSigningWriter{store: s, observer: &localNonceObserver{nonce: "23"}, signer: signer, cipher: cipher, now: func() time.Time { return testNow }},
		verifier: verifier, executor: &manualSubmissionExecutor{verifier: verifier, store: tradeStore, transport: transport, timeout: time.Second}}
	h.onFailure = func(err error) { t.Logf("local action sequence failed: %v", err) }
	for _, table := range []string{"manual_admission_commands", "manual_execution_bindings", "manual_nonce_claims", "manual_signing_intents", "manual_signed_artifact_commands", "canary_send_permits", "transaction_submissions"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("target work was prepared before POST", table)
		}
	}
	return localPostFixture{d, s, path, h, seed.Request, signer, transport, clock, backend}
}

func localPostRecord(t *testing.T, w *httptest.ResponseRecorder, status int) Record {
	t.Helper()
	var r Record
	if w.Code != status || json.Unmarshal(w.Body.Bytes(), &r) != nil || r.OperationID == nil || r.SendAuthorized || r.SubmissionQueued || r.ContractDeadline || r.DeadlineCapability != "APPLICATION_TTL_ONLY" {
		t.Fatalf("unexpected local HTTP result: %d %s", w.Code, w.Body.String())
	}
	assertExecutionPublicJSON(t, w.Body.String())
	return r
}

func (f localPostFixture) assertOne(t *testing.T, r Record, calls int64) {
	t.Helper()
	for _, table := range []string{"manual_admission_commands", "manual_execution_bindings", "manual_nonce_claims", "transaction_attempts", "manual_signed_artifact_commands", "canary_send_permits", "transaction_submissions", "canary_risk_reservations", "canary_authorization_operation_usage"} {
		if count(t, f.store.db, table) != 1 {
			t.Fatal("economic identity duplicated", table)
		}
	}
	var used int
	var total, nonce, raw, txHash, expiry string
	if err := f.store.db.QueryRow(`SELECT u.operation_count,u.total_input,q.nonce,b.artifact_hash,b.tx_hash,e.expires_at
 FROM canary_authorization_usage u JOIN manual_first_submission_bindings b ON b.authorization_id=u.authorization_id JOIN manual_nonce_claims q ON q.operation_id=b.operation_id JOIN manual_execution_bindings e ON e.operation_id=b.operation_id WHERE b.operation_id=?`, *r.OperationID).Scan(&used, &total, &nonce, &raw, &txHash, &expiry); err != nil || used != 1 || total != r.Validation.Output.Resolution.AmountIn || nonce != "23" || expiry != r.ExpiresAt {
		t.Fatal("budget/nonce/TTL drift", used, total, nonce, expiry, err)
	}
	f.transport.mu.Lock()
	defer f.transport.mu.Unlock()
	if f.signer.calls.Load() != 1 || f.transport.calls.Load() != calls {
		t.Fatal("wrong sign/send count", f.signer.calls.Load(), f.transport.calls.Load())
	}
	if calls > 0 && (f.transport.rawHash != raw || txHash != r.Execution.TxHash) {
		t.Fatal("wrong exact signed artifact")
	}
	for _, b := range f.transport.borrowed {
		if b != 0 {
			t.Fatal("borrowed plaintext retained")
		}
	}
	if count(t, f.store.db, "execution_reservations") != 0 {
		t.Fatal("legacy risk accounting used")
	}
}

// Permanent B3-D regressions promoted from the original independent B3-C
// overlay (SHA256 ab63ce71839acc0fcfe57fab8bbe151a130c6f5a96649e037a6460b55b58211a).
// These author-maintained tests do not constitute a new independent verdict.
package tradeapi

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type acceptanceDCipher struct {
	manualartifact.Cipher
	stage  string
	cancel context.CancelFunc
	keys   int
}

func (c *acceptanceDCipher) KeyVersion() string {
	c.keys++
	if c.stage == "key_panic" {
		panic("private injected secret")
	}
	return c.Cipher.KeyVersion()
}
func (c *acceptanceDCipher) Encrypt(p, a []byte) ([]byte, []byte, error) {
	if c.stage == "encrypt_panic" {
		panic("private injected secret")
	}
	if c.stage == "encrypt_cancel" {
		c.cancel()
	}
	return c.Cipher.Encrypt(p, a)
}
func (c *acceptanceDCipher) Decrypt(v string, p, n, a []byte) ([]byte, error) {
	if c.stage == "decrypt_panic" {
		panic("private injected secret")
	}
	if c.stage == "decrypt_cancel" {
		c.cancel()
	}
	return c.Cipher.Decrypt(v, p, n, a)
}

func TestHTTPM3B3DAdversarialCrypto(t *testing.T) {
	for _, stage := range []string{"key_panic", "encrypt_panic", "decrypt_panic", "encrypt_cancel", "decrypt_cancel"} {
		t.Run(stage, func(t *testing.T) {
			_, s, h, r, scope, _, w, signer, observer := localSigningFixture(t, "buy")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := w.cipher
			w.cipher = &acceptanceDCipher{Cipher: original, stage: stage, cancel: cancel}
			got, err := w.sign(ctx, h.identity, r.RequestID, scope)
			if err == nil || got != (manualLocalSigningResult{}) || strings.Contains(err.Error(), "private injected") || signer.calls.Load() != 1 || count(t, s.db, "manual_signing_intents") != 1 || count(t, s.db, "manual_signed_artifact_commands") != 0 {
				t.Fatal(got, err, signer.calls.Load())
			}
			if strings.Contains(stage, "cancel") && !errors.Is(err, context.Canceled) {
				t.Fatal("cancel lost", err)
			}
			w.cipher = original
			w.now = nil
			w.signer = nil
			w.observer = nil
			got, err = w.sign(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || !got.Duplicate || got.Status != "SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION" || signer.calls.Load() != 1 || observer.calls.Load() != 1 {
				t.Fatal("retry resumed", got, err)
			}
		})
	}
}

func TestHTTPM3B3DLateSynchronousSigner(t *testing.T) {
	_, s, h, r, scope, _, w, signer, _ := localSigningFixture(t, "buy")
	exited := false
	signer.invoke = func(ctx context.Context, tx *types.Transaction, chain *big.Int) (*types.Transaction, error) {
		<-ctx.Done()
		time.Sleep(25 * time.Millisecond)
		exited = true
		return signer.signer.SignTransaction(context.Background(), tx, chain)
	}
	got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
	if !errors.Is(err, context.DeadlineExceeded) || got != (manualLocalSigningResult{}) || !exited || signer.calls.Load() != 1 || count(t, s.db, "manual_signing_intents") != 1 || count(t, s.db, "manual_signed_artifact_commands") != 0 {
		t.Fatal(got, err, exited)
	}
}

func TestHTTPM3B3DCorruptHistoryNeverRepairs(t *testing.T) {
	for _, kind := range []string{"marker", "artifact"} {
		t.Run(kind, func(t *testing.T) {
			d, s, h, r, scope, path, w, signer, observer := localSigningFixture(t, "buy")
			if kind == "marker" {
				w.checkpoint = func(at string) error {
					if at == "after_marker_commit" {
						return errors.New("stop")
					}
					return nil
				}
			}
			_, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
			if (kind == "marker") != (err != nil) {
				t.Fatal(err)
			}
			if kind == "marker" {
				seedSigningWitnessCorruption(t, s, "manual_signing_intents", `UPDATE manual_signing_intents SET facts_hash=?`, strings.Repeat("0", 64))
			} else {
				seedSigningWitnessCorruption(t, s, "transaction_attempts", `UPDATE transaction_attempts SET encrypted_raw_tx=X'00'`)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			_, reopened := bindingReopen(t, d, path)
			fresh := &manualLocalSigningWriter{store: reopened, cipher: w.cipher}
			for i := 0; i < 2; i++ {
				got, err := fresh.sign(context.Background(), h.identity, r.RequestID, scope)
				if err == nil || got != (manualLocalSigningResult{}) || admittedDatabaseSnapshot(t, reopened.db) != before {
					t.Fatal(got, err)
				}
			}
			if observer.calls.Load() != 1 || signer.calls.Load() > 1 {
				t.Fatal("resumed", signer.calls.Load(), observer.calls.Load())
			}
		})
	}
}

// Characterization only: commit occurred while fresh; this is now durable
// false-permission artifact metadata, and the next call must be history.
func TestHTTPM3B3DPostCommitClockCharacterization(t *testing.T) {
	for _, kind := range []string{"expiry", "reversal"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, scope, _, w, signer, _ := localSigningFixture(t, "buy")
			clock := testNow
			w.now = func() time.Time { return clock }
			w.checkpoint = func(at string) error {
				if at == "after_artifact_commit" {
					if kind == "expiry" {
						clock, _ = canonicalManualTime(r.ExpiresAt)
					} else {
						clock = testNow.Add(-time.Nanosecond)
					}
				}
				return nil
			}
			got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || got.Status != "LOCAL_ARTIFACT_STORED_NOT_PERMISSION" || got.SigningAuthorized || got.ExecutionAuthorized || got.SendAuthorized || signer.calls.Load() != 1 || count(t, s.db, "manual_signed_artifact_commands") != 1 {
				t.Fatal(got, err)
			}
			t.Log("post-valid-commit clock changes are not sampled; metadata returned with all permissions false")
			w.now = nil
			w.observer = nil
			w.signer = nil
			w.checkpoint = nil
			got, err = w.sign(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || !got.Duplicate || got.Status != "HISTORICAL_VERIFIED_ARTIFACT_NOT_PERMISSION" {
				t.Fatal(got, err)
			}
		})
	}
}

type acceptanceDAddressClock struct {
	*localCountingSigner
	addresses int
	expire    func()
}

func (s *acceptanceDAddressClock) Address() common.Address {
	s.addresses++
	if s.addresses == 3 {
		s.expire()
	}
	return s.localCountingSigner.Address()
}
func TestHTTPM3B3DImmediateCallbackAfterSignerChecks(t *testing.T) {
	_, s, h, r, scope, _, w, signer, _ := localSigningFixture(t, "buy")
	clock := testNow
	w.now = func() time.Time { return clock }
	address := &acceptanceDAddressClock{localCountingSigner: signer, expire: func() { clock, _ = canonicalManualTime(r.ExpiresAt) }}
	w.signer = address
	got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
	if err == nil || got != (manualLocalSigningResult{}) || address.addresses != 3 || signer.calls.Load() != 0 || count(t, s.db, "manual_signing_intents") != 1 {
		t.Fatal(got, err, address.addresses, signer.calls.Load())
	}
}

func TestHTTPM3B3DSecondFenceAccountingDrift(t *testing.T) {
	for _, mutation := range []string{`UPDATE canary_risk_reservations SET state='frozen'`, `UPDATE canary_authorization_operation_usage SET state='FROZEN'`, `UPDATE canary_runtime_authorizations SET state='EXHAUSTED'`, `UPDATE dry_run_wallets SET enabled=0`} {
		t.Run(mutation, func(t *testing.T) {
			_, s, h, r, scope, path, w, signer, _ := localSigningFixture(t, "buy")
			d2, err := storage.Open(context.Background(), storage.TradeOwner, path)
			if err != nil {
				t.Fatal(err)
			}
			defer d2.Close()
			s2, err := NewStore(d2)
			if err != nil {
				t.Fatal(err)
			}
			w.checkpoint = func(at string) error {
				if at == "after_marker_commit" {
					_, err := s2.db.Exec(mutation)
					if err != nil {
						t.Fatal("fixture mutation failed", err)
					}
				}
				return nil
			}
			got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || got != (manualLocalSigningResult{}) || signer.calls.Load() != 0 || count(t, s.db, "manual_signing_intents") != 1 || count(t, s.db, "manual_signed_artifact_commands") != 0 {
				t.Fatal(got, err, signer.calls.Load())
			}
		})
	}
}

func TestHTTPM3B3DCancellationAfterDurableCommits(t *testing.T) {
	for _, stage := range []string{"after_marker_commit", "after_artifact_commit"} {
		t.Run(stage, func(t *testing.T) {
			_, s, h, r, scope, _, w, signer, observer := localSigningFixture(t, "buy")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w.checkpoint = func(at string) error {
				if at == stage {
					cancel()
				}
				return nil
			}
			got, err := w.sign(ctx, h.identity, r.RequestID, scope)
			if !errors.Is(err, context.Canceled) || got != (manualLocalSigningResult{}) || count(t, s.db, "manual_signing_intents") != 1 {
				t.Fatal(got, err)
			}
			calls := signer.calls.Load()
			want := "SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION"
			if stage == "after_artifact_commit" {
				want = "HISTORICAL_VERIFIED_ARTIFACT_NOT_PERMISSION"
				if calls != 1 {
					t.Fatal(calls)
				}
			} else if calls != 0 {
				t.Fatal(calls)
			}
			w.checkpoint = nil
			w.now = nil
			w.signer = nil
			w.observer = nil
			got, err = w.sign(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || got.Status != want || !got.Duplicate || signer.calls.Load() != calls || observer.calls.Load() != 1 {
				t.Fatal(got, err)
			}
		})
	}
}

func TestHTTPM3B3DOriginalBudgetEquality(t *testing.T) {
	_, _, _, _, _, _, w, signer, _ := localSigningFixture(t, "buy")
	_, s, h, r, scope, _ := manualInputsFixtureBudgetWallet(t, "buy", 1, "10000000000000000", strings.ToLower(signer.Address().Hex()))
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	w.store = s
	got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || got.Duplicate || signer.calls.Load() != 1 {
		t.Fatal(got, err)
	}
	assertArtifactPhase(t, s, r)
}

func TestHTTPM3B3DSecondFenceRetainedGateCorruption(t *testing.T) {
	for _, test := range []struct{ table, sql string }{
		{"canary_wallet_allowlist", `UPDATE canary_wallet_allowlist SET enabled=0`},
		{"canary_contract_allowlist", `UPDATE canary_contract_allowlist SET enabled=0`},
		{"canary_token_allowlist", `UPDATE canary_token_allowlist SET enabled=0`},
		{"canary_gate_attestations", `UPDATE canary_gate_attestations SET evidence_hash='0000000000000000000000000000000000000000000000000000000000000000' WHERE gate='C04'`},
		{"canary_runtime_authorizations", `UPDATE canary_runtime_authorizations SET epoch=epoch+1`},
		{"canary_runtime_authorizations", `UPDATE canary_runtime_authorizations SET deployment_id='wrong-deployment'`},
		{"canary_authorization_usage", `UPDATE canary_authorization_usage SET total_input='1'`},
	} {
		t.Run(test.table+test.sql, func(t *testing.T) {
			_, s, h, r, scope, path, w, signer, _ := localSigningFixture(t, "buy")
			d2, err := storage.Open(context.Background(), storage.TradeOwner, path)
			if err != nil {
				t.Fatal(err)
			}
			defer d2.Close()
			s2, err := NewStore(d2)
			if err != nil {
				t.Fatal(err)
			}
			w.checkpoint = func(at string) error {
				if at == "after_marker_commit" {
					seedSigningWitnessCorruption(t, s2, test.table, test.sql)
				}
				return nil
			}
			got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || got != (manualLocalSigningResult{}) || signer.calls.Load() != 0 || count(t, s.db, "manual_signing_intents") != 1 || count(t, s.db, "manual_signed_artifact_commands") != 0 {
				t.Fatal(got, err, signer.calls.Load())
			}
		})
	}
}

// A deferred FK forces the actual SQLite COMMIT to fail, rather than merely
// returning an injected callback error before COMMIT. All original schema
// guards stay intact; extra test-only tables/triggers exist in this fixture.
func TestHTTPM3B3DActualCommitFailureRetainsOriginalClaim(t *testing.T) {
	for _, phase := range []string{"marker", "artifact"} {
		t.Run(phase, func(t *testing.T) {
			d, s, h, r, scope, path, w, signer, observer := localSigningFixture(t, "buy")
			for _, statement := range []string{
				`CREATE TABLE acceptance_parent(id INTEGER PRIMARY KEY)`,
				`CREATE TABLE acceptance_commit_failure(id INTEGER REFERENCES acceptance_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
			} {
				if _, err := s.db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			table := "manual_signing_intents"
			if phase == "artifact" {
				table = "manual_signed_artifact_commands"
			}
			if _, err := s.db.Exec(`CREATE TRIGGER acceptance_commit_fault AFTER INSERT ON ` + table + ` BEGIN INSERT INTO acceptance_commit_failure VALUES(1); END`); err != nil {
				t.Fatal(err)
			}
			got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
			var sqliteError interface{ Code() int }
			if err == nil || !errors.As(err, &sqliteError) || sqliteError.Code() != 787 || got != (manualLocalSigningResult{}) {
				t.Fatal("expected deferred foreign-key COMMIT failure", got, err)
			}
			calls := int64(0)
			markers := 0
			if phase == "artifact" {
				calls = 1
				markers = 1
			}
			if signer.calls.Load() != calls || observer.calls.Load() != 1 || count(t, s.db, "manual_signing_intents") != markers || count(t, s.db, "manual_signed_artifact_commands") != 0 || count(t, s.db, "acceptance_commit_failure") != 0 {
				t.Fatal("failed COMMIT leaked effects or signing count", phase, signer.calls.Load())
			}
			assertNonceClaim(t, s, r, "23")
			if _, err := s.db.Exec(`DROP TRIGGER acceptance_commit_fault`); err != nil {
				t.Fatal(err)
			}
			_, reopened := bindingReopen(t, d, path)
			w.store = reopened
			before := admittedDatabaseSnapshot(t, reopened.db)
			got, err = w.sign(context.Background(), h.identity, r.RequestID, scope)
			if phase == "artifact" {
				if err != nil || !got.Duplicate || got.Status != "SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION" || signer.calls.Load() != 1 || observer.calls.Load() != 1 || admittedDatabaseSnapshot(t, reopened.db) != before {
					t.Fatal("artifact COMMIT failure retried signer", got, err)
				}
			} else {
				// No committed marker and no signer call: a later separately
				// requested invocation can sign the SAME original nonce once.
				if err != nil || got.Duplicate || signer.calls.Load() != 1 || observer.calls.Load() != 2 {
					t.Fatal("proven pre-invocation rollback corrupted original claim", got, err)
				}
				assertArtifactPhase(t, reopened, r)
			}
		})
	}
}

package tradeapi

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

type localCountingSigner struct {
	signer *trade.LocalSigner
	calls  atomic.Int64
	invoke func(context.Context, *types.Transaction, *big.Int) (*types.Transaction, error)
}

func (s *localCountingSigner) Address() common.Address { return s.signer.Address() }
func (s *localCountingSigner) SignTransaction(ctx context.Context, tx *types.Transaction, chain *big.Int) (*types.Transaction, error) {
	s.calls.Add(1)
	if s.invoke != nil {
		return s.invoke(ctx, tx, chain)
	}
	return s.signer.SignTransaction(ctx, tx, chain)
}

func localSigningFixture(t *testing.T, side string) (*storage.Database, *Store, *API, Record, manualAdmissionScope, string, *manualLocalSigningWriter, *localCountingSigner, *localNonceObserver) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	d, s, h, r, scope, path := manualInputsFixtureBudgetWallet(t, side, 10, "1000000000000000000", strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex()))
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	signer, err := trade.NewLocalSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := trade.NewAESGCMCipher("local-signing-fixture", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	counting := &localCountingSigner{signer: signer}
	o := &localNonceObserver{nonce: "23"}
	w := &manualLocalSigningWriter{store: s, observer: o, signer: counting, cipher: cipher, now: func() time.Time { return testNow }}
	return d, s, h, r, scope, path, w, counting, o
}

func TestHTTPM3B3CLocalExactSignHistoryAndReopen(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			d, s, h, r, scope, path, w, signer, observer := localSigningFixture(t, side)
			v, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || v.Status != "LOCAL_ARTIFACT_STORED_NOT_PERMISSION" || v.Duplicate || v.SigningAuthorized || v.SendAuthorized || v.ExecutionAuthorized || v.Artifact.TxHash == "" || signer.calls.Load() != 1 || observer.calls.Load() != 1 {
				t.Fatal("local first sign", v, err, signer.calls.Load(), observer.calls.Load())
			}
			assertArtifactPhase(t, s, r)
			if _, err := s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'; UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_authorization_operation_usage SET state='FROZEN'`); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			_, reopened := bindingReopen(t, d, path)
			// Fresh composition deliberately has no signer, observer or clock.
			fresh := &manualLocalSigningWriter{store: reopened, cipher: w.cipher}
			for i := 0; i < 2; i++ {
				got, err := fresh.sign(context.Background(), h.identity, r.RequestID, scope)
				if err != nil || !got.Duplicate || got.Artifact != v.Artifact || got.SigningAuthorized || got.SendAuthorized || got.ExecutionAuthorized || admittedDatabaseSnapshot(t, reopened.db) != before || signer.calls.Load() != 1 || observer.calls.Load() != 1 {
					t.Fatal("restart renewed signing", got, err)
				}
			}
		})
	}
}

func TestHTTPM3B3CLocalGateDenialsZeroSigning(t *testing.T) {
	for _, kind := range []string{"stop", "revoke", "scope", "expired", "clock_zero", "clock_reversed", "wrong_signer", "wrong_nonce", "stale_query", "runtime", "frozen_risk", "frozen_usage"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, scope, _, w, signer, observer := localSigningFixture(t, "buy")
			switch kind {
			case "stop":
				_, _ = s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`)
				scope.AuthorizationEpoch++ // stop must win over scope/clock faults
				w.now = func() time.Time { return time.Time{} }
			case "revoke":
				_, _ = s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`)
			case "scope":
				scope.DeploymentID = "different"
			case "expired":
				expiry, _ := canonicalManualTime(r.ExpiresAt)
				w.now = func() time.Time { return expiry }
			case "clock_zero":
				w.now = func() time.Time { return time.Time{} }
			case "clock_reversed":
				i := 0
				w.now = func() time.Time { i++; return testNow.Add(time.Duration(1-i) * time.Nanosecond) }
			case "wrong_signer":
				key, _ := crypto.GenerateKey()
				signer.signer, _ = trade.NewLocalSigner(key)
			case "wrong_nonce":
				observer.nonce = "24"
			case "stale_query":
				observer.change = func(o *manualnonce.Observation) {
					o.ObservedAt = testNow.Add(-3 * time.Second).Format(time.RFC3339Nano)
				}
			case "runtime":
				observer.change = func(o *manualnonce.Observation) { o.RuntimeCodeHash = "0x" + strings.Repeat("d", 64) }
			case "frozen_risk":
				_, _ = s.db.Exec(`UPDATE canary_risk_reservations SET state='frozen'`)
			case "frozen_usage":
				_, _ = s.db.Exec(`UPDATE canary_authorization_operation_usage SET state='FROZEN'`)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || got != (manualLocalSigningResult{}) || signer.calls.Load() != 0 || count(t, s.db, "manual_signing_intents") != 0 || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("denied action signed or mutated", kind, got, err, signer.calls.Load())
			}
			if kind == "stop" && !strings.Contains(err.Error(), "EMERGENCY_STOPPED") {
				t.Fatal("stop did not have priority", err)
			}
		})
	}
}

func TestHTTPM3B3CLocalFaultsNeverResign(t *testing.T) {
	for _, stage := range []string{"before_marker", "after_marker_commit", "after_fence", "before_sign", "after_sign", "before_artifact_insert", "after_artifact_insert", "before_artifact_commit", "after_artifact_commit"} {
		t.Run(stage, func(t *testing.T) {
			d, s, h, r, scope, path, w, signer, observer := localSigningFixture(t, "buy")
			fault := errors.New("controlled injected fault")
			w.checkpoint = func(at string) error {
				if at == stage {
					return fault
				}
				return nil
			}
			got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
			if !errors.Is(err, fault) || got != (manualLocalSigningResult{}) {
				t.Fatal("fault escaped", got, err)
			}
			calls := signer.calls.Load()
			wantCalls := int64(1)
			if stage == "before_marker" || stage == "after_marker_commit" || stage == "after_fence" || stage == "before_sign" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatal("fault window signer invocation count", stage, calls, wantCalls)
			}
			if stage == "before_marker" {
				if calls != 0 || count(t, s.db, "manual_signing_intents") != 0 {
					t.Fatal("pre-marker signed")
				}
				return
			}
			if count(t, s.db, "manual_signing_intents") != 1 {
				t.Fatal("lost durable ambiguity")
			}
			before := admittedDatabaseSnapshot(t, s.db)
			_, reopened := bindingReopen(t, d, path)
			w.store, w.checkpoint = reopened, nil
			got, err = w.sign(context.Background(), h.identity, r.RequestID, scope)
			want := "SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION"
			if stage == "after_artifact_commit" {
				want = "HISTORICAL_VERIFIED_ARTIFACT_NOT_PERMISSION"
			}
			if err != nil || !got.Duplicate || got.Status != want || signer.calls.Load() != calls || observer.calls.Load() != 1 || admittedDatabaseSnapshot(t, reopened.db) != before {
				t.Fatal("restart resumed signer", got, err)
			}
		})
	}
}

func TestHTTPM3B3CLocalTwoHandlesOneInvocation(t *testing.T) {
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
	start := make(chan struct{})
	var wg sync.WaitGroup
	var failed atomic.Int64
	var first, busy atomic.Int64
	for i := 0; i < 6; i++ {
		local := *w
		if i%2 == 1 {
			local.store = s2
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := local.sign(context.Background(), h.identity, r.RequestID, scope)
			if err != nil {
				var sqliteError interface{ Code() int }
				if errors.As(err, &sqliteError) && sqliteError.Code() == 5 && got == (manualLocalSigningResult{}) {
					// Real bounded writer contention is a fail-closed operational
					// rejection, not a reason to retry signer/effects internally.
					busy.Add(1)
					return
				}
				failed.Add(1)
				t.Errorf("unexpected parallel error: %v", err)
				return
			}
			if !got.Duplicate {
				first.Add(1)
			}
			if got.ExecutionAuthorized || got.SigningAuthorized || got.SendAuthorized {
				failed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if failed.Load() != 0 || first.Load() != 1 || signer.calls.Load() != 1 {
		t.Fatal("parallel invocation", failed.Load(), first.Load(), signer.calls.Load())
	}
	assertArtifactPhase(t, s, r)
	before := admittedDatabaseSnapshot(t, s.db)
	for i := 0; i < 6; i++ {
		got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
		if err != nil || !got.Duplicate || got.Status != "HISTORICAL_VERIFIED_ARTIFACT_NOT_PERMISSION" || signer.calls.Load() != 1 || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("serial history retry after contention changed outcome", got, err)
		}
	}
	t.Log("fail-closed SQLITE_BUSY outcomes", busy.Load(), "unexplained outcomes", failed.Load())
}

type localSigningFailCipher struct{ manualartifact.Cipher }

func (c localSigningFailCipher) Encrypt([]byte, []byte) ([]byte, []byte, error) {
	return nil, nil, errors.New("secret injected cipher error")
}

func TestHTTPM3B3CLocalPostInvocationFailureFrozen(t *testing.T) {
	for _, kind := range []string{"signer_error", "signer_panic", "wrong_transaction", "cipher_error", "cancel", "expiry", "clock_reversal", "evidence_age"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, scope, _, w, signer, observer := localSigningFixture(t, "buy")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			clock := testNow
			w.now = func() time.Time { return clock }
			if kind == "cipher_error" {
				w.cipher = localSigningFailCipher{w.cipher}
			}
			signer.invoke = func(ctx context.Context, tx *types.Transaction, chain *big.Int) (*types.Transaction, error) {
				switch kind {
				case "signer_error":
					return nil, errors.New("secret injected signer error")
				case "signer_panic":
					panic("secret injected panic")
				case "wrong_transaction":
					tx = types.NewTx(&types.DynamicFeeTx{ChainID: chain, Nonce: tx.Nonce() + 1, To: tx.To(), Value: tx.Value(), Data: tx.Data(), Gas: tx.Gas(), GasFeeCap: tx.GasFeeCap(), GasTipCap: tx.GasTipCap()})
				case "cancel":
					cancel()
				case "expiry":
					clock, _ = canonicalManualTime(r.ExpiresAt)
				case "clock_reversal":
					clock = testNow.Add(-time.Nanosecond)
				case "evidence_age":
					clock = testNow.Add(3 * time.Second)
				}
				return signer.signer.SignTransaction(ctx, tx, chain)
			}
			got, err := w.sign(ctx, h.identity, r.RequestID, scope)
			if err == nil || got != (manualLocalSigningResult{}) || strings.Contains(err.Error(), "secret injected") || signer.calls.Load() != 1 || count(t, s.db, "manual_signing_intents") != 1 || count(t, s.db, "manual_signed_artifact_commands") != 0 {
				t.Fatal("post invocation result escaped", kind, got, err, signer.calls.Load())
			}
			assertNonceClaim(t, s, r, "23")
			before := admittedDatabaseSnapshot(t, s.db)
			got, err = w.sign(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || got.Status != "SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION" || !got.Duplicate || signer.calls.Load() != 1 || observer.calls.Load() != 1 || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("post-failure resigned", got, err)
			}
		})
	}
}

func TestHTTPM3B3CLocalControlChangesBeforeFinalFence(t *testing.T) {
	for _, stage := range []string{"after_observation", "after_marker_commit"} {
		for _, change := range []string{"stop", "revoke", "expiry"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
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
				clock := testNow
				w.now = func() time.Time { return clock }
				w.checkpoint = func(at string) error {
					if at != stage {
						return nil
					}
					switch change {
					case "stop":
						_, err := s2.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`)
						return err
					case "revoke":
						_, err := s2.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`)
						return err
					default:
						clock, _ = canonicalManualTime(r.ExpiresAt)
						return nil
					}
				}
				got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
				if err == nil || got != (manualLocalSigningResult{}) || signer.calls.Load() != 0 || count(t, s.db, "manual_signed_artifact_commands") != 0 {
					t.Fatal("final fence missed control", got, err)
				}
				want := 0
				if stage == "after_marker_commit" {
					want = 1
				}
				if count(t, s.db, "manual_signing_intents") != want {
					t.Fatal("ambiguity marker lost")
				}
			})
		}
	}
}

func TestHTTPM3B3CLocalWriterFenceBlocksControlCommit(t *testing.T) {
	for _, change := range []string{"stop", "revoke"} {
		t.Run(change, func(t *testing.T) {
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
			conn, err := s2.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			// A real competing SQLite writer attempt, not a timing-only sleep.
			if _, err := conn.ExecContext(context.Background(), `PRAGMA busy_timeout=0`); err != nil {
				t.Fatal(err)
			}
			attempts := 0
			query := `UPDATE canary_control_state SET emergency_stopped=1`
			if change == "revoke" {
				query = `UPDATE canary_runtime_authorizations SET state='REVOKED'`
			}
			compete := func() error {
				attempts++
				// Run an actual mutation from a second goroutine/connection while
				// the first stack holds the sign/outcome fence. Require SQLITE_BUSY,
				// not just any error or a not-yet-scheduled competitor.
				done := make(chan error, 1)
				go func() { _, err := conn.ExecContext(context.Background(), query); done <- err }()
				err := <-done
				if err == nil || !strings.Contains(err.Error(), "SQLITE_BUSY") {
					return errors.New("competing control mutation not fenced by SQLite")
				}
				return nil
			}
			w.checkpoint = func(at string) error {
				if at == "before_artifact_commit" {
					return compete()
				}
				return nil
			}
			signer.invoke = func(ctx context.Context, tx *types.Transaction, chain *big.Int) (*types.Transaction, error) {
				if err := compete(); err != nil {
					return nil, err
				}
				return signer.signer.SignTransaction(ctx, tx, chain)
			}
			got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || got.Artifact.TxHash == "" || attempts != 2 || signer.calls.Load() != 1 {
				t.Fatal("protected writer failed", got, err, attempts)
			}
			// The same writer succeeds once signing/outcome commit releases fence.
			if _, err := conn.ExecContext(context.Background(), query); err != nil {
				t.Fatal("control writer never released", err)
			}
			assertArtifactPhase(t, s, r)
		})
	}
}

func TestHTTPM3B3CLocalDeadlineAwaitsSignerAndRetainsAmbiguity(t *testing.T) {
	_, s, h, r, scope, _, w, signer, _ := localSigningFixture(t, "buy")
	exited := false
	signer.invoke = func(ctx context.Context, _ *types.Transaction, _ *big.Int) (*types.Transaction, error) {
		<-ctx.Done()
		exited = true
		return nil, ctx.Err()
	}
	got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
	if !errors.Is(err, context.DeadlineExceeded) || got != (manualLocalSigningResult{}) || !exited || signer.calls.Load() != 1 || count(t, s.db, "manual_signing_intents") != 1 || count(t, s.db, "manual_signed_artifact_commands") != 0 {
		t.Fatal("timeout released live signer or returned artifact", got, err, exited)
	}
	assertNonceClaim(t, s, r, "23")
	got, err = w.sign(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || got.Status != "SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION" || signer.calls.Load() != 1 {
		t.Fatal("deadline retry resigned", got, err)
	}
}

func TestHTTPM3B3CLocalBoundaryExpiryNeverReturnsArtifact(t *testing.T) {
	for _, stage := range []string{"before_sign", "after_sign", "before_artifact_insert", "after_artifact_insert", "before_artifact_commit"} {
		t.Run(stage, func(t *testing.T) {
			_, s, h, r, scope, _, w, signer, _ := localSigningFixture(t, "buy")
			clock := testNow
			w.now = func() time.Time { return clock }
			w.checkpoint = func(at string) error {
				if at == stage {
					clock, _ = canonicalManualTime(r.ExpiresAt)
				}
				return nil
			}
			got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
			wantCalls := int64(1)
			if stage == "before_sign" {
				wantCalls = 0
			}
			if err == nil || got != (manualLocalSigningResult{}) || signer.calls.Load() != wantCalls || count(t, s.db, "manual_signing_intents") != 1 || count(t, s.db, "manual_signed_artifact_commands") != 0 {
				t.Fatal("expiry boundary returned artifact", stage, got, err, signer.calls.Load())
			}
			assertNonceClaim(t, s, r, "23")
			before := admittedDatabaseSnapshot(t, s.db)
			w.checkpoint = nil
			got, err = w.sign(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || got.Status != "SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION" || signer.calls.Load() != wantCalls || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("expiry retry refreshed window", stage, got, err)
			}
		})
	}
}

func TestHTTPM3B3CLocalCompositionDisconnected(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			// The accepted A3 bundle is the sole new private dependency owner.
			if id, ok := n.(*ast.Ident); ok && id.Name == "manualLocalSigningWriter" && name != "manual_local_signing.go" && name != "manual_trust_composition.go" {
				t.Errorf("local writer wired into %s", name)
			}
			return true
		})
	}
	for _, name := range []string{"manual_local_signing.go", "../manualsign/sign.go"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"SubmissionService", "RawBroadcaster", "SendRawTransaction", "ReplayUnknown", "ReserveExecution", "ConsumeCanaryPermit", "ethclient", "crypto/ecdsa", "net/http"} {
			if bytes.Contains(raw, []byte(forbidden)) {
				t.Fatal("prohibited composition dependency", name, forbidden)
			}
		}
	}
}

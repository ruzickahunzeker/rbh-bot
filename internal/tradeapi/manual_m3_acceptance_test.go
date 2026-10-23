package tradeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualsign"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

func m3AcceptanceNoPermissions(t *testing.T, got manualLocalSigningResult) {
	t.Helper()
	if got.ExecutionAuthorized || got.SigningAuthorized || got.SendAuthorized || got.Artifact.ExecutionAuthorized || got.Artifact.SigningAuthorized || got.Artifact.SendAuthorized {
		t.Fatal("historical metadata acquired action permission", got)
	}
}

// Exercise original intake/admission -> M1 -> M2 -> B1 -> B2 -> B3 on one
// real SQLite file. Reopen at every coordination phase; never normalize an
// old initial-only reader into permission for a later phase.
func TestHTTPM3B4CrossStageReopenAndHistory(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			ctx := context.Background()
			key, err := crypto.GenerateKey()
			if err != nil {
				t.Fatal(err)
			}
			d, s, h, r, scope, path := manualInputsFixtureBudgetWallet(t, side, 1, "1000000000000000000", strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex()))
			if _, err := atomicWriter(s).admit(ctx, h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			admitted, err := admittedInputReader(t, s, scope).Load(ctx, h.identity, r.RequestID)
			if err != nil || admitted.ExecutionAuthorized || admitted.SendAuthorized || admitted.Contract.Output != r.Validation.Output {
				t.Fatal("M1 original candidate", admitted, err)
			}
			originalContract := encode(admitted.Contract)
			if bound, err := bindingWriter(s).bind(ctx, h.identity, r.RequestID, scope); err != nil || bound.Duplicate || bound.ExecutionAuthorized || bound.SendAuthorized {
				t.Fatal("M2 original binding", bound, err)
			}
			candidate, err := boundSigningReader(t, s, scope).Load(ctx, h.identity, r.RequestID)
			if err != nil || encode(candidate.Contract) != originalContract || candidate.ExecutionAuthorized || candidate.SigningAuthorized || candidate.SendAuthorized || candidate.Envelope.GasTipCap != "0" {
				t.Fatal("B1 original candidate", candidate, err)
			}
			reopen := func() {
				t.Helper()
				before := admittedDatabaseSnapshot(t, s.db)
				d, s = bindingReopen(t, d, path)
				h.store, h.validator.store = s, s
				if admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("reopen changed original durable state")
				}
			}
			reopen()
			loaded, err := boundSigningReader(t, s, scope).Load(ctx, h.identity, r.RequestID)
			if err != nil || !reflect.DeepEqual(loaded, candidate) {
				t.Fatal("bound reopen changed immutable input", err)
			}
			observer := &localNonceObserver{nonce: "23"}
			claim, err := nonceWriter(s, observer).claim(ctx, h.identity, r.RequestID, scope)
			if err != nil || claim.Nonce != "23" || claim.ExpiresAt != r.ExpiresAt || claim.ExecutionAuthorized || claim.SigningAuthorized || claim.SendAuthorized {
				t.Fatal("B2 original claim", claim, err)
			}
			assertNonceClaim(t, s, r, "23")
			reopen()
			before := admittedDatabaseSnapshot(t, s.db)
			requireBoundInputDenied(t, boundSigningReader(t, s, scope), h.identity, r.RequestID)
			if _, err := admittedInputReader(t, s, scope).Load(ctx, h.identity, r.RequestID); err == nil {
				t.Fatal("claimed phase regained initial M1 candidate")
			}
			duplicate, err := nonceWriter(s, observer).claim(ctx, h.identity, r.RequestID, scope)
			if err != nil || !duplicate.Duplicate || duplicate.Nonce != claim.Nonce || duplicate.ExpiresAt != claim.ExpiresAt || duplicate.ExecutionAuthorized || duplicate.SigningAuthorized || duplicate.SendAuthorized || observer.calls.Load() != 1 || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("claimed reopen renewed nonce/expiry/accounting", duplicate, err)
			}
			signer, err := trade.NewLocalSigner(key)
			if err != nil {
				t.Fatal(err)
			}
			counting := &localCountingSigner{signer: signer}
			cipher, err := trade.NewAESGCMCipher("m3-b4-fixture", bytes.Repeat([]byte{9}, 32))
			if err != nil {
				t.Fatal(err)
			}
			w := &manualLocalSigningWriter{store: s, observer: observer, signer: counting, cipher: cipher, now: func() time.Time { return testNow }}
			signed, err := w.sign(ctx, h.identity, r.RequestID, scope)
			if err != nil || signed.Duplicate || signed.Artifact.TxHash == "" || counting.calls.Load() != 1 || observer.calls.Load() != 2 {
				t.Fatal("B3 one original invocation", signed, err)
			}
			m3AcceptanceNoPermissions(t, signed)
			assertArtifactPhase(t, s, r)
			reopen()
			before = admittedDatabaseSnapshot(t, s.db)
			// A fresh historical composition has no observer, signer or clock.
			fresh := &manualLocalSigningWriter{store: s, cipher: cipher}
			for i := 0; i < 3; i++ {
				got, err := fresh.sign(ctx, h.identity, r.RequestID, scope)
				if err != nil || !got.Duplicate || got.Artifact != signed.Artifact {
					t.Fatal("signed reopen lost exact artifact", got, err)
				}
				m3AcceptanceNoPermissions(t, got)
			}
			if old, err := nonceWriter(s, observer).claim(ctx, h.identity, r.RequestID, scope); err == nil || old != (manualNonceClaimResult{}) {
				t.Fatal("old B2 resumed signed phase", old, err)
			}
			requireBoundInputDenied(t, boundSigningReader(t, s, scope), h.identity, r.RequestID)
			if old, err := bindingWriter(s).bind(ctx, h.identity, r.RequestID, scope); err == nil || old != (manualExecutionBindingResult{}) {
				t.Fatal("old M2 resumed signed phase", old, err)
			}
			legacy, err := trade.NewStore(d)
			if err != nil {
				t.Fatal(err)
			}
			if work, err := legacy.Recoverable(ctx); err != nil || len(work) != 0 {
				t.Fatal("MANUAL work entered legacy dry-run worker", work, err)
			}
			if work, err := legacy.ListControlledRecoveryItems(ctx); err != nil || len(work) != 0 {
				t.Fatal("local-only artifact entered submission recovery", work, err)
			}
			var retained string
			if err := s.db.QueryRow(`SELECT contract_json FROM manual_admission_commands`).Scan(&retained); err != nil || retained != originalContract || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("cross-stage history rewrote original contract/accounting", err)
			}
			backend := h.validator.backend.(*validationBackend)
			if backend.resolves.Load() != 1 || backend.completes.Load() != 1 || counting.calls.Load() != 1 || observer.calls.Load() != 2 {
				t.Fatal("cross-stage requote/resimulation/second signature/query")
			}
			assertArtifactPhase(t, s, r)
		})
	}
}

func TestHTTPM3B4SignedHTTPHistoryDoesNotWireSigning(t *testing.T) {
	_, s, h, r, scope, _, w, signer, observer := localSigningFixture(t, "buy")
	if _, err := w.sign(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'; UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_authorization_operation_usage SET state='FROZEN'`); err != nil {
		t.Fatal(err)
	}
	expired, err := canonicalManualTime(r.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	h.now = func() time.Time { return expired.Add(time.Hour) }
	before := admittedDatabaseSnapshot(t, s.db)
	backend := h.validator.backend.(*validationBackend)
	resolves, completes := backend.resolves.Load(), backend.completes.Load()
	for _, request := range []struct{ method, path, key, body string }{
		{"GET", "/v1/trades/" + r.RequestID, "", ""},
		{"POST", "/v1/trades", "b2-inputs", encode(r.Request)},
	} {
		response := call(h, request.method, request.path, request.key, request.body)
		wantCode := http.StatusOK
		if request.method == "POST" {
			wantCode = http.StatusAccepted
		}
		var got Record
		if response.Code != wantCode || json.Unmarshal(response.Body.Bytes(), &got) != nil || got.RequestID != r.RequestID || got.ExpiresAt != r.ExpiresAt || got.SendAuthorized || got.SubmissionQueued || got.ContractDeadline || got.DeadlineCapability != "APPLICATION_TTL_ONLY" || got.Admission == nil || got.Admission.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" || got.Admission.ExecutionAuthorized || got.Admission.SendAuthorized {
			t.Fatal("HTTP history acquired economic permission", response.Code, response.Body.String())
		}
		if request.method == "POST" && !got.Duplicate {
			t.Fatal("POST minted a second request")
		}
		for _, forbidden := range []string{"ciphertext", "encryption_nonce", "owner_id", "signed_raw_transaction"} {
			if strings.Contains(response.Body.String(), `"`+forbidden+`"`) {
				t.Fatal("HTTP exposed private signing material", forbidden)
			}
		}
	}
	if signer.calls.Load() != 1 || observer.calls.Load() != 1 || backend.resolves.Load() != resolves || backend.completes.Load() != completes || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("HTTP history caused revalidation/sign/query/durable mutation")
	}
	assertArtifactPhase(t, s, r)
}

// Promoted from the fresh cumulative review overlay, SHA256
// f115da614568144d408d1396301f87afcdc6f45c10aa47208254fb5bc3829471.
// These permanent author-maintained regressions are NOT a new independent review.
func TestHTTPM3B4OwnerIsNotResumeAuthority(t *testing.T) {
	_, s, h, r, scope, path, w, signer, observer := localSigningFixture(t, "buy")
	db2, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	s2, err := NewStore(db2)
	if err != nil {
		t.Fatal(err)
	}
	checked := false
	w.checkpoint = func(stage string) error {
		if stage != "after_marker_commit" {
			return nil
		}
		var owner string
		if err := s2.db.QueryRow(`SELECT owner_id FROM manual_signing_intents WHERE operation_id=?`, *r.OperationID).Scan(&owner); err != nil {
			return err
		}
		if len(owner) != 64 {
			t.Fatal("missing original owner")
		}
		other := *w
		other.store, other.checkpoint = s2, nil
		before := admittedDatabaseSnapshot(t, s2.db)
		got, err := other.sign(context.Background(), h.identity, r.RequestID, scope)
		if err != nil || !got.Duplicate || got.Status != "SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION" || signer.calls.Load() != 0 || observer.calls.Load() != 1 || admittedDatabaseSnapshot(t, s2.db) != before {
			t.Fatal("owner history resumed first invocation", got, err)
		}
		m3AcceptanceNoPermissions(t, got)
		checked = true
		return nil
	}
	got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || !checked || got.Duplicate || signer.calls.Load() != 1 || observer.calls.Load() != 1 {
		t.Fatal("first owner lost sole invocation", got, err)
	}
	m3AcceptanceNoPermissions(t, got)
	assertArtifactPhase(t, s, r)
}

type m3AcceptanceLateObserver struct{ original manualNonceObserver }

func (o m3AcceptanceLateObserver) ObserveManualNonce(ctx context.Context, f manualnonce.Facts) (manualnonce.Observation, error) {
	value, err := o.original.ObserveManualNonce(context.Background(), f)
	<-ctx.Done()
	return value, err
}

func TestHTTPM3B4LateValidObservationRejected(t *testing.T) {
	_, s, h, r, scope, _, w, signer, observer := localSigningFixture(t, "buy")
	w.observer = m3AcceptanceLateObserver{observer}
	before := admittedDatabaseSnapshot(t, s.db)
	got, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
	if !errors.Is(err, context.DeadlineExceeded) || got != (manualLocalSigningResult{}) || signer.calls.Load() != 0 || observer.calls.Load() != 1 || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("late query granted marker or invocation", got, err)
	}
	assertNonceClaim(t, s, r, "23")
}

func TestHTTPM3B4FinalCallbackCancellationPreventsCall(t *testing.T) {
	_, s, h, r, scope, _, w, signer, _ := localSigningFixture(t, "buy")
	facts, err := loadManualArtifactFacts(context.Background(), s.db, h.identity, r.RequestID, scope)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before := admittedDatabaseSnapshot(t, s.db)
	got, err := manualsign.SignExact(ctx, facts, signer, w.cipher, func() error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) || got.FactsHash != "" || len(got.Ciphertext) != 0 || signer.calls.Load() != 0 || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("canceled final callback invoked signer", got, err)
	}
}

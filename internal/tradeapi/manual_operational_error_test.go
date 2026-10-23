package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

// Faults affect a single actual SQLite read after earlier reads have succeeded.
// This is a verifier -> HTTP mapper integration harness, not an end-to-end
// timed HTTP cancellation claim, and adds no production checkpoint/interface.
type operationalFaultQuery struct {
	db      *sql.DB
	queries int
	failAt  int
	fault   string
	closed  *sql.DB
}

func (q *operationalFaultQuery) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	q.queries++
	if q.queries == q.failAt {
		switch q.fault {
		case "cancel":
			faultCtx, cancel := context.WithCancel(ctx)
			cancel()
			return q.db.QueryRowContext(faultCtx, query, args...)
		case "deadline":
			faultCtx, cancel := context.WithDeadline(ctx, time.Unix(0, 0))
			defer cancel()
			return q.db.QueryRowContext(faultCtx, query, args...)
		case "closed":
			return q.closed.QueryRowContext(ctx, query, args...)
		default:
			return q.db.QueryRowContext(ctx, `SELECT * FROM secret_operational_fault`)
		}
	}
	return q.db.QueryRowContext(ctx, query, args...)
}

func requireOperational503(t *testing.T, err error) {
	t.Helper()
	var input *manualInputError
	if err == nil || errors.As(err, &input) {
		t.Fatalf("operational cause lost: %v", err)
	}
	w := httptest.NewRecorder()
	writeError(w, httptest.NewRequest("GET", "/v1/trades/test", nil), err)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"API_UNAVAILABLE"`) || strings.Contains(w.Body.String(), "secret_operational_fault") || strings.Contains(w.Body.String(), err.Error()) {
		t.Fatalf("unsanitized or misclassified operational response: %d %s", w.Code, w.Body.String())
	}
}

func TestHTTPC3CHardeningEveryHistoricalReadPreservesOperationalError(t *testing.T) {
	_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
	h := admissionHTTP(t, s, original, scope, &validationBackend{})
	r := admissionRecord(t, call(h, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202)
	cmd, err := scanManualCommand(s.db.QueryRow(`SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE request_id=?`, source.RequestID))
	if err != nil {
		t.Fatal(err)
	}
	closed, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err = closed.Close(); err != nil {
		t.Fatal(err)
	}
	unit := manualrisk.Unit{ChainID: 4663, Asset: "native", Decimals: 18, Kind: manualrisk.TradeInput}
	checks := map[string]func(queryRow) error{
		"command": func(q queryRow) error {
			_, e := verifyManualCommandTx(context.Background(), q, cmd)
			return e
		},
		"usage": func(q queryRow) error {
			_, _, e := verifyManualAuthorizationUsage(context.Background(), q, scope.AuthorizationID, unit)
			return e
		},
	}
	for name, check := range checks {
		counter := &operationalFaultQuery{db: s.db}
		if err := check(counter); err != nil || counter.queries < 2 {
			t.Fatal("invalid positive fixture", name, err, counter.queries)
		}
		for _, fault := range []string{"cancel", "deadline", "closed", "query"} {
			for nth := 1; nth <= counter.queries; nth++ {
				t.Run(fmt.Sprintf("%s/%s/read_%d", name, fault, nth), func(t *testing.T) {
					q := &operationalFaultQuery{db: s.db, failAt: nth, fault: fault, closed: closed}
					err := check(q)
					requireOperational503(t, err)
					if fault == "cancel" && !errors.Is(err, context.Canceled) || fault == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal("context cause not preserved", err)
					}
					assertAtomicAdmission(t, s, r, 1)
				})
			}
		}
	}
	// Reads still expose complete history after faults; no rollback/refund/new TTL.
	history := admissionRecord(t, call(h, "GET", "/v1/trades/"+r.RequestID, "", ""), 200)
	if history.Admission.ContractHash != r.Admission.ContractHash || history.ExpiresAt != r.ExpiresAt || history.Admission.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" {
		t.Fatal("history changed after operational fault")
	}
}

func TestHTTPC3CHardeningVerifierCancellationRegression(t *testing.T) {
	_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
	h := admissionHTTP(t, s, original, scope, &validationBackend{})
	r := admissionRecord(t, call(h, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202)
	cmd, err := scanManualCommand(s.db.QueryRow(`SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE request_id=?`, source.RequestID))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = verifyManualCommandTx(ctx, s.db, cmd)
	requireOperational503(t, err)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	assertAtomicAdmission(t, s, r, 1)
}

func TestHTTPC3CHardeningAdmissionInputsAndExposureOperationalErrors(t *testing.T) {
	_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
	h := admissionHTTP(t, s, original, scope, &validationBackend{})
	r := admissionRecord(t, call(h, "POST", "/v1/trades", "b2-inputs", encode(source.Request)), 202)
	cmd, err := scanManualCommand(s.db.QueryRow(`SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE request_id=?`, source.RequestID))
	if err != nil {
		t.Fatal(err)
	}
	c, err := verifyManualCommandTx(context.Background(), s.db, cmd)
	if err != nil {
		t.Fatal(err)
	}
	p, digest, err := loadManualPolicy(context.Background(), s.db, scope.ManualPolicyID, c, original.now())
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]func(queryRow) error{
		"control": func(q queryRow) error { _, e := readManualControl(context.Background(), q, original.now()); return e },
		"authorization": func(q queryRow) error {
			_, e := loadManualAuthorizationIdentity(context.Background(), q, scope, c, p, digest, cmd.GateHash, original.now())
			return e
		},
	}
	for name, check := range checks {
		counter := &operationalFaultQuery{db: s.db}
		if err := check(counter); err != nil {
			t.Fatal(err)
		}
		for nth := 1; nth <= counter.queries; nth++ {
			t.Run(fmt.Sprintf("%s/read_%d", name, nth), func(t *testing.T) {
				requireOperational503(t, check(&operationalFaultQuery{db: s.db, failAt: nth, fault: "query"}))
			})
		}
	}
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = loadManualExposureTx(ctx, conn, original.identity.WalletID, p, original.now())
	requireOperational503(t, err)
	_, _, err = verifyManualExposureRow(ctx, conn, manualExposureRow{Operation: cmd.Operation}, original.now())
	requireOperational503(t, err)
	// Release the connection before checking ledger state (single DB pool).
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	assertAtomicAdmission(t, s, r, 1)
}

func TestHTTPC3CHardeningVerifiedRejectionsRemainTyped(t *testing.T) {
	for _, err := range []error{nil, sql.ErrNoRows, ErrInvalid, ErrConflict, ErrNotFound, ErrPolicy, ErrWallet, rejectManualInput("GATE_ATTESTATION_INVALID")} {
		var input *manualInputError
		got := manualVerificationError(err, "ADMISSION_CONTRACT_INVALID")
		if !errors.As(got, &input) || input.Code != "ADMISSION_CONTRACT_INVALID" {
			t.Fatal("verified rejection lost", got)
		}
		w := httptest.NewRecorder()
		writeError(w, httptest.NewRequest("GET", "/v1/trades/test", nil), got)
		if w.Code != 422 || !strings.Contains(w.Body.String(), `"code":"MANUAL_ADMISSION_REJECTED"`) {
			t.Fatal("invalid data reported as operational", w.Body.String())
		}
	}
}

func TestHTTPC3CHardeningWriterOperationalFaultRollsBack(t *testing.T) {
	for _, stage := range []string{"after_command", "before_commit"} {
		for _, fault := range []string{"cancel", "closed"} {
			t.Run(stage+"/"+fault, func(t *testing.T) {
				_, s, original, source, scope, _ := manualInputsFixture(t, "buy")
				closed, err := sql.Open("sqlite", ":memory:")
				if err != nil {
					t.Fatal(err)
				}
				if err = closed.Close(); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				w := atomicWriter(s)
				reached := false
				w.checkpoint = func(at string) error {
					if at != stage {
						return nil
					}
					reached = true
					if fault == "cancel" {
						cancel()
						return nil
					}
					var n int
					return closed.QueryRowContext(ctx, "SELECT 1").Scan(&n)
				}
				_, err = w.admit(ctx, original.identity, source.RequestID, scope)
				if !reached {
					t.Fatal("fault did not reach transaction checkpoint", err)
				}
				requireOperational503(t, err)
				assertAtomicAdmission(t, s, source, 0)
				w.checkpoint = nil
				if _, err := w.admit(context.Background(), original.identity, source.RequestID, scope); err != nil {
					t.Fatal("rollback lost original durable work", err)
				}
				assertAtomicAdmission(t, s, source, 1)
			})
		}
	}
}

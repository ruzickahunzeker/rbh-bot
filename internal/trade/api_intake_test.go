package trade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/health"
	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"modernc.org/sqlite"
)

func tradeAPIFixture(t *testing.T) (*Store, *DisabledTradeAPI, SubmissionIntakeRequest, time.Time) {
	t.Helper()
	s, closeDB := openTradeStore(t)
	t.Cleanup(closeDB)
	request := buyRequest()
	expiry, err := parseCanonicalExpiry(request.Intent.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	now := expiry.Add(-time.Minute)
	if err := s.RegisterDryRunWallet(context.Background(), request.WalletID, testWallet); err != nil {
		t.Fatal(err)
	}
	engine, _ := NewEngine(s, newFakeBackend())
	engine.now = func() time.Time { return now }
	result, err := engine.DryRun(context.Background(), request)
	if err != nil || result.Status != "success" {
		t.Fatalf("fixture dry-run=%+v err=%v", result, err)
	}
	h, err := NewDisabledTradeAPI(s, request.WalletID)
	if err != nil {
		t.Fatal(err)
	}
	h.now = func() time.Time { return now }
	return s, h, SubmissionIntakeRequest{WalletID: request.WalletID, IdempotencyKey: "api-1", PolicyVersion: request.Intent.PolicyVersion, ExpiresAt: request.Intent.ExpiresAt}, now
}

func tradeAPIHTTP(t *testing.T, h *DisabledTradeAPI, method, path, body string, signed bool) *httptest.ResponseRecorder {
	t.Helper()
	auth, err := ipc.NewAuthenticator([]byte(strings.Repeat("a", 32)), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if signed {
		if err := auth.Sign(r, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	ipc.RequestContext(auth.Middleware(h)).ServeHTTP(w, r)
	return w
}

func encodeIntake(t *testing.T, request SubmissionIntakeRequest) string {
	t.Helper()
	b, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTradeAPIDisabledSubmissionDurableAtomicRejection(t *testing.T) {
	s, h, request, _ := tradeAPIFixture(t)
	path := "/internal/trade/operations/intent-buy/submission-requests"
	w := tradeAPIHTTP(t, h, "POST", path, encodeIntake(t, request), true)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"reason_code":"SUBMISSION_DISABLED"`) || strings.Contains(w.Body.String(), `"submission_queued":true`) || strings.Contains(w.Body.String(), `"send_authorized":true`) {
		t.Fatalf("disabled response=%d %s", w.Code, w.Body)
	}
	var record SubmissionIntakeRecord
	if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil || record.Duplicate {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	expiry, _ := parseCanonicalExpiry(request.ExpiresAt)
	h.now = func() time.Time { return expiry.Add(time.Hour) }
	w = tradeAPIHTTP(t, h, "POST", path, encodeIntake(t, request), true)
	var duplicate SubmissionIntakeRecord
	if err := json.Unmarshal(w.Body.Bytes(), &duplicate); err != nil || w.Code != 503 || !duplicate.Duplicate || duplicate.ID != record.ID || duplicate.CreatedAt != record.CreatedAt || duplicate.ExpiresAt != record.ExpiresAt {
		t.Fatalf("duplicate=%+v err=%v body=%s", duplicate, err, w.Body)
	}
	for _, table := range []string{"trade_api_submission_requests", "canary_runtime_audit", "canary_alert_outbox"} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	assertTradeAPINoEconomicWrites(t, s)
}

func TestTradeAPIValidationAndAuthenticationFailClosed(t *testing.T) {
	s, h, request, _ := tradeAPIFixture(t)
	path := "/internal/trade/operations/intent-buy/submission-requests"
	for _, tc := range []struct {
		name, path, body string
		signed           bool
		status           int
	}{
		{"unauthenticated", path, encodeIntake(t, request), false, 401},
		{"unknown_fields", path, strings.TrimSuffix(encodeIntake(t, request), "}") + `,"raw_tx":"0xdead"}`, true, 400},
		{"caller_control", path, strings.TrimSuffix(encodeIntake(t, request), "}") + `,"send_authorized":true}`, true, 400},
		{"trailing_json", path, encodeIntake(t, request) + `{}`, true, 400},
		{"duplicate_key", path, strings.TrimSuffix(encodeIntake(t, request), "}") + `,"wallet_id":"wallet-1"}`, true, 400},
		{"case_alias_key", path, strings.TrimSuffix(encodeIntake(t, request), "}") + `,"WALLET_ID":"wallet-1"}`, true, 400},
		{"query_bypass", path + "?enable=true", encodeIntake(t, request), true, 400},
		{"missing_operation", "/internal/trade/operations/not-found/submission-requests", encodeIntake(t, request), true, 404},
		{"oversized", path, strings.Repeat(" ", (1<<20)+1), true, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := tradeAPIHTTP(t, h, "POST", tc.path, tc.body, tc.signed)
			if w.Code != tc.status {
				t.Fatalf("response=%d %s", w.Code, w.Body)
			}
		})
	}
	other := request
	other.WalletID = "wallet-other"
	if w := tradeAPIHTTP(t, h, "POST", path, encodeIntake(t, other), true); w.Code != 403 {
		t.Fatalf("wallet spoof response=%d", w.Code)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM trade_api_submission_requests`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid requests persisted=%d err=%v", count, err)
	}
	assertTradeAPINoEconomicWrites(t, s)
}

func TestTradeAPIExpiryAndIdentityConflict(t *testing.T) {
	s, _, request, now := tradeAPIFixture(t)
	ctx := context.Background()
	expiry, _ := parseCanonicalExpiry(request.ExpiresAt)
	if _, err := s.RecordDisabledSubmissionIntake(ctx, "intent-buy", request, expiry); !errors.Is(err, ErrTTLExpired) {
		t.Fatalf("boundary expiry: %v", err)
	}
	for _, malformed := range []string{"", "invalid", expiry.Format("2006-01-02T15:04:05.000000000Z"), expiry.Format("2006-01-02T15:04:05+00:00")} {
		r := request
		r.ExpiresAt = malformed
		if _, err := s.RecordDisabledSubmissionIntake(ctx, "intent-buy", r, now); !errors.Is(err, ErrTTLUnverifiable) {
			t.Fatalf("malformed %q: %v", malformed, err)
		}
	}
	if _, err := s.RecordDisabledSubmissionIntake(ctx, "intent-buy", request, time.Time{}); !errors.Is(err, ErrTTLUnverifiable) {
		t.Fatal(err)
	}
	if _, err := s.RecordDisabledSubmissionIntake(ctx, "intent-buy", request, now); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"expiry", "policy", "operation"} {
		r, operation := request, "intent-buy"
		switch kind {
		case "expiry":
			r.ExpiresAt = expiry.Add(time.Second).Format(time.RFC3339Nano)
		case "policy":
			r.PolicyVersion++
		case "operation":
			operation = "other-operation"
		}
		if _, err := s.RecordDisabledSubmissionIntake(ctx, operation, r, now); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("%s conflict: %v", kind, err)
		}
		r.IdempotencyKey += "-fresh"
		if _, err := s.RecordDisabledSubmissionIntake(ctx, operation, r, now); err == nil {
			t.Fatalf("%s fresh key bypassed binding", kind)
		}
	}
	assertTradeAPINoEconomicWrites(t, s)
}

func TestTradeAPISchemaAtomicAuditFailureAndImmutability(t *testing.T) {
	for _, target := range []string{"canary_runtime_audit", "canary_alert_outbox"} {
		t.Run(target, func(t *testing.T) {
			s, _, r, now := tradeAPIFixture(t)
			if _, err := s.db.Exec(`CREATE TRIGGER api_test_fail BEFORE INSERT ON ` + target + ` BEGIN SELECT RAISE(ABORT,'forced audit/outbox failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RecordDisabledSubmissionIntake(context.Background(), "intent-buy", r, now); err == nil {
				t.Fatal("insert succeeded without audit/outbox")
			}
			for _, table := range []string{"trade_api_submission_requests", "canary_runtime_audit", "canary_alert_outbox"} {
				var count int
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("partial %s commit=%d err=%v", table, count, err)
				}
			}
		})
	}
	s, _, r, now := tradeAPIFixture(t)
	if _, err := s.RecordDisabledSubmissionIntake(context.Background(), "intent-buy", r, now); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{`UPDATE trade_api_submission_requests SET outcome='QUEUED'`, `UPDATE trade_api_submission_requests SET created_at='changed'`, `DELETE FROM trade_api_submission_requests`} {
		if _, err := s.db.Exec(mutation); err == nil {
			t.Fatalf("SQL mutation allowed: %s", mutation)
		}
	}
	// Valid direct insert gets the same schema-owned exactly-once audit/outbox.
	if _, err := s.db.Exec(`INSERT INTO trade_api_submission_requests(id,operation_id,chain_id,wallet_id,idempotency_key,request_fingerprint,operation_fingerprint,policy_version,expires_at,outcome,reason_code,created_at) SELECT 'direct',operation_id,chain_id,wallet_id,'direct-key',request_fingerprint,operation_fingerprint,policy_version,expires_at,outcome,reason_code,created_at FROM trade_api_submission_requests LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"trade_api_submission_requests", "canary_runtime_audit", "canary_alert_outbox"} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 2 {
			t.Fatalf("direct %s count=%d err=%v", table, count, err)
		}
	}
	for _, columns := range []string{`'forged',operation_id,chain_id,'other-wallet','forged-key',request_fingerprint,operation_fingerprint,policy_version,expires_at,outcome,reason_code,created_at`, `'queued',operation_id,chain_id,wallet_id,'queued-key',request_fingerprint,operation_fingerprint,policy_version,expires_at,'QUEUED',reason_code,created_at`} {
		if _, err := s.db.Exec(`INSERT INTO trade_api_submission_requests(id,operation_id,chain_id,wallet_id,idempotency_key,request_fingerprint,operation_fingerprint,policy_version,expires_at,outcome,reason_code,created_at) SELECT ` + columns + ` FROM trade_api_submission_requests LIMIT 1`); err == nil {
			t.Fatal("direct SQL bypassed durable binding or rejection-only schema")
		}
	}
}

func TestTradeAPIConcurrentDuplicateAndSQLiteRestart(t *testing.T) {
	s, _, r, now := tradeAPIFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.RecordDisabledSubmissionIntake(ctx, "intent-buy", r, now)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var seq int
	var name, path string
	if err := s.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, _ := NewStore(db)
	expiry, _ := parseCanonicalExpiry(r.ExpiresAt)
	duplicate, err := reopened.RecordDisabledSubmissionIntake(ctx, "intent-buy", r, expiry.Add(time.Hour))
	if err != nil || !duplicate.Duplicate || duplicate.ExpiresAt != r.ExpiresAt || duplicate.CreatedAt != now.Format(time.RFC3339Nano) || duplicate.SubmissionQueued || duplicate.SendAuthorized {
		t.Fatalf("restart duplicate=%+v err=%v", duplicate, err)
	}
	for _, table := range []string{"trade_api_submission_requests", "canary_runtime_audit", "canary_alert_outbox"} {
		var count int
		if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("restart %s=%d err=%v", table, count, err)
		}
	}
	assertTradeAPINoEconomicWrites(t, reopened)
}

func TestTradeAPIStatusAndIncrementalEventsAreRedactedAndReadOnly(t *testing.T) {
	s, h, r, now := tradeAPIFixture(t)
	ctx := context.Background()
	if _, err := s.RecordDisabledSubmissionIntake(ctx, "intent-buy", r, now); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/internal/trade/operations/intent-buy", "/internal/trade/operations/intent-buy/events"} {
		if w := tradeAPIHTTP(t, h, "GET", path, "", false); w.Code != 401 {
			t.Fatalf("unauthenticated query=%d", w.Code)
		}
		w := tradeAPIHTTP(t, h, "GET", path, "", true)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("query=%d %s", w.Code, w.Body)
		}
		for _, secret := range []string{"encrypted_raw_tx", "ciphertext", "calldata", "private_key", "unsigned_call", "details_json"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("query exposed %s", secret)
			}
		}
	}
	for _, path := range []string{"/internal/trade/operations/intent-buy/events?after=01", "/internal/trade/operations/intent-buy/events?after=1&after=2", "/internal/trade/operations/intent-buy?enable=true"} {
		if w := tradeAPIHTTP(t, h, "GET", path, "", true); w.Code != 400 {
			t.Fatalf("query bypass %s=%d", path, w.Code)
		}
	}
	other, _ := NewDisabledTradeAPI(s, "other-wallet")
	if w := tradeAPIHTTP(t, other, "GET", "/internal/trade/operations/intent-buy", "", true); w.Code != 404 {
		t.Fatalf("cross-wallet query=%d", w.Code)
	}
	first, err := s.OperationAPIEvents(ctx, "intent-buy", r.WalletID, 0)
	if err != nil || len(first.Events) != 1 {
		t.Fatalf("events=%+v err=%v", first, err)
	}
	for i := range 105 {
		r.IdempotencyKey = fmt.Sprintf("page-%03d", i)
		if _, err := s.RecordDisabledSubmissionIntake(ctx, "intent-buy", r, now.Add(time.Duration(i)*time.Nanosecond)); err != nil {
			t.Fatal(err)
		}
	}
	next, err := s.OperationAPIEvents(ctx, "intent-buy", r.WalletID, first.NextCursor)
	if err != nil || len(next.Events) != 100 || next.Events[0].Cursor <= first.NextCursor {
		t.Fatalf("next page=%+v err=%v", next, err)
	}
	last, err := s.OperationAPIEvents(ctx, "intent-buy", r.WalletID, next.NextCursor)
	if err != nil || len(last.Events) != 5 || last.Events[0].Cursor <= next.NextCursor {
		t.Fatalf("last page=%+v err=%v", last, err)
	}
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		t.Fatal(err)
	}
	stable, err := s.OperationAPIEvents(ctx, "intent-buy", r.WalletID, next.NextCursor)
	if err != nil || !reflect.DeepEqual(stable, last) {
		t.Fatalf("VACUUM changed durable cursor: %v", err)
	}
	assertTradeAPINoEconomicWrites(t, s)
}

func TestTradeAPIAmbiguousSubmittedAndExpiredQueryDoesNotSend(t *testing.T) {
	for _, state := range []string{"submitted", "broadcast_unknown"} {
		t.Run(state, func(t *testing.T) {
			s, _, artifact, closeDB := seededSignedArtifact(t)
			defer closeDB()
			before, _, _ := s.LoadEncryptedArtifact(context.Background(), artifact.Operation)
			now := time.Now().UTC()
			sub, err := s.BeginSubmission(context.Background(), artifact, false, now)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.FinishSubmission(context.Background(), artifact, sub, state, artifact.TxHash, "", "", now); err != nil {
				t.Fatal(err)
			}
			h, _ := NewDisabledTradeAPI(s, artifact.WalletID)
			h.now = func() time.Time { return now.Add(24 * time.Hour) }
			for range 3 {
				w := tradeAPIHTTP(t, h, "GET", "/internal/trade/operations/"+artifact.Operation, "", true)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"`+state+`"`) {
					t.Fatalf("query=%d %s", w.Code, w.Body)
				}
			}
			after, _, _ := s.LoadEncryptedArtifact(context.Background(), artifact.Operation)
			if !bytes.Equal(before.Ciphertext, after.Ciphertext) || before.Nonce != after.Nonce || before.TxHash != after.TxHash {
				t.Fatal("query changed artifact/nonce")
			}
			assertAttemptCount(t, s, 1)
			if state == "broadcast_unknown" {
				assertFrozenRecovery(t, s, artifact)
			}
		})
	}
}

func TestTradeAPIDependencyGraphHasNoEconomicCapabilities(t *testing.T) {
	typ := reflect.TypeOf(DisabledTradeAPI{})
	for i := 0; i < typ.NumField(); i++ {
		for _, forbidden := range []string{"ExecutionKernel", "TransactionSigner", "SubmissionService", "RawBroadcaster", "ControlledSubmissionWorker", "ControlledCanaryOrchestrator"} {
			if strings.Contains(typ.Field(i).Type.String(), forbidden) {
				t.Fatalf("API dependency %s", forbidden)
			}
		}
	}
	for _, filename := range []string{"api_http.go", "api_intake.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch expr := call.Fun.(type) {
			case *ast.Ident:
				name = expr.Name
			case *ast.SelectorExpr:
				name = expr.Sel.Name
			}
			for _, forbidden := range []string{"Prepare", "Submit", "ReplayUnknown", "SendRawTransaction", "SendControlledPrepared", "SignTransaction", "NewSubmissionService", "NewExecutionKernel", "NewControlledSubmissionWorker", "CreateRuntimeAuthorization", "IssueCanaryPermitAfterGate", "ImmediatePreSend"} {
				if name == forbidden {
					t.Errorf("API source %s reaches prohibited call %s", filename, name)
				}
			}
			return true
		})
	}
}

func TestTradeAPIIndependentConnectionsConflictAndRetry(t *testing.T) {
	s, _, r, now := tradeAPIFixture(t)
	ctx := context.Background()
	var seq int
	var name, path string
	if err := s.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other, _ := NewStore(db)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, store := range []*Store{s, other} {
		go func() {
			<-start
			_, err := store.RecordDisabledSubmissionIntake(ctx, "intent-buy", r, now)
			results <- err
		}()
	}
	close(start)
	succeeded := 0
	for range 2 {
		err := <-results
		if err == nil {
			succeeded++
		} else {
			// SQLITE_BUSY_SNAPSHOT (517) is a valid fail-closed contender
			// outcome. Driver messages can contain only its numeric code.
			var sqliteErr *sqlite.Error
			if !errors.As(err, &sqliteErr) || sqliteErr.Code()&0xff != 5 {
				t.Fatal(err)
			}
		}
	}
	if succeeded < 1 {
		t.Fatal("no request committed")
	}
	duplicate, err := other.RecordDisabledSubmissionIntake(ctx, "intent-buy", r, now)
	if err != nil || !duplicate.Duplicate || duplicate.SendAuthorized || duplicate.SubmissionQueued {
		t.Fatalf("retry=%+v err=%v", duplicate, err)
	}
	for _, table := range []string{"trade_api_submission_requests", "canary_runtime_audit", "canary_alert_outbox"} {
		var count int
		if err := other.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("concurrent %s=%d err=%v", table, count, err)
		}
	}
	assertTradeAPINoEconomicWrites(t, s)
}

func TestTradeAPIUnixTransport(t *testing.T) {
	s, h, body, _ := tradeAPIFixture(t)
	auth, _ := ipc.NewAuthenticator([]byte(strings.Repeat("u", 32)), 30*time.Second)
	socket := filepath.Join(t.TempDir(), "api.sock")
	ready := health.NewReadiness("live_disabled")
	ready.Set("live_disabled", true)
	server := health.New("trade-api-test", socket, slog.New(slog.NewTextHandler(io.Discard, nil)), ready)
	server.Handle("/internal/trade/operations/", ipc.RequestContext(auth.Middleware(h)))
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	client := ipc.NewUnixClient(socket, time.Second)
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		method, path, body string
		signed             bool
		status             int
	}{
		{"POST", "/intent-buy/submission-requests", encodeIntake(t, body), false, 401},
		{"POST", "/intent-buy/submission-requests", encodeIntake(t, body), true, 503},
		{"GET", "/intent-buy", "", false, 401},
		{"GET", "/intent-buy", "", true, 200},
		{"GET", "/intent-buy/events", "", true, 200},
	} {
		r, _ := http.NewRequest(tc.method, "http://trade/internal/trade/operations"+tc.path, strings.NewReader(tc.body))
		if tc.signed {
			if err := auth.Sign(r, []byte(tc.body)); err != nil {
				t.Fatal(err)
			}
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != tc.status || response.Header.Get(ipc.HeaderRequestID) == "" {
			t.Fatalf("UDS %s %s status=%d err=%v", tc.method, tc.path, response.StatusCode, readErr)
		}
	}
	assertTradeAPINoEconomicWrites(t, s)
}

func assertTradeAPINoEconomicWrites(t *testing.T, s *Store) {
	t.Helper()
	for _, table := range []string{"transaction_attempts", "transaction_submissions", "execution_reservations", "execution_wallet_lanes", "canary_runtime_authorizations", "canary_send_permits", "canary_authorization_operation_usage", "position_effects"} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("economic table %s=%d err=%v", table, count, err)
		}
	}
	if _, err := s.OperationAPIStatus(context.Background(), "intent-buy", "wallet-1"); err != nil && !errors.Is(err, ErrTradeAPINotFound) {
		t.Fatal(err)
	}
}

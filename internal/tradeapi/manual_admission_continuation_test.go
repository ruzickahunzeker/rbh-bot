package tradeapi

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

// Only the inherited disposable fixture seeds a synthetic authorization. Put
// the EXACT production no-arm definition back before any A2 target action.
// No signer, transport, executor or key is constructed by this fixture.
func a2Fixture(t *testing.T) (*storage.Database, *Store, *API, Record, manualAdmissionScope, string) {
	t.Helper()
	d, s, original, r, scope, path := manualInputsFixture(t, "buy")
	raw, err := os.ReadFile("../storage/migrations/trade/018_manual_risk_units.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, guard, ok := strings.Cut(string(raw), "CREATE TRIGGER manual_b1_no_arm ")
	if !ok {
		t.Fatal("no-arm definition missing")
	}
	body, _, ok := strings.Cut(guard, "END;")
	if !ok {
		t.Fatal("no-arm terminator missing")
	}
	definition := "CREATE TRIGGER manual_b1_no_arm " + body + "END;"
	if _, err := s.db.Exec(definition); err != nil {
		t.Fatal(err)
	}
	var restored string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='manual_b1_no_arm'`).Scan(&restored); err != nil || restored+";" != definition {
		t.Fatal("no-arm not restored exactly", err)
	}
	return d, s, admissionHTTP(t, s, original, scope, &validationBackend{}), r, scope, path
}

func TestA2FreshCommitWitnessAndSharedCopyConcurrency(t *testing.T) {
	_, s, h, r, scope, _ := a2Fixture(t)
	result, c, err := atomicWriter(s).admitWithContinuation(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || c == nil || result.Duplicate {
		t.Fatal("fresh commit lost owner", result, err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		copy := *c
		wg.Add(1)
		go func() {
			defer wg.Done()
			owner, err := copy.take(context.Background())
			if err != nil {
				if !errors.Is(err, ErrConflict) {
					t.Error(err)
				}
				return
			}
			accepted.Add(1) // TEST-ONLY ownership claim, not an economic action.
			if owner.identity != h.identity || owner.request != r.RequestID || owner.operation != *r.OperationID || owner.reservation != result.ReservationID || owner.contractHash != result.ContractHash || owner.expiresAt != r.ExpiresAt || owner.scope != scope {
				t.Error("owner not bound to durable source")
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("copy reset one-shot guard", accepted.Load())
	}
	if _, err := c.take(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatal("reference reused", err)
	}
	history, next, err := atomicWriter(s).admitWithContinuation(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || next != nil || !history.Duplicate || history.ContractHash != result.ContractHash {
		t.Fatal("historical commit minted owner", history, err)
	}
	if before != admittedDatabaseSnapshot(t, s.db) {
		t.Fatal("ownership/history changed durable state")
	}
	assertAtomicAdmission(t, s, r, 1)
}

func TestA2InvalidCanceledDiscardedCannotReconstruct(t *testing.T) {
	var nilToken *manualAdmissionContinuation
	var zero manualAdmissionContinuation
	for _, c := range []*manualAdmissionContinuation{nilToken, &zero} {
		c.discard()
		if _, err := c.take(context.Background()); !errors.Is(err, ErrConflict) {
			t.Fatal("zero/nil owner accepted", err)
		}
	}
	for _, mode := range []string{"discard", "origin_cancel", "caller_cancel", "nil_context"} {
		t.Run(mode, func(t *testing.T) {
			_, s, h, r, scope, _ := a2Fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, c, err := atomicWriter(s).admitWithContinuation(ctx, h.identity, r.RequestID, scope)
			if err != nil || c == nil {
				t.Fatal(err)
			}
			copy := *c
			use := context.Background()
			switch mode {
			case "discard":
				c.discard()
			case "origin_cancel":
				cancel()
			case "caller_cancel":
				canceled, done := context.WithCancel(context.Background())
				done()
				use = canceled
			case "nil_context":
				use = nil
			}
			if _, err := copy.take(use); err == nil {
				t.Fatal("invalidated owner accepted")
			}
			if _, err := c.take(context.Background()); !errors.Is(err, ErrConflict) {
				t.Fatal("failure did not burn shared guard", err)
			}
			//lint:ignore SA9005 Intentionally prove the ownership witness has no JSON fields.
			encoded, err := json.Marshal(c)
			if err != nil || string(encoded) != "{}" {
				t.Fatal("handoff serialized", string(encoded), err)
			}
			var reconstructed manualAdmissionContinuation
			//lint:ignore SA9005 Intentionally prove public JSON cannot reconstruct private state.
			if err := json.Unmarshal([]byte(`{"state":{},"request":"`+r.RequestID+`","operation":"`+*r.OperationID+`"}`), &reconstructed); err != nil {
				t.Fatal(err)
			}
			if _, err := reconstructed.take(context.Background()); !errors.Is(err, ErrConflict) {
				t.Fatal("JSON reconstructed owner", err)
			}
			assertAtomicAdmission(t, s, r, 1)
		})
	}
}

func TestA2ObservationCompatibilityDoesNotRetainOwnership(t *testing.T) {
	_, s, h, r, scope, _ := a2Fixture(t)
	if err := h.validateAndAdmit(context.Background(), &r); err != nil || r.Admission == nil || r.Admission.Status != "ADMITTED_NOT_SIGNED_NOT_SUBMITTED" {
		t.Fatal("original observation-only helper changed", err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	result, c, err := atomicWriter(s).admitWithContinuation(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || c != nil || !result.Duplicate {
		t.Fatal("observation helper retained recoverable ownership", err)
	}
	if err := h.validateAndAdmit(context.Background(), &r); err != nil || !r.Duplicate {
		t.Fatal("historical compatibility path changed", err)
	}
	if before != admittedDatabaseSnapshot(t, s.db) {
		t.Fatal("observation-only helper wrote history")
	}
	assertAtomicAdmission(t, s, r, 1)
}

func TestA2RollbackAndRealCommitFailureMintNothing(t *testing.T) {
	for _, fault := range []string{"before_command", "after_command", "before_commit", "cancel_before_commit", "audit_insert", "deferred_commit"} {
		t.Run(fault, func(t *testing.T) {
			_, s, h, r, scope, _ := a2Fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := atomicWriter(s)
			reached := false
			w.checkpoint = func(stage string) error {
				if stage == "before_commit" {
					reached = true
				}
				if stage == fault {
					return errors.New("controlled checkpoint fault")
				}
				if fault == "cancel_before_commit" && stage == "before_commit" {
					cancel()
				}
				return nil
			}
			switch fault {
			case "audit_insert":
				if _, err := s.db.Exec(`CREATE TRIGGER a2_audit_fault BEFORE INSERT ON canary_runtime_audit WHEN NEW.id LIKE 'manual-admission%' BEGIN SELECT RAISE(ABORT,'a2 audit failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "deferred_commit":
				// The command and every trigger run successfully, but the real
				// COMMIT fails its deferred FK. No fake before-commit error.
				if _, err := s.db.Exec(`CREATE TABLE a2_parent(id INTEGER PRIMARY KEY);
CREATE TABLE a2_child(parent_id INTEGER REFERENCES a2_parent(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TRIGGER a2_deferred_fault AFTER INSERT ON manual_admission_commands BEGIN INSERT INTO a2_child VALUES(77); END;`); err != nil {
					t.Fatal(err)
				}
			}
			before := admittedDatabaseSnapshot(t, s.db)
			result, c, err := w.admitWithContinuation(ctx, h.identity, r.RequestID, scope)
			if err == nil || c != nil || result != (manualAdmissionResult{}) {
				t.Fatal("failed writer minted owner", result, c, err)
			}
			if fault == "deferred_commit" && (!reached || !strings.Contains(err.Error(), "FOREIGN KEY")) {
				t.Fatal("did not exercise real COMMIT failure", reached, err)
			}
			if before != admittedDatabaseSnapshot(t, s.db) {
				t.Fatal("failure retained partial durable state")
			}
			assertAtomicAdmission(t, s, r, 0)
		})
	}
}

func TestA2PostCommitProjectionFailureBurnsAllCopies(t *testing.T) {
	for _, fault := range []string{"read_cancel", "projection_mismatch", "identity_mismatch", "scope_mismatch"} {
		t.Run(fault, func(t *testing.T) {
			_, s, h, r, scope, _ := a2Fixture(t)
			result, c, err := atomicWriter(s).admitWithContinuation(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || c == nil {
				t.Fatal(err)
			}
			copy := *c
			ctx := context.Background()
			switch fault {
			case "read_cancel":
				canceled, done := context.WithCancel(ctx)
				done()
				ctx = canceled
			case "projection_mismatch":
				result.ContractHash = "wrong"
			case "identity_mismatch":
				h.identity.PrincipalID = "other"
			case "scope_mismatch":
				h.admissionScope.DeploymentID = "other"
			}
			before := admittedDatabaseSnapshot(t, s.db)
			got, err := h.completeManualAdmission(ctx, &r, result, c)
			if err == nil || got != nil {
				t.Fatal("post-commit failure retained owner", err)
			}
			if _, err := copy.take(context.Background()); !errors.Is(err, ErrConflict) {
				t.Fatal("post-commit failure did not burn escaped copy", err)
			}
			if before != admittedDatabaseSnapshot(t, s.db) {
				t.Fatal("pretended post-commit failure rolled back admission")
			}
			assertAtomicAdmission(t, s, r, 1)
		})
	}
}

func TestA2DifferentIntakeAdmissionWinnersTwoHandles(t *testing.T) {
	_, s, h, seed, scope, path := a2Fixture(t)
	d2, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	s2, err := NewStore(d2)
	if err != nil {
		t.Fatal(err)
	}
	h2 := admissionHTTP(t, s2, h, scope, &validationBackend{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first, err := s.RecordManual(ctx, h.identity, "a2-different-winners", seed.Request, h.policy, h.now)
	if err != nil || first.Duplicate {
		t.Fatal("intake winner", err)
	}
	second, err := s2.RecordManual(ctx, h.identity, "a2-different-winners", seed.Request, h.policy, h.now)
	if err != nil || !second.Duplicate || first.RequestID != second.RequestID {
		t.Fatal("duplicate intake", err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	h.validator.backend.(*validationBackend).during = func() {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	type outcome struct {
		c   *manualAdmissionContinuation
		err error
	}
	done := make(chan outcome, 1)
	go func() { c, err := h.validateAndAdmitWithContinuation(ctx, &first); done <- outcome{c, err} }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("validation barrier not reached")
	}
	winner, err := h2.validateAndAdmitWithContinuation(ctx, &second)
	if err != nil || winner == nil || !second.Duplicate || second.Admission.Status != "ADMITTED_NOT_SIGNED_NOT_SUBMITTED" {
		t.Fatal("composite duplicate suppressed fresh admission owner", err)
	}
	if _, err := winner.take(ctx); err != nil {
		t.Fatal("fresh owner unavailable", err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	releaseOnce.Do(func() { close(release) })
	select {
	case loser := <-done:
		if loser.err != nil || loser.c != nil || !first.Duplicate || first.Admission.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" {
			t.Fatal("intake winner gained historical ownership", loser.err)
		}
	case <-ctx.Done():
		t.Fatal("historical loser did not finish")
	}
	if before != admittedDatabaseSnapshot(t, s.db) {
		t.Fatal("historical loser changed admission state")
	}
	assertAtomicAdmission(t, s, second, 1)
}

func TestA2LostResponseQueriesDuplicatesAndReopenNoResume(t *testing.T) {
	d, s, h, seed, scope, path := a2Fixture(t)
	request := httptest.NewRequest("POST", "/v1/trades", strings.NewReader(encode(seed.Request)))
	request.Header.Set("Authorization", "Bearer "+testCredential)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "a2-lost-response")
	lost := &lostAdmissionResponse{headers: make(http.Header)}
	h.ServeHTTP(lost, request) // response writer fails AFTER the durable commit
	location := lost.Header().Get("Location")
	if location == "" {
		t.Fatal("lost recovery location")
	}
	before := admittedDatabaseSnapshot(t, s.db)
	r := admissionRecord(t, call(h, "POST", "/v1/trades", "a2-lost-response", encode(seed.Request)), 202)
	if !r.Duplicate || r.Admission.Status != "HISTORICAL_ADMISSION_NOT_PERMISSION" {
		t.Fatal("lost response retried action")
	}
	for _, url := range []string{location, "/v1/trades/by-idempotency-key"} {
		w := call(h, "GET", url, "a2-lost-response", "")
		observed := admissionRecord(t, w, 200)
		if observed.ExpiresAt != r.ExpiresAt || observed.Admission.ContractHash != r.Admission.ContractHash || strings.Contains(w.Body.String(), "continuation") {
			t.Fatal("query changed/exposed owner")
		}
	}
	if w := call(h, "GET", location+"/events", "", ""); w.Code != 200 || strings.Contains(w.Body.String(), "continuation") {
		t.Fatal("event query", w.Code, w.Body.String())
	}
	if before != admittedDatabaseSnapshot(t, s.db) {
		t.Fatal("retry/query wrote durable state")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	fresh, err := NewStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	w := atomicWriter(fresh)
	w.now = func() time.Time { return testNow.Add(2 * time.Hour) }
	result, c, err := w.admitWithContinuation(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || c != nil || !result.Duplicate || result.ExpiresAt != r.ExpiresAt {
		t.Fatal("restart/expiry restored owner", result, err)
	}
	if before != admittedDatabaseSnapshot(t, fresh.db) {
		t.Fatal("restart reset durable history/usage")
	}
	assertAtomicAdmission(t, fresh, r, 1)
	// The existing active reservation still blocks another order before
	// admission. This A2 fixture deliberately creates NO retained nonce claim.
	h2 := admissionHTTP(t, fresh, h, scope, &validationBackend{})
	if response := call(h2, "POST", "/v1/trades", "a2-second-order", encode(seed.Request)); response.Code != 403 || !strings.Contains(response.Body.String(), "WALLET_UNAVAILABLE") {
		t.Fatal("unexpected second-order rejection", response.Code, response.Body.String())
	}
	assertAtomicAdmission(t, fresh, r, 1)
}

func TestA2PrivateUncachedNoExecutionConsumerBoundary(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(API{}), reflect.TypeOf(Store{}), reflect.TypeOf(Record{}), reflect.TypeOf(Options{})} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if strings.Contains(strings.ToLower(f.Name+f.Type.String()), "continuation") {
				t.Fatal("handoff cached/configurable", typ, f.Name)
			}
		}
	}
	file, err := parser.ParseFile(token.NewFileSet(), "manual_admission_continuation.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, im := range file.Imports {
		if im.Path.Value != `"context"` && im.Path.Value != `"sync/atomic"` {
			t.Fatal("handoff acquired dependency", im.Path.Value)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.TypeSpec:
			if v.Name.IsExported() {
				t.Error("public handoff type")
			}
		case *ast.FuncDecl:
			if v.Name.IsExported() || v.Recv == nil {
				t.Error("public constructor/function")
			}
		case *ast.StructType:
			for _, f := range v.Fields.List {
				for _, name := range f.Names {
					if name.IsExported() {
						t.Error("serializable field")
					}
				}
			}
		}
		return true
	})
	root := filepath.Join("..", "..")
	mintSites := 0
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if literal, ok := n.(*ast.CompositeLit); ok {
					if typ, ok := literal.Type.(*ast.Ident); ok && typ.Name == "manualAdmissionContinuationState" {
						mintSites++
						if filepath.Base(path) != "manual_atomic_admission.go" {
							t.Errorf("ownership minted outside original admission writer: %s", path)
						}
					}
				}
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "take" {
						t.Errorf("production ownership consumer: %s", path)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if mintSites != 1 {
		t.Fatal("expected exactly one private mint site", mintSites)
	}
}

package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

type localNonceObserver struct {
	calls   atomic.Int64
	nonce   string
	change  func(*manualnonce.Observation)
	observe func(context.Context, manualnonce.Facts) (manualnonce.Observation, error)
}

func (o *localNonceObserver) ObserveManualNonce(ctx context.Context, f manualnonce.Facts) (manualnonce.Observation, error) {
	o.calls.Add(1)
	if o.observe != nil {
		return o.observe(ctx, f)
	}
	n := o.nonce
	if n == "" {
		n = "0"
	}
	v := manualnonce.Observation{OperationID: f.OperationID, RequestID: f.RequestID, BindingHash: f.BindingHash, ChainID: f.ChainID,
		WalletID: f.WalletID, WalletAddress: f.WalletAddress, ContractAddress: f.ContractAddress, RuntimeCodeHash: f.RuntimeCodeHash,
		BlockNumber: f.QuoteBlock, BlockHash: f.QuoteHash, BlockCanonical: true, ConfirmedNonce: n, PendingValue: n, ObservedAt: testNow.Format(time.RFC3339Nano)}
	if o.change != nil {
		o.change(&v)
	}
	return v, nil
}

func nonceWriter(s *Store, o *localNonceObserver) *manualNonceClaimWriter {
	return &manualNonceClaimWriter{store: s, observer: o, now: func() time.Time { return testNow }}
}

func assertNonceClaim(t *testing.T, s *Store, r Record, nonce string) {
	t.Helper()
	for _, table := range []string{"manual_nonce_claims", "transaction_attempts"} {
		if count(t, s.db, table) != 1 {
			t.Fatal("not exactly one claim/attempt", table)
		}
	}
	for _, table := range []string{"execution_reservations", "transaction_submissions", "receipt_observations", "position_effects", "canary_send_permits", "canary_runtime_gate_snapshots"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("prohibited/duplicate economic row", table)
		}
	}
	var n, step, lane string
	if err := s.db.QueryRow(`SELECT q.nonce,s.status,l.reserved_nonce FROM manual_nonce_claim_complete q JOIN execution_steps s ON s.id=q.execution_step_id JOIN execution_wallet_lanes l ON l.wallet_id=q.wallet_id`).Scan(&n, &step, &lane); err != nil || n != nonce || step != "nonce_reserved" || lane != nonce {
		t.Fatal("claim incomplete", n, step, lane, err)
	}
	if count(t, s.db, "canary_risk_reservations") != 1 || count(t, s.db, "canary_authorization_operation_usage") != 1 {
		t.Fatal("second risk/budget owner")
	}
	var operations int
	var used string
	if err := s.db.QueryRow(`SELECT operation_count,total_input FROM canary_authorization_usage`).Scan(&operations, &used); err != nil || operations != 1 || used != r.Validation.Output.Resolution.AmountIn {
		t.Fatal("budget recharged/refunded", operations, used, err)
	}
	var audits int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='MANUAL_NONCE_CLAIMED'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("nonce audit not exactly once", audits, err)
	}
}

func nonceCommand(t *testing.T, s *Store, h *API, r Record, n string) manualNonceClaim {
	t.Helper()
	b := bindingCommand(t, s, r)
	cmd, err := scanManualCommand(s.db.QueryRow(`SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE operation_id=?`, b.Operation))
	if err != nil {
		t.Fatal(err)
	}
	c, err := verifyManualCommandTx(context.Background(), s.db, cmd)
	if err != nil {
		t.Fatal(err)
	}
	o, err := (&localNonceObserver{nonce: n}).ObserveManualNonce(context.Background(), manualNonceFacts(c, b))
	if err != nil {
		t.Fatal(err)
	}
	raw := manualnonce.Encode(o)
	return manualNonceClaim{Operation: b.Operation, Step: b.ExecutionStep, Attempt: "manual-nonce:" + b.Operation, Wallet: b.Wallet,
		Address: strings.ToLower(b.Address), Nonce: n, Reservation: cmd.Reservation, ContractHash: b.ContractHash,
		ObservationRaw: raw, ObservationHash: manualnonce.Digest(raw), CheckedAt: testNow.Format(time.RFC3339Nano)}
}

func insertNonceSQL(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, q manualNonceClaim, verb string) error {
	_, err := db.ExecContext(ctx, verb+` INTO manual_nonce_claims(`+manualNonceClaimColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, q.arguments()...)
	return err
}

func TestHTTPM3B2ExactClaimNoSecondAccountingAndDuplicate(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			d, s, h, r, scope, _ := boundSigningFixture(t, side)
			observer := &localNonceObserver{}
			w := nonceWriter(s, observer)
			v, err := w.claim(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || v.Nonce != "0" || v.Status != "NONCE_CLAIMED_NOT_SIGNED_NOT_PERMISSION" || v.Duplicate || v.ExecutionAuthorized || v.SigningAuthorized || v.SendAuthorized {
				t.Fatal(v, err)
			}
			assertNonceClaim(t, s, r, "0")
			legacy, err := trade.NewStore(d)
			if err != nil {
				t.Fatal(err)
			}
			if work, err := legacy.Recoverable(context.Background()); err != nil || len(work) != 0 {
				t.Fatal("claimed MANUAL operation entered legacy execution recovery", len(work), err)
			}
			var fee, tip, data, value string
			if err := s.db.QueryRow(`SELECT gas_fee_cap,gas_tip_cap,calldata,value FROM transaction_attempts`).Scan(&fee, &tip, &data, &value); err != nil || fee != r.Validation.Output.GasFeeCap || tip != "0" || data != r.Validation.Output.Data || value != r.Validation.Output.Value {
				t.Fatal("immutable envelope drift", err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			observer.nonce = "9"
			for i := 0; i < 3; i++ {
				v, err = w.claim(context.Background(), h.identity, r.RequestID, scope)
				if err != nil || !v.Duplicate || v.Nonce != "0" || v.Status != "HISTORICAL_NONCE_CLAIM_NOT_PERMISSION" || v.SigningAuthorized || v.SendAuthorized {
					t.Fatal("duplicate reallocated", v, err)
				}
			}
			if observer.calls.Load() != 1 || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("duplicate queried/recharged/changed expiry")
			}
			for _, reader := range []func() (any, error){func() (any, error) {
				return boundSigningReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID)
			}, func() (any, error) {
				return admittedInputReader(t, s, scope).Load(context.Background(), h.identity, r.RequestID)
			}} {
				if _, err := reader(); err == nil {
					t.Fatal("initial reader accepted claimed phase")
				}
			}
			if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err == nil {
				t.Fatal("M2 history treated claimed phase as initial complete")
			}
		})
	}
}

func TestHTTPM3B2FactsNormalizeAddressesWithoutChangingEnvelope(t *testing.T) {
	_, s, _, r, _, _ := boundSigningFixture(t, "buy")
	cmd, err := scanManualCommand(s.db.QueryRow(`SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE request_id=?`, r.RequestID))
	if err != nil {
		t.Fatal(err)
	}
	c, err := verifyManualCommandTx(context.Background(), s.db, cmd)
	if err != nil {
		t.Fatal(err)
	}
	c.WalletAddress = "0xABcdabcdabcdabcdabcdabcdabcdabcdabcdABCD"
	c.Output.To = "0xABcdabcdabcdabcdabcdabcdabcdabcdabcdABCD"
	before := encode(c)
	f := manualNonceFacts(c, bindingCommand(t, s, r))
	if f.WalletAddress != strings.ToLower(c.WalletAddress) || f.ContractAddress != strings.ToLower(c.Output.To) || encode(c) != before {
		t.Fatal("query normalization changed transaction fields")
	}
}

func TestHTTPM3B2Uint64NonceAboveSignedIntegerRange(t *testing.T) {
	_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
	const nonce = "18446744073709551614"
	v, err := nonceWriter(s, &localNonceObserver{nonce: nonce}).claim(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || v.Nonce != nonce {
		t.Fatal("uint64 text converted/overflowed", v, err)
	}
	assertNonceClaim(t, s, r, nonce)
}

func TestHTTPM3B2DirectSQLCompleteAndHalfFaultRollback(t *testing.T) {
	for _, kind := range []string{"complete", "audit", "alert", "attempt", "step", "lane", "ignored_attempt", "ignored_alert", "ignored_step", "ignored_lane"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, _, _ := boundSigningFixture(t, "buy")
			q := nonceCommand(t, s, h, r, "23")
			query := ""
			switch kind {
			case "audit":
				query = `CREATE TRIGGER nonce_fault BEFORE INSERT ON canary_runtime_audit WHEN NEW.event_type='MANUAL_NONCE_CLAIMED' BEGIN SELECT RAISE(ABORT,'fault'); END`
			case "alert":
				query = `CREATE TRIGGER nonce_fault BEFORE INSERT ON canary_alert_outbox WHEN NEW.id LIKE 'manual-nonce-alert:%' BEGIN SELECT RAISE(ABORT,'fault'); END`
			case "attempt":
				query = `CREATE TRIGGER nonce_fault BEFORE INSERT ON transaction_attempts BEGIN SELECT RAISE(ABORT,'fault'); END`
			case "step":
				query = `CREATE TRIGGER nonce_fault BEFORE UPDATE ON execution_steps WHEN NEW.status='nonce_reserved' BEGIN SELECT RAISE(ABORT,'fault'); END`
			case "lane":
				query = `CREATE TRIGGER nonce_fault BEFORE UPDATE ON execution_wallet_lanes WHEN NEW.reserved_nonce IS NOT NULL BEGIN SELECT RAISE(ABORT,'fault'); END`
			case "ignored_attempt":
				query = `CREATE TRIGGER nonce_fault BEFORE INSERT ON transaction_attempts BEGIN SELECT RAISE(IGNORE); END`
			case "ignored_alert":
				query = `CREATE TRIGGER nonce_fault BEFORE INSERT ON canary_alert_outbox WHEN NEW.id LIKE 'manual-nonce-alert:%' BEGIN SELECT RAISE(IGNORE); END`
			case "ignored_step":
				query = `CREATE TRIGGER nonce_fault BEFORE UPDATE ON execution_steps WHEN NEW.status='nonce_reserved' BEGIN SELECT RAISE(IGNORE); END`
			case "ignored_lane":
				query = `CREATE TRIGGER nonce_fault BEFORE UPDATE ON execution_wallet_lanes WHEN NEW.reserved_nonce IS NOT NULL BEGIN SELECT RAISE(IGNORE); END`
			}
			if query != "" {
				if _, err := s.db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			before := admittedDatabaseSnapshot(t, s.db)
			err := insertNonceSQL(context.Background(), s.db, q, "INSERT OR IGNORE")
			if kind == "complete" {
				if err != nil {
					t.Fatal(err)
				}
				assertNonceClaim(t, s, r, "23")
			} else if err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("half effect or missed failure", kind, err)
			}
		})
	}
}

func TestHTTPM3B2BadObservationAndStopBeforeQuery(t *testing.T) {
	for _, kind := range []string{"stop", "disabled", "revoke", "scope", "chain", "wallet", "address", "contract", "runtime", "binding", "request", "pending", "zero_block", "wrong_hash", "not_canonical", "stale", "future", "bad_time", "noncanonical_time", "negative_nonce", "leading_zero", "overflow", "successor_overflow"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			o := &localNonceObserver{change: func(v *manualnonce.Observation) {
				switch kind {
				case "chain":
					v.ChainID = 1
				case "wallet":
					v.WalletID = "other"
				case "address":
					v.WalletAddress = "0x" + strings.Repeat("9", 40)
				case "contract":
					v.ContractAddress = "0x" + strings.Repeat("9", 40)
				case "runtime":
					v.RuntimeCodeHash = "0x" + strings.Repeat("9", 64)
				case "binding":
					v.BindingHash = strings.Repeat("9", 64)
				case "request":
					v.RequestID = "other"
				case "pending":
					v.PendingValue = "1"
				case "zero_block":
					v.BlockNumber = 0
				case "wrong_hash":
					v.BlockHash = "0x" + strings.Repeat("9", 64)
				case "not_canonical":
					v.BlockCanonical = false
				case "stale":
					v.ObservedAt = testNow.Add(-3 * time.Second).Format(time.RFC3339Nano)
				case "future":
					v.ObservedAt = testNow.Add(time.Nanosecond).Format(time.RFC3339Nano)
				case "bad_time":
					v.ObservedAt = "invalid"
				case "noncanonical_time":
					v.ObservedAt = strings.ReplaceAll(v.ObservedAt, "Z", "+00:00")
				case "negative_nonce":
					v.PendingValue = "-1"
					v.ConfirmedNonce = "-1"
				case "leading_zero":
					v.PendingValue = "00"
					v.ConfirmedNonce = "00"
				case "overflow":
					v.PendingValue = "18446744073709551616"
					v.ConfirmedNonce = v.PendingValue
				case "successor_overflow":
					v.PendingValue = "18446744073709551615"
					v.ConfirmedNonce = v.PendingValue
				}
			}}
			preQuery := false
			switch kind {
			case "stop":
				_, _ = s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`)
				preQuery = true
			case "disabled":
				_, _ = s.db.Exec(`UPDATE canary_control_state SET mode='DISABLED'`)
				preQuery = true
			case "revoke":
				_, _ = s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`)
				preQuery = true
			case "scope":
				scope.DeploymentID = "other"
				preQuery = true
			}
			before := admittedDatabaseSnapshot(t, s.db)
			v, err := nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("invalid evidence/control changed durable state", kind, v, err)
			}
			if preQuery && o.calls.Load() != 0 {
				t.Fatal("denied before query still queried")
			}
		})
	}
}

func TestHTTPM3B2WriterFaultWindowsAndExpiry(t *testing.T) {
	for _, stage := range []string{"after_read", "after_observation", "after_begin", "before_insert", "after_insert", "before_commit"} {
		t.Run(stage, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			before := admittedDatabaseSnapshot(t, s.db)
			w := nonceWriter(s, &localNonceObserver{})
			fault := errors.New("controlled fault")
			w.checkpoint = func(at string) error {
				if at == stage {
					return fault
				}
				return nil
			}
			v, err := w.claim(context.Background(), h.identity, r.RequestID, scope)
			if !errors.Is(err, fault) || !reflect.DeepEqual(v, manualNonceClaimResult{}) || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("fault escaped effects", stage, v, err)
			}
		})
	}
	for _, stage := range []string{"after_observation", "after_insert", "before_commit"} {
		t.Run("expiry_"+stage, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			before := admittedDatabaseSnapshot(t, s.db)
			w := nonceWriter(s, &localNonceObserver{})
			expiry, _ := canonicalManualTime(r.ExpiresAt)
			w.checkpoint = func(at string) error {
				if at == stage {
					w.now = func() time.Time { return expiry }
				}
				return nil
			}
			v, err := w.claim(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("expired path committed claim", v, err)
			}
		})
	}
}

func TestHTTPM3B2ResponseLossReopenHistoryAfterExpiry(t *testing.T) {
	d, s, h, r, scope, path := boundSigningFixture(t, "sell_percent")
	o := &localNonceObserver{nonce: "23"}
	w := nonceWriter(s, o)
	w.checkpoint = func(at string) error {
		if at == "after_commit" {
			return errors.New("lost response")
		}
		return nil
	}
	if v, err := w.claim(context.Background(), h.identity, r.RequestID, scope); err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) {
		t.Fatal("lost response returned permission", v, err)
	}
	assertNonceClaim(t, s, r, "23")
	before := admittedDatabaseSnapshot(t, s.db)
	_, s = bindingReopen(t, d, path)
	if admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("reopen changed claimed work")
	}
	if _, err := s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`); err != nil {
		t.Fatal(err)
	}
	w = nonceWriter(s, o)
	expiry, _ := canonicalManualTime(r.ExpiresAt)
	w.now = func() time.Time { return expiry.Add(time.Second) }
	before = admittedDatabaseSnapshot(t, s.db)
	v, err := w.claim(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || !v.Duplicate || v.Nonce != "23" || v.SigningAuthorized || v.SendAuthorized || o.calls.Load() != 1 || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("restart historical duplicate reallocated/refreshed", v, err)
	}
	assertNonceClaim(t, s, r, "23")
}

func TestHTTPM3B2TwoHandleConcurrentClaims(t *testing.T) {
	_, s, h, r, scope, path := boundSigningFixture(t, "buy")
	d2, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	s2, err := NewStore(d2)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var accepted atomic.Int64
	start := make(chan struct{})
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			owner := s
			if i%2 != 0 {
				owner = s2
			}
			v, err := nonceWriter(owner, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope)
			if err != nil {
				t.Errorf("contest error: %v", err)
				return
			}
			if !v.Duplicate {
				accepted.Add(1)
			}
			if v.Nonce != "23" || v.SigningAuthorized || v.SendAuthorized {
				t.Error("contest identity drift")
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("more than one claim", accepted.Load())
	}
	assertNonceClaim(t, s, r, "23")
}

func TestHTTPM3B2ClosedDependenciesAndGuards(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "manual_nonce_claim.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		for _, bad := range []string{"internal/trade\"", "ethclient", "/rpc\"", "crypto/ecdsa", "core/types"} {
			if strings.Contains(imp.Path.Value, bad) {
				t.Fatal("economic import", imp.Path.Value)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok {
			switch s.Sel.Name {
			case "SignTransaction", "Submit", "ReplayUnknown", "SendRawTransaction", "ReserveExecution", "Prepare", "Encrypt", "Decrypt", "NewTx":
				t.Errorf("economic call %s", s.Sel.Name)
			}
		}
		return true
	})
	_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
	if _, err := nonceWriter(s, &localNonceObserver{}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manual_b1_no_permit", "manual_b1_no_pass_snapshot", "manual_execution_no_financial_reservation", "manual_binding_lane_retained_delete", "manual_binding_lane_update_victim", "manual_binding_lane_insert_victim", "manual_execution_no_attempt_update"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Fatal("closed guard removed", name, err)
		}
	}
	assertNonceClaim(t, s, r, "0")
}

func TestHTTPM3B2DirectSQLMutationAndConflictVictims(t *testing.T) {
	for _, recursion := range []int{0, 1} {
		t.Run(fmt.Sprint(recursion), func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(fmt.Sprintf("PRAGMA recursive_triggers=%d", recursion)); err != nil {
				t.Fatal(err)
			}
			bindingLegacyReferences(t, s)
			before := admittedDatabaseSnapshot(t, s.db)
			for _, sql := range []string{
				`UPDATE manual_nonce_claims SET nonce='24'`, `DELETE FROM manual_nonce_claims`, `INSERT OR REPLACE INTO manual_nonce_claims SELECT * FROM manual_nonce_claims`,
				`UPDATE execution_steps SET status='signing' WHERE kind='manual_trade_execution'`,
				`UPDATE execution_steps SET status='manual_execution_bound' WHERE kind='manual_trade_execution'`,
				`UPDATE execution_wallet_lanes SET reserved_nonce=NULL WHERE wallet_id=(SELECT wallet_id FROM manual_nonce_claims)`,
				`UPDATE transaction_attempts SET nonce='24'`, `DELETE FROM transaction_attempts`,
				`INSERT OR REPLACE INTO transaction_attempts SELECT * FROM transaction_attempts`,
				`INSERT OR REPLACE INTO transaction_attempts(rowid,id,step_id,nonce,status,created_at) SELECT rowid,'alias-attempt','alias-step','7','failed',created_at FROM transaction_attempts`,
				`UPDATE OR REPLACE execution_steps SET rowid=(SELECT rowid FROM execution_steps WHERE kind='manual_trade_execution') WHERE id='alias-step'`,
				`INSERT OR REPLACE INTO execution_steps(rowid,id,operation_id,step_index,kind,status) SELECT rowid,'victim-step','alias-operation',9,'curve_swap','failed' FROM execution_steps WHERE kind='manual_trade_execution'`,
				`UPDATE canary_runtime_audit SET reason_code='bad' WHERE event_type='MANUAL_NONCE_CLAIMED'`,
				`DELETE FROM canary_runtime_audit WHERE event_type='MANUAL_NONCE_CLAIMED'`,
				`INSERT OR REPLACE INTO canary_runtime_audit SELECT * FROM canary_runtime_audit WHERE event_type='MANUAL_NONCE_CLAIMED'`,
				`INSERT OR REPLACE INTO canary_runtime_audit(rowid,id,event_type,reason_code,details_json,created_at) SELECT rowid,'victim-audit','LOCAL_METADATA','TEST','{}',created_at FROM canary_runtime_audit WHERE event_type='MANUAL_NONCE_CLAIMED'`,
				`DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-nonce-alert:%'`,
				`INSERT OR REPLACE INTO canary_alert_outbox SELECT * FROM canary_alert_outbox WHERE id LIKE 'manual-nonce-alert:%'`,
				`UPDATE canary_alert_outbox SET audit_id='different' WHERE id LIKE 'manual-nonce-alert:%'`,
			} {
				if _, err := s.db.Exec(sql); err == nil {
					t.Fatal("mutation/bypass accepted", sql)
				}
				if admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("rejected mutation changed history", sql)
				}
			}
		})
	}
}

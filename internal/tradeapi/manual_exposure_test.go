package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

func exposureFixture(t *testing.T) (*storage.Database, *Store, *API, manualrisk.Policy, string, string) {
	t.Helper()
	d, s, h, _, path := validationAPI(t)
	var sourceHash string
	if err := s.db.QueryRow(`SELECT policy_hash FROM canary_policies WHERE version=1`).Scan(&sourceHash); err != nil {
		t.Fatal(err)
	}
	p := manualrisk.Policy{ID: "exposure-policy", SourcePolicyVersion: 1, SourcePolicyHash: sourceHash, ChainID: 4663, Protocol: "PONS_V2_CURVE",
		InputLimits: []manualrisk.InputLimit{
			{Unit: manualrisk.Unit{ChainID: 4663, Asset: testToken, Decimals: 6, Kind: manualrisk.TradeInput}, MaxOperation: "1000000000", MaxWalletActive: "2000000000", MaxGlobalActive: "3000000000"},
			{Unit: manualrisk.Unit{ChainID: 4663, Asset: "native", Decimals: 18, Kind: manualrisk.TradeInput}, MaxOperation: "1000000000000000000", MaxWalletActive: "2000000000000000000", MaxGlobalActive: "3000000000000000000"}},
		MaxNativeGasPerOperation: "100000000000000", MaxNativeWalletOutlay: "4000000000000000000", MaxNativeGlobalOutlay: "8000000000000000000", MinNativeBalance: "1000000000000000", MaxSellBPS: 5000,
		MaxWalletOperationsWindow: 10, MaxGlobalOperationsWindow: 100, WindowSeconds: 3600}
	raw, digest, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO manual_risk_policies VALUES(?,?,?,?,?,?)`, p.ID, 1, sourceHash, raw, digest, testNow.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return d, s, h, p, digest, path
}

func exposureOrder(t *testing.T, h *API, key, side string) Record {
	t.Helper()
	req := buy()
	req.WalletID = h.identity.WalletID
	req.Fee.TipGwei = "0"
	if side == "sell" {
		req.Side, req.Amount = "sell", "750.25"
	}
	return manualRecord(t, call(h, "POST", "/v1/trades", key, encode(req)))
}

func exposureParent(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, r Record, wallet, state string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO canary_risk_reservations(id,operation_id,wallet_id,token_address,policy_version,input_amount,gas_budget,state,created_at,updated_at,quote_block_number,quote_block_hash,source_hash)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, "exposure-"+r.RequestID, *r.OperationID, wallet, testToken, r.Validation.PolicyVersion, r.Validation.Output.Resolution.AmountIn, r.Validation.Output.GasBudget, state, testNow.Format(time.RFC3339Nano), testNow.Format(time.RFC3339Nano), r.Validation.Output.Resolution.Token.Block.Number, r.Validation.Output.Resolution.Token.Block.Hash, r.Validation.ResultHash)
	return err
}

func exposureUnitArgs(r Record, p manualrisk.Policy, digest string) []any {
	asset, decimals := "native", uint8(18)
	if r.Request.Side == "sell" {
		asset, decimals = testToken, 6
	}
	return []any{"exposure-" + r.RequestID, *r.OperationID, p.ID, digest, 4663, asset, decimals, manualrisk.TradeInput, r.Validation.ResultHash, hash([]byte(encode(r.Request))), r.ExpiresAt, testNow.Format(time.RFC3339Nano)}
}

const exposureUnitSQL = `INSERT INTO manual_reservation_units VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`

func seedExposure(t *testing.T, s *Store, r Record, p manualrisk.Policy, digest, wallet, state string) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = exposureParent(ctx, tx, r, wallet, state); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, exposureUnitSQL, exposureUnitArgs(r, p, digest)...); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func assertExposureNoAuthority(t *testing.T, s *Store) {
	t.Helper()
	for _, table := range []string{"canary_authorization_usage", "canary_authorization_operation_usage", "canary_runtime_authorizations", "canary_send_permits", "canary_runtime_gate_snapshots", "execution_reservations", "execution_wallet_lanes", "transaction_attempts", "transaction_submissions"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("metadata/read-only foundation created authority/economic side effect", table)
		}
	}
}

func TestHTTPC3B3FoundationSQLSourceIdentityAndImmutableParent(t *testing.T) {
	_, s, h, p, digest, _ := exposureFixture(t)
	r := exposureOrder(t, h, "binding", "sell")
	if err := exposureParent(context.Background(), s.db, r, h.identity.WalletID, "reserved"); err != nil {
		t.Fatal(err)
	}
	args := exposureUnitArgs(r, p, digest)
	for i, bad := range map[int]any{0: "absent", 1: "absent", 2: "absent", 3: strings.Repeat("0", 64), 4: 8453, 5: "native", 6: 18, 7: manualrisk.NativeGas, 8: strings.Repeat("0", 64), 9: strings.Repeat("0", 64), 10: testNow.Add(time.Hour).Format(time.RFC3339Nano), 11: testNow.Add(time.Second).Format(time.RFC3339Nano)} {
		changed := append([]any(nil), args...)
		changed[i] = bad
		if _, err := s.db.Exec(exposureUnitSQL, changed...); err == nil {
			t.Fatal("unit/source mismatch accepted", i)
		}
	}
	if count(t, s.db, "manual_reservation_units") != 0 {
		t.Fatal("failed unit binding persisted")
	}
	if _, err := s.db.Exec(exposureUnitSQL, args...); err != nil {
		t.Fatal(err)
	}
	for _, assignment := range []string{
		`id='changed'`, `operation_id='changed'`, `wallet_id='changed'`, `token_address='changed'`, `policy_version=2`, `input_amount='1'`, `gas_budget='1'`,
		`quote_block_number=101`, `quote_block_hash='changed'`, `source_hash='changed'`, `created_at='changed'`,
	} {
		if _, err := s.db.Exec(`UPDATE canary_risk_reservations SET ` + assignment); err == nil {
			t.Fatal("typed parent field changed", assignment)
		}
	}
	for _, q := range []string{
		`UPDATE manual_reservation_units SET input_decimals=18`, `DELETE FROM manual_reservation_units`, `INSERT OR REPLACE INTO manual_reservation_units SELECT * FROM manual_reservation_units`,
		`UPDATE canary_risk_reservations SET input_amount='1'`, `UPDATE canary_risk_reservations SET wallet_id='other'`, `UPDATE canary_risk_reservations SET source_hash='` + strings.Repeat("0", 64) + `'`,
		`DELETE FROM canary_risk_reservations`, `INSERT OR REPLACE INTO canary_risk_reservations SELECT * FROM canary_risk_reservations`,
		`INSERT INTO canary_window_usage VALUES(1,'wallet-01','` + testToken + `','2026-10-07T12:00:00Z',1,'1','0','2026-10-07T12:00:00Z')`,
	} {
		if _, err := s.db.Exec(q); err == nil {
			t.Fatal("identity/accounting bypass accepted", q)
		}
	}
	if _, err := s.db.Exec(`UPDATE canary_risk_reservations SET state='frozen'`); err != nil {
		t.Fatal("existing freeze lifecycle blocked", err)
	}
	var audit, alerts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='MANUAL_RESERVATION_UNIT_BOUND'`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM canary_alert_outbox WHERE id LIKE 'manual-reservation-unit-alert:%'`).Scan(&alerts); err != nil || audit != 1 || alerts != 1 {
		t.Fatal("binding audit/outbox not exactly once", audit, alerts, err)
	}
	assertExposureNoAuthority(t, s)
}

func TestHTTPC3B3FoundationForcedAuditOutboxFailureRollsBackParentAndUnit(t *testing.T) {
	for _, table := range []string{"canary_runtime_audit", "canary_alert_outbox"} {
		t.Run(table, func(t *testing.T) {
			_, s, h, p, digest, _ := exposureFixture(t)
			r := exposureOrder(t, h, "fault", "buy")
			before := count(t, s.db, "canary_runtime_audit")
			if _, err := s.db.Exec(`CREATE TRIGGER foundation_fault BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'forced fault'); END`); err != nil {
				t.Fatal(err)
			}
			withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
				if err := exposureParent(ctx, conn, r, h.identity.WalletID, "reserved"); err != nil {
					t.Fatal(err)
				}
				if _, err := conn.ExecContext(ctx, exposureUnitSQL, exposureUnitArgs(r, p, digest)...); err == nil {
					t.Fatal("audit/outbox failure accepted")
				}
				var n int
				if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM manual_reservation_units`).Scan(&n); err != nil || n != 0 {
					t.Fatal("failed statement left unit", err)
				}
				// Statement ABORT is not a transaction rollback; the owner must
				// roll back the parent too. The helper defers exactly that.
			})
			if count(t, s.db, "canary_risk_reservations") != 0 || count(t, s.db, "manual_reservation_units") != 0 || count(t, s.db, "canary_runtime_audit") != before || count(t, s.db, "canary_alert_outbox") != before {
				t.Fatal("transaction left partial parent/unit/audit/outbox")
			}
			assertExposureNoAuthority(t, s)
		})
	}
}

func TestHTTPC3B3FoundationTwoHandleBindingContestAndActualRestart(t *testing.T) {
	d, s, h, p, digest, path := exposureFixture(t)
	r := exposureOrder(t, h, "contest", "buy")
	if err := exposureParent(context.Background(), s.db, r, h.identity.WalletID, "reserved"); err != nil {
		t.Fatal(err)
	}
	other, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStore(other)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var accepted atomic.Int64
	start := make(chan struct{})
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			db := s.db
			if i%2 == 1 {
				db = second.db
			}
			if _, err := db.Exec(exposureUnitSQL, exposureUnitArgs(r, p, digest)...); err == nil {
				accepted.Add(1)
			} else if !strings.Contains(err.Error(), "exists") {
				t.Errorf("unexplained contest rejection: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if accepted.Load() != 1 || count(t, s.db, "manual_reservation_units") != 1 {
		t.Fatal("binding contest not exactly once", accepted.Load())
	}
	var prior manualExposure
	withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
		prior, err = loadManualExposureTx(ctx, conn, h.identity.WalletID, p, testNow)
		if err != nil {
			t.Fatal(err)
		}
	})
	other.Close()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	store, err := NewStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	withManualInputTransaction(t, store, func(ctx context.Context, conn *sql.Conn) {
		got, err := loadManualExposureTx(ctx, conn, h.identity.WalletID, p, testNow)
		if err != nil || !reflect.DeepEqual(got, prior) {
			t.Fatal("restart changed authoritative exposure", err)
		}
	})
	var audits int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='MANUAL_RESERVATION_UNIT_BOUND'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("restart/duplicates audited binding twice", err)
	}
	assertExposureNoAuthority(t, store)
}

func TestHTTPC3B3FoundationMixedAssetsCountsFrozenExpiryAndSelectOnly(t *testing.T) {
	_, s, h, p, digest, _ := exposureFixture(t)
	one := exposureOrder(t, h, "buy-history", "buy")
	sell := exposureOrder(t, h, "sell-active", "sell")
	// Distinct dummy wallet, never real multi-wallet authorization or a chain.
	const secondWallet = "0x1000000000000000000000000000000000000002"
	if _, err := s.db.Exec(`INSERT INTO dry_run_wallets VALUES('wallet-02',4663,?,1)`, secondWallet); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO canary_wallet_allowlist VALUES('wallet-02',?,4663,1,1,?)`, secondWallet, testNow.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	id := h.identity
	id.WalletID = "wallet-02"
	id.WalletAddress = secondWallet
	other, err := NewWithOptions(s, id, testCredential, h.policy, nil, Options{ManualIntake: true, ValidationBackend: &validationBackend{wallet: secondWallet}, ValidationPolicyVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	other.now, other.validator.now = h.now, h.now
	two := exposureOrder(t, other, "buy-active", "buy")
	seedExposure(t, s, one, p, digest, h.identity.WalletID, "released")
	seedExposure(t, s, sell, p, digest, h.identity.WalletID, "frozen")
	seedExposure(t, s, two, p, digest, id.WalletID, "reserved")
	// Historical accounting is not new economic permission: no authorization
	// exists, and stop/disabled mode must not make retained exposure disappear.
	stamp := testNow.Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`INSERT INTO canary_control_state VALUES(1,4663,'DISABLED',1,1,'fixture stop','fixture',?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	for _, now := range []time.Time{testNow, testNow.Add(time.Hour), testNow.Add(time.Hour + time.Nanosecond)} {
		withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
			var before, after int64
			if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			v, err := loadManualExposureTx(ctx, conn, h.identity.WalletID, p, now)
			if err != nil {
				t.Fatal(err)
			}
			counts := uint64(3)
			if now.After(testNow.Add(time.Hour)) {
				counts = 0
			}
			native := manualrisk.Unit{ChainID: 4663, Asset: "native", Decimals: 18, Kind: manualrisk.TradeInput}
			tokenUnit := manualrisk.Unit{ChainID: 4663, Asset: testToken, Decimals: 6, Kind: manualrisk.TradeInput}
			if len(v.GlobalInput) != 2 || len(v.WalletInput) != 1 || v.GlobalInput[native].Units != "10000000000000000" || v.GlobalInput[tokenUnit].Units != "750250000" || v.WalletOutlay.Units != "100000000000000" || v.GlobalOutlay.Units != "10200000000000000" || v.WalletActiveOperations != 1 || v.GlobalWindowOperations != counts {
				t.Fatal("mixed quantities/window/freeze semantics", v)
			}
			if counts == 3 && v.WalletWindowOperations != 2 || counts == 0 && v.WalletWindowOperations != 0 {
				t.Fatal("window count split by asset or release refunded history")
			}
			if err := manualrisk.CheckRetainedBalance("102000000000000", v.WalletOutlay.Units, "2000000000000"); err != nil {
				t.Fatal("retained balance equality rejected", err)
			}
			if err := manualrisk.CheckRetainedBalance("101999999999999", v.WalletOutlay.Units, "2000000000000"); err == nil {
				t.Fatal("retained balance short by one accepted")
			}
			if err = manualrisk.CheckCap(v.GlobalInput[native], manualrisk.Quantity{Unit: native, Units: "1"}, manualrisk.Quantity{Unit: native, Units: "10000000000000001"}); err != nil {
				t.Fatal("cap equality rejected")
			}
			if err = manualrisk.CheckCap(v.GlobalInput[native], manualrisk.Quantity{Unit: native, Units: "2"}, manualrisk.Quantity{Unit: native, Units: "10000000000000001"}); err == nil {
				t.Fatal("cap +1 accepted")
			}
			if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil || before != after {
				t.Fatal("exposure loader wrote SQL", err)
			}
		})
	}
	assertExposureNoAuthority(t, s)
}

func TestHTTPC3B3FoundationUntypedCorruptUnsupportedExposureRejected(t *testing.T) {
	for _, fault := range []string{"untyped_active", "untyped_recent_released", "legacy_window", "legacy_overlapping_window", "wrong_unit", "false_policy_hash", "parent_amount", "noncanonical_stamp", "current_policy_unit_missing"} {
		t.Run(fault, func(t *testing.T) {
			_, s, h, p, digest, _ := exposureFixture(t)
			r := exposureOrder(t, h, "bad-exposure", "buy")
			state := "reserved"
			if fault == "untyped_recent_released" {
				state = "released"
			}
			if err := exposureParent(context.Background(), s.db, r, h.identity.WalletID, state); err != nil {
				t.Fatal(err)
			}
			if fault != "untyped_active" && fault != "untyped_recent_released" && !strings.HasPrefix(fault, "legacy_") {
				if _, err := s.db.Exec(exposureUnitSQL, exposureUnitArgs(r, p, digest)...); err != nil {
					t.Fatal(err)
				}
			}
			var statements []string
			switch fault {
			case "legacy_window":
				statements = []string{`INSERT INTO canary_window_usage VALUES(1,'wallet-01','` + testToken + `','2026-10-07T12:00:00Z',1,'1','0','2026-10-07T12:00:00Z')`}
			case "legacy_overlapping_window":
				statements = []string{`INSERT INTO canary_window_usage VALUES(1,'wallet-01','` + testToken + `','2026-10-07T10:30:00Z',1,'1','0','2026-10-07T11:15:00Z')`}
			case "wrong_unit":
				statements = []string{`DROP TRIGGER manual_reservation_unit_no_update`, `UPDATE manual_reservation_units SET input_asset='` + testToken + `',input_decimals=6`}
			case "false_policy_hash":
				statements = []string{`DROP TRIGGER manual_risk_policy_no_update`, `UPDATE manual_risk_policies SET policy_hash='` + strings.Repeat("0", 64) + `'`}
			case "parent_amount":
				statements = []string{`DROP TRIGGER manual_reservation_parent_identity`, `UPDATE canary_risk_reservations SET input_amount='1'`}
			case "noncanonical_stamp":
				statements = []string{`DROP TRIGGER manual_reservation_parent_identity`, `UPDATE canary_risk_reservations SET created_at='2026-10-07T12:00:00+00:00'`}
			case "current_policy_unit_missing":
				p.InputLimits = p.InputLimits[:1]
			}
			for _, q := range statements {
				if _, err := s.db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
				v, err := loadManualExposureTx(ctx, conn, h.identity.WalletID, p, testNow)
				if err == nil || !reflect.DeepEqual(v, manualExposure{}) {
					t.Fatal("invalid exposure accepted or partial result leaked", err, v)
				}
			})
			assertExposureNoAuthority(t, s)
		})
	}
}

func TestHTTPC3B3FoundationLegacyAdmissionRejectsManualButAuditsStopPriority(t *testing.T) {
	for _, stopped := range []int{0, 1} {
		d, s, h, _, _, _ := exposureFixture(t)
		r := exposureOrder(t, h, "legacy-bypass", "buy")
		stamp := testNow.Format(time.RFC3339Nano)
		if _, err := s.db.Exec(`INSERT INTO canary_control_state VALUES(1,4663,'CONTROLLED_CANARY',1,?,'fixture','fixture',?,?)`, stopped, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		legacy, err := trade.NewStore(d)
		if err != nil {
			t.Fatal(err)
		}
		request := trade.CanaryRequest{RequestID: "legacy-manual", OperationID: *r.OperationID, PolicyVersion: 1, WalletID: h.identity.WalletID, TokenAddress: testToken, Amount: "1"}
		v, err := legacy.AdmitControlledCanary(context.Background(), request, testNow)
		want := "MANUAL_ATOMIC_PATH_REQUIRED"
		if stopped == 1 {
			want = "EMERGENCY_STOPPED"
		}
		if !errors.Is(err, trade.ErrCanaryRejected) || v.Decision != "REJECTED" || v.ReasonCode != want || count(t, s.db, "canary_admission_decisions") != 1 || count(t, s.db, "canary_admission_attempts") != 1 || count(t, s.db, "canary_audit_events") != 1 || count(t, s.db, "canary_risk_reservations") != 0 {
			t.Fatal("legacy MANUAL bypass or rejection not audited/priority", v, err)
		}
		v, err = legacy.AdmitControlledCanary(context.Background(), request, testNow.Add(time.Second))
		if !errors.Is(err, trade.ErrCanaryRejected) || !v.Duplicate || v.Decision != "REJECTED" || v.ReasonCode != want || count(t, s.db, "canary_admission_attempts") != 2 || count(t, s.db, "canary_admission_decisions") != 1 {
			t.Fatal("legacy generic dedupe reactivated MANUAL rejection", v, err)
		}
		if _, err := s.db.Exec(`UPDATE canary_admission_decisions SET decision='ADMITTED'`); err == nil {
			t.Fatal("direct SQL upgraded MANUAL legacy rejection into admission")
		}
		if _, err := s.db.Exec(`INSERT OR REPLACE INTO canary_admission_decisions SELECT id,request_id,operation_id,request_fingerprint,policy_version,'ADMITTED',reason_code,wallet_id,token_address,amount,reservation_id,created_at FROM canary_admission_decisions`); err == nil {
			t.Fatal("direct SQL replaced rejection with admission")
		}
		if _, err := s.db.Exec(`UPDATE canary_admission_attempts SET outcome='DEDUPED'`); err == nil {
			t.Fatal("direct SQL upgraded MANUAL rejected attempt")
		}
		if _, err := s.db.Exec(`INSERT INTO canary_admission_attempts SELECT 'forged',request_id,decision_id,operation_id,'ADMITTED',reason_code,created_at FROM canary_admission_attempts LIMIT 1`); err == nil {
			t.Fatal("direct SQL inserted MANUAL admitted attempt")
		}
		assertExposureNoAuthority(t, s)
	}
}

func TestHTTPC3B3FoundationLegacyRawPathRejectsTypedHistoryAndStopFirst(t *testing.T) {
	for _, stopped := range []int{0, 1} {
		d, s, h, p, digest, _ := exposureFixture(t)
		r := exposureOrder(t, h, "typed-history", "buy")
		// Released historic rows are not erased or guessed back into legacy
		// raw-scalar mode. This foundation's mode guard is global/permanent.
		seedExposure(t, s, r, p, digest, h.identity.WalletID, "released")
		stamp := testNow.Format(time.RFC3339Nano)
		if _, err := s.db.Exec(`INSERT INTO canary_control_state VALUES(1,4663,'CONTROLLED_CANARY',1,?,'fixture','fixture',?,?)`, stopped, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		legacy, err := trade.NewStore(d)
		if err != nil {
			t.Fatal(err)
		}
		v, err := legacy.AdmitControlledCanary(context.Background(), trade.CanaryRequest{RequestID: "legacy-after-typed", OperationID: "unbound-legacy", PolicyVersion: 1, WalletID: "wallet-02", TokenAddress: testToken, Amount: "1"}, testNow)
		want := "TYPED_ACCOUNTING_REQUIRES_MANUAL_PATH"
		if stopped == 1 {
			want = "EMERGENCY_STOPPED"
		}
		if !errors.Is(err, trade.ErrCanaryRejected) || v.Decision != "REJECTED" || v.ReasonCode != want || count(t, s.db, "canary_admission_attempts") != 1 || count(t, s.db, "canary_audit_events") != 1 {
			t.Fatal("legacy raw accounting bypass/stop priority", v, err)
		}
		if _, err := s.db.Exec(`INSERT INTO canary_risk_reservations(id,operation_id,wallet_id,token_address,policy_version,input_amount,gas_budget,state,created_at,updated_at) VALUES('legacy-new','legacy-new','wallet-02',?,1,'1','1','reserved',?,?)`, testToken, stamp, stamp); err == nil {
			t.Fatal("direct SQL mixed raw reservation after typed history")
		}
		if _, err := s.db.Exec(`INSERT INTO canary_admission_sources VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, *r.OperationID, h.identity.WalletID, testWallet, "PONS_V2_CURVE", "BUY", testToken, validationCurve, "CURVE", r.Validation.Output.Resolution.Token.CurveRuntimeCodeHash, r.Validation.Output.Resolution.AmountIn, "100000", "1000000000", "0", "2000000000000000000", 300, 0, 1, 100, r.Validation.Output.Resolution.Token.Block.Hash, r.ExpiresAt, r.Validation.ResultHash, stamp); err == nil {
			t.Fatal("direct SQL attached MANUAL source to legacy path")
		}
		assertExposureNoAuthority(t, s)
	}
}

func TestHTTPC3B3FoundationSameWriterReadsUncommittedBindingAndRollback(t *testing.T) {
	_, s, h, p, digest, _ := exposureFixture(t)
	r := exposureOrder(t, h, "writer-visibility", "buy")
	before := count(t, s.db, "canary_runtime_audit")
	withManualInputTransaction(t, s, func(ctx context.Context, conn *sql.Conn) {
		if err := exposureParent(ctx, conn, r, h.identity.WalletID, "frozen"); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, exposureUnitSQL, exposureUnitArgs(r, p, digest)...); err != nil {
			t.Fatal(err)
		}
		v, err := loadManualExposureTx(ctx, conn, h.identity.WalletID, p, testNow)
		if err != nil || v.WalletActiveOperations != 1 || v.GlobalWindowOperations != 1 || v.WalletOutlay.Units != "10100000000000000" {
			t.Fatal("loader did not read caller's exact writer snapshot", v, err)
		}
	})
	if count(t, s.db, "canary_runtime_audit") != before || count(t, s.db, "canary_alert_outbox") != before || count(t, s.db, "canary_risk_reservations") != 0 || count(t, s.db, "manual_reservation_units") != 0 {
		t.Fatal("read committed caller transaction or failed rollback")
	}
	assertExposureNoAuthority(t, s)
}

func TestHTTPC3B3FoundationExposureNoEconomicOrRPCDependencies(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "manual_exposure.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"context": true, "database/sql": true, "encoding/json": true, "strconv": true, "time": true, "github.com/ruzickahunzeker/rbh-bot/internal/chainquery": true, "github.com/ruzickahunzeker/rbh-bot/internal/manualrisk": true}
	for _, im := range f.Imports {
		if !allowed[strings.Trim(im.Path.Value, `"`)] {
			t.Fatal("new dependency", im.Path.Value)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			for _, forbidden := range []string{"ExecContext", "BeginTx", "Commit", "Submit", "ReplayUnknown", "SendRawTransaction", "Sign", "ReserveNonce", "Resolve", "Complete"} {
				if sel.Sel.Name == forbidden {
					t.Error("read-only foundation acquired side effect", forbidden)
				}
			}
		}
		return true
	})
	// Checked overflow remains a pure failure, never wrapping to free capacity.
	u := manualrisk.Unit{ChainID: 4663, Asset: "native", Decimals: 18, Kind: manualrisk.TradeInput}
	m := map[manualrisk.Unit]manualrisk.Quantity{u: {Unit: u, Units: "115792089237316195423570985008687907853269984665640564039457584007913129639935"}}
	if err := addManualExposure(m, manualrisk.Quantity{Unit: u, Units: "1"}); inputFailureCode(err) != "EXPOSURE_OVERFLOW" || m[u].Units != "115792089237316195423570985008687907853269984665640564039457584007913129639935" {
		t.Fatal("overflow mutated exposure", err)
	}
}

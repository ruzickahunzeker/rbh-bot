package tradeapi

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pons "github.com/0xfnzero/rbh-trade-sdk/adapters/pons"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

const validationCurve = "0x3000000000000000000000000000000000000003"

type validationBackend struct {
	resolves, completes atomic.Int64
	fault               string
	during              func()
	balance             atomic.Int64
	wallet              string // optional controlled multi-wallet fixture identity
}

func (b *validationBackend) Resolve(_ context.Context, i chainquery.ValidationIntent) (chainquery.Resolution, error) {
	b.resolves.Add(1)
	if b.fault == "resolve_failure" {
		return chainquery.Resolution{}, chainquery.ErrUnavailable
	}
	token := chainquery.Token{Chain: "robinhood", Address: testToken, Name: "Token", Symbol: "TKN", Decimals: 6, Protocol: "pons_v2_curve", Curve: validationCurve, Factory: chainquery.FactoryAddress, RuntimeCodeHash: common.HexToHash("0x123").Hex(), CurveRuntimeCodeHash: chainquery.CurveCodeHash, QuoteAsset: "native", Block: chainquery.Block{Number: 100, Hash: common.HexToHash("0x456").Hex()}}
	balance := b.balance.Load()
	if balance == 0 {
		balance = 1500500000
	}
	var amount string
	var err error
	if i.SellBPS != 0 {
		amount = new(big.Int).Div(new(big.Int).Mul(big.NewInt(balance), new(big.Int).SetUint64(i.SellBPS)), big.NewInt(10000)).String()
	} else {
		d := uint8(6)
		if i.Side == "buy" {
			d = 18
		}
		amount, err = HumanUnits(i.Amount, d)
		if err != nil {
			return chainquery.Resolution{}, err
		}
	}
	wallet := b.wallet
	if wallet == "" {
		wallet = testWallet
	}
	r := chainquery.Resolution{Token: token, Wallet: wallet, Side: i.Side, AmountIn: amount, NativeBalance: "2000000000000000000", TokenBalance: strconv.FormatInt(balance, 10), Allowance: "1000000000"}
	if i.MinReceive != "" {
		d := uint8(18)
		if i.Side == "buy" {
			d = 6
		}
		r.MinReceiveUnits, err = HumanUnits(i.MinReceive, d)
		if err != nil {
			return r, err
		}
	}
	return r, nil
}
func (b *validationBackend) Complete(_ context.Context, i chainquery.ValidationIntent, r chainquery.Resolution, l chainquery.ValidationLimits) (chainquery.ValidationOutput, error) {
	b.completes.Add(1)
	if b.during != nil {
		b.during()
	}
	if b.fault == "complete_failure" {
		return chainquery.ValidationOutput{}, chainquery.ErrUnavailable
	}
	out := big.NewInt(1000000)
	if i.Side == "sell" {
		out = big.NewInt(1000000000000000)
	}
	min := new(big.Int).Div(new(big.Int).Mul(out, new(big.Int).SetUint64(10000-i.SlippageBPS)), big.NewInt(10000))
	if r.MinReceiveUnits != "" {
		floor, _ := new(big.Int).SetString(r.MinReceiveUnits, 10)
		if floor.Cmp(min) > 0 {
			min = floor
		}
	}
	amount, _ := new(big.Int).SetString(r.AmountIn, 10)
	call, err := pons.BuildBuy(common.HexToAddress(validationCurve), amount, min, common.HexToAddress(r.Wallet), true)
	spent := r.AmountIn
	if i.Side == "sell" {
		call, err = pons.BuildSell(common.HexToAddress(validationCurve), amount, min, common.HexToAddress(r.Wallet))
		spent = "0"
	}
	if err != nil {
		return chainquery.ValidationOutput{}, err
	}
	fee, tip := i.MaxFeeWei, i.TipWei
	if fee == "" {
		fee = "2100000000"
	}
	if tip == "" {
		tip = "100000000"
	}
	f, _ := new(big.Int).SetString(fee, 10)
	gas := uint64(100000)
	result := chainquery.ValidationOutput{Resolution: r, QuotedOut: out.String(), MinimumOut: min.String(), QuoteSpent: spent, To: validationCurve, From: r.Wallet, Value: call.Value.String(), Data: hexutil.Encode(call.Data), GasLimit: gas, GasFeeCap: fee, GasTipCap: tip, GasBudget: new(big.Int).Mul(f, new(big.Int).SetUint64(gas)).String(), ReturnData: hexutil.Encode(common.LeftPadBytes(out.Bytes(), 32))}
	if b.fault == "wrong_amount" {
		result.Resolution.AmountIn = "1"
	}
	if b.fault == "wrong_calldata" {
		result.Data = "0x1234"
	}
	if b.fault == "wrong_budget" {
		result.GasBudget = "1"
	}
	return result, nil
}
func validationSeed(t *testing.T, s *Store, p validationPolicy) {
	t.Helper()
	if p.Version == 0 {
		p = validationPolicy{Version: 1, Protocol: "PONS_V2_CURVE", MaxOperation: "2000000000000000000", MaxWallet: "4000000000000000000", MaxToken: "4000000000000000000", MaxTotal: "8000000000000000000", MaxGas: "1000000", MaxFee: "3000000000", MaxTip: "1000000000", MinBalance: "1000000000000000", MaxSellBPS: 10000, MaxSlippageBPS: 500, MaxOperations: 100, WindowSeconds: 3600, MaxUnresolved: 1}
	}
	_, err := s.db.Exec(`INSERT INTO canary_policies(version,protocol,max_operation_input,max_sell_bps,max_wallet_exposure,max_token_exposure,max_total_exposure,max_operations_per_window,window_seconds,max_unresolved_steps_per_wallet,max_gas_limit,max_gas_fee_cap,max_gas_tip_cap,max_slippage_bps,min_native_balance,policy_hash,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.Version, p.Protocol, p.MaxOperation, p.MaxSellBPS, p.MaxWallet, p.MaxToken, p.MaxTotal, p.MaxOperations, p.WindowSeconds, p.MaxUnresolved, p.MaxGas, p.MaxFee, p.MaxTip, p.MaxSlippageBPS, p.MinBalance, hash([]byte(encode(p))), testNow.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`INSERT INTO canary_wallet_allowlist VALUES('wallet-01','` + testWallet + `',4663,` + strconv.FormatUint(p.Version, 10) + `,1,'2026-01-01T00:00:00Z')`, `INSERT INTO canary_token_allowlist VALUES('` + testToken + `','PONS_V2_CURVE',` + strconv.FormatUint(p.Version, 10) + `,1,'2026-01-01T00:00:00Z')`, `INSERT INTO canary_contract_allowlist VALUES('` + validationCurve + `','CURVE','PONS_V2_CURVE','` + chainquery.CurveCodeHash + `',` + strconv.FormatUint(p.Version, 10) + `,1,'C02','2026-01-01T00:00:00Z')`} {
		if _, err = s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}
func validationAPI(t *testing.T) (*storage.Database, *Store, *API, *validationBackend, string) {
	t.Helper()
	d, s, original, path := fixture(t)
	validationSeed(t, s, validationPolicy{})
	b := &validationBackend{}
	h, err := NewWithOptions(s, original.identity, testCredential, original.policy, nil, Options{ManualIntake: true, ValidationBackend: b, ValidationPolicyVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	h.now = original.now
	h.validator.now = original.now
	return d, s, h, b, path
}
func TestHTTPC2DurableValidationHTTPAndBinding(t *testing.T) {
	_, s, h, b, _ := validationAPI(t)
	r := manualRecord(t, call(h, "POST", "/v1/trades", "validate", encode(buy())))
	if r.Validation == nil || r.Validation.SendAuthorized || r.Validation.Status != "VALIDATED_NOT_AUTHORIZED" || r.Validation.PolicyVersion != 1 || r.Validation.Output.MinimumOut != "970000" || r.Validation.Output.GasFeeCap != "1000000000" {
		t.Fatal("result not bound")
	}
	var state, step string
	if err := s.db.QueryRow(`SELECT o.status,s.status FROM operations o JOIN execution_steps s ON s.operation_id=o.id`).Scan(&state, &step); err != nil {
		t.Fatal(err)
	}
	if state != "manual_validation_succeeded" || step != "validation_succeeded" {
		t.Fatal("second state machine or execution admission")
	}
	h.validator.now = func() time.Time { t.Fatal("duplicate consulted TTL"); return time.Time{} }
	dup := manualRecord(t, call(h, "POST", "/v1/trades", "validate", encode(buy())))
	if !dup.Duplicate || dup.Validation.ResultHash != r.Validation.ResultHash || b.resolves.Load() != 1 || b.completes.Load() != 1 {
		t.Fatal("duplicate revalidated/rebuilt")
	}
	for _, path := range []string{"/v1/trades/" + r.RequestID, "/v1/trades/by-idempotency-key"} {
		w := call(h, "GET", path, "validate", "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), r.Validation.ResultHash) {
			t.Fatal(w.Body.String())
		}
	}
	if count(t, s.db, "manual_trade_resolutions") != 1 || count(t, s.db, "manual_trade_validations") != 1 || count(t, s.db, "canary_runtime_audit") != 3 || count(t, s.db, "canary_alert_outbox") != 3 {
		t.Fatal("not atomic/exactly once")
	}
	noIntakeEconomicWork(t, s)
}
func TestHTTPC2ResolutionSurvivesFailureRestartWithoutNewPercentage(t *testing.T) {
	d, s, h, b, path := validationAPI(t)
	b.fault = "complete_failure"
	r := Request{Chain: "robinhood", WalletID: "wallet-01", Token: testToken, Side: "sell", SellPercent: "50", SlippagePercent: "3"}
	if w := call(h, "POST", "/v1/trades", "percent", encode(r)); w.Code != http.StatusServiceUnavailable {
		t.Fatal(w.Body.String())
	}
	if count(t, s.db, "manual_trade_resolutions") != 1 || count(t, s.db, "manual_trade_validations") != 0 {
		t.Fatal("resolution not durable before quote")
	}
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
	newBackend := &validationBackend{}
	newBackend.balance.Store(500000000)
	newAPI, err := NewWithOptions(store, h.identity, testCredential, DefaultPolicy(), nil, Options{ManualIntake: true, ValidationBackend: newBackend, ValidationPolicyVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	newAPI.now = h.now
	newAPI.validator.now = h.now
	completed := manualRecord(t, call(newAPI, "POST", "/v1/trades", "percent", encode(r)))
	if completed.Validation.Output.Resolution.AmountIn != "750250000" || completed.ExpiresAt != testNow.Add(30*time.Second).Format(time.RFC3339Nano) || newBackend.resolves.Load() != 0 || newBackend.completes.Load() != 1 {
		t.Fatal("restart re-resolved percent")
	}
	newAPI.validator.now = func() time.Time { return testNow.Add(30 * time.Second) }
	manualRecord(t, call(newAPI, "POST", "/v1/trades", "percent", encode(r)))
	noIntakeEconomicWork(t, store)
}
func TestHTTPC2PostRPCAndCommitTTLBoundaries(t *testing.T) {
	for _, fault := range []string{"admission", "post_rpc", "zero_clock"} {
		t.Run(fault, func(t *testing.T) {
			_, s, h, b, _ := validationAPI(t)
			clock := testNow
			h.validator.now = func() time.Time { return clock }
			if fault == "admission" {
				clock = testNow.Add(30 * time.Second)
			} else {
				b.during = func() {
					clock = testNow.Add(30 * time.Second)
					if fault == "zero_clock" {
						clock = time.Time{}
					}
				}
			}
			if w := call(h, "POST", "/v1/trades", "expiry", encode(buy())); w.Code != 422 || !strings.Contains(w.Body.String(), "APPLICATION_TTL_EXPIRED") {
				t.Fatal(w.Body.String())
			}
			if count(t, s.db, "manual_trade_validations") != 0 {
				t.Fatal("expired successful result durable")
			}
			if fault == "admission" && (b.resolves.Load() != 0 || b.completes.Load() != 0) {
				t.Fatal("expired RPC")
			}
			noIntakeEconomicWork(t, s)
		})
	}
}
func TestHTTPC2AtomicValidationFaultsAndImmutableSchema(t *testing.T) {
	for _, phase := range []string{"resolution", "result"} {
		for _, table := range []string{"self", "canary_runtime_audit", "canary_alert_outbox"} {
			t.Run(phase+table, func(t *testing.T) {
				_, s, h, b, _ := validationAPI(t)
				b.fault = "complete_failure"
				if phase == "result" {
					call(h, "POST", "/v1/trades", "atomic", encode(buy()))
				}
				b.fault = ""
				target := table
				if table == "self" {
					target = "manual_trade_resolutions"
					if phase == "result" {
						target = "manual_trade_validations"
					}
				}
				when := ""
				if table != "self" {
					event := "manual-resolution"
					if phase == "result" {
						event = "manual-validation"
					}
					when = " WHEN NEW.id LIKE '" + event + "%'"
				}
				if _, err := s.db.Exec(`CREATE TRIGGER fail_validation BEFORE INSERT ON ` + target + when + ` BEGIN SELECT RAISE(ABORT,'test fault'); END`); err != nil {
					t.Fatal(err)
				}
				if w := call(h, "POST", "/v1/trades", "atomic", encode(buy())); w.Code != http.StatusServiceUnavailable {
					t.Fatal(w.Body.String())
				}
				wantRes, wantAudit := 0, 1
				if phase == "result" {
					wantRes, wantAudit = 1, 2
				}
				if count(t, s.db, "manual_trade_resolutions") != wantRes || count(t, s.db, "manual_trade_validations") != 0 || count(t, s.db, "canary_runtime_audit") != wantAudit || count(t, s.db, "canary_alert_outbox") != wantAudit {
					t.Fatal("partial validation commit")
				}
				if _, err := s.db.Exec(`DROP TRIGGER fail_validation`); err != nil {
					t.Fatal(err)
				}
				manualRecord(t, call(h, "POST", "/v1/trades", "atomic", encode(buy())))
			})
		}
	}
	_, s, h, _, _ := validationAPI(t)
	manualRecord(t, call(h, "POST", "/v1/trades", "immutable", encode(buy())))
	for _, q := range []string{`UPDATE manual_trade_resolutions SET resolution_json='{}'`, `DELETE FROM manual_trade_resolutions`, `INSERT OR REPLACE INTO manual_trade_resolutions SELECT * FROM manual_trade_resolutions`, `UPDATE manual_trade_validations SET result_json='{}'`, `DELETE FROM manual_trade_validations`, `INSERT OR REPLACE INTO manual_trade_validations SELECT * FROM manual_trade_validations`, `UPDATE operations SET policy_version=2`, `UPDATE execution_steps SET quote_block_hash='0x00'`} {
		if _, err := s.db.Exec(q); err == nil {
			t.Fatal("immutable SQL bypass")
		}
	}
	noIntakeEconomicWork(t, s)
}

func TestHTTPC2ExpiryBeforeObservationCommitRollsBackEffects(t *testing.T) {
	for _, phase := range []string{"resolution", "result"} {
		t.Run(phase, func(t *testing.T) {
			_, s, h, b, _ := validationAPI(t)
			if phase == "result" {
				b.fault = "complete_failure"
				call(h, "POST", "/v1/trades", "commit-expiry", encode(buy()))
				b.fault = ""
			}
			// Each persistence transaction checks TTL at entry, at its write
			// timestamp, then immediately before commit. Expiry at that third
			// check rolls back observation, lifecycle, audit and outbox together.
			calls := 0
			clock := func() time.Time {
				calls++
				if calls >= 3 {
					return testNow.Add(30 * time.Second)
				}
				return testNow
			}
			r, _, err := validationSource(context.Background(), s.db, h.identity, "req_"+hash([]byte(h.identity.PrincipalID+"\x00commit-expiry")))
			if phase == "resolution" {
				// Record without running validation first.
				r, err = s.RecordManual(context.Background(), h.identity, "commit-expiry", buy(), h.policy, h.now)
			}
			if err != nil {
				t.Fatal(err)
			}
			p, digest, err := loadValidationPolicy(context.Background(), s.db, 1)
			if err != nil {
				t.Fatal(err)
			}
			i, err := intentFromRecord(r, h.identity.WalletAddress)
			if err != nil {
				t.Fatal(err)
			}
			resolution, err := b.Resolve(context.Background(), i)
			if err != nil {
				t.Fatal(err)
			}
			h.validator.now = clock
			wantRes, wantAudit := 0, 1
			if phase == "resolution" {
				_, _, _, err = h.validator.persistResolution(context.Background(), h.identity, r.RequestID, resolution, p, digest)
			} else {
				out, e := b.Complete(context.Background(), i, resolution, limitsFromPolicy(p, Record{validationMaxFee: h.policy.MaxFeeGwei}))
				if e != nil {
					t.Fatal(e)
				}
				_, err = h.validator.persistResult(context.Background(), h.identity, r.RequestID, out, p, digest)
				wantRes, wantAudit = 1, 2
			}
			if !errors.Is(err, chainquery.ErrExpired) || calls != 3 || count(t, s.db, "manual_trade_resolutions") != wantRes || count(t, s.db, "manual_trade_validations") != 0 || count(t, s.db, "canary_runtime_audit") != wantAudit || count(t, s.db, "canary_alert_outbox") != wantAudit {
				t.Fatal("expiry allowed partial observation commit", err, calls)
			}
			noIntakeEconomicWork(t, s)
		})
	}
}
func TestHTTPC2PolicySourcesLaneAndContradictoryAdapter(t *testing.T) {
	for _, fault := range []string{"missing_policy", "bad_attestation", "wallet_disabled", "frozen_lane", "wrong_amount", "wrong_calldata", "wrong_budget", "wallet_changes_during_rpc"} {
		t.Run(fault, func(t *testing.T) {
			_, s, h, b, _ := validationAPI(t)
			switch fault {
			case "missing_policy":
				h.validator.policyVersion = 99
			case "bad_attestation":
				if _, err := s.db.Exec(`INSERT OR REPLACE INTO canary_gate_attestations(gate,status,evidence_hash,attested_at) VALUES('C08','PASS','0000000000000000000000000000000000000000000000000000000000000000','2026-01-01T00:00:00Z')`); err != nil {
					t.Fatal(err)
				}
			case "wallet_disabled":
				b.during = func() { s.db.Exec(`UPDATE dry_run_wallets SET enabled=0`) }
			case "frozen_lane":
				s.db.Exec(`INSERT INTO execution_wallet_lanes(wallet_id,address,state,updated_at) VALUES('wallet-01','` + testWallet + `','frozen','2026-01-01T00:00:00Z')`)
			case "wallet_changes_during_rpc":
				b.during = func() { s.db.Exec(`UPDATE dry_run_wallets SET address='` + validationCurve + `'`) }
			default:
				b.fault = fault
			}
			w := call(h, "POST", "/v1/trades", "fail", encode(buy()))
			if w.Code == 202 || count(t, s.db, "manual_trade_validations") != 0 {
				t.Fatal("fault accepted: " + fault)
			}
		})
	}
}
func TestHTTPC2ConcurrentResolutionConservationAcrossHandles(t *testing.T) {
	_, s, h, _, path := validationAPI(t)
	d, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	second, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	b := &validationBackend{}
	h2, err := NewWithOptions(second, h.identity, testCredential, DefaultPolicy(), nil, Options{ManualIntake: true, ValidationBackend: b, ValidationPolicyVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	h2.now = h.now
	h2.validator.now = h.now
	var wg sync.WaitGroup
	results := make(chan string, 24)
	for j := 0; j < 24; j++ {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			handler := h
			if j%2 != 0 {
				handler = h2
			}
			w := call(handler, "POST", "/v1/trades", "race", encode(buy()))
			if w.Code != 202 {
				results <- w.Body.String()
				return
			}
			results <- ""
		}(j)
	}
	wg.Wait()
	close(results)
	for errorText := range results {
		if errorText != "" {
			t.Fatal(errorText)
		}
	}
	if count(t, s.db, "operations") != 1 || count(t, s.db, "manual_trade_resolutions") != 1 || count(t, s.db, "manual_trade_validations") != 1 || count(t, s.db, "canary_runtime_audit") != 3 || count(t, s.db, "canary_alert_outbox") != 3 {
		t.Fatal("validation conservation failed")
	}
	noIntakeEconomicWork(t, s)
}
func TestHTTPC2LegacyRejectedNeverValidates(t *testing.T) {
	_, s, h, b, _ := validationAPI(t)
	disabled, err := New(s, h.identity, testCredential, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	disabled.now = h.now
	record(t, call(disabled, "POST", "/v1/trades", "legacy", encode(buy())))
	record(t, call(h, "POST", "/v1/trades", "legacy", encode(buy())))
	if b.resolves.Load() != 0 || b.completes.Load() != 0 || count(t, s.db, "operations") != 0 {
		t.Fatal("legacy reject activated")
	}
	if _, err = h.validator.Validate(context.Background(), h.identity, "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing source accepted")
	}
}

func TestHTTPC2StoppedWithoutAuthorizationValidatesButNeverAdmits(t *testing.T) {
	_, s, h, _, _ := validationAPI(t)
	if _, err := s.db.Exec(`INSERT INTO canary_control_state VALUES(1,4663,'DISABLED',1,1,'test','test','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	r := manualRecord(t, call(h, "POST", "/v1/trades", "stopped-query", encode(buy())))
	if r.Validation == nil || r.Validation.SendAuthorized || r.SubmissionQueued {
		t.Fatal("read validation mistaken for admission")
	}
	noIntakeEconomicWork(t, s)
}

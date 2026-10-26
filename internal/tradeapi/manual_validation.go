package tradeapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
)

// Internal dependency only; never a caller-supplied arbitrary RPC or call.
type ManualValidationBackend interface {
	Resolve(context.Context, chainquery.ValidationIntent) (chainquery.Resolution, error)
	Complete(context.Context, chainquery.ValidationIntent, chainquery.Resolution, chainquery.ValidationLimits) (chainquery.ValidationOutput, error)
}
type ManualValidator struct {
	store         *Store
	backend       ManualValidationBackend
	policyVersion uint64
	now           func() time.Time
}
type ManualValidation struct {
	Status         string                      `json:"status"`
	PolicyVersion  uint64                      `json:"policy_version"`
	PolicyHash     string                      `json:"policy_hash"`
	ExpiresAt      string                      `json:"expires_at"`
	ResultHash     string                      `json:"result_hash"`
	Output         chainquery.ValidationOutput `json:"output"`
	SendAuthorized bool                        `json:"send_authorized"`
}

// JSON field ordering mirrors the CLOSED C07 policy hash, without importing
// its broad execution package or changing the existing policy implementation.
type validationPolicy struct {
	Version                                                                                   uint64
	Protocol, MaxOperation, MaxWallet, MaxToken, MaxTotal, MaxGas, MaxFee, MaxTip, MinBalance string
	MaxSellBPS, MaxSlippageBPS, MaxOperations, WindowSeconds, MaxUnresolved                   uint64
}
type queryRow interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func NewManualValidator(store *Store, backend ManualValidationBackend, policyVersion uint64) (*ManualValidator, error) {
	if store == nil || store.db == nil || backend == nil || policyVersion == 0 || policyVersion > 1<<63-1 {
		return nil, ErrInvalid
	}
	if _, _, err := loadValidationPolicy(context.Background(), store.db, policyVersion); err != nil {
		return nil, err
	}
	return &ManualValidator{store: store, backend: backend, policyVersion: policyVersion, now: time.Now}, nil
}
func ttlRecord(r Record, now time.Time) error {
	e, err := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	if err != nil || e.UTC().Format(time.RFC3339Nano) != r.ExpiresAt || now.IsZero() || !now.UTC().Before(e) {
		return chainquery.ErrExpired
	}
	return nil
}
func intentFromRecord(r Record, wallet string) (chainquery.ValidationIntent, error) {
	i := chainquery.ValidationIntent{Wallet: strings.ToLower(wallet), Token: r.Request.Token, Side: r.Request.Side, Amount: r.Request.Amount, MinReceive: r.Request.MinReceive, ExpiresAt: r.ExpiresAt}
	_, slip, err := decimal(r.Request.SlippagePercent, 2, true)
	if err != nil {
		return i, err
	}
	i.SlippageBPS = slip.Uint64()
	if r.Request.SellPercent != "" {
		_, bps, err := decimal(r.Request.SellPercent, 2, false)
		if err != nil {
			return i, err
		}
		i.SellBPS = bps.Uint64()
	}
	if r.Request.Fee != nil {
		for _, field := range []struct {
			value    string
			decimals int
			zero     bool
			out      *string
		}{{r.Request.Fee.MaxGwei, 9, false, &i.MaxFeeWei}, {r.Request.Fee.TipGwei, 9, true, &i.TipWei}, {r.Request.Fee.MaxTotalNative, 18, false, &i.MaxTotalWei}} {
			if field.value != "" {
				_, n, e := decimal(field.value, field.decimals, field.zero)
				if e != nil || n.BitLen() > 256 {
					return i, ErrInvalid
				}
				*field.out = n.String()
			}
		}
	}
	return i, nil
}
func loadValidationPolicy(ctx context.Context, q queryRow, version uint64) (validationPolicy, string, error) {
	var p validationPolicy
	var digest string
	err := q.QueryRowContext(ctx, `SELECT version,protocol,max_operation_input,max_wallet_exposure,max_token_exposure,max_total_exposure,max_gas_limit,max_gas_fee_cap,max_gas_tip_cap,min_native_balance,max_sell_bps,max_slippage_bps,max_operations_per_window,window_seconds,max_unresolved_steps_per_wallet,policy_hash FROM canary_policies WHERE version=?`, version).Scan(&p.Version, &p.Protocol, &p.MaxOperation, &p.MaxWallet, &p.MaxToken, &p.MaxTotal, &p.MaxGas, &p.MaxFee, &p.MaxTip, &p.MinBalance, &p.MaxSellBPS, &p.MaxSlippageBPS, &p.MaxOperations, &p.WindowSeconds, &p.MaxUnresolved, &digest)
	if err != nil {
		return p, "", err
	}
	if p.Version != version || p.Protocol != "PONS_V2_CURVE" || hash([]byte(encode(p))) != digest || p.MaxUnresolved != 1 || p.MaxSellBPS == 0 || p.MaxSellBPS > 10000 || p.MaxSlippageBPS >= 10000 || p.MaxOperations == 0 || p.WindowSeconds == 0 {
		return p, "", ErrPolicy
	}
	for _, field := range []struct {
		s    string
		zero bool
	}{{p.MaxOperation, false}, {p.MaxWallet, false}, {p.MaxToken, false}, {p.MaxTotal, false}, {p.MaxGas, false}, {p.MaxFee, false}, {p.MaxTip, true}, {p.MinBalance, true}} {
		n, ok := new(big.Int).SetString(field.s, 10)
		if !ok || n.String() != field.s || n.Sign() < 0 || (!field.zero && n.Sign() == 0) || n.BitLen() > 256 {
			return p, "", ErrPolicy
		}
	}
	// Durable gate identity, NOT merely configuration. This is a read validation
	// prerequisite, not a C07 decision or runtime authorization grant.
	for gate, want := range map[string]string{"C04": "c1af9454a6a4433b488158c25224d13dfbf31178c71dae7468c80917743381fd", "C05": "1a793f13d9304a31ed405f9fe25f1713cf79b6f8d28bb3da8ad16a01dc24c40b", "C08": "8178e7b976dbea67be1de83cec529171e56d154cd2114f1bf290bebbd1fc77d8"} {
		var state, evidence string
		if err = q.QueryRowContext(ctx, `SELECT status,evidence_hash FROM canary_gate_attestations WHERE gate=?`, gate).Scan(&state, &evidence); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return p, "", ErrPolicy
			}
			return p, "", err
		}
		if state != "PASS" || evidence != want {
			return p, "", ErrPolicy
		}
	}
	return p, digest, nil
}
func validationSource(ctx context.Context, q queryRow, id Identity, requestID string) (Record, string, error) {
	r, _, err := scanManualRecord(q.QueryRowContext(ctx, selectManualRecord+`WHERE m.id=? AND m.principal_id=? AND m.wallet_id=?`, requestID, id.PrincipalID, id.WalletID))
	if err != nil {
		return r, "", err
	}
	var address, opState, stepState, expiry, request string
	err = q.QueryRowContext(ctx, `SELECT m.wallet_address,o.status,s.status,o.expires_at,o.request_json FROM manual_http_order_sources m JOIN operations o ON o.id=m.operation_id JOIN execution_steps s ON s.id=m.step_id WHERE m.id=?`, requestID).Scan(&address, &opState, &stepState, &expiry, &request)
	if err != nil {
		return r, "", err
	}
	if common.HexToAddress(address) != common.HexToAddress(id.WalletAddress) || expiry != r.ExpiresAt || request != encode(r.Request) || !((opState == "manual_validation_pending" && stepState == "validation_pending") || (opState == "manual_parameters_resolved" && stepState == "parameters_resolved") || (opState == "manual_validation_succeeded" && stepState == "validation_succeeded")) {
		return r, "", ErrConflict
	}
	return r, address, nil
}
func validationWallet(ctx context.Context, q queryRow, id Identity, p validationPolicy, r *chainquery.Resolution) error {
	if err := checkManualWalletReusable(ctx, q, id); err != nil {
		return err
	}
	var address string
	var blocked bool
	err := q.QueryRowContext(ctx, `SELECT w.address,EXISTS(SELECT 1 FROM execution_wallet_lanes WHERE wallet_id=w.id AND state!='idle') OR EXISTS(SELECT 1 FROM execution_reservations WHERE wallet_id=w.id AND status IN ('reserved','signed','frozen')) OR EXISTS(SELECT 1 FROM canary_risk_reservations WHERE wallet_id=w.id AND state IN ('reserved','frozen')) OR EXISTS(SELECT 1 FROM execution_steps WHERE wallet_id=w.id AND status IN ('nonce_reserved','signing','signed','submitted','broadcast_unknown','reconciling','manual_resolution')) FROM dry_run_wallets w JOIN canary_wallet_allowlist a ON a.wallet_id=w.id AND a.chain_id=4663 AND a.policy_version=? AND a.enabled=1 AND lower(a.address)=lower(w.address) WHERE w.id=? AND w.chain_id=4663 AND w.enabled=1`, p.Version, id.WalletID).Scan(&address, &blocked)
	if err != nil {
		return err
	}
	if blocked || common.HexToAddress(address) != common.HexToAddress(id.WalletAddress) {
		return ErrWallet
	}
	if r != nil {
		var n int
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM canary_contract_allowlist c JOIN canary_token_allowlist t ON t.policy_version=c.policy_version WHERE c.policy_version=? AND lower(c.address)=? AND c.role='CURVE' AND c.protocol='PONS_V2_CURVE' AND c.runtime_code_hash=? AND c.enabled=1 AND c.baseline_ref!='' AND lower(t.token_address)=? AND t.protocol='PONS_V2_CURVE' AND t.enabled=1`, p.Version, r.Token.Curve, r.Token.CurveRuntimeCodeHash, r.Token.Address).Scan(&n)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrPolicy
		}
	}
	return nil
}
func limitsFromPolicy(p validationPolicy, r Record) chainquery.ValidationLimits {
	max, _ := new(big.Int).SetString(p.MaxFee, 10)
	_, api, _ := decimalFromRecordPolicy(r)
	if api.Cmp(max) < 0 {
		max = api
	}
	return chainquery.ValidationLimits{MaxInput: p.MaxOperation, MaxGas: p.MaxGas, MaxFee: max.String(), MaxTip: p.MaxTip, MinNative: p.MinBalance}
}

// API policy snapshot is loaded from immutable source rather than current cfg.
func decimalFromRecordPolicy(r Record) (string, *big.Int, error) {
	// Filled by loadPlan through source api_policy_json; no new API default.
	return decimal(r.validationMaxFee, 9, false)
}

func (v *ManualValidator) Validate(ctx context.Context, id Identity, requestID string) (ManualValidation, error) {
	if v == nil || v.store == nil || ctx == nil || !id.valid() || !validID(requestID) || v.now == nil {
		return ManualValidation{}, ErrInvalid
	}
	r, wallet, err := validationSource(ctx, v.store.db, id, requestID)
	if err != nil {
		return ManualValidation{}, err
	}
	resolution, p, digest, exists, err := v.loadResolution(ctx, *r.OperationID)
	if err != nil {
		return ManualValidation{}, err
	}
	if exists {
		if result, found, err := v.loadResult(ctx, *r.OperationID, resolution, p, digest, r.ExpiresAt); err != nil || found {
			return result, err
		}
	}
	if err = ttlRecord(r, v.now()); err != nil {
		return ManualValidation{}, err
	}
	if exists && p.Version != v.policyVersion {
		return ManualValidation{}, ErrConflict
	}
	if !exists {
		p, digest, err = loadValidationPolicy(ctx, v.store.db, v.policyVersion)
		if err != nil {
			return ManualValidation{}, err
		}
	}
	if err = validationWallet(ctx, v.store.db, id, p, nil); err != nil {
		return ManualValidation{}, err
	}
	intent, err := intentFromRecord(r, wallet)
	if err != nil {
		return ManualValidation{}, err
	}
	if intent.SlippageBPS > p.MaxSlippageBPS || intent.SellBPS > p.MaxSellBPS {
		return ManualValidation{}, ErrPolicy
	}
	var apiJSON string
	if err = v.store.db.QueryRowContext(ctx, `SELECT api_policy_json FROM manual_http_order_sources WHERE operation_id=?`, *r.OperationID).Scan(&apiJSON); err != nil {
		return ManualValidation{}, err
	}
	var api Policy
	if json.Unmarshal([]byte(apiJSON), &api) != nil {
		return ManualValidation{}, ErrInvalid
	}
	r.validationMaxFee = api.MaxFeeGwei
	if !exists {
		resolution, err = v.backend.Resolve(ctx, intent)
		if err != nil {
			return ManualValidation{}, err
		}
		resolution, p, digest, err = v.persistResolution(ctx, id, requestID, resolution, p, digest)
		if err != nil {
			return ManualValidation{}, err
		}
	}
	if err = ttlRecord(r, v.now()); err != nil {
		return ManualValidation{}, err
	}
	output, err := v.backend.Complete(ctx, intent, resolution, limitsFromPolicy(p, r))
	if err != nil {
		return ManualValidation{}, err
	}
	if output.Resolution != resolution {
		return ManualValidation{}, ErrConflict
	}
	if result, found, err := v.loadResult(ctx, *r.OperationID, resolution, p, digest, r.ExpiresAt); err != nil || found {
		return result, err
	}
	if err = chainquery.CheckValidation(intent, resolution, limitsFromPolicy(p, r), output); err != nil {
		return ManualValidation{}, err
	}
	return v.persistResult(ctx, id, requestID, output, p, digest)
}

func (v *ManualValidator) loadResolution(ctx context.Context, operation string) (chainquery.Resolution, validationPolicy, string, bool, error) {
	return readResolution(ctx, v.store.db, operation)
}
func readResolution(ctx context.Context, q queryRow, operation string) (chainquery.Resolution, validationPolicy, string, bool, error) {
	var r chainquery.Resolution
	var p validationPolicy
	var raw, digest, praw, phash, expiry, sourceExpiry, wallet, request string
	var version uint64
	err := q.QueryRowContext(ctx, `SELECT r.resolution_json,r.resolution_hash,r.policy_json,r.policy_hash,r.policy_version,r.expires_at,m.expires_at,m.wallet_address,m.request_json FROM manual_trade_resolutions r JOIN manual_http_order_sources m ON m.operation_id=r.operation_id WHERE r.operation_id=?`, operation).Scan(&raw, &digest, &praw, &phash, &version, &expiry, &sourceExpiry, &wallet, &request)
	if errors.Is(err, sql.ErrNoRows) {
		return r, p, "", false, nil
	}
	if err != nil {
		return r, p, "", false, err
	}
	if json.Unmarshal([]byte(raw), &r) != nil || encode(r) != raw || hash([]byte(raw)) != digest || json.Unmarshal([]byte(praw), &p) != nil || encode(p) != praw || hash([]byte(praw)) != phash || p.Version != version || expiry != sourceExpiry {
		return r, p, "", false, ErrConflict
	}
	var source Record
	if decodeRequest([]byte(request), &source.Request) != nil {
		return r, p, "", false, ErrConflict
	}
	source.ExpiresAt = sourceExpiry
	i, err := intentFromRecord(source, wallet)
	if err != nil || chainquery.CheckResolution(i, r) != nil {
		return r, p, "", false, ErrConflict
	}
	return r, p, phash, true, nil
}
func (v *ManualValidator) loadResult(ctx context.Context, op string, r chainquery.Resolution, p validationPolicy, phash, expiry string) (ManualValidation, bool, error) {
	var raw, digest string
	err := v.store.db.QueryRowContext(ctx, `SELECT result_json,result_hash FROM manual_trade_validations WHERE operation_id=?`, op).Scan(&raw, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return ManualValidation{}, false, nil
	}
	if err != nil {
		return ManualValidation{}, false, err
	}
	var out chainquery.ValidationOutput
	if json.Unmarshal([]byte(raw), &out) != nil || encode(out) != raw || hash([]byte(raw)) != digest || out.Resolution != r {
		return ManualValidation{}, false, ErrConflict
	}
	source, _, err := scanManualRecord(v.store.db.QueryRowContext(ctx, selectManualRecord+`WHERE m.operation_id=?`, op))
	if err != nil {
		return ManualValidation{}, false, err
	}
	var policyRaw string
	if err = v.store.db.QueryRowContext(ctx, `SELECT api_policy_json FROM manual_http_order_sources WHERE operation_id=?`, op).Scan(&policyRaw); err != nil {
		return ManualValidation{}, false, err
	}
	var api Policy
	if json.Unmarshal([]byte(policyRaw), &api) != nil {
		return ManualValidation{}, false, ErrInvalid
	}
	source.validationMaxFee = api.MaxFeeGwei
	i, err := intentFromRecord(source, r.Wallet)
	if err != nil {
		return ManualValidation{}, false, err
	}
	if err = chainquery.CheckValidation(i, r, limitsFromPolicy(p, source), out); err != nil {
		return ManualValidation{}, false, err
	}
	return ManualValidation{Status: "VALIDATED_NOT_AUTHORIZED", PolicyVersion: p.Version, PolicyHash: phash, ExpiresAt: expiry, ResultHash: digest, Output: out}, true, nil
}

func (v *ManualValidator) persistResolution(ctx context.Context, id Identity, key string, candidate chainquery.Resolution, p validationPolicy, digest string) (chainquery.Resolution, validationPolicy, string, error) {
	conn, err := v.store.db.Conn(ctx)
	if err != nil {
		return candidate, p, digest, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return candidate, p, digest, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	r, wallet, err := validationSource(ctx, conn, id, key)
	if err != nil {
		return candidate, p, digest, err
	}
	old, oldP, existingHash, found, err := readResolution(ctx, conn, *r.OperationID)
	if err != nil {
		return candidate, p, digest, err
	}
	if found {
		if oldP != p || existingHash != digest {
			return candidate, p, digest, ErrConflict
		}
		_, err = conn.ExecContext(ctx, "COMMIT")
		return old, oldP, existingHash, err
	}
	if err = ttlRecord(r, v.now()); err != nil {
		return candidate, p, digest, err
	}
	loaded, h, err := loadValidationPolicy(ctx, conn, p.Version)
	if err != nil || h != digest || loaded != p {
		return candidate, p, digest, ErrPolicy
	}
	i, err := intentFromRecord(r, wallet)
	if err != nil {
		return candidate, p, digest, err
	}
	if err = chainquery.CheckResolution(i, candidate); err != nil {
		return candidate, p, digest, err
	}
	amount, _ := new(big.Int).SetString(candidate.AmountIn, 10)
	cap, _ := new(big.Int).SetString(p.MaxOperation, 10)
	if amount.Cmp(cap) > 0 {
		return candidate, p, digest, ErrPolicy
	}
	if err = validationWallet(ctx, conn, id, p, &candidate); err != nil {
		return candidate, p, digest, err
	}
	stamp := v.now().UTC()
	if err = ttlRecord(r, stamp); err != nil {
		return candidate, p, digest, err
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO manual_trade_resolutions(operation_id,resolution_json,resolution_hash,policy_version,policy_hash,policy_json,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?)`, *r.OperationID, encode(candidate), hash([]byte(encode(candidate))), p.Version, digest, encode(p), r.ExpiresAt, stamp.Format(time.RFC3339Nano))
	if err != nil {
		return candidate, p, digest, err
	}
	if err = ttlRecord(r, v.now()); err != nil {
		return candidate, p, digest, err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return candidate, p, digest, err
}
func (v *ManualValidator) persistResult(ctx context.Context, id Identity, key string, out chainquery.ValidationOutput, p validationPolicy, digest string) (ManualValidation, error) {
	conn, err := v.store.db.Conn(ctx)
	if err != nil {
		return ManualValidation{}, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return ManualValidation{}, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	r, _, err := validationSource(ctx, conn, id, key)
	if err != nil {
		return ManualValidation{}, err
	}
	var existingRaw, existingHash string
	err = conn.QueryRowContext(ctx, `SELECT result_json,result_hash FROM manual_trade_validations WHERE operation_id=?`, *r.OperationID).Scan(&existingRaw, &existingHash)
	if err == nil {
		var existing chainquery.ValidationOutput
		if json.Unmarshal([]byte(existingRaw), &existing) != nil || encode(existing) != existingRaw || hash([]byte(existingRaw)) != existingHash || existing.Resolution != out.Resolution {
			return ManualValidation{}, ErrConflict
		}
		_, err = conn.ExecContext(ctx, "COMMIT")
		return ManualValidation{Status: "VALIDATED_NOT_AUTHORIZED", PolicyVersion: p.Version, PolicyHash: digest, ExpiresAt: r.ExpiresAt, ResultHash: existingHash, Output: existing}, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ManualValidation{}, err
	}
	if err = ttlRecord(r, v.now()); err != nil {
		return ManualValidation{}, err
	}
	loaded, h, err := loadValidationPolicy(ctx, conn, p.Version)
	if err != nil || h != digest || loaded != p {
		return ManualValidation{}, ErrPolicy
	}
	if err = validationWallet(ctx, conn, id, p, &out.Resolution); err != nil {
		return ManualValidation{}, err
	}
	raw := encode(out)
	resultHash := hash([]byte(raw))
	stamp := v.now().UTC()
	if err = ttlRecord(r, stamp); err != nil {
		return ManualValidation{}, err
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO manual_trade_validations(operation_id,result_json,result_hash,created_at,send_authorized) VALUES(?,?,?,?,0)`, *r.OperationID, raw, resultHash, stamp.Format(time.RFC3339Nano))
	if err != nil {
		return ManualValidation{}, err
	}
	if err = ttlRecord(r, v.now()); err != nil {
		return ManualValidation{}, err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return ManualValidation{Status: "VALIDATED_NOT_AUTHORIZED", PolicyVersion: p.Version, PolicyHash: digest, ExpiresAt: r.ExpiresAt, ResultHash: resultHash, Output: out}, err
}

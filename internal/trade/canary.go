package trade

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"
)

const (
	CanaryDisabled   = "DISABLED"
	CanaryControlled = "CONTROLLED_CANARY"
	CanaryProtocol   = "PONS_V2_CURVE"
)

var ErrCanaryRejected = errors.New("controlled canary admission rejected")
var requiredCanaryGates = map[string]string{"C04": "c1af9454a6a4433b488158c25224d13dfbf31178c71dae7468c80917743381fd", "C05": "1a793f13d9304a31ed405f9fe25f1713cf79b6f8d28bb3da8ad16a01dc24c40b", "C08": "8178e7b976dbea67be1de83cec529171e56d154cd2114f1bf290bebbd1fc77d8"}

type CanaryRequest struct {
	RequestID, OperationID, WalletID, WalletAddress, TokenAddress, ContractAddress, ContractRole, RuntimeCodeHash string
	Protocol, Direction, Amount, GasBudget, GasFeeCap, GasTipCap, NativeBalance                                   string
	GasLimit, SlippageBPS, SellBPS, PolicyVersion, QuoteBlockNumber                                               uint64
	QuoteBlockHash, ExpiresAt                                                                                     string
}
type CanaryDecision struct {
	Decision, ReasonCode, ReservationID string
	Duplicate                           bool
}
type CanaryMetrics struct{ Admitted, Deduped, Queued, Rejected, Reservations, EmergencyStopped int }
type canaryPolicy struct {
	Version                                                                                   uint64
	Protocol, MaxOperation, MaxWallet, MaxToken, MaxTotal, MaxGas, MaxFee, MaxTip, MinBalance string
	MaxSellBPS, MaxSlippageBPS, MaxOperations, WindowSeconds, MaxUnresolved                   uint64
}
type canarySource struct {
	OperationID, WalletID, WalletAddress, Protocol, Direction, TokenAddress, ContractAddress, ContractRole, RuntimeCodeHash, Amount, GasLimit, GasFeeCap, GasTipCap, NativeBalance string
	SlippageBPS, SellBPS, PolicyVersion, QuoteBlockNumber                                                                                                                          uint64
	QuoteBlockHash, ExpiresAt, SourceHash                                                                                                                                          string
}

func (s *Store) SetCanaryHookForTest(h func(string) error) { s.canaryHook = h }

func (s *Store) AdmitControlledCanary(ctx context.Context, r CanaryRequest, now time.Time) (CanaryDecision, error) {
	if s == nil || s.db == nil || ctx == nil || now.IsZero() || r.RequestID == "" || r.OperationID == "" || r.PolicyVersion == 0 {
		return CanaryDecision{}, ErrCanaryRejected
	}
	fingerprint, err := canaryFingerprint(r)
	if err != nil {
		return CanaryDecision{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CanaryDecision{}, err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339Nano)
	controlReason := canaryControlReason(ctx, tx, r.PolicyVersion)
	var existingID, existingFingerprint, existingReason string
	err = tx.QueryRowContext(ctx, `SELECT id,request_fingerprint,reason_code FROM canary_admission_decisions WHERE request_id=?`, r.RequestID).Scan(&existingID, &existingFingerprint, &existingReason)
	if err == nil {
		if existingFingerprint != fingerprint {
			return CanaryDecision{}, ErrIdempotencyConflict
		}
		outcome, reason := "DEDUPED", existingReason
		if controlReason != "" {
			outcome, reason = "REJECTED", controlReason
		}
		if err = s.insertCanaryAttempt(ctx, tx, r, existingID, outcome, reason, stamp); err != nil {
			return CanaryDecision{}, err
		}
		if err = tx.Commit(); err != nil {
			return CanaryDecision{}, err
		}
		v := CanaryDecision{Decision: outcome, ReasonCode: reason, Duplicate: true}
		if outcome == "REJECTED" {
			return v, ErrCanaryRejected
		}
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CanaryDecision{}, err
	}
	reason := controlReason
	var source canarySource
	var policy canaryPolicy
	if reason == "" {
		source, policy, reason = s.canaryGateReason(ctx, tx, r, now)
	}
	decision := "ADMITTED"
	if reason != "" {
		decision = "REJECTED"
	}
	did := canaryID("decision", r.RequestID)
	reservationID := ""
	if decision == "ADMITTED" {
		reservationID = canaryID("reservation", r.OperationID)
		window := now.UTC().Truncate(time.Duration(policy.WindowSeconds) * time.Second).Format(time.RFC3339Nano)
		var count int
		var reserved string
		err = tx.QueryRowContext(ctx, `SELECT operation_count,reserved_amount FROM canary_window_usage WHERE policy_version=? AND wallet_id=? AND token_address=? AND window_start=?`, r.PolicyVersion, source.WalletID, strings.ToLower(source.TokenAddress), window).Scan(&count, &reserved)
		if errors.Is(err, sql.ErrNoRows) {
			reserved = "0"
		} else if err != nil {
			return CanaryDecision{}, err
		}
		updated, ok := addCanonical(reserved, source.Amount)
		if !ok {
			return CanaryDecision{}, ErrCanaryRejected
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO canary_window_usage(policy_version,wallet_id,token_address,window_start,operation_count,reserved_amount,committed_amount,updated_at) VALUES(?,?,?,?,?,?,'0',?) ON CONFLICT(policy_version,wallet_id,token_address,window_start) DO UPDATE SET operation_count=excluded.operation_count,reserved_amount=excluded.reserved_amount,updated_at=excluded.updated_at`, r.PolicyVersion, source.WalletID, strings.ToLower(source.TokenAddress), window, count+1, updated, stamp); err != nil {
			return CanaryDecision{}, err
		}
		if err = s.canaryFault("after_usage"); err != nil {
			return CanaryDecision{}, err
		}
		gasBudget, ok := maximumGasCost(source.GasLimit, source.GasFeeCap)
		if !ok {
			return CanaryDecision{}, ErrCanaryRejected
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO canary_risk_reservations(id,operation_id,wallet_id,token_address,policy_version,input_amount,gas_budget,state,created_at,updated_at,quote_block_number,quote_block_hash,source_hash) VALUES(?,?,?,?,?,?,?,'reserved',?,?,?,?,?)`, reservationID, source.OperationID, source.WalletID, strings.ToLower(source.TokenAddress), source.PolicyVersion, source.Amount, gasBudget, stamp, stamp, source.QuoteBlockNumber, source.QuoteBlockHash, source.SourceHash); err != nil {
			return CanaryDecision{}, err
		}
		if err = s.canaryFault("after_reservation"); err != nil {
			return CanaryDecision{}, err
		}
	}
	if reason == "" {
		reason = "CANARY_ADMITTED"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO canary_admission_decisions(id,request_id,operation_id,request_fingerprint,policy_version,decision,reason_code,wallet_id,token_address,amount,reservation_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, did, r.RequestID, r.OperationID, fingerprint, r.PolicyVersion, decision, reason, r.WalletID, strings.ToLower(r.TokenAddress), r.Amount, nullText(reservationID), stamp); err != nil {
		return CanaryDecision{}, err
	}
	if err = s.canaryFault("after_decision"); err != nil {
		return CanaryDecision{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO canary_audit_events(id,decision_id,event_type,policy_version,reason_code,created_at) VALUES(?,?,'CANARY_ADMISSION_DECISION',?,?,?)`, canaryID("audit", did), did, r.PolicyVersion, reason, stamp); err != nil {
		return CanaryDecision{}, err
	}
	if err = s.insertCanaryAttempt(ctx, tx, r, did, decision, reason, stamp); err != nil {
		return CanaryDecision{}, err
	}
	if err = s.canaryFault("before_commit"); err != nil {
		return CanaryDecision{}, err
	}
	if err = tx.Commit(); err != nil {
		return CanaryDecision{}, err
	}
	v := CanaryDecision{Decision: decision, ReasonCode: reason, ReservationID: reservationID}
	if decision == "REJECTED" {
		return v, ErrCanaryRejected
	}
	return v, nil
}

func canaryControlReason(ctx context.Context, tx *sql.Tx, version uint64) string {
	var mode string
	var current uint64
	var stopped int
	if tx.QueryRowContext(ctx, `SELECT mode,policy_version,emergency_stopped FROM canary_control_state WHERE singleton=1 AND chain_id=4663`).Scan(&mode, &current, &stopped) != nil {
		return "CONTROL_STATE_UNAVAILABLE"
	}
	if stopped != 0 {
		return "EMERGENCY_STOPPED"
	}
	if mode != CanaryControlled {
		return "CANARY_DISABLED"
	}
	if current != version {
		return "POLICY_VERSION_MISMATCH"
	}
	return ""
}

func (s *Store) canaryGateReason(ctx context.Context, tx *sql.Tx, r CanaryRequest, now time.Time) (canarySource, canaryPolicy, string) {
	for gate, expected := range requiredCanaryGates {
		var status, hash string
		if tx.QueryRowContext(ctx, `SELECT status,evidence_hash FROM canary_gate_attestations WHERE gate=?`, gate).Scan(&status, &hash) != nil || status != "PASS" || hash != expected {
			return canarySource{}, canaryPolicy{}, "GATE_ATTESTATION_INVALID"
		}
	}
	source, err := loadCanarySource(ctx, tx, r.OperationID)
	if err != nil || !source.matches(r) {
		return canarySource{}, canaryPolicy{}, "DURABLE_SOURCE_MISMATCH"
	}
	expires, err := parseCanonicalExpiry(source.ExpiresAt)
	if err != nil || !now.UTC().Before(expires) {
		return canarySource{}, canaryPolicy{}, "TTL_EXPIRED"
	}
	policy, err := loadCanaryPolicy(ctx, tx, r.PolicyVersion)
	if err != nil || policy.Protocol != CanaryProtocol || policy.hash() != sourcePolicyHash(ctx, tx, r.PolicyVersion) {
		return canarySource{}, canaryPolicy{}, "POLICY_INTEGRITY_INVALID"
	}
	checks := []struct {
		q      string
		a      []any
		reason string
	}{{`SELECT 1 FROM canary_wallet_allowlist WHERE wallet_id=? AND lower(address)=lower(?) AND chain_id=4663 AND policy_version=? AND enabled=1`, []any{source.WalletID, source.WalletAddress, source.PolicyVersion}, "WALLET_NOT_ALLOWED"}, {`SELECT 1 FROM canary_contract_allowlist WHERE lower(address)=lower(?) AND role=? AND protocol='PONS_V2_CURVE' AND lower(runtime_code_hash)=lower(?) AND policy_version=? AND enabled=1`, []any{source.ContractAddress, source.ContractRole, source.RuntimeCodeHash, source.PolicyVersion}, "CONTRACT_NOT_ALLOWED"}, {`SELECT 1 FROM canary_token_allowlist WHERE lower(token_address)=lower(?) AND protocol='PONS_V2_CURVE' AND policy_version=? AND enabled=1`, []any{source.TokenAddress, source.PolicyVersion}, "TOKEN_NOT_ALLOWED"}}
	for _, c := range checks {
		var one int
		if tx.QueryRowContext(ctx, c.q, c.a...).Scan(&one) != nil {
			return canarySource{}, canaryPolicy{}, c.reason
		}
	}
	var active int
	if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM canary_risk_reservations WHERE wallet_id=? AND state IN ('reserved','frozen')`, source.WalletID).Scan(&active) != nil || active >= int(policy.MaxUnresolved) {
		return canarySource{}, canaryPolicy{}, "WALLET_LANE_BUSY"
	}
	return source, policy, canaryRiskReason(ctx, tx, source, policy, now)
}

func canaryRiskReason(ctx context.Context, tx *sql.Tx, s canarySource, p canaryPolicy, now time.Time) string {
	amount, ok := canonicalPositive(s.Amount)
	if !ok {
		return "RISK_INPUT_INVALID"
	}
	gasCost, ok := maximumGasCost(s.GasLimit, s.GasFeeCap)
	if !ok {
		return "RISK_INPUT_INVALID"
	}
	gas, _ := canonicalPositive(gasCost)
	fee, fo := canonicalPositive(s.GasFeeCap)
	tip, to := canonicalPositive(s.GasTipCap)
	balance, bo := canonicalPositive(s.NativeBalance)
	gasLimit, goK := canonicalPositive(s.GasLimit)
	if !fo || !to || !bo || !goK {
		return "RISK_INPUT_INVALID"
	}
	if exceeds(amount, p.MaxOperation) || exceeds(gasLimit, p.MaxGas) || exceeds(fee, p.MaxFee) || exceeds(tip, p.MaxTip) {
		return "OPERATION_LIMIT_EXCEEDED"
	}
	minimum, ok := new(big.Int).SetString(p.MinBalance, 10)
	if !ok {
		return "POLICY_INTEGRITY_INVALID"
	}
	required := new(big.Int).Add(new(big.Int).Set(gas), minimum)
	if s.Direction == "BUY" {
		required.Add(required, amount)
	}
	if balance.Cmp(required) < 0 {
		return "MINIMUM_BALANCE_VIOLATION"
	}
	if s.SlippageBPS > p.MaxSlippageBPS || (s.Direction == "SELL" && (s.SellBPS == 0 || s.SellBPS > p.MaxSellBPS)) {
		return "TRADE_LIMIT_EXCEEDED"
	}
	window := now.UTC().Truncate(time.Duration(p.WindowSeconds) * time.Second).Format(time.RFC3339Nano)
	var count int
	var reserved string
	err := tx.QueryRowContext(ctx, `SELECT operation_count,reserved_amount FROM canary_window_usage WHERE policy_version=? AND wallet_id=? AND token_address=? AND window_start=?`, s.PolicyVersion, s.WalletID, strings.ToLower(s.TokenAddress), window).Scan(&count, &reserved)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "USAGE_UNAVAILABLE"
	}
	if count >= int(p.MaxOperations) {
		return "RATE_LIMIT_EXCEEDED"
	}
	used := new(big.Int)
	used.SetString(reserved, 10)
	used.Add(used, amount)
	if exceeds(used, p.MaxWallet) {
		return "WALLET_EXPOSURE_EXCEEDED"
	}
	tokenTotal, e := sumReservationAmounts(ctx, tx, `SELECT input_amount FROM canary_risk_reservations WHERE policy_version=? AND lower(token_address)=lower(?) AND state IN ('reserved','frozen')`, s.PolicyVersion, s.TokenAddress)
	if e != nil {
		return "USAGE_UNAVAILABLE"
	}
	globalTotal, e := sumReservationAmounts(ctx, tx, `SELECT input_amount FROM canary_risk_reservations WHERE policy_version=? AND state IN ('reserved','frozen')`, s.PolicyVersion)
	if e != nil {
		return "USAGE_UNAVAILABLE"
	}
	tv, _ := new(big.Int).SetString(tokenTotal, 10)
	tv.Add(tv, amount)
	gv, _ := new(big.Int).SetString(globalTotal, 10)
	gv.Add(gv, amount)
	if exceeds(tv, p.MaxToken) {
		return "TOKEN_EXPOSURE_EXCEEDED"
	}
	if exceeds(gv, p.MaxTotal) {
		return "GLOBAL_EXPOSURE_EXCEEDED"
	}
	return ""
}

func (s *Store) insertCanaryAttempt(ctx context.Context, tx *sql.Tx, r CanaryRequest, decisionID, outcome, reason, stamp string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM canary_admission_attempts WHERE request_id=?`, r.RequestID).Scan(&n); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO canary_admission_attempts(id,request_id,decision_id,operation_id,outcome,reason_code,created_at) VALUES(?,?,?,?,?,?,?)`, canaryID("attempt", r.RequestID, strconv.Itoa(n+1)), r.RequestID, decisionID, r.OperationID, outcome, reason, stamp)
	return err
}
func (s *Store) canaryFault(stage string) error {
	if s.canaryHook != nil {
		return s.canaryHook(stage)
	}
	return nil
}
func (s *Store) CanaryMetrics(ctx context.Context) (CanaryMetrics, error) {
	var m CanaryMetrics
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM canary_admission_attempts WHERE outcome='ADMITTED'),(SELECT COUNT(*) FROM canary_admission_attempts WHERE outcome='DEDUPED'),(SELECT COUNT(*) FROM canary_admission_attempts WHERE outcome='QUEUED'),(SELECT COUNT(*) FROM canary_admission_attempts WHERE outcome='REJECTED'),(SELECT COUNT(*) FROM canary_risk_reservations WHERE state IN ('reserved','frozen')),(SELECT emergency_stopped FROM canary_control_state WHERE singleton=1)`).Scan(&m.Admitted, &m.Deduped, &m.Queued, &m.Rejected, &m.Reservations, &m.EmergencyStopped)
	if errors.Is(err, sql.ErrNoRows) {
		m.EmergencyStopped = 1
	}
	return m, err
}
func loadCanarySource(ctx context.Context, tx *sql.Tx, operation string) (canarySource, error) {
	var s canarySource
	err := tx.QueryRowContext(ctx, `SELECT operation_id,wallet_id,wallet_address,protocol,direction,token_address,contract_address,contract_role,runtime_code_hash,amount,gas_limit,gas_fee_cap,gas_tip_cap,native_balance,slippage_bps,sell_bps,policy_version,quote_block_number,quote_block_hash,expires_at,source_hash FROM canary_admission_sources WHERE operation_id=?`, operation).Scan(&s.OperationID, &s.WalletID, &s.WalletAddress, &s.Protocol, &s.Direction, &s.TokenAddress, &s.ContractAddress, &s.ContractRole, &s.RuntimeCodeHash, &s.Amount, &s.GasLimit, &s.GasFeeCap, &s.GasTipCap, &s.NativeBalance, &s.SlippageBPS, &s.SellBPS, &s.PolicyVersion, &s.QuoteBlockNumber, &s.QuoteBlockHash, &s.ExpiresAt, &s.SourceHash)
	if err == nil && s.hash() != s.SourceHash {
		return canarySource{}, ErrCanaryRejected
	}
	return s, err
}
func (s canarySource) matches(r CanaryRequest) bool {
	return s.OperationID == r.OperationID && s.WalletID == r.WalletID && strings.EqualFold(s.WalletAddress, r.WalletAddress) && s.Protocol == r.Protocol && s.Direction == r.Direction && strings.EqualFold(s.TokenAddress, r.TokenAddress) && strings.EqualFold(s.ContractAddress, r.ContractAddress) && s.ContractRole == r.ContractRole && strings.EqualFold(s.RuntimeCodeHash, r.RuntimeCodeHash) && s.Amount == r.Amount && s.GasLimit == strconv.FormatUint(r.GasLimit, 10) && s.GasFeeCap == r.GasFeeCap && s.GasTipCap == r.GasTipCap && s.NativeBalance == r.NativeBalance && s.SlippageBPS == r.SlippageBPS && s.SellBPS == r.SellBPS && s.PolicyVersion == r.PolicyVersion && s.QuoteBlockNumber == r.QuoteBlockNumber && strings.EqualFold(s.QuoteBlockHash, r.QuoteBlockHash) && s.ExpiresAt == r.ExpiresAt
}
func (s canarySource) hash() string {
	v := s
	v.SourceHash = ""
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func loadCanaryPolicy(ctx context.Context, tx *sql.Tx, v uint64) (canaryPolicy, error) {
	var p canaryPolicy
	err := tx.QueryRowContext(ctx, `SELECT version,protocol,max_operation_input,max_wallet_exposure,max_token_exposure,max_total_exposure,max_gas_limit,max_gas_fee_cap,max_gas_tip_cap,min_native_balance,max_sell_bps,max_slippage_bps,max_operations_per_window,window_seconds,max_unresolved_steps_per_wallet FROM canary_policies WHERE version=?`, v).Scan(&p.Version, &p.Protocol, &p.MaxOperation, &p.MaxWallet, &p.MaxToken, &p.MaxTotal, &p.MaxGas, &p.MaxFee, &p.MaxTip, &p.MinBalance, &p.MaxSellBPS, &p.MaxSlippageBPS, &p.MaxOperations, &p.WindowSeconds, &p.MaxUnresolved)
	return p, err
}
func (p canaryPolicy) hash() string {
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func sourcePolicyHash(ctx context.Context, tx *sql.Tx, v uint64) string {
	var h string
	_ = tx.QueryRowContext(ctx, `SELECT policy_hash FROM canary_policies WHERE version=?`, v).Scan(&h)
	return h
}
func maximumGasCost(limit, fee string) (string, bool) {
	l, lo := canonicalPositive(limit)
	f, fo := canonicalPositive(fee)
	if !lo || !fo {
		return "", false
	}
	return l.Mul(l, f).String(), true
}
func canonicalPositive(v string) (*big.Int, bool) {
	n, ok := new(big.Int).SetString(v, 10)
	return n, ok && n.Sign() > 0 && n.String() == v
}
func addCanonical(a, b string) (string, bool) {
	l, lo := new(big.Int).SetString(a, 10)
	r, ro := new(big.Int).SetString(b, 10)
	if !lo || !ro || l.Sign() < 0 || r.Sign() < 0 {
		return "", false
	}
	return l.Add(l, r).String(), true
}
func sumReservationAmounts(ctx context.Context, tx *sql.Tx, q string, args ...any) (string, error) {
	rows, e := tx.QueryContext(ctx, q, args...)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	total := new(big.Int)
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			return "", e
		}
		n, ok := new(big.Int).SetString(v, 10)
		if !ok || n.Sign() < 0 {
			return "", ErrCanaryRejected
		}
		total.Add(total, n)
	}
	return total.String(), rows.Err()
}
func exceeds(n *big.Int, limit string) bool {
	v, ok := canonicalPositive(limit)
	return !ok || n.Cmp(v) > 0
}
func canaryFingerprint(r CanaryRequest) (string, error) {
	b, e := json.Marshal(r)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func canaryID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}

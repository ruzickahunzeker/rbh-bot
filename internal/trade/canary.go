package trade

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	CanaryDisabled   = "DISABLED"
	CanaryControlled = "CONTROLLED_CANARY"
	CanaryProtocol   = "PONS_V2_CURVE"
)

var ErrCanaryRejected = errors.New("controlled canary admission rejected")

type CanaryRequest struct {
	RequestID, OperationID, WalletID, WalletAddress, TokenAddress, ContractAddress, ContractRole, RuntimeCodeHash string
	Protocol, Direction, Amount, GasBudget, GasFeeCap, GasTipCap, NativeBalance                                   string
	GasLimit, SlippageBPS, SellBPS                                                                                uint64
	PolicyVersion                                                                                                 uint64
	ExpiresAt                                                                                                     time.Time
}

type CanaryDecision struct {
	Decision, ReasonCode, ReservationID string
	Duplicate                           bool
}

type CanaryMetrics struct{ Admitted, Rejected, Reservations, EmergencyStopped int }

func (s *Store) AdmitControlledCanary(ctx context.Context, r CanaryRequest, now time.Time) (CanaryDecision, error) {
	if s == nil || s.db == nil || ctx == nil || now.IsZero() || r.RequestID == "" || r.OperationID == "" || r.PolicyVersion == 0 {
		return CanaryDecision{}, ErrCanaryRejected
	}
	amount, amountOK := canonicalPositive(r.Amount)
	gas, gasOK := canonicalPositive(r.GasBudget)
	if !amountOK || !gasOK {
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
	var existingFingerprint, existingDecision, existingReason string
	err = tx.QueryRowContext(ctx, `SELECT request_fingerprint,decision,reason_code FROM canary_admission_decisions WHERE request_id=?`, r.RequestID).Scan(&existingFingerprint, &existingDecision, &existingReason)
	if err == nil {
		if existingFingerprint != fingerprint {
			return CanaryDecision{}, ErrIdempotencyConflict
		}
		if err = tx.Commit(); err != nil {
			return CanaryDecision{}, err
		}
		return CanaryDecision{Decision: "DEDUPED", ReasonCode: existingReason, Duplicate: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CanaryDecision{}, err
	}
	reason := s.canaryGateReason(ctx, tx, r, now, amount, gas)
	decision := "ADMITTED"
	if reason != "" {
		decision = "REJECTED"
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	did := canaryID("decision", r.RequestID)
	reservationID := ""
	if decision == "ADMITTED" {
		reservationID = canaryID("reservation", r.OperationID)
		var windowSeconds int
		if err = tx.QueryRowContext(ctx, `SELECT window_seconds FROM canary_policies WHERE version=?`, r.PolicyVersion).Scan(&windowSeconds); err != nil || windowSeconds <= 0 {
			return CanaryDecision{}, ErrCanaryRejected
		}
		window := now.UTC().Truncate(time.Duration(windowSeconds) * time.Second).Format(time.RFC3339Nano)
		var currentCount int
		var currentReserved string
		err = tx.QueryRowContext(ctx, `SELECT operation_count,reserved_amount FROM canary_window_usage WHERE policy_version=? AND wallet_id=? AND token_address=? AND window_start=?`, r.PolicyVersion, r.WalletID, strings.ToLower(r.TokenAddress), window).Scan(&currentCount, &currentReserved)
		if errors.Is(err, sql.ErrNoRows) {
			currentReserved = "0"
		} else if err != nil {
			return CanaryDecision{}, err
		}
		updatedReserved, ok := addCanonical(currentReserved, r.Amount)
		if !ok {
			return CanaryDecision{}, ErrCanaryRejected
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO canary_window_usage(policy_version,wallet_id,token_address,window_start,operation_count,reserved_amount,committed_amount,updated_at) VALUES(?,?,?,?,?,?,'0',?) ON CONFLICT(policy_version,wallet_id,token_address,window_start) DO UPDATE SET operation_count=excluded.operation_count,reserved_amount=excluded.reserved_amount,updated_at=excluded.updated_at`, r.PolicyVersion, r.WalletID, strings.ToLower(r.TokenAddress), window, currentCount+1, updatedReserved, stamp); err != nil {
			return CanaryDecision{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO canary_risk_reservations(id,operation_id,wallet_id,token_address,policy_version,input_amount,gas_budget,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'reserved',?,?)`, reservationID, r.OperationID, r.WalletID, strings.ToLower(r.TokenAddress), r.PolicyVersion, r.Amount, r.GasBudget, stamp, stamp); err != nil {
			return CanaryDecision{}, err
		}
	} else if reason == "" {
		reason = "CANARY_REJECTED"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO canary_admission_decisions(id,request_id,operation_id,request_fingerprint,policy_version,decision,reason_code,wallet_id,token_address,amount,reservation_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, did, r.RequestID, r.OperationID, fingerprint, r.PolicyVersion, decision, reason, r.WalletID, strings.ToLower(r.TokenAddress), r.Amount, nullText(reservationID), stamp); err != nil {
		return CanaryDecision{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO canary_audit_events(id,decision_id,event_type,policy_version,reason_code,created_at) VALUES(?,?,'CANARY_ADMISSION_DECISION',?,?,?)`, canaryID("audit", did), did, r.PolicyVersion, reason, stamp); err != nil {
		return CanaryDecision{}, err
	}
	if err = tx.Commit(); err != nil {
		return CanaryDecision{}, err
	}
	result := CanaryDecision{Decision: decision, ReasonCode: reason, ReservationID: reservationID}
	if decision == "REJECTED" {
		return result, ErrCanaryRejected
	}
	return result, nil
}

func (s *Store) canaryGateReason(ctx context.Context, tx *sql.Tx, r CanaryRequest, now time.Time, amount, gas *big.Int) string {
	if r.Protocol != CanaryProtocol {
		return "PROTOCOL_NOT_ALLOWED"
	}
	if r.Direction != "BUY" && r.Direction != "SELL" {
		return "DIRECTION_NOT_ALLOWED"
	}
	if r.ExpiresAt.IsZero() || !now.UTC().Before(r.ExpiresAt.UTC()) {
		return "TTL_EXPIRED"
	}
	var mode string
	var version uint64
	var stopped int
	if tx.QueryRowContext(ctx, `SELECT mode,policy_version,emergency_stopped FROM canary_control_state WHERE singleton=1 AND chain_id=4663`).Scan(&mode, &version, &stopped) != nil {
		return "CONTROL_STATE_UNAVAILABLE"
	}
	if stopped != 0 {
		return "EMERGENCY_STOPPED"
	}
	if mode != CanaryControlled {
		return "CANARY_DISABLED"
	}
	if version != r.PolicyVersion {
		return "POLICY_VERSION_MISMATCH"
	}
	var gates int
	if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM canary_gate_attestations WHERE gate IN ('C04','C05','C08') AND status='PASS'`).Scan(&gates) != nil || gates != 3 {
		return "GATE_ATTESTATION_INVALID"
	}
	var maxOp, maxWallet, maxToken, maxTotal, maxGas, maxFee, maxTip, minBalance string
	var maxCount, windowSeconds int
	var maxSellBPS, maxSlippageBPS uint64
	if tx.QueryRowContext(ctx, `SELECT max_operation_input,max_wallet_exposure,max_token_exposure,max_total_exposure,max_gas_limit,max_gas_fee_cap,max_gas_tip_cap,min_native_balance,max_sell_bps,max_slippage_bps,max_operations_per_window,window_seconds FROM canary_policies WHERE version=? AND protocol='PONS_V2_CURVE'`, r.PolicyVersion).Scan(&maxOp, &maxWallet, &maxToken, &maxTotal, &maxGas, &maxFee, &maxTip, &minBalance, &maxSellBPS, &maxSlippageBPS, &maxCount, &windowSeconds) != nil {
		return "POLICY_NOT_FOUND"
	}
	for _, q := range []struct {
		sql    string
		args   []any
		reason string
	}{{`SELECT 1 FROM canary_wallet_allowlist WHERE wallet_id=? AND lower(address)=lower(?) AND chain_id=4663 AND policy_version=? AND enabled=1`, []any{r.WalletID, r.WalletAddress, r.PolicyVersion}, "WALLET_NOT_ALLOWED"}, {`SELECT 1 FROM canary_contract_allowlist WHERE lower(address)=lower(?) AND role=? AND protocol='PONS_V2_CURVE' AND lower(runtime_code_hash)=lower(?) AND policy_version=? AND enabled=1`, []any{r.ContractAddress, r.ContractRole, r.RuntimeCodeHash, r.PolicyVersion}, "CONTRACT_NOT_ALLOWED"}, {`SELECT 1 FROM canary_token_allowlist WHERE lower(token_address)=lower(?) AND protocol='PONS_V2_CURVE' AND policy_version=? AND enabled=1`, []any{r.TokenAddress, r.PolicyVersion}, "TOKEN_NOT_ALLOWED"}} {
		var one int
		if tx.QueryRowContext(ctx, q.sql, q.args...).Scan(&one) != nil {
			return q.reason
		}
	}
	fee, feeOK := canonicalPositive(r.GasFeeCap)
	tip, tipOK := canonicalPositive(r.GasTipCap)
	balance, balanceOK := canonicalPositive(r.NativeBalance)
	if !feeOK || !tipOK || !balanceOK || r.GasLimit == 0 {
		return "RISK_INPUT_INVALID"
	}
	if exceeds(amount, maxOp) || exceeds(gas, maxGas) || exceeds(new(big.Int).SetUint64(r.GasLimit), maxGas) || exceeds(fee, maxFee) || exceeds(tip, maxTip) {
		return "OPERATION_LIMIT_EXCEEDED"
	}
	minimum, ok := new(big.Int).SetString(minBalance, 10)
	if !ok || balance.Cmp(minimum) < 0 {
		return "MINIMUM_BALANCE_VIOLATION"
	}
	if r.SlippageBPS > maxSlippageBPS || (r.Direction == "SELL" && (r.SellBPS == 0 || r.SellBPS > maxSellBPS)) {
		return "TRADE_LIMIT_EXCEEDED"
	}
	window := now.UTC().Truncate(time.Duration(windowSeconds) * time.Second).Format(time.RFC3339Nano)
	var count int
	var reserved string
	err := tx.QueryRowContext(ctx, `SELECT operation_count,reserved_amount FROM canary_window_usage WHERE policy_version=? AND wallet_id=? AND token_address=? AND window_start=?`, r.PolicyVersion, r.WalletID, strings.ToLower(r.TokenAddress), window).Scan(&count, &reserved)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "USAGE_UNAVAILABLE"
	}
	if count >= maxCount {
		return "RATE_LIMIT_EXCEEDED"
	}
	used := new(big.Int)
	used.SetString(reserved, 10)
	used.Add(used, amount)
	if exceeds(used, maxWallet) {
		return "WALLET_EXPOSURE_EXCEEDED"
	}
	tokenReserved, err := sumReservationAmounts(ctx, tx, `SELECT input_amount FROM canary_risk_reservations WHERE policy_version=? AND lower(token_address)=lower(?) AND state IN ('reserved','frozen')`, r.PolicyVersion, r.TokenAddress)
	if err != nil {
		return "USAGE_UNAVAILABLE"
	}
	totalReserved, err := sumReservationAmounts(ctx, tx, `SELECT input_amount FROM canary_risk_reservations WHERE policy_version=? AND state IN ('reserved','frozen')`, r.PolicyVersion)
	if err != nil {
		return "USAGE_UNAVAILABLE"
	}
	tokenUsed := new(big.Int)
	tokenUsed.SetString(tokenReserved, 10)
	tokenUsed.Add(tokenUsed, amount)
	totalUsed := new(big.Int)
	totalUsed.SetString(totalReserved, 10)
	totalUsed.Add(totalUsed, amount)
	if exceeds(tokenUsed, maxToken) {
		return "TOKEN_EXPOSURE_EXCEEDED"
	}
	if exceeds(totalUsed, maxTotal) {
		return "GLOBAL_EXPOSURE_EXCEEDED"
	}
	return ""
}

func (s *Store) CanaryMetrics(ctx context.Context) (CanaryMetrics, error) {
	var m CanaryMetrics
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM canary_admission_decisions WHERE decision='ADMITTED'),(SELECT COUNT(*) FROM canary_admission_decisions WHERE decision='REJECTED'),(SELECT COUNT(*) FROM canary_risk_reservations WHERE state IN ('reserved','frozen')),(SELECT COALESCE(emergency_stopped,1) FROM canary_control_state WHERE singleton=1)`).Scan(&m.Admitted, &m.Rejected, &m.Reservations, &m.EmergencyStopped)
	return m, err
}
func canonicalPositive(v string) (*big.Int, bool) {
	n, ok := new(big.Int).SetString(v, 10)
	return n, ok && n.Sign() > 0 && n.String() == v
}
func addCanonical(a, b string) (string, bool) {
	left, okLeft := new(big.Int).SetString(a, 10)
	right, okRight := new(big.Int).SetString(b, 10)
	if !okLeft || !okRight || left.Sign() < 0 || right.Sign() < 0 {
		return "", false
	}
	return left.Add(left, right).String(), true
}
func sumReservationAmounts(ctx context.Context, tx *sql.Tx, query string, args ...any) (string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	total := new(big.Int)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return "", err
		}
		parsed, ok := new(big.Int).SetString(value, 10)
		if !ok || parsed.Sign() < 0 {
			return "", ErrCanaryRejected
		}
		total.Add(total, parsed)
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
func (r CanaryDecision) String() string { return fmt.Sprintf("%s:%s", r.Decision, r.ReasonCode) }

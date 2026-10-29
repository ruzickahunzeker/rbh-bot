package tradeapi

import (
	"context"
	"database/sql"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

// All types/functions here are package-private and disconnected. These are
// verified transaction-local inputs, NOT an admission decision or permission.
// Scope comes from a future operator-owned composition, NEVER HTTP parameters.
type manualAdmissionScope struct {
	AuthorizationID, ManualPolicyID                string
	AuthorizationEpoch                             uint64
	Environment, BuildSHA, ReleaseID, DeploymentID string
}

type manualControl struct {
	PolicyVersion          uint64
	EffectiveAt, UpdatedAt string
}

type manualAuthorizationInput struct {
	ID, ExpiresAt, CreatedAt, GateEvidenceSetHash string
	Epoch, MaxOperations                          uint64
	MaxTotalInput                                 string
}

type manualAdmissionInputs struct {
	Contract           ManualExecutionContract
	Policy             manualrisk.Policy
	PolicyHash         string
	Control            manualControl
	Authorization      manualAuthorizationInput
	Input, Gas, Outlay manualrisk.Quantity
	CheckedAt          string
}

type manualInputError struct{ Code string }

func (e *manualInputError) Error() string { return "manual transaction input rejected: " + e.Code }
func rejectManualInput(code string) error { return &manualInputError{code} }

func canonicalManualTime(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || t.IsZero() || t.Year() < 1970 || t.Year() > 9998 || t.UTC().Format(time.RFC3339Nano) != raw {
		return time.Time{}, ErrInvalid
	}
	return t, nil
}

func manualHex(raw string, sizes ...int) bool {
	if raw != strings.ToLower(raw) {
		return false
	}
	for _, size := range sizes {
		if len(raw) == size {
			_, err := hex.DecodeString(raw)
			return err == nil
		}
	}
	return false
}

func (s manualAdmissionScope) valid() bool {
	return validID(s.AuthorizationID) && validID(s.ManualPolicyID) && s.AuthorizationEpoch > 0 && s.AuthorizationEpoch <= 1<<63-1 &&
		validID(s.Environment) && validID(s.ReleaseID) && validID(s.DeploymentID) && manualHex(s.BuildSHA, 40, 64)
}

// readManualControl reads emergency stop BEFORE any source/policy/auth lookup.
// Missing/unreadable control fails closed; a durable stop has highest priority
// even if other input identity/policy/auth data is invalid.
func readManualControl(ctx context.Context, q queryRow, now time.Time) (manualControl, error) {
	var c manualControl
	var mode string
	var stop int
	err := q.QueryRowContext(ctx, `SELECT emergency_stopped,mode,policy_version,effective_at,updated_at FROM canary_control_state WHERE singleton=1 AND chain_id=4663`).Scan(&stop, &mode, &c.PolicyVersion, &c.EffectiveAt, &c.UpdatedAt)
	if err != nil {
		return c, manualVerificationError(err, "CONTROL_UNAVAILABLE")
	}
	if stop == 1 {
		return c, rejectManualInput("EMERGENCY_STOPPED")
	}
	effective, ee := canonicalManualTime(c.EffectiveAt)
	updated, ue := canonicalManualTime(c.UpdatedAt)
	if stop != 0 || ee != nil || ue != nil || now.IsZero() || effective.After(now) || updated.After(now) || updated.Before(effective) {
		return c, rejectManualInput("CONTROL_UNVERIFIABLE")
	}
	if mode != "CONTROLLED_CANARY" {
		return c, rejectManualInput("CANARY_DISABLED")
	}
	return c, nil
}

// Hash of the ordered, verified durable triples including their attestation
// stamps. New MANUAL authorization must bind this exact set, not an arbitrary
// caller-supplied digest. This does not alter legacy authorization semantics.
func manualGateEvidenceHash(ctx context.Context, q queryRow, now time.Time) (string, error) {
	type identity struct{ Gate, Status, EvidenceHash, AttestedAt string }
	identities := make([]identity, 0, 3)
	for _, expected := range [][2]string{
		{"C04", "c1af9454a6a4433b488158c25224d13dfbf31178c71dae7468c80917743381fd"},
		{"C05", "1a793f13d9304a31ed405f9fe25f1713cf79b6f8d28bb3da8ad16a01dc24c40b"},
		{"C08", "8178e7b976dbea67be1de83cec529171e56d154cd2114f1bf290bebbd1fc77d8"},
	} {
		v := identity{Gate: expected[0]}
		if err := q.QueryRowContext(ctx, `SELECT status,evidence_hash,attested_at FROM canary_gate_attestations WHERE gate=?`, v.Gate).Scan(&v.Status, &v.EvidenceHash, &v.AttestedAt); err != nil || v.Status != "PASS" || v.EvidenceHash != expected[1] {
			return "", manualVerificationError(err, "GATE_ATTESTATION_INVALID")
		}
		stamp, err := canonicalManualTime(v.AttestedAt)
		if err != nil || now.IsZero() || stamp.After(now) {
			return "", rejectManualInput("GATE_ATTESTATION_INVALID")
		}
		identities = append(identities, v)
	}
	return hash([]byte(encode(identities))), nil
}

func loadManualPolicy(ctx context.Context, q queryRow, id string, c ManualExecutionContract, now time.Time) (manualrisk.Policy, string, error) {
	var raw, digest, sourceHash, created string
	var version uint64
	err := q.QueryRowContext(ctx, `SELECT policy_json,policy_hash,source_policy_version,source_policy_hash,created_at FROM manual_risk_policies WHERE id=?`, id).Scan(&raw, &digest, &version, &sourceHash, &created)
	if err != nil || version != c.PolicyVersion || sourceHash != c.PolicyHash {
		return manualrisk.Policy{}, "", manualVerificationError(err, "MANUAL_POLICY_SOURCE_INVALID")
	}
	p, err := manualrisk.ParsePolicy(raw, digest, c.PolicyVersion, c.PolicyHash)
	stamp, te := canonicalManualTime(created)
	if err != nil || p.ID != id || te != nil || now.IsZero() || stamp.After(now) {
		return manualrisk.Policy{}, "", rejectManualInput("MANUAL_POLICY_INTEGRITY_INVALID")
	}
	return p, digest, nil
}

func loadManualAuthorizationIdentity(ctx context.Context, q queryRow, scope manualAdmissionScope, c ManualExecutionContract, p manualrisk.Policy, policyHash, gateHash string, now time.Time) (manualAuthorizationInput, error) {
	var a manualAuthorizationInput
	var state, mode, wallet, environment, build, release, deployment, ref, authorized, updated string
	var chain, policy uint64
	err := q.QueryRowContext(ctx, `SELECT id,epoch,state,mode,chain_id,wallet_id,policy_version,environment,build_sha,release_id,deployment_id,gate_evidence_set_hash,authorization_ref,max_operations,max_total_input,authorized_at,expires_at,created_at,updated_at FROM canary_runtime_authorizations WHERE id=?`, scope.AuthorizationID).Scan(
		&a.ID, &a.Epoch, &state, &mode, &chain, &wallet, &policy, &environment, &build, &release, &deployment, &a.GateEvidenceSetHash, &ref, &a.MaxOperations, &a.MaxTotalInput, &authorized, &a.ExpiresAt, &a.CreatedAt, &updated)
	if err != nil {
		return a, manualVerificationError(err, "AUTHORIZATION_UNAVAILABLE")
	}
	if state != "ARMED" {
		return a, rejectManualInput("AUTHORIZATION_NOT_ARMED")
	}
	if a.ID != scope.AuthorizationID || a.Epoch != scope.AuthorizationEpoch || mode != "CONTROLLED_CANARY" || chain != c.ChainID || wallet != c.WalletID || policy != c.PolicyVersion || environment != scope.Environment || build != scope.BuildSHA || release != scope.ReleaseID || deployment != scope.DeploymentID || ref == "" || a.GateEvidenceSetHash != gateHash {
		return a, rejectManualInput("AUTHORIZATION_BINDING_INVALID")
	}
	start, se := canonicalManualTime(authorized)
	expires, ee := canonicalManualTime(a.ExpiresAt)
	created, ce := canonicalManualTime(a.CreatedAt)
	update, ue := canonicalManualTime(updated)
	if se != nil || ee != nil || ce != nil || ue != nil || now.IsZero() || start.After(now) || created.After(now) || update.Before(created) || update.After(now) || !start.Before(expires) || !now.Before(expires) {
		return a, rejectManualInput("AUTHORIZATION_TIME_INVALID_OR_EXPIRED")
	}
	var declaredEpoch, declaredChain uint64
	var declaredPolicy, declaredHash, source, asset, kind, declarationStamp string
	var decimals uint8
	err = q.QueryRowContext(ctx, `SELECT authorization_epoch,manual_policy_id,manual_policy_hash,source_mode,chain_id,input_asset,input_decimals,accounting_kind,created_at FROM manual_authorization_units WHERE authorization_id=?`, a.ID).Scan(&declaredEpoch, &declaredPolicy, &declaredHash, &source, &declaredChain, &asset, &decimals, &kind, &declarationStamp)
	unit := manualrisk.Unit{ChainID: declaredChain, Asset: asset, Decimals: decimals, Kind: kind}
	input := manualrisk.Unit{ChainID: c.Input.ChainID, Asset: c.Input.Asset, Decimals: c.Input.Decimals, Kind: manualrisk.TradeInput}
	if err != nil || declaredEpoch != a.Epoch || declaredPolicy != p.ID || declaredHash != policyHash || source != "MANUAL" || declarationStamp != a.CreatedAt || unit.Validate() != nil || unit != input {
		return a, manualVerificationError(err, "AUTHORIZATION_UNIT_INVALID")
	}
	if _, err = p.Limit(unit); err != nil {
		return a, rejectManualInput("AUTHORIZATION_UNIT_INVALID")
	}
	return a, nil
}

func loadManualAuthorization(ctx context.Context, q queryRow, scope manualAdmissionScope, c ManualExecutionContract, p manualrisk.Policy, policyHash, gateHash string, now time.Time) (manualAuthorizationInput, error) {
	a, err := loadManualAuthorizationIdentity(ctx, q, scope, c, p, policyHash, gateHash, now)
	if err != nil {
		return a, err
	}
	unit := manualrisk.Unit{ChainID: c.Input.ChainID, Asset: c.Input.Asset, Decimals: c.Input.Decimals, Kind: manualrisk.TradeInput}
	used, total, err := verifyManualAuthorizationUsage(ctx, q, a.ID, unit)
	if err != nil {
		return a, err
	}
	if (manualrisk.Budget{Unit: unit, MaxOperations: a.MaxOperations, MaxTotalInput: a.MaxTotalInput}).Check(used, total, manualrisk.Quantity{Unit: unit, Units: c.Input.Units}) != nil {
		return a, rejectManualInput("AUTHORIZATION_BUDGET_UNVERIFIABLE_OR_EXCEEDED")
	}
	return a, nil
}

// loadManualAdmissionInputsTx is intentionally not exported or wired to HTTP.
// The caller must own BEGIN IMMEDIATE on conn. This function performs SELECTs
// only on that same connection; it never begins/commits/rolls back, calls a
// validator backend, grants auth, writes reservations/usage, or creates permits.
func loadManualAdmissionInputsTx(ctx context.Context, conn *sql.Conn, id Identity, requestID string, scope manualAdmissionScope, now func() time.Time) (manualAdmissionInputs, error) {
	var v manualAdmissionInputs
	if ctx == nil || conn == nil || now == nil {
		return v, ErrInvalid
	}
	stamp := now().UTC()
	control, err := readManualControl(ctx, conn, stamp)
	if err != nil {
		return v, err
	}
	if !id.valid() || !validID(requestID) || !scope.valid() || stamp.IsZero() {
		return v, ErrInvalid
	}
	c, err := loadManualExecutionContractTx(ctx, conn, id, requestID, now)
	if err != nil {
		return v, err
	}
	if control.PolicyVersion != c.PolicyVersion {
		return v, rejectManualInput("CONTROL_POLICY_MISMATCH")
	}
	if err := checkManualWalletReusable(ctx, conn, id); err != nil {
		return v, err
	}
	p, phash, err := loadManualPolicy(ctx, conn, scope.ManualPolicyID, c, stamp)
	if err != nil {
		return v, err
	}
	input := manualrisk.Quantity{Unit: manualrisk.Unit{ChainID: c.Input.ChainID, Asset: c.Input.Asset, Decimals: c.Input.Decimals, Kind: manualrisk.TradeInput}, Units: c.Input.Units}
	limit, err := p.Limit(input.Unit)
	if err != nil || manualrisk.CheckCap(manualrisk.Quantity{Unit: input.Unit, Units: "0"}, input, manualrisk.Quantity{Unit: input.Unit, Units: limit.MaxOperation}) != nil {
		return v, rejectManualInput("MANUAL_OPERATION_INPUT_LIMIT")
	}
	gas, err := manualrisk.GasBudget(strconv.FormatUint(c.Output.GasLimit, 10), c.Output.GasFeeCap, c.Output.GasTipCap)
	if err != nil || gas.Units != c.GasBudget.Units || manualrisk.CheckCap(manualrisk.Quantity{Unit: gas.Unit, Units: "0"}, gas, manualrisk.Quantity{Unit: gas.Unit, Units: p.MaxNativeGasPerOperation}) != nil {
		return v, rejectManualInput("MANUAL_GAS_LIMIT")
	}
	outlay, err := manualrisk.Outlay(manualrisk.Quantity{Unit: manualrisk.Unit{ChainID: 4663, Asset: "native", Decimals: 18, Kind: manualrisk.NativeValue}, Units: c.NativeValue.Units}, gas)
	if err != nil || manualrisk.CheckRetainedBalance(c.Output.Resolution.NativeBalance, outlay.Units, p.MinNativeBalance) != nil {
		return v, rejectManualInput("MANUAL_RETAINED_BALANCE_LIMIT")
	}
	if manualrisk.CheckCap(manualrisk.Quantity{Unit: outlay.Unit, Units: "0"}, outlay, manualrisk.Quantity{Unit: outlay.Unit, Units: p.MaxNativeWalletOutlay}) != nil {
		return v, rejectManualInput("MANUAL_OPERATION_OUTLAY_LIMIT")
	}
	if c.Output.Resolution.Side == "sell" && manualrisk.CheckFixedSell(input.Units, c.Output.Resolution.TokenBalance, p.MaxSellBPS) != nil {
		return v, rejectManualInput("MANUAL_SELL_LIMIT")
	}
	gateHash, err := manualGateEvidenceHash(ctx, conn, stamp)
	if err != nil {
		return v, err
	}
	a, err := loadManualAuthorization(ctx, conn, scope, c, p, phash, gateHash, stamp)
	if err != nil {
		return v, err
	}
	final := now().UTC()
	expiry, err := canonicalManualTime(a.ExpiresAt)
	if err != nil || final.Before(stamp) || !final.Before(expiry) || ttlRecord(Record{ExpiresAt: c.ExpiresAt}, final) != nil {
		return v, rejectManualInput("FINAL_TIME_INVALID_OR_EXPIRED")
	}
	return manualAdmissionInputs{Contract: c, Policy: p, PolicyHash: phash, Control: control, Authorization: a, Input: input, Gas: gas, Outlay: outlay, CheckedAt: final.Format(time.RFC3339Nano)}, nil
}

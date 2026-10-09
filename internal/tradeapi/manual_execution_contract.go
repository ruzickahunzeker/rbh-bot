package tradeapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/big"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
)

// AssetQuantity has no addition/valuation API: quantities of different assets
// MUST NOT be summed. Native gas is separate from token/native trade input.
type AssetQuantity struct {
	ChainID  uint64 `json:"chain_id"`
	Asset    string `json:"asset"` // "native" or the verified token address
	Decimals uint8  `json:"decimals"`
	Units    string `json:"units"`
}

// ManualExecutionContract is a detached copy of durable C1/C2 parameters, NOT a
// reservation, signed artifact, gate snapshot, send permit or authorization.
// A future economic admission must reload it and atomically recheck controls;
// retaining this object across a stop/revoke/expiry never grants permission.
type ManualExecutionContract struct {
	Version             uint64                      `json:"version"`
	Status              string                      `json:"status"`
	Source              string                      `json:"source"`
	RequestID           string                      `json:"request_id"`
	PrincipalID         string                      `json:"principal_id"`
	OperationID         string                      `json:"operation_id"`
	StepID              string                      `json:"step_id"`
	WalletID            string                      `json:"wallet_id"`
	WalletAddress       string                      `json:"wallet_address"`
	ChainID             uint64                      `json:"chain_id"`
	RequestFingerprint  string                      `json:"request_fingerprint"`
	Request             Request                     `json:"request"`
	APIPolicyVersion    uint64                      `json:"api_policy_version"`
	APIPolicyHash       string                      `json:"api_policy_hash"`
	PolicyVersion       uint64                      `json:"policy_version"`
	PolicyHash          string                      `json:"policy_hash"`
	ResolutionHash      string                      `json:"resolution_hash"`
	ResultHash          string                      `json:"result_hash"`
	CreatedAt           string                      `json:"created_at"`
	ExpiresAt           string                      `json:"expires_at"`
	DeadlineCapability  string                      `json:"deadline_capability"`
	ContractDeadline    bool                        `json:"contract_deadline"`
	Output              chainquery.ValidationOutput `json:"output"`
	Input               AssetQuantity               `json:"input"`
	QuotedOutput        AssetQuantity               `json:"quoted_output"`
	MinimumOutput       AssetQuantity               `json:"minimum_output"`
	GasBudget           AssetQuantity               `json:"gas_budget"`
	NativeValue         AssetQuantity               `json:"native_value"`
	AdmissionAuthorized bool                        `json:"admission_authorized"`
	SendAuthorized      bool                        `json:"send_authorized"`
	BindingHash         string                      `json:"binding_hash"`
}

// ManualExecutionReader has only a store and clock. No backend, execution
// kernel, signer, reservation service, nonce allocator or submission dependency.
// C3-A deliberately adds no HTTP route or production composition.
type ManualExecutionReader struct {
	store *Store
	now   func() time.Time
}

func NewManualExecutionReader(store *Store) (*ManualExecutionReader, error) {
	if store == nil || store.db == nil {
		return nil, ErrInvalid
	}
	return &ManualExecutionReader{store: store, now: time.Now}, nil
}

// Load accepts scoped identity and a durable request ID, never caller-supplied
// security/transaction fields. All SELECTs share a single SQLite read snapshot.
// Unlike historical GET/duplicate validation, an expired contract is rejected.
func (a *ManualExecutionReader) Load(ctx context.Context, id Identity, requestID string) (ManualExecutionContract, error) {
	if a == nil || a.store == nil || a.store.db == nil || a.now == nil || ctx == nil || !id.valid() || !validID(requestID) {
		return ManualExecutionContract{}, ErrInvalid
	}
	tx, err := a.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ManualExecutionContract{}, err
	}
	defer tx.Rollback()
	r, wallet, err := validationSource(ctx, tx, id, requestID)
	if err != nil {
		return ManualExecutionContract{}, err
	}
	if err = ttlRecord(r, a.now()); err != nil {
		return ManualExecutionContract{}, err
	}
	resolution, p, digest, found, err := readResolution(ctx, tx, *r.OperationID)
	if err != nil {
		return ManualExecutionContract{}, err
	}
	if !found {
		return ManualExecutionContract{}, ErrConflict
	}
	current, currentHash, err := loadValidationPolicy(ctx, tx, p.Version)
	if err != nil {
		return ManualExecutionContract{}, err
	}
	if current != p || currentHash != digest {
		return ManualExecutionContract{}, ErrConflict
	}
	if err = validationWallet(ctx, tx, id, p, &resolution); err != nil {
		return ManualExecutionContract{}, err
	}
	var resultRaw, resultHash, apiRaw, step, source string
	var opStatus, stepStatus, stepKind, stepExpiry, parameters, blockHash, capability string
	var chain, opPolicy, stepPolicy, blockNumber, stepIndex uint64
	var contractDeadline, sendAuthorized bool
	err = tx.QueryRowContext(ctx, `SELECT v.result_json,v.result_hash,m.api_policy_json,m.step_id,m.source,
 o.status,s.status,s.kind,s.expires_at,s.parameters_json,s.quote_block_hash,o.deadline_capability,
 o.chain_id,o.policy_version,s.policy_version,s.quote_block_number,s.step_index,m.contract_deadline,v.send_authorized
 FROM manual_http_order_sources m JOIN manual_trade_validations v ON v.operation_id=m.operation_id
 JOIN operations o ON o.id=m.operation_id AND o.wallet_id=m.wallet_id
 JOIN execution_steps s ON s.id=m.step_id AND s.operation_id=o.id AND s.wallet_id=m.wallet_id
 WHERE m.id=?`, requestID).Scan(&resultRaw, &resultHash, &apiRaw, &step, &source,
		&opStatus, &stepStatus, &stepKind, &stepExpiry, &parameters, &blockHash, &capability,
		&chain, &opPolicy, &stepPolicy, &blockNumber, &stepIndex, &contractDeadline, &sendAuthorized)
	if err != nil {
		return ManualExecutionContract{}, err
	}
	if source != "MANUAL" || chain != 4663 || step != "manual_step_"+requestID || stepIndex != 0 || stepKind != "manual_trade_validation" || opStatus != "manual_validation_succeeded" || stepStatus != "validation_succeeded" || opPolicy != p.Version || stepPolicy != p.Version || stepExpiry != r.ExpiresAt || capability != "APPLICATION_TTL_ONLY" || contractDeadline || sendAuthorized || blockNumber != resolution.Token.Block.Number || blockHash != resolution.Token.Block.Hash || parameters != resultRaw {
		return ManualExecutionContract{}, ErrConflict
	}
	var api Policy
	var out chainquery.ValidationOutput
	if json.Unmarshal([]byte(apiRaw), &api) != nil || json.Unmarshal([]byte(resultRaw), &out) != nil || encode(out) != resultRaw || hash([]byte(resultRaw)) != resultHash || out.Resolution != resolution {
		return ManualExecutionContract{}, ErrConflict
	}
	r.validationMaxFee = api.MaxFeeGwei
	intent, err := intentFromRecord(r, wallet)
	if err != nil {
		return ManualExecutionContract{}, err
	}
	if intent.SlippageBPS > p.MaxSlippageBPS || intent.SellBPS > p.MaxSellBPS {
		return ManualExecutionContract{}, ErrPolicy
	}
	if err = chainquery.CheckValidation(intent, resolution, limitsFromPolicy(p, r), out); err != nil {
		return ManualExecutionContract{}, err
	}
	// Fixed-amount SELL remains fixed-amount. Do not invent SellBPS or re-resolve
	// a percentage. Cross multiplication avoids rounding the risk limit upward.
	if resolution.Side == "sell" {
		amount, _ := new(big.Int).SetString(resolution.AmountIn, 10)
		balance, _ := new(big.Int).SetString(resolution.TokenBalance, 10)
		used := new(big.Int).Mul(amount, big.NewInt(10000))
		limit := new(big.Int).Mul(balance, new(big.Int).SetUint64(p.MaxSellBPS))
		if used.Cmp(limit) > 0 {
			return ManualExecutionContract{}, ErrPolicy
		}
	}
	c := manualContract(r, id, wallet, step, p, digest, resultHash, out)
	// Final TTL check does not turn a read snapshot into an economic gate.
	if err = ttlRecord(r, a.now()); err != nil {
		return ManualExecutionContract{}, err
	}
	if err = tx.Commit(); err != nil {
		return ManualExecutionContract{}, err
	}
	return c, nil
}

func manualContract(r Record, id Identity, wallet, step string, p validationPolicy, policyHash, resultHash string, out chainquery.ValidationOutput) ManualExecutionContract {
	res := out.Resolution
	inputAsset, inputDecimals := "native", uint8(18)
	outputAsset, outputDecimals := res.Token.Address, res.Token.Decimals
	if res.Side == "sell" {
		inputAsset, inputDecimals, outputAsset, outputDecimals = outputAsset, outputDecimals, inputAsset, inputDecimals
	}
	c := ManualExecutionContract{
		Version: 1, Status: "PARAMETER_CONTRACT_NOT_ADMITTED", Source: "MANUAL",
		RequestID: r.RequestID, PrincipalID: id.PrincipalID, OperationID: *r.OperationID, StepID: step,
		WalletID: id.WalletID, WalletAddress: wallet, ChainID: 4663,
		RequestFingerprint: hash([]byte(encode(r.Request))), Request: r.Request,
		APIPolicyVersion: r.APIPolicyVersion, APIPolicyHash: r.APIPolicyHash,
		PolicyVersion: p.Version, PolicyHash: policyHash,
		ResolutionHash: hash([]byte(encode(res))), ResultHash: resultHash,
		CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, DeadlineCapability: "APPLICATION_TTL_ONLY", Output: out,
		Input:         AssetQuantity{4663, inputAsset, inputDecimals, res.AmountIn},
		QuotedOutput:  AssetQuantity{4663, outputAsset, outputDecimals, out.QuotedOut},
		MinimumOutput: AssetQuantity{4663, outputAsset, outputDecimals, out.MinimumOut},
		GasBudget:     AssetQuantity{4663, "native", 18, out.GasBudget},
		NativeValue:   AssetQuantity{4663, "native", 18, out.Value},
	}
	// Deterministic checksum of the complete contract with binding_hash empty.
	// It is neither an HMAC, a signature nor an execution permission.
	c.BindingHash = hash([]byte(encode(c)))
	return c
}

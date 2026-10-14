// Package manualartifact validates exact MANUAL transaction candidates and
// encrypted local artifact witnesses. It has no DB, signer, RPC, clock, gate,
// authorization, allocation or submission capability. Facts are not permission.
package manualartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
)

const (
	Version      = 1
	MaxDataBytes = 65536
	MaxRawBytes  = 131072
)

var (
	ErrIdentity  = errors.New("invalid MANUAL artifact identity")
	ErrIntegrity = errors.New("MANUAL artifact integrity failure")
	ErrCipher    = errors.New("MANUAL artifact encryption failure")
)

// All fields are supplied by a future verified DB projection, never a request.
// Keeping them in the authenticated envelope prevents same-transaction bytes
// from being relabeled with another policy, expiry or deployment identity.
type Facts struct {
	Version              uint64 `json:"version"`
	OperationID          string `json:"operation_id"`
	RequestID            string `json:"request_id"`
	ExecutionStepID      string `json:"execution_step_id"`
	AttemptID            string `json:"attempt_id"`
	WalletID             string `json:"wallet_id"`
	ReservationID        string `json:"reservation_id"`
	ContractHash         string `json:"contract_hash"`
	ChainID              uint64 `json:"chain_id"`
	From                 string `json:"from"`
	To                   string `json:"to"`
	Value                string `json:"value"`
	Data                 string `json:"data"`
	Nonce                string `json:"nonce"`
	GasLimit             uint64 `json:"gas_limit"`
	GasFeeCap            string `json:"gas_fee_cap"`
	GasTipCap            string `json:"gas_tip_cap"`
	PolicyVersion        uint64 `json:"policy_version"`
	PolicyHash           string `json:"policy_hash"`
	QuoteBlockNumber     uint64 `json:"quote_block_number"`
	QuoteBlockHash       string `json:"quote_block_hash"`
	RuntimeCodeHash      string `json:"runtime_code_hash"`
	ExpiresAt            string `json:"expires_at"`
	ClaimedAt            string `json:"claimed_at"`
	ClaimObservationHash string `json:"claim_observation_hash"`
	DeadlineCapability   string `json:"deadline_capability"`
	ContractDeadline     bool   `json:"contract_deadline"`
	AuthorizationID      string `json:"authorization_id"`
	AuthorizationEpoch   uint64 `json:"authorization_epoch"`
	ManualPolicyID       string `json:"manual_policy_id"`
	ManualPolicyHash     string `json:"manual_policy_hash"`
	GateEvidenceSetHash  string `json:"gate_evidence_set_hash"`
	Environment          string `json:"environment"`
	BuildSHA             string `json:"build_sha"`
	ReleaseID            string `json:"release_id"`
	DeploymentID         string `json:"deployment_id"`
}

func validID(v string) bool {
	if len(v) == 0 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:-", c)) {
			return false
		}
	}
	return true
}

func fixedHex(v string, size int, prefix, nonzero bool) bool {
	if prefix {
		if !strings.HasPrefix(v, "0x") {
			return false
		}
		v = v[2:]
	}
	if len(v) != size || strings.ToLower(v) != v {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil && (!nonzero || strings.Trim(v, "0") != "")
}

func uint256(v string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(v, 10)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 || n.String() != v {
		return nil, ErrIdentity
	}
	return n, nil
}

func address(v string) bool {
	return len(v) == 42 && strings.HasPrefix(v, "0x") && common.IsHexAddress(v) && common.HexToAddress(v) != (common.Address{})
}

func (f Facts) Validate() error {
	for _, v := range []string{f.OperationID, f.RequestID, f.ExecutionStepID, f.AttemptID, f.WalletID, f.ReservationID, f.AuthorizationID, f.ManualPolicyID, f.Environment, f.ReleaseID, f.DeploymentID} {
		if !validID(v) {
			return ErrIdentity
		}
	}
	if f.Version != Version || f.ChainID != 4663 || f.ExecutionStepID != "manual-exec:"+f.OperationID || f.AttemptID != "manual-nonce:"+f.OperationID || f.ReservationID != "manual-risk:"+f.OperationID ||
		!address(f.From) || !address(f.To) || f.GasLimit == 0 || f.GasLimit > math.MaxInt64 || f.PolicyVersion == 0 || f.PolicyVersion > math.MaxInt64 ||
		f.QuoteBlockNumber == 0 || f.QuoteBlockNumber > math.MaxInt64 || f.AuthorizationEpoch == 0 || f.AuthorizationEpoch > math.MaxInt64 ||
		!fixedHex(f.ContractHash, 64, false, true) || !fixedHex(f.PolicyHash, 64, false, true) || !fixedHex(f.ClaimObservationHash, 64, false, true) || !fixedHex(f.ManualPolicyHash, 64, false, true) || !fixedHex(f.GateEvidenceSetHash, 64, false, true) ||
		!fixedHex(f.QuoteBlockHash, 64, true, true) || !fixedHex(f.RuntimeCodeHash, 64, true, true) || !(fixedHex(f.BuildSHA, 40, false, true) || fixedHex(f.BuildSHA, 64, false, true)) {
		return ErrIdentity
	}
	if _, err := manualnonce.Nonce(f.Nonce); err != nil {
		return ErrIdentity
	}
	expires, ee := manualnonce.CanonicalTime(f.ExpiresAt)
	claimed, ce := manualnonce.CanonicalTime(f.ClaimedAt)
	if ee != nil || ce != nil || !claimed.Before(expires) || f.DeadlineCapability != "APPLICATION_TTL_ONLY" || f.ContractDeadline {
		return ErrIdentity
	}
	if !strings.HasPrefix(f.Data, "0x") || len(f.Data) <= 2 || len(f.Data) > 2+2*MaxDataBytes || strings.ToLower(f.Data) != f.Data {
		return ErrIdentity
	}
	if _, err := hex.DecodeString(f.Data[2:]); err != nil {
		return ErrIdentity
	}
	value, ve := uint256(f.Value)
	fee, fe := uint256(f.GasFeeCap)
	tip, te := uint256(f.GasTipCap)
	if ve != nil || fe != nil || te != nil || value == nil || fee.Sign() <= 0 || tip.Cmp(fee) > 0 {
		return ErrIdentity
	}
	gasBudget := new(big.Int).Mul(fee, new(big.Int).SetUint64(f.GasLimit))
	if gasBudget.BitLen() > 256 || new(big.Int).Add(gasBudget, value).BitLen() > 256 {
		return ErrIdentity
	}
	return nil
}

// Canonical validates the whole bounded object. Digest is not authorization.
func (f Facts) Canonical() (string, string, error) {
	if err := f.Validate(); err != nil {
		return "", "", err
	}
	b, err := json.Marshal(f)
	if err != nil {
		return "", "", ErrIdentity
	}
	h := sha256.Sum256(b)
	return string(b), hex.EncodeToString(h[:]), nil
}

func Decode(raw string) (Facts, error) {
	var f Facts
	if len(raw) > 2*MaxDataBytes+8192 || json.Unmarshal([]byte(raw), &f) != nil {
		return Facts{}, ErrIdentity
	}
	canonical, _, err := f.Canonical()
	if err != nil || canonical != raw {
		return Facts{}, ErrIdentity
	}
	return f, nil
}

func (f Facts) Transaction() (*types.Transaction, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	n, _ := manualnonce.Nonce(f.Nonce)
	value, _ := uint256(f.Value)
	fee, _ := uint256(f.GasFeeCap)
	tip, _ := uint256(f.GasTipCap)
	data, _ := hex.DecodeString(f.Data[2:])
	to := common.HexToAddress(f.To)
	return types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(4663), Nonce: n, To: &to, Value: value, Data: data, Gas: f.GasLimit, GasFeeCap: fee, GasTipCap: tip}), nil
}

// AAD v1 is explicit and distinct from legacy artifact AAD. Never fall back.
func (f Facts) AAD() ([]byte, error) {
	_, digest, err := f.Canonical()
	if err != nil {
		return nil, err
	}
	return []byte("rbh:4663:" + f.OperationID + ":" + f.ExecutionStepID + ":" + f.AttemptID + ":manual:v1:" + digest), nil
}

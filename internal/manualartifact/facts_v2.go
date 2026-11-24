package manualartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const (
	DeadlineCapabilityApplicationTTLOnly   = "APPLICATION_TTL_ONLY"
	DeadlineCapabilityContractDeadlineUnix = "CONTRACT_DEADLINE_UNIX"
)

// FactsV2 is the multi-chain artifact identity format (Step 1.5-A/B).
// Existing RBH v1 facts and AAD ("rbh:4663:...:manual:v1:") remain completely untouched.
type FactsV2 struct {
	Version              int    `json:"version"`
	ChainID              uint64 `json:"chain_id"`
	ProtocolID           string `json:"protocol_id"`
	AdapterRevision      string `json:"adapter_revision"`
	OperationID          string `json:"operation_id"`
	ExecutionStepID      string `json:"execution_step_id"`
	AttemptID            string `json:"attempt_id"`
	WalletAddress        string `json:"wallet_address"`
	Nonce                uint64 `json:"nonce"`
	DeadlineCapability   string `json:"deadline_capability"`
	ExpiresAt            string `json:"expires_at"`
	ContractDeadlineUnix int64  `json:"contract_deadline_unix"`
}

// DeriveContractDeadline computes the deterministic contract_deadline_unix from immutable expiresAt
// when capability is CONTRACT_DEADLINE_UNIX; returns 0 for APPLICATION_TTL_ONLY.
func DeriveContractDeadline(capability, expiresAt string) (int64, error) {
	ts, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return 0, fmt.Errorf("invalid expires_at timestamp: %w", err)
	}
	switch capability {
	case DeadlineCapabilityApplicationTTLOnly:
		return 0, nil
	case DeadlineCapabilityContractDeadlineUnix:
		u := ts.UTC().Unix()
		if u <= 0 {
			return 0, errors.New("derived contract deadline must be positive")
		}
		return u, nil
	default:
		return 0, fmt.Errorf("unknown deadline capability %q", capability)
	}
}

func (f FactsV2) Validate() error {
	if f.Version != 2 || f.ChainID == 0 || f.ProtocolID == "" || f.AdapterRevision == "" ||
		f.OperationID == "" || f.ExecutionStepID == "" || f.AttemptID == "" || f.WalletAddress == "" {
		return errors.New("incomplete FactsV2 identity")
	}
	expectedDeadline, err := DeriveContractDeadline(f.DeadlineCapability, f.ExpiresAt)
	if err != nil {
		return err
	}
	if f.ContractDeadlineUnix != expectedDeadline {
		return fmt.Errorf("contract_deadline_unix=%d does not match derived %d", f.ContractDeadlineUnix, expectedDeadline)
	}
	return nil
}

// CanonicalJSONAndHash returns the canonical JSON bytes and lowercase SHA-256 hex digest.
func (f FactsV2) CanonicalJSONAndHash() ([]byte, string, error) {
	if err := f.Validate(); err != nil {
		return nil, "", err
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

// AADv2 returns the version-2 authenticated associated data bound to numeric chain_id:
// "evm:<chain_id>:<operation_id>:<execution_step_id>:<attempt_id>:manual:v2:<facts_hash>".
func (f FactsV2) AADv2() ([]byte, error) {
	_, digest, err := f.CanonicalJSONAndHash()
	if err != nil {
		return nil, err
	}
	return []byte("evm:" + strconv.FormatUint(f.ChainID, 10) + ":" + f.OperationID + ":" + f.ExecutionStepID + ":" + f.AttemptID + ":manual:v2:" + digest), nil
}

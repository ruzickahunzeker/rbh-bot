package chainquery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// Immutable operator-owned read profile. NetworkCheckpoint distinguishes
// configured environments sharing a chain ID. No HTTP caller controls it.
type SubmissionRuntimeProfile struct {
	ID, Environment   string
	NetworkCheckpoint Block
}

type SubmissionRuntimeObservation struct {
	ChainID                                        uint64
	ProfileID, Environment, Curve, RuntimeCodeHash string
	NetworkCheckpoint, Quote, Anchor               Block
	EvidenceHash                                   string
}

func (o SubmissionRuntimeObservation) Digest() string {
	o.EvidenceHash = ""
	raw, _ := json.Marshal(o)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// Separate read-only observer; no generic RPC, validator, quote, simulation,
// signer, nonce allocator or send dependency. New performs no I/O.
type SubmissionRuntimeObserver struct {
	client  *Client
	profile SubmissionRuntimeProfile
}

func NewSubmissionRuntimeObserver(endpoint string, profile SubmissionRuntimeProfile) (*SubmissionRuntimeObserver, error) {
	if !runtimeProfileID(profile.ID) || !runtimeProfileID(profile.Environment) || profile.NetworkCheckpoint.Number == 0 || !runtimeHash(profile.NetworkCheckpoint.Hash) {
		return nil, ErrObservation
	}
	c, err := New(endpoint)
	if err != nil {
		return nil, err
	}
	return &SubmissionRuntimeObserver{c, profile}, nil
}

func runtimeProfileID(raw string) bool {
	if len(raw) == 0 || len(raw) > 128 {
		return false
	}
	for _, c := range raw {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:-", c)) {
			return false
		}
	}
	return true
}

func runtimeHash(raw string) bool {
	if len(raw) != 66 || !strings.HasPrefix(raw, "0x") || raw != strings.ToLower(raw) {
		return false
	}
	bytes, err := hexutil.Decode(raw)
	return err == nil && len(bytes) == 32 && common.BytesToHash(bytes) != (common.Hash{})
}

// Inputs must be the original durable contract/allowlist projection, not HTTP.
// Block-hash pinned code + re-read numbered headers; no mutable latest-code mix.
func (r *SubmissionRuntimeObserver) Observe(ctx context.Context, curve, baseline string, quote Block) (SubmissionRuntimeObservation, error) {
	var zero SubmissionRuntimeObservation
	address, err := Address(curve)
	if r == nil || r.client == nil || ctx == nil || err != nil || curve != strings.ToLower(curve) || !runtimeHash(baseline) || !runtimeHash(quote.Hash) || quote.Number == 0 {
		return zero, ErrObservation
	}
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	anchor, err := r.client.begin(ctx)
	if err != nil {
		return zero, err
	}
	if anchor.block.Number < quote.Number || anchor.block.Number < r.profile.NetworkCheckpoint.Number {
		return zero, ErrObservation
	}
	network, err := r.client.header(ctx, hexutil.EncodeUint64(r.profile.NetworkCheckpoint.Number))
	if err != nil {
		return zero, err
	}
	if network.block != r.profile.NetworkCheckpoint {
		return zero, ErrObservation
	}
	quoted, err := r.client.header(ctx, hexutil.EncodeUint64(quote.Number))
	if err != nil {
		return zero, err
	}
	if quoted.block != quote {
		return zero, ErrObservation
	}
	code, err := r.client.code(ctx, address, anchor)
	if err != nil {
		return zero, err
	}
	if crypto.Keccak256Hash(code).Hex() != baseline {
		return zero, ErrObservation
	}
	for _, observed := range []snapshot{network, quoted, anchor} {
		if err = r.client.finish(ctx, observed); err != nil {
			return zero, err
		}
	}
	if err = ctx.Err(); err != nil {
		return zero, err
	}
	o := SubmissionRuntimeObservation{ChainID: ChainID, ProfileID: r.profile.ID, Environment: r.profile.Environment, Curve: curve, RuntimeCodeHash: baseline, NetworkCheckpoint: network.block, Quote: quoted.block, Anchor: anchor.block}
	o.EvidenceHash = o.Digest()
	return o, nil
}

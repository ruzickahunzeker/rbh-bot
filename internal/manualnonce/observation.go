// Package manualnonce contains pure, bounded local nonce-observation validation.
// It has no client, database, signer, clock or allocation capability.
package manualnonce

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

const MaxObservationAge = 2 * time.Second

var ErrEvidence = errors.New("invalid MANUAL nonce observation")

// Facts originate only from a verified durable command/binding, never HTTP.
type Facts struct {
	OperationID     string `json:"operation_id"`
	RequestID       string `json:"request_id"`
	BindingHash     string `json:"binding_hash"`
	ChainID         uint64 `json:"chain_id"`
	WalletID        string `json:"wallet_id"`
	WalletAddress   string `json:"wallet_address"`
	ContractAddress string `json:"contract_address"`
	RuntimeCodeHash string `json:"runtime_code_hash"`
	QuoteBlock      uint64 `json:"quote_block"`
	QuoteHash       string `json:"quote_hash"`
	BoundAt         string `json:"bound_at"`
	ExpiresAt       string `json:"expires_at"`
}

// Observation is evidence from a controlled local, query-only backend. It is
// not permission or a production RPC adapter. Both nonce values must agree;
// an existing pending transaction is deliberately outside this first claim.
type Observation struct {
	OperationID     string `json:"operation_id"`
	RequestID       string `json:"request_id"`
	BindingHash     string `json:"binding_hash"`
	ChainID         uint64 `json:"chain_id"`
	WalletID        string `json:"wallet_id"`
	WalletAddress   string `json:"wallet_address"`
	ContractAddress string `json:"contract_address"`
	RuntimeCodeHash string `json:"runtime_code_hash"`
	BlockNumber     uint64 `json:"block_number"`
	BlockHash       string `json:"block_hash"`
	BlockCanonical  bool   `json:"block_canonical"`
	ConfirmedNonce  string `json:"confirmed_nonce"`
	PendingValue    string `json:"pending_nonce"`
	ObservedAt      string `json:"observed_at"`
}

func Encode(v Observation) string { b, _ := json.Marshal(v); return string(b) }
func Digest(raw string) string    { d := sha256.Sum256([]byte(raw)); return hex.EncodeToString(d[:]) }

func CanonicalTime(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || t.IsZero() || t.Year() < 1970 || t.Year() > 9998 || t.UTC().Format(time.RFC3339Nano) != raw {
		return time.Time{}, ErrEvidence
	}
	return t, nil
}

// Nonce zero is valid; reject noncanonical uint64 and the value whose successor
// overflows. This function parses only; it never increments or allocates.
func Nonce(raw string) (uint64, error) {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == math.MaxUint64 || strconv.FormatUint(n, 10) != raw {
		return 0, ErrEvidence
	}
	return n, nil
}

func textHex(raw string, size int, prefix bool) bool {
	if prefix {
		if !strings.HasPrefix(raw, "0x") {
			return false
		}
		raw = raw[2:]
	}
	if len(raw) != size || raw != strings.ToLower(raw) {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil && (!prefix || strings.Trim(raw, "0") != "")
}

func decode(raw string, dst any) error {
	if len(raw) == 0 || len(raw) > 4096 {
		return ErrEvidence
	}
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil {
		return ErrEvidence
	}
	return nil
}

// DecodeFacts is shared by the SQLite scalar; source identity is supplied by
// its durable joins, not the observation. No cast of TEXT nonce to SQL INTEGER.
func DecodeFacts(raw string) (Facts, error) {
	var f Facts
	if err := decode(raw, &f); err != nil {
		return Facts{}, err
	}
	b, _ := json.Marshal(f)
	if string(b) != raw {
		return Facts{}, ErrEvidence
	}
	return f, nil
}

func Verify(raw, digest, nonce string, f Facts, claimedAt string) (Observation, error) {
	var o Observation
	if decode(raw, &o) != nil || Encode(o) != raw || Digest(raw) != digest {
		return Observation{}, ErrEvidence
	}
	if f.OperationID == "" || f.RequestID == "" || f.WalletID == "" || f.ChainID != 4663 ||
		!textHex(f.BindingHash, 64, false) || !textHex(f.WalletAddress, 40, true) ||
		!textHex(f.ContractAddress, 40, true) || !textHex(f.RuntimeCodeHash, 64, true) ||
		!textHex(f.QuoteHash, 64, true) || f.QuoteBlock == 0 || f.QuoteBlock > math.MaxInt64 ||
		o.OperationID != f.OperationID || o.RequestID != f.RequestID || o.BindingHash != f.BindingHash ||
		o.ChainID != f.ChainID || o.WalletID != f.WalletID || o.WalletAddress != f.WalletAddress ||
		o.ContractAddress != f.ContractAddress || o.RuntimeCodeHash != f.RuntimeCodeHash ||
		!o.BlockCanonical || o.BlockNumber < f.QuoteBlock || o.BlockNumber > math.MaxInt64 ||
		!textHex(o.BlockHash, 64, true) || (o.BlockNumber == f.QuoteBlock && o.BlockHash != f.QuoteHash) ||
		o.ConfirmedNonce != nonce || o.PendingValue != nonce {
		return Observation{}, ErrEvidence
	}
	if _, err := Nonce(nonce); err != nil {
		return Observation{}, err
	}
	bound, be := CanonicalTime(f.BoundAt)
	expiry, ee := CanonicalTime(f.ExpiresAt)
	observed, oe := CanonicalTime(o.ObservedAt)
	claimed, ce := CanonicalTime(claimedAt)
	if be != nil || ee != nil || oe != nil || ce != nil || observed.Before(bound) ||
		claimed.Before(observed) || !claimed.Before(expiry) || claimed.Sub(observed) > MaxObservationAge {
		return Observation{}, ErrEvidence
	}
	return o, nil
}

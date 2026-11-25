package tradeapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// AuthorizedWallet describes one wallet identity authorized for a caller on a trade-service instance.
type AuthorizedWallet struct {
	WalletID   string
	Address    common.Address
	WalletType string // "keystore" | "readonly"
	CanSign    bool
}

// WalletResolver resolves an authorized wallet_id into its EVM address and signing capability.
type WalletResolver interface {
	ResolveWallet(ctx context.Context, walletID string) (AuthorizedWallet, bool)
}

// WalletPreflightProvider is an optional extension of LocalV2Provider that accepts an explicit
// resolved wallet address per request instead of using a single static wallet address.
type WalletPreflightProvider interface {
	QuotePreflightForWallet(ctx context.Context, req QuotePreflightRequest, walletAddr common.Address, spender common.Address) (QuotePreflightResponse, error)
}

type keystoreFileHeader struct {
	Version    int    `json:"version"`
	WalletID   string `json:"wallet_id"`
	Address    string `json:"address"`
	WalletType string `json:"wallet_type"`
}

// KeystoreDirWalletResolver resolves chain-agnostic v2 and legacy v1 encrypted keystores from dir.
// It only reads public non-sensitive JSON metadata (wallet_id, address); private keys remain encrypted.
type KeystoreDirWalletResolver struct {
	dir string
}

func NewKeystoreDirWalletResolver(dir string) *KeystoreDirWalletResolver {
	return &KeystoreDirWalletResolver{dir: strings.TrimSpace(dir)}
}

func (r *KeystoreDirWalletResolver) ResolveWallet(_ context.Context, walletID string) (AuthorizedWallet, bool) {
	if r == nil || r.dir == "" || walletID == "" || strings.ContainsAny(walletID, `/\.. `) {
		return AuthorizedWallet{}, false
	}
	p := filepath.Join(r.dir, walletID+".json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return AuthorizedWallet{}, false
	}
	var hdr keystoreFileHeader
	if json.Unmarshal(raw, &hdr) != nil || hdr.WalletID != walletID || !common.IsHexAddress(hdr.Address) {
		return AuthorizedWallet{}, false
	}
	if hdr.Version != 1 && hdr.Version != 2 {
		return AuthorizedWallet{}, false
	}
	wType := hdr.WalletType
	if wType == "" {
		wType = "keystore"
	}
	return AuthorizedWallet{
		WalletID:   hdr.WalletID,
		Address:    common.HexToAddress(hdr.Address),
		WalletType: wType,
		CanSign:    wType == "keystore",
	}, true
}

// SetWalletResolver configures dynamic per-wallet resolution on LocalV2Extension.
func (e *LocalV2Extension) SetWalletResolver(resolver WalletResolver) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.resolver = resolver
	e.mu.Unlock()
}

func (e *LocalV2Extension) resolveWallet(ctx context.Context, primary Identity, walletID string) (AuthorizedWallet, bool) {
	if walletID == "" {
		return AuthorizedWallet{}, false
	}
	if walletID == primary.WalletID {
		return AuthorizedWallet{
			WalletID:   primary.WalletID,
			Address:    common.HexToAddress(primary.WalletAddress),
			WalletType: "readonly",
			CanSign:    false,
		}, true
	}
	e.mu.Lock()
	res := e.resolver
	e.mu.Unlock()
	if res == nil {
		return AuthorizedWallet{}, false
	}
	return res.ResolveWallet(ctx, walletID)
}

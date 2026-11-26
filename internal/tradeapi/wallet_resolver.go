package tradeapi

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/walletmeta"
)

type AuthorizedWallet struct {
	WalletID   string
	Address    common.Address
	WalletType string
	CanSign    bool
}

// WalletAccessPolicy is a deployment-owned grant for ONE authenticated API principal.
// A shared bot credential grants service-level access to the listed owners, not per-chat HTTP isolation.
type WalletAccessPolicy struct {
	AllowedOwnerChatIDs  []int64               `json:"allowed_owner_chat_ids,omitempty"`
	AllowSharedKeystores bool                  `json:"allow_shared_keystores,omitempty"`
	PrimaryCanSign       bool                  `json:"primary_can_sign,omitempty"`
	ReadonlyWallets      []ReadonlyWalletGrant `json:"readonly_wallets,omitempty"`
}
type ReadonlyWalletGrant struct {
	WalletID string `json:"wallet_id"`
	Address  string `json:"address"`
}

type WalletAccessGrant struct {
	PrincipalID string
	Primary     AuthorizedWallet
	Policy      WalletAccessPolicy
}

func (p WalletAccessPolicy) clone() WalletAccessPolicy {
	p.AllowedOwnerChatIDs = append([]int64(nil), p.AllowedOwnerChatIDs...)
	p.ReadonlyWallets = append([]ReadonlyWalletGrant(nil), p.ReadonlyWallets...)
	return p
}

// WalletResolver receives only the server-authenticated principal, never a request-supplied owner ID.
type WalletResolver interface {
	ResolveWallet(context.Context, string, string) (AuthorizedWallet, bool)
}
type WalletPreflightProvider interface {
	QuotePreflightForWallet(context.Context, QuotePreflightRequest, common.Address, common.Address) (QuotePreflightResponse, error)
}

type KeystoreDirWalletResolver struct {
	dir    string
	grants map[string]WalletAccessGrant
	err    error
}

// No grant means no access, including owner_chat_id=0 keystores.
func NewKeystoreDirWalletResolver(dir string, grants ...WalletAccessGrant) *KeystoreDirWalletResolver {
	r := &KeystoreDirWalletResolver{dir: strings.TrimSpace(dir), grants: make(map[string]WalletAccessGrant)}
	for _, g := range grants {
		g.Policy = g.Policy.clone()
		if g.PrincipalID == "" {
			r.err = ErrInvalid
			continue
		}
		if _, dup := r.grants[g.PrincipalID]; dup {
			r.err = ErrInvalid
		}
		seen := map[string]common.Address{}
		if g.Primary.WalletID != "" {
			if !walletmeta.ValidWalletID(g.Primary.WalletID) || g.Primary.Address == (common.Address{}) {
				r.err = ErrInvalid
			}
			seen[g.Primary.WalletID] = g.Primary.Address
		}
		for _, owner := range g.Policy.AllowedOwnerChatIDs {
			if owner == 0 {
				r.err = ErrInvalid
			}
		}
		for _, w := range g.Policy.ReadonlyWallets {
			if w.WalletID == g.Primary.WalletID && g.Primary.CanSign {
				r.err = errors.New("primary signing grant conflicts with readonly mapping")
			}
			addr := common.HexToAddress(w.Address)
			if !walletmeta.ValidWalletID(w.WalletID) || !common.IsHexAddress(w.Address) || addr == (common.Address{}) {
				r.err = ErrInvalid
			}
			if prev, dup := seen[w.WalletID]; dup && prev != addr {
				r.err = errors.New("conflicting wallet addresses in principal grant")
			}
			seen[w.WalletID] = addr
		}
		r.grants[g.PrincipalID] = g
	}
	// Reject configured ID/address conflicts with on-disk metadata before mounting the API.
	if r.err == nil {
		for _, g := range r.grants {
			static := append([]ReadonlyWalletGrant(nil), g.Policy.ReadonlyWallets...)
			if g.Primary.WalletID != "" {
				static = append(static, ReadonlyWalletGrant{g.Primary.WalletID, g.Primary.Address.Hex()})
			}
			for _, w := range static {
				summary, err := walletmeta.ReadWalletSummary(r.dir, w.WalletID)
				if err != nil && !os.IsNotExist(err) && !errors.Is(err, walletmeta.ErrWalletDeleting) {
					r.err = errors.New("cannot validate configured wallet metadata")
				}
				if err == nil && summary.Address != common.HexToAddress(w.Address) {
					r.err = errors.New("configured wallet address conflicts with keystore")
				}
			}
		}
	}
	return r
}
func (r *KeystoreDirWalletResolver) Validate() error {
	if r == nil {
		return ErrInvalid
	}
	return r.err
}

func (r *KeystoreDirWalletResolver) ResolveWallet(_ context.Context, principalID, walletID string) (AuthorizedWallet, bool) {
	if r == nil || r.err != nil || !walletmeta.ValidWalletID(walletID) {
		return AuthorizedWallet{}, false
	}
	g, ok := r.grants[principalID]
	if !ok {
		return AuthorizedWallet{}, false
	}
	// Tombstones also cover readonly mappings and a configured primary identity.
	if err := walletmeta.CheckWalletAvailable(r.dir, walletID); err != nil {
		return AuthorizedWallet{}, false
	}
	var configured *AuthorizedWallet
	if walletID == g.Primary.WalletID {
		w := g.Primary
		configured = &w
	}
	for _, w := range g.Policy.ReadonlyWallets {
		if w.WalletID == walletID {
			if configured == nil {
				configured = &AuthorizedWallet{WalletID: walletID, Address: common.HexToAddress(w.Address), WalletType: "readonly", CanSign: false}
			}
			break
		}
	}
	summary, err := walletmeta.ReadWalletSummary(r.dir, walletID)
	if configured != nil {
		if err != nil && !os.IsNotExist(err) {
			return AuthorizedWallet{}, false
		}
		if err == nil && summary.Address != configured.Address {
			return AuthorizedWallet{}, false
		}
		return *configured, true
	}
	if err != nil {
		return AuthorizedWallet{}, false
	}
	allowed := summary.OwnerChatID == 0 && g.Policy.AllowSharedKeystores
	for _, owner := range g.Policy.AllowedOwnerChatIDs {
		if owner == summary.OwnerChatID {
			allowed = true
		}
	}
	if !allowed {
		return AuthorizedWallet{}, false
	}
	return AuthorizedWallet{WalletID: walletID, Address: summary.Address, WalletType: summary.WalletType, CanSign: summary.WalletType == "keystore"}, true
}

func (h *API) ConfigureWalletResolver(ext *LocalV2Extension, dir string) error {
	primary := AuthorizedWallet{WalletID: h.identity.WalletID, Address: common.HexToAddress(h.identity.WalletAddress), WalletType: "readonly", CanSign: h.walletAccess.PrimaryCanSign}
	if primary.CanSign {
		primary.WalletType = "external-signer"
	}
	resolver := NewKeystoreDirWalletResolver(dir, WalletAccessGrant{PrincipalID: h.identity.PrincipalID, Primary: primary, Policy: h.walletAccess})
	if err := resolver.Validate(); err != nil {
		return err
	}
	ext.SetWalletResolver(resolver)
	return nil
}
func (e *LocalV2Extension) SetWalletResolver(resolver WalletResolver) {
	if e != nil {
		e.mu.Lock()
		e.resolver = resolver
		e.mu.Unlock()
	}
}
func (e *LocalV2Extension) resolveWallet(ctx context.Context, primary Identity, walletID string) (AuthorizedWallet, bool) {
	if !walletmeta.ValidWalletID(walletID) {
		return AuthorizedWallet{}, false
	}
	e.mu.Lock()
	res := e.resolver
	e.mu.Unlock()
	if res != nil {
		return res.ResolveWallet(ctx, primary.PrincipalID, walletID)
	}
	if walletID == primary.WalletID {
		return AuthorizedWallet{WalletID: primary.WalletID, Address: common.HexToAddress(primary.WalletAddress), WalletType: "readonly", CanSign: false}, true
	}
	return AuthorizedWallet{}, false
}

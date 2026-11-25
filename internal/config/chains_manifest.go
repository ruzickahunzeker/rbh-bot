package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ChainManifestEntry declaratively defines one EVM chain instance attached to bot-service,
// eliminating per-chain hardcoded environment variables when scaling beyond BSC + RBH.
type ChainManifestEntry struct {
	ChainID           uint64   `json:"chain_id"`
	Slug              string   `json:"slug"`
	Name              string   `json:"name"`
	NativeSymbol      string   `json:"native_symbol"`
	TradeSocket       string   `json:"trade_socket"`
	TradeToken        string   `json:"trade_token"`
	FeedSocket        string   `json:"feed_socket"`
	RPCURL            string   `json:"rpc_url"`
	DefaultWallet     string   `json:"default_wallet"`
	Protocols         []string `json:"protocols"`
	DefaultBuyPresets string   `json:"default_buy_presets"`
}

// LoadChainsManifest parses and validates a JSON array of ChainManifestEntry records.
func LoadChainsManifest(path string) ([]ChainManifestEntry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read chains manifest %s: %w", path, err)
	}
	var entries []ChainManifestEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse chains manifest %s: %w", path, err)
	}
	seen := make(map[uint64]bool, len(entries))
	for i := range entries {
		e := &entries[i]
		if e.ChainID == 0 {
			return nil, fmt.Errorf("manifest entry %d missing chain_id", i)
		}
		if seen[e.ChainID] {
			return nil, fmt.Errorf("duplicate chain_id %d in manifest", e.ChainID)
		}
		seen[e.ChainID] = true
		if e.Slug == "" {
			e.Slug = DefaultChainSlug(e.ChainID)
		}
		if e.Name == "" {
			e.Name = strings.ToUpper(e.Slug)
		}
		if e.NativeSymbol == "" {
			e.NativeSymbol = DefaultNativeSymbol(e.ChainID)
		}
		if e.DefaultWallet == "" {
			e.DefaultWallet = "wallet-" + e.Slug + "-readonly"
		}
		if e.DefaultBuyPresets == "" {
			e.DefaultBuyPresets = DefaultBuyPresetsForChain(e.ChainID)
		}
	}
	if len(entries) == 0 {
		return nil, errors.New("chains manifest is empty")
	}
	return entries, nil
}

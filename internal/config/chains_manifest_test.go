package config

import (
	"path/filepath"
	"testing"
)

func TestLoadChainsManifestExample(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "chains", "manifest.example.json")
	entries, err := LoadChainsManifest(path)
	if err != nil {
		t.Fatalf("LoadChainsManifest: %v", err)
	}
	if len(entries) != 7 {
		t.Fatalf("expected 7 chains in manifest.example.json, got %d", len(entries))
	}
	for _, e := range entries {
		if !IsSupportedChainID(e.ChainID) {
			t.Fatalf("manifest chain_id %d not marked supported", e.ChainID)
		}
	}
}

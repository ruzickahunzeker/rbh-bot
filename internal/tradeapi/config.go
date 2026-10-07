package tradeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

// LoadFile uses a dedicated API credential, never the internal IPC credential.
// The single caller/wallet grant is deployment-owned and immutable for the
// process lifetime. Multiple callers/TCP/TLS require a separate reviewed slice.
func LoadFile(database *storage.Database, path string, internalCredential string) (*API, error) {
	configurationError := errors.New("invalid user HTTP API credential configuration")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, configurationError
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, configurationError
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Mode().Perm()&0o077 != 0 || !os.SameFile(info, opened) {
		return nil, configurationError
	}
	body, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(body) > 16384 {
		return nil, configurationError
	}
	if validateConfigObject(json.NewDecoder(bytes.NewReader(body)), false) != nil {
		return nil, configurationError
	}
	var cfg struct {
		PrincipalID   string  `json:"principal_id"`
		WalletID      string  `json:"wallet_id"`
		WalletAddress string  `json:"wallet_address"`
		Token         string  `json:"token"`
		Policy        *Policy `json:"api_policy,omitempty"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&cfg) != nil {
		return nil, configurationError
	}
	var extra any
	if err = d.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, configurationError
	}
	if cfg.Token == internalCredential {
		return nil, configurationError
	}
	p := DefaultPolicy()
	if cfg.Policy != nil {
		p = *cfg.Policy
	}
	s, err := NewStore(database)
	if err != nil {
		return nil, configurationError
	}
	h, err := New(s, Identity{PrincipalID: cfg.PrincipalID, WalletID: cfg.WalletID, WalletAddress: cfg.WalletAddress}, cfg.Token, p)
	if err != nil {
		return nil, configurationError
	}
	if _, err = s.Wallet(context.Background(), h.identity); err != nil {
		return nil, configurationError
	}
	return h, nil
}

// Deployment config also rejects duplicate/case-alias keys rather than taking
// the last credential or policy. Values/types are checked by the typed decoder.
func validateConfigObject(d *json.Decoder, policy bool) error {
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		t, err = d.Token()
		if err != nil {
			return ErrInvalid
		}
		key, ok := t.(string)
		if !ok || seen[key] {
			return ErrInvalid
		}
		seen[key] = true
		if !policy && key == "api_policy" {
			if err = validateConfigObject(d, true); err != nil {
				return err
			}
			continue
		}
		allowed := false
		if policy {
			switch key {
			case "version", "default_ttl_seconds", "max_ttl_seconds", "max_slippage_bps", "max_fee_gwei":
				allowed = true
			}
		} else {
			switch key {
			case "principal_id", "wallet_id", "wallet_address", "token":
				allowed = true
			}
		}
		if !allowed {
			return ErrInvalid
		}
		t, err = d.Token()
		if err != nil || t == nil {
			return ErrInvalid
		}
		if _, nested := t.(json.Delim); nested {
			return ErrInvalid
		}
	}
	t, err = d.Token()
	if err != nil || t != json.Delim('}') {
		return ErrInvalid
	}
	return nil
}

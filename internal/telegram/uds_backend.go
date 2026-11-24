package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// ChainUDSConfig describes one local trade-service Unix Domain Socket + Bearer credential.
type ChainUDSConfig struct {
	TradeSocket string
	BearerToken string
}

// UDSBackend implements Backend by talking strictly over local Unix Domain Sockets
// to each chain's trade-<chain>.sock (never exposing HTTP over TCP).
type UDSBackend struct {
	chains map[uint64]ChainUDSConfig
}

func NewUDSBackend(chains map[uint64]ChainUDSConfig) *UDSBackend {
	cp := make(map[uint64]ChainUDSConfig, len(chains))
	for k, v := range chains {
		if v.TradeSocket != "" {
			cp[k] = v
		}
	}
	return &UDSBackend{chains: cp}
}

func (u *UDSBackend) udsClient(socketPath string) *http.Client {
	return &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

func (u *UDSBackend) getJSON(ctx context.Context, chainID uint64, path string, out any) error {
	cfg, ok := u.chains[chainID]
	if !ok {
		return fmt.Errorf("chain %d trade socket not configured", chainID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix"+path, nil)
	if err != nil {
		return err
	}
	if cfg.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	}
	resp, err := u.udsClient(cfg.TradeSocket).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("trade UDS %s returned %d: %s", path, resp.StatusCode, string(raw))
	}
	return json.Unmarshal(raw, out)
}

func (u *UDSBackend) ListWallets(ctx context.Context, chainID uint64) ([]string, error) {
	var resp struct {
		Wallet struct {
			ID      string `json:"wallet_id"`
			Address string `json:"address"`
		} `json:"wallet"`
	}
	if err := u.getJSON(ctx, chainID, "/v1/wallets", &resp); err != nil {
		return nil, err
	}
	if resp.Wallet.ID == "" {
		return []string{}, nil
	}
	return []string{fmt.Sprintf("%s (%s)", resp.Wallet.ID, resp.Wallet.Address)}, nil
}

func (u *UDSBackend) ReadPosition(ctx context.Context, chainID uint64, walletID string, token common.Address) (*big.Int, *big.Int, error) {
	var snap struct {
		NativeBalanceRaw string `json:"native_balance_raw"`
		TokenBalanceRaw  string `json:"token_balance_raw"`
	}
	path := fmt.Sprintf("/v1/wallets/%s/positions?token=%s", walletID, token.Hex())
	if err := u.getJSON(ctx, chainID, path, &snap); err != nil {
		return nil, nil, err
	}
	tok, _ := new(big.Int).SetString(snap.TokenBalanceRaw, 10)
	nat, _ := new(big.Int).SetString(snap.NativeBalanceRaw, 10)
	if tok == nil {
		tok = big.NewInt(0)
	}
	if nat == nil {
		nat = big.NewInt(0)
	}
	return tok, nat, nil
}

func (u *UDSBackend) SubmitManualTrade(_ context.Context, _ uint64, _, _ string, _ common.Address, _ *big.Int) (string, error) {
	return "", errors.New("live trade submission is disabled in read-only mode (live_enabled=false)")
}

func (u *UDSBackend) AddWatchedWallet(_ context.Context, _ uint64, _ common.Address) error {
	return nil
}

func (u *UDSBackend) ConfigureCopyStrategy(_ context.Context, _ uint64, _ common.Address, _ *big.Int) error {
	return nil
}

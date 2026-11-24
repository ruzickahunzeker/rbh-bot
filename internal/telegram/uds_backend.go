package telegram

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	basechain "github.com/ruzickahunzeker/rbh-bot/internal/chains/base"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/honeypot"
	"github.com/ruzickahunzeker/rbh-bot/internal/ipc"
	pancakev2 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/pancakeswap/v2"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

// ChainUDSConfig describes one chain's local Unix Domain Sockets, Bearer credential, and read-only RPC.
type ChainUDSConfig struct {
	ChainSlug   string
	TradeSocket string
	BearerToken string
	FeedSocket  string
	RPCURL      string
}

// UDSBackend implements Backend, QuoteBackend, HoneypotBackend, and bot.ShadowQuoter
// by talking strictly over local Unix Domain Sockets (and read-only RPC for eth_simulateV1).
type UDSBackend struct {
	chains   map[uint64]ChainUDSConfig
	db       *sql.DB
	auth     *ipc.Authenticator
	watchMu  sync.Mutex
	watchVer int64
}

func NewUDSBackend(chains map[uint64]ChainUDSConfig) *UDSBackend {
	return NewUDSBackendWithWatchlist(chains, nil, nil)
}

func NewUDSBackendWithWatchlist(chains map[uint64]ChainUDSConfig, database *storage.Database, auth *ipc.Authenticator) *UDSBackend {
	cp := make(map[uint64]ChainUDSConfig, len(chains))
	for k, v := range chains {
		if v.TradeSocket != "" || v.FeedSocket != "" || v.RPCURL != "" {
			cp[k] = v
		}
	}
	var db *sql.DB
	if database != nil {
		db, _ = database.SQLDB(storage.BotOwner)
	}
	return &UDSBackend{
		chains:   cp,
		db:       db,
		auth:     auth,
		watchVer: time.Now().UTC().Unix(),
	}
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
	if !ok || cfg.TradeSocket == "" {
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

func (u *UDSBackend) postTradeJSON(ctx context.Context, chainID uint64, path string, bodyObj any, out any) error {
	cfg, ok := u.chains[chainID]
	if !ok || cfg.TradeSocket == "" {
		return fmt.Errorf("chain %d trade socket not configured", chainID)
	}
	body, err := json.Marshal(bodyObj)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
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
		Wallets []struct {
			ID      string `json:"wallet_id"`
			Chain   string `json:"chain"`
			Address string `json:"address"`
		} `json:"wallets"`
	}
	if err := u.getJSON(ctx, chainID, "/v1/wallets", &resp); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(resp.Wallets))
	for _, w := range resp.Wallets {
		out = append(out, fmt.Sprintf("%s (%s)", w.ID, w.Address))
	}
	return out, nil
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

type udsQuoteResponse struct {
	ChainID          uint64 `json:"chain_id"`
	Protocol         string `json:"protocol"`
	PoolPhase        string `json:"pool_phase"`
	ExpectedOutRaw   string `json:"expected_out_raw"`
	MinOutRaw        string `json:"min_out_raw"`
	EstimatedGas     uint64 `json:"estimated_gas"`
	CurrentAllowance string `json:"current_allowance_raw"`
	NeedsApprove     bool   `json:"needs_approve"`
	QuoteBlockNumber uint64 `json:"quote_block_number"`
}

func (u *UDSBackend) fetchQuote(ctx context.Context, chainID uint64, walletID, protocol string, token common.Address, side string, amountRaw *big.Int) (udsQuoteResponse, error) {
	cfg, ok := u.chains[chainID]
	if !ok {
		return udsQuoteResponse{}, fmt.Errorf("chain %d not configured", chainID)
	}
	slug := cfg.ChainSlug
	if slug == "" {
		switch chainID {
		case 56:
			slug = "bsc"
		case 8453:
			slug = "base"
		default:
			slug = "rbh"
		}
	}
	var qResp udsQuoteResponse
	err := u.postTradeJSON(ctx, chainID, "/v1/quotes", map[string]any{
		"chain":        slug,
		"wallet_id":    walletID,
		"protocol":     protocol,
		"token":        token.Hex(),
		"side":         side,
		"amount_raw":   amountRaw.String(),
		"slippage_bps": 100,
	}, &qResp)
	return qResp, err
}

func (u *UDSBackend) QuoteToken(ctx context.Context, chainID uint64, walletID, protocol string, token common.Address, side string, amountRaw *big.Int) (string, error) {
	q, err := u.fetchQuote(ctx, chainID, walletID, protocol, token, side, amountRaw)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"Quote chain=%d proto=%s (%s)\nblock=%d side=%s\nin=%s\nexpected_out=%s\nmin_out(1%%)=%s\nneeds_approve=%v",
		q.ChainID, q.Protocol, q.PoolPhase, q.QuoteBlockNumber, side, amountRaw.String(), q.ExpectedOutRaw, q.MinOutRaw, q.NeedsApprove,
	), nil
}

// QuoteShadowFill satisfies bot.ShadowQuoter using live read-only POST /v1/quotes over local UDS.
func (u *UDSBackend) QuoteShadowFill(ctx context.Context, chainID uint64, walletID string, token common.Address, side string, amountInRaw *big.Int) (*big.Int, *big.Int, error) {
	q, err := u.fetchQuote(ctx, chainID, walletID, defaultProtocolForChain(chainID), token, side, amountInRaw)
	if err != nil {
		return nil, nil, err
	}
	outRaw, ok := new(big.Int).SetString(q.ExpectedOutRaw, 10)
	if !ok || outRaw.Sign() <= 0 {
		return nil, nil, errors.New("invalid quote expected_out_raw")
	}
	spot := new(big.Int).Mul(amountInRaw, big.NewInt(1_000_000_000))
	spot.Div(spot, outRaw)
	if spot.Sign() <= 0 {
		spot = big.NewInt(1)
	}
	return outRaw, spot, nil
}

func (u *UDSBackend) CheckHoneypot(ctx context.Context, chainID uint64, token common.Address) (string, error) {
	cfg, ok := u.chains[chainID]
	if !ok || cfg.RPCURL == "" {
		return "RPC URL not configured for honeypot simulation on this chain.", nil
	}
	if chainID != 56 && chainID != 8453 {
		return "Honeypot eth_simulateV1 is currently configured for BSC (56) and Base (8453).", nil
	}
	probeWallet := common.HexToAddress("0x000000000000000000000000000000000000CAFE")
	probeWei := big.NewInt(1_000_000_000_000_000) // 0.001 BNB/ETH
	deadline := time.Now().UTC().Add(10 * time.Minute).Unix()

	var routerAddr, wrappedAddr common.Address
	var buyCalldata, approveCalldata []byte
	var buildSell func(*big.Int) ([]byte, error)
	var err error

	if chainID == 56 {
		adapter := pancakev2.NewAdapter()
		routerAddr = bsc.PancakeV2Router
		wrappedAddr = bsc.WBNBAddress
		route := ports.ResolvedRoute{ChainID: 56, Token: token, RouterOrSpender: routerAddr}
		buyTx, bErr := adapter.BuildUnsigned(ports.QuoteRequest{
			Route: route, Side: ports.SideBuy, AmountInRaw: probeWei, Recipient: probeWallet, ContractExpiry: deadline,
		}, ports.QuoteResult{MinOutRaw: big.NewInt(0)})
		if bErr != nil {
			return "", bErr
		}
		buyCalldata = buyTx.Calldata
		approveCalldata, err = pancakev2.BuildApproveCalldata(routerAddr, new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)))
		if err != nil {
			return "", err
		}
		buildSell = func(bought *big.Int) ([]byte, error) {
			sellTx, sErr := adapter.BuildUnsigned(ports.QuoteRequest{
				Route: route, Side: ports.SideSell, AmountInRaw: bought, Recipient: probeWallet, ContractExpiry: deadline,
			}, ports.QuoteResult{MinOutRaw: big.NewInt(0)})
			if sErr != nil {
				return nil, sErr
			}
			return sellTx.Calldata, nil
		}
	} else {
		routerAddr = basechain.UniswapV3SwapRouter02
		wrappedAddr = basechain.WETHAddress
		return "Base honeypot simulation requires pool fee resolution; use /quote on Base.", nil
	}

	sim := honeypot.NewSimulator(cfg.RPCURL, 10*time.Second)
	res, err := sim.SimulateBuyApproveSell(ctx, honeypot.SimulationRequest{
		ProbeWallet:         probeWallet,
		Router:              routerAddr,
		Token:               token,
		WrappedNative:       wrappedAddr,
		ProbeNativeWei:      probeWei,
		BuyCalldata:         buyCalldata,
		ApproveCalldata:     approveCalldata,
		BuildSellCalldata:   buildSell,
		MaxRoundtripLossBps: 1500,
	})
	if err != nil {
		return fmt.Sprintf("Honeypot simulation unavailable on current RPC: %v", err), nil
	}
	return fmt.Sprintf(
		"Honeypot Check chain=%d token=%s\nverdict=%s safe=%v\nroundtrip_loss=%.2f%% max_gas=%d\ndetail=%s",
		chainID, strings.ToLower(token.Hex()), res.Verdict, res.Safe, float64(res.RoundtripLossBps)/100.0, res.MaxGasUsed, res.Detail,
	), nil
}

func (u *UDSBackend) SubmitManualTrade(_ context.Context, _ uint64, _, _ string, _ common.Address, _ *big.Int) (string, error) {
	return "", errors.New("live trade submission is disabled in read-only mode (live_enabled=false)")
}

// AddWatchedWallet persists trader into bot.db watched_wallets and pushes a signed
// PUT /internal/v1/watchlist snapshot to feed-<chain>.sock.
func (u *UDSBackend) AddWatchedWallet(ctx context.Context, chainID uint64, trader common.Address) error {
	traderHex := strings.ToLower(trader.Hex())
	var wallets []string
	if u.db != nil {
		id := fmt.Sprintf("ww-%d-%s", chainID, traderHex[2:10])
		if _, err := u.db.ExecContext(ctx,
			`INSERT INTO watched_wallets(id, chain_id, address, enabled) VALUES(?,?,?,1)
			 ON CONFLICT(chain_id, address) DO UPDATE SET enabled = 1`,
			id, chainID, traderHex,
		); err != nil {
			return fmt.Errorf("persist watched wallet: %w", err)
		}
		rows, err := u.db.QueryContext(ctx, `SELECT address FROM watched_wallets WHERE chain_id = ? AND enabled = 1`, chainID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var addr string
				if rows.Scan(&addr) == nil {
					wallets = append(wallets, addr)
				}
			}
		}
	} else {
		wallets = []string{traderHex}
	}

	cfg, ok := u.chains[chainID]
	if !ok || cfg.FeedSocket == "" || u.auth == nil {
		return nil
	}
	u.watchMu.Lock()
	u.watchVer++
	ver := u.watchVer
	u.watchMu.Unlock()

	body, err := json.Marshal(map[string]any{
		"version": ver,
		"wallets": wallets,
		"tokens":  []string{},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://unix/internal/v1/watchlist", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := u.auth.Sign(req, body); err != nil {
		return fmt.Errorf("sign watchlist push: %w", err)
	}
	resp, err := u.udsClient(cfg.FeedSocket).Do(req)
	if err != nil {
		return fmt.Errorf("push watchlist to feed-service: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("feed watchlist push status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func (u *UDSBackend) ConfigureCopyStrategy(_ context.Context, _ uint64, _ common.Address, _ *big.Int) error {
	return nil
}

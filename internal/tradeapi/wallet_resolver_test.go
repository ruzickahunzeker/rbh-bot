package tradeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	pancakev2 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/pancakeswap/v2"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
	"github.com/ruzickahunzeker/rbh-bot/internal/registry"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	tradecore "github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

type recordingWalletProvider struct {
	lastQuoteWallet common.Address
	lastPosWallet   common.Address
}

func (p *recordingWalletProvider) QuotePreflight(_ context.Context, _ QuotePreflightRequest, _ common.Address) (QuotePreflightResponse, error) {
	return QuotePreflightResponse{}, nil
}

func (p *recordingWalletProvider) QuotePreflightForWallet(_ context.Context, req QuotePreflightRequest, walletAddr common.Address, spender common.Address) (QuotePreflightResponse, error) {
	p.lastQuoteWallet = walletAddr
	return QuotePreflightResponse{
		ChainID:          56,
		Protocol:         req.Protocol,
		PoolPhase:        "DEX_V2",
		Spender:          strings.ToLower(spender.Hex()),
		ExpectedOutRaw:   "12345",
		MinOutRaw:        "12000",
		EstimatedGas:     200000,
		CurrentAllowance: "0",
		NeedsApprove:     req.Side == "sell",
		QuoteBlockNumber: 100,
	}, nil
}

func (p *recordingWalletProvider) ReadPosition(_ context.Context, walletID string, walletAddr common.Address, token common.Address, _ common.Address, _ uint64) (PositionSnapshot, error) {
	p.lastPosWallet = walletAddr
	return PositionSnapshot{
		ChainID:          56,
		WalletID:         walletID,
		WalletAddress:    strings.ToLower(walletAddr.Hex()),
		WalletState:      "ready",
		Token:            strings.ToLower(token.Hex()),
		NativeBalanceRaw: "500000000000000000",
		TokenBalanceRaw:  "1000000",
		Decimals:         18,
		BlockNumber:      100,
	}, nil
}

func TestLocalV2DynamicGlobalWalletResolutionAndScopeDenial(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.TradeOwner, filepath.Join(t.TempDir(), "trade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	ksDir := t.TempDir()
	globalSum, err := tradecore.CreateGlobalEncryptedWallet(ksDir, "w-evm-global-1", 7131128175, "StrongPass#2026")
	if err != nil {
		t.Fatal(err)
	}

	primaryAddr := common.HexToAddress("0x00000000000000000000000000000000000000AA")
	token := strings.Repeat("a", 32)
	pol := DefaultPolicy()
	pol.AllowedChain = "bsc"
	api, err := New(store, Identity{
		PrincipalID:   "operator-bsc",
		WalletID:      "wallet-bsc-readonly",
		WalletAddress: primaryAddr.Hex(),
	}, token, pol)
	if err != nil {
		t.Fatal(err)
	}

	reg, err := registry.New(bsc.Profile(), []ports.ProtocolAdapter{pancakev2.NewAdapter()}, map[string]registry.TrustedSpender{
		pancakev2.ProtocolID: {Address: bsc.PancakeV2Router},
	})
	if err != nil {
		t.Fatal(err)
	}
	prov := &recordingWalletProvider{}
	ext, err := NewLocalV2Extension(reg, prov, nil)
	if err != nil {
		t.Fatal(err)
	}
	ext.SetWalletResolver(NewKeystoreDirWalletResolver(ksDir))
	if err := api.AttachLocalV2(ext); err != nil {
		t.Fatal(err)
	}

	// 1. Authorized global keystore wallet resolves to globalSum.Address on /v1/quotes.
	qBody, _ := json.Marshal(QuotePreflightRequest{
		Chain: "bsc", WalletID: "w-evm-global-1", Protocol: pancakev2.ProtocolID,
		Token: "0x55d398326f99059fF775485246999027B3197955", Side: "sell", AmountRaw: "1000", SlippageBps: 100,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/quotes", bytes.NewReader(qBody))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || prov.lastQuoteWallet != globalSum.Address {
		t.Fatalf("expected 200 with wallet %s, got code=%d wallet=%s body=%s", globalSum.Address.Hex(), rec.Code, prov.lastQuoteWallet.Hex(), rec.Body.String())
	}

	// 2. Authorized global keystore wallet resolves to globalSum.Address on /v1/wallets/{id}/positions.
	posReq := httptest.NewRequest(http.MethodGet, "/v1/wallets/w-evm-global-1/positions?token=0x55d398326f99059fF775485246999027B3197955", nil)
	posReq.Header.Set("Authorization", "Bearer "+token)
	posRec := httptest.NewRecorder()
	api.ServeHTTP(posRec, posReq)
	if posRec.Code != http.StatusOK || prov.lastPosWallet != globalSum.Address {
		t.Fatalf("expected 200 with position wallet %s, got code=%d wallet=%s body=%s", globalSum.Address.Hex(), posRec.Code, prov.lastPosWallet.Hex(), posRec.Body.String())
	}

	// 3. Unknown / unauthorized wallet_id MUST be rejected with 403 WALLET_SCOPE_DENIED (never falls back!).
	badReq := httptest.NewRequest(http.MethodGet, "/v1/wallets/unauthorized-wallet/positions?token=0x55d398326f99059fF775485246999027B3197955", nil)
	badReq.Header.Set("Authorization", "Bearer "+token)
	badRec := httptest.NewRecorder()
	api.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unauthorized wallet, got %d body=%s", badRec.Code, badRec.Body.String())
	}
}

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
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/robinhood"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/pons/v2curve"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
	"github.com/ruzickahunzeker/rbh-bot/internal/registry"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type stubLocalV2Provider struct {
	block uint64
}

func (s *stubLocalV2Provider) QuotePreflight(_ context.Context, req QuotePreflightRequest, spender common.Address) (QuotePreflightResponse, error) {
	return QuotePreflightResponse{
		ChainID:          4663,
		Protocol:         req.Protocol,
		PoolPhase:        "CURVE",
		Spender:          strings.ToLower(spender.Hex()),
		ExpectedOutRaw:   "950000000000000000000",
		MinOutRaw:        "921500000000000000000",
		EstimatedGas:     180000,
		CurrentAllowance: "0",
		NeedsApprove:     req.Side == "sell",
		QuoteBlockNumber: s.block,
	}, nil
}

func (s *stubLocalV2Provider) ReadPosition(_ context.Context, walletID string, walletAddr, token, spender common.Address, requiredBlock uint64) (PositionSnapshot, error) {
	if err := evm.CheckRequiredBlock(s.block, requiredBlock); err != nil {
		return PositionSnapshot{}, err
	}
	return PositionSnapshot{
		ChainID:           4663,
		WalletID:          walletID,
		WalletAddress:     strings.ToLower(walletAddr.Hex()),
		WalletState:       "ready",
		Token:             strings.ToLower(token.Hex()),
		NativeBalanceRaw:  "1000000000000000000",
		TokenBalanceRaw:   "950000000000000000000",
		Decimals:          18,
		AllowanceRaw:      "0",
		Spender:           strings.ToLower(spender.Hex()),
		HasUnfinalizedBuy: true,
		BlockNumber:       s.block,
	}, nil
}

func TestStep15LocalQuotesApprovalsAndPositions(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.TradeOwner, filepath.Join(t.TempDir(), "step15.db"))
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
	id := Identity{PrincipalID: "tg-bot", WalletID: "wallet-01", WalletAddress: "0x1000000000000000000000000000000000000001"}
	api, err := NewWithOptions(store, id, testCredential, DefaultPolicy(), nil, Options{WalletAccess: &WalletAccessPolicy{PrimaryCanSign: true}})
	if err != nil {
		t.Fatal(err)
	}

	trustedSpender := common.HexToAddress("0x5000000000000000000000000000000000000005")
	reg, err := registry.New(
		robinhood.Profile(),
		[]ports.ProtocolAdapter{v2curve.NewAdapter()},
		map[string]registry.TrustedSpender{
			v2curve.ProtocolID: {Address: trustedSpender, ExpectedCodeHash: common.HexToHash("0x1234")},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	provider := &stubLocalV2Provider{block: 100}
	ext, err := NewLocalV2Extension(reg, provider, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	if err := api.ConfigureWalletResolver(ext, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := api.AttachLocalV2(ext); err != nil {
		t.Fatal(err)
	}

	// 1. POST /v1/quotes (read-only preflight, zero DB writes).
	qReq := httptest.NewRequest(http.MethodPost, "/v1/quotes", bytes.NewReader([]byte(`{"chain":"robinhood","wallet_id":"wallet-01","protocol":"pons-v2-curve","token":"0x2000000000000000000000000000000000000002","side":"sell","amount_raw":"950000000000000000000","slippage_bps":300}`)))
	qReq.Header.Set("Authorization", "Bearer "+testCredential)
	qw := httptest.NewRecorder()
	api.ServeHTTP(qw, qReq)
	if qw.Code != http.StatusOK || !strings.Contains(qw.Body.String(), `"needs_approve":true`) {
		t.Fatalf("POST /v1/quotes failed: %d %s", qw.Code, qw.Body.String())
	}

	// 2. AT-10 / AT-25: POST /v1/approvals rejects arbitrary caller-supplied "spender" field!
	badApprove := httptest.NewRequest(http.MethodPost, "/v1/approvals", bytes.NewReader([]byte(`{"chain":"rbh","wallet_id":"wallet-01","protocol":"pons-v2-curve","token":"0x2000000000000000000000000000000000000002","spender":"0xdead000000000000000000000000000000000000","amount_raw":"1000000000000000000000000","ttl_seconds":60}`)))
	badApprove.Header.Set("Authorization", "Bearer "+testCredential)
	badApprove.Header.Set("Idempotency-Key", "appr-bad-1")
	bw := httptest.NewRecorder()
	api.ServeHTTP(bw, badApprove)
	if bw.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting injected spender field, got %d %s", bw.Code, bw.Body.String())
	}

	// 3. Valid POST /v1/approvals derives spender from registry, sets native_input_wei="0", and supports symmetrical GET recovery.
	apprBody := `{"chain":"rbh","wallet_id":"wallet-01","protocol":"pons-v2-curve","token":"0x2000000000000000000000000000000000000002","amount_raw":"1000000000000000000000000","ttl_seconds":60}`
	aReq := httptest.NewRequest(http.MethodPost, "/v1/approvals", bytes.NewReader([]byte(apprBody)))
	aReq.Header.Set("Authorization", "Bearer "+testCredential)
	aReq.Header.Set("Idempotency-Key", "appr-ok-1")
	aw := httptest.NewRecorder()
	api.ServeHTTP(aw, aReq)
	if aw.Code != http.StatusAccepted {
		t.Fatalf("POST /v1/approvals failed: %d %s", aw.Code, aw.Body.String())
	}
	var appr ApprovalRecord
	if err := json.Unmarshal(aw.Body.Bytes(), &appr); err != nil {
		t.Fatal(err)
	}
	if appr.Spender != strings.ToLower(trustedSpender.Hex()) || appr.NativeInputWei != "0" || appr.OperationKind != "approval" {
		t.Fatalf("unexpected approval record: %+v", appr)
	}
	// Lookup by idempotency key and by request_id (response-loss recovery).
	byKeyReq := httptest.NewRequest(http.MethodGet, "/v1/approvals/by-idempotency-key?idempotency_key=appr-ok-1", nil)
	byKeyReq.Header.Set("Authorization", "Bearer "+testCredential)
	kw := httptest.NewRecorder()
	api.ServeHTTP(kw, byKeyReq)
	if kw.Code != http.StatusOK || !strings.Contains(kw.Body.String(), appr.RequestID) {
		t.Fatalf("GET /v1/approvals/by-idempotency-key failed: %d %s", kw.Code, kw.Body.String())
	}

	// 3b. P1-1: Re-approving the EXACT same (token, amount) with a NEW Idempotency-Key must succeed (no request_id collision).
	aReq2 := httptest.NewRequest(http.MethodPost, "/v1/approvals", bytes.NewReader([]byte(apprBody)))
	aReq2.Header.Set("Authorization", "Bearer "+testCredential)
	aReq2.Header.Set("Idempotency-Key", "appr-ok-2")
	aw2 := httptest.NewRecorder()
	api.ServeHTTP(aw2, aReq2)
	if aw2.Code != http.StatusAccepted {
		t.Fatalf("second approval with new key failed: %d %s", aw2.Code, aw2.Body.String())
	}
	var appr2 ApprovalRecord
	if err := json.Unmarshal(aw2.Body.Bytes(), &appr2); err != nil || appr2.RequestID == appr.RequestID {
		t.Fatalf("expected distinct request_id for new idempotency key: first=%s second=%s err=%v", appr.RequestID, appr2.RequestID, err)
	}

	// 3c. P1-1: Trailing JSON garbage must be rejected with 400.
	badTrail := httptest.NewRequest(http.MethodPost, "/v1/approvals", bytes.NewReader([]byte(apprBody+`{"extra":1}`)))
	badTrail.Header.Set("Authorization", "Bearer "+testCredential)
	badTrail.Header.Set("Idempotency-Key", "appr-trail-1")
	tw := httptest.NewRecorder()
	api.ServeHTTP(tw, badTrail)
	if tw.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting trailing JSON garbage, got %d %s", tw.Code, tw.Body.String())
	}

	// 4. AT-22: GET /v1/wallets/{id}/positions returns 503 RPC_STATE_LAGGING (retryable=true) when node lags behind required block.
	ext.SetWalletCanonicalBlock("wallet-01", 101) // wallet's BUY confirmed at 101, stub node still at 100
	posReq := httptest.NewRequest(http.MethodGet, "/v1/wallets/wallet-01/positions?token=0x2000000000000000000000000000000000000002&protocol=pons-v2-curve", nil)
	posReq.Header.Set("Authorization", "Bearer "+testCredential)
	pw := httptest.NewRecorder()
	api.ServeHTTP(pw, posReq)
	if pw.Code != http.StatusServiceUnavailable || !strings.Contains(pw.Body.String(), `"RPC_STATE_LAGGING"`) || !strings.Contains(pw.Body.String(), `"retryable":true`) {
		t.Fatalf("expected 503 RPC_STATE_LAGGING retryable=true, got %d %s", pw.Code, pw.Body.String())
	}
	// Once node catches up to 101, exact atomic position returns 200 OK.
	provider.block = 101
	pw2 := httptest.NewRecorder()
	api.ServeHTTP(pw2, posReq)
	if pw2.Code != http.StatusOK || !strings.Contains(pw2.Body.String(), `"token_balance_raw":"950000000000000000000"`) {
		t.Fatalf("expected 200 OK exact position, got %d %s", pw2.Code, pw2.Body.String())
	}

	// 5. AT-26: GET /v1/trade-updates?after_seq=0 returns durable approval event with operation_kind="approval",
	// and out-of-range cursor returns 410 Gone (CURSOR_OUT_OF_RANGE).
	uReq := httptest.NewRequest(http.MethodGet, "/v1/trade-updates?after_seq=0&limit=10", nil)
	uReq.Header.Set("Authorization", "Bearer "+testCredential)
	uw := httptest.NewRecorder()
	api.ServeHTTP(uw, uReq)
	if uw.Code != http.StatusOK || !strings.Contains(uw.Body.String(), `"operation_kind":"approval"`) || !strings.Contains(uw.Body.String(), appr.RequestID) {
		t.Fatalf("GET /v1/trade-updates failed: %d %s", uw.Code, uw.Body.String())
	}
	// Out-of-range cursor (after_seq > max_seq) must return 410 Gone.
	goneReq := httptest.NewRequest(http.MethodGet, "/v1/trade-updates?after_seq=999", nil)
	goneReq.Header.Set("Authorization", "Bearer "+testCredential)
	gw := httptest.NewRecorder()
	api.ServeHTTP(gw, goneReq)
	if gw.Code != http.StatusGone || !strings.Contains(gw.Body.String(), "CURSOR_OUT_OF_RANGE") {
		t.Fatalf("expected 410 CURSOR_OUT_OF_RANGE, got %d %s", gw.Code, gw.Body.String())
	}
}

func TestBSCMultiChainWalletTradeIntakeAndAuditUpdates(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.TradeOwner, filepath.Join(t.TempDir(), "trade-bsc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.VerifyOrInitChainIdentity(ctx, storage.ChainIdentity{ChainID: 56, ChainSlug: "bsc", NetworkKind: "mainnet"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	walletAddr := common.HexToAddress("0x1000000000000000000000000000000000000056")
	testNow := time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC)
	if err := store.RegisterMultiChainWallet(ctx, 56, "w-bsc-1", walletAddr, testNow); err != nil {
		t.Fatalf("RegisterMultiChainWallet failed: %v", err)
	}
	api, err := New(store, Identity{PrincipalID: "p-bsc", WalletID: "w-bsc-1", WalletAddress: walletAddr.Hex()}, testCredential, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	api.now = func() time.Time { return testNow }

	bscReg, err := registry.New(
		evm.ChainProfile{ChainID: 56, ChainSlug: "bsc", NetworkKind: "mainnet", NativeSymbol: "BNB"},
		nil,
		map[string]registry.TrustedSpender{"pancake-v2": {Address: common.HexToAddress("0x10ED43C718714eb63d5aA57B78B54704E256024E")}},
	)
	if err != nil {
		t.Fatal(err)
	}
	ext, err := NewLocalV2Extension(bscReg, nil, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	api.walletAccess = WalletAccessPolicy{PrimaryCanSign: true}
	if err := api.ConfigureWalletResolver(ext, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := api.AttachLocalV2(ext); err != nil {
		t.Fatal(err)
	}

	wrongReq := httptest.NewRequest(http.MethodPost, "/v1/trades", bytes.NewReader([]byte(`{"chain":"robinhood","wallet_id":"w-bsc-1","token":"0x2000000000000000000000000000000000000002","side":"buy","amount":"0.01","slippage_percent":"1"}`)))
	wrongReq.Header.Set("Authorization", "Bearer "+testCredential)
	wrongReq.Header.Set("Content-Type", "application/json")
	wrongReq.Header.Set("Idempotency-Key", "bsc-wrong-1")
	ww := httptest.NewRecorder()
	api.ServeHTTP(ww, wrongReq)
	if ww.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 rejecting robinhood order on BSC instance, got %d %s", ww.Code, ww.Body.String())
	}

	okReq := httptest.NewRequest(http.MethodPost, "/v1/trades", bytes.NewReader([]byte(`{"chain":"bsc","wallet_id":"w-bsc-1","token":"0x2000000000000000000000000000000000000002","side":"buy","amount":"0.01","slippage_percent":"1"}`)))
	okReq.Header.Set("Authorization", "Bearer "+testCredential)
	okReq.Header.Set("Content-Type", "application/json")
	okReq.Header.Set("Idempotency-Key", "bsc-ok-1")
	ow := httptest.NewRecorder()
	api.ServeHTTP(ow, okReq)
	if ow.Code != http.StatusServiceUnavailable || !strings.Contains(ow.Body.String(), `"CONTRACT_DEADLINE_UNIX"`) {
		t.Fatalf("expected disabled-send intake rejection journal with CONTRACT_DEADLINE_UNIX on BSC, got %d %s", ow.Code, ow.Body.String())
	}

	uReq := httptest.NewRequest(http.MethodGet, "/v1/trade-updates?after_seq=0&limit=10", nil)
	uReq.Header.Set("Authorization", "Bearer "+testCredential)
	uw := httptest.NewRecorder()
	api.ServeHTTP(uw, uReq)
	if uw.Code != http.StatusOK || !strings.Contains(uw.Body.String(), `"operation_kind":"trade"`) {
		t.Fatalf("expected BSC trade event in /v1/trade-updates, got %d %s", uw.Code, uw.Body.String())
	}
}

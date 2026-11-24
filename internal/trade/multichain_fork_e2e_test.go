package trade

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	pancakev2 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/pancakeswap/v2"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
	"github.com/ruzickahunzeker/rbh-bot/internal/registry"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestPrivateRelayBroadcasterFailClosedAndMerge(t *testing.T) {
	// 1. private_only with empty private endpoints must fail closed (never fall back to public RPC).
	if _, err := NewMultiRelayBroadcaster(RelayModePrivateOnly, nil, "https://bsc-rpc.publicnode.com", 2*time.Second); !errors.Is(err, ErrPrivateRelayRequired) {
		t.Fatalf("expected ErrPrivateRelayRequired, got %v", err)
	}

	txHash := common.HexToHash("0x1111")
	rawTx := []byte{0x02, 0xf8, 0x01}

	rejectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32000, "message": "nonce too low"}})
	}))
	defer rejectSrv.Close()

	timeoutSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer timeoutSrv.Close()

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": txHash.Hex()})
	}))
	defer okSrv.Close()

	// 2. [rejected, timeout] -> conservative BROADCAST_UNKNOWN
	bUnknown, err := NewMultiRelayBroadcaster(RelayModePrivateOnly, []string{rejectSrv.URL, timeoutSrv.URL}, "", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := bUnknown.BroadcastRawTx(context.Background(), txHash, rawTx)
	if !errors.Is(err, ErrRelayBroadcastUnknown) || res.MergedState != MergedBroadcastUnknown {
		t.Fatalf("expected MergedBroadcastUnknown, got state=%s err=%v", res.MergedState, err)
	}

	// 3. [rejected, submitted] -> SUBMITTED
	bOK, err := NewMultiRelayBroadcaster(RelayModePrivateOnly, []string{rejectSrv.URL, okSrv.URL}, "", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err = bOK.BroadcastRawTx(context.Background(), txHash, rawTx)
	if err != nil || res.MergedState != MergedSubmitted {
		t.Fatalf("expected MergedSubmitted, got state=%s err=%v", res.MergedState, err)
	}

	// 4. [rejected, rejected] -> KNOWN_UNSENT
	bRej, err := NewMultiRelayBroadcaster(RelayModePrivateOnly, []string{rejectSrv.URL, rejectSrv.URL}, "", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err = bRej.BroadcastRawTx(context.Background(), txHash, rawTx)
	if !errors.Is(err, ErrBroadcastRejected) || res.MergedState != MergedKnownUnsent {
		t.Fatalf("expected MergedKnownUnsent, got state=%s err=%v", res.MergedState, err)
	}
}

// statefulBSCForkBackend simulates a local BSC EVM fork for deterministic E2E testing:
// verifies real EIP-1559 signatures, tracks wallet nonce, ERC-20 balance & allowance,
// applies a 5% Fee-on-Transfer (FoT) tax on buy/sell, and produces authentic Transfer/WBNB receipts.
type statefulBSCForkBackend struct {
	mu           sync.Mutex
	chainID      uint64
	wallet       common.Address
	token        common.Address
	pair         common.Address
	blockNum     uint64
	blockHash    common.Hash
	nonce        uint64
	tokenBalance *big.Int
	allowance    *big.Int
	failApprove  bool
	receipts     map[common.Hash]*gethtypes.Receipt
	txs          map[common.Hash]*gethtypes.Transaction
}

var (
	forkFactoryABI, _ = abi.JSON(strings.NewReader(`[{"name":"getPair","type":"function","inputs":[{"type":"address"},{"type":"address"}],"outputs":[{"type":"address"}]}]`))
	forkRouterABI, _  = abi.JSON(strings.NewReader(`[{"name":"getAmountsOut","type":"function","inputs":[{"type":"uint256"},{"type":"address[]"}],"outputs":[{"type":"uint256[]"}]}]`))
	forkTransferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	forkWBNBWithdraw  = crypto.Keccak256Hash([]byte("Withdrawal(address,uint256)"))
)

func newStatefulBSCFork(wallet, token common.Address) *statefulBSCForkBackend {
	return &statefulBSCForkBackend{
		chainID:      bsc.ChainID,
		wallet:       wallet,
		token:        token,
		pair:         common.HexToAddress("0x000000000000000000000000000000000000FA12"),
		blockNum:     50_000_000,
		blockHash:    common.HexToHash("0xb5c00001"),
		tokenBalance: big.NewInt(0),
		allowance:    big.NewInt(0),
		receipts:     make(map[common.Hash]*gethtypes.Receipt),
		txs:          make(map[common.Hash]*gethtypes.Transaction),
	}
}

func (f *statefulBSCForkBackend) CodeAt(_ context.Context, _ common.Address, _ common.Hash) ([]byte, error) {
	return []byte{0x60, 0x80, 0x60, 0x40}, nil
}

func (f *statefulBSCForkBackend) CallContractAt(_ context.Context, to common.Address, data []byte, _ common.Hash) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(data) < 4 {
		return nil, errors.New("short calldata")
	}
	switch {
	case to == bsc.PancakeV2Factory && bytes.Equal(data[:4], forkFactoryABI.Methods["getPair"].ID):
		return forkFactoryABI.Methods["getPair"].Outputs.Pack(f.pair)
	case to == bsc.PancakeV2Router && bytes.Equal(data[:4], forkRouterABI.Methods["getAmountsOut"].ID):
		args, err := forkRouterABI.Methods["getAmountsOut"].Inputs.Unpack(data[4:])
		if err != nil {
			return nil, err
		}
		amtIn := args[0].(*big.Int)
		path := args[1].([]common.Address)
		var out *big.Int
		if path[0] == bsc.WBNBAddress {
			// Buy quote: 1e17 wei (0.1 BNB) -> 1,000,000 raw tokens pre-tax
			out = new(big.Int).Div(amtIn, big.NewInt(1e11))
		} else {
			// Sell quote: 950,000 raw tokens -> 9.025e16 wei BNB
			out = new(big.Int).Mul(amtIn, big.NewInt(95_000_000_000))
		}
		return forkRouterABI.Methods["getAmountsOut"].Outputs.Pack([]*big.Int{amtIn, out})
	case to == f.token && bytes.Equal(data[:4], erc20ExecABI.Methods["allowance"].ID):
		return erc20ExecABI.Methods["allowance"].Outputs.Pack(f.allowance)
	default:
		return nil, errors.New("unexpected contract call")
	}
}

func (f *statefulBSCForkBackend) LatestBlock(_ context.Context) (uint64, common.Hash, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.blockNum, f.blockHash, nil
}

func (f *statefulBSCForkBackend) PendingNonce(_ context.Context, _ common.Address) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nonce, nil
}

func (f *statefulBSCForkBackend) SuggestFees(_ context.Context) (*big.Int, *big.Int, error) {
	return big.NewInt(3_000_000_000), big.NewInt(1_000_000_000), nil
}

func (f *statefulBSCForkBackend) WaitReceipt(_ context.Context, txHash common.Hash) (*gethtypes.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rcpt, ok := f.receipts[txHash]
	if !ok {
		return nil, errors.New("receipt not found")
	}
	return rcpt, nil
}

// executeRawTx verifies signature + nonce and executes Buy, Approve, Sell (5% FoT), or Cancel.
func (f *statefulBSCForkBackend) executeRawTx(rawHex string) (common.Hash, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rawBytes, err := hex.DecodeString(strings.TrimPrefix(rawHex, "0x"))
	if err != nil {
		return common.Hash{}, err
	}
	var tx gethtypes.Transaction
	if err := tx.UnmarshalBinary(rawBytes); err != nil {
		return common.Hash{}, err
	}
	signer := gethtypes.LatestSignerForChainID(new(big.Int).SetUint64(f.chainID))
	from, err := signer.Sender(&tx)
	if err != nil || from != f.wallet {
		return common.Hash{}, errors.New("invalid tx signature or sender")
	}
	if tx.Nonce() != f.nonce {
		return common.Hash{}, errors.New("nonce mismatch")
	}
	f.nonce++
	f.blockNum++
	f.blockHash = crypto.Keccak256Hash(f.blockHash.Bytes())
	f.txs[tx.Hash()] = &tx

	rcpt := &gethtypes.Receipt{
		Status:    gethtypes.ReceiptStatusSuccessful,
		TxHash:    tx.Hash(),
		BlockHash: f.blockHash,
		GasUsed:   120_000,
	}

	to := tx.To()
	data := tx.Data()
	switch {
	case to != nil && *to == f.wallet && tx.Value().Sign() == 0 && len(data) == 0:
		// Same-nonce 0-value Cancel self-transfer
		rcpt.GasUsed = 21_000
	case to != nil && *to == f.token && len(data) >= 4 && bytes.Equal(data[:4], erc20ExecABI.Methods["approve"].ID):
		if f.failApprove {
			rcpt.Status = gethtypes.ReceiptStatusFailed
		} else {
			args, _ := erc20ExecABI.Methods["approve"].Inputs.Unpack(data[4:])
			f.allowance = new(big.Int).Set(args[1].(*big.Int))
		}
	case to != nil && *to == bsc.PancakeV2Router && tx.Value().Sign() > 0:
		// Buy: 5% FoT tax -> 0.1 BNB yields 950,000 net tokens (5% less than 1,000,000)
		gross := new(big.Int).Div(tx.Value(), big.NewInt(1e11))
		netTokens := new(big.Int).Mul(gross, big.NewInt(95))
		netTokens.Div(netTokens, big.NewInt(100))
		f.tokenBalance.Add(f.tokenBalance, netTokens)
		tokData := make([]byte, 32)
		netTokens.FillBytes(tokData)
		rcpt.Logs = []*gethtypes.Log{
			{
				Address: f.token,
				Topics:  []common.Hash{forkTransferTopic, common.BytesToHash(f.pair.Bytes()), common.BytesToHash(f.wallet.Bytes())},
				Data:    tokData,
			},
		}
	case to != nil && *to == bsc.PancakeV2Router && tx.Value().Sign() == 0:
		// Sell: must use swapExactTokensForETHSupportingFeeOnTransferTokens (0x791ac947)
		if len(data) < 4 || hex.EncodeToString(data[:4]) != "791ac947" {
			rcpt.Status = gethtypes.ReceiptStatusFailed
		} else if f.allowance.Cmp(f.tokenBalance) < 0 {
			rcpt.Status = gethtypes.ReceiptStatusFailed
		} else {
			sellAmt := new(big.Int).Set(f.tokenBalance)
			f.tokenBalance = big.NewInt(0)
			f.allowance = big.NewInt(0)
			tokData := make([]byte, 32)
			sellAmt.FillBytes(tokData)
			wbnbOut := new(big.Int).Mul(sellAmt, big.NewInt(90_000_000_000))
			wbnbData := make([]byte, 32)
			wbnbOut.FillBytes(wbnbData)
			rcpt.Logs = []*gethtypes.Log{
				{
					Address: f.token,
					Topics:  []common.Hash{forkTransferTopic, common.BytesToHash(f.wallet.Bytes()), common.BytesToHash(f.pair.Bytes())},
					Data:    tokData,
				},
				{
					Address: bsc.WBNBAddress,
					Topics:  []common.Hash{forkWBNBWithdraw, common.BytesToHash(bsc.PancakeV2Router.Bytes())},
					Data:    wbnbData,
				},
			}
		}
	}
	f.receipts[tx.Hash()] = rcpt
	return tx.Hash(), nil
}

type blockingHoneypotGuard struct{ block bool }

func (g blockingHoneypotGuard) CheckBeforeBuy(_ context.Context, _ ports.ResolvedRoute, _ *big.Int) error {
	if g.block {
		return errors.New("simulated sell tax 100% (honeypot)")
	}
	return nil
}

func TestMultiChainExecutorLiveGateAndHoneypotBlock(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.TradeOwner, filepath.Join(t.TempDir(), "trade-gate.db"))
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
	reg, err := registry.New(bsc.Profile(), []ports.ProtocolAdapter{pancakev2.NewAdapter()}, map[string]registry.TrustedSpender{
		pancakev2.ProtocolID: {Address: bsc.PancakeV2Router},
	})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := crypto.GenerateKey()
	wallet := crypto.PubkeyToAddress(key.PublicKey)
	token := common.HexToAddress("0x000000000000000000000000000000000000F077")
	fork := newStatefulBSCFork(wallet, token)
	dummyRelay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("broadcast must NOT be called when live_enabled=false or honeypot blocked")
	}))
	defer dummyRelay.Close()
	broadcaster, _ := NewMultiRelayBroadcaster(RelayModePrivateOnly, []string{dummyRelay.URL}, "", 2*time.Second)

	// 1. live_enabled=false must fail closed before signing/broadcast
	execOff, err := NewMultiChainExecutor(false, reg, store, fork, broadcaster, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execOff.Execute(ctx, MultiChainOrderRequest{
		OperationID: "op-disabled",
		ProtocolID:  pancakev2.ProtocolID,
		Token:       token,
		Side:        ports.SideBuy,
		AmountInRaw: big.NewInt(1e17),
		SlippageBps: 500,
	}, time.Now()); !errors.Is(err, ErrLiveExecutionDisabled) {
		t.Fatalf("expected ErrLiveExecutionDisabled, got %v", err)
	}

	// 2. Honeypot guard must block before signing or touching nonce
	execHP, err := NewMultiChainExecutor(true, reg, store, fork, broadcaster, blockingHoneypotGuard{block: true}, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execHP.Execute(ctx, MultiChainOrderRequest{
		OperationID: "op-honeypot",
		ProtocolID:  pancakev2.ProtocolID,
		Token:       token,
		Side:        ports.SideBuy,
		AmountInRaw: big.NewInt(1e17),
		SlippageBps: 500,
	}, time.Now()); !errors.Is(err, ErrHoneypotBlocked) {
		t.Fatalf("expected ErrHoneypotBlocked, got %v", err)
	}
	if fork.nonce != 0 {
		t.Fatalf("expected nonce untouched (0), got %d", fork.nonce)
	}
}

func TestMultiChainForkE2E_BuyApproveSell_FoTTax_And_SameNonceCancel(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 23, 0, 0, 0, time.UTC)
	db, err := storage.Open(ctx, storage.TradeOwner, filepath.Join(t.TempDir(), "trade-fork-e2e.db"))
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
	reg, err := registry.New(bsc.Profile(), []ports.ProtocolAdapter{pancakev2.NewAdapter()}, map[string]registry.TrustedSpender{
		pancakev2.ProtocolID: {Address: bsc.PancakeV2Router},
	})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := crypto.GenerateKey()
	wallet := crypto.PubkeyToAddress(key.PublicKey)
	token := common.HexToAddress("0x000000000000000000000000000000000000F077")
	fork := newStatefulBSCFork(wallet, token)

	var forceTimeout bool
	// Local private relay + read RPC backed by our stateful BSC fork.
	privateRelaySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string   `json:"method"`
			Params []string `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "eth_sendRawTransaction" {
			if forceTimeout {
				w.WriteHeader(http.StatusGatewayTimeout)
				return
			}
			h, err := fork.executeRawTx(req.Params[0])
			if err != nil {
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32000, "message": err.Error()}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": h.Hex()})
		}
	}))
	defer privateRelaySrv.Close()

	broadcaster, err := NewMultiRelayBroadcaster(RelayModePrivateOnly, []string{privateRelaySrv.URL}, "", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	exec, err := NewMultiChainExecutor(true, reg, store, fork, broadcaster, blockingHoneypotGuard{block: false}, key)
	if err != nil {
		t.Fatal(err)
	}

	// 1. BUY 0.1 BNB of 5% FoT tax token -> receives 950,000 net tokens (nonce 0).
	buyRes, err := exec.Execute(ctx, MultiChainOrderRequest{
		OperationID: "op-buy-1",
		ProtocolID:  pancakev2.ProtocolID,
		Token:       token,
		Side:        ports.SideBuy,
		AmountInRaw: big.NewInt(1e17),
		SlippageBps: 800,
	}, now)
	if err != nil {
		t.Fatalf("Buy failed: %v", err)
	}
	if !buyRes.Effects.EvidenceComplete || buyRes.Effects.NetTokenDeltaRaw.Cmp(big.NewInt(950_000)) != 0 {
		t.Fatalf("unexpected buy effects: %+v", buyRes.Effects)
	}

	// 2. APPROVE REVERT -> Sell step 1 MUST remain locked (AT-11).
	fork.failApprove = true
	if _, err := exec.Execute(ctx, MultiChainOrderRequest{
		OperationID: "op-sell-locked",
		ProtocolID:  pancakev2.ProtocolID,
		Token:       token,
		Side:        ports.SideSell,
		AmountInRaw: big.NewInt(950_000),
		SlippageBps: 800,
	}, now.Add(time.Second)); !errors.Is(err, ErrApproveNotConfirmed) {
		t.Fatalf("expected ErrApproveNotConfirmed when approve reverts, got %v", err)
	}
	unlocked, err := store.CanUnlockStep(ctx, "op-sell-locked", 1)
	if err != nil || unlocked {
		t.Fatalf("sell step 1 must stay locked when approve fails: unlocked=%v err=%v", unlocked, err)
	}
	fork.failApprove = false

	// 3. 100% SELL of 5% FoT token (950,000) -> Approve (nonce 2) -> Sell SupportingFoT (nonce 3).
	sellRes, err := exec.Execute(ctx, MultiChainOrderRequest{
		OperationID: "op-sell-1",
		ProtocolID:  pancakev2.ProtocolID,
		Token:       token,
		Side:        ports.SideSell,
		AmountInRaw: big.NewInt(950_000),
		SlippageBps: 800,
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("Approve+Sell failed: %v", err)
	}
	if sellRes.ApproveTxHash == (common.Hash{}) || sellRes.SwapTxHash == (common.Hash{}) {
		t.Fatalf("expected both approve and swap tx hashes, got %+v", sellRes)
	}
	if !sellRes.Effects.EvidenceComplete || sellRes.Effects.NetTokenDeltaRaw.Cmp(big.NewInt(-950_000)) != 0 || sellRes.Effects.NetNativeDeltaWei.Sign() <= 0 {
		t.Fatalf("unexpected sell effects: %+v", sellRes.Effects)
	}
	if fork.tokenBalance.Sign() != 0 {
		t.Fatalf("expected 100%% token balance sold (0 remaining), got %s", fork.tokenBalance)
	}

	// 4. Relay timeout -> BROADCAST_UNKNOWN at nonce 4 -> Same-nonce 0-value Cancel unfreezes nonce 4 (AT-07).
	forceTimeout = true
	_, err = exec.Execute(ctx, MultiChainOrderRequest{
		OperationID: "op-stuck-4",
		ProtocolID:  pancakev2.ProtocolID,
		Token:       token,
		Side:        ports.SideBuy,
		AmountInRaw: big.NewInt(1e17),
		SlippageBps: 800,
	}, now.Add(3*time.Second))
	if !errors.Is(err, ErrRelayBroadcastUnknown) {
		t.Fatalf("expected ErrRelayBroadcastUnknown on relay timeout, got %v", err)
	}
	stuckNonce := fork.nonce // 4
	forceTimeout = false

	cancelHash, err := exec.CancelStuckNonce(
		ctx, "op-stuck-4", "op-stuck-4:step:0", stuckNonce,
		big.NewInt(6_000_000_000), big.NewInt(2_000_000_000),
		now.Add(4*time.Second),
	)
	if err != nil {
		t.Fatalf("CancelStuckNonce failed: %v", err)
	}
	if cancelHash == (common.Hash{}) || fork.nonce != stuckNonce+1 {
		t.Fatalf("expected cancel to confirm and advance nonce to %d, got hash=%s nonce=%d", stuckNonce+1, cancelHash.Hex(), fork.nonce)
	}

	// Verify DB state: initial attempt is SUPERSEDED, cancel attempt is FINALIZED.
	var supersededCnt, finalizedCancelCnt int
	_ = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trade_step_attempts WHERE operation_id = 'op-stuck-4' AND attempt_kind = 'initial' AND state = 'SUPERSEDED'`).Scan(&supersededCnt)
	_ = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trade_step_attempts WHERE operation_id = 'op-stuck-4' AND attempt_kind = 'cancel' AND state = 'FINALIZED'`).Scan(&finalizedCancelCnt)
	if supersededCnt != 1 || finalizedCancelCnt != 1 {
		t.Fatalf("expected 1 SUPERSEDED initial + 1 FINALIZED cancel, got superseded=%d finalizedCancel=%d", supersededCnt, finalizedCancelCnt)
	}
}

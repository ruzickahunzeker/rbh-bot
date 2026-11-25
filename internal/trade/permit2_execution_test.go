package trade

import (
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/permit2"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
	univ4 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/uniswap/v4"
	"github.com/ruzickahunzeker/rbh-bot/internal/registry"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

var testPermitABI = mustParseExecABI(`[
{"type":"function","name":"allowance","inputs":[{"type":"address"},{"type":"address"},{"type":"address"}],"outputs":[{"type":"uint160"},{"type":"uint48"},{"type":"uint48"}]},
{"type":"function","name":"approve","inputs":[{"type":"address"},{"type":"address"},{"type":"uint160"},{"type":"uint48"}],"outputs":[]}
]`)

type permitExecutionBackend struct {
	*statefulBSCForkBackend
	t            *testing.T
	permitAmount *big.Int
	expiry       uint64
	deadline     uint64
	failAt       int
	noOpApprove  bool
	sent         []*gethtypes.Transaction
}

func (f *permitExecutionBackend) CallContractAt(_ context.Context, to common.Address, cd []byte, _ common.Hash) ([]byte, error) {
	switch to {
	case f.token:
		args, err := erc20ExecABI.Methods["allowance"].Inputs.Unpack(cd[4:])
		if err != nil || args[1].(common.Address) != permit2.Address {
			return nil, errors.New("must read ERC20 allowance to Permit2")
		}
		return common.LeftPadBytes(f.allowance.Bytes(), 32), nil
	case permit2.Address:
		return testPermitABI.Methods["allowance"].Outputs.Pack(f.permitAmount, new(big.Int).SetUint64(f.expiry), big.NewInt(0))
	case bsc.UniswapV4Quoter:
		return append(common.LeftPadBytes(big.NewInt(90).Bytes(), 32), common.LeftPadBytes(big.NewInt(220000).Bytes(), 32)...), nil
	}
	return nil, errors.New("unexpected read")
}

func (f *permitExecutionBackend) BroadcastRawTx(_ context.Context, hash common.Hash, raw []byte) (RelayBroadcastResult, error) {
	var tx gethtypes.Transaction
	if err := tx.UnmarshalBinary(raw); err != nil {
		return RelayBroadcastResult{}, err
	}
	from, err := gethtypes.Sender(gethtypes.LatestSignerForChainID(big.NewInt(56)), &tx)
	if err != nil || from != f.wallet || tx.Nonce() != f.nonce {
		return RelayBroadcastResult{}, errors.New("invalid signed transaction")
	}
	f.sent = append(f.sent, &tx)
	f.nonce++
	f.blockNum++
	f.blockHash = crypto.Keccak256Hash(f.blockHash.Bytes())
	rcpt := &gethtypes.Receipt{Status: 1, TxHash: hash, BlockHash: f.blockHash}
	if len(f.sent) == f.failAt {
		rcpt.Status = 0
	} else if *tx.To() == f.token {
		args, err := erc20ExecABI.Methods["approve"].Inputs.Unpack(tx.Data()[4:])
		if err != nil || args[0].(common.Address) != permit2.Address {
			f.t.Fatal("incorrect ERC20 approval")
		}
		if !f.noOpApprove {
			f.allowance = new(big.Int).Set(args[1].(*big.Int))
		}
	} else if *tx.To() == permit2.Address {
		args, err := testPermitABI.Methods["approve"].Inputs.Unpack(tx.Data()[4:])
		if err != nil || args[0].(common.Address) != f.token || args[1].(common.Address) != bsc.UniswapV4UniversalRouter {
			f.t.Fatal("incorrect Permit2 approval")
		}
		if !f.noOpApprove {
			f.permitAmount, f.expiry = args[2].(*big.Int), args[3].(*big.Int).Uint64()
		}
	} else if *tx.To() == bsc.UniswapV4UniversalRouter {
		if f.allowance.Int64() < 100 || f.permitAmount.Int64() < 100 || f.expiry <= f.deadline {
			f.t.Fatal("swap sent before both allowance layers are usable")
		}
		keyData := make([]byte, 160)
		copy(keyData[32:64], common.LeftPadBytes(f.token.Bytes(), 32))
		copy(keyData[64:96], common.LeftPadBytes(big.NewInt(3000).Bytes(), 32))
		copy(keyData[96:128], common.LeftPadBytes(big.NewInt(60).Bytes(), 32))
		swapData := make([]byte, 192)
		big.NewInt(90).FillBytes(swapData[:32])
		rcpt.Logs = []*gethtypes.Log{
			{Address: bsc.UniswapV4PoolManager, Topics: []common.Hash{univ4.V4PoolSwapTopic, crypto.Keccak256Hash(keyData), common.BytesToHash(bsc.UniswapV4UniversalRouter.Bytes())}, Data: swapData},
			{Address: f.token, Topics: []common.Hash{forkTransferTopic, common.BytesToHash(f.wallet.Bytes()), common.BytesToHash(bsc.UniswapV4PoolManager.Bytes())}, Data: common.LeftPadBytes(big.NewInt(100).Bytes(), 32)},
		}
	} else {
		f.t.Fatal("untrusted transaction target")
	}
	f.receipts[hash] = rcpt
	return RelayBroadcastResult{MergedState: MergedSubmitted}, nil
}

func TestV4Permit2ExecutionAndApprovalFailureGates(t *testing.T) {
	for _, tc := range []struct {
		name          string
		erc20, router int64
		expiryOffset  int64
		failAt        int
		noOp          bool
		targets       []int
		wantErr       bool
	}{
		{"both approvals", 0, 0, 0, 0, false, []int{0, 1, 2}, false},
		{"only ERC20", 0, 100, 1000, 0, false, []int{0, 2}, false},
		{"only router", 100, 0, 1000, 0, false, []int{1, 2}, false},
		{"expired router", 100, 100, -1, 0, false, []int{1, 2}, false},
		{"expires before swap", 100, 100, 30, 0, false, []int{1, 2}, false},
		{"already ready", 100, 100, 1000, 0, false, []int{2}, false},
		{"ERC20 reverted", 0, 0, 0, 1, false, []int{0}, true},
		{"Permit2 reverted", 0, 0, 0, 2, false, []int{0, 1}, true},
		{"approval did not change state", 0, 0, 0, 0, true, []int{0, 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, now := context.Background(), time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
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
			reg, err := registry.New(bsc.Profile(), []ports.ProtocolAdapter{univ4.NewBSCAdapter()}, map[string]registry.TrustedSpender{univ4.ProtocolIDBSC: {Address: bsc.UniswapV4UniversalRouter}})
			if err != nil {
				t.Fatal(err)
			}
			key, _ := crypto.GenerateKey()
			token := common.HexToAddress("0x1234")
			backend := &permitExecutionBackend{statefulBSCForkBackend: newStatefulBSCFork(crypto.PubkeyToAddress(key.PublicKey), token), t: t, permitAmount: big.NewInt(tc.router), expiry: uint64(now.Unix() + tc.expiryOffset), deadline: uint64(now.Unix() + 120), failAt: tc.failAt, noOpApprove: tc.noOp}
			backend.allowance = big.NewInt(tc.erc20)
			executor, err := NewMultiChainExecutor(true, reg, store, backend, backend, nil, key)
			if err != nil {
				t.Fatal(err)
			}
			res, err := executor.Execute(ctx, MultiChainOrderRequest{OperationID: "test-v4", ProtocolID: univ4.ProtocolIDBSC, Token: token, Side: ports.SideSell, AmountInRaw: big.NewInt(100), SlippageBps: 100}, now)
			if tc.wantErr {
				if !errors.Is(err, ErrApproveNotConfirmed) {
					t.Fatalf("expected blocked approval, got %v", err)
				}
			} else if err != nil || !res.Effects.EvidenceComplete {
				t.Fatalf("effects=%+v err=%v", res.Effects, err)
			}
			if len(backend.sent) != len(tc.targets) {
				t.Fatalf("sent %d transactions, expected %d", len(backend.sent), len(tc.targets))
			}
			targets := []common.Address{token, permit2.Address, bsc.UniswapV4UniversalRouter}
			for i, idx := range tc.targets {
				if *backend.sent[i].To() != targets[idx] {
					t.Fatal("transaction order mismatch")
				}
			}
			if tc.wantErr {
				var status string
				if err := store.db.QueryRowContext(ctx, `SELECT status FROM trade_operation_steps WHERE operation_id = 'test-v4' AND step_kind = 'sell'`).Scan(&status); err != nil || status != "PENDING" {
					t.Fatalf("sell gate=%s err=%v", status, err)
				}
			}
		})
	}
}

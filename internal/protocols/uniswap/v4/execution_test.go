package v4

import (
	"bytes"
	"math/big"
	"testing"

	sdk "github.com/0xfnzero/rbh-trade-sdk/rbhtrade"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

func testPoolID(t *testing.T, route ports.ResolvedRoute) common.Hash {
	t.Helper()
	key, _, ok := decodeKeyMeta(route.DeploymentRevision, route.Token)
	if !ok {
		t.Fatal("bad pool metadata")
	}
	data, err := poolKeyArgs.Pack(key.Currency0, key.Currency1, key.Fee, key.TickSpacing, key.Hooks)
	if err != nil {
		t.Fatal(err)
	}
	return crypto.Keccak256Hash(data)
}

func testSwapData(amount0, amount1 *big.Int) []byte {
	data := make([]byte, 192)
	for i, amount := range []*big.Int{amount0, amount1} {
		n := new(big.Int).Set(amount)
		if n.Sign() < 0 {
			n.Add(n, new(big.Int).Lsh(big.NewInt(1), 256))
		}
		n.FillBytes(data[i*32 : (i+1)*32])
	}
	return data
}

func TestV4DeploymentDialects(t *testing.T) {
	key := makeOrderedPoolKey(common.HexToAddress("0x100"), common.HexToAddress("0x200"), 3000, 60, common.Address{})
	hookData := []byte{0xa1, 0xb2, 0xc3}
	for _, a := range []*Adapter{NewETHAdapter(), NewBSCAdapter(), NewBaseAdapter(), NewRBHAdapter()} {
		t.Run(a.protocolID, func(t *testing.T) {
			encoded, err := a.encodeSwap(key, true, big.NewInt(100), big.NewInt(90), hookData)
			if err != nil {
				t.Fatal(err)
			}
			word := func(i int) *big.Int { return new(big.Int).SetBytes(encoded[32*i : 32*(i+1)]) }
			offsetWord, tupleWords := 9, 9
			if a.chainID == 4663 {
				offsetWord, tupleWords = 10, 10
			}
			if word(0).Uint64() != 32 || word(offsetWord).Uint64() != uint64(tupleWords*32) ||
				word(offsetWord+1).Uint64() != uint64(len(hookData)) || !bytes.Equal(encoded[(offsetWord+2)*32:(offsetWord+2)*32+3], hookData) {
				t.Fatal("swap tuple offsets or hook data differ from deployment layout")
			}
			if a.chainID == 4663 {
				if word(9).Sign() != 0 {
					t.Fatal("RBH price floor must be explicitly zero")
				}
				// Differential check against the independent, pinned RBH SDK encoder.
				call, err := sdk.BuildV4ExactInputSingle(sdk.ExactInputRequest{
					PoolKey:    sdk.PoolKey{Currency0: key.Currency0, Currency1: key.Currency1, Fee: 3000, TickSpacing: 60},
					CurrencyIn: key.Currency0, CurrencyOut: key.Currency1, AmountIn: big.NewInt(100), AmountOutMinimum: big.NewInt(90), HookData: hookData, Deadline: 1900000000,
				})
				if err != nil {
					t.Fatal(err)
				}
				outer, err := universalRouterABI.Methods["execute"].Inputs.Unpack(call.Data[4:])
				if err != nil {
					t.Fatal(err)
				}
				plan, err := v4PlanArgs.Unpack(outer[1].([][]byte)[0])
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(encoded, plan[1].([][]byte)[0]) {
					t.Fatal("RBH encoding differs from SDK")
				}
			}
		})
	}
}

func TestV4NativeAndWrappedBuySellPlans(t *testing.T) {
	for _, a := range []*Adapter{NewETHAdapter(), NewBSCAdapter(), NewBaseAdapter(), NewRBHAdapter()} {
		for _, wrapped := range []bool{false, true} {
			for _, side := range []ports.Side{ports.SideBuy, ports.SideSell} {
				quoteCur := common.Address{}
				if wrapped {
					quoteCur = a.wrappedNative
				}
				req := ports.QuoteRequest{
					Route: ports.ResolvedRoute{Token: common.HexToAddress("0x1234"), DeploymentRevision: encodeKeyMeta(quoteCur, 3000, 60, common.Address{})},
					Side:  side, Recipient: common.HexToAddress("0xCAFE"), AmountInRaw: big.NewInt(100), ContractExpiry: 1900000000,
				}
				tx, err := a.BuildUnsigned(req, ports.QuoteResult{MinOutRaw: big.NewInt(90)})
				if err != nil {
					t.Fatal(err)
				}
				if (side == ports.SideBuy && tx.Value.Cmp(req.AmountInRaw) != 0) || (side == ports.SideSell && tx.Value.Sign() != 0) {
					t.Fatal("incorrect native value")
				}
				outer, err := universalRouterABI.Methods["execute"].Inputs.Unpack(tx.Calldata[4:])
				if err != nil {
					t.Fatal(err)
				}
				commands, inputs := outer[0].([]byte), outer[1].([][]byte)
				want, planIndex := []byte{0x10}, 0
				if wrapped && side == ports.SideBuy {
					want, planIndex = []byte{0x0b, 0x10, 0x0c}, 1
				} else if wrapped {
					want = []byte{0x10, 0x0c}
				} else if side == ports.SideBuy {
					want = []byte{0x10, 0x04}
				}
				if !bytes.Equal(commands, want) {
					t.Fatalf("%s %s wrapped=%v commands %x", a.protocolID, side, wrapped, commands)
				}
				plan, err := v4PlanArgs.Unpack(inputs[planIndex])
				if err != nil {
					t.Fatal(err)
				}
				actions, params := plan[0].([]byte), plan[1].([][]byte)
				settle := byte(0x0c)
				if wrapped && side == ports.SideBuy {
					settle = 0x0b
					wrap, err := addrUintArgs.Unpack(inputs[0])
					if err != nil || wrap[0].(common.Address) != common.BigToAddress(big.NewInt(2)) || wrap[1].(*big.Int).Cmp(req.AmountInRaw) != 0 {
						t.Fatal("wrap must fund router")
					}
					v, err := addrUintBoolArgs.Unpack(params[1])
					if err != nil || v[0].(common.Address) != a.wrappedNative || v[1].(*big.Int).Sign() != 0 || v[2].(bool) {
						t.Fatal("wrapped input must settle router funds and actual debt")
					}
				}
				if !bytes.Equal(actions, []byte{0x06, settle, 0x0e}) {
					t.Fatalf("wrong V4 actions: %x", actions)
				}
				take, err := addrAddrUintArgs.Unpack(params[2])
				if err != nil {
					t.Fatal(err)
				}
				recipient := req.Recipient
				if wrapped && side == ports.SideSell {
					recipient = common.BigToAddress(big.NewInt(2))
				}
				if take[1].(common.Address) != recipient || take[2].(*big.Int).Sign() != 0 {
					t.Fatal("incorrect take recipient/amount")
				}
				if wrapped {
					unwrap, err := addrUintArgs.Unpack(inputs[len(inputs)-1])
					if err != nil || unwrap[0].(common.Address) != req.Recipient {
						t.Fatal("incorrect native recipient")
					}
					min := int64(0)
					if side == ports.SideSell {
						min = 90
					}
					if unwrap[1].(*big.Int).Int64() != min {
						t.Fatal("incorrect unwrap minimum")
					}
				}
			}
		}
	}
}

func TestV4DecodeNativeAndWrappedEffects(t *testing.T) {
	a := NewBaseAdapter()
	token, wallet := common.HexToAddress("0x1234"), common.HexToAddress("0xCAFE")
	for _, wrapped := range []bool{false, true} {
		for _, side := range []ports.Side{ports.SideBuy, ports.SideSell} {
			quoteCur := common.Address{}
			if wrapped {
				quoteCur = a.wrappedNative
			}
			route := ports.ResolvedRoute{Token: token, DeploymentRevision: encodeKeyMeta(quoteCur, 3000, 60, common.Address{})}
			from, to := a.poolManager, wallet
			nativeDelta := int64(-100)
			if side == ports.SideSell {
				from, to, nativeDelta = wallet, a.poolManager, 90
			}
			swap := &types.Log{Address: a.poolManager, Topics: []common.Hash{V4PoolSwapTopic, testPoolID(t, route), common.BytesToHash(a.router.Bytes())}, Data: testSwapData(big.NewInt(nativeDelta), big.NewInt(10))}
			logs := []*types.Log{swap, {Address: token, Topics: []common.Hash{erc20TransferTopic, common.BytesToHash(from.Bytes()), common.BytesToHash(to.Bytes())}, Data: common.LeftPadBytes(big.NewInt(10).Bytes(), 32)}}
			if wrapped {
				topic, amount := wethDepositTopic, int64(100)
				if side == ports.SideSell {
					topic, amount = wethWithdrawalTopic, 90
				}
				logs = append(logs, &types.Log{Address: a.wrappedNative, Topics: []common.Hash{topic, common.BytesToHash(a.router.Bytes())}, Data: common.LeftPadBytes(big.NewInt(amount).Bytes(), 32)})
				// An unrelated withdrawal in the same receipt must not inflate proceeds.
				logs = append(logs, &types.Log{Address: a.wrappedNative, Topics: []common.Hash{wethWithdrawalTopic, common.HexToHash("0x777")}, Data: common.LeftPadBytes(big.NewInt(999).Bytes(), 32)})
			}
			receipt := &types.Receipt{Status: 1, Logs: logs}
			eff, err := a.DecodeEffects(route, wallet, side, receipt)
			if err != nil || !eff.EvidenceComplete || eff.NetNativeDeltaWei.Int64() != nativeDelta {
				t.Fatalf("wrapped=%v side=%s effects=%+v err=%v", wrapped, side, eff, err)
			}
			swap.Topics[1] = common.HexToHash("0xdead")
			eff, err = a.DecodeEffects(route, wallet, side, receipt)
			if err != nil || eff.EvidenceComplete {
				t.Fatal("unrelated pool cannot supply evidence")
			}
		}
	}
}

func TestV4RejectsInvalidMetadataAndRecipient(t *testing.T) {
	a := NewBSCAdapter()
	req := ports.QuoteRequest{Route: ports.ResolvedRoute{Token: common.HexToAddress("0x123")}, Side: ports.SideBuy, AmountInRaw: big.NewInt(1), Recipient: common.HexToAddress("0x234"), ContractExpiry: 1900000000}
	if _, err := a.BuildUnsigned(req, ports.QuoteResult{MinOutRaw: big.NewInt(1)}); err == nil {
		t.Fatal("must reject absent metadata")
	}
	req.Route.DeploymentRevision = encodeKeyMeta(common.Address{}, 3000, 60, common.Address{})
	req.Recipient = common.Address{}
	if _, err := a.BuildUnsigned(req, ports.QuoteResult{MinOutRaw: big.NewInt(1)}); err == nil {
		t.Fatal("must reject zero recipient")
	}
}

func TestV4WrappedMintBurnAndDuplicateEvents(t *testing.T) {
	a := NewRBHAdapter()
	token, wallet := common.HexToAddress("0x1234"), common.HexToAddress("0xCAFE")
	route := ports.ResolvedRoute{Token: token, DeploymentRevision: encodeKeyMeta(a.wrappedNative, 3000, 60, common.Address{})}
	wrapTransfer := func(from, to common.Address, amount int64) *types.Log {
		return &types.Log{Address: a.wrappedNative, Topics: []common.Hash{erc20TransferTopic, common.BytesToHash(from.Bytes()), common.BytesToHash(to.Bytes())}, Data: common.LeftPadBytes(big.NewInt(amount).Bytes(), 32)}
	}
	for _, side := range []ports.Side{ports.SideBuy, ports.SideSell} {
		for _, duplicateEvents := range []bool{false, true} {
			from, to, nativeWant := a.poolManager, wallet, int64(-80)
			if side == ports.SideSell {
				from, to, nativeWant = wallet, a.poolManager, 90
			}
			logs := []*types.Log{
				{Address: a.poolManager, Topics: []common.Hash{V4PoolSwapTopic, testPoolID(t, route), common.BytesToHash(a.router.Bytes())}, Data: testSwapData(big.NewInt(nativeWant), big.NewInt(10))},
				{Address: token, Topics: []common.Hash{erc20TransferTopic, common.BytesToHash(from.Bytes()), common.BytesToHash(to.Bytes())}, Data: common.LeftPadBytes(big.NewInt(10).Bytes(), 32)},
				// Ordinary transfers of WETH to or from the pool are not native wrapping.
				wrapTransfer(a.poolManager, a.router, 1000),
				wrapTransfer(common.Address{}, common.HexToAddress("0xdead"), 999),
			}
			if side == ports.SideBuy {
				logs = append(logs, wrapTransfer(common.Address{}, a.router, 100), wrapTransfer(a.router, common.Address{}, 20))
				if duplicateEvents {
					logs = append(logs,
						&types.Log{Address: a.wrappedNative, Topics: []common.Hash{wethDepositTopic, common.BytesToHash(a.router.Bytes())}, Data: common.LeftPadBytes(big.NewInt(100).Bytes(), 32)},
						&types.Log{Address: a.wrappedNative, Topics: []common.Hash{wethWithdrawalTopic, common.BytesToHash(a.router.Bytes())}, Data: common.LeftPadBytes(big.NewInt(20).Bytes(), 32)})
				}
			} else {
				logs = append(logs, wrapTransfer(a.router, common.Address{}, 90))
				if duplicateEvents {
					logs = append(logs, &types.Log{Address: a.wrappedNative, Topics: []common.Hash{wethWithdrawalTopic, common.BytesToHash(a.router.Bytes())}, Data: common.LeftPadBytes(big.NewInt(90).Bytes(), 32)})
				}
			}
			eff, err := a.DecodeEffects(route, wallet, side, &types.Receipt{Status: 1, Logs: logs})
			if err != nil || !eff.EvidenceComplete || eff.NetNativeDeltaWei.Int64() != nativeWant {
				t.Fatalf("side=%s duplicates=%v effects=%+v err=%v", side, duplicateEvents, eff, err)
			}
		}
	}
}

package trade

import (
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/bot"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

var (
	testWallet = common.HexToAddress("0x1000000000000000000000000000000000000001")
	testToken  = common.HexToAddress("0x2000000000000000000000000000000000000002")
	testCurve  = common.HexToAddress("0x3000000000000000000000000000000000000003")
)

type fakeBackend struct {
	block         BlockRef
	route         CurveRoute
	quoteOut      *big.Int
	minimumOut    *big.Int
	balance       *big.Int
	resolveErr    error
	simulateErr   error
	verifyErr     error
	simulations   int
	pendingNonce  uint64
	nativeBalance *big.Int
	fee           FeeParameters
	chainID       *big.Int
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		block:    BlockRef{Number: 100, Hash: common.HexToHash("0xabc"), Time: time.Unix(100, 0)},
		route:    CurveRoute{Protocol: "pons-v2-curve", Token: testToken, Curve: testCurve, NativeQuote: true},
		quoteOut: big.NewInt(10_000), minimumOut: big.NewInt(9_500), balance: big.NewInt(40_000),
		pendingNonce: 7, nativeBalance: new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil),
		fee:     FeeParameters{GasLimit: 100_000, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(10)},
		chainID: big.NewInt(int64(ChainID)),
	}
}

func (f *fakeBackend) Snapshot(context.Context) (BlockRef, error) { return f.block, nil }
func (f *fakeBackend) ExecutionChainID(context.Context) (*big.Int, error) {
	return cloneInt(f.chainID), nil
}
func (f *fakeBackend) ResolveCurve(context.Context, common.Address, BlockRef) (CurveRoute, error) {
	return f.route, f.resolveErr
}
func (f *fakeBackend) QuoteBuy(context.Context, CurveRoute, *big.Int, common.Address, BlockRef, uint64) (*big.Int, *big.Int, error) {
	return cloneInt(f.quoteOut), cloneInt(f.minimumOut), nil
}
func (f *fakeBackend) QuoteSell(context.Context, CurveRoute, *big.Int, BlockRef, uint64) (*big.Int, *big.Int, error) {
	return cloneInt(f.quoteOut), cloneInt(f.minimumOut), nil
}
func (f *fakeBackend) TokenBalance(context.Context, common.Address, common.Address, BlockRef) (*big.Int, error) {
	return cloneInt(f.balance), nil
}
func (f *fakeBackend) Simulate(context.Context, common.Address, common.Address, *big.Int, []byte, BlockRef) ([]byte, error) {
	f.simulations++
	return []byte{1, 2, 3}, f.simulateErr
}
func (f *fakeBackend) VerifySnapshot(context.Context, BlockRef) error { return f.verifyErr }
func (f *fakeBackend) PendingNonce(context.Context, common.Address) (uint64, error) {
	return f.pendingNonce, nil
}
func (f *fakeBackend) NativeBalance(context.Context, common.Address, BlockRef) (*big.Int, error) {
	return cloneInt(f.nativeBalance), nil
}
func (f *fakeBackend) FeeParameters(context.Context, common.Address, UnsignedCall, BlockRef) (FeeParameters, error) {
	return FeeParameters{GasLimit: f.fee.GasLimit, GasTipCap: cloneInt(f.fee.GasTipCap), GasFeeCap: cloneInt(f.fee.GasFeeCap)}, nil
}

func TestCurveBuyDryRunUsesPinnedSDKAndIsIdempotent(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	engine, _ := NewEngine(store, backend)
	request := buyRequest()
	result, err := engine.DryRun(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.Parameters.Direction != "buy" || result.UnsignedCall.To != testCurve.Hex() || result.UnsignedCall.Value != "1000" {
		t.Fatalf("unexpected buy result: %#v", result)
	}
	if len(result.UnsignedCall.Data) < 10 || result.UnsignedCall.Data[:10] != "0x59a87bc1" {
		t.Fatalf("buy was not built by Pons Curve SDK: %s", result.UnsignedCall.Data)
	}
	replay, err := engine.DryRun(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Duplicate || replay.OperationID != result.OperationID || backend.simulations != 1 {
		t.Fatalf("replay caused second simulation: %#v simulations=%d", replay, backend.simulations)
	}
	assertNoExecutionArtifacts(t, store)
}

func TestCurveSellDryRunResolvesBalancePercentage(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	engine, _ := NewEngine(store, backend)
	result, err := engine.DryRun(context.Background(), sellRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.Parameters.Direction != "sell" || result.Parameters.AmountIn != "10000" || result.UnsignedCall.Value != "0" {
		t.Fatalf("unexpected sell result: %#v", result)
	}
	if len(result.UnsignedCall.Data) < 10 || result.UnsignedCall.Data[:10] != "0xd04c6983" {
		t.Fatalf("sell was not built by Pons Curve SDK: %s", result.UnsignedCall.Data)
	}
	assertNoExecutionArtifacts(t, store)
}

func TestSimulationRevertAndUnknownRouteFailClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		set  func(*fakeBackend)
		code string
	}{
		{"revert", func(b *fakeBackend) { b.simulateErr = ErrSimulationReverted }, "SIMULATION_REVERTED"},
		{"unknown-route", func(b *fakeBackend) { b.resolveErr = ErrUnknownRoute }, "UNKNOWN_ROUTE"},
		{"stale", func(b *fakeBackend) { b.verifyErr = ErrStaleState }, "STALE_STATE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := openTradeStore(t)
			defer closeDB()
			backend := newFakeBackend()
			test.set(backend)
			engine, _ := NewEngine(store, backend)
			result, err := engine.DryRun(context.Background(), buyRequest())
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != "fail_closed" || result.FailureCode != test.code {
				t.Fatalf("unexpected failure: %#v", result)
			}
			stored, found, err := store.Result(context.Background(), result.OperationID)
			if err != nil || !found || stored.FailureCode != test.code {
				t.Fatalf("failure not durable: %#v found=%v err=%v", stored, found, err)
			}
			assertNoExecutionArtifacts(t, store)
		})
	}
}

func TestRestartRecoversAdmittedDryRunWithoutSecondOperation(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	request := buyRequest()
	if _, err := store.Admit(context.Background(), request, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	backend := newFakeBackend()
	restarted, _ := NewEngine(store, backend)
	results, err := restarted.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "success" || backend.simulations != 1 {
		t.Fatalf("unexpected recovery: %#v", results)
	}
	var operations, steps int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM execution_steps`).Scan(&steps); err != nil {
		t.Fatal(err)
	}
	if operations != 1 || steps != 1 {
		t.Fatalf("operations/steps=%d/%d", operations, steps)
	}
}

func TestIdempotencyConflictFailsBeforeSimulation(t *testing.T) {
	store, closeDB := openTradeStore(t)
	defer closeDB()
	backend := newFakeBackend()
	engine, _ := NewEngine(store, backend)
	request := buyRequest()
	if _, err := engine.DryRun(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Intent.AmountValue = "1001"
	if _, err := engine.DryRun(context.Background(), request); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("err=%v", err)
	}
	if backend.simulations != 1 {
		t.Fatalf("conflict reached simulation: %d", backend.simulations)
	}
}

func openTradeStore(t *testing.T) (*Store, func()) {
	t.Helper()
	database, err := storage.Open(context.Background(), storage.TradeOwner, filepath.Join(t.TempDir(), "trade.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(context.Background()); err != nil {
		database.Close()
		t.Fatal(err)
	}
	store, err := NewStore(database)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := store.RegisterDryRunWallet(context.Background(), "wallet-1", testWallet); err != nil {
		database.Close()
		t.Fatal(err)
	}
	return store, func() { _ = database.Close() }
}

func buyRequest() DryRunRequest {
	return request("intent-buy", "copy_buy", "fixed_input", "1000")
}

func sellRequest() DryRunRequest {
	return request("intent-sell", "copy_sell", "balance_bps", "2500")
}

func request(id, kind, mode, amount string) DryRunRequest {
	return DryRunRequest{WalletID: "wallet-1", Intent: bot.OperationIntent{
		ID: id, IdempotencyKey: id, StrategyID: "strategy-1", WatchedWalletID: "watched-1",
		SourceEventID: "event-1", SourceObservationID: "observation-1", SourceTxHash: common.HexToHash("0x1234").Hex(),
		Kind: kind, Token: testToken.Hex(), AmountMode: mode, AmountValue: amount, PolicyVersion: 1, Status: "created",
	}}
}

func assertNoExecutionArtifacts(t *testing.T, store *Store) {
	t.Helper()
	var attempts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatalf("nonce/sign/broadcast artifact created: %d", attempts)
	}
}

func cloneInt(value *big.Int) *big.Int { return new(big.Int).Set(value) }

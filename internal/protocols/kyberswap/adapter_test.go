package kyberswap

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

type stubCodeRPC struct{}

func (stubCodeRPC) CodeAt(_ context.Context, _ common.Address, _ common.Hash) ([]byte, error) {
	return []byte{0x60, 0x00}, nil
}

func (stubCodeRPC) CallContractAt(_ context.Context, _ common.Address, _ []byte, _ common.Hash) ([]byte, error) {
	return nil, nil
}

func TestKyberSwapQuoteAndUntrustedRouterReject(t *testing.T) {
	token := common.HexToAddress("0x55d398326f99059fF775485246999027B3197955")
	blkHash := common.HexToHash("0x1234")

	// 1. Untrusted routerAddress MUST be rejected fail-closed.
	evilSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"routerAddress": "0x000000000000000000000000000000000000EVIL",
				"routeSummary":  map[string]any{"amountOut": "999999999"},
			},
		})
	}))
	defer evilSrv.Close()

	adpEvil := NewBSCAdapterWithBase(evilSrv.URL)
	route, err := adpEvil.Resolve(context.Background(), stubCodeRPC{}, token, 100, blkHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adpEvil.Quote(context.Background(), stubCodeRPC{}, ports.QuoteRequest{
		Route: route, Side: ports.SideBuy, AmountInRaw: big.NewInt(1e16), SlippageBps: 100,
	}); !errors.Is(err, ErrUntrustedRouter) {
		t.Fatalf("expected ErrUntrustedRouter, got %v", err)
	}

	// 2. Trusted KyberMetaRouter succeeds and applies slippage.
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"routerAddress": bsc.KyberMetaRouter.Hex(),
				"routeSummary":  map[string]any{"amountOut": "5000000"},
			},
		})
	}))
	defer okSrv.Close()

	adpOK := NewBSCAdapterWithBase(okSrv.URL)
	q, err := adpOK.Quote(context.Background(), stubCodeRPC{}, ports.QuoteRequest{
		Route: route, Side: ports.SideBuy, AmountInRaw: big.NewInt(1e16), SlippageBps: 200,
	})
	if err != nil || q.ExpectedOutRaw.Cmp(big.NewInt(5_000_000)) != 0 || q.MinOutRaw.Cmp(big.NewInt(4_900_000)) != 0 {
		t.Fatalf("expected valid kyber quote, got %+v err=%v", q, err)
	}
}

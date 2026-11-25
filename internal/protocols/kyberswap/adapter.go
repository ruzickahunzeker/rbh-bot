// Package kyberswap implements a pure, whitelisted-router ProtocolAdapter for BSC (56)
// long-tail token quoting via KyberSwap Aggregator API, strictly enforcing that returned
// routerAddress matches the hardcoded audited KyberMetaRouter (0x6131...37b5).
package kyberswap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

const (
	ProtocolID      = "kyber-agg-bsc"
	AdapterRevision = "kyberswap-bsc-r1"
	nativeSentinel  = "0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE"
)

var (
	ErrUntrustedRouter = errors.New("kyberswap API returned untrusted routerAddress; rejected fail-closed")
	erc20TransferTopic = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
)

type Adapter struct {
	chainID uint64
	router  common.Address
	apiBase string
	http    *http.Client
}

func NewBSCAdapter() *Adapter {
	return NewBSCAdapterWithBase("https://aggregator-api.kyberswap.com/bsc/api/v1")
}

func NewBSCAdapterWithBase(apiBase string) *Adapter {
	return &Adapter{
		chainID: bsc.ChainID,
		router:  bsc.KyberMetaRouter,
		apiBase: strings.TrimRight(apiBase, "/"),
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

func (a *Adapter) Describe() ports.AdapterDescriptor {
	return ports.AdapterDescriptor{
		ChainID:              a.chainID,
		ProtocolID:           ProtocolID,
		AdapterRevision:      AdapterRevision,
		PoolPhase:            "AGGREGATOR",
		QuoteAsset:           "BNB",
		SupportsBuy:          true,
		SupportsSell:         true,
		AllowanceModel:       ports.AllowanceERC20Direct,
		FeeOnTransferSupport: false,
		DeadlineCapability:   ports.DeadlineApplicationTTLOnly,
	}
}

func (a *Adapter) Resolve(ctx context.Context, rpc ports.ReadOnlyRPC, token common.Address, blockNumber uint64, blockHash common.Hash) (ports.ResolvedRoute, error) {
	if rpc == nil || token == (common.Address{}) || blockHash == (common.Hash{}) {
		return ports.ResolvedRoute{}, errors.New("invalid kyberswap resolve inputs")
	}
	routerCode, err := rpc.CodeAt(ctx, a.router, blockHash)
	if err != nil || len(routerCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("kyberswap MetaRouter has no runtime code")
	}
	tokCode, err := rpc.CodeAt(ctx, token, blockHash)
	if err != nil || len(tokCode) == 0 {
		return ports.ResolvedRoute{}, errors.New("target token has no runtime code")
	}
	return ports.ResolvedRoute{
		ChainID:            a.chainID,
		ProtocolID:         ProtocolID,
		AdapterRevision:    AdapterRevision,
		DeploymentRevision: "kyberswap-bsc-mainnet",
		PoolPhase:          "AGGREGATOR",
		Token:              token,
		PoolOrCurve:        a.router,
		RouterOrSpender:    a.router,
		RuntimeCodeHash:    crypto.Keccak256Hash(routerCode),
		QuoteBlockNumber:   blockNumber,
		QuoteBlockHash:     blockHash,
	}, nil
}

// Quote fetches the best long-tail BSC route from KyberSwap Aggregator API and enforces
// strict routerAddress == bsc.KyberMetaRouter whitelisting.
func (a *Adapter) Quote(ctx context.Context, _ ports.ReadOnlyRPC, req ports.QuoteRequest) (ports.QuoteResult, error) {
	if req.Route.Token == (common.Address{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 || req.SlippageBps > 10000 {
		return ports.QuoteResult{}, errors.New("invalid kyberswap quote request")
	}
	tokenIn := nativeSentinel
	tokenOut := req.Route.Token.Hex()
	if req.Side == ports.SideSell {
		tokenIn = req.Route.Token.Hex()
		tokenOut = nativeSentinel
	}
	qURL := fmt.Sprintf("%s/routes?tokenIn=%s&tokenOut=%s&amountIn=%s",
		a.apiBase, url.QueryEscape(tokenIn), url.QueryEscape(tokenOut), url.QueryEscape(req.AmountInRaw.String()))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, qURL, nil)
	if err != nil {
		return ports.QuoteResult{}, err
	}
	httpReq.Header.Set("x-client-id", "evm-bot")
	resp, err := a.http.Do(httpReq)
	if err != nil {
		return ports.QuoteResult{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ports.QuoteResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return ports.QuoteResult{}, fmt.Errorf("kyberswap routes status %d", resp.StatusCode)
	}
	var env struct {
		Code int `json:"code"`
		Data struct {
			RouteSummary struct {
				AmountOut string `json:"amountOut"`
				Gas       string `json:"gas"`
			} `json:"routeSummary"`
			RouterAddress string `json:"routerAddress"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return ports.QuoteResult{}, err
	}
	if env.Code != 0 {
		return ports.QuoteResult{}, fmt.Errorf("kyberswap api error code %d", env.Code)
	}
	if !common.IsHexAddress(env.Data.RouterAddress) || common.HexToAddress(env.Data.RouterAddress) != a.router {
		return ports.QuoteResult{}, fmt.Errorf("%w: got %s want %s", ErrUntrustedRouter, env.Data.RouterAddress, a.router.Hex())
	}
	expectedOut, ok := new(big.Int).SetString(env.Data.RouteSummary.AmountOut, 10)
	if !ok || expectedOut.Sign() <= 0 {
		return ports.QuoteResult{}, errors.New("kyberswap returned zero output")
	}
	minOut := new(big.Int).Mul(expectedOut, new(big.Int).SetUint64(10000-req.SlippageBps))
	minOut.Div(minOut, big.NewInt(10000))
	return ports.QuoteResult{
		ExpectedOutRaw:   expectedOut,
		MinOutRaw:        minOut,
		FeeBps:           0,
		EstimatedGas:     350_000,
		QuoteBlockNumber: req.Route.QuoteBlockNumber,
		QuoteBlockHash:   req.Route.QuoteBlockHash,
	}, nil
}

func (a *Adapter) BuildUnsigned(_ ports.QuoteRequest, _ ports.QuoteResult) (ports.UnsignedTx, error) {
	return ports.UnsignedTx{}, errors.New("kyber-agg-bsc requires /route/build calldata verification before live execution")
}

func (a *Adapter) DecodeEffects(route ports.ResolvedRoute, wallet common.Address, side ports.Side, receipt *types.Receipt) (ports.DecodedEffects, error) {
	if receipt == nil || wallet == (common.Address{}) || route.Token == (common.Address{}) {
		return ports.DecodedEffects{}, errors.New("invalid decode inputs")
	}
	netToken := big.NewInt(0)
	for _, lg := range receipt.Logs {
		if lg == nil || lg.Address != route.Token || len(lg.Topics) != 3 || lg.Topics[0] != erc20TransferTopic || len(lg.Data) != 32 {
			continue
		}
		from := common.BytesToAddress(lg.Topics[1].Bytes())
		to := common.BytesToAddress(lg.Topics[2].Bytes())
		amt := new(big.Int).SetBytes(lg.Data)
		if to == wallet {
			netToken.Add(netToken, amt)
		}
		if from == wallet {
			netToken.Sub(netToken, amt)
		}
	}
	return ports.DecodedEffects{
		WalletAddress:      wallet,
		TokenAddress:       route.Token,
		Side:               side,
		NetNativeDeltaWei:  big.NewInt(0),
		NetTokenDeltaRaw:   netToken,
		EvidenceComplete:   receipt.Status == types.ReceiptStatusSuccessful,
		CanonicalBlockHash: receipt.BlockHash,
	}, nil
}

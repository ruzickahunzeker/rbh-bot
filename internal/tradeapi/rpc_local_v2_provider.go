package tradeapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/permit2"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
	"github.com/ruzickahunzeker/rbh-bot/internal/registry"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

const multicall3ABIJSON = `[
  {"name":"aggregate3","type":"function","stateMutability":"payable","inputs":[{"name":"calls","type":"tuple[]","components":[{"name":"target","type":"address"},{"name":"allowFailure","type":"bool"},{"name":"callData","type":"bytes"}]}],"outputs":[{"name":"returnData","type":"tuple[]","components":[{"name":"success","type":"bool"},{"name":"returnData","type":"bytes"}]}]},
  {"name":"getEthBalance","type":"function","stateMutability":"view","inputs":[{"name":"addr","type":"address"}],"outputs":[{"name":"balance","type":"uint256"}]}
]`

const erc20ReadABIJSON = `[
  {"name":"balanceOf","type":"function","stateMutability":"view","inputs":[{"name":"account","type":"address"}],"outputs":[{"name":"","type":"uint256"}]},
  {"name":"decimals","type":"function","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint8"}]},
  {"name":"allowance","type":"function","stateMutability":"view","inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"outputs":[{"name":"","type":"uint256"}]}
]`

var (
	mc3ABI       = mustParseLocalABI(multicall3ABIJSON)
	erc20ReadABI = mustParseLocalABI(erc20ReadABIJSON)
)

func mustParseLocalABI(src string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(src))
	if err != nil {
		panic(err)
	}
	return parsed
}

type multicall3Call3 struct {
	Target       common.Address
	AllowFailure bool
	CallData     []byte
}

type multicall3Result struct {
	Success    bool
	ReturnData []byte
}

// RPCLocalV2Provider implements LocalV2Provider and ports.ReadOnlyRPC against a live
// JSON-RPC endpoint using Keep-Alive HTTP, EIP-1898 blockHash pinning, and single-request
// JSON-RPC Batch Multicall3 position snapshots (zero private key, zero state mutation).
type RPCLocalV2Provider struct {
	endpoint   string
	reg        *registry.Registry
	database   *storage.Database
	walletAddr common.Address
	batch      *evm.BatchRPCClient
	http       *http.Client
}

func NewRPCLocalV2Provider(endpoint string, reg *registry.Registry, database *storage.Database, walletAddr common.Address, timeout time.Duration) *RPCLocalV2Provider {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &RPCLocalV2Provider{
		endpoint:   endpoint,
		reg:        reg,
		database:   database,
		walletAddr: walletAddr,
		batch:      evm.NewBatchRPCClient(endpoint, timeout),
		http: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DisableKeepAlives:     false,
				MaxIdleConns:          32,
				MaxIdleConnsPerHost:   16,
				IdleConnTimeout:       60 * time.Second,
				ResponseHeaderTimeout: timeout,
			},
			Timeout: timeout,
		},
	}
}

// WalletAddress returns the API instance's configured wallet address.
func (h *API) WalletAddress() common.Address {
	if h == nil {
		return common.Address{}
	}
	return common.HexToAddress(h.identity.WalletAddress)
}

func (p *RPCLocalV2Provider) rpcCall(ctx context.Context, method string, params []any, out any) error {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("rpc %s http status %d", method, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if env.Error != nil {
		return fmt.Errorf("rpc %s error %d: %s", method, env.Error.Code, env.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func blockParamForHash(blockHash common.Hash) any {
	if blockHash == (common.Hash{}) {
		return "latest"
	}
	return map[string]any{"blockHash": blockHash.Hex()}
}

func (p *RPCLocalV2Provider) CallContractAt(ctx context.Context, to common.Address, data []byte, blockHash common.Hash) ([]byte, error) {
	var hexRes string
	err := p.rpcCall(ctx, "eth_call", []any{
		map[string]any{
			"to":   to.Hex(),
			"data": "0x" + common.Bytes2Hex(data),
		},
		blockParamForHash(blockHash),
	}, &hexRes)
	if err != nil {
		return nil, err
	}
	return common.FromHex(hexRes), nil
}

func (p *RPCLocalV2Provider) CodeAt(ctx context.Context, addr common.Address, blockHash common.Hash) ([]byte, error) {
	var hexRes string
	if err := p.rpcCall(ctx, "eth_getCode", []any{addr.Hex(), blockParamForHash(blockHash)}, &hexRes); err != nil {
		return nil, err
	}
	return common.FromHex(hexRes), nil
}

func (p *RPCLocalV2Provider) latestBlockHead(ctx context.Context) (uint64, common.Hash, error) {
	var head struct {
		Number string `json:"number"`
		Hash   string `json:"hash"`
	}
	if err := p.rpcCall(ctx, "eth_getBlockByNumber", []any{"latest", false}, &head); err != nil {
		return 0, common.Hash{}, err
	}
	numHex := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(head.Number)), "0x")
	if numHex == "" || head.Hash == "" {
		return 0, common.Hash{}, errors.New("empty block head from RPC")
	}
	num, err := strconv.ParseUint(numHex, 16, 64)
	if err != nil {
		return 0, common.Hash{}, err
	}
	return num, common.HexToHash(head.Hash), nil
}

// QuotePreflight resolves the protocol route and quotes expected/min output on a pinned blockHash,
// plus live ERC-20/Permit2 allowance when Side=="sell". Zero DB writes, zero reservations.
func (p *RPCLocalV2Provider) QuotePreflight(ctx context.Context, req QuotePreflightRequest, spender common.Address) (QuotePreflightResponse, error) {
	return p.QuotePreflightForWallet(ctx, req, p.walletAddr, spender)
}

// QuotePreflightForWallet resolves the protocol route and checks live allowance for the explicit walletAddr.
func (p *RPCLocalV2Provider) QuotePreflightForWallet(ctx context.Context, req QuotePreflightRequest, walletAddr common.Address, spender common.Address) (QuotePreflightResponse, error) {
	if p == nil || p.reg == nil {
		return QuotePreflightResponse{}, evm.ErrRPCStateLagging
	}
	adapter, err := p.reg.Adapter(req.Protocol)
	if err != nil {
		return QuotePreflightResponse{}, err
	}
	amtIn, ok := new(big.Int).SetString(req.AmountRaw, 10)
	if !ok || amtIn.Sign() <= 0 {
		return QuotePreflightResponse{}, ErrInvalid
	}
	side := ports.Side(strings.ToLower(strings.TrimSpace(req.Side)))
	if side != ports.SideBuy && side != ports.SideSell {
		return QuotePreflightResponse{}, ErrInvalid
	}
	blockNum, blockHash, err := p.latestBlockHead(ctx)
	if err != nil {
		return QuotePreflightResponse{}, fmt.Errorf("%w: %v", evm.ErrRPCStateLagging, err)
	}
	tokenAddr := common.HexToAddress(req.Token)
	route, err := adapter.Resolve(ctx, p, tokenAddr, blockNum, blockHash)
	if err != nil {
		return QuotePreflightResponse{}, err
	}
	quote, err := adapter.Quote(ctx, p, ports.QuoteRequest{
		Route:       route,
		Side:        side,
		AmountInRaw: amtIn,
		SlippageBps: req.SlippageBps,
		Recipient:   walletAddr,
	})
	if err != nil {
		return QuotePreflightResponse{}, err
	}

	allowance := big.NewInt(0)
	needsApprove := false
	if side == ports.SideSell && spender != (common.Address{}) && walletAddr != (common.Address{}) {
		if adapter.Describe().AllowanceModel == ports.AllowancePermit2 {
			state, readErr := permit2.Read(ctx, p, walletAddr, tokenAddr, spender, blockHash)
			if readErr != nil {
				return QuotePreflightResponse{}, readErr
			}
			var header struct {
				Timestamp string `json:"timestamp"`
			}
			if err := p.rpcCall(ctx, "eth_getBlockByHash", []any{blockHash.Hex(), false}, &header); err != nil {
				return QuotePreflightResponse{}, err
			}
			blockTime, err := strconv.ParseUint(strings.TrimPrefix(header.Timestamp, "0x"), 16, 64)
			if err != nil {
				return QuotePreflightResponse{}, errors.New("invalid pinned block timestamp")
			}
			allowance = state.Effective(blockTime)
		} else {
			allowData, err := erc20ReadABI.Pack("allowance", walletAddr, spender)
			if err == nil {
				if rawAllow, cErr := p.CallContractAt(ctx, tokenAddr, allowData, blockHash); cErr == nil && len(rawAllow) >= 32 {
					allowance = new(big.Int).SetBytes(rawAllow[:32])
				}
			}
		}
		needsApprove = allowance.Cmp(amtIn) < 0
	}
	return QuotePreflightResponse{
		ChainID:          p.reg.Chain().ChainID,
		Protocol:         req.Protocol,
		PoolPhase:        route.PoolPhase,
		Spender:          strings.ToLower(spender.Hex()),
		ExpectedOutRaw:   quote.ExpectedOutRaw.String(),
		MinOutRaw:        quote.MinOutRaw.String(),
		EstimatedGas:     quote.EstimatedGas,
		CurrentAllowance: allowance.String(),
		NeedsApprove:     needsApprove,
		QuoteBlockNumber: blockNum,
	}, nil
}

// ReadPosition fetches an atomic single-block [nativeBalance, tokenBalance, decimals, allowance]
// snapshot via Multicall3.aggregate3 inside a single JSON-RPC Batch request, enforcing requiredBlock.
func (p *RPCLocalV2Provider) ReadPosition(
	ctx context.Context,
	walletID string,
	walletAddr common.Address,
	token common.Address,
	spender common.Address,
	requiredBlock uint64,
) (PositionSnapshot, error) {
	if p == nil || p.reg == nil || p.batch == nil {
		return PositionSnapshot{}, evm.ErrRPCStateLagging
	}
	mc3Addr := p.reg.Chain().Multicall3
	if mc3Addr == (common.Address{}) {
		mc3Addr = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")
	}
	ethBalCall, _ := mc3ABI.Pack("getEthBalance", walletAddr)
	tokBalCall, _ := erc20ReadABI.Pack("balanceOf", walletAddr)
	decCall, _ := erc20ReadABI.Pack("decimals")

	calls := []multicall3Call3{
		{Target: mc3Addr, AllowFailure: false, CallData: ethBalCall},
		{Target: token, AllowFailure: false, CallData: tokBalCall},
		{Target: token, AllowFailure: true, CallData: decCall},
	}
	if spender != (common.Address{}) {
		allowCall, _ := erc20ReadABI.Pack("allowance", walletAddr, spender)
		calls = append(calls, multicall3Call3{Target: token, AllowFailure: true, CallData: allowCall})
	}
	aggCalldata, err := mc3ABI.Pack("aggregate3", calls)
	if err != nil {
		return PositionSnapshot{}, err
	}

	snap, err := p.batch.FetchMulticallAndNonces(ctx, mc3Addr, aggCalldata, walletAddr, requiredBlock)
	if err != nil {
		return PositionSnapshot{}, err
	}
	unpacked, err := mc3ABI.Unpack("aggregate3", snap.MulticallData)
	if err != nil || len(unpacked) != 1 {
		return PositionSnapshot{}, fmt.Errorf("unpack Multicall3.aggregate3: %w", err)
	}
	rawResults, ok := unpacked[0].([]struct {
		Success    bool   `json:"success"`
		ReturnData []byte `json:"returnData"`
	})
	if !ok || len(rawResults) < 3 || !rawResults[0].Success || len(rawResults[0].ReturnData) < 32 || !rawResults[1].Success || len(rawResults[1].ReturnData) < 32 {
		return PositionSnapshot{}, errors.New("incomplete Multicall3 position return data")
	}

	nativeBal := new(big.Int).SetBytes(rawResults[0].ReturnData[:32])
	tokenBal := new(big.Int).SetBytes(rawResults[1].ReturnData[:32])
	decimals := uint8(18)
	if rawResults[2].Success && len(rawResults[2].ReturnData) >= 32 {
		decimals = rawResults[2].ReturnData[31]
	}
	allowance := big.NewInt(0)
	if len(rawResults) >= 4 && rawResults[3].Success && len(rawResults[3].ReturnData) >= 32 {
		allowance = new(big.Int).SetBytes(rawResults[3].ReturnData[:32])
	}

	walletState, hasUnfinalized := p.readLocalWalletRiskState(ctx, walletID)
	var spenderStr string
	if spender != (common.Address{}) {
		spenderStr = strings.ToLower(spender.Hex())
	}
	return PositionSnapshot{
		ChainID:           p.reg.Chain().ChainID,
		WalletID:          walletID,
		WalletAddress:     strings.ToLower(walletAddr.Hex()),
		WalletState:       walletState,
		Token:             strings.ToLower(token.Hex()),
		NativeBalanceRaw:  nativeBal.String(),
		TokenBalanceRaw:   tokenBal.String(),
		Decimals:          decimals,
		AllowanceRaw:      allowance.String(),
		Spender:           spenderStr,
		HasUnfinalizedBuy: hasUnfinalized,
		BlockNumber:       snap.BlockNumber,
	}, nil
}

func (p *RPCLocalV2Provider) readLocalWalletRiskState(ctx context.Context, walletID string) (state string, hasUnfinalized bool) {
	state = "ready"
	if p == nil || p.database == nil || walletID == "" {
		return state, false
	}
	rdb, err := p.database.ReadDB(storage.TradeOwner)
	if err != nil || rdb == nil {
		return state, false
	}
	var resState string
	err = rdb.QueryRowContext(ctx,
		`SELECT state FROM canary_risk_reservations WHERE wallet_id = ? AND state IN ('reserved', 'frozen') ORDER BY created_at DESC LIMIT 1`,
		walletID,
	).Scan(&resState)
	if err == nil {
		if resState == "frozen" {
			state = "frozen"
		} else {
			state = "in_flight"
		}
		hasUnfinalized = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return state, false
	}
	return state, hasUnfinalized
}

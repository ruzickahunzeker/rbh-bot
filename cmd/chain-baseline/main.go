// Command chain-baseline captures an immutable, read-only contract identity
// baseline. It never signs or sends transactions.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	pons "github.com/0xfnzero/rbh-trade-sdk/adapters/pons"
	trade "github.com/0xfnzero/rbh-trade-sdk/rbhtrade"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	chainID            = 4663
	parserSDKCommit    = "72cf9cee394bbd2566fca1d03202b05981eb51b4"
	tradeSDKCommit     = "a894cf496b862144ea00910e73afe553073a858b"
	implementationSlot = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
)

type contractSpec struct {
	Protocol string
	Role     string
	Address  common.Address
}

type contractEvidence struct {
	Protocol                   string `json:"protocol"`
	Role                       string `json:"role"`
	Address                    string `json:"address"`
	RuntimeBytecodeHash        string `json:"runtime_bytecode_hash"`
	RuntimeBytecodeBytes       int    `json:"runtime_bytecode_bytes"`
	ProxyType                  string `json:"proxy_type"`
	ImplementationAddress      string `json:"implementation_address,omitempty"`
	ImplementationBytecodeHash string `json:"implementation_bytecode_hash,omitempty"`
}

type baseline struct {
	SchemaVersion        int                `json:"schema_version"`
	Status               string             `json:"status"`
	ChainID              uint64             `json:"chain_id"`
	VerifiedBlockNumber  uint64             `json:"verified_block_number"`
	VerifiedBlockHash    string             `json:"verified_block_hash"`
	CapturedAt           time.Time          `json:"captured_at"`
	RPCSource            string             `json:"rpc_source"`
	ParserSDKCommit      string             `json:"parser_sdk_commit"`
	TradeSDKCommit       string             `json:"trade_sdk_commit"`
	SDKAddressBooksMatch bool               `json:"sdk_addressbooks_match"`
	Contracts            []contractEvidence `json:"contracts"`
}

func main() {
	output := flag.String("output", "configs/mainnet/contracts.lock.json", "output path")
	blockNumber := flag.Uint64("block", 0, "verification block; zero selects latest")
	flag.Parse()
	rpcURL := os.Getenv("ROBINHOOD_RPC_URL")
	if rpcURL == "" {
		fatal(errors.New("ROBINHOOD_RPC_URL is required"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		fatal(fmt.Errorf("dial RPC: %w", err))
	}
	defer client.Close()
	gotChainID, err := client.ChainID(ctx)
	if err != nil || !gotChainID.IsUint64() || gotChainID.Uint64() != chainID {
		fatal(fmt.Errorf("chain identity: got %v: %w", gotChainID, err))
	}
	var requested *big.Int
	if *blockNumber != 0 {
		requested = new(big.Int).SetUint64(*blockNumber)
	}
	header, err := client.HeaderByNumber(ctx, requested)
	if err != nil {
		fatal(fmt.Errorf("read verification block: %w", err))
	}
	result := baseline{
		SchemaVersion: 1, Status: "CAPTURED_REQUIRES_CLASSIFICATION_REVIEW", ChainID: chainID,
		VerifiedBlockNumber: header.Number.Uint64(), VerifiedBlockHash: header.Hash().Hex(),
		CapturedAt: time.Now().UTC(), RPCSource: "ROBINHOOD_RPC_URL (redacted)",
		ParserSDKCommit: parserSDKCommit, TradeSDKCommit: tradeSDKCommit,
		SDKAddressBooksMatch: addressBooksMatch(),
	}
	if !result.SDKAddressBooksMatch {
		fatal(errors.New("locked Parser and Trade SDK address books disagree"))
	}
	for _, spec := range ponsContracts() {
		evidence, err := inspect(ctx, client, header.Number, spec)
		if err != nil {
			fatal(err)
		}
		result.Contracts = append(result.Contracts, evidence)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o750); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*output, append(encoded, '\n'), 0o640); err != nil {
		fatal(err)
	}
}

func addressBooksMatch() bool {
	p := parser.DefaultAddressBook()
	t := trade.DefaultAddressBook()
	return p.PoolManager == t.PoolManager && p.UniversalRouter == t.UniversalRouter &&
		p.WETH == t.WETH && p.USDG == t.USDG && p.PonsFactory == t.PonsFactory &&
		p.PonsLaunchAndBuy == t.PonsLaunchAndBuy && p.PonsMemeHook == t.PonsMemeHook
}

func ponsContracts() []contractSpec {
	root := trade.DefaultAddressBook()
	p := pons.PonsV2Addresses
	return []contractSpec{
		{"pons-v2", "factory", p.Factory}, {"pons-v2", "launch_and_buy", p.LaunchAndBuy},
		{"pons-v2", "meme_hook", p.MemeHook}, {"pons-v2", "fee_escrow", p.FeeEscrow},
		{"pons-v2", "buyback_vault", p.BuybackVault}, {"pons-v2", "launch_locker", p.LaunchLocker},
		{"pons-v2", "launch_deployer", p.LaunchDeployer}, {"pons-v2", "graduation_executor", p.GraduationExecutor},
		{"pons-v2", "graduation_guard", p.GraduationGuard},
		{"uniswap-v4", "pool_manager", root.PoolManager}, {"uniswap-v4", "position_manager", root.PositionManager},
		{"uniswap-v4", "quoter", root.V4Quoter}, {"uniswap-v4", "state_view", root.StateView},
		{"uniswap-v4", "universal_router", root.UniversalRouter}, {"uniswap-v4", "permit2", root.Permit2},
		{"shared", "weth", root.WETH}, {"shared", "usdg", root.USDG},
	}
}

func inspect(ctx context.Context, client *ethclient.Client, block *big.Int, spec contractSpec) (contractEvidence, error) {
	code, err := client.CodeAt(ctx, spec.Address, block)
	if err != nil {
		return contractEvidence{}, fmt.Errorf("read %s code: %w", spec.Role, err)
	}
	if len(code) == 0 {
		return contractEvidence{}, fmt.Errorf("%s has no code at %s", spec.Role, block)
	}
	result := contractEvidence{Protocol: spec.Protocol, Role: spec.Role, Address: spec.Address.Hex(), RuntimeBytecodeHash: crypto.Keccak256Hash(code).Hex(), RuntimeBytecodeBytes: len(code), ProxyType: "no_eip1967_implementation_slot"}
	storage, err := client.StorageAt(ctx, spec.Address, common.HexToHash(implementationSlot), block)
	if err != nil {
		return contractEvidence{}, fmt.Errorf("read %s implementation slot: %w", spec.Role, err)
	}
	implementation := common.BytesToAddress(storage)
	if implementation != (common.Address{}) {
		implementationCode, err := client.CodeAt(ctx, implementation, block)
		if err != nil || len(implementationCode) == 0 {
			return contractEvidence{}, fmt.Errorf("read %s implementation code: %w", spec.Role, err)
		}
		result.ProxyType = "eip1967"
		result.ImplementationAddress = implementation.Hex()
		result.ImplementationBytecodeHash = crypto.Keccak256Hash(implementationCode).Hex()
	}
	return result, nil
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// Command classify-pons-curve classifies only contracts required to trust Pons Curve feed events.
// It is read-only and fails closed when a proxy form cannot be identified.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	chainID            = 4663
	parserSDKCommit    = "72cf9cee394bbd2566fca1d03202b05981eb51b4"
	ponsSourceCommit   = "58cba1cb2bbc7f8dc4b13b810929461eb36bc15b"
	implementationSlot = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
	beaconSlot         = "0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50"
)

type target struct {
	Role                string
	Address             common.Address
	AuthoritativeSource string
	RequiredForFeed     bool
}

type classification struct {
	Address                    string `json:"address"`
	Role                       string `json:"role"`
	Classification             string `json:"classification"`
	RuntimeCodeHash            string `json:"runtime_code_hash"`
	RuntimeCodeBytes           int    `json:"runtime_code_bytes"`
	ImplementationAddress      string `json:"implementation_address,omitempty"`
	ImplementationCodeHash     string `json:"implementation_code_hash,omitempty"`
	BeaconAddress              string `json:"beacon_address,omitempty"`
	VerifiedBlock              uint64 `json:"verified_block"`
	AuthoritativeSource        string `json:"authoritative_source"`
	RequiredForFeed            bool   `json:"required_for_feed"`
	TrustedForFeed             bool   `json:"trusted_for_feed"`
	ReachableDelegatecallCount int    `json:"reachable_delegatecall_count"`
	Reason                     string `json:"reason"`
}

type report struct {
	SchemaVersion int              `json:"schema_version"`
	Status        string           `json:"status"`
	ChainID       uint64           `json:"chain_id"`
	BlockNumber   uint64           `json:"block_number"`
	BlockHash     string           `json:"block_hash"`
	CapturedAt    time.Time        `json:"captured_at"`
	Contracts     []classification `json:"contracts"`
}

func main() {
	curveText := flag.String("curve", "0x4197749b1b3bbb9e1d8a183f968cc54c42e882a7", "fixture curve address")
	blockNumber := flag.Uint64("block", 64376127, "verification block; zero selects latest")
	output := flag.String("output", "rbh-validation-package/runs/c02-pons-curve-classification.json", "output file")
	flag.Parse()
	if !common.IsHexAddress(*curveText) {
		fatal(errors.New("invalid curve address"))
	}
	rpcURL := os.Getenv("ROBINHOOD_RPC_URL")
	if rpcURL == "" {
		fatal(errors.New("ROBINHOOD_RPC_URL is required"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		fatal(err)
	}
	defer client.Close()
	gotChainID, err := client.ChainID(ctx)
	if err != nil || !gotChainID.IsUint64() || gotChainID.Uint64() != chainID {
		fatal(fmt.Errorf("wrong chain: %v: %w", gotChainID, err))
	}
	var block *big.Int
	if *blockNumber != 0 {
		block = new(big.Int).SetUint64(*blockNumber)
	}
	header, err := client.HeaderByNumber(ctx, block)
	if err != nil {
		fatal(fmt.Errorf("verification block unavailable: %w", err))
	}
	addresses := parser.DefaultAddressBook()
	targets := []target{
		{Role: "pons_factory", Address: addresses.PonsFactory, AuthoritativeSource: "parser-sdk v0.4.0 commit " + parserSDKCommit + " AddressBook plus official ponsdotdev/ponsfamily commit " + ponsSourceCommit + " PonsV2LaunchFactory", RequiredForFeed: true},
		{Role: "pons_curve", Address: common.HexToAddress(*curveText), AuthoritativeSource: "PonsFactory TokenLaunched raw log in tx 0x45142a829ca4ce83909c0f87408b27f70da8a1853a101921fffc20ff5fc08b10 plus official ponsdotdev/ponsfamily commit " + ponsSourceCommit + " PonsV2BondingCurve", RequiredForFeed: true},
		{Role: "pons_launch_and_buy", Address: addresses.PonsLaunchAndBuy, AuthoritativeSource: "parser-sdk v0.4.0 commit " + parserSDKCommit + " DefaultAddressBook.PonsLaunchAndBuy", RequiredForFeed: false},
	}
	block = header.Number
	result := report{SchemaVersion: 1, Status: "C02_PONS_CURVE_PASS", ChainID: chainID, BlockNumber: block.Uint64(), BlockHash: header.Hash().Hex(), CapturedAt: time.Now().UTC()}
	for _, item := range targets {
		entry, err := inspect(ctx, client, block, item)
		if err != nil {
			fatal(err)
		}
		if item.RequiredForFeed && !entry.TrustedForFeed {
			result.Status = "C02_PONS_CURVE_PARTIAL_FAIL_CLOSED"
		}
		result.Contracts = append(result.Contracts, entry)
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

func inspect(ctx context.Context, client *ethclient.Client, block *big.Int, item target) (classification, error) {
	code, err := client.CodeAt(ctx, item.Address, block)
	if err != nil {
		return classification{}, fmt.Errorf("read %s code: %w", item.Role, err)
	}
	result := classification{Address: strings.ToLower(item.Address.Hex()), Role: item.Role, Classification: "unknown", RuntimeCodeHash: crypto.Keccak256Hash(code).Hex(), RuntimeCodeBytes: len(code), VerifiedBlock: block.Uint64(), AuthoritativeSource: item.AuthoritativeSource, RequiredForFeed: item.RequiredForFeed}
	if len(code) == 0 {
		result.Reason = "no runtime code"
		return result, nil
	}
	implementation, err := storageAddress(ctx, client, item.Address, implementationSlot, block)
	if err != nil {
		return result, err
	}
	if implementation != (common.Address{}) {
		implementationCode, readErr := client.CodeAt(ctx, implementation, block)
		if readErr != nil || len(implementationCode) == 0 {
			result.Reason = "ERC-1967 implementation has no readable code"
			return result, nil
		}
		result.Classification, result.ImplementationAddress = "erc1967_proxy", strings.ToLower(implementation.Hex())
		result.ImplementationCodeHash = crypto.Keccak256Hash(implementationCode).Hex()
		result.TrustedForFeed, result.Reason = true, "non-zero ERC-1967 implementation slot with code"
		return result, nil
	}
	beacon, err := storageAddress(ctx, client, item.Address, beaconSlot, block)
	if err != nil {
		return result, err
	}
	if beacon != (common.Address{}) {
		result.Classification, result.BeaconAddress = "beacon_proxy", strings.ToLower(beacon.Hex())
		result.Reason = "beacon detected; implementation resolution not independently verified"
		return result, nil
	}
	if implementation, ok := minimalProxyImplementation(code); ok {
		implementationCode, readErr := client.CodeAt(ctx, implementation, block)
		if readErr != nil || len(implementationCode) == 0 {
			result.Reason = "minimal proxy implementation has no readable code"
			return result, nil
		}
		result.Classification, result.ImplementationAddress = "minimal_proxy", strings.ToLower(implementation.Hex())
		result.ImplementationCodeHash = crypto.Keccak256Hash(implementationCode).Hex()
		result.TrustedForFeed, result.Reason = true, "recognized EIP-1167 runtime with implementation code"
		return result, nil
	}
	executable := executableRuntime(code)
	result.ReachableDelegatecallCount = opcodeCount(executable, 0xf4)
	if result.ReachableDelegatecallCount == 0 {
		result.Classification, result.TrustedForFeed, result.Reason = "direct_implementation", true, "no proxy slots, no EIP-1167 pattern and no DELEGATECALL opcode"
		return result, nil
	}
	result.Reason = "DELEGATECALL present without a recognized proxy standard"
	return result, nil
}

// Solidity appends CBOR metadata followed by its two-byte big-endian length.
// Metadata is not executable and may contain bytes that resemble opcodes.
func executableRuntime(code []byte) []byte {
	if len(code) < 3 {
		return code
	}
	metadataLength := int(code[len(code)-2])<<8 | int(code[len(code)-1])
	start := len(code) - metadataLength - 2
	if start < 0 || start >= len(code)-2 {
		return code
	}
	if code[start] < 0xa0 || code[start] > 0xbf {
		return code
	}
	return code[:start]
}

func storageAddress(ctx context.Context, client *ethclient.Client, address common.Address, slot string, block *big.Int) (common.Address, error) {
	value, err := client.StorageAt(ctx, address, common.HexToHash(slot), block)
	if err != nil {
		return common.Address{}, fmt.Errorf("read storage %s at %s: %w", slot, address, err)
	}
	return common.BytesToAddress(value), nil
}

func minimalProxyImplementation(code []byte) (common.Address, bool) {
	prefix, _ := hex.DecodeString("363d3d373d3d3d363d73")
	suffix, _ := hex.DecodeString("5af43d82803e903d91602b57fd5bf3")
	if len(code) != len(prefix)+20+len(suffix) || !strings.HasPrefix(hex.EncodeToString(code), hex.EncodeToString(prefix)) || !strings.HasSuffix(hex.EncodeToString(code), hex.EncodeToString(suffix)) {
		return common.Address{}, false
	}
	return common.BytesToAddress(code[len(prefix) : len(prefix)+20]), true
}

func opcodeCount(code []byte, wanted byte) int {
	count := 0
	for index := 0; index < len(code); index++ {
		opcode := code[index]
		if opcode == wanted {
			count++
		}
		if opcode >= 0x60 && opcode <= 0x7f {
			index += int(opcode - 0x5f)
		}
	}
	return count
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// Package permit2 provides pinned, read-only allowance checks and unsigned approval calls.
package permit2

import (
	"context"
	"errors"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
)

var Address = common.HexToAddress("0x000000000022D473030F116dDEE9F6B43aC78BA3")

var tokenABI = parse(`[
{"type":"function","name":"allowance","inputs":[{"type":"address"},{"type":"address"}],"outputs":[{"type":"uint256"}]},
{"type":"function","name":"approve","inputs":[{"type":"address"},{"type":"uint256"}],"outputs":[{"type":"bool"}]}
]`)
var contractABI = parse(`[
{"type":"function","name":"allowance","inputs":[{"type":"address"},{"type":"address"},{"type":"address"}],"outputs":[{"type":"uint160"},{"type":"uint48"},{"type":"uint48"}]},
{"type":"function","name":"approve","inputs":[{"type":"address"},{"type":"address"},{"type":"uint160"},{"type":"uint48"}],"outputs":[]}
]`)

func parse(src string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(src))
	if err != nil {
		panic(err)
	}
	return a
}

type State struct {
	ERC20Amount  *big.Int
	RouterAmount *big.Int
	Expiration   uint64
}

// Read fails closed on missing/malformed RPC results. All reads use the same block hash.
func Read(ctx context.Context, rpc ports.ReadOnlyRPC, owner, token, router common.Address, block common.Hash) (State, error) {
	cd, err := tokenABI.Pack("allowance", owner, Address)
	if err != nil {
		return State{}, err
	}
	raw, err := rpc.CallContractAt(ctx, token, cd, block)
	if err != nil {
		return State{}, err
	}
	tokenOut, err := tokenABI.Unpack("allowance", raw)
	if err != nil || len(tokenOut) != 1 {
		return State{}, errors.New("invalid ERC20 allowance response")
	}
	cd, err = contractABI.Pack("allowance", owner, token, router)
	if err != nil {
		return State{}, err
	}
	raw, err = rpc.CallContractAt(ctx, Address, cd, block)
	if err != nil {
		return State{}, err
	}
	out, err := contractABI.Unpack("allowance", raw)
	if err != nil || len(out) != 3 {
		return State{}, errors.New("invalid Permit2 allowance response")
	}
	amount, ok := out[0].(*big.Int)
	expiry, expOK := out[1].(*big.Int)
	if !ok || !expOK || !expiry.IsUint64() {
		return State{}, errors.New("invalid Permit2 allowance types")
	}
	return State{ERC20Amount: tokenOut[0].(*big.Int), RouterAmount: amount, Expiration: expiry.Uint64()}, nil
}

// Effective returns the amount usable through validThrough, including the expiration check.
func (s State) Effective(validThrough uint64) *big.Int {
	if s.Expiration <= validThrough {
		return big.NewInt(0)
	}
	if s.ERC20Amount.Cmp(s.RouterAmount) < 0 {
		return new(big.Int).Set(s.ERC20Amount)
	}
	return new(big.Int).Set(s.RouterAmount)
}

type Approval struct {
	To       common.Address
	Calldata []byte
	Kind     string
}

// Approvals authorizes only the requested amount, and renews an allowance that expires
// before the swap deadline. The executor confirms each call before unlocking the next.
func (s State) Approvals(token, router common.Address, amount *big.Int, validThrough uint64) ([]Approval, error) {
	if amount == nil || amount.Sign() <= 0 || amount.BitLen() > 160 || validThrough >= (1<<48)-1 {
		return nil, errors.New("invalid Permit2 approval bounds")
	}
	var calls []Approval
	if s.ERC20Amount.Cmp(amount) < 0 {
		cd, err := tokenABI.Pack("approve", Address, amount)
		if err != nil {
			return nil, err
		}
		calls = append(calls, Approval{token, cd, "approve_exact"})
	}
	if s.RouterAmount.Cmp(amount) < 0 || s.Expiration <= validThrough {
		// One second beyond the contract deadline keeps equality from expiring early.
		cd, err := contractABI.Pack("approve", token, router, amount, new(big.Int).SetUint64(validThrough+1))
		if err != nil {
			return nil, err
		}
		calls = append(calls, Approval{Address, cd, "permit2"})
	}
	return calls, nil
}

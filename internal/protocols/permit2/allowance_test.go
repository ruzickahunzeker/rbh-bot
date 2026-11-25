package permit2

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

type allowanceRPC struct {
	t                    *testing.T
	owner, token, router common.Address
	block                common.Hash
	state                State
	bad                  bool
}

func (r allowanceRPC) CodeAt(context.Context, common.Address, common.Hash) ([]byte, error) {
	return nil, nil
}
func (r allowanceRPC) CallContractAt(_ context.Context, to common.Address, cd []byte, block common.Hash) ([]byte, error) {
	if block != r.block {
		r.t.Fatal("allowance read is not pinned")
	}
	if r.bad {
		return []byte{}, nil
	}
	if to == r.token {
		args, err := tokenABI.Methods["allowance"].Inputs.Unpack(cd[4:])
		if err != nil || args[0].(common.Address) != r.owner || args[1].(common.Address) != Address {
			return nil, errors.New("wrong ERC20 spender")
		}
		return tokenABI.Methods["allowance"].Outputs.Pack(r.state.ERC20Amount)
	}
	if to != Address {
		return nil, errors.New("wrong Permit2 target")
	}
	args, err := contractABI.Methods["allowance"].Inputs.Unpack(cd[4:])
	if err != nil || args[0].(common.Address) != r.owner || args[1].(common.Address) != r.token || args[2].(common.Address) != r.router {
		return nil, errors.New("wrong Permit2 allowance identity")
	}
	return contractABI.Methods["allowance"].Outputs.Pack(r.state.RouterAmount, new(big.Int).SetUint64(r.state.Expiration), big.NewInt(0))
}

func TestPermit2AllowanceLayersAndExpiry(t *testing.T) {
	owner, token, router := common.HexToAddress("0x1"), common.HexToAddress("0x2"), common.HexToAddress("0x3")
	for _, tc := range []struct {
		name          string
		erc20, router int64
		expiry        uint64
		effective     int64
		approvals     int
	}{
		{"both missing", 0, 0, 0, 0, 2},
		{"ERC20 missing", 0, 100, 1001, 0, 1},
		{"router missing", 100, 0, 1001, 0, 1},
		{"expired", 100, 100, 999, 0, 1},
		{"expires at deadline", 100, 100, 1000, 0, 1},
		{"ready", 100, 100, 1001, 100, 0},
		{"limited ERC20", 50, 100, 1001, 50, 1},
		{"limited router", 100, 50, 1001, 50, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := allowanceRPC{t: t, owner: owner, token: token, router: router, block: common.HexToHash("0xaa"), state: State{big.NewInt(tc.erc20), big.NewInt(tc.router), tc.expiry}}
			state, err := Read(context.Background(), rpc, owner, token, router, rpc.block)
			if err != nil {
				t.Fatal(err)
			}
			if state.Effective(1000).Int64() != tc.effective {
				t.Fatal("incorrect effective allowance")
			}
			calls, err := state.Approvals(token, router, big.NewInt(100), 1000)
			if err != nil || len(calls) != tc.approvals {
				t.Fatalf("calls=%v err=%v", calls, err)
			}
			for _, call := range calls {
				if call.To == token {
					args, err := tokenABI.Methods["approve"].Inputs.Unpack(call.Calldata[4:])
					if err != nil || args[0].(common.Address) != Address || args[1].(*big.Int).Int64() != 100 {
						t.Fatal("wrong token approval")
					}
				} else if call.To == Address {
					args, err := contractABI.Methods["approve"].Inputs.Unpack(call.Calldata[4:])
					if err != nil || args[0].(common.Address) != token || args[1].(common.Address) != router || args[2].(*big.Int).Int64() != 100 || args[3].(*big.Int).Int64() != 1001 {
						t.Fatal("wrong Permit2 approval")
					}
				} else {
					t.Fatal("untrusted target")
				}
			}
		})
	}
	rpc := allowanceRPC{t: t, bad: true}
	if _, err := Read(context.Background(), rpc, owner, token, router, common.Hash{}); err == nil {
		t.Fatal("malformed response accepted")
	}
	state := State{big.NewInt(0), big.NewInt(0), 0}
	if _, err := state.Approvals(token, router, new(big.Int).Lsh(big.NewInt(1), 160), 1000); err == nil {
		t.Fatal("uint160 overflow accepted")
	}
}

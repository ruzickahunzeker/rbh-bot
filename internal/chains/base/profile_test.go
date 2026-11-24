package base

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/evm"
)

type stubOracleRPC struct {
	l1FeeWei *big.Int
}

func (s *stubOracleRPC) CodeAt(_ context.Context, _ common.Address, _ common.Hash) ([]byte, error) {
	return []byte{0x60, 0x80}, nil
}

func (s *stubOracleRPC) CallContractAt(_ context.Context, to common.Address, data []byte, _ common.Hash) ([]byte, error) {
	if to == OPGasPriceOracle && len(data) >= 4 && bytes.Equal(data[:4], gasOracleABI.Methods["getL1Fee"].ID) {
		return gasOracleABI.Methods["getL1Fee"].Outputs.Pack(s.l1FeeWei)
	}
	return nil, nil
}

func TestBaseProfileAndOPStackL1DataFeeBound(t *testing.T) {
	p := Profile()
	if p.ChainID != 8453 || p.ChainSlug != "base" || p.Fee.EstimateIncludesL1Fee {
		t.Fatalf("unexpected Base profile: %+v", p)
	}

	// Verify OP Stack GasPriceOracle.getL1Fee(rawTx) with +20% (2000 bps) buffer.
	rpc := &stubOracleRPC{l1FeeWei: big.NewInt(50_000_000_000_000)} // 0.00005 ETH
	l1Bound, err := EstimateL1DataFee(context.Background(), rpc, []byte{0x02, 0xf8, 0x01, 0x02}, common.HexToHash("0x1234"), 2000)
	if err != nil {
		t.Fatalf("EstimateL1DataFee failed: %v", err)
	}
	wantL1 := big.NewInt(60_000_000_000_000) // +20%
	if l1Bound.Cmp(wantL1) != 0 {
		t.Fatalf("expected buffered L1 fee %s, got %s", wantL1, l1Bound)
	}

	// Verify total native outlay bound includes tx.value + L2 gas + OP Stack L1 data fee.
	total, err := evm.ComputeNativeOutlayBound(big.NewInt(1_000_000_000_000_000), 200_000, big.NewInt(10_000_000), l1Bound)
	if err != nil {
		t.Fatalf("ComputeNativeOutlayBound failed: %v", err)
	}
	wantTotal := big.NewInt(1_062_000_000_000_000) // 1e15 + 2e12 + 6e13
	if total.Cmp(wantTotal) != 0 {
		t.Fatalf("expected total outlay %s, got %s", wantTotal, total)
	}
}

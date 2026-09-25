package rbhparser

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestParseModifyLiquidity(t *testing.T) {
	poolID := common.HexToHash("0x1234")
	sender := common.HexToAddress("0x1000000000000000000000000000000000000001")
	data := make([]byte, 128)
	putSignedWord(data[0:32], -887220)
	putSignedWord(data[32:64], 887220)
	putSignedWord(data[64:96], 1_000_000)
	salt := common.HexToHash("0x55")
	copy(data[96:128], salt[:])
	log := types.Log{Address: DefaultAddressBook().PoolManager, Topics: []common.Hash{TopicPoolLiquidity, poolID, common.BytesToHash(sender.Bytes())}, Data: data}
	event, ok, err := New().ParseLog(log)
	if err != nil || !ok || event.Kind != EventLiquidityModified {
		t.Fatalf("event = %#v, ok=%v err=%v", event, ok, err)
	}
	modified := event.Data.(LiquidityModified)
	if modified.PoolID != poolID || modified.Sender != sender || modified.TickLower != -887220 || modified.TickUpper != 887220 || modified.LiquidityDelta.Cmp(big.NewInt(1_000_000)) != 0 {
		t.Fatalf("modified = %#v", modified)
	}
}

func TestParseModifyLiquidityPositionPokeWithZeroDelta(t *testing.T) {
	poolID := common.HexToHash("0xd5866ecf2733446c41e31ea04b504b0f99b9a61463c53434336e3565857aa481")
	sender := common.HexToAddress("0x58daec3116aae6d93017baaea7749052e8a04fa7")
	data := common.FromHex("0xfffffffffffffffffffffffffffffffffffffffffffffffffffffffffffcf964" +
		"fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffd0936" +
		"0000000000000000000000000000000000000000000000000000000000000000" +
		"00000000000000000000000000000000000000000000000000000000001fe552")
	log := types.Log{
		Address: DefaultAddressBook().PoolManager,
		Topics: []common.Hash{
			TopicPoolLiquidity,
			poolID,
			common.BytesToHash(sender.Bytes()),
		},
		Data: data,
	}
	event, ok, err := New().ParseLog(log)
	if err != nil || !ok || event.Kind != EventLiquidityModified {
		t.Fatalf("event = %#v, ok=%v err=%v", event, ok, err)
	}
	modified := event.Data.(LiquidityModified)
	if modified.PoolID != poolID || modified.Sender != sender || modified.LiquidityDelta.Sign() != 0 {
		t.Fatalf("modified = %#v", modified)
	}
}

func putSignedWord(dst []byte, value int64) {
	n := big.NewInt(value)
	if value < 0 {
		n.Add(n, new(big.Int).Lsh(big.NewInt(1), 256))
	}
	n.FillBytes(dst)
}

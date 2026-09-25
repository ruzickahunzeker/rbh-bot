package rbhparser

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
)

const maxLogDataBytes = 1 << 20

func validateLog(log gethtypes.Log, topics int, dataBytes int) error {
	if len(log.Topics) != topics || len(log.Data) != dataBytes || len(log.Data) > maxLogDataBytes {
		return ErrMalformedLog
	}
	return nil
}

func topicAddress(topic common.Hash) (common.Address, error) {
	for _, value := range topic[:12] {
		if value != 0 {
			return common.Address{}, ErrMalformedLog
		}
	}
	return common.BytesToAddress(topic[12:]), nil
}

func word(data []byte, index int) ([]byte, error) {
	start := index * 32
	if index < 0 || start+32 > len(data) {
		return nil, ErrMalformedLog
	}
	return data[start : start+32], nil
}

func uintWord(data []byte, index, bits int) (*big.Int, error) {
	w, err := word(data, index)
	if err != nil {
		return nil, err
	}
	value := new(big.Int).SetBytes(w)
	if value.BitLen() > bits {
		return nil, ErrMalformedLog
	}
	return value, nil
}

func uint24Word(data []byte, index int) (uint32, error) {
	w, err := word(data, index)
	if err != nil {
		return 0, err
	}
	for _, value := range w[:29] {
		if value != 0 {
			return 0, ErrMalformedLog
		}
	}
	return uint32(w[29])<<16 | uint32(w[30])<<8 | uint32(w[31]), nil
}

func int24Word(data []byte, index int) (int32, error) {
	w, err := word(data, index)
	if err != nil {
		return 0, err
	}
	negative := w[29]&0x80 != 0
	wantPrefix := byte(0)
	if negative {
		wantPrefix = 0xff
	}
	for _, value := range w[:29] {
		if value != wantPrefix {
			return 0, ErrMalformedLog
		}
	}
	value := int32(w[29])<<16 | int32(w[30])<<8 | int32(w[31])
	if negative {
		return value - 1<<24, nil
	}
	return value, nil
}

func signedWord(data []byte, index, bits int) (*big.Int, error) {
	w, err := word(data, index)
	if err != nil {
		return nil, err
	}
	bytesUsed := bits / 8
	start := 32 - bytesUsed
	negative := w[start]&0x80 != 0
	wantPrefix := byte(0)
	if negative {
		wantPrefix = 0xff
	}
	for _, value := range w[:start] {
		if value != wantPrefix {
			return nil, ErrMalformedLog
		}
	}
	value := new(big.Int).SetBytes(w[start:])
	if negative {
		value.Sub(value, new(big.Int).Lsh(big.NewInt(1), uint(bits)))
	}
	return value, nil
}

func addressWord(data []byte, index int) (common.Address, error) {
	w, err := word(data, index)
	if err != nil {
		return common.Address{}, err
	}
	for _, value := range w[:12] {
		if value != 0 {
			return common.Address{}, ErrMalformedLog
		}
	}
	return common.BytesToAddress(w[12:]), nil
}

func decodeInitialize(log gethtypes.Log) (PoolInitialized, error) {
	if err := validateLog(log, 4, 160); err != nil {
		return PoolInitialized{}, err
	}
	currency0, err := topicAddress(log.Topics[2])
	if err != nil {
		return PoolInitialized{}, err
	}
	currency1, err := topicAddress(log.Topics[3])
	if err != nil {
		return PoolInitialized{}, err
	}
	fee, err := uint24Word(log.Data, 0)
	if err != nil {
		return PoolInitialized{}, err
	}
	tickSpacing, err := int24Word(log.Data, 1)
	if err != nil || tickSpacing <= 0 {
		return PoolInitialized{}, ErrMalformedLog
	}
	hooks, err := addressWord(log.Data, 2)
	if err != nil {
		return PoolInitialized{}, err
	}
	sqrtPrice, err := uintWord(log.Data, 3, 160)
	if err != nil {
		return PoolInitialized{}, err
	}
	tick, err := int24Word(log.Data, 4)
	if err != nil {
		return PoolInitialized{}, err
	}
	result := PoolInitialized{PoolID: log.Topics[1], PoolKey: PoolKey{Currency0: currency0, Currency1: currency1, Fee: fee, TickSpacing: tickSpacing, Hooks: hooks}, SqrtPriceX96: sqrtPrice, Tick: tick}
	id, err := PoolID(result.PoolKey)
	if err != nil || id != result.PoolID {
		return PoolInitialized{}, ErrPoolIDMismatch
	}
	return result, nil
}

func decodeSwap(log gethtypes.Log, registration PoolRegistration) (Swap, error) {
	if err := validateLog(log, 3, 192); err != nil {
		return Swap{}, err
	}
	sender, err := topicAddress(log.Topics[2])
	if err != nil {
		return Swap{}, err
	}
	amount0, err := signedWord(log.Data, 0, 128)
	if err != nil {
		return Swap{}, err
	}
	amount1, err := signedWord(log.Data, 1, 128)
	if err != nil {
		return Swap{}, err
	}
	sqrtPrice, err := uintWord(log.Data, 2, 160)
	if err != nil {
		return Swap{}, err
	}
	liquidity, err := uintWord(log.Data, 3, 128)
	if err != nil {
		return Swap{}, err
	}
	tick, err := int24Word(log.Data, 4)
	if err != nil {
		return Swap{}, err
	}
	fee, err := uint24Word(log.Data, 5)
	if err != nil {
		return Swap{}, err
	}
	return Swap{PoolID: log.Topics[1], Sender: sender, Amount0: amount0, Amount1: amount1, SqrtPriceX96: sqrtPrice, Liquidity: liquidity, Tick: tick, Fee: fee, Registration: registration}, nil
}

func decodeLiquidityModified(log gethtypes.Log) (LiquidityModified, error) {
	if err := validateLog(log, 3, 128); err != nil {
		return LiquidityModified{}, err
	}
	sender, err := topicAddress(log.Topics[2])
	if err != nil {
		return LiquidityModified{}, err
	}
	lower, err := int24Word(log.Data, 0)
	if err != nil {
		return LiquidityModified{}, err
	}
	upper, err := int24Word(log.Data, 1)
	if err != nil || lower >= upper {
		return LiquidityModified{}, ErrMalformedLog
	}
	delta, err := signedWord(log.Data, 2, 256)
	if err != nil {
		return LiquidityModified{}, ErrMalformedLog
	}
	salt, err := word(log.Data, 3)
	if err != nil {
		return LiquidityModified{}, err
	}
	return LiquidityModified{PoolID: log.Topics[1], Sender: sender, TickLower: lower, TickUpper: upper, LiquidityDelta: delta, Salt: common.BytesToHash(salt)}, nil
}

func malformed(name string, err error) error {
	return fmt.Errorf("decode %s: %w", name, err)
}

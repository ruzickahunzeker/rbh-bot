package rbhparser

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func PoolID(key PoolKey) (common.Hash, error) {
	if key.Currency0 == key.Currency1 || key.Currency0.Cmp(key.Currency1) >= 0 || key.Fee > 0xffffff || key.TickSpacing <= 0 || key.TickSpacing > 8388607 {
		return common.Hash{}, ErrInvalidRegistration
	}
	var encoded [5 * abiWordSize]byte
	copy(encoded[12:abiWordSize], key.Currency0[:])
	copy(encoded[abiWordSize+12:2*abiWordSize], key.Currency1[:])
	putUint64(encoded[:], 2*abiWordSize, uint64(key.Fee))
	putUint64(encoded[:], 3*abiWordSize, uint64(key.TickSpacing))
	copy(encoded[4*abiWordSize+12:], key.Hooks[:])
	return crypto.Keccak256Hash(encoded[:]), nil
}

func putUint64(dst []byte, offset int, value uint64) {
	for i := 0; i < 8; i++ {
		dst[offset+abiWordSize-1-i] = byte(value)
		value >>= 8
	}
}

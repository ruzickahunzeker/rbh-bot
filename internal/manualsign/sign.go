// Package manualsign contains disconnected controlled-local signing primitives.
// It has no database, observer, HTTP/startup, nonce allocator or send capability.
package manualsign

import (
	"context"
	"errors"
	"math/big"
	"reflect"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
)

var ErrSigning = errors.New("controlled local MANUAL signing failed")

// Signer is structurally compatible with the existing local signer. Only a
// trusted synchronous, context-bounded local implementation belongs here.
type Signer interface {
	Address() common.Address
	SignTransaction(context.Context, *types.Transaction, *big.Int) (*types.Transaction, error)
}

func usable(v any) bool {
	if v == nil {
		return false
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan:
		return !r.IsNil()
	}
	return true
}

func Matches(s Signer, f manualartifact.Facts) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return usable(s) && f.Validate() == nil && s.Address() == common.HexToAddress(f.From)
}

// SignExact does NOT authorize invocation. The caller must own the fresh
// durable writer fence and first-call ownership for its entire synchronous
// duration. It never starts a goroutine, retries or returns plaintext bytes.
func SignExact(ctx context.Context, f manualartifact.Facts, s Signer, cipher manualartifact.Cipher, beforeInvoke func() error) (result manualartifact.Encrypted, err error) {
	defer func() {
		if recover() != nil {
			result, err = manualartifact.Encrypted{}, ErrSigning
		}
		if ctx != nil && ctx.Err() != nil {
			result, err = manualartifact.Encrypted{}, ctx.Err()
		}
	}()
	if ctx == nil || ctx.Err() != nil || beforeInvoke == nil || !usable(cipher) || !Matches(s, f) {
		return manualartifact.Encrypted{}, ErrSigning
	}
	tx, err := f.Transaction()
	if err != nil || ctx.Err() != nil {
		return manualartifact.Encrypted{}, ErrSigning
	}
	if err := beforeInvoke(); err != nil {
		return manualartifact.Encrypted{}, err
	}
	if ctx.Err() != nil {
		return manualartifact.Encrypted{}, ctx.Err()
	}
	signed, err := s.SignTransaction(ctx, tx, big.NewInt(4663))
	if err != nil || signed == nil || ctx.Err() != nil {
		return manualartifact.Encrypted{}, ErrSigning
	}
	raw, err := signed.MarshalBinary()
	defer clear(raw)
	if err != nil {
		return manualartifact.Encrypted{}, ErrSigning
	}
	v, err := manualartifact.Seal(raw, f, cipher)
	if err != nil {
		return manualartifact.Encrypted{}, ErrSigning
	}
	return v, nil
}

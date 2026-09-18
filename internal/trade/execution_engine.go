package trade

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	pons "github.com/0xfnzero/rbh-trade-sdk/adapters/pons"
	root "github.com/0xfnzero/rbh-trade-sdk/rbhtrade"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type PreBroadcastBackend interface {
	CurveBackend
	ExecutionBackend
}

type ExecutionHook func(stage string)

type ExecutionKernel struct {
	store   *Store
	backend PreBroadcastBackend
	signer  TransactionSigner
	cipher  ArtifactCipher
	now     func() time.Time
	hook    ExecutionHook
}

func NewExecutionKernel(store *Store, backend PreBroadcastBackend, signer TransactionSigner, artifactCipher ArtifactCipher) (*ExecutionKernel, error) {
	if store == nil || backend == nil || signer == nil || artifactCipher == nil || signer.Address() == (common.Address{}) || artifactCipher.KeyVersion() == "" {
		return nil, ErrInvalidRequest
	}
	return &ExecutionKernel{store: store, backend: backend, signer: signer, cipher: artifactCipher, now: time.Now}, nil
}

func (k *ExecutionKernel) SetHookForTest(hook ExecutionHook) { k.hook = hook }

func (k *ExecutionKernel) Prepare(ctx context.Context, operation, walletID string) (SignedArtifact, error) {
	if k == nil || ctx == nil || operation == "" || walletID == "" {
		return SignedArtifact{}, ErrInvalidRequest
	}
	if existing, found, err := k.store.LoadEncryptedArtifact(ctx, operation); err != nil {
		return SignedArtifact{}, err
	} else if found {
		artifact, err := k.verifyEncrypted(existing)
		artifact.Duplicate = true
		artifact.Recovered = true
		return artifact, err
	}
	request, dryRun, wallet, err := k.store.ExecutionCandidate(ctx, operation, walletID)
	if err != nil {
		return SignedArtifact{}, err
	}
	if wallet != k.signer.Address() || request.Intent.ID != operation || request.Intent.PolicyVersion == 0 {
		return SignedArtifact{}, ErrWrongSigner
	}
	executionChainID, err := k.backend.ExecutionChainID(ctx)
	if err != nil || executionChainID == nil || executionChainID.Cmp(new(big.Int).SetUint64(ChainID)) != 0 {
		return SignedArtifact{}, ErrArtifactIntegrity
	}
	block, err := k.backend.Snapshot(ctx)
	if err != nil || block.Number == 0 || block.Hash == (common.Hash{}) {
		return SignedArtifact{}, fmt.Errorf("%w: execution snapshot", ErrStaleState)
	}
	token := common.HexToAddress(request.Intent.Token)
	route, err := k.backend.ResolveCurve(ctx, token, block)
	if err != nil || route.Protocol != "pons-v2-curve" || route.Token != token || route.Curve == (common.Address{}) {
		return SignedArtifact{}, ErrUnknownRoute
	}
	if dryRun.Route.Protocol != "pons-v2-curve" || dryRun.Route.Token != route.Token || dryRun.Route.Curve != route.Curve {
		return SignedArtifact{}, ErrStaleState
	}
	call, amountIn, err := k.finalCall(ctx, request, route, wallet, block)
	if err != nil {
		return SignedArtifact{}, err
	}
	unsigned := UnsignedCall{From: wallet.Hex(), To: call.To.Hex(), Value: call.Value.String(), Data: "0x" + hex.EncodeToString(call.Data)}
	fee, err := k.backend.FeeParameters(ctx, wallet, unsigned, block)
	if err != nil || fee.GasLimit == 0 || fee.GasTipCap == nil || fee.GasFeeCap == nil || fee.GasFeeCap.Sign() <= 0 {
		return SignedArtifact{}, fmt.Errorf("%w: %v", ErrFeePolicy, err)
	}
	gasBudget := new(big.Int).Mul(new(big.Int).SetUint64(fee.GasLimit), fee.GasFeeCap)
	nativeBalance, err := k.backend.NativeBalance(ctx, wallet, block)
	if err != nil {
		return SignedArtifact{}, fmt.Errorf("%w: native balance", ErrReservation)
	}
	nativeRequired := new(big.Int).Set(gasBudget)
	inputAsset := token.Hex()
	if request.Intent.Kind == "copy_buy" {
		inputAsset = "native"
		nativeRequired.Add(nativeRequired, call.Value)
	} else {
		tokenBalance, balanceErr := k.backend.TokenBalance(ctx, token, wallet, block)
		if balanceErr != nil || tokenBalance.Cmp(amountIn) < 0 {
			return SignedArtifact{}, ErrReservation
		}
	}
	if nativeBalance.Cmp(nativeRequired) < 0 {
		return SignedArtifact{}, ErrReservation
	}
	if err := k.backend.VerifySnapshot(ctx, block); err != nil {
		return SignedArtifact{}, ErrStaleState
	}
	nonce, err := k.backend.PendingNonce(ctx, wallet)
	if err != nil {
		return SignedArtifact{}, fmt.Errorf("%w: %v", ErrNonceConflict, err)
	}
	reservation, err := k.store.ReserveExecution(ctx, operation, walletID, wallet, nonce, inputAsset, amountIn.String(), gasBudget.String(), k.now().UTC())
	if err != nil {
		return SignedArtifact{}, err
	}
	if k.hook != nil {
		k.hook("after_nonce_reservation_commit")
	}
	currentNonce, err := k.backend.PendingNonce(ctx, wallet)
	if err != nil || currentNonce != reservation.Nonce {
		_ = k.store.FreezeExecution(ctx, walletID, operation, "pending_nonce_changed_before_sign", k.now().UTC())
		return SignedArtifact{}, ErrNonceConflict
	}
	chainID := new(big.Int).SetUint64(ChainID)
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: chainID, Nonce: reservation.Nonce, GasTipCap: fee.GasTipCap, GasFeeCap: fee.GasFeeCap, Gas: fee.GasLimit, To: &call.To, Value: call.Value, Data: append([]byte(nil), call.Data...)})
	signed, err := k.signer.SignTransaction(ctx, tx, chainID)
	if err != nil {
		_ = k.store.FreezeExecution(ctx, walletID, operation, "signing_failed", k.now().UTC())
		return SignedArtifact{}, err
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		return SignedArtifact{}, err
	}
	artifact := SignedArtifact{AttemptID: reservation.AttemptID, Operation: operation, StepID: reservation.StepID, WalletID: walletID, KeyVersion: k.cipher.KeyVersion(), Nonce: nonce, TxHash: signed.Hash().Hex(), From: wallet.Hex(), To: call.To.Hex(), Value: call.Value.String(), Data: unsigned.Data, GasLimit: fee.GasLimit, GasTipCap: fee.GasTipCap.String(), GasFeeCap: fee.GasFeeCap.String()}
	if err := verifyRawArtifact(raw, artifact); err != nil {
		_ = k.store.FreezeExecution(ctx, walletID, operation, "artifact_integrity_failed", k.now().UTC())
		return SignedArtifact{}, err
	}
	ciphertext, encryptionNonce, err := k.cipher.Encrypt(raw, artifactAAD(operation, artifact.StepID, artifact.AttemptID))
	if err != nil {
		_ = k.store.FreezeExecution(ctx, walletID, operation, "artifact_encryption_failed", k.now().UTC())
		return SignedArtifact{}, fmt.Errorf("%w: %v", ErrEncryption, err)
	}
	if err := k.store.CommitSigned(ctx, reservation, artifact, ciphertext, encryptionNonce, k.now().UTC()); err != nil {
		if existing, found, loadErr := k.store.LoadEncryptedArtifact(ctx, operation); loadErr == nil && found {
			recovered, verifyErr := k.verifyEncrypted(existing)
			recovered.Duplicate = true
			recovered.Recovered = true
			return recovered, verifyErr
		}
		return SignedArtifact{}, err
	}
	if k.hook != nil {
		k.hook("after_signed_artifact_commit")
	}
	return artifact, nil
}

func (k *ExecutionKernel) finalCall(ctx context.Context, request DryRunRequest, route CurveRoute, wallet common.Address, block BlockRef) (root.Call, *big.Int, error) {
	requested, ok := new(big.Int).SetString(request.Intent.AmountValue, 10)
	if !ok || requested.Sign() <= 0 {
		return root.Call{}, nil, ErrInvalidRequest
	}
	amount := new(big.Int).Set(requested)
	if request.Intent.Kind == "copy_buy" {
		_, minimum, err := k.backend.QuoteBuy(ctx, route, amount, wallet, block, DefaultSlippageBPS)
		if err != nil || minimum == nil || minimum.Sign() <= 0 {
			return root.Call{}, nil, ErrInvalidRequest
		}
		call, err := pons.BuildBuy(route.Curve, amount, minimum, wallet, route.NativeQuote)
		return call, amount, err
	}
	balance, err := k.backend.TokenBalance(ctx, route.Token, wallet, block)
	if err != nil {
		return root.Call{}, nil, ErrReservation
	}
	amount.Mul(balance, requested)
	amount.Div(amount, big.NewInt(10_000))
	_, minimum, err := k.backend.QuoteSell(ctx, route, amount, block, DefaultSlippageBPS)
	if err != nil || minimum == nil || minimum.Sign() <= 0 || amount.Sign() <= 0 {
		return root.Call{}, nil, ErrInvalidRequest
	}
	call, err := pons.BuildSell(route.Curve, amount, minimum, wallet)
	return call, amount, err
}

func (k *ExecutionKernel) verifyEncrypted(stored encryptedArtifact) (SignedArtifact, error) {
	raw, err := k.cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
	if err != nil {
		return SignedArtifact{}, err
	}
	if err := verifyRawArtifact(raw, stored.SignedArtifact); err != nil {
		return SignedArtifact{}, err
	}
	return stored.SignedArtifact, nil
}

func verifyRawArtifact(raw []byte, artifact SignedArtifact) error {
	var tx types.Transaction
	if len(raw) == 0 || tx.UnmarshalBinary(raw) != nil || tx.Hash().Hex() != artifact.TxHash || tx.ChainId().Cmp(new(big.Int).SetUint64(ChainID)) != 0 || tx.Nonce() != artifact.Nonce || tx.To() == nil || tx.To().Hex() != common.HexToAddress(artifact.To).Hex() || tx.Value().String() != artifact.Value || "0x"+hex.EncodeToString(tx.Data()) != artifact.Data || tx.Gas() != artifact.GasLimit || tx.GasTipCap().String() != artifact.GasTipCap || tx.GasFeeCap().String() != artifact.GasFeeCap {
		return ErrArtifactIntegrity
	}
	sender, err := types.Sender(types.LatestSignerForChainID(new(big.Int).SetUint64(ChainID)), &tx)
	if err != nil || sender != common.HexToAddress(artifact.From) {
		return ErrWrongSigner
	}
	return nil
}

func IsExecutionFailClosed(err error) bool {
	return errors.Is(err, ErrWrongSigner) || errors.Is(err, ErrArtifactIntegrity) || errors.Is(err, ErrEncryption) || errors.Is(err, ErrUnknownKeyVersion) || errors.Is(err, ErrNonceConflict) || errors.Is(err, ErrWalletLaneBusy) || errors.Is(err, ErrReservation) || errors.Is(err, ErrFeePolicy) || errors.Is(err, ErrStaleState) || errors.Is(err, ErrUnknownRoute)
}

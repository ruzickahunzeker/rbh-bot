package tradeapi

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

type manualReadOnlyRecoveryRPC struct{}

type manualPendingReceiptBackend struct{}

func (manualPendingReceiptBackend) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return nil, nil
}
func (manualPendingReceiptBackend) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return nil, errors.New("header must not be queried before receipt")
}

type manualNoEffectResolver struct{}

func (manualNoEffectResolver) ResolvePositionEffect(context.Context, trade.SignedArtifact, *types.Receipt) (trade.PositionEffect, error) {
	return trade.PositionEffect{}, errors.New("effect must not be resolved before canonical receipt")
}

func (manualReadOnlyRecoveryRPC) TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error) {
	return nil, false, nil
}
func (manualReadOnlyRecoveryRPC) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	return nil, nil
}
func (manualReadOnlyRecoveryRPC) PendingNonceAt(context.Context, common.Address) (uint64, error) {
	return 23, nil
}

func TestManualSignedArtifactReadOnlyRecoveryQueryUsesOriginalAAD(t *testing.T) {
	database, store, h, order, scope, _, writer, signer, _ := localSigningFixture(t, "buy")
	if _, err := writer.sign(context.Background(), h.identity, order.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	var attempt, hash string
	if err := store.db.QueryRow(`SELECT a.id,a.tx_hash FROM transaction_attempts a JOIN execution_steps s ON s.id=a.step_id WHERE s.operation_id=?`, *order.OperationID).Scan(&attempt, &hash); err != nil {
		t.Fatal(err)
	}
	tradeStore, err := trade.NewStore(database)
	if err != nil {
		t.Fatal(err)
	}
	querier, err := trade.NewProductionRecoveryQuerier(tradeStore, writer.cipher, manualReadOnlyRecoveryRPC{})
	if err != nil {
		t.Fatal(err)
	}
	before := count(t, store.db, "transaction_submissions")
	evidence, err := querier.QueryRecovery(context.Background(), trade.ControlledRecoveryQuery{OperationID: *order.OperationID, AttemptID: attempt, TxHash: hash})
	if err != nil || evidence.TxFound || evidence.ReceiptFound || evidence.NonceState != "RESERVED_UNRESOLVED" || len(evidence.EvidenceHash) != 64 {
		t.Fatalf("manual query failed closed incorrectly: %+v %v", evidence, err)
	}
	if count(t, store.db, "transaction_submissions") != before || count(t, store.db, "receipt_observations") != 0 || signer.calls.Load() != 1 {
		t.Fatal("recovery query sent, signed or mutated durable submission state")
	}
	recovery, err := trade.NewRecoveryServiceWithCipher(tradeStore, writer.cipher, manualPendingReceiptBackend{}, manualNoEffectResolver{}, trade.CanonicalPolicy{Confirmations: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = recovery.Reconcile(context.Background(), *order.OperationID); !errors.Is(err, trade.ErrReceiptPending) {
		t.Fatalf("manual receipt recovery could not authenticate original artifact: %v", err)
	}
	if count(t, store.db, "receipt_observations") != 0 || count(t, store.db, "position_effects") != 0 || signer.calls.Load() != 1 {
		t.Fatal("receipt-pending recovery mutated economic state")
	}
}

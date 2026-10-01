package trade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	parser "github.com/0xfnzero/rbh-parser-sdk/rbhparser"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ReadOnlyRecoveryRPC deliberately exposes no transaction-send method.
const recoveryRPCTimeout = 10 * time.Second

type ReadOnlyRecoveryRPC struct{ eth *ethclient.Client }

func recoveryRPCContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, recoveryRPCTimeout)
}

func DialReadOnlyRecoveryRPC(ctx context.Context, rpcURL string) (*ReadOnlyRecoveryRPC, error) {
	if ctx == nil || rpcURL == "" {
		return nil, ErrInvalidRequest
	}
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, err
	}
	return &ReadOnlyRecoveryRPC{eth: client}, nil
}

func (r *ReadOnlyRecoveryRPC) Close() {
	if r != nil && r.eth != nil {
		r.eth.Close()
	}
}

func (r *ReadOnlyRecoveryRPC) TransactionReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	if r == nil || r.eth == nil {
		return nil, ErrInvalidRequest
	}
	rpcCtx, cancel := recoveryRPCContext(ctx)
	defer cancel()
	receipt, err := r.eth.TransactionReceipt(rpcCtx, hash)
	if errors.Is(err, ethereum.NotFound) {
		return nil, nil
	}
	return receipt, err
}

func (r *ReadOnlyRecoveryRPC) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	if r == nil || r.eth == nil {
		return nil, ErrInvalidRequest
	}
	rpcCtx, cancel := recoveryRPCContext(ctx)
	defer cancel()
	return r.eth.HeaderByNumber(rpcCtx, number)
}

func (r *ReadOnlyRecoveryRPC) TransactionByHash(ctx context.Context, hash common.Hash) (*types.Transaction, bool, error) {
	if r == nil || r.eth == nil {
		return nil, false, ErrInvalidRequest
	}
	rpcCtx, cancel := recoveryRPCContext(ctx)
	defer cancel()
	tx, pending, err := r.eth.TransactionByHash(rpcCtx, hash)
	if errors.Is(err, ethereum.NotFound) {
		return nil, false, nil
	}
	return tx, pending, err
}

func (r *ReadOnlyRecoveryRPC) PendingNonceAt(ctx context.Context, address common.Address) (uint64, error) {
	if r == nil || r.eth == nil || address == (common.Address{}) {
		return 0, ErrInvalidRequest
	}
	rpcCtx, cancel := recoveryRPCContext(ctx)
	defer cancel()
	return r.eth.PendingNonceAt(rpcCtx, address)
}

func (r *ReadOnlyRecoveryRPC) CheckRecoveryHealth(ctx context.Context) error {
	if r == nil || r.eth == nil {
		return ErrInvalidRequest
	}
	rpcCtx, cancel := recoveryRPCContext(ctx)
	defer cancel()
	chainID, err := r.eth.ChainID(rpcCtx)
	if err != nil || chainID == nil || chainID.Cmp(new(big.Int).SetUint64(ChainID)) != 0 {
		return ErrCanaryRuntimeRejected
	}
	return nil
}

type recoveryQueryRPC interface {
	TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error)
	TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error)
	PendingNonceAt(context.Context, common.Address) (uint64, error)
}

type ProductionRecoveryQuerier struct {
	store  *Store
	cipher ArtifactCipher
	rpc    recoveryQueryRPC
	now    func() time.Time
}

func NewProductionRecoveryQuerier(store *Store, artifactCipher ArtifactCipher, rpc recoveryQueryRPC) (*ProductionRecoveryQuerier, error) {
	if store == nil || artifactCipher == nil || rpc == nil {
		return nil, ErrInvalidRequest
	}
	return &ProductionRecoveryQuerier{store: store, cipher: artifactCipher, rpc: rpc, now: time.Now}, nil
}

func (q *ProductionRecoveryQuerier) QueryRecovery(ctx context.Context, query ControlledRecoveryQuery) (ControlledRecoveryEvidence, error) {
	if q == nil || ctx == nil || query.OperationID == "" || query.AttemptID == "" || len(common.FromHex(query.TxHash)) != common.HashLength {
		return ControlledRecoveryEvidence{}, ErrInvalidRequest
	}
	stored, found, err := q.store.LoadEncryptedArtifact(ctx, query.OperationID)
	if err != nil || !found || stored.AttemptID != query.AttemptID || !strings.EqualFold(stored.TxHash, query.TxHash) {
		return ControlledRecoveryEvidence{}, ErrArtifactIntegrity
	}
	raw, err := q.cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
	if err != nil {
		return ControlledRecoveryEvidence{}, err
	}
	defer clear(raw)
	if err = verifyRawArtifact(raw, stored.SignedArtifact); err != nil {
		return ControlledRecoveryEvidence{}, err
	}
	hash := common.HexToHash(stored.TxHash)
	tx, _, err := q.rpc.TransactionByHash(ctx, hash)
	if err != nil {
		return ControlledRecoveryEvidence{}, err
	}
	receipt, err := q.rpc.TransactionReceipt(ctx, hash)
	if err != nil {
		return ControlledRecoveryEvidence{}, err
	}
	txFound, receiptFound := tx != nil, receipt != nil
	nonceState := "PROPAGATED"
	if !txFound && !receiptFound {
		nonce, nonceErr := q.rpc.PendingNonceAt(ctx, common.HexToAddress(stored.From))
		if nonceErr != nil {
			return ControlledRecoveryEvidence{}, nonceErr
		}
		nonceState = "RESERVED_UNRESOLVED"
		if nonce > stored.Nonce {
			nonceState = "NONCE_ADVANCED"
		}
	}
	stamp := q.now().UTC()
	payload, _ := json.Marshal(struct {
		TxHash       string `json:"tx_hash"`
		TxFound      bool   `json:"tx_found"`
		ReceiptFound bool   `json:"receipt_found"`
		NonceState   string `json:"nonce_state"`
		ObservedAt   string `json:"observed_at"`
	}{strings.ToLower(stored.TxHash), txFound, receiptFound, nonceState, stamp.Format(time.RFC3339Nano)})
	digest := sha256.Sum256(payload)
	return ControlledRecoveryEvidence{TxFound: txFound, ReceiptFound: receiptFound, NonceState: nonceState, EvidenceHash: hex.EncodeToString(digest[:]), ObservedAt: stamp}, nil
}

// PonsCurveEffectResolver derives the position delta exclusively from the
// canonical Pons v2 Curve receipt event bound to the durable dry-run route.
type PonsCurveEffectResolver struct{ store *Store }

func NewPonsCurveEffectResolver(store *Store) (*PonsCurveEffectResolver, error) {
	if store == nil {
		return nil, ErrInvalidRequest
	}
	return &PonsCurveEffectResolver{store: store}, nil
}

func (r *PonsCurveEffectResolver) ResolvePositionEffect(ctx context.Context, artifact SignedArtifact, receipt *types.Receipt) (PositionEffect, error) {
	if r == nil || ctx == nil || artifact.Operation == "" || receipt == nil || receipt.Status != types.ReceiptStatusSuccessful {
		return PositionEffect{}, ErrArtifactIntegrity
	}
	if receipt.TxHash != (common.Hash{}) && !strings.EqualFold(receipt.TxHash.Hex(), artifact.TxHash) {
		return PositionEffect{}, ErrArtifactIntegrity
	}
	dryRun, found, err := r.store.Result(ctx, artifact.Operation)
	if err != nil || !found || dryRun.Status != "success" || dryRun.Route.Protocol != "pons-v2-curve" || dryRun.Route.Curve == (common.Address{}) || dryRun.Route.Token == (common.Address{}) {
		return PositionEffect{}, ErrArtifactIntegrity
	}
	p := parser.New()
	if err = p.Registry().RegisterCurve(parser.CurveRegistration{Curve: dryRun.Route.Curve, Protocol: parser.ProtocolPonsV2, Token: dryRun.Route.Token, Quote: dryRun.Route.QuoteToken}); err != nil {
		return PositionEffect{}, err
	}
	events, err := p.ParseReceipt(receipt)
	if err != nil {
		return PositionEffect{}, err
	}
	var delta *big.Int
	for _, event := range events {
		if event.Protocol != parser.ProtocolPonsV2 || event.Log.Address != dryRun.Route.Curve {
			continue
		}
		trade, ok := event.Data.(parser.CurveTrade)
		wallet := common.HexToAddress(artifact.From)
		if !ok || trade.AmountIn == nil || trade.AmountOut == nil || trade.AmountIn.Sign() <= 0 || trade.AmountOut.Sign() <= 0 || trade.BuyerOrSeller != wallet || trade.Recipient != wallet {
			return PositionEffect{}, ErrArtifactIntegrity
		}
		var candidate *big.Int
		switch {
		case dryRun.Parameters.Direction == "buy" && event.Kind == parser.EventCurveBuy:
			candidate = new(big.Int).Set(trade.AmountOut)
		case dryRun.Parameters.Direction == "sell" && event.Kind == parser.EventCurveSell:
			candidate = new(big.Int).Neg(new(big.Int).Set(trade.AmountIn))
		default:
			continue
		}
		if delta != nil {
			return PositionEffect{}, fmt.Errorf("%w: multiple matching curve effects", ErrArtifactIntegrity)
		}
		delta = candidate
	}
	if delta == nil || delta.Sign() == 0 {
		return PositionEffect{}, ErrArtifactIntegrity
	}
	return PositionEffect{Asset: dryRun.Route.Token.Hex(), Delta: delta.String()}, nil
}

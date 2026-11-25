package trade

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/permit2"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
	"github.com/ruzickahunzeker/rbh-bot/internal/registry"
)

var (
	ErrLiveExecutionDisabled = errors.New("live execution disabled (live_enabled=false); signing and broadcast refused")
	ErrHoneypotBlocked       = errors.New("honeypot guard rejected token before signing")
	ErrApproveNotConfirmed   = errors.New("approve step did not reach CONFIRMED_CONTINUITY; sell step remains locked")

	erc20ExecABI = mustParseExecABI(`[
		{"name":"allowance","type":"function","stateMutability":"view","inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"outputs":[{"name":"","type":"uint256"}]},
		{"name":"approve","type":"function","stateMutability":"nonpayable","inputs":[{"name":"spender","type":"address"},{"name":"value","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]}
	]`)
)

func mustParseExecABI(src string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(src))
	if err != nil {
		panic(err)
	}
	return parsed
}

// ChainExecutionBackend abstracts read-only state queries, gas/nonce lookup, and receipt waiting
// so MultiChainExecutor works identically on live RPC nodes and local EVM fork harnesses.
type ChainExecutionBackend interface {
	ports.ReadOnlyRPC
	LatestBlock(ctx context.Context) (uint64, common.Hash, error)
	PendingNonce(ctx context.Context, wallet common.Address) (uint64, error)
	SuggestFees(ctx context.Context) (maxFeeWei, maxTipWei *big.Int, err error)
	WaitReceipt(ctx context.Context, txHash common.Hash) (*gethtypes.Receipt, error)
}

// RawTxBroadcaster abstracts MultiRelayBroadcaster for testability and private_only enforcement.
type RawTxBroadcaster interface {
	BroadcastRawTx(ctx context.Context, txHash common.Hash, rawSignedTx []byte) (RelayBroadcastResult, error)
}

// HoneypotGuard optionally simulates buy+sell before signing any live trade.
type HoneypotGuard interface {
	CheckBeforeBuy(ctx context.Context, route ports.ResolvedRoute, amountInWei *big.Int) error
}

// MultiChainOrderRequest describes a single V2 multi-chain buy or sell execution.
type MultiChainOrderRequest struct {
	OperationID string
	ProtocolID  string
	Token       common.Address
	Side        ports.Side
	AmountInRaw *big.Int
	SlippageBps uint64
	DeadlineSec int64
}

// MultiChainOrderResult summarizes the executed step(s), tx hashes, and decoded receipt effects.
type MultiChainOrderResult struct {
	OperationID      string
	ApproveTxHash    common.Hash
	ApprovalTxHashes []common.Hash
	SwapTxHash       common.Hash
	Route            ports.ResolvedRoute
	Quote            ports.QuoteResult
	Effects          ports.DecodedEffects
}

// MultiChainExecutor orchestrates V2 multi-chain Resolve -> HoneypotGuard -> Quote ->
// BuildUnsigned -> (optional Approve step 0) -> Sign -> CommitPreSendBatch -> Broadcast ->
// WaitReceipt -> DecodeEffects behind a strict live_enabled gate.
type MultiChainExecutor struct {
	liveEnabled bool
	reg         *registry.Registry
	store       *Store
	backend     ChainExecutionBackend
	broadcaster RawTxBroadcaster
	honeypot    HoneypotGuard
	privKey     *ecdsa.PrivateKey
	wallet      common.Address
}

func NewMultiChainExecutor(
	liveEnabled bool,
	reg *registry.Registry,
	store *Store,
	backend ChainExecutionBackend,
	broadcaster RawTxBroadcaster,
	honeypot HoneypotGuard,
	privKey *ecdsa.PrivateKey,
) (*MultiChainExecutor, error) {
	if reg == nil || store == nil || backend == nil || broadcaster == nil {
		return nil, errors.New("incomplete multichain executor dependencies")
	}
	var wallet common.Address
	if privKey != nil {
		wallet = crypto.PubkeyToAddress(privKey.PublicKey)
	}
	return &MultiChainExecutor{
		liveEnabled: liveEnabled,
		reg:         reg,
		store:       store,
		backend:     backend,
		broadcaster: broadcaster,
		honeypot:    honeypot,
		privKey:     privKey,
		wallet:      wallet,
	}, nil
}

func (e *MultiChainExecutor) WalletAddress() common.Address { return e.wallet }

// Execute runs the full V2 multi-chain trade lifecycle, failing closed if live_enabled=false.
func (e *MultiChainExecutor) Execute(ctx context.Context, req MultiChainOrderRequest, now time.Time) (MultiChainOrderResult, error) {
	if !e.liveEnabled {
		return MultiChainOrderResult{}, ErrLiveExecutionDisabled
	}
	if e.privKey == nil || e.wallet == (common.Address{}) {
		return MultiChainOrderResult{}, errors.New("no signing key configured for live execution")
	}
	if req.OperationID == "" || req.Token == (common.Address{}) || req.AmountInRaw == nil || req.AmountInRaw.Sign() <= 0 {
		return MultiChainOrderResult{}, errors.New("invalid multichain order request")
	}

	adapter, err := e.reg.Adapter(req.ProtocolID)
	if err != nil {
		return MultiChainOrderResult{}, err
	}
	blkNum, blkHash, err := e.backend.LatestBlock(ctx)
	if err != nil {
		return MultiChainOrderResult{}, fmt.Errorf("latest block: %w", err)
	}
	route, err := adapter.Resolve(ctx, e.backend, req.Token, blkNum, blkHash)
	if err != nil {
		return MultiChainOrderResult{}, fmt.Errorf("resolve route: %w", err)
	}

	// Pre-trade honeypot simulation BEFORE allocating nonce or signing anything.
	if req.Side == ports.SideBuy && e.honeypot != nil {
		if err := e.honeypot.CheckBeforeBuy(ctx, route, req.AmountInRaw); err != nil {
			return MultiChainOrderResult{}, fmt.Errorf("%w: %v", ErrHoneypotBlocked, err)
		}
	}

	qReq := ports.QuoteRequest{
		Route:          route,
		Side:           req.Side,
		AmountInRaw:    new(big.Int).Set(req.AmountInRaw),
		SlippageBps:    req.SlippageBps,
		Recipient:      e.wallet,
		ContractExpiry: now.UTC().Unix() + maxInt64(req.DeadlineSec, 120),
	}
	quote, err := adapter.Quote(ctx, e.backend, qReq)
	if err != nil {
		return MultiChainOrderResult{}, fmt.Errorf("quote: %w", err)
	}
	unsignedSwap, err := adapter.BuildUnsigned(qReq, quote)
	if err != nil {
		return MultiChainOrderResult{}, fmt.Errorf("build unsigned: %w", err)
	}

	var approvals []permit2.Approval
	if req.Side == ports.SideSell && unsignedSwap.Spender != (common.Address{}) {
		if adapter.Describe().AllowanceModel == ports.AllowancePermit2 {
			state, readErr := permit2.Read(ctx, e.backend, e.wallet, req.Token, unsignedSwap.Spender, blkHash)
			if readErr != nil {
				return MultiChainOrderResult{}, fmt.Errorf("read Permit2 allowances: %w", readErr)
			}
			approvals, err = state.Approvals(req.Token, unsignedSwap.Spender, req.AmountInRaw, uint64(qReq.ContractExpiry))
		} else {
			var allowance *big.Int
			allowance, err = e.readAllowance(ctx, req.Token, e.wallet, unsignedSwap.Spender, blkHash)
			if err == nil && allowance.Cmp(req.AmountInRaw) < 0 {
				var cd []byte
				cd, err = erc20ExecABI.Pack("approve", unsignedSwap.Spender, req.AmountInRaw)
				approvals = append(approvals, permit2.Approval{To: req.Token, Calldata: cd, Kind: "approve_exact"})
			}
		}
		if err != nil {
			return MultiChainOrderResult{}, fmt.Errorf("prepare approvals: %w", err)
		}
	}

	res := MultiChainOrderResult{OperationID: req.OperationID, Route: route, Quote: quote}
	stepID := func(i int) string { return fmt.Sprintf("%s:step:%d", req.OperationID, i) }
	steps := make([]StepSpec, 0, len(approvals)+1)
	for i, approval := range approvals {
		steps = append(steps, StepSpec{StepID: stepID(i), StepIndex: i, StepKind: approval.Kind})
	}
	swapStepID := stepID(len(approvals))
	steps = append(steps, StepSpec{StepID: swapStepID, StepIndex: len(approvals), StepKind: string(req.Side)})
	if err := e.store.RegisterStepChain(ctx, req.OperationID, steps, now); err != nil {
		return res, fmt.Errorf("register step chain: %w", err)
	}
	for i, approval := range approvals {
		unlocked, unlockErr := e.store.CanUnlockStep(ctx, req.OperationID, i)
		if unlockErr != nil || !unlocked {
			return res, ErrApproveNotConfirmed
		}
		approveTxHash, approveRcpt, sendErr := e.signCommitBroadcastWait(
			ctx, req.OperationID, stepID(i), "initial", approval.To, big.NewInt(0), approval.Calldata, 80_000, now,
		)
		res.ApproveTxHash = approveTxHash
		res.ApprovalTxHashes = append(res.ApprovalTxHashes, approveTxHash)
		if sendErr != nil || approveRcpt == nil || approveRcpt.Status != gethtypes.ReceiptStatusSuccessful {
			_ = e.store.UpdateStepStatus(ctx, stepID(i), "FAILED", now)
			return res, fmt.Errorf("%w: %v", ErrApproveNotConfirmed, sendErr)
		}
		if err := e.store.UpdateStepStatus(ctx, stepID(i), "CONFIRMED_CONTINUITY", now); err != nil {
			return res, err
		}
	}
	if unlocked, err := e.store.CanUnlockStep(ctx, req.OperationID, len(approvals)); err != nil || !unlocked {
		return res, ErrApproveNotConfirmed
	}
	if len(approvals) > 0 && adapter.Describe().AllowanceModel == ports.AllowancePermit2 {
		_, approvalBlock, err := e.backend.LatestBlock(ctx)
		if err != nil {
			return res, err
		}
		state, err := permit2.Read(ctx, e.backend, e.wallet, req.Token, unsignedSwap.Spender, approvalBlock)
		if err != nil {
			return res, fmt.Errorf("verify confirmed Permit2 allowances: %w", err)
		}
		if state.Effective(uint64(qReq.ContractExpiry)).Cmp(req.AmountInRaw) < 0 {
			return res, ErrApproveNotConfirmed
		}
	}

	gasLimit := quote.EstimatedGas
	if gasLimit == 0 {
		gasLimit = 300_000
	}
	swapTxHash, swapRcpt, err := e.signCommitBroadcastWait(
		ctx, req.OperationID, swapStepID, "initial",
		unsignedSwap.To, unsignedSwap.Value, unsignedSwap.Calldata, gasLimit, now,
	)
	res.SwapTxHash = swapTxHash
	if err != nil {
		return res, err
	}
	if swapRcpt.Status != gethtypes.ReceiptStatusSuccessful {
		_ = e.store.UpdateStepStatus(ctx, swapStepID, "REVERTED", now)
		return res, fmt.Errorf("swap tx %s reverted on-chain", swapTxHash.Hex())
	}
	if err := e.store.UpdateStepStatus(ctx, swapStepID, "CONFIRMED_CONTINUITY", now); err != nil {
		return res, err
	}
	eff, err := adapter.DecodeEffects(route, e.wallet, req.Side, swapRcpt)
	if err != nil {
		return res, fmt.Errorf("decode receipt effects: %w", err)
	}
	res.Effects = eff
	return res, nil
}

// CancelStuckNonce signs, commits, broadcasts, and confirms a same-nonce 0-value self-transfer
// Cancel tx with bumped gas fees, then cleanly unfreezes the nonce generation (AT-07).
func (e *MultiChainExecutor) CancelStuckNonce(
	ctx context.Context,
	operationID, stepID string,
	stuckNonce uint64,
	bumpedMaxFeeWei, bumpedMaxTipWei *big.Int,
	now time.Time,
) (common.Hash, error) {
	if !e.liveEnabled {
		return common.Hash{}, ErrLiveExecutionDisabled
	}
	if e.privKey == nil {
		return common.Hash{}, errors.New("no signing key configured")
	}
	chainID := e.reg.Chain().ChainID
	unsignedCancel, err := BuildSameNonceCancelTx(chainID, e.wallet, stuckNonce, bumpedMaxFeeWei, bumpedMaxTipWei)
	if err != nil {
		return common.Hash{}, err
	}
	signer := gethtypes.LatestSignerForChainID(new(big.Int).SetUint64(chainID))
	signedTx, err := gethtypes.SignTx(unsignedCancel, signer, e.privKey)
	if err != nil {
		return common.Hash{}, err
	}
	rawBytes, err := signedTx.MarshalBinary()
	if err != nil {
		return common.Hash{}, err
	}
	artHash := sha256.Sum256(rawBytes)
	attemptID := fmt.Sprintf("%s:cancel:%d:%d", stepID, stuckNonce, now.UTC().UnixNano())
	if err := e.store.CommitPreSendBatch(ctx, StepAttemptRecord{
		AttemptID:         attemptID,
		StepID:            stepID,
		OperationID:       operationID,
		WalletAddress:     e.wallet,
		Nonce:             stuckNonce,
		AttemptKind:       "cancel",
		TxHash:            signedTx.Hash(),
		ArtifactHash:      hex.EncodeToString(artHash[:]),
		MaxFeePerGasWei:   bumpedMaxFeeWei.String(),
		MaxPriorityFeeWei: bumpedMaxTipWei.String(),
		State:             "SIGNED",
	}, now); err != nil {
		return common.Hash{}, err
	}
	if _, err := e.broadcaster.BroadcastRawTx(ctx, signedTx.Hash(), rawBytes); err != nil {
		return signedTx.Hash(), err
	}
	rcpt, err := e.backend.WaitReceipt(ctx, signedTx.Hash())
	if err != nil {
		return signedTx.Hash(), err
	}
	if rcpt.Status != gethtypes.ReceiptStatusSuccessful {
		return signedTx.Hash(), errors.New("cancel tx reverted")
	}
	// After cancel confirms at stuckNonce, finalized on-chain nonce is at least stuckNonce+1.
	if _, err := e.store.TryUnfreezeAmbiguousNonce(ctx, e.wallet, stuckNonce, stuckNonce+1, false, signedTx.Hash(), now); err != nil {
		return signedTx.Hash(), err
	}
	return signedTx.Hash(), nil
}

func (e *MultiChainExecutor) signCommitBroadcastWait(
	ctx context.Context,
	operationID, stepID, attemptKind string,
	to common.Address,
	value *big.Int,
	calldata []byte,
	gasLimit uint64,
	now time.Time,
) (common.Hash, *gethtypes.Receipt, error) {
	nonce, err := e.backend.PendingNonce(ctx, e.wallet)
	if err != nil {
		return common.Hash{}, nil, fmt.Errorf("pending nonce: %w", err)
	}
	maxFee, maxTip, err := e.backend.SuggestFees(ctx)
	if err != nil {
		return common.Hash{}, nil, fmt.Errorf("suggest fees: %w", err)
	}
	chainID := e.reg.Chain().ChainID
	if value == nil {
		value = big.NewInt(0)
	}

	unsigned := gethtypes.NewTx(&gethtypes.DynamicFeeTx{
		ChainID:   new(big.Int).SetUint64(chainID),
		Nonce:     nonce,
		GasTipCap: maxTip,
		GasFeeCap: maxFee,
		Gas:       gasLimit,
		To:        &to,
		Value:     new(big.Int).Set(value),
		Data:      calldata,
	})
	signer := gethtypes.LatestSignerForChainID(new(big.Int).SetUint64(chainID))
	signedTx, err := gethtypes.SignTx(unsigned, signer, e.privKey)
	if err != nil {
		return common.Hash{}, nil, fmt.Errorf("sign tx: %w", err)
	}
	rawBytes, err := signedTx.MarshalBinary()
	if err != nil {
		return common.Hash{}, nil, err
	}
	artHash := sha256.Sum256(rawBytes)
	attemptID := fmt.Sprintf("%s:%s:%d", stepID, attemptKind, nonce)

	// Durable pre-send commit BEFORE any network broadcast.
	if err := e.store.CommitPreSendBatch(ctx, StepAttemptRecord{
		AttemptID:         attemptID,
		StepID:            stepID,
		OperationID:       operationID,
		WalletAddress:     e.wallet,
		Nonce:             nonce,
		AttemptKind:       attemptKind,
		TxHash:            signedTx.Hash(),
		ArtifactHash:      hex.EncodeToString(artHash[:]),
		MaxFeePerGasWei:   maxFee.String(),
		MaxPriorityFeeWei: maxTip.String(),
		State:             "SIGNED",
	}, now); err != nil {
		return common.Hash{}, nil, fmt.Errorf("commit pre-send batch: %w", err)
	}

	bRes, bErr := e.broadcaster.BroadcastRawTx(ctx, signedTx.Hash(), rawBytes)
	switch bRes.MergedState {
	case MergedSubmitted:
		_, _ = e.store.db.ExecContext(ctx,
			`UPDATE trade_step_attempts SET state = 'SUBMITTED', updated_at = ? WHERE attempt_id = ?`,
			now.UTC().Format(time.RFC3339Nano), attemptID)
	case MergedKnownUnsent:
		_, _ = e.store.db.ExecContext(ctx,
			`UPDATE trade_step_attempts SET state = 'KNOWN_UNSENT', updated_at = ? WHERE attempt_id = ?`,
			now.UTC().Format(time.RFC3339Nano), attemptID)
		return signedTx.Hash(), nil, bErr
	default:
		_, _ = e.store.db.ExecContext(ctx,
			`UPDATE trade_step_attempts SET state = 'BROADCAST_UNKNOWN', updated_at = ? WHERE attempt_id = ?`,
			now.UTC().Format(time.RFC3339Nano), attemptID)
		return signedTx.Hash(), nil, bErr
	}

	rcpt, err := e.backend.WaitReceipt(ctx, signedTx.Hash())
	if err != nil {
		return signedTx.Hash(), nil, err
	}
	_, _ = e.store.db.ExecContext(ctx,
		`UPDATE trade_step_attempts SET state = 'FINALIZED', updated_at = ? WHERE attempt_id = ?`,
		now.UTC().Format(time.RFC3339Nano), attemptID)
	return signedTx.Hash(), rcpt, nil
}

func (e *MultiChainExecutor) readAllowance(ctx context.Context, token, owner, spender common.Address, blockHash common.Hash) (*big.Int, error) {
	data, err := erc20ExecABI.Pack("allowance", owner, spender)
	if err != nil {
		return nil, err
	}
	raw, err := e.backend.CallContractAt(ctx, token, data, blockHash)
	if err != nil {
		return nil, err
	}
	out, err := erc20ExecABI.Unpack("allowance", raw)
	if err != nil || len(out) == 0 {
		return nil, errors.New("invalid allowance output")
	}
	val, ok := out[0].(*big.Int)
	if !ok || val == nil {
		return big.NewInt(0), nil
	}
	return val, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

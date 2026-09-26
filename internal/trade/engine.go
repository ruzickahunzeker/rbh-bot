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
)

type Engine struct {
	store   *Store
	backend CurveBackend
	now     func() time.Time
}

func NewEngine(store *Store, backend CurveBackend) (*Engine, error) {
	if store == nil || backend == nil {
		return nil, ErrInvalidRequest
	}
	return &Engine{store: store, backend: backend, now: time.Now}, nil
}

func (e *Engine) DryRun(ctx context.Context, request DryRunRequest) (DryRunResult, error) {
	if e == nil || e.store == nil || e.backend == nil || ctx == nil {
		return DryRunResult{}, ErrInvalidRequest
	}
	admission, err := e.store.Admit(ctx, request, e.now().UTC())
	if err != nil {
		return DryRunResult{}, err
	}
	if admission.Terminal != nil {
		return *admission.Terminal, nil
	}
	result := DryRunResult{OperationID: admission.OperationID, StepID: admission.StepID, Status: "fail_closed", Duplicate: admission.Duplicate}
	if err := e.execute(ctx, request, admission, &result); err != nil {
		result.FailureCode = failureCode(err)
		result.FailureDetail = err.Error()
		if persistErr := e.store.FailClosed(ctx, result, e.now().UTC()); persistErr != nil {
			return DryRunResult{}, fmt.Errorf("dry-run failure %v; persist: %w", err, persistErr)
		}
		return result, nil
	}
	if err := request.CheckTTL(e.now().UTC()); err != nil {
		result.FailureCode = failureCode(err)
		result.FailureDetail = err.Error()
		if persistErr := e.store.FailClosed(ctx, result, e.now().UTC()); persistErr != nil {
			return DryRunResult{}, fmt.Errorf("dry-run expiry %v; persist: %w", err, persistErr)
		}
		return result, nil
	}
	result.Status = "success"
	if err := e.store.Complete(ctx, result, e.now().UTC()); err != nil {
		return DryRunResult{}, err
	}
	return result, nil
}

func (e *Engine) execute(ctx context.Context, request DryRunRequest, admission Admission, result *DryRunResult) error {
	if err := request.CheckTTL(e.now().UTC()); err != nil {
		return err
	}
	block, err := e.backend.Snapshot(ctx)
	if err != nil || block.Number == 0 || block.Hash == (common.Hash{}) {
		return fmt.Errorf("%w: snapshot: %v", ErrStaleState, err)
	}
	result.Block = block
	token := common.HexToAddress(request.Intent.Token)
	route, err := e.backend.ResolveCurve(ctx, token, block)
	if err != nil {
		if errors.Is(err, ErrUnknownRoute) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrUnknownRoute, err)
	}
	if route.Protocol != "pons-v2-curve" || route.Token != token || route.Curve == (common.Address{}) {
		return ErrUnknownRoute
	}
	result.Route = route
	requested, ok := new(big.Int).SetString(request.Intent.AmountValue, 10)
	if !ok {
		return ErrInvalidRequest
	}
	amountIn := new(big.Int).Set(requested)
	var quoted, minimum *big.Int
	var call root.Call
	direction := "buy"
	if request.Intent.Kind == "copy_buy" {
		quoted, minimum, err = e.backend.QuoteBuy(ctx, route, amountIn, admission.Wallet, block, DefaultSlippageBPS)
		if err == nil {
			call, err = pons.BuildBuy(route.Curve, amountIn, minimum, admission.Wallet, route.NativeQuote)
		}
	} else {
		direction = "sell"
		balance, balanceErr := e.backend.TokenBalance(ctx, token, admission.Wallet, block)
		if balanceErr != nil {
			return fmt.Errorf("read sell balance: %w", balanceErr)
		}
		amountIn.Mul(balance, requested)
		amountIn.Div(amountIn, big.NewInt(10_000))
		if amountIn.Sign() <= 0 {
			return ErrInvalidRequest
		}
		quoted, minimum, err = e.backend.QuoteSell(ctx, route, amountIn, block, DefaultSlippageBPS)
		if err == nil {
			call, err = pons.BuildSell(route.Curve, amountIn, minimum, admission.Wallet)
		}
	}
	if err != nil {
		return fmt.Errorf("%w: execution parameters: %v", ErrInvalidRequest, err)
	}
	if quoted == nil || minimum == nil || quoted.Sign() <= 0 || minimum.Sign() <= 0 {
		return ErrInvalidRequest
	}
	parameters := ExecutionParameters{Direction: direction, AmountIn: amountIn.String(), MinimumOut: minimum.String(), QuotedOut: quoted.String(), SlippageBPS: DefaultSlippageBPS, Recipient: admission.Wallet.Hex()}
	unsigned := UnsignedCall{From: admission.Wallet.Hex(), To: call.To.Hex(), Value: call.Value.String(), Data: "0x" + hex.EncodeToString(call.Data)}
	result.Parameters = parameters
	result.UnsignedCall = unsigned
	if err := e.store.MarkRunning(ctx, admission.OperationID, admission.StepID, route, parameters, unsigned, block, request.Intent.PolicyVersion, request.Intent.ExpiresAt, e.now().UTC()); err != nil {
		return err
	}
	returnData, err := e.backend.Simulate(ctx, admission.Wallet, call.To, call.Value, call.Data, block)
	if err != nil {
		return err
	}
	if err := e.backend.VerifySnapshot(ctx, block); err != nil {
		return fmt.Errorf("%w: %v", ErrStaleState, err)
	}
	if err := request.CheckTTL(e.now().UTC()); err != nil {
		return err
	}
	result.ReturnData = "0x" + hex.EncodeToString(returnData)
	return nil
}

func (e *Engine) Recover(ctx context.Context) ([]DryRunResult, error) {
	requests, err := e.store.Recoverable(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]DryRunResult, 0, len(requests))
	for _, request := range requests {
		if ttlErr := request.CheckTTL(e.now().UTC()); ttlErr != nil {
			result := DryRunResult{OperationID: operationID(request), StepID: stepID(operationID(request)), Status: "fail_closed", FailureCode: failureCode(ttlErr), FailureDetail: ttlErr.Error()}
			if err := e.store.FailClosed(ctx, result, e.now().UTC()); err != nil {
				return results, err
			}
			results = append(results, result)
			continue
		}
		result, err := e.DryRun(ctx, request)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func failureCode(err error) string {
	switch {
	case errors.Is(err, ErrUnknownRoute):
		return "UNKNOWN_ROUTE"
	case errors.Is(err, ErrStaleState):
		return "STALE_STATE"
	case errors.Is(err, ErrSimulationReverted):
		return "SIMULATION_REVERTED"
	case errors.Is(err, ErrInvalidRequest):
		return "INVALID_PARAMETERS"
	case errors.Is(err, ErrTTLExpired):
		return "TTL_EXPIRED"
	case errors.Is(err, ErrTTLUnverifiable):
		return "TTL_UNVERIFIABLE"
	default:
		return "DRY_RUN_FAILED"
	}
}

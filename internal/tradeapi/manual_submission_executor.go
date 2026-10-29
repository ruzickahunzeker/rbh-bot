package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

// Private local-harness capability, deliberately incompatible with a public
// RawBroadcaster. No production implementation or HTTP/startup constructor.
// Implementations are trusted synchronous primitives: honor the deadline and
// do not retain raw bytes or launch asynchronous work after returning.
type manualFirstTransport interface {
	submitExact(context.Context, []byte) (manualTransportResult, error)
}

type manualTransportResult struct {
	txHash string
	// Only a trusted transport's explicit non-propagation result qualifies.
	deterministicUnsent bool
}

type manualSubmissionExecutor struct {
	verifier   *manualRuntimeVerifier
	store      *trade.Store
	transport  manualFirstTransport
	timeout    time.Duration
	checkpoint func(string) error
}

func (e *manualSubmissionExecutor) at(stage string) error {
	if e.checkpoint != nil {
		return e.checkpoint(stage)
	}
	return nil
}

// execute does not adopt an earlier claim. Each invocation must win a fresh
// ISSUED-permit claim; a failed/ambiguous prior call is query-only durable work.
func (e *manualSubmissionExecutor) execute(ctx context.Context, id Identity, request, permitID string) (sub trade.SubmissionRecord, err error) {
	if e == nil || e.verifier == nil || e.store == nil || e.transport == nil || ctx == nil {
		return sub, ErrInvalid
	}
	v := e.verifier
	if v.store == nil || v.timeSource == nil || v.identity == nil || v.decryptor == nil || v.observer == nil || v.unhealthy.Load() {
		return sub, ErrInvalid
	}
	if err = runtimeStopFirst(ctx, v.store.db); err != nil {
		return sub, err
	}
	if !validID(permitID) || e.timeout <= 0 || e.timeout > time.Minute {
		return sub, ErrInvalid
	}
	clock := &manualRuntimeClock{source: v.timeSource, maximumError: v.profile.maximumError}
	defer func() {
		if recover() != nil {
			v.unhealthy.Store(true)
			err = rejectManualInput("SUBMISSION_PRIMITIVE_FAILED")
		}
		if clock.invalid {
			v.unhealthy.Store(true)
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			err = errors.Join(err, latchManualRuntimeClock(cleanup, v.store, v.profile.process.scope.Environment, clock.faultReason, clock.faultWall))
		}
	}()
	p, err := v.prepare(ctx, id, request, clock)
	if err != nil {
		return sub, err
	}
	f := p.gate.projection.Foundation.Facts
	r := trade.ManualFirstSubmissionRequest{
		Gate:     trade.CanaryGateRequest{OperationID: f.OperationID, AttemptID: f.AttemptID, AuthorizationID: f.AuthorizationID, AuthorizationEpoch: f.AuthorizationEpoch, WalletID: f.WalletID, ChainID: trade.ChainID, PolicyVersion: f.PolicyVersion},
		PermitID: permitID, ArtifactHash: p.gate.projection.Foundation.RawHash, TxHash: p.gate.projection.Foundation.TxHash,
		Lease: trade.SubmissionLeaseFence{Environment: f.Environment, HolderID: v.profile.process.holder, Epoch: uint64(v.profile.process.leaseEpoch)},
	}
	r.Gate.Deployment.Environment, r.Gate.Deployment.BuildSHA = f.Environment, f.BuildSHA
	r.Gate.Deployment.ReleaseID, r.Gate.Deployment.DeploymentID = f.ReleaseID, f.DeploymentID
	recheck := func(claimed bool) trade.ManualSubmissionRecheck {
		return func(c context.Context, tx *sql.Tx) error {
			_, _, checkErr := v.finalCheckPhase(c, tx, id, request, clock, p, claimed)
			return checkErr
		}
	}
	now, err := submissionSampleTime(ctx, clock)
	if err != nil {
		return sub, err
	}
	sub, err = e.store.BeginManualFirstSubmission(ctx, r, now, recheck(false))
	if err != nil {
		return sub, err
	}
	if err = e.at("after_claim"); err != nil {
		return sub, err
	}
	p, err = v.preparePhase(ctx, id, request, clock, true)
	if err != nil {
		return sub, err
	}
	now, err = submissionSampleTime(ctx, clock)
	if err != nil {
		return sub, err
	}
	if err = e.store.MarkManualFirstSendIntent(ctx, r, sub, now, recheck(true)); err != nil {
		return sub, err
	}
	defer e.store.DiscardManualFirstInvocation(sub.ID)
	if err = e.at("after_intent"); err != nil {
		return sub, err
	}
	// Fresh read-only runtime observation after the durable ambiguity marker.
	// Failure leaves durable ambiguous work; it never recreates send permission.
	p, err = v.preparePhase(ctx, id, request, clock, true)
	if err != nil {
		return sub, err
	}
	now, err = submissionSampleTime(ctx, clock)
	if err != nil {
		return sub, err
	}
	err = e.store.WithManualFirstSubmissionOutcome(ctx, r, sub, now, recheck(true), func(c context.Context, tx *sql.Tx) (out trade.ManualSubmissionOutcome, callErr error) {
		var envelope string
		if callErr = tx.QueryRowContext(c, `SELECT envelope_json FROM manual_first_submission_signed_artifact_complete WHERE operation_id=?`, f.OperationID).Scan(&envelope); callErr != nil {
			return out, callErr
		}
		sealed, _, decodeErr := manualartifact.DecodeEnvelopeMetadata(envelope, f)
		if decodeErr != nil {
			return out, decodeErr
		}
		callErr = withManualSubmissionBytes(c, sealed, f, v.decryptor, func(raw []byte) error {
			// Decryption is synchronous but potentially slow. Recheck under the
			// same writer immediately before the sole bounded invocation.
			if checkErr := recheck(true)(c, tx); checkErr != nil {
				return checkErr
			}
			if checkErr := e.at("before_invocation"); checkErr != nil {
				return checkErr
			}
			bounded, cancel := context.WithTimeout(c, e.timeout)
			defer cancel()
			result, sendErr := e.transport.submitExact(bounded, raw)
			if sendErr != nil || bounded.Err() != nil {
				return errors.Join(sendErr, bounded.Err())
			}
			switch {
			case result.deterministicUnsent && result.txHash == "":
				out = trade.ManualSubmissionOutcome{State: "known_unsent", FailureClass: "KNOWN_UNSENT_RETAINED"}
			case !result.deterministicUnsent && result.txHash == r.TxHash:
				out = trade.ManualSubmissionOutcome{State: "submitted", RPCHash: result.txHash}
			default:
				out = trade.ManualSubmissionOutcome{State: "manual_resolution", FailureClass: "TRANSACTION_IDENTITY_CONTRADICTION"}
			}
			return e.at("after_invocation")
		})
		return out, callErr
	})
	readErr := v.store.db.QueryRowContext(ctx, `SELECT state,COALESCE(rpc_hash,''),COALESCE(failure_class,''),COALESCE(failure_detail,'') FROM transaction_submissions WHERE id=?`, sub.ID).Scan(&sub.State, &sub.RPCHash, &sub.FailureClass, &sub.FailureDetail)
	err = errors.Join(err, readErr)
	return sub, err
}

func submissionSampleTime(ctx context.Context, clock *manualRuntimeClock) (time.Time, error) {
	s, err := clock.read(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return canonicalManualTime(s.wall)
}

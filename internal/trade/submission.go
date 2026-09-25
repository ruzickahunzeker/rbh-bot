package trade

import (
	"context"
	"errors"
	"time"
)

var (
	ErrBroadcastAmbiguous = errors.New("broadcast outcome unknown")
	ErrBroadcastRejected  = errors.New("broadcast deterministically rejected")
	ErrRPCHashMismatch    = errors.New("RPC transaction hash mismatch")
)

type RawBroadcaster interface {
	SendRawTransaction(context.Context, []byte) (string, error)
}

type SubmissionService struct {
	store       *Store
	kernel      *ExecutionKernel
	broadcaster RawBroadcaster
	now         func() time.Time
	hook        func(string)
}

func (s *SubmissionService) SetHookForTest(h func(string)) { s.hook = h }

func NewSubmissionService(store *Store, kernel *ExecutionKernel, b RawBroadcaster) (*SubmissionService, error) {
	if store == nil || kernel == nil || b == nil {
		return nil, ErrInvalidRequest
	}
	return &SubmissionService{store: store, kernel: kernel, broadcaster: b, now: time.Now}, nil
}

func (s *SubmissionService) Submit(ctx context.Context, operation string) (SubmissionRecord, error) {
	return s.submit(ctx, operation, false)
}

func (s *SubmissionService) ReplayUnknown(ctx context.Context, operation string, policyAllows bool) (SubmissionRecord, error) {
	if !policyAllows {
		return SubmissionRecord{}, ErrBroadcastAmbiguous
	}
	return s.submit(ctx, operation, true)
}

func (s *SubmissionService) submit(ctx context.Context, operation string, replayUnknown bool) (SubmissionRecord, error) {
	stored, found, err := s.store.LoadEncryptedArtifact(ctx, operation)
	if err != nil || !found {
		return SubmissionRecord{}, ErrArtifactIntegrity
	}
	raw, err := s.kernel.cipher.Decrypt(stored.KeyVersion, stored.Ciphertext, stored.EncryptionNonce, artifactAAD(stored.Operation, stored.StepID, stored.AttemptID))
	if err != nil {
		return SubmissionRecord{}, err
	}
	defer clear(raw)
	if err = verifyRawArtifact(raw, stored.SignedArtifact); err != nil {
		return SubmissionRecord{}, err
	}
	if latest, ok, e := s.store.LatestSubmission(ctx, stored.AttemptID); e != nil {
		return SubmissionRecord{}, e
	} else if ok && latest.State == "submitted" {
		return latest, nil
	} else if ok && latest.State == "submitting" {
		_ = s.store.FinishSubmission(ctx, stored.SignedArtifact, latest, "broadcast_unknown", "", "restart_with_inflight_submission", "send outcome was not durably recorded", s.now())
		latest.State = "broadcast_unknown"
		return latest, ErrBroadcastAmbiguous
	} else if ok && latest.State == "broadcast_unknown" && !replayUnknown {
		return latest, ErrBroadcastAmbiguous
	} else if replayUnknown && (!ok || latest.State != "broadcast_unknown") {
		return SubmissionRecord{}, ErrInvalidRequest
	}
	sub, err := s.store.BeginSubmission(ctx, stored.SignedArtifact, replayUnknown, s.now())
	if err != nil {
		return SubmissionRecord{}, err
	}
	if s.hook != nil {
		s.hook("before_send")
	}
	hash, sendErr := s.broadcaster.SendRawTransaction(ctx, raw)
	if s.hook != nil {
		s.hook("after_send_before_outcome_commit")
	}
	if sendErr != nil {
		if errors.Is(sendErr, ErrBroadcastRejected) {
			_ = s.store.FinishSubmission(ctx, stored.SignedArtifact, sub, "manual_resolution", "", "deterministic_rejection", sendErr.Error(), s.now())
			return sub, sendErr
		}
		_ = s.store.FinishSubmission(ctx, stored.SignedArtifact, sub, "broadcast_unknown", "", "ambiguous_transport", sendErr.Error(), s.now())
		return sub, ErrBroadcastAmbiguous
	}
	if hash != stored.TxHash {
		_ = s.store.FinishSubmission(ctx, stored.SignedArtifact, sub, "manual_resolution", hash, "hash_mismatch", ErrRPCHashMismatch.Error(), s.now())
		return sub, ErrRPCHashMismatch
	}
	if err = s.store.FinishSubmission(ctx, stored.SignedArtifact, sub, "submitted", hash, "", "", s.now()); err != nil {
		return sub, ErrBroadcastAmbiguous
	}
	sub.State = "submitted"
	sub.RPCHash = hash
	return sub, nil
}

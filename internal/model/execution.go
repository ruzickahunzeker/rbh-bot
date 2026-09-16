package model

import "time"

type OperationStatus string

const (
	OperationCreated             OperationStatus = "created"
	OperationAdmitted            OperationStatus = "admitted"
	OperationExecuting           OperationStatus = "executing"
	OperationSucceeded           OperationStatus = "succeeded"
	OperationFailed              OperationStatus = "failed"
	OperationBlocked             OperationStatus = "blocked"
	OperationNeedsReconciliation OperationStatus = "needs_reconciliation"
)

type AttemptStatus string

const (
	AttemptPrepared         AttemptStatus = "prepared"
	AttemptSigned           AttemptStatus = "signed"
	AttemptBroadcasting     AttemptStatus = "broadcasting"
	AttemptBroadcastUnknown AttemptStatus = "broadcast_unknown"
	AttemptSeen             AttemptStatus = "seen"
	AttemptIncludedSuccess  AttemptStatus = "included_success"
	AttemptIncludedRevert   AttemptStatus = "included_revert"
	AttemptOrphaned         AttemptStatus = "orphaned"
	AttemptReplaced         AttemptStatus = "replaced"
)

type Operation struct {
	ID             string
	ChainID        uint64
	WalletID       string
	IdempotencyKey string
	Kind           string
	Status         OperationStatus
	CreatedAt      time.Time
}

type ExecutionStep struct {
	ID          string
	OperationID string
	Index       uint32
	Kind        string
}

type TransactionAttempt struct {
	ID        string
	StepID    string
	Nonce     uint64
	TxHash    string
	Status    AttemptStatus
	CreatedAt time.Time
}

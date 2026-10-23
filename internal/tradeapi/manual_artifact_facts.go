package tradeapi

import (
	"context"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
)

// SELECT-only original CLAIMED-phase projection for future B3 orchestration.
// Not a fresh control/TTL gate or signer permission. No signer/cipher/backend
// is owned here and no HTTP or production constructor calls this helper.
func loadManualArtifactFacts(ctx context.Context, query queryRow, id Identity, request string, scope manualAdmissionScope) (manualartifact.Facts, error) {
	var zero manualartifact.Facts
	if ctx == nil || query == nil || !scope.valid() {
		return zero, ErrInvalid
	}
	if err := ownManualNonceRequest(ctx, query, id, request); err != nil {
		return zero, err
	}
	q, err := readManualNonceClaim(ctx, query, request)
	if err != nil {
		return zero, err
	}
	c, err := verifyManualNonceClaim(ctx, query, q, id, scope)
	if err != nil {
		return zero, err
	}
	b, err := scanManualExecutionBinding(query.QueryRowContext(ctx, `SELECT `+manualExecutionBindingColumns+` FROM manual_execution_bindings WHERE operation_id=?`, q.Operation))
	if err != nil {
		return zero, err
	}
	f := manualartifact.Facts{Version: manualartifact.Version, OperationID: c.OperationID, RequestID: c.RequestID, ExecutionStepID: q.Step, AttemptID: q.Attempt, WalletID: c.WalletID,
		ReservationID: q.Reservation, ContractHash: c.BindingHash, ChainID: c.ChainID, From: c.Output.From, To: c.Output.To, Value: c.Output.Value, Data: c.Output.Data,
		Nonce: q.Nonce, GasLimit: c.Output.GasLimit, GasFeeCap: c.Output.GasFeeCap, GasTipCap: c.Output.GasTipCap, PolicyVersion: c.PolicyVersion, PolicyHash: c.PolicyHash,
		QuoteBlockNumber: c.Output.Resolution.Token.Block.Number, QuoteBlockHash: c.Output.Resolution.Token.Block.Hash, RuntimeCodeHash: c.Output.Resolution.Token.CurveRuntimeCodeHash,
		ExpiresAt: c.ExpiresAt, ClaimedAt: q.CheckedAt, ClaimObservationHash: q.ObservationHash, DeadlineCapability: c.DeadlineCapability, ContractDeadline: c.ContractDeadline,
		AuthorizationID: b.Scope.AuthorizationID, AuthorizationEpoch: b.Scope.AuthorizationEpoch, ManualPolicyID: b.Scope.ManualPolicyID,
		ManualPolicyHash: b.PolicyHash, GateEvidenceSetHash: b.GateHash, Environment: b.Scope.Environment, BuildSHA: b.Scope.BuildSHA, ReleaseID: b.Scope.ReleaseID, DeploymentID: b.Scope.DeploymentID}
	if err = f.Validate(); err != nil {
		return zero, err
	}
	return f, nil
}

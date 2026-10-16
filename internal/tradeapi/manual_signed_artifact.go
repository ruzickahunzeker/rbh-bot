package tradeapi

import (
	"context"
	"strings"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

// SELECT-only, package-private history. No command writer, signer, observer,
// HTTP or production constructor is connected in this phase-contract batch.
type manualSignedArtifactHistory struct {
	OperationID, AttemptID, FactsHash, TxHash, RawSHA256, KeyVersion, CompletedAt, Status string
	ExecutionAuthorized, SigningAuthorized, SendAuthorized                                bool
}

func loadManualSignedArtifactHistory(ctx context.Context, q queryRow, id Identity, request string, scope manualAdmissionScope, cipher manualartifact.Cipher) (manualSignedArtifactHistory, error) {
	var zero manualSignedArtifactHistory
	if ctx == nil || q == nil || !scope.valid() {
		return zero, ErrInvalid
	}
	if err := ownManualNonceRequest(ctx, q, id, request); err != nil {
		return zero, err
	}
	claim, err := readManualNonceClaim(ctx, q, request)
	if err != nil {
		return zero, err
	}
	cmd, err := scanManualCommand(q.QueryRowContext(ctx, `SELECT `+manualCommandColumns+` FROM manual_admission_commands WHERE operation_id=?`, claim.Operation))
	if err != nil {
		return zero, err
	}
	c, err := verifyManualCommandTx(ctx, q, cmd)
	if err != nil {
		return zero, err
	}
	if c.PrincipalID != id.PrincipalID || c.WalletID != id.WalletID || !strings.EqualFold(c.WalletAddress, id.WalletAddress) {
		return zero, ErrNotFound
	}
	b, err := scanManualExecutionBinding(q.QueryRowContext(ctx, `SELECT `+manualExecutionBindingColumns+` FROM manual_execution_bindings WHERE operation_id=?`, claim.Operation))
	if err != nil || b.Scope != scope || b != executionBindingFor(c, cmd, b.CheckedAt) {
		return zero, ErrConflict
	}
	if _, err := manualnonce.Verify(claim.ObservationRaw, claim.ObservationHash, claim.Nonce, manualNonceFacts(c, b), claim.CheckedAt); err != nil {
		return zero, ErrConflict
	}
	unit := manualrisk.Unit{ChainID: c.Input.ChainID, Asset: c.Input.Asset, Decimals: c.Input.Decimals, Kind: manualrisk.TradeInput}
	if _, _, err := verifyManualAuthorizationUsage(ctx, q, scope.AuthorizationID, unit); err != nil {
		return zero, err
	}
	var factsRaw, envelopeRaw string
	if err := q.QueryRowContext(ctx, `SELECT facts_json,envelope_json FROM manual_signed_artifact_complete WHERE operation_id=?`, claim.Operation).Scan(&factsRaw, &envelopeRaw); err != nil {
		return zero, err
	}
	f, err := manualartifact.Decode(factsRaw)
	if err != nil {
		return zero, err
	}
	// SQL phase completeness verifies the full original projection; independently
	// retain exact request/owner/deployment identity before any cipher invocation.
	if f.OperationID != c.OperationID || f.RequestID != request || f.ContractHash != c.BindingHash || f.AttemptID != claim.Attempt || f.Nonce != claim.Nonce ||
		f.ExecutionStepID != b.ExecutionStep || f.WalletID != c.WalletID || f.ReservationID != cmd.Reservation || f.ExpiresAt != c.ExpiresAt ||
		f.AuthorizationID != scope.AuthorizationID || f.AuthorizationEpoch != scope.AuthorizationEpoch || f.ManualPolicyID != scope.ManualPolicyID ||
		f.Environment != scope.Environment || f.BuildSHA != scope.BuildSHA || f.ReleaseID != scope.ReleaseID || f.DeploymentID != scope.DeploymentID {
		return zero, ErrConflict
	}
	v, completed, err := manualartifact.DecodeEnvelopeMetadata(envelopeRaw, f)
	if err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	verified, err := manualartifact.Open(v, f, cipher)
	// The synchronous cipher has no context parameter. Cancellation during
	// authenticated verification must still suppress the historical result.
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err != nil {
		return zero, err
	}
	return manualSignedArtifactHistory{OperationID: f.OperationID, AttemptID: f.AttemptID, FactsHash: verified.FactsHash, TxHash: verified.TxHash,
		RawSHA256: verified.RawSHA256, KeyVersion: v.KeyVersion, CompletedAt: completed, Status: "HISTORICAL_VERIFIED_ARTIFACT_NOT_PERMISSION"}, nil
}

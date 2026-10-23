package tradeapi

import (
	"context"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
)

// Read-only ambiguity history. This is not an invocation API or a signer
// permit. There is intentionally no code to insert a marker or call a signer
// in this B3-B1 foundation; controlled direct SQL tests exercise the schema.
type manualSigningIntentHistory struct {
	OperationID, OwnerID, FactsHash, MarkedAt, Status      string
	Facts                                                  manualartifact.Facts
	ExecutionAuthorized, SigningAuthorized, SendAuthorized bool
}

func loadManualSigningIntentHistory(ctx context.Context, q queryRow, id Identity, request string, scope manualAdmissionScope) (manualSigningIntentHistory, error) {
	var zero manualSigningIntentHistory
	facts, err := loadManualArtifactFacts(ctx, q, id, request, scope)
	if err != nil {
		return zero, err
	}
	var result manualSigningIntentHistory
	var storedJSON string
	if err = q.QueryRowContext(ctx, `SELECT operation_id,owner_id,facts_json,facts_hash,checked_at FROM manual_signing_intent_complete WHERE operation_id=?`, facts.OperationID).Scan(
		&result.OperationID, &result.OwnerID, &storedJSON, &result.FactsHash, &result.MarkedAt); err != nil {
		return zero, err
	}
	canonical, digest, err := facts.Canonical()
	if err != nil || canonical != storedJSON || digest != result.FactsHash {
		return zero, ErrConflict
	}
	result.Facts = facts
	result.Status = "SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION"
	return result, nil
}

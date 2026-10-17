package tradeapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// manualFirstBroadcastIssuer is private controlled-local composition. It
// issues metadata only; no consumer, signer, nonce allocator or transport is
// reachable from this type.
type manualFirstBroadcastIssuer struct{ verifier *manualRuntimeVerifier }

type manualFirstBroadcastPermit struct {
	ID, OperationID, AttemptID, AuthorizationID, ArtifactHash, TxHash string
	AuthorizationEpoch                                                uint64
	IssuedAt, ExpiresAt                                               time.Time
}

type manualFirstBroadcastSourceMeta struct {
	Authorization struct {
		ExpiresAt string `json:"expires_at"`
	} `json:"authorization"`
	Lease struct {
		ExpiresAt string `json:"expires_at"`
	} `json:"submission_lease"`
}

func (i *manualFirstBroadcastIssuer) issue(ctx context.Context, id Identity, request, attemptID, artifactHash, txHash string) (permit manualFirstBroadcastPermit, err error) {
	if i == nil || i.verifier == nil || ctx == nil || !id.valid() || !validID(request) || !validID(attemptID) || !manualHex(artifactHash, 64) || len(txHash) != 66 || !strings.HasPrefix(txHash, "0x") || !manualHex(strings.TrimPrefix(txHash, "0x"), 64) {
		return permit, ErrInvalid
	}
	clock := &manualRuntimeClock{source: i.verifier.timeSource, maximumError: i.verifier.profile.maximumError}
	p, err := i.verifier.prepare(ctx, id, request, clock)
	if err != nil {
		return permit, err
	}
	conn, err := i.verifier.store.db.Conn(ctx)
	if err != nil {
		return permit, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return permit, err
	}
	defer conn.ExecContext(context.Background(), `ROLLBACK`)
	g, sample, err := i.verifier.finalCheck(ctx, conn, id, request, clock, p)
	if err != nil {
		return permit, err
	}
	f := g.projection.Foundation.Facts
	if f.AttemptID != attemptID || g.projection.Foundation.RawHash != artifactHash || g.projection.Foundation.TxHash != txHash {
		return permit, rejectManualInput("FIRST_BROADCAST_ARTIFACT_MISMATCH")
	}
	var meta manualFirstBroadcastSourceMeta
	if err = json.Unmarshal([]byte(g.source), &meta); err != nil {
		return permit, err
	}
	operationExpiry, err := canonicalManualTime(f.ExpiresAt)
	if err != nil {
		return permit, err
	}
	authExpiry, err := canonicalManualTime(meta.Authorization.ExpiresAt)
	if err != nil {
		return permit, err
	}
	leaseExpiry, err := canonicalManualTime(meta.Lease.ExpiresAt)
	if err != nil {
		return permit, err
	}
	issued, err := canonicalManualTime(sample.wall)
	if err != nil {
		return permit, err
	}
	expires := operationExpiry
	if authExpiry.Before(expires) {
		expires = authExpiry
	}
	if leaseExpiry.Before(expires) {
		expires = leaseExpiry
	}
	window := issued.Add(i.verifier.profile.maxAge)
	if window.Before(expires) {
		expires = window
	}
	if !issued.Before(expires) {
		return permit, rejectManualInput("FIRST_BROADCAST_PERMIT_EXPIRED")
	}
	var prior manualFirstBroadcastPermit
	var priorIssued, priorExpires string
	if err = conn.QueryRowContext(ctx, `SELECT id,operation_id,attempt_id,authorization_id,authorization_epoch,artifact_hash,tx_hash,issued_at,expires_at FROM manual_first_broadcast_issuances WHERE operation_id=? AND attempt_id=?`, f.OperationID, attemptID).Scan(&prior.ID, &prior.OperationID, &prior.AttemptID, &prior.AuthorizationID, &prior.AuthorizationEpoch, &prior.ArtifactHash, &prior.TxHash, &priorIssued, &priorExpires); err == nil {
		prior.IssuedAt, err = canonicalManualTime(priorIssued)
		if err == nil {
			prior.ExpiresAt, err = canonicalManualTime(priorExpires)
		}
		if err != nil || prior.AuthorizationID != f.AuthorizationID || prior.AuthorizationEpoch != f.AuthorizationEpoch || prior.ArtifactHash != artifactHash || prior.TxHash != txHash {
			return permit, ErrConflict
		}
		if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
			return permit, err
		}
		return prior, nil
	} else if err != sql.ErrNoRows {
		return permit, err
	}
	if err = insertManualClockEvent(ctx, conn, i.verifier.profile.process.scope.Environment, g.clock.generation+1, sample.wall, 0, "", nil, sample.wall); err != nil {
		return permit, err
	}
	issuanceID := "manual-first-permit:" + attemptID
	if _, err = conn.ExecContext(ctx, `INSERT INTO manual_first_broadcast_issuances(id,operation_id,attempt_id,authorization_id,authorization_epoch,source_json,source_hash,process_hash,observation_hash,clock_generation,artifact_hash,tx_hash,issued_at,expires_at,state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, issuanceID, f.OperationID, attemptID, f.AuthorizationID, f.AuthorizationEpoch, g.source, g.sourceHash, hash([]byte(encode(i.verifier.profile.process))), p.observation.Digest(), g.clock.generation+1, artifactHash, txHash, issued.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano), "ISSUED"); err != nil {
		return permit, err
	}
	checked := issued.UTC()
	controlUpdated, parseErr := canonicalManualTime(g.projection.Control.UpdatedAt)
	if parseErr != nil {
		controlUpdated = checked
	}
	snapshotID := "manual-first-gate:" + attemptID
	if _, err = conn.ExecContext(ctx, `INSERT INTO canary_runtime_gate_snapshots(id,operation_id,attempt_id,stage,authorization_id,authorization_epoch,build_sha,release_id,deployment_id,policy_version,policy_hash,gate_evidence_set_hash,emergency_stop_revision,decision,reason_code,checked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, snapshotID, f.OperationID, attemptID, "FIRST_BROADCAST", f.AuthorizationID, f.AuthorizationEpoch, strings.ToLower(f.BuildSHA), f.ReleaseID, f.DeploymentID, f.PolicyVersion, strings.ToLower(f.PolicyHash), strings.ToLower(f.GateEvidenceSetHash), controlUpdated.UnixNano(), "PASS", "CANARY_GATE_PASS", checked.Format(time.RFC3339Nano)); err != nil {
		return permit, err
	}
	permitID := "manual-first-permit:" + attemptID
	if _, err = conn.ExecContext(ctx, `INSERT INTO canary_send_permits(id,operation_id,attempt_id,authorization_id,authorization_epoch,purpose,artifact_hash,tx_hash,query_evidence_hash,queried_at,state,issued_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?, ?,?)`, permitID, f.OperationID, attemptID, f.AuthorizationID, f.AuthorizationEpoch, "FIRST_BROADCAST", artifactHash, txHash, nil, nil, "ISSUED", checked.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano)); err != nil {
		return permit, err
	}
	if err = ctx.Err(); err != nil {
		return permit, err
	}
	if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
		return permit, err
	}
	if err = ctx.Err(); err != nil {
		return permit, err
	}
	return manualFirstBroadcastPermit{ID: permitID, OperationID: f.OperationID, AttemptID: attemptID, AuthorizationID: f.AuthorizationID, AuthorizationEpoch: f.AuthorizationEpoch, ArtifactHash: artifactHash, TxHash: txHash, IssuedAt: checked, ExpiresAt: expires}, nil
}

var _ queryRow = (*sql.Tx)(nil)

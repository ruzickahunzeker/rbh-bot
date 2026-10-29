package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
)

// Invocation-local samples, shared bundle failure flag. No independent signer,
// state machine or durable queue. All economic transitions remain in the old
// signing writer; this helper only checks inputs and appends existing039 events.
type manualPreSignTrust struct {
	bundle          *manualTrustComposition
	ctx             context.Context
	q               queryRow
	clock           manualRuntimeClock
	cleanupErr      error
	artifactWritten bool // Only the original writer sets this after its INSERT.
	observation     chainquery.SubmissionRuntimeObservation
	observedStarted manualRuntimeSample
}

// External read-only work happens after releasing the initial snapshot and
// before the signing writer. Final checks only verify this bounded evidence.
func (t *manualPreSignTrust) observe(g manualSigningGate) error {
	if _, err := t.read(); err != nil {
		return err
	}
	t.observedStarted = t.clock.previous
	ctx, cancel := context.WithTimeout(t.ctx, t.bundle.profile.queryTimeout)
	defer cancel()
	f := g.facts
	o, err := t.bundle.runtime.observe(ctx, manualRuntimeChainFacts{curve: f.To, runtimeHash: f.RuntimeCodeHash, quote: chainquery.Block{Number: f.QuoteBlockNumber, Hash: f.QuoteBlockHash}})
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	t.observation = o
	return nil
}

func (t *manualPreSignTrust) read() (time.Time, error) {
	// Stop is inspected before a provider/measurement call or shared latch.
	if err := runtimeStopFirst(t.ctx, t.q); err != nil {
		return time.Time{}, err
	}
	if t.bundle.health.unhealthy.Load() {
		return time.Time{}, rejectManualInput("SIGNING_TRUST_UNHEALTHY")
	}
	if t.bundle.cipher.KeyVersion() != t.bundle.keyVersion || t.bundle.decryptor.KeyVersion() != t.bundle.keyVersion {
		return time.Time{}, rejectManualInput("SIGNING_TRUST_KEY_VERSION_CHANGED")
	}
	ctx, cancel := context.WithTimeout(t.ctx, t.bundle.profile.queryTimeout)
	defer cancel()
	// Measure before sampling: hashing the running image consumes time and must
	// not leave a pre-measurement timestamp masquerading as the final sample.
	p, err := t.bundle.identity.identify(ctx)
	if err != nil || ctx.Err() != nil || p != t.bundle.profile.process {
		return time.Time{}, rejectManualInput("SIGNING_PROCESS_IDENTITY_UNVERIFIABLE")
	}
	s, err := t.clock.read(ctx)
	if err != nil {
		if t.clock.invalid {
			t.bundle.health.unhealthy.Store(true)
		}
		return time.Time{}, err
	}
	if s.provider != t.bundle.profile.process.provider {
		t.clock.invalid = true
		t.clock.faultReason = "CLOCK_UNVERIFIABLE"
		t.bundle.health.unhealthy.Store(true)
		return time.Time{}, rejectManualInput("SIGNING_CLOCK_PROVIDER_MISMATCH")
	}
	state, err := readManualRuntimeClock(t.ctx, t.q, t.bundle.profile.process.scope.Environment)
	if err != nil {
		return time.Time{}, err
	}
	if err = checkManualClockFloor(state, s); err != nil {
		t.clock.invalid = true
		t.clock.faultReason = "CLOCK_REVERSAL"
		t.clock.faultWall = s.wall
		t.bundle.health.unhealthy.Store(true)
		return time.Time{}, err
	}
	return canonicalManualTime(s.wall)
}

func (t *manualPreSignTrust) check(g manualSigningGate, raw, digest, notBefore string) error {
	if t.bundle.health.unhealthy.Load() {
		return rejectManualInput("SIGNING_TRUST_UNHEALTHY")
	}
	scope := t.bundle.profile.process.scope
	// Reuse durable source verification, not caller-supplied profile/security
	// fields. This does not call the signed-artifact runtime verifier at pre-sign.
	f := g.facts
	if t.artifactWritten {
		h, e := loadManualSignedArtifactHistory(t.ctx, t.q, t.bundle.owner, f.RequestID, scope, manualArtifactReadCipher{t.bundle.decryptor})
		_, digest, de := f.Canonical()
		if e != nil || de != nil || h.FactsHash != digest || h.OperationID != f.OperationID || h.AttemptID != f.AttemptID {
			return manualVerificationError(e, "SIGNING_TRUST_ARTIFACT_CHANGED")
		}
	} else {
		current, e := loadManualArtifactFacts(t.ctx, t.q, t.bundle.owner, f.RequestID, scope)
		if e != nil || current != f {
			return manualVerificationError(e, "SIGNING_TRUST_SOURCE_CHANGED")
		}
	}
	// Source verification/decryption consumes time. Sample AFTER it, then apply
	// BOTH causality and expiry bounds; never reuse the pre-crypto sample.
	if _, err := t.read(); err != nil {
		return err
	}
	s := t.clock.previous
	lo, hi, err := s.bounds()
	if err != nil {
		return err
	}
	control, err := readManualControl(t.ctx, t.q, lo)
	if err != nil {
		return err
	}
	gate, err := manualGateEvidenceHash(t.ctx, t.q, lo)
	if err != nil || gate != f.GateEvidenceSetHash || control.PolicyVersion != f.PolicyVersion {
		return manualVerificationError(err, "SIGNING_TRUST_GATE_CHANGED")
	}
	var n int
	err = t.q.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM canary_runtime_authorizations a
JOIN manual_risk_policies p ON p.id=? JOIN canary_policies cp ON cp.version=a.policy_version
JOIN manual_admission_commands cmd ON cmd.operation_id=? AND cmd.authorization_id=a.id
JOIN manual_execution_bindings b ON b.operation_id=cmd.operation_id
JOIN canary_worker_leases l ON l.role='SUBMISSION' AND l.environment=a.environment
WHERE a.id=? AND a.state='ARMED' AND a.epoch=? AND a.wallet_id=? AND a.chain_id=4663
AND a.environment=? AND a.build_sha=? AND a.release_id=? AND a.deployment_id=?
AND l.holder_id=? AND l.lease_epoch=?
AND manual_time_cmp(?,a.expires_at)<0 AND manual_time_cmp(?,l.expires_at)<0
AND NOT EXISTS(SELECT 1 FROM json_each(json_array(a.authorized_at,a.created_at,a.updated_at,p.created_at,cp.created_at,cmd.checked_at,b.checked_at,l.updated_at)) x WHERE manual_time_cmp(?,x.value)<0)`,
		f.ManualPolicyID, f.OperationID, f.AuthorizationID, f.AuthorizationEpoch, f.WalletID, scope.Environment, scope.BuildSHA, scope.ReleaseID, scope.DeploymentID,
		t.bundle.profile.process.holder, t.bundle.profile.process.leaseEpoch, hi.UTC().Format(time.RFC3339Nano), hi.UTC().Format(time.RFC3339Nano), lo.UTC().Format(time.RFC3339Nano)).Scan(&n)
	if err != nil || n != 1 {
		return manualVerificationError(err, "SIGNING_TRUST_AUTH_OWNER_OR_TIME_INVALID")
	}
	// Include all allowlist timestamp identities, not only config/profile.
	err = t.q.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM canary_wallet_allowlist w JOIN canary_contract_allowlist c ON c.policy_version=w.policy_version JOIN canary_token_allowlist tok ON tok.policy_version=w.policy_version
WHERE w.wallet_id=? AND w.policy_version=? AND w.enabled=1 AND c.enabled=1 AND tok.enabled=1
AND c.role='CURVE' AND c.protocol='PONS_V2_CURVE' AND tok.protocol='PONS_V2_CURVE' AND c.runtime_code_hash=? AND lower(c.address)=? AND lower(tok.token_address)=(SELECT json_extract(request_json,'$.token') FROM manual_http_order_sources WHERE id=?)
AND manual_time_cmp(?,w.created_at)>=0 AND manual_time_cmp(?,c.created_at)>=0 AND manual_time_cmp(?,tok.created_at)>=0`,
		f.WalletID, f.PolicyVersion, f.RuntimeCodeHash, f.To, f.RequestID, lo.UTC().Format(time.RFC3339Nano), lo.UTC().Format(time.RFC3339Nano), lo.UTC().Format(time.RFC3339Nano)).Scan(&n)
	if err != nil || n != 1 {
		return manualVerificationError(err, "SIGNING_TRUST_ALLOWLIST_TIME_INVALID")
	}
	for _, stamp := range []string{f.ClaimedAt, notBefore} {
		if stamp == "" {
			continue
		}
		bound, e := canonicalManualTime(stamp)
		if e != nil || lo.Before(bound) {
			return rejectManualInput("SIGNING_TRUST_CAUSALITY_INVALID")
		}
	}
	if ttlRecord(Record{ExpiresAt: f.ExpiresAt}, hi) != nil {
		return rejectManualInput("SIGNING_TRUST_EXPIRED")
	}
	if raw != "" {
		nonceEvidence, e := manualnonce.Verify(raw, digest, f.Nonce, g.observation, hi.UTC().Format(time.RFC3339Nano))
		at, ae := canonicalManualTime(nonceEvidence.ObservedAt)
		if e != nil || ae != nil || lo.Before(at) || !hi.Before(at.Add(manualnonce.MaxObservationAge)) || !hi.Before(at.Add(t.bundle.profile.maxAge)) {
			return rejectManualInput("SIGNING_TRUST_OBSERVATION_INVALID")
		}
		o := t.observation
		p := t.bundle.profile.process
		start, se := canonicalManualTime(t.observedStarted.wall)
		if se != nil || s.ticks < t.observedStarted.ticks || s.ticks-t.observedStarted.ticks >= t.bundle.profile.maxAge || !hi.Before(start.Add(t.bundle.profile.maxAge)) {
			return rejectManualInput("SIGNING_TRUST_RUNTIME_OBSERVATION_STALE")
		}
		if o.ChainID != 4663 || o.ProfileID != p.networkProfile || o.Environment != scope.Environment || o.NetworkCheckpoint != p.networkCheckpoint || o.Curve != f.To || o.RuntimeCodeHash != f.RuntimeCodeHash || o.Quote.Number != f.QuoteBlockNumber || o.Quote.Hash != f.QuoteBlockHash || o.Anchor.Number < o.Quote.Number || len(o.Anchor.Hash) != 66 || !strings.HasPrefix(o.Anchor.Hash, "0x") || !manualHex(strings.TrimPrefix(o.Anchor.Hash, "0x"), 64) || strings.Trim(strings.TrimPrefix(o.Anchor.Hash, "0x"), "0") == "" || o.EvidenceHash != o.Digest() {
			return rejectManualInput("SIGNING_TRUST_RUNTIME_OBSERVATION_INVALID")
		}
	}
	return t.ctx.Err()
}

func (t *manualPreSignTrust) advance(conn *sql.Conn) error {
	s := t.clock.previous
	v, err := readManualRuntimeClock(t.ctx, conn, t.bundle.profile.process.scope.Environment)
	if err != nil {
		return err
	}
	if err = checkManualClockFloor(v, s); err != nil {
		return err
	}
	return insertManualClockEvent(t.ctx, conn, v.environment, v.generation+1, s.wall, 0, "", nil, s.wall)
}

// Registered BEFORE all read/connection cleanup defers in the original writer.
// Hence it cannot hold a pool connection when calling the independent latch.
func (t *manualPreSignTrust) finish(result *manualLocalSigningResult, err *error) {
	if t == nil {
		return
	}
	if t.cleanupErr != nil {
		t.bundle.health.unhealthy.Store(true)
		*result = manualLocalSigningResult{}
		*err = errors.Join(*err, t.cleanupErr)
		return
	}
	if !t.clock.invalid {
		return
	}
	t.bundle.health.unhealthy.Store(true)
	if latchErr := latchManualRuntimeClock(t.ctx, t.bundle.store, t.bundle.profile.process.scope.Environment, t.clock.faultReason, t.clock.faultWall); latchErr != nil {
		if verifyErr := t.verifyLatched(); verifyErr != nil {
			*err = errors.Join(*err, latchErr, verifyErr)
		}
	}
	*result = manualLocalSigningResult{}
}

func (t *manualPreSignTrust) verifyLatched() error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.ctx), 2*time.Second)
	defer cancel()
	var count int
	err := t.bundle.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM manual_gate_clock_state WHERE chain_id=4663 AND environment=? AND fault_latched=1 AND fault_reason IN ('CLOCK_REVERSAL','CLOCK_UNVERIFIABLE')
AND typeof(generation)='integer' AND generation>=1 AND typeof(environment)='text'
AND manual_artifact_time_valid(hex(high_water_at),typeof(high_water_at))=1
AND manual_artifact_time_valid(hex(fault_at),typeof(fault_at))=1 AND updated_at IS fault_at`, t.bundle.profile.process.scope.Environment).Scan(&count)
	if err != nil || count != 1 {
		return manualVerificationError(err, "SIGNING_CLOCK_LATCH_NOT_DURABLE")
	}
	return nil
}

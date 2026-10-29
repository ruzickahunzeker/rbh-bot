package tradeapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualsign"
)

// Private controlled-local composition only. The private A3 bundle can select
// bounded trust; original scalar-clock fixtures remain compatible. No HTTP/
// startup caller, key loader, grant, allocator or send dependency.
type manualLocalSigningWriter struct {
	store      *Store
	observer   manualNonceObserver
	signer     manualsign.Signer
	cipher     manualartifact.Cipher
	now        func() time.Time
	trust      *manualTrustComposition // Optional only for the new disconnected trusted-local bundle.
	checkpoint func(string) error
}

type manualLocalSigningResult struct {
	Artifact                                               manualSignedArtifactHistory
	OperationID, Status                                    string
	Duplicate                                              bool
	ExecutionAuthorized, SigningAuthorized, SendAuthorized bool
}

// Per-invocation tracker; a reversed intermediate sample stays invalid even
// if the clock later recovers. No shared clock state across concurrent callers.
type manualSigningClock struct {
	now      func() time.Time
	previous time.Time
	invalid  bool
	trust    *manualPreSignTrust
}

func (c *manualSigningClock) read() (time.Time, error) {
	if c.trust != nil {
		return c.trust.read()
	}
	n := c.now().UTC()
	if n.IsZero() || n.Year() < 1970 || n.Year() > 9998 || (!c.previous.IsZero() && n.Before(c.previous)) {
		c.invalid = true
	}
	c.previous = n
	if c.invalid {
		return time.Time{}, rejectManualInput("SIGNING_CLOCK_UNVERIFIABLE")
	}
	return n, nil
}

func (w *manualLocalSigningWriter) at(stage string) error {
	if w.checkpoint != nil {
		return w.checkpoint(stage)
	}
	return nil
}

// History never checks active controls/time, calls an observer or invokes a
// signer. Raw retained rows also block retries when completeness is corrupt.
func (w *manualLocalSigningWriter) history(ctx context.Context, q queryRow, id Identity, request string, scope manualAdmissionScope) (manualLocalSigningResult, bool, error) {
	var zero manualLocalSigningResult
	if err := ownManualNonceRequest(ctx, q, id, request); err != nil {
		return zero, false, err
	}
	var operation string
	if err := q.QueryRowContext(ctx, `SELECT operation_id FROM manual_http_order_sources WHERE id=?`, request).Scan(&operation); err != nil {
		return zero, false, err
	}
	for _, table := range []string{"manual_signed_artifact_commands", "manual_signing_intents"} {
		var count int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE hex(operation_id)=hex(?) OR hex(CASE WHEN json_valid(facts_json) THEN json_extract(facts_json,'$.operation_id') END)=hex(?)`, operation, operation).Scan(&count); err != nil {
			return zero, false, err
		}
		if count == 0 {
			continue
		}
		if table == "manual_signed_artifact_commands" {
			v, err := loadManualSignedArtifactHistory(ctx, q, id, request, scope, w.cipher)
			if err != nil {
				return zero, true, err
			}
			return manualLocalSigningResult{Artifact: v, OperationID: operation, Status: v.Status, Duplicate: true}, true, nil
		}
		v, err := loadManualSigningIntentHistory(ctx, q, id, request, scope)
		if err != nil {
			return zero, true, err
		}
		if err := ctx.Err(); err != nil {
			return zero, true, err
		}
		return manualLocalSigningResult{OperationID: operation, Status: v.Status, Duplicate: true}, true, nil
	}
	return zero, false, nil
}

type manualSigningGate struct {
	facts       manualartifact.Facts
	observation manualnonce.Facts
	authExpiry  string
}

// SELECTs on the caller's one snapshot/writer. Reuses the original claim and
// conserved allowance; never asks admission to charge another operation.
func loadManualSigningGate(ctx context.Context, q queryRow, id Identity, request string, scope manualAdmissionScope, clock *manualSigningClock) (manualSigningGate, error) {
	var zero manualSigningGate
	if clock.trust != nil {
		clock.trust.q = q
	}
	now, clockErr := clock.read()
	control, err := readManualControl(ctx, q, now) // stop has first DB priority
	if err != nil {
		return zero, err
	}
	if clockErr != nil {
		return zero, clockErr
	}
	f, err := loadManualArtifactFacts(ctx, q, id, request, scope)
	if err != nil {
		return zero, err
	}
	claimed, ce := canonicalManualTime(f.ClaimedAt)
	if ce != nil || now.Before(claimed) || ttlRecord(Record{ExpiresAt: f.ExpiresAt}, now) != nil {
		return zero, rejectManualInput("SIGNING_TIME_INVALID_OR_EXPIRED")
	}
	claim, err := readManualNonceClaim(ctx, q, request)
	if err != nil {
		return zero, err
	}
	c, err := verifyManualNonceClaim(ctx, q, claim, id, scope)
	if err != nil {
		return zero, err
	}
	b, err := scanManualExecutionBinding(q.QueryRowContext(ctx, `SELECT `+manualExecutionBindingColumns+` FROM manual_execution_bindings WHERE operation_id=?`, f.OperationID))
	if err != nil {
		return zero, err
	}
	p, digest, err := loadManualPolicy(ctx, q, scope.ManualPolicyID, c, now)
	if err != nil {
		return zero, err
	}
	gate, err := manualGateEvidenceHash(ctx, q, now)
	if err != nil {
		return zero, err
	}
	if control.PolicyVersion != f.PolicyVersion || digest != f.ManualPolicyHash || gate != f.GateEvidenceSetHash {
		return zero, ErrConflict
	}
	a, err := loadManualAuthorizationIdentity(ctx, q, scope, c, p, digest, gate, now)
	if err != nil {
		return zero, err
	}
	var allowed int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM canary_risk_reservations r
 JOIN canary_authorization_operation_usage u ON u.operation_id=r.operation_id
 JOIN dry_run_wallets w ON w.id=r.wallet_id AND w.chain_id=4663 AND w.enabled=1
 JOIN canary_wallet_allowlist wa ON wa.wallet_id=w.id AND wa.chain_id=4663 AND wa.policy_version=? AND wa.enabled=1 AND lower(wa.address)=lower(w.address)
 WHERE r.id=? AND r.operation_id=? AND r.wallet_id=? AND r.state='reserved' AND u.authorization_id=? AND u.state='RESERVED'
 AND lower(w.address)=? AND NOT EXISTS(SELECT 1 FROM manual_nonce_unresolved_wallet_steps WHERE operation_id=r.operation_id)
 AND NOT EXISTS(SELECT 1 FROM execution_reservations x WHERE manual_nonce_identity_relation(hex(x.wallet_id),typeof(x.wallet_id),hex(r.wallet_id),0)>0 AND x.status IN ('reserved','signed','frozen'))
 AND NOT EXISTS(SELECT 1 FROM canary_risk_reservations x WHERE x.operation_id!=r.operation_id AND manual_nonce_identity_relation(hex(x.wallet_id),typeof(x.wallet_id),hex(r.wallet_id),0)>0 AND x.state IN ('reserved','frozen'))
 AND EXISTS(SELECT 1 FROM canary_contract_allowlist ca JOIN canary_token_allowlist ta ON ta.policy_version=ca.policy_version
 WHERE ca.policy_version=? AND ca.role='CURVE' AND ca.protocol='PONS_V2_CURVE' AND ca.enabled=1 AND ca.baseline_ref!=''
 AND lower(ca.address)=? AND ca.runtime_code_hash=? AND ta.protocol='PONS_V2_CURVE' AND ta.enabled=1 AND lower(ta.token_address)=?)`,
		f.PolicyVersion, f.ReservationID, f.OperationID, f.WalletID, f.AuthorizationID, f.From, f.PolicyVersion,
		c.Output.Resolution.Token.Curve, f.RuntimeCodeHash, c.Output.Resolution.Token.Address).Scan(&allowed); err != nil || allowed != 1 {
		return zero, manualVerificationError(err, "SIGNING_WALLET_RISK_ALLOWLIST_INVALID")
	}
	result := manualSigningGate{facts: f, observation: manualNonceFacts(c, b), authExpiry: a.ExpiresAt}
	if clock.trust != nil {
		if err := clock.trust.check(result, "", "", ""); err != nil {
			return zero, err
		}
	}
	return result, nil
}

func (g manualSigningGate) fresh(clock *manualSigningClock, raw, digest, notBefore string) (time.Time, error) {
	now, err := clock.read()
	expires, ae := canonicalManualTime(g.authExpiry)
	bound, be := canonicalManualTime(notBefore)
	if err != nil || ae != nil || be != nil || now.Before(bound) || !now.Before(expires) || ttlRecord(Record{ExpiresAt: g.facts.ExpiresAt}, now) != nil {
		return time.Time{}, rejectManualInput("SIGNING_TIME_INVALID_OR_EXPIRED")
	}
	if _, err := manualnonce.Verify(raw, digest, g.facts.Nonce, g.observation, now.Format(time.RFC3339Nano)); err != nil {
		return time.Time{}, rejectManualInput("SIGNING_OBSERVATION_INVALID_OR_EXPIRED")
	}
	if clock.trust != nil {
		if err := clock.trust.check(g, raw, digest, notBefore); err != nil {
			return time.Time{}, err
		}
	}
	return now, nil
}

func (w *manualLocalSigningWriter) sign(ctx context.Context, id Identity, request string, scope manualAdmissionScope) (result manualLocalSigningResult, resultErr error) {
	var zero manualLocalSigningResult
	if w == nil || w.store == nil || w.store.db == nil || ctx == nil || !scope.valid() {
		return zero, ErrInvalid
	}
	var trust *manualPreSignTrust
	// This defer is first: every transaction and connection is released before
	// fault persistence can acquire a new connection on the one-connection pool.
	defer func() {
		if r := recover(); r != nil {
			if trust == nil {
				panic(r)
			}
			result = manualLocalSigningResult{}
			resultErr = errors.New("SIGNING_TRUST_PRIMITIVE_FAILED")
			trust.bundle.health.unhealthy.Store(true)
		}
		if trust != nil {
			trust.finish(&result, &resultErr)
			if e := w.at("trust_cleanup_finished"); e != nil {
				result = manualLocalSigningResult{}
				resultErr = errors.Join(resultErr, e)
			}
		}
	}()
	// One coherent initial snapshot. Historical results do not need a clock,
	// observer or signer, and can never resume a retained invocation owner.
	tx, err := w.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return zero, err
	}
	defer func() {
		e := tx.Rollback()
		if trust != nil && e != nil && !errors.Is(e, sql.ErrTxDone) {
			trust.cleanupErr = errors.Join(trust.cleanupErr, e)
		}
	}()
	history, exists, err := w.history(ctx, tx, id, request, scope)
	if err != nil {
		return zero, err
	}
	if exists {
		if err := tx.Commit(); err != nil {
			return zero, err
		}
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		return history, nil
	}
	if (w.now == nil && w.trust == nil) || w.observer == nil || w.signer == nil || w.cipher == nil {
		return zero, ErrInvalid
	}
	clock := &manualSigningClock{now: w.now}
	if w.trust != nil {
		trust, err = w.trust.session(ctx, id, scope)
		if err != nil {
			return zero, err
		}
		clock.trust = trust
	}
	candidate, err := loadManualSigningGate(ctx, tx, id, request, scope, clock)
	if err != nil {
		return zero, err
	}
	if !manualsign.Matches(w.signer, candidate.facts) {
		return zero, manualsign.ErrSigning
	}
	readAt, err := clock.read()
	authExpiry, ae := canonicalManualTime(candidate.authExpiry)
	if err != nil || ae != nil || !readAt.Before(authExpiry) || ttlRecord(Record{ExpiresAt: candidate.facts.ExpiresAt}, readAt) != nil {
		return zero, rejectManualInput("SIGNING_READ_TIME_INVALID_OR_EXPIRED")
	}
	if err := tx.Commit(); err != nil {
		return zero, err
	}
	if trust != nil {
		trust.q = w.store.db
	}
	if err := w.at("after_read"); err != nil {
		return zero, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, manualnonce.MaxObservationAge)
	o, err := w.observer.ObserveManualNonce(queryCtx, candidate.observation)
	cause := queryCtx.Err()
	cancel()
	if cause != nil {
		return zero, cause
	}
	if err != nil {
		return zero, rejectManualInput("SIGNING_QUERY_FAILED")
	}
	raw, digest := manualnonce.Encode(o), manualnonce.Digest(manualnonce.Encode(o))
	observedAt, observedErr := canonicalManualTime(o.ObservedAt)
	if observedErr != nil || observedAt.Before(readAt) {
		return zero, rejectManualInput("SIGNING_QUERY_TIME_REVERSED")
	}
	if trust != nil {
		if err := trust.observe(candidate); err != nil {
			return zero, err
		}
	}
	if _, err := candidate.fresh(clock, raw, digest, candidate.facts.ClaimedAt); err != nil {
		return zero, err
	}
	if err := w.at("after_observation"); err != nil {
		return zero, err
	}
	conn, err := w.store.db.Conn(ctx)
	if err != nil {
		return zero, err
	}
	active := false
	defer func() {
		if trust == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if active {
			_, e := conn.ExecContext(cleanupCtx, "ROLLBACK")
			trust.cleanupErr = errors.Join(trust.cleanupErr, e)
		}
		trust.cleanupErr = errors.Join(trust.cleanupErr, conn.Close())
		if e := w.at("trust_connection_released"); e != nil {
			trust.cleanupErr = errors.Join(trust.cleanupErr, e)
		}
	}()
	defer func() {
		if trust == nil {
			_ = conn.Close()
		}
	}()
	if err := verifyManualWriterConnection(ctx, conn); err != nil {
		return zero, err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return zero, err
	}
	active = true
	defer func() {
		if trust == nil {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if v, found, err := w.history(ctx, conn, id, request, scope); err != nil {
		return zero, err
	} else if found {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return zero, err
		}
		active = false
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		return v, nil
	}
	if err := w.at("before_marker"); err != nil {
		return zero, err
	}
	current, err := loadManualSigningGate(ctx, conn, id, request, scope, clock)
	if err != nil || current.facts != candidate.facts {
		return zero, manualVerificationError(err, "SIGNING_SOURCE_CHANGED")
	}
	marked, err := current.fresh(clock, raw, digest, o.ObservedAt)
	if err != nil {
		return zero, err
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return zero, ErrInvalid
	}
	owner := hex.EncodeToString(entropy[:]) // never accepted as an input/resume token
	factsJSON, factsHash, _ := current.facts.Canonical()
	if _, err := conn.ExecContext(ctx, `INSERT INTO manual_signing_intents VALUES(?,?,?,?,?,?,?)`, current.facts.OperationID, owner, factsJSON, factsHash, raw, digest, marked.Format(time.RFC3339Nano)); err != nil {
		return zero, err
	}
	if _, err := current.fresh(clock, raw, digest, marked.Format(time.RFC3339Nano)); err != nil {
		return zero, err
	}
	if trust != nil {
		if err := trust.advance(conn); err != nil {
			return zero, err
		}
		if err := w.at("trust_marker_clock_staged"); err != nil {
			return zero, err
		}
		if trust.bundle.health.unhealthy.Load() {
			return zero, rejectManualInput("SIGNING_TRUST_UNHEALTHY")
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return zero, err // never invoke if marker commit outcome is uncertain
	}
	active = false
	if err := w.at("after_marker_commit"); err != nil {
		return zero, err
	}
	// Only this uninterrupted stack frame owns continuation. Reacquisition or
	// failure below never removes the durable ambiguity marker or signs twice.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return zero, err
	}
	active = true
	if err := w.at("after_fence"); err != nil {
		return zero, err
	}
	current, err = loadManualSigningGate(ctx, conn, id, request, scope, clock)
	if err != nil || current.facts != candidate.facts {
		return zero, manualVerificationError(err, "SIGNING_FINAL_SOURCE_CHANGED")
	}
	intent, err := loadManualSigningIntentHistory(ctx, conn, id, request, scope)
	if err != nil || intent.OwnerID != owner || intent.Facts != current.facts || intent.MarkedAt != marked.Format(time.RFC3339Nano) {
		return zero, manualVerificationError(err, "SIGNING_OWNER_INCOMPLETE")
	}
	if err := w.at("before_sign"); err != nil {
		return zero, err
	}
	if !manualsign.Matches(w.signer, current.facts) {
		return zero, manualsign.ErrSigning
	}
	if _, err := current.fresh(clock, raw, digest, intent.MarkedAt); err != nil {
		return zero, err
	}
	// Trusted local synchronous call only: no goroutine can outlive this fence.
	// A late implementation is awaited, but its output is rejected on timeout.
	signCtx, finish := context.WithTimeout(ctx, time.Second)
	v, err := manualsign.SignExact(signCtx, current.facts, w.signer, w.cipher, func() error {
		_, err := current.fresh(clock, raw, digest, intent.MarkedAt)
		return err
	})
	cause = signCtx.Err()
	finish()
	if cause != nil {
		return zero, cause
	}
	if err != nil {
		return zero, err
	}
	if err := w.at("after_sign"); err != nil {
		return zero, err
	}
	current, err = loadManualSigningGate(ctx, conn, id, request, scope, clock)
	if err != nil || current.facts != candidate.facts {
		return zero, manualVerificationError(err, "SIGNING_OUTCOME_SOURCE_CHANGED")
	}
	completed, err := current.fresh(clock, raw, digest, intent.MarkedAt)
	if err != nil {
		return zero, err
	}
	envelope := manualartifact.EnvelopeMetadata{Version: v.Version, FactsHash: v.FactsHash, TxHash: v.TxHash, RawSHA256: v.RawSHA256, KeyVersion: v.KeyVersion,
		Ciphertext: hex.EncodeToString(v.Ciphertext), EncryptionNonce: hex.EncodeToString(v.EncryptionNonce), CompletedAt: completed.Format(time.RFC3339Nano)}
	if err := w.at("before_artifact_insert"); err != nil {
		return zero, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO manual_signed_artifact_commands VALUES(?,?,?,?,?,?,?,?)`, current.facts.OperationID, owner, factsJSON, factsHash, encode(envelope), raw, digest, envelope.CompletedAt); err != nil {
		return zero, err
	}
	if trust != nil {
		trust.artifactWritten = true
	}
	if err := w.at("after_artifact_insert"); err != nil {
		return zero, err
	}
	verified, err := loadManualSignedArtifactHistory(ctx, conn, id, request, scope, w.cipher)
	if err != nil {
		return zero, err
	}
	if err := w.at("before_artifact_commit"); err != nil {
		return zero, err
	}
	if _, err := current.fresh(clock, raw, digest, envelope.CompletedAt); err != nil {
		return zero, err
	}
	if trust != nil {
		if err := trust.advance(conn); err != nil {
			return zero, err
		}
		if err := w.at("trust_artifact_clock_staged"); err != nil {
			return zero, err
		}
		if trust.bundle.health.unhealthy.Load() {
			return zero, rejectManualInput("SIGNING_TRUST_UNHEALTHY")
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return zero, err
	}
	active = false
	if err := w.at("after_artifact_commit"); err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return manualLocalSigningResult{Artifact: verified, OperationID: current.facts.OperationID, Status: "LOCAL_ARTIFACT_STORED_NOT_PERMISSION"}, nil
}

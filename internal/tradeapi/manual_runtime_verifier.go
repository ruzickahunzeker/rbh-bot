package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
)

// No public constructor/caller or production composition. Immutable dependencies
// are trusted harness primitives, never HTTP security flags or artifact bytes.
type manualRuntimeVerifier struct {
	store      *Store
	profile    manualRuntimeProfile
	timeSource manualRuntimeTimeSource
	identity   manualRuntimeIdentitySource
	decryptor  manualArtifactDecryptor
	observer   manualRuntimeObserver
	unhealthy  atomic.Bool
	checkpoint func(string) error
}

type manualRuntimeCandidate struct {
	status, operation, request, step, attempt, expires                       string
	factsHash, txHash, rawHash, sourceHash, observationID, processHash       string
	clockGeneration                                                          int64
	sample                                                                   manualRuntimeSample
	observation                                                              chainquery.SubmissionRuntimeObservation
	snapshotOnly, freshGateRequired                                          bool
	executionAuthorized, signingAuthorized, sendAuthorized, replayAuthorized bool
}

type manualRuntimePrepared struct {
	gate        manualRuntimeGate
	started     manualRuntimeSample
	observation chainquery.SubmissionRuntimeObservation
}

func (v *manualRuntimeVerifier) measured(ctx context.Context) error {
	p, err := v.identity.identify(ctx)
	if err != nil || p != v.profile.process || !v.profile.valid() || ctx.Err() != nil {
		return rejectManualInput("RUNTIME_PROCESS_IDENTITY_UNVERIFIABLE")
	}
	return nil
}

func (v *manualRuntimeVerifier) authenticate(ctx context.Context, q queryRow, id Identity, request string, g manualRuntimeGate) error {
	h, err := loadManualSignedArtifactHistory(ctx, q, id, request, v.profile.process.scope, manualArtifactReadCipher{v.decryptor})
	if err != nil {
		return err
	}
	f := g.projection.Foundation
	if h.FactsHash != f.FactsHash || h.TxHash != f.TxHash || h.RawSHA256 != f.RawHash {
		return ErrConflict
	}
	return ctx.Err()
}

func (v *manualRuntimeVerifier) at(stage string) error {
	if v.checkpoint != nil {
		return v.checkpoint(stage)
	}
	return nil
}

func (v *manualRuntimeVerifier) checkObservation(p manualRuntimePrepared, s manualRuntimeSample, g manualRuntimeGate) error {
	start, err := canonicalManualTime(p.started.wall)
	if err != nil {
		return err
	}
	_, upper, err := s.bounds()
	if err != nil {
		return err
	}
	if s.ticks < p.started.ticks || s.ticks-p.started.ticks > v.profile.maxAge || upper.Sub(start) > v.profile.maxAge {
		return rejectManualInput("RUNTIME_OBSERVATION_STALE")
	}
	f := g.projection.Foundation.Facts
	o := p.observation
	anchor := strings.TrimPrefix(o.Anchor.Hash, "0x")
	if o.ChainID != 4663 || o.ProfileID != v.profile.process.networkProfile || o.Environment != f.Environment ||
		o.NetworkCheckpoint != v.profile.process.networkCheckpoint ||
		o.Curve != g.projection.Foundation.Contract.Output.Resolution.Token.Curve || o.RuntimeCodeHash != f.RuntimeCodeHash ||
		o.Quote.Number != f.QuoteBlockNumber || o.Quote.Hash != f.QuoteBlockHash || o.Anchor.Number < o.Quote.Number ||
		len(o.Anchor.Hash) != 66 || !strings.HasPrefix(o.Anchor.Hash, "0x") || !manualHex(anchor, 64) || anchor == stringZeros64 ||
		!manualHex(o.EvidenceHash, 64) || o.EvidenceHash != o.Digest() {
		return rejectManualInput("RUNTIME_RPC_IDENTITY_UNVERIFIABLE")
	}
	return nil
}

const stringZeros64 = "0000000000000000000000000000000000000000000000000000000000000000"

func (v *manualRuntimeVerifier) prepare(ctx context.Context, id Identity, request string, clock *manualRuntimeClock) (manualRuntimePrepared, error) {
	var p manualRuntimePrepared
	if err := runtimeStopFirst(ctx, v.store.db); err != nil {
		return p, err
	}
	if !v.profile.valid() {
		return p, ErrInvalid
	}
	s, err := clock.read(ctx)
	if err != nil {
		return p, err
	}
	if s.provider != v.profile.process.provider {
		return p, rejectManualInput("RUNTIME_CLOCK_PROVIDER_MISMATCH")
	}
	if err = v.measured(ctx); err != nil {
		return p, err
	}
	tx, err := v.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	p.gate, err = loadManualRuntimeGate(ctx, tx, id, request, v.profile, clock, s)
	if err != nil {
		return p, err
	}
	if err = v.authenticate(ctx, tx, id, request, p.gate); err != nil {
		return p, err
	}
	if _, err = clock.read(ctx); err != nil {
		return p, err
	}
	if err = tx.Commit(); err != nil {
		return p, err
	}
	p.started, err = clock.read(ctx)
	if err != nil {
		return p, err
	}
	if err = v.measured(ctx); err != nil {
		return p, err
	}
	c := p.gate.projection.Foundation.Contract
	rpcCtx, cancel := context.WithTimeout(ctx, v.profile.queryTimeout)
	defer cancel()
	p.observation, err = v.observer.observe(rpcCtx, manualRuntimeChainFacts{curve: c.Output.Resolution.Token.Curve, runtimeHash: c.Output.Resolution.Token.CurveRuntimeCodeHash, quote: c.Output.Resolution.Token.Block})
	if err != nil {
		return p, err
	}
	if err = rpcCtx.Err(); err != nil {
		return p, err
	}
	if _, err = clock.read(ctx); err != nil {
		return p, err
	}
	return p, v.at("after_observation")
}

// Transaction-local final check never BEGINs/COMMITs, queries RPC or grants
// permission. Future B23 must freshly prepare and call it on its own writer.
func (v *manualRuntimeVerifier) finalCheck(ctx context.Context, q queryRow, id Identity, request string, clock *manualRuntimeClock, p manualRuntimePrepared) (manualRuntimeGate, manualRuntimeSample, error) {
	var zero manualRuntimeGate
	if err := runtimeStopFirst(ctx, q); err != nil {
		return zero, manualRuntimeSample{}, err
	}
	if err := v.measured(ctx); err != nil {
		return zero, manualRuntimeSample{}, err
	}
	s, err := clock.read(ctx)
	if err != nil {
		return zero, s, err
	}
	g, err := loadManualRuntimeGate(ctx, q, id, request, v.profile, clock, s)
	if err != nil {
		return g, s, err
	}
	if g.source != p.gate.source || g.sourceHash != p.gate.sourceHash || g.clock.generation != p.gate.clock.generation {
		return g, s, rejectManualInput("RUNTIME_FINAL_SOURCE_DRIFT")
	}
	if err = v.authenticate(ctx, q, id, request, g); err != nil {
		return g, s, err
	}
	if err = v.measured(ctx); err != nil {
		return g, s, err
	}
	if err = v.at("before_final_time"); err != nil {
		return g, s, err
	}
	s, err = clock.read(ctx)
	if err != nil {
		return g, s, err
	}
	if _, err = loadManualRuntimeGate(ctx, q, id, request, v.profile, clock, s); err != nil {
		return g, s, err
	}
	if err = v.checkObservation(p, s, g); err != nil {
		return g, s, err
	}
	return g, s, ctx.Err()
}

// Capture commits metadata only. Transient candidates never survive as action
// permission; even exact duplicate metadata requires fresh verification.
func (v *manualRuntimeVerifier) capture(ctx context.Context, id Identity, request string) (result manualRuntimeCandidate, err error) {
	if v == nil || v.store == nil || v.store.db == nil || ctx == nil || v.timeSource == nil || v.identity == nil || v.decryptor == nil || v.observer == nil || v.unhealthy.Load() {
		return result, ErrInvalid
	}
	clock := &manualRuntimeClock{source: v.timeSource, maximumError: v.profile.maximumError}
	defer func() {
		if recover() != nil {
			result = manualRuntimeCandidate{}
			err = rejectManualInput("RUNTIME_PRIMITIVE_FAILED")
			v.unhealthy.Store(true)
		}
		if err != nil && clock.invalid {
			if latchErr := latchManualRuntimeClock(ctx, v.store, v.profile.process.scope.Environment, clock.faultReason, clock.faultWall); latchErr != nil {
				v.unhealthy.Store(true)
				err = errors.Join(err, latchErr)
			}
		}
	}()
	p, err := v.prepare(ctx, id, request, clock)
	if err != nil {
		return result, err
	}
	conn, err := v.store.db.Conn(ctx)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, err
	}
	defer conn.ExecContext(context.Background(), `ROLLBACK`)
	if err = v.at("writer_acquired"); err != nil {
		return result, err
	}
	g, s, err := v.finalCheck(ctx, conn, id, request, clock, p)
	if err != nil {
		return result, err
	}
	if err = insertManualClockEvent(ctx, conn, v.profile.process.scope.Environment, g.clock.generation+1, s.wall, 0, "", nil, s.wall); err != nil {
		return result, err
	}
	var observationID string
	f := g.projection.Foundation.Facts
	if err = conn.QueryRowContext(ctx, `SELECT 'manual-gate-evidence:'||manual_artifact_bytes_sha256(hex(json_object('operation_id',?,'source_hash',?,'checked_at',?)))`, f.OperationID, g.sourceHash, s.wall).Scan(&observationID); err != nil {
		return result, err
	}
	var existing int
	if err = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM manual_gate_evidence_complete WHERE id=? AND operation_id=? AND source_json=? AND source_hash=? AND checked_at=?`, observationID, f.OperationID, g.source, g.sourceHash, s.wall).Scan(&existing); err != nil {
		return result, err
	}
	if existing == 0 {
		if _, err = conn.ExecContext(ctx, `INSERT INTO manual_submission_gate_evidence(id,operation_id,source_json,source_hash,checked_at) VALUES(?,?,?,?,?)`, observationID, f.OperationID, g.source, g.sourceHash, s.wall); err != nil {
			return result, err
		}
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	return manualRuntimeCandidate{status: "VERIFIED_RUNTIME_CANDIDATE_NOT_PERMISSION", operation: f.OperationID, request: f.RequestID, step: f.ExecutionStepID, attempt: f.AttemptID, expires: f.ExpiresAt,
		factsHash: g.projection.Foundation.FactsHash, txHash: g.projection.Foundation.TxHash, rawHash: g.projection.Foundation.RawHash, sourceHash: g.sourceHash, observationID: observationID,
		processHash: hash([]byte(encode(v.profile.process.scope) + v.profile.process.binaryHash + v.profile.process.manifestHash)), clockGeneration: g.clock.generation + 1, sample: s, observation: p.observation, snapshotOnly: true, freshGateRequired: true}, nil
}

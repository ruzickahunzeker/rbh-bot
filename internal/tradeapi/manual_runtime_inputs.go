package tradeapi

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/chainquery"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
)

// Trusted private composition dependencies; not caller security parameters.
// There is no production attestor/provider/key-loader composition in B2.2.
type manualRuntimeProcess struct {
	scope                                                      manualAdmissionScope
	binaryHash, manifestHash, networkProfile, holder, provider string
	leaseEpoch                                                 int64
	networkCheckpoint                                          chainquery.Block
	dirty                                                      bool
}

type manualRuntimeIdentitySource interface {
	identify(context.Context) (manualRuntimeProcess, error)
}

type manualRuntimeChainFacts struct {
	curve, runtimeHash string
	quote              chainquery.Block
}

type manualRuntimeObserver interface {
	observe(context.Context, manualRuntimeChainFacts) (chainquery.SubmissionRuntimeObservation, error)
}

type manualRuntimeRPC struct {
	reader *chainquery.SubmissionRuntimeObserver
}

func (r manualRuntimeRPC) observe(ctx context.Context, f manualRuntimeChainFacts) (chainquery.SubmissionRuntimeObservation, error) {
	return r.reader.Observe(ctx, f.curve, f.runtimeHash, f.quote)
}

type manualRuntimeProfile struct {
	process                            manualRuntimeProcess
	maximumError, queryTimeout, maxAge time.Duration
}

func (p manualRuntimeProfile) valid() bool {
	const zero = "0000000000000000000000000000000000000000000000000000000000000000"
	return p.process.scope.valid() && manualHex(p.process.binaryHash, 64) && p.process.binaryHash != zero && manualHex(p.process.manifestHash, 64) && p.process.manifestHash != zero &&
		!p.process.dirty && p.process.manifestHash == runtimeManifestDigest(p.process) && p.process.networkCheckpoint.Number > 0 && len(p.process.networkCheckpoint.Hash) == 66 && strings.HasPrefix(p.process.networkCheckpoint.Hash, "0x") && manualHex(strings.TrimPrefix(p.process.networkCheckpoint.Hash, "0x"), 64) && validID(p.process.networkProfile) && validID(p.process.holder) && validID(p.process.provider) && p.process.leaseEpoch > 0 &&
		p.maximumError >= 0 && p.queryTimeout > 0 && p.queryTimeout <= time.Minute && p.maxAge > 0 && p.maxAge <= time.Minute
}

type manualRuntimeProjection struct {
	Control struct {
		UpdatedAt string `json:"updated_at"`
	} `json:"control"`
	Foundation struct {
		Facts     manualartifact.Facts    `json:"facts"`
		Contract  ManualExecutionContract `json:"contract"`
		FactsHash string                  `json:"facts_hash"`
		TxHash    string                  `json:"tx_hash"`
		RawHash   string                  `json:"raw_sha256"`
	} `json:"foundation"`
	Lease struct {
		Role        string `json:"role"`
		Environment string `json:"environment"`
		Holder      string `json:"holder_id"`
		Epoch       int64  `json:"epoch"`
	} `json:"submission_lease"`
}

type manualRuntimeGate struct {
	source, sourceHash string
	projection         manualRuntimeProjection
	clock              manualClockState
}

func runtimeStopFirst(ctx context.Context, q queryRow) error {
	var stop int
	var mode string
	err := q.QueryRowContext(ctx, `SELECT emergency_stopped,mode FROM canary_control_state WHERE singleton=1 AND chain_id=4663 AND typeof(emergency_stopped)='integer' AND typeof(mode)='text'`).Scan(&stop, &mode)
	if err != nil {
		return manualVerificationError(err, "CONTROL_UNAVAILABLE")
	}
	if stop == 1 {
		return rejectManualInput("EMERGENCY_STOPPED")
	}
	if stop != 0 || mode != "CONTROLLED_CANARY" {
		return rejectManualInput("CANARY_DISABLED")
	}
	return nil
}

// SELECT-only on the caller's current snapshot/writer, not a new admission.
// Original038 performs complete signed-phase/ownership/accounting validation.
func loadManualRuntimeGate(ctx context.Context, q queryRow, id Identity, request string, profile manualRuntimeProfile, clock *manualRuntimeClock, sample manualRuntimeSample) (manualRuntimeGate, error) {
	var g manualRuntimeGate
	if err := runtimeStopFirst(ctx, q); err != nil {
		return g, err
	}
	if !id.valid() || !validID(request) || !profile.valid() {
		return g, ErrInvalid
	}
	if err := ownManualNonceRequest(ctx, q, id, request); err != nil {
		return g, err
	}
	lower, upper, err := sample.bounds()
	if err != nil {
		return g, err
	}
	g.clock, err = readManualRuntimeClock(ctx, q, profile.process.scope.Environment)
	if err != nil {
		return g, err
	}
	if err = checkManualClockFloor(g.clock, sample); err != nil {
		clock.invalid, clock.faultReason, clock.faultWall = true, "CLOCK_REVERSAL", sample.wall
		return g, err
	}
	if _, err = readManualControl(ctx, q, lower); err != nil {
		return g, err
	}
	err = q.QueryRowContext(ctx, `WITH bounds AS (SELECT ? AS lo,? AS hi)
 SELECT e.source_json,manual_artifact_bytes_sha256(hex(e.source_json)) FROM manual_gate_evidence_valid_source e,bounds
 WHERE e.operation_id=? AND manual_time_cmp(bounds.hi,e.operation_expires_at)<0
 AND manual_time_cmp(bounds.hi,e.auth_expires_at)<0 AND manual_time_cmp(bounds.hi,e.lease_expires_at)<0
 AND NOT EXISTS(SELECT 1 FROM json_each(json_array(e.foundation_created_at,e.artifact_completed_at,e.control_effective_at,e.control_updated_at,
 e.authorized_at,e.auth_created_at,e.auth_updated_at,e.lease_updated_at,e.policy_created_at,
 json_extract(e.source_json,'$.allowlists.wallet_created_at'),json_extract(e.source_json,'$.allowlists.contract_created_at'),json_extract(e.source_json,'$.allowlists.token_created_at'))) t WHERE manual_time_cmp(bounds.lo,t.value)<0)
 AND NOT EXISTS(SELECT 1 FROM canary_gate_attestations a WHERE manual_artifact_time_valid(hex(a.attested_at),typeof(a.attested_at))!=1 OR manual_time_cmp(bounds.lo,a.attested_at)<0)`,
		lower.UTC().Format(time.RFC3339Nano), upper.UTC().Format(time.RFC3339Nano), "manual_op_"+request).Scan(&g.source, &g.sourceHash)
	if err != nil {
		return g, manualVerificationError(err, "RUNTIME_SOURCE_TIME_OR_CONTROL_INVALID")
	}
	if err = json.Unmarshal([]byte(g.source), &g.projection); err != nil {
		return g, ErrConflict
	}
	f, c := g.projection.Foundation.Facts, g.projection.Foundation.Contract
	s := profile.process.scope
	if f.Validate() != nil || f.RequestID != request || f.WalletID != id.WalletID || f.From != strings.ToLower(id.WalletAddress) || c.PrincipalID != id.PrincipalID || f.ContractHash != c.BindingHash || f.OperationID != c.OperationID ||
		f.AuthorizationID != s.AuthorizationID || f.AuthorizationEpoch != s.AuthorizationEpoch || f.ManualPolicyID != s.ManualPolicyID ||
		f.Environment != s.Environment || f.BuildSHA != s.BuildSHA || f.ReleaseID != s.ReleaseID || f.DeploymentID != s.DeploymentID ||
		g.projection.Lease.Role != "SUBMISSION" || g.projection.Lease.Environment != s.Environment || g.projection.Lease.Holder != profile.process.holder || g.projection.Lease.Epoch != profile.process.leaseEpoch {
		return g, rejectManualInput("RUNTIME_SCOPE_OR_OWNER_MISMATCH")
	}
	return g, nil
}

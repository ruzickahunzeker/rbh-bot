package trade

import (
	"context"
	"errors"
	"io"
	"time"
)

const ProductionModeDisabled = "DISABLED"

var ErrProductionCompositionRejected = errors.New("controlled canary production composition rejected")

// ProductionReadiness deliberately separates permission for new economic
// actions from availability of recovery for already durable work.
type ProductionReadiness struct {
	CanaryAdmissionReady bool
	SubmissionSendReady  bool
	RecoveryReady        bool
	ReasonCode           string
}

// ProductionComposition is the W4 production lifecycle boundary. W4-A only
// installs the disabled composition: it has no signer, SubmissionService,
// RawBroadcaster, orchestrator, or worker dependency. Later slices must add
// those dependencies without creating a second execution state machine.
type ProductionComposition struct {
	store *Store
	mode  string
	now   func() time.Time
}

func NewProductionComposition(store *Store, mode string) (*ProductionComposition, error) {
	if store == nil || mode != ProductionModeDisabled {
		return nil, ErrProductionCompositionRejected
	}
	return &ProductionComposition{store: store, mode: mode, now: time.Now}, nil
}

func (c *ProductionComposition) Start(ctx context.Context) error {
	if c == nil || c.store == nil || ctx == nil || c.mode != ProductionModeDisabled {
		return ErrProductionCompositionRejected
	}
	return c.store.RecordRuntimeAlert(ctx, "W4_PRODUCTION_STARTUP", "", "", "", 0, "PRODUCTION_WIRING_DISABLED", "INFO", c.now().UTC())
}

func (c *ProductionComposition) Readiness() ProductionReadiness {
	return ProductionReadiness{
		CanaryAdmissionReady: false,
		SubmissionSendReady:  false,
		RecoveryReady:        false,
		ReasonCode:           "W4A_DISABLED_RECOVERY_NOT_WIRED",
	}
}

func (c *ProductionComposition) WriteMetrics(_ context.Context, w io.Writer) error {
	if c == nil || w == nil {
		return ErrProductionCompositionRejected
	}
	_, err := io.WriteString(w,
		"# TYPE rbh_canary_admission_ready gauge\nrbh_canary_admission_ready 0\n"+
			"# TYPE rbh_submission_send_ready gauge\nrbh_submission_send_ready 0\n"+
			"# TYPE rbh_recovery_ready gauge\nrbh_recovery_ready 0\n")
	return err
}

package trade

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const ProductionModeDisabled = "DISABLED"

var ErrProductionCompositionRejected = errors.New("controlled canary production composition rejected")

type ProductionReadiness struct {
	CanaryAdmissionReady bool
	SubmissionSendReady  bool
	RecoveryReady        bool
	ReasonCode           string
}

type RecoveryHealthProbe interface{ CheckRecoveryHealth(context.Context) error }

type ProductionRecoveryConfig struct {
	Environment  string
	HolderID     string
	LeaseTTL     time.Duration
	ScanInterval time.Duration
}

type productionRecoveryDependencies struct {
	recovery *RecoveryService
	querier  ControlledRecoveryQuerier
	health   RecoveryHealthProbe
	config   ProductionRecoveryConfig
}

// ProductionComposition is the production lifecycle boundary. Admission and
// submission remain disabled. Its optional W4-C dependency graph contains only
// recovery/query capabilities and cannot reach a signer or broadcaster.
type ProductionComposition struct {
	store *Store
	mode  string
	now   func() time.Time

	mu             sync.RWMutex
	recovery       *productionRecoveryDependencies
	worker         *ControlledRecoveryWorker
	recoveryReady  bool
	recoveryReason string
	started        bool
	runDone        chan struct{}
}

func NewProductionComposition(store *Store, mode string) (*ProductionComposition, error) {
	if store == nil || mode != ProductionModeDisabled {
		return nil, ErrProductionCompositionRejected
	}
	return &ProductionComposition{store: store, mode: mode, now: time.Now, recoveryReason: "W4A_DISABLED_RECOVERY_NOT_WIRED"}, nil
}

func (c *ProductionComposition) ConfigureRecovery(recovery *RecoveryService, querier ControlledRecoveryQuerier, health RecoveryHealthProbe, cfg ProductionRecoveryConfig) error {
	if c == nil || recovery == nil || querier == nil || health == nil || cfg.Environment == "" || cfg.HolderID == "" || cfg.LeaseTTL <= 0 || cfg.ScanInterval <= 0 || cfg.ScanInterval >= cfg.LeaseTTL/2 {
		return ErrProductionCompositionRejected
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started || c.recovery != nil {
		return ErrProductionCompositionRejected
	}
	c.recovery = &productionRecoveryDependencies{recovery: recovery, querier: querier, health: health, config: cfg}
	c.runDone = make(chan struct{})
	c.recoveryReason = "RECOVERY_NOT_STARTED"
	return nil
}

func (c *ProductionComposition) Start(ctx context.Context) error {
	if c == nil || c.store == nil || ctx == nil || c.mode != ProductionModeDisabled {
		return ErrProductionCompositionRejected
	}
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return ErrProductionCompositionRejected
	}
	c.started = true
	deps := c.recovery
	c.mu.Unlock()
	if deps == nil {
		return c.store.RecordRuntimeAlert(ctx, "W4_PRODUCTION_STARTUP", "", "", "", 0, "PRODUCTION_WIRING_DISABLED", "INFO", c.now().UTC())
	}
	if err := c.checkRecoveryHealth(ctx, deps); err != nil {
		c.setRecoveryReadiness(false, "RECOVERY_HEALTH_UNAVAILABLE")
		_ = c.store.RecordRuntimeAlert(ctx, "W4C_PRODUCTION_RECOVERY", "", "", "", 0, "RECOVERY_HEALTH_UNAVAILABLE", "CRITICAL", c.now().UTC())
		return err
	}
	epoch, err := c.store.AcquireNextCanaryWorkerLease(ctx, "RECOVERY", deps.config.Environment, deps.config.HolderID, deps.config.LeaseTTL, c.now().UTC())
	if err != nil {
		c.setRecoveryReadiness(false, "RECOVERY_LEASE_UNAVAILABLE")
		_ = c.store.RecordRuntimeAlert(ctx, "W4C_PRODUCTION_RECOVERY", "", "", "", 0, "RECOVERY_LEASE_UNAVAILABLE", "CRITICAL", c.now().UTC())
		return err
	}
	worker, err := NewControlledRecoveryWorker(c.store, deps.recovery, deps.querier, deps.config.Environment, deps.config.HolderID, epoch)
	if err != nil {
		return err
	}
	worker.now = c.now
	c.mu.Lock()
	c.worker = worker
	c.recoveryReady = true
	c.recoveryReason = "RECOVERY_READY"
	c.mu.Unlock()
	return c.store.RecordRuntimeAlert(ctx, "W4C_PRODUCTION_RECOVERY", "", "", "", epoch, "RECOVERY_STARTED_WITHOUT_SEND_AUTHORIZATION", "INFO", c.now().UTC())
}

// Run maintains the independent RECOVERY lease while the W3 worker performs
// durable discovery. Cancellation drains the current scan; lease renewal stays
// active until that drain finishes, and unfinished work remains discoverable.
func (c *ProductionComposition) Run(ctx context.Context) error {
	if c == nil || ctx == nil {
		return ErrProductionCompositionRejected
	}
	c.mu.RLock()
	worker, deps := c.worker, c.recovery
	c.mu.RUnlock()
	if worker == nil || deps == nil {
		return ErrProductionCompositionRejected
	}
	defer close(c.runDone)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker.Run(workerCtx, deps.config.ScanInterval) }()
	renew := time.NewTicker(deps.config.LeaseTTL / 3)
	defer renew.Stop()
	ctxDone := ctx.Done()
	stopping := false
	var terminalErr error
	for {
		select {
		case err := <-done:
			if terminalErr != nil {
				return terminalErr
			}
			c.setRecoveryReadiness(false, "RECOVERY_STOPPED")
			return err
		case <-ctxDone:
			stopping = true
			ctxDone = nil
			cancel()
		case <-renew.C:
			healthCtx := context.WithoutCancel(ctx)
			if err := c.checkRecoveryHealth(healthCtx, deps); err != nil {
				c.setRecoveryReadiness(false, "RECOVERY_HEALTH_UNAVAILABLE")
				_ = c.store.RecordRuntimeAlert(healthCtx, "W4C_PRODUCTION_RECOVERY", "", "", "", worker.epoch, "RECOVERY_HEALTH_UNAVAILABLE", "CRITICAL", c.now().UTC())
				cancel()
				terminalErr = err
				continue
			}
			if err := c.store.RenewCanaryWorkerLease(healthCtx, "RECOVERY", deps.config.Environment, deps.config.HolderID, worker.epoch, deps.config.LeaseTTL, c.now().UTC()); err != nil {
				c.setRecoveryReadiness(false, "RECOVERY_LEASE_LOST")
				_ = c.store.RecordRuntimeAlert(healthCtx, "W4C_PRODUCTION_RECOVERY", "", "", "", worker.epoch, "RECOVERY_LEASE_LOST", "CRITICAL", c.now().UTC())
				cancel()
				terminalErr = err
				continue
			}
			if !stopping {
				c.setRecoveryReadiness(true, "RECOVERY_READY")
			}
		}
	}
}

func (c *ProductionComposition) Wait(ctx context.Context) error {
	if c == nil || ctx == nil {
		return ErrProductionCompositionRejected
	}
	c.mu.RLock()
	done := c.runDone
	c.mu.RUnlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *ProductionComposition) checkRecoveryHealth(ctx context.Context, deps *productionRecoveryDependencies) error {
	if err := c.store.db.PingContext(ctx); err != nil {
		return err
	}
	return deps.health.CheckRecoveryHealth(ctx)
}

func (c *ProductionComposition) setRecoveryReadiness(ready bool, reason string) {
	c.mu.Lock()
	c.recoveryReady, c.recoveryReason = ready, reason
	c.mu.Unlock()
}

func (c *ProductionComposition) Readiness() ProductionReadiness {
	if c == nil {
		return ProductionReadiness{ReasonCode: "RECOVERY_UNAVAILABLE"}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ProductionReadiness{CanaryAdmissionReady: false, SubmissionSendReady: false, RecoveryReady: c.recoveryReady, ReasonCode: c.recoveryReason}
}

func (c *ProductionComposition) WriteMetrics(_ context.Context, w io.Writer) error {
	if c == nil || w == nil {
		return ErrProductionCompositionRejected
	}
	ready := c.Readiness()
	recovery := 0
	if ready.RecoveryReady {
		recovery = 1
	}
	_, err := fmt.Fprintf(w,
		"# TYPE rbh_canary_admission_ready gauge\nrbh_canary_admission_ready 0\n"+
			"# TYPE rbh_submission_send_ready gauge\nrbh_submission_send_ready 0\n"+
			"# TYPE rbh_recovery_ready gauge\nrbh_recovery_ready %d\n", recovery)
	return err
}

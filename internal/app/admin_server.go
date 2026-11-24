package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/health"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type runtimeEmergencyControl struct {
	stopped atomic.Bool
	mu      sync.Mutex
	reason  string
	updated time.Time
}

func (c *runtimeEmergencyControl) set(stopped bool, reason string, updated time.Time) {
	c.mu.Lock()
	c.reason = reason
	c.updated = updated
	c.stopped.Store(stopped)
	c.mu.Unlock()
}

func (c *runtimeEmergencyControl) Snapshot() (stopped bool, reason string, updated time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped.Load(), c.reason, c.updated
}

func newAdminServer(serviceName, socketPath string, chainID uint64, log *slog.Logger, database *storage.Database, control *runtimeEmergencyControl, isInFlight func(string) bool) *health.Server {
	ready := health.NewReadiness("admin_ready")
	ready.Set("admin_ready", true)
	admin := health.New(serviceName+"-admin", socketPath, log, ready)
	admin.SetSocketMode(0o600)

	admin.Handle("GET /admin/v1/control/status", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stopped, reason, updated, err := readDurableEmergencyStop(r.Context(), database, chainID, control)
		if err != nil {
			writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"emergency_stopped": stopped,
			"reason":            reason,
			"updated_at":        updated.Format(time.RFC3339Nano),
		})
	}))

	admin.Handle("POST /admin/v1/control/emergency-stop", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
		now := time.Now().UTC()
		if err := persistDurableEmergencyStop(r.Context(), database, chainID, true, body.Reason, now); err != nil {
			writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		control.set(true, body.Reason, now)
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"emergency_stopped": true,
			"reason":            body.Reason,
			"updated_at":        now.Format(time.RFC3339Nano),
		})
	}))

	admin.Handle("POST /admin/v1/control/resume", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		if err := persistDurableEmergencyStop(r.Context(), database, chainID, false, "", now); err != nil {
			writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		control.set(false, "", now)
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"emergency_stopped": false,
			"reason":            "",
			"updated_at":        now.Format(time.RFC3339Nano),
		})
	}))

	admin.Handle("POST /admin/v1/backup", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Dest string `json:"dest"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Dest == "" {
			writeAdminJSON(w, http.StatusBadRequest, map[string]any{"error": "dest path is required"})
			return
		}
		digest, size, err := database.VacuumBackup(r.Context(), body.Dest)
		if err != nil {
			writeAdminJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"dest":       body.Dest,
			"sha256":     digest,
			"size_bytes": size,
		})
	}))

	admin.Handle("GET /admin/v1/diagnostics/stuck", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := listStuckOperations(r.Context(), database)
		if err != nil {
			writeAdminJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{"stuck_operations": items})
	}))

	admin.Handle("POST /admin/v1/operations/{operation_id}/abort-pre-sign", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opID := r.PathValue("operation_id")
		var body struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
		if body.Reason == "" {
			body.Reason = "operator_abort_pre_sign"
		}
		if err := abortPreSignOperation(r.Context(), database, opID, body.Reason, isInFlight, time.Now().UTC()); err != nil {
			writeAdminJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"operation_id": opID,
			"status":       "aborted_pre_sign",
			"reason":       body.Reason,
		})
	}))
	return admin
}

type stuckOperationDiagnostic struct {
	OperationID       string `json:"operation_id"`
	WalletID          string `json:"wallet_id"`
	ReservationID     string `json:"reservation_id"`
	ReservationState  string `json:"reservation_state"`
	HasSigningIntent  bool   `json:"has_signing_intent"`
	CanAbortPreSign   bool   `json:"can_abort_pre_sign"`
}

func listStuckOperations(ctx context.Context, database *storage.Database) ([]stuckOperationDiagnostic, error) {
	if database == nil {
		return nil, errors.New("database is unavailable")
	}
	readDB, err := database.ReadDB(storage.TradeOwner)
	if err != nil {
		return []stuckOperationDiagnostic{}, nil
	}
	rows, err := readDB.QueryContext(ctx, `
		SELECT r.operation_id, r.wallet_id, r.id, r.state,
		       EXISTS(SELECT 1 FROM manual_signing_intents si WHERE si.operation_id = r.operation_id)
		FROM canary_risk_reservations r
		WHERE r.state IN ('reserved', 'frozen')
		ORDER BY r.created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]stuckOperationDiagnostic, 0)
	for rows.Next() {
		var item stuckOperationDiagnostic
		var hasIntent int
		if err := rows.Scan(&item.OperationID, &item.WalletID, &item.ReservationID, &item.ReservationState, &hasIntent); err != nil {
			return nil, err
		}
		item.HasSigningIntent = hasIntent != 0
		item.CanAbortPreSign = item.ReservationState == "reserved" && !item.HasSigningIntent
		out = append(out, item)
	}
	return out, rows.Err()
}

func abortPreSignOperation(ctx context.Context, database *storage.Database, operationID, reason string, isInFlight func(string) bool, now time.Time) error {
	if database == nil || operationID == "" {
		return errors.New("operation_id is required")
	}
	if isInFlight != nil && isInFlight(operationID) {
		return fmt.Errorf("operation %s has an active in-memory continuation", operationID)
	}
	sqlDB, err := database.SQLDB(storage.TradeOwner)
	if err != nil {
		return err
	}
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var resID, resState string
	if err := tx.QueryRowContext(ctx, `SELECT id, state FROM canary_risk_reservations WHERE operation_id = ?`, operationID).Scan(&resID, &resState); err != nil {
		return fmt.Errorf("lookup reservation for %s: %w", operationID, err)
	}
	if resState != "reserved" {
		return fmt.Errorf("operation %s reservation state is %s, not reserved", operationID, resState)
	}
	// Trigger manual_pre_send_abort_forbids_signing_intent also enforces this at SQLite layer.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO manual_pre_send_aborts(operation_id, reservation_id, reason, aborted_at) VALUES(?,?,?,?)`,
		operationID, resID, reason, now.UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("record pre-sign abort for %s: %w", operationID, err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE canary_risk_reservations SET state = 'released', updated_at = ? WHERE id = ? AND state = 'reserved'`,
		now.UTC().Format(time.RFC3339Nano), resID,
	); err != nil {
		return fmt.Errorf("release reservation %s: %w", resID, err)
	}
	return tx.Commit()
}

func controlStateTableForChain(chainID uint64) string {
	if chainID == 0 || chainID == 4663 {
		return "canary_control_state"
	}
	return "multichain_control_state"
}

func persistDurableEmergencyStop(ctx context.Context, database *storage.Database, chainID uint64, stopped bool, reason string, now time.Time) error {
	if database == nil {
		return errors.New("database is unavailable")
	}
	sqlDB, err := database.SQLDB(storage.TradeOwner)
	if err != nil {
		// Non-trade services do not own canary_control_state.
		return nil
	}
	tbl := controlStateTableForChain(chainID)
	nowStr := now.UTC().Format(time.RFC3339Nano)
	if stopped {
		_, err = sqlDB.ExecContext(ctx,
			`INSERT INTO `+tbl+`(singleton, chain_id, mode, policy_version, emergency_stopped, reason, authorization_ref, effective_at, updated_at)
			 VALUES(1, ?, 'DISABLED', 1, 1, ?, 'operator-admin-stop', ?, ?)
			 ON CONFLICT(singleton) DO UPDATE SET emergency_stopped = 1, reason = excluded.reason, updated_at = excluded.updated_at`,
			chainID, reason, nowStr, nowStr,
		)
		if err != nil {
			return fmt.Errorf("persist emergency stop in %s: %w", tbl, err)
		}
		return nil
	}
	res, err := sqlDB.ExecContext(ctx,
		`UPDATE `+tbl+` SET emergency_stopped = 0, reason = '', updated_at = ? WHERE singleton = 1`,
		nowStr,
	)
	if err != nil {
		return fmt.Errorf("clear emergency stop in %s: %w", tbl, err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%s row not initialized; cannot resume", tbl)
	}
	return nil
}

func readDurableEmergencyStop(ctx context.Context, database *storage.Database, chainID uint64, control *runtimeEmergencyControl) (bool, string, time.Time, error) {
	if database != nil {
		if sqlDB, err := database.SQLDB(storage.TradeOwner); err == nil {
			tbl := controlStateTableForChain(chainID)
			var stoppedInt int
			var reason, updatedStr string
			err := sqlDB.QueryRowContext(ctx, `SELECT emergency_stopped, reason, updated_at FROM `+tbl+` WHERE singleton = 1`).Scan(&stoppedInt, &reason, &updatedStr)
			if err == nil {
				ts, _ := time.Parse(time.RFC3339Nano, updatedStr)
				return stoppedInt != 0, reason, ts, nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return false, "", time.Time{}, err
			}
		}
	}
	stopped, reason, updated := control.Snapshot()
	return stopped, reason, updated, nil
}

func writeAdminJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

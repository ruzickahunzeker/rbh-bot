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

func newAdminServer(serviceName, socketPath string, chainID uint64, log *slog.Logger, database *storage.Database, control *runtimeEmergencyControl) *health.Server {
	ready := health.NewReadiness("admin_ready")
	ready.Set("admin_ready", true)
	admin := health.New(serviceName+"-admin", socketPath, log, ready)
	admin.SetSocketMode(0o600)

	admin.Handle("GET /admin/v1/control/status", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stopped, reason, updated, err := readDurableEmergencyStop(r.Context(), database, control)
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
	return admin
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
	nowStr := now.UTC().Format(time.RFC3339Nano)
	if stopped {
		_, err = sqlDB.ExecContext(ctx,
			`INSERT INTO canary_control_state(singleton, chain_id, mode, policy_version, emergency_stopped, reason, authorization_ref, effective_at, updated_at)
			 VALUES(1, ?, 'DISABLED', 1, 1, ?, 'operator-admin-stop', ?, ?)
			 ON CONFLICT(singleton) DO UPDATE SET emergency_stopped = 1, reason = excluded.reason, updated_at = excluded.updated_at`,
			chainID, reason, nowStr, nowStr,
		)
		if err != nil {
			return fmt.Errorf("persist emergency stop in canary_control_state: %w", err)
		}
		return nil
	}
	res, err := sqlDB.ExecContext(ctx,
		`UPDATE canary_control_state SET emergency_stopped = 0, reason = '', updated_at = ? WHERE singleton = 1`,
		nowStr,
	)
	if err != nil {
		return fmt.Errorf("clear emergency stop in canary_control_state: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("canary_control_state row not initialized; cannot resume")
	}
	return nil
}

func readDurableEmergencyStop(ctx context.Context, database *storage.Database, control *runtimeEmergencyControl) (bool, string, time.Time, error) {
	if database != nil {
		if sqlDB, err := database.SQLDB(storage.TradeOwner); err == nil {
			var stoppedInt int
			var reason, updatedStr string
			err := sqlDB.QueryRowContext(ctx, `SELECT emergency_stopped, reason, updated_at FROM canary_control_state WHERE singleton = 1`).Scan(&stoppedInt, &reason, &updatedStr)
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

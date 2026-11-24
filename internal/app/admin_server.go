package app

import (
	"encoding/json"
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

func (c *runtimeEmergencyControl) Stop(reason string) {
	c.mu.Lock()
	c.reason = reason
	c.updated = time.Now().UTC()
	c.stopped.Store(true)
	c.mu.Unlock()
}

func (c *runtimeEmergencyControl) Resume() {
	c.mu.Lock()
	c.reason = ""
	c.updated = time.Now().UTC()
	c.stopped.Store(false)
	c.mu.Unlock()
}

func (c *runtimeEmergencyControl) Snapshot() (stopped bool, reason string, updated time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped.Load(), c.reason, c.updated
}

func newAdminServer(serviceName, socketPath string, log *slog.Logger, database *storage.Database, control *runtimeEmergencyControl) *health.Server {
	admin := health.New(serviceName+"-admin", socketPath, log, health.NewReadiness("admin_ready"))
	admin.SetSocketMode(0o600)
	admin.Handle("GET /admin/v1/control/status", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		stopped, reason, updated := control.Snapshot()
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
		control.Stop(body.Reason)
		stopped, reason, updated := control.Snapshot()
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"emergency_stopped": stopped,
			"reason":            reason,
			"updated_at":        updated.Format(time.RFC3339Nano),
		})
	}))
	admin.Handle("POST /admin/v1/control/resume", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		control.Resume()
		stopped, reason, updated := control.Snapshot()
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"emergency_stopped": stopped,
			"reason":            reason,
			"updated_at":        updated.Format(time.RFC3339Nano),
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

func writeAdminJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

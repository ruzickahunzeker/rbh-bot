package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

type Server struct {
	name  string
	path  string
	log   *slog.Logger
	ready atomic.Bool
	http  *http.Server
}

func New(name, path string, log *slog.Logger) *Server {
	s := &Server{name: name, path: path, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /ready", s.readiness)
	mux.HandleFunc("GET /metrics", s.metrics)
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	return s
}

func (s *Server) SetReady(ready bool) { s.ready.Store(ready) }

func (s *Server) ListenAndServe() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	if err := removeStaleSocket(s.path); err != nil {
		return err
	}
	listener, err := net.Listen("unix", s.path)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.path, 0o660); err != nil {
		_ = listener.Close()
		return err
	}
	s.log.Info("internal server listening", "socket", s.path)
	err = s.http.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": s.name})
}

func (s *Server) readiness(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "service": s.name})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "service": s.name})
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	ready := 0
	if s.ready.Load() {
		ready = 1
	}
	_, _ = fmt.Fprintf(w, "# TYPE rbh_service_ready gauge\nrbh_service_ready{service=%q} %d\n", s.name, ready)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket path %s", path)
	}
	return os.Remove(path)
}

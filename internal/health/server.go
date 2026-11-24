package health

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type MetricsWriter func(context.Context, io.Writer) error

type Server struct {
	name          string
	path          string
	socketMode    os.FileMode
	log           *slog.Logger
	ready         *Readiness
	metricsWriter MetricsWriter
	mux           *http.ServeMux
	http          *http.Server
}

func New(name, path string, log *slog.Logger, ready *Readiness) *Server {
	s := &Server{name: name, path: path, socketMode: 0o660, log: log, ready: ready}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /ready", s.readiness)
	mux.HandleFunc("GET /metrics", s.metrics)
	s.mux = mux
	s.http = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

// SetSocketMode sets the Unix domain socket file permission (e.g. 0o600 for admin sockets).
func (s *Server) SetSocketMode(mode os.FileMode) {
	if s != nil && mode != 0 {
		s.socketMode = mode
	}
}

// SetTimeouts overrides HTTP server read/write/idle timeouts before Serve starts.
func (s *Server) SetTimeouts(readTimeout, writeTimeout, idleTimeout time.Duration) {
	if s == nil || s.http == nil {
		return
	}
	if readTimeout > 0 {
		s.http.ReadTimeout = readTimeout
	}
	if writeTimeout > 0 {
		s.http.WriteTimeout = writeTimeout
	}
	if idleTimeout > 0 {
		s.http.IdleTimeout = idleTimeout
	}
}

// Handle registers a service-owned internal endpoint before Serve starts.
func (s *Server) Handle(pattern string, handler http.Handler) {
	if s != nil && s.mux != nil && handler != nil {
		s.mux.Handle(pattern, handler)
	}
}

// SetMetricsWriter must be called before Serve. It allows service-specific
// metrics without coupling the generic health package to service internals.
func (s *Server) SetMetricsWriter(writer MetricsWriter) {
	if s != nil {
		s.metricsWriter = writer
	}
}

func (s *Server) Listen() (net.Listener, error) {
	dirMode := os.FileMode(0o750)
	if s.socketMode == 0o600 {
		// Restrict parent directory for owner-only admin sockets to close the Listen->Chmod window.
		dirMode = 0o700
	}
	if err := os.MkdirAll(filepath.Dir(s.path), dirMode); err != nil {
		return nil, err
	}
	if err := removeStaleSocket(s.path); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", s.path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(s.path, s.socketMode); err != nil {
		_ = listener.Close()
		return nil, err
	}
	s.log.Info("internal server listening", "socket", s.path, "mode", fmt.Sprintf("%04o", s.socketMode))
	return listener, nil
}

func (s *Server) Serve(listener net.Listener) error {
	err := s.http.Serve(listener)
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
	ready, gates := s.ready.Snapshot()
	if !ready {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "service": s.name, "gates": gates})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "service": s.name, "gates": gates})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	value := 0
	if ready, _ := s.ready.Snapshot(); ready {
		value = 1
	}
	_, _ = fmt.Fprintf(&buf, "# TYPE rbh_service_ready gauge\nrbh_service_ready{service=%q} %d\n", s.name, value)
	if s.metricsWriter != nil {
		if err := s.metricsWriter(r.Context(), &buf); err != nil {
			s.log.Error("write service metrics", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
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
	// Probe whether a live process is actively listening before unlinking.
	conn, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("refusing to replace active socket %s", path)
	}
	return os.Remove(path)
}

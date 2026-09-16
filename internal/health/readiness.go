package health

import "sync"

type Readiness struct {
	mu    sync.RWMutex
	gates map[string]bool
}

func NewReadiness(names ...string) *Readiness {
	gates := make(map[string]bool, len(names))
	for _, name := range names {
		gates[name] = false
	}
	return &Readiness{gates: gates}
}

func (r *Readiness) Set(name string, value bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gates[name] = value
}

func (r *Readiness) Snapshot() (bool, map[string]bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ready := true
	gates := make(map[string]bool, len(r.gates))
	for name, value := range r.gates {
		gates[name] = value
		ready = ready && value
	}
	return ready, gates
}

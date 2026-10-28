package tradeapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

type manualSubmissionFixtureTemplate struct {
	fixture manualSubmissionFixture
	db      []byte
}

var manualSubmissionFixtureTemplates = struct {
	sync.Mutex
	values map[string]manualSubmissionFixtureTemplate
}{values: make(map[string]manualSubmissionFixtureTemplate)}

// cloneManualSubmissionFixture keeps the expensive production migration and
// trigger compilation out of per-case corruption tests. VACUUM INTO creates a
// self-contained SQLite copy, so every case still owns an independent durable
// database and can drop/alter its guards without affecting another case.
func cloneManualSubmissionFixture(t *testing.T, source manualSubmissionFixture) manualSubmissionFixture {
	t.Helper()
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if _, err := source.e.verifier.store.db.Exec(`VACUUM INTO ?`, snapshot); err != nil {
		t.Fatal("clone submission fixture", err)
	}
	db, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal("read submission fixture snapshot", err)
	}
	return cloneManualSubmissionFixtureBytes(t, source, db)
}

func cloneManualSubmissionFixtureBytes(t *testing.T, source manualSubmissionFixture, db []byte) manualSubmissionFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trade.db")
	if err := os.WriteFile(path, db, 0o600); err != nil {
		t.Fatal("write submission fixture clone", err)
	}
	d, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	tradeStore, err := trade.NewStore(d)
	if err != nil {
		t.Fatal(err)
	}

	verifier := &manualRuntimeVerifier{
		store:      s,
		profile:    source.e.verifier.profile,
		timeSource: source.e.verifier.timeSource,
		identity:   source.e.verifier.identity,
		decryptor:  source.e.verifier.decryptor,
		observer:   source.e.verifier.observer,
	}
	if clock, ok := source.e.verifier.timeSource.(*runtimeTestClock); ok {
		clock.mu.Lock()
		clockCopy := &runtimeTestClock{wall: clock.wall, ticks: clock.ticks, uncertainty: clock.uncertainty, calls: clock.calls, change: clock.change}
		clock.mu.Unlock()
		verifier.timeSource = clockCopy
	}
	if identity, ok := source.e.verifier.identity.(*runtimeTestIdentity); ok {
		identityCopy := *identity
		verifier.identity = &identityCopy
	}
	if observer, ok := source.e.verifier.observer.(*runtimeTestObserver); ok {
		observerCopy := *observer
		verifier.observer = &observerCopy
	}
	fake := &manualFakeFirstTransport{
		calls:    source.fake.calls,
		borrowed: append([]byte(nil), source.fake.borrowed...),
		rawHash:  source.fake.rawHash,
		result:   source.fake.result,
		err:      source.fake.err,
	}
	executor := &manualSubmissionExecutor{verifier: verifier, store: tradeStore, transport: fake, timeout: source.e.timeout}

	var clock *runtimeTestClock
	if c, ok := verifier.timeSource.(*runtimeTestClock); ok {
		clock = c
	}
	return manualSubmissionFixture{
		d: d, path: path, e: executor, fake: fake, id: source.id,
		request: source.request, permit: source.permit, clock: clock,
	}
}

// manualSubmissionFixtureTemplateFor builds each expensive state/side fixture
// once per test process and keeps only immutable SQLite bytes. Every caller
// still receives a fresh file-backed database and fresh harness objects.
func manualSubmissionFixtureTemplateFor(t *testing.T, state, side string) manualSubmissionFixtureTemplate {
	t.Helper()
	key := state + "\x00" + side
	manualSubmissionFixtureTemplates.Lock()
	defer manualSubmissionFixtureTemplates.Unlock()
	if template, ok := manualSubmissionFixtureTemplates.values[key]; ok {
		return template
	}
	f := newManualSubmissionFixture(t, side)
	switch state {
	case "broadcast_unknown":
		f.fake.result, f.fake.err = manualTransportResult{}, errors.New("ambiguous fixture transport")
	case "known_unsent":
		f.fake.result, f.fake.err = manualTransportResult{deterministicUnsent: true}, nil
	}
	sub, err := f.e.execute(context.Background(), f.id, f.request, f.permit.ID)
	if err != nil && state != "broadcast_unknown" {
		t.Fatalf("submission fixture template: %v", err)
	}
	if sub.State != state {
		t.Fatalf("submission fixture template outcome %s: want %s", sub.State, state)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if _, err := f.e.verifier.store.db.Exec(`VACUUM INTO ?`, snapshot); err != nil {
		t.Fatal("snapshot submission fixture", err)
	}
	db, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal("read submission fixture template", err)
	}
	_ = f.d.Close()
	template := manualSubmissionFixtureTemplate{fixture: f, db: db}
	manualSubmissionFixtureTemplates.values[key] = template
	return template
}

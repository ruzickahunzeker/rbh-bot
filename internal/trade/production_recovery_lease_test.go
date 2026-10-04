package trade

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func TestW4CLeaseChronologicalNanosecondBoundaries(t *testing.T) {
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	for _, fraction := range []time.Duration{0, 700 * time.Millisecond, 700000001 * time.Nanosecond} {
		for _, offset := range []time.Duration{-time.Second, -time.Nanosecond, 0, time.Nanosecond} {
			for _, action := range []string{"renew", "takeover"} {
				t.Run(fmt.Sprintf("%s/fraction=%s/offset=%s", action, fraction, offset), func(t *testing.T) {
					s := canaryStore(t)
					ctx := context.Background()
					expiry := start.Add(time.Second + fraction)
					epoch, err := s.AcquireNextCanaryWorkerLease(ctx, "RECOVERY", "boundary", "old", expiry.Sub(start), start)
					if err != nil || epoch != 1 {
						t.Fatalf("initial epoch=%d err=%v", epoch, err)
					}
					now := expiry.Add(offset)
					if action == "renew" {
						err = s.RenewCanaryWorkerLease(ctx, "RECOVERY", "boundary", "old", epoch, time.Minute, now)
						if offset < 0 && err != nil {
							t.Fatalf("active renewal: %v", err)
						}
						if offset >= 0 && !errors.Is(err, ErrCanaryRecoveryLeaseLost) {
							t.Fatalf("expired renewal: %v", err)
						}
						wantExpiry := expiry
						if offset < 0 {
							wantExpiry = now.Add(time.Minute)
						}
						assertW4CLeaseRow(t, s, "boundary", "old", 1, wantExpiry.Format(time.RFC3339Nano))
					} else {
						epoch, err = s.AcquireNextCanaryWorkerLease(ctx, "RECOVERY", "boundary", "new", time.Minute, now)
						if offset < 0 {
							if !errors.Is(err, ErrCanaryRuntimeRejected) || epoch != 0 {
								t.Fatalf("active takeover epoch=%d err=%v", epoch, err)
							}
							assertW4CLeaseRow(t, s, "boundary", "old", 1, expiry.Format(time.RFC3339Nano))
						} else {
							if err != nil || epoch != 2 {
								t.Fatalf("expired takeover epoch=%d err=%v", epoch, err)
							}
							assertW4CLeaseRow(t, s, "boundary", "new", 2, now.Add(time.Minute).Format(time.RFC3339Nano))
							assertW4CStaleOwner(t, s, "boundary", "old", 1, now)
						}
					}
				})
			}
		}
	}
}

func assertW4CLeaseRow(t *testing.T, s *Store, environment, holder string, epoch uint64, expiry string) {
	t.Helper()
	var gotHolder, gotExpiry string
	var gotEpoch uint64
	if err := s.db.QueryRow(`SELECT holder_id,lease_epoch,expires_at FROM canary_worker_leases WHERE role='RECOVERY' AND environment=?`, environment).Scan(&gotHolder, &gotEpoch, &gotExpiry); err != nil {
		t.Fatal(err)
	}
	if gotHolder != holder || gotEpoch != epoch || gotExpiry != expiry {
		t.Fatalf("lease=(%s,%d,%s), want=(%s,%d,%s)", gotHolder, gotEpoch, gotExpiry, holder, epoch, expiry)
	}
}

func assertW4CStaleOwner(t *testing.T, s *Store, environment, holder string, epoch uint64, now time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := s.RenewCanaryWorkerLease(ctx, "RECOVERY", environment, holder, epoch, time.Minute, now); !errors.Is(err, ErrCanaryRecoveryLeaseLost) {
		t.Fatalf("stale renewal: %v", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fence := &RecoveryLeaseFence{Environment: environment, HolderID: holder, Epoch: epoch, clock: func() time.Time { return now }}
	if err := assertRecoveryLeaseTx(ctx, tx, fence); !errors.Is(err, ErrCanaryRecoveryLeaseLost) {
		t.Fatalf("stale mutation fence: %v", err)
	}
}

func TestW4CLeaseMalformedExpiryAndEpochOverflowFailClosed(t *testing.T) {
	for _, kind := range []string{"malformed_expiry", "epoch_overflow"} {
		t.Run(kind, func(t *testing.T) {
			s := canaryStore(t)
			ctx := context.Background()
			start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
			expiry := start.Add(700 * time.Millisecond).Format(time.RFC3339Nano)
			epoch := uint64(1)
			if _, err := s.AcquireNextCanaryWorkerLease(ctx, "RECOVERY", "bad", "old", 700*time.Millisecond, start); err != nil {
				t.Fatal(err)
			}
			if kind == "malformed_expiry" {
				expiry = "not-a-timestamp"
			} else {
				epoch = 1<<63 - 1
			}
			if _, err := s.db.Exec(`UPDATE canary_worker_leases SET expires_at=?,lease_epoch=? WHERE role='RECOVERY' AND environment='bad'`, expiry, epoch); err != nil {
				t.Fatal(err)
			}
			now := start.Add(time.Second)
			if err := s.RenewCanaryWorkerLease(ctx, "RECOVERY", "bad", "old", epoch, time.Minute, now); !errors.Is(err, ErrCanaryRecoveryLeaseLost) {
				t.Fatalf("invalid renewal: %v", err)
			}
			if next, err := s.AcquireNextCanaryWorkerLease(ctx, "RECOVERY", "bad", "new", time.Minute, now); next != 0 || !errors.Is(err, ErrCanaryRuntimeRejected) {
				t.Fatalf("invalid takeover epoch=%d err=%v", next, err)
			}
			assertW4CLeaseRow(t, s, "bad", "old", epoch, expiry)
		})
	}
}

func TestW4CLeaseConcurrentIndependentConnections(t *testing.T) {
	s := canaryStore(t)
	var seq int
	var name, path string
	if err := s.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	other, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	expiry := start.Add(700 * time.Millisecond)
	for round := 0; round < 16; round++ {
		for _, kind := range []string{"active_renew_vs_takeover", "expired_renew_vs_takeover", "two_takeovers"} {
			env := fmt.Sprintf("race-%d-%s", round, kind)
			if _, err := s.AcquireNextCanaryWorkerLease(ctx, "RECOVERY", env, "old", expiry.Sub(start), start); err != nil {
				t.Fatal(err)
			}
			now := expiry.Add(time.Nanosecond)
			if kind == "active_renew_vs_takeover" {
				now = start // Whole-second now with fractional expiry.
			}
			barrier := make(chan struct{})
			results := make(chan error, 2)
			go func() {
				<-barrier
				if kind == "two_takeovers" {
					_, err := s.AcquireNextCanaryWorkerLease(ctx, "RECOVERY", env, "new-1", time.Minute, now)
					results <- err
				} else {
					results <- s.RenewCanaryWorkerLease(ctx, "RECOVERY", env, "old", 1, time.Minute, now)
				}
			}()
			go func() {
				<-barrier
				_, err := other.AcquireNextCanaryWorkerLease(ctx, "RECOVERY", env, "new-2", time.Minute, now)
				results <- err
			}()
			close(barrier)
			accepted := 0
			for i := 0; i < 2; i++ {
				if <-results == nil {
					accepted++
				}
			}
			if accepted != 1 {
				t.Fatalf("%s: accepted=%d, want exactly one", env, accepted)
			}
			if kind == "active_renew_vs_takeover" {
				assertW4CLeaseRow(t, other, env, "old", 1, now.Add(time.Minute).Format(time.RFC3339Nano))
			} else {
				var holder string
				if err := other.db.QueryRow(`SELECT holder_id FROM canary_worker_leases WHERE role='RECOVERY' AND environment=?`, env).Scan(&holder); err != nil {
					t.Fatal(err)
				}
				if holder != "new-2" && (kind != "two_takeovers" || holder != "new-1") {
					t.Fatalf("unexplained owner: %s", holder)
				}
				assertW4CLeaseRow(t, other, env, holder, 2, now.Add(time.Minute).Format(time.RFC3339Nano))
				assertW4CStaleOwner(t, s, env, "old", 1, now)
			}
		}
	}
}

func TestW4CFractionalLeaseLossStopsReadinessWithAuditAlert(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	query := &productionRecoveryQueryFake{entered: entered, release: release}
	s, c, artifact, start := productionRecoveryCompositionFixture(t, query, nil)
	c.recovery.config.LeaseTTL = 700 * time.Millisecond
	var clock atomic.Int64
	clock.Store(start.UnixNano())
	c.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("query never started")
	}
	clock.Store(start.Add(700*time.Millisecond + time.Nanosecond).UnixNano())
	deadline := time.After(5 * time.Second)
	for c.Readiness().RecoveryReady {
		select {
		case <-deadline:
			t.Fatal("expired lease left recovery ready")
		case <-time.After(time.Millisecond):
		}
	}
	// Wait for the terminal lease-loss alert, before unblocking the in-flight query.
	for {
		var alerts int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM canary_alert_outbox o JOIN canary_runtime_audit a ON a.id=o.audit_id WHERE a.event_type='W4C_PRODUCTION_RECOVERY' AND a.reason_code='RECOVERY_LEASE_LOST'`).Scan(&alerts); err != nil {
			t.Fatal(err)
		}
		if alerts == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("missing durable lease-loss audit/alert")
		case <-time.After(time.Millisecond):
		}
	}
	unblock()
	select {
	case err := <-done:
		if !errors.Is(err, ErrCanaryRecoveryLeaseLost) {
			t.Fatalf("terminal error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lease-loss drain did not finish")
	}
	r := c.Readiness()
	if r.RecoveryReady || r.CanaryAdmissionReady || r.SubmissionSendReady {
		t.Fatalf("lease-loss readiness=%+v", r)
	}
	assertFrozenRecovery(t, s, artifact)
}

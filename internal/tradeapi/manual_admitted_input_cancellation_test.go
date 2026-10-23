package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

// Regression for the independent review's public Load commit-window failure.
// A single-connection pool provides a barrier: the competing SELECT completes
// only after database/sql's automatic rollback releases the canceled read tx.
// No sleeps, production hooks, writes, HTTP route or economic dependency.
func TestHTTPM1HardeningPublicLateCancellationPreservesCause(t *testing.T) {
	for _, kind := range []string{"cancel", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, scope, _ := manualInputsFixture(t, "sell_percent")
			if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			s.db.SetMaxOpenConns(1)
			s.db.SetMaxIdleConns(1)
			if _, err := s.db.Exec(`PRAGMA query_only=ON`); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			a := admittedInputReader(t, s, scope)
			if _, err := a.Load(context.Background(), h.identity, r.RequestID); err != nil {
				t.Fatal("public read-only success", err)
			}
			var ctx context.Context
			var cancel context.CancelFunc
			want := context.Canceled
			if kind == "deadline" {
				ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
				want = context.DeadlineExceeded
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			clocks := 0
			a.now = func() time.Time {
				clocks++
				if clocks == 2 {
					if kind == "cancel" {
						cancel()
					}
					<-ctx.Done()
					barrier, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					var one int
					if err := s.db.QueryRowContext(barrier, `SELECT 1`).Scan(&one); err != nil || one != 1 {
						t.Fatal("automatic rollback barrier", one, err)
					}
				}
				return testNow
			}
			v, err := a.Load(ctx, h.identity, r.RequestID)
			if clocks != 2 || !errors.Is(ctx.Err(), want) || !errors.Is(err, want) || !errors.Is(err, sql.ErrTxDone) || !reflect.DeepEqual(v, ManualAdmittedInput{}) {
				t.Fatalf("commit cancellation cause/output: clocks=%d context=%v err=%v candidate=%+v", clocks, ctx.Err(), err, v)
			}
			requireOperational503(t, err)
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("canceled commit changed durable state")
			}
			// Failure must not poison the connection or future fresh reads.
			a.now = h.now
			if _, err := a.Load(context.Background(), h.identity, r.RequestID); err != nil {
				t.Fatal("fresh read unavailable after cancellation", err)
			}
			assertAtomicAdmission(t, s, r, 1)
		})
	}
}

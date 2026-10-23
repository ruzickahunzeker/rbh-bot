package tradeapi

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func runtimeClockStore(t *testing.T) (*storage.Database, *Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clock.db")
	d, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err = d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	return d, s, path
}

func runtimeClockExec(t *testing.T, s *Store, gen int64, high string, fault int, reason string, at any, stamp string) error {
	t.Helper()
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return insertManualClockEvent(context.Background(), conn, "local", gen, high, fault, reason, at, stamp)
}

const runtimeClockStamp = "2026-10-17T00:00:00Z"

func TestHTTPM4B22ClockDirectSQLImmutableAuditAndLatch(t *testing.T) {
	d, s, path := runtimeClockStore(t)
	if err := runtimeClockExec(t, s, 1, runtimeClockStamp, 0, "", nil, runtimeClockStamp); err != nil {
		t.Fatal(err)
	}
	if err := runtimeClockExec(t, s, 2, "2026-10-17T00:00:00.000000001Z", 0, "", nil, "2026-10-17T00:00:00.000000001Z"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE manual_gate_clock_events SET generation=generation`,
		`DELETE FROM manual_gate_clock_events`,
		`INSERT INTO manual_gate_clock_empty VALUES(1)`,
		`INSERT OR REPLACE INTO manual_gate_clock_events SELECT * FROM manual_gate_clock_events`,
		`INSERT OR REPLACE INTO canary_runtime_audit(rowid,id,event_type,reason_code,details_json,created_at) SELECT rowid,'victim','outside','outside','{}',created_at FROM canary_runtime_audit WHERE event_type='MANUAL_SUBMISSION_CLOCK' LIMIT 1`,
		`UPDATE canary_alert_outbox SET severity='CRITICAL' WHERE id LIKE 'manual-clock-alert:%'`,
		`DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-clock-alert:%'`,
	} {
		before := admittedDatabaseSnapshot(t, s.db)
		if _, err := s.db.Exec(q); err == nil || before != admittedDatabaseSnapshot(t, s.db) {
			t.Fatal("direct mutation succeeded", q, err)
		}
	}
	if err := runtimeClockExec(t, s, 3, "2026-10-17T00:00:00.000000001Z", 1, "CLOCK_REVERSAL", runtimeClockStamp, runtimeClockStamp); err != nil {
		t.Fatal(err)
	}
	if count(t, s.db, "manual_gate_clock_complete") != 3 || count(t, s.db, "canary_runtime_audit") != 3 || count(t, s.db, "canary_alert_outbox") != 3 {
		t.Fatal("transition not exactly audited")
	}
	_, s = bindingReopen(t, d, path)
	for _, fault := range []int{0, 1} {
		if err := runtimeClockExec(t, s, 4, "2026-10-17T00:00:00.000000001Z", fault, "", nil, "2026-10-17T00:00:00.000000001Z"); err == nil {
			t.Fatal("restart cleared latch")
		}
	}
	var latched int
	if err := s.db.QueryRow(`SELECT fault_latched FROM manual_gate_clock_state WHERE environment='local'`).Scan(&latched); err != nil || latched != 1 {
		t.Fatal(latched, err)
	}
}

func TestHTTPM4B22ClockDirectSQLCanonicalAndGenerationFailClosed(t *testing.T) {
	_, s, _ := runtimeClockStore(t)
	if err := runtimeClockExec(t, s, 1, runtimeClockStamp, 0, "", nil, runtimeClockStamp); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"backward", "gap", "duplicate", "format", "blob", "NUL", "clear_identity", "rowid", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			var gen any = int64(2)
			var high any = runtimeClockStamp
			var env any = "local"
			var rowid any = nil
			switch mode {
			case "backward":
				high = "2026-10-16T23:59:59.999999999Z"
			case "gap":
				gen = int64(3)
			case "duplicate":
				gen = int64(1)
			case "format":
				high = "2026-10-17T00:00:00+00:00"
			case "blob":
				high = []byte(runtimeClockStamp)
			case "NUL":
				env = "local\x00"
			case "clear_identity":
				env = []byte("local")
			case "rowid":
				rowid = int64(1)
			case "overflow":
				gen = "9223372036854775808"
			}
			before := admittedDatabaseSnapshot(t, s.db)
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE", "INSERT OR FAIL"} {
				_, err := s.db.Exec(verb+` INTO manual_gate_clock_events(rowid,id,chain_id,environment,generation,high_water_at,fault_latched,fault_reason,fault_at,updated_at)
 VALUES(?,'manual-clock:'||manual_artifact_bytes_sha256(hex(json_object('chain_id',4663,'environment',?,'generation',?))),4663,?,?,?,0,'',NULL,?)`, rowid, env, gen, env, gen, high, high)
				if err == nil || before != admittedDatabaseSnapshot(t, s.db) {
					t.Fatal("encoded/generation invalid accepted", mode, verb, err)
				}
			}
		})
	}
}

func TestHTTPM4B22ClockWitnessFaultCannotRepairAcrossStatements(t *testing.T) {
	for _, table := range []string{"canary_runtime_audit", "canary_alert_outbox"} {
		for _, action := range []string{"ABORT", "IGNORE", "FAIL"} {
			t.Run(table+"/"+action, func(t *testing.T) {
				_, s, _ := runtimeClockStore(t)
				if err := runtimeClockExec(t, s, 1, runtimeClockStamp, 0, "", nil, runtimeClockStamp); err != nil {
					t.Fatal(err)
				}
				field, match := "event_type", "MANUAL_SUBMISSION_CLOCK"
				if table == "canary_alert_outbox" {
					field, match = "id", "manual-clock-alert:%"
				}
				raise := action
				if action != "IGNORE" {
					raise += ",'clock witness fault'"
				}
				if _, err := s.db.Exec(`CREATE TRIGGER clock_fault BEFORE INSERT ON ` + table + ` WHEN NEW.` + field + ` LIKE '` + match + `' AND NOT EXISTS(SELECT 1 FROM canary_runtime_audit WHERE id='fault-cleared') BEGIN SELECT RAISE(` + raise + `); END`); err != nil {
					t.Fatal(err)
				}
				before := admittedDatabaseSnapshot(t, s.db)
				for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE", "INSERT OR FAIL"} {
					conn, err := s.db.Conn(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					if _, err = conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
						t.Fatal(err)
					}
					_, err = conn.ExecContext(context.Background(), verb+` INTO manual_gate_clock_events VALUES('manual-clock:'||manual_artifact_bytes_sha256(hex(json_object('chain_id',4663,'environment','local','generation',2))),4663,'local',2,?,0,'',NULL,?)`, runtimeClockStamp, runtimeClockStamp)
					var parents, scopes int
					if scanErr := conn.QueryRowContext(context.Background(), `SELECT (SELECT COUNT(*) FROM manual_gate_clock_events),(SELECT COUNT(*) FROM manual_gate_clock_scopes)`).Scan(&parents, &scopes); scanErr != nil {
						t.Fatal(scanErr)
					}
					if action == "FAIL" {
						// FAIL may retain staging; later repair must remain uncommittable.
						if parents != 1 || scopes != 1 {
							t.Fatal("unexpected fault window", action, parents, scopes, err)
						}
						if _, e := conn.ExecContext(context.Background(), `INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at) VALUES('fault-cleared','TEST','cleared','{}',?)`, runtimeClockStamp); e != nil {
							t.Fatal(e)
						}
						if table == "canary_runtime_audit" {
							if _, e := conn.ExecContext(context.Background(), `INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at) SELECT 'manual-clock-audit:'||id,'MANUAL_SUBMISSION_CLOCK','CLOCK_STATE_METADATA_NOT_PERMISSION',details_json,created_at FROM manual_gate_clock_scopes`); e != nil {
								t.Fatal(e)
							}
						}
						if _, e := conn.ExecContext(context.Background(), `INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) SELECT 'manual-clock-alert:'||id,'manual-clock-audit:'||id,severity,'PENDING',created_at FROM manual_gate_clock_scopes`); e != nil {
							t.Fatal(e)
						}
						if _, e := conn.ExecContext(context.Background(), `DELETE FROM manual_gate_clock_scopes`); e == nil {
							t.Fatal("late repair closed failed scope")
						}
						_, e := conn.ExecContext(context.Background(), `COMMIT`)
						var coded interface{ Code() int }
						if e == nil || !errors.As(e, &coded) || coded.Code() != 787 {
							t.Fatal("late repaired scope committed", e)
						}
					} else if err == nil || parents != 1 || scopes != 0 {
						t.Fatal("ABORT left scope", parents, scopes, err)
					}
					if _, err = conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
						t.Fatal(err)
					}
					_ = conn.Close()
					if before != admittedDatabaseSnapshot(t, s.db) {
						t.Fatal("fault rollback incomplete")
					}
				}
			})
		}
	}
}

func TestHTTPM4B22ClockTwoHandleConservationAndNoLatestFallback(t *testing.T) {
	_, s, path := runtimeClockStore(t)
	if err := runtimeClockExec(t, s, 1, runtimeClockStamp, 0, "", nil, runtimeClockStamp); err != nil {
		t.Fatal(err)
	}
	d2, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	s2, err := NewStore(d2)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			store := s
			if i%2 != 0 {
				store = s2
			}
			conn, e := store.db.Conn(context.Background())
			if e != nil {
				results <- e
				return
			}
			defer conn.Close()
			results <- insertManualClockEvent(context.Background(), conn, "local", 2, runtimeClockStamp, 0, "", nil, runtimeClockStamp)
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !strings.Contains(err.Error(), "clock") && !strings.Contains(strings.ToLower(err.Error()), "locked") && !strings.Contains(strings.ToLower(err.Error()), "busy") {
			t.Fatal("unexplained contender", err)
		}
	}
	if accepted != 1 || count(t, s.db, "manual_gate_clock_events") != 2 || count(t, s.db, "canary_runtime_audit") != 2 {
		t.Fatal("clock overcommit", accepted)
	}
	seedSigningWitnessCorruption(t, s, "canary_runtime_audit", `DELETE FROM canary_runtime_audit WHERE event_type='MANUAL_SUBMISSION_CLOCK' AND json_extract(details_json,'$.generation')=2`)
	if count(t, s.db, "manual_gate_clock_state") != 0 {
		t.Fatal("fell back from corrupt latest generation")
	}
	if err := runtimeClockExec(t, s, 3, runtimeClockStamp, 0, "", nil, runtimeClockStamp); err == nil {
		t.Fatal("repaired corrupt latest")
	}
}

func TestHTTPM4B22ClockActualProcessCrashWindows(t *testing.T) {
	if path := os.Getenv("RBH_B22_CLOCK_CHILD_PATH"); path != "" {
		d, err := storage.Open(context.Background(), storage.TradeOwner, path)
		if err != nil {
			t.Fatal(err)
		}
		s, err := NewStore(d)
		if err != nil {
			t.Fatal(err)
		}
		stage := os.Getenv("RBH_B22_CLOCK_CHILD_STAGE")
		if stage == "before_writer" {
			os.Exit(77)
		}
		conn, err := s.db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
			t.Fatal(err)
		}
		if err = insertManualClockEvent(context.Background(), conn, "local", 2, runtimeClockStamp, 0, "", nil, runtimeClockStamp); err != nil {
			t.Fatal(err)
		}
		if stage == "after_commit" {
			if _, err = conn.ExecContext(context.Background(), `COMMIT`); err != nil {
				t.Fatal(err)
			}
		}
		os.Exit(77)
	}
	for _, stage := range []string{"before_writer", "before_commit", "after_commit"} {
		t.Run(stage, func(t *testing.T) {
			d, s, path := runtimeClockStore(t)
			if err := runtimeClockExec(t, s, 1, runtimeClockStamp, 0, "", nil, runtimeClockStamp); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestHTTPM4B22ClockActualProcessCrashWindows$")
			cmd.Env = append(os.Environ(), "RBH_B22_CLOCK_CHILD_PATH="+path, "RBH_B22_CLOCK_CHILD_STAGE="+stage)
			out, err := cmd.CombinedOutput()
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 77 {
				t.Fatal("child window failed", err, string(out))
			}
			reopened, err := storage.Open(context.Background(), storage.TradeOwner, path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			s, err = NewStore(reopened)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if stage == "after_commit" {
				want = 2
			}
			if count(t, s.db, "manual_gate_clock_complete") != want || count(t, s.db, "manual_gate_clock_scopes") != 0 || count(t, s.db, "canary_runtime_audit") != want {
				t.Fatal("crash retained partial work", stage)
			}
		})
	}
}

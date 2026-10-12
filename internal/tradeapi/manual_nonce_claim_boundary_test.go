package tradeapi

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
)

func TestHTTPM3B2ControlRaceBeforeWriterRejectsAndCommitWindowFences(t *testing.T) {
	for _, update := range []string{`UPDATE canary_control_state SET emergency_stopped=1`, `UPDATE canary_runtime_authorizations SET state='REVOKED'`} {
		t.Run("before_writer_"+update, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			w := nonceWriter(s, &localNonceObserver{})
			w.checkpoint = func(at string) error {
				if at == "after_observation" {
					_, err := s.db.Exec(update)
					return err
				}
				return nil
			}
			v, err := w.claim(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || count(t, s.db, "manual_nonce_claims") != 0 || count(t, s.db, "transaction_attempts") != 0 {
				t.Fatal("stale snapshot granted claim", v, err)
			}
			assertBinding(t, s, r, 1)
		})
		t.Run("commit_window_"+update, func(t *testing.T) {
			_, s, h, r, scope, path := boundSigningFixture(t, "buy")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			entered, allow := make(chan struct{}), make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(allow) })
			w := nonceWriter(s, &localNonceObserver{})
			w.checkpoint = func(at string) error {
				if at == "before_commit" {
					close(entered)
					<-allow
				}
				return nil
			}
			done := make(chan error, 1)
			go func() { _, e := w.claim(context.Background(), h.identity, r.RequestID, scope); done <- e }()
			select {
			case <-entered:
			case e := <-done:
				t.Fatal("claim failed before barrier", e)
			case <-time.After(10 * time.Second):
				t.Fatal("claim did not reach barrier")
			}
			conn, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err = conn.ExecContext(context.Background(), `PRAGMA busy_timeout=0`); err != nil {
				t.Fatal(err)
			}
			if _, err = conn.ExecContext(context.Background(), update); err == nil || !strings.Contains(err.Error(), "SQLITE_BUSY") {
				t.Fatal("control committed inside claim transaction", err)
			}
			release.Do(func() { close(allow) })
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			if _, err = conn.ExecContext(context.Background(), update); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			v, err := w.claim(context.Background(), h.identity, r.RequestID, scope)
			if strings.Contains(update, "emergency_stopped") {
				if inputFailureCode(err) != "EMERGENCY_STOPPED" || v.Status != "" {
					t.Fatal(v, err)
				}
			} else if err != nil || !v.Duplicate || v.SigningAuthorized || v.SendAuthorized {
				t.Fatal(v, err)
			}
			if admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("control path repaired/reallocated claim")
			}
			assertNonceClaim(t, s, r, "0")
		})
	}
}

func TestHTTPM3B2CancellationQueryFailureAndFinalClockRollback(t *testing.T) {
	for _, mode := range []string{"query_error", "query_cancel", "query_deadline", "cancel_before_commit", "deadline_before_commit", "reversal", "stale_after_insert", "auth_expiry"} {
		t.Run(mode, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			if mode == "auth_expiry" {
				if _, err := s.db.Exec(`DROP TRIGGER canary_runtime_authorization_identity_immutable; UPDATE canary_runtime_authorizations SET expires_at=?`, testNow.Add(time.Second).Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
			}
			before := admittedDatabaseSnapshot(t, s.db)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := error(nil)
			o := &localNonceObserver{}
			switch mode {
			case "query_error":
				want = errors.New("query unavailable")
				o.observe = func(context.Context, manualnonce.Facts) (manualnonce.Observation, error) {
					return manualnonce.Observation{}, want
				}
			case "query_cancel":
				want = context.Canceled
				o.observe = func(context.Context, manualnonce.Facts) (manualnonce.Observation, error) {
					cancel()
					return manualnonce.Observation{}, want
				}
			case "query_deadline":
				want = context.DeadlineExceeded
				o.observe = func(ctx context.Context, _ manualnonce.Facts) (manualnonce.Observation, error) {
					<-ctx.Done()
					return manualnonce.Observation{}, ctx.Err()
				}
			case "cancel_before_commit":
				want = context.Canceled
			case "deadline_before_commit":
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				want = context.DeadlineExceeded
			}
			w := nonceWriter(s, o)
			w.checkpoint = func(at string) error {
				if at == "before_commit" {
					switch mode {
					case "cancel_before_commit":
						cancel()
					case "deadline_before_commit":
						<-ctx.Done()
					case "reversal":
						w.now = func() time.Time { return testNow.Add(-time.Nanosecond) }
					case "stale_after_insert":
						w.now = func() time.Time { return testNow.Add(3 * time.Second) }
					case "auth_expiry":
						w.now = func() time.Time { return testNow.Add(time.Second) }
					}
				}
				return nil
			}
			v, err := w.claim(ctx, h.identity, r.RequestID, scope)
			if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("cancellation/clock committed effects", v, err)
			}
			if want != nil && !errors.Is(err, want) {
				t.Fatal("operational cause lost", want, err)
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				requireOperational503(t, err)
			}
		})
	}
}

func TestHTTPM3B2DurablePhasePollutionFailsWithoutRepair(t *testing.T) {
	for _, kind := range []string{"claim_digest", "attempt_fee", "attempt_tip", "attempt_nonce", "attempt_raw", "step_parameters", "step_phase", "lane_nonce", "old_audit", "new_audit", "alert_missing", "usage_total"} {
		t.Run(kind, func(t *testing.T) {
			d, s, h, r, scope, path := boundSigningFixture(t, "buy")
			if _, err := nonceWriter(s, &localNonceObserver{}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			// Explicit corruption injection only in disposable fixtures, not a
			// reachable SQL bypass: production keeps all of these guards intact.
			if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
				t.Fatal(err)
			}
			rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='trigger'`)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for rows.Next() {
				var n string
				if err = rows.Scan(&n); err != nil {
					t.Fatal(err)
				}
				names = append(names, n)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			for _, n := range names {
				if _, err = s.db.Exec(`DROP TRIGGER "` + n + `"`); err != nil {
					t.Fatal(err)
				}
			}
			query := map[string]string{
				"claim_digest":    `UPDATE manual_nonce_claims SET observation_hash='bad'`,
				"attempt_fee":     `UPDATE transaction_attempts SET gas_fee_cap='1'`,
				"attempt_tip":     `UPDATE transaction_attempts SET gas_tip_cap='1'`,
				"attempt_nonce":   `UPDATE transaction_attempts SET nonce='1'`,
				"attempt_raw":     `UPDATE transaction_attempts SET encrypted_raw_tx=X'01'`,
				"step_parameters": `UPDATE execution_steps SET parameters_json='{}' WHERE kind='manual_trade_execution'`,
				"step_phase":      `UPDATE execution_steps SET status='signing' WHERE kind='manual_trade_execution'`,
				"lane_nonce":      `UPDATE execution_wallet_lanes SET reserved_nonce='1'`,
				"old_audit":       `DELETE FROM canary_runtime_audit WHERE event_type='MANUAL_EXECUTION_BOUND'`,
				"new_audit":       `DELETE FROM canary_runtime_audit WHERE event_type='MANUAL_NONCE_CLAIMED'`,
				"alert_missing":   `DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-nonce-alert:%'`,
				"usage_total":     `UPDATE canary_authorization_usage SET total_input='0'`,
			}[kind]
			if _, err = s.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			_, s = bindingReopen(t, d, path)
			o := &localNonceObserver{nonce: "9"}
			v, err := nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || o.calls.Load() != 0 || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("pollution adopted/repaired/resigned", kind, v, err)
			}
		})
	}
}

func TestHTTPM3B2BudgetEqualityRetainedFreezeAndProhibitedSQL(t *testing.T) {
	_, s, h, r, scope, _ := manualInputsFixtureBudget(t, "buy", 1, "10000000000000000")
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := nonceWriter(s, &localNonceObserver{}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE canary_risk_reservations SET state='released'`, `UPDATE canary_risk_reservations SET state='committed'`,
		`UPDATE canary_authorization_operation_usage SET state='RELEASED'`, `UPDATE canary_authorization_operation_usage SET state='CONSUMED'`,
		`INSERT INTO transaction_submissions(id,attempt_id,tx_hash,sequence,state,created_at,updated_at) SELECT 'bypass',attempt_id,'0x00',1,'submitted',checked_at,checked_at FROM manual_nonce_claims`,
		`INSERT INTO receipt_observations(id,attempt_id,chain_id,tx_hash,block_number,block_hash,receipt_status,canonical_state,reconciliation_version,observed_at,updated_at) SELECT 'receipt',attempt_id,4663,'0x00',1,'0x00',1,'observed',1,checked_at,checked_at FROM manual_nonce_claims`,
	} {
		before := admittedDatabaseSnapshot(t, s.db)
		if _, err := s.db.Exec(query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("prohibited effect/release allowed", query, err)
		}
	}
	if _, err := s.db.Exec(`UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_authorization_operation_usage SET state='FROZEN'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE canary_risk_reservations SET state='reserved'`); err == nil {
		t.Fatal("frozen risk thawed")
	}
	if _, err := s.db.Exec(`UPDATE canary_authorization_operation_usage SET state='RESERVED'`); err == nil {
		t.Fatal("frozen usage thawed")
	}
	before := admittedDatabaseSnapshot(t, s.db)
	v, err := nonceWriter(s, &localNonceObserver{nonce: "9"}).claim(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || !v.Duplicate || v.Nonce != "0" || v.SigningAuthorized || v.SendAuthorized || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("freeze history changed claim", v, err)
	}
	assertNonceClaim(t, s, r, "0")
}

func TestHTTPM3B2LegacyNonceConflictAndRetainedPollution(t *testing.T) {
	t.Run("legacy_nonce_without_attempt_wallet_metadata", func(t *testing.T) {
		_, s, h, r, scope, _ := bindingFixture(t, "buy")
		bindingLegacyReferences(t, s)
		// A retired other step with operation wallet identity only must still
		// exclude reuse of its observed nonce. These are inert legacy facts.
		if _, err := s.db.Exec(`UPDATE operations SET wallet_id=? WHERE id='alias-operation';
 UPDATE execution_steps SET status='failed' WHERE id='alias-step';
 INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('legacy-used','alias-step','0','failed','2026-01-01T00:00:00Z')`, h.identity.WalletID); err != nil {
			t.Fatal(err)
		}
		if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
			t.Fatal(err)
		}
		before := admittedDatabaseSnapshot(t, s.db)
		v, err := nonceWriter(s, &localNonceObserver{}).claim(context.Background(), h.identity, r.RequestID, scope)
		if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("legacy nonce reused", v, err)
		}
	})
	t.Run("actual_021_address_only_pollution", func(t *testing.T) {
		d, _, h, r, scope, path := bindingRetainedLaneFixture(t)
		_, s := bindingReopen(t, d, path)
		before := admittedDatabaseSnapshot(t, s.db)
		o := &localNonceObserver{}
		v, err := nonceWriter(s, o).claim(context.Background(), h.identity, r.RequestID, scope)
		if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || o.calls.Load() != 0 || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("retained pollution adopted/queried", v, err)
		}
	})
}

func TestHTTPM3B2DirectSQLInvalidClaimIdentityAndEvidence(t *testing.T) {
	for _, field := range []string{"Step", "Attempt", "Wallet", "Address", "Nonce", "Reservation", "ContractHash", "ObservationRaw", "ObservationHash", "CheckedAt"} {
		t.Run(field, func(t *testing.T) {
			_, s, h, r, _, _ := boundSigningFixture(t, "buy")
			q := nonceCommand(t, s, h, r, "0")
			reflect.ValueOf(&q).Elem().FieldByName(field).SetString("invalid")
			before := admittedDatabaseSnapshot(t, s.db)
			if err := insertNonceSQL(context.Background(), s.db, q, "INSERT"); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("invalid SQL command accepted/half written", field, err)
			}
		})
	}
}

func TestHTTPM3B2EntryTTLBoundaryAndNoObserverOnDenial(t *testing.T) {
	for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		t.Run(offset.String(), func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			expiry, _ := canonicalManualTime(r.ExpiresAt)
			now := expiry.Add(offset)
			o := &localNonceObserver{change: func(v *manualnonce.Observation) { v.ObservedAt = now.Format(time.RFC3339Nano) }}
			w := nonceWriter(s, o)
			w.now = func() time.Time { return now }
			before := admittedDatabaseSnapshot(t, s.db)
			v, err := w.claim(context.Background(), h.identity, r.RequestID, scope)
			if offset < 0 {
				if err != nil || v.Nonce != "0" {
					t.Fatal("valid pre-expiry boundary rejected", v, err)
				}
				assertNonceClaim(t, s, r, "0")
			} else if err == nil || o.calls.Load() != 0 || !reflect.DeepEqual(v, manualNonceClaimResult{}) || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("expired admission observed/claimed nonce", v, err)
			}
		})
	}
}

func TestHTTPM3B2PublicLateReadCancellationPreservesCause(t *testing.T) {
	for _, kind := range []string{"cancel", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			s.db.SetMaxOpenConns(1)
			s.db.SetMaxIdleConns(1)
			before := admittedDatabaseSnapshot(t, s.db)
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if kind == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
				want = context.DeadlineExceeded
			}
			defer cancel()
			o := &localNonceObserver{}
			w := nonceWriter(s, o)
			clocks := 0
			w.now = func() time.Time {
				clocks++
				if clocks == 3 {
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
			v, err := w.claim(ctx, h.identity, r.RequestID, scope)
			if clocks != 3 || !errors.Is(err, want) || !errors.Is(err, sql.ErrTxDone) || !reflect.DeepEqual(v, manualNonceClaimResult{}) || o.calls.Load() != 0 || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("late canceled read lost cause or claimed", clocks, v, err)
			}
			requireOperational503(t, err)
			w.now = func() time.Time { return testNow }
			v, err = w.claim(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || v.Duplicate || v.Nonce != "0" {
				t.Fatal("fresh claim unavailable after canceled read", v, err)
			}
			assertNonceClaim(t, s, r, "0")
		})
	}
}

func TestHTTPM3B2ClaimedOwnerOnlyAttemptAliasRejectedAndNotRepaired(t *testing.T) {
	_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
	if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	bindingLegacyReferences(t, s)
	if _, err := s.db.Exec(`UPDATE operations SET wallet_id=(SELECT wallet_id FROM manual_nonce_claims) WHERE id='alias-operation'`); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	query := `INSERT INTO transaction_attempts(id,step_id,nonce,status,created_at) VALUES('owner-only-alias','alias-step','23','failed','2026-01-01T00:00:00Z')`
	if _, err := s.db.Exec(query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("owner-only claimed nonce alias inserted", err)
	}
	// Inject retained pollution solely to check phase-aware negative completeness
	// and no implicit repair; this is not a reachable bypass of the guard above.
	if _, err := s.db.Exec(`DROP TRIGGER manual_execution_no_attempt`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(query); err != nil {
		t.Fatal(err)
	}
	before = admittedDatabaseSnapshot(t, s.db)
	v, err := nonceWriter(s, &localNonceObserver{}).claim(context.Background(), h.identity, r.RequestID, scope)
	if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("retained alias accepted/repaired", v, err)
	}
	for _, repair := range []string{`DELETE FROM transaction_attempts WHERE id='owner-only-alias'`, `UPDATE transaction_attempts SET nonce='24' WHERE id='owner-only-alias'`} {
		if _, err := s.db.Exec(repair); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("retained alias hidden by repair", repair, err)
		}
	}
}

func TestHTTPM3B2ClockMonotonicAcrossReadObservationWriter(t *testing.T) {
	t.Run("intermediate_read_recovers", func(t *testing.T) {
		_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
		future := testNow.Add(100 * time.Millisecond)
		o := &localNonceObserver{change: func(v *manualnonce.Observation) { v.ObservedAt = future.Format(time.RFC3339Nano) }}
		w := nonceWriter(s, o)
		clocks := 0
		w.now = func() time.Time {
			clocks++
			if clocks == 2 {
				return testNow
			}
			return future
		}
		before := admittedDatabaseSnapshot(t, s.db)
		v, err := w.claim(context.Background(), h.identity, r.RequestID, scope)
		if err == nil || o.calls.Load() != 0 || !reflect.DeepEqual(v, manualNonceClaimResult{}) || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("recovered intermediate clock reversal reached claim", v, err)
		}
	})
	t.Run("read_entry", func(t *testing.T) {
		_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
		o := &localNonceObserver{}
		w := nonceWriter(s, o)
		clocks := 0
		w.now = func() time.Time {
			clocks++
			if clocks == 1 {
				return testNow.Add(time.Second)
			}
			return testNow
		}
		before := admittedDatabaseSnapshot(t, s.db)
		v, err := w.claim(context.Background(), h.identity, r.RequestID, scope)
		if err == nil || o.calls.Load() != 0 || !reflect.DeepEqual(v, manualNonceClaimResult{}) || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("read-entry reversal reached observer/claim", v, err)
		}
	})
	for _, boundary := range []string{"after_read", "after_observation", "before_insert"} {
		t.Run(boundary, func(t *testing.T) {
			_, s, h, r, scope, _ := boundSigningFixture(t, "buy")
			future := testNow.Add(100 * time.Millisecond)
			o := &localNonceObserver{change: func(v *manualnonce.Observation) { v.ObservedAt = future.Format(time.RFC3339Nano) }}
			w := nonceWriter(s, o)
			w.now = func() time.Time { return future }
			w.checkpoint = func(at string) error {
				if at == boundary {
					w.now = func() time.Time { return testNow }
				}
				return nil
			}
			before := admittedDatabaseSnapshot(t, s.db)
			v, err := w.claim(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || !reflect.DeepEqual(v, manualNonceClaimResult{}) || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("cross-stage clock reversal allowed claim", boundary, v, err)
			}
		})
	}
}

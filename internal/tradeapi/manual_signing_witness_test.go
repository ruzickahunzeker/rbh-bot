package tradeapi

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func signingWitnessFixture(t *testing.T) (*storage.Database, *Store, *API, Record, manualAdmissionScope, string, signingIntentCommand) {
	t.Helper()
	d, s, h, r, scope, path := boundSigningFixture(t, "buy")
	if _, err := nonceWriter(s, &localNonceObserver{nonce: "23"}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	q := signingIntentFor(t, s, h, r, scope)
	return d, s, h, r, scope, path, q
}

// Controlled retained-corruption injection ONLY. Every original table trigger
// and FK setting is restored before testing reads/writes or reopening the file.
// This does not claim the intact schema allows the seed operation.
func seedSigningWitnessCorruption(t *testing.T, s *Store, table, query string, args ...any) {
	t.Helper()
	s.db.SetMaxOpenConns(1)
	rows, err := s.db.Query(`SELECT name,sql FROM sqlite_master WHERE type='trigger' AND tbl_name=? ORDER BY name`, table)
	if err != nil {
		t.Fatal(err)
	}
	type definition struct{ name, sql string }
	var guards []definition
	for rows.Next() {
		var g definition
		if err := rows.Scan(&g.name, &g.sql); err != nil {
			t.Fatal(err)
		}
		guards = append(guards, g)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, g := range guards {
		if _, err := s.db.Exec(`DROP TRIGGER "` + strings.ReplaceAll(g.name, `"`, `""`) + `"`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	for _, g := range guards {
		if _, err := s.db.Exec(g.sql); err != nil {
			t.Fatal(err)
		}
		var restored string
		if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name=?`, g.name).Scan(&restored); err != nil || restored != g.sql {
			t.Fatal("fixture failed to restore exact guard", g.name, err)
		}
	}
}

func assertSigningWitnessIncomplete(t *testing.T, s *Store, h *API, r Record, scope manualAdmissionScope) {
	t.Helper()
	var fk, recursive int
	if err := s.db.QueryRow(`SELECT (SELECT foreign_keys FROM pragma_foreign_keys),(SELECT recursive_triggers FROM pragma_recursive_triggers)`).Scan(&fk, &recursive); err != nil || fk != 1 || recursive != 0 {
		t.Fatal("fixture not under restored FK/nonrecursive controls", fk, recursive, err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	v, err := loadManualSigningIntentHistory(context.Background(), s.db, h.identity, r.RequestID, scope)
	if err == nil || !reflect.DeepEqual(v, manualSigningIntentHistory{}) || count(t, s.db, "manual_signing_intent_complete") != 0 || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("retained pollution became history or was repaired", err)
	}
	assertNonceClaim(t, s, r, "23")
}

func TestHTTPM3B3WitnessOwnerFullBytesAllVerbs(t *testing.T) {
	_, s, _, _, _, _, original := signingWitnessFixture(t)
	owners := []any{strings.Repeat("a", 32) + "\x00" + strings.Repeat("a", 31), "\x00" + strings.Repeat("a", 63), strings.Repeat("a", 63) + "\x00",
		strings.Repeat("a", 63) + "g", strings.Repeat("a", 63) + "A", strings.Repeat("a", 62) + "é", []byte(strings.Repeat("a", 64)), nil}
	before := admittedDatabaseSnapshot(t, s.db)
	for _, owner := range owners {
		for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
			_, err := s.db.Exec(verb+` INTO manual_signing_intents(operation_id,owner_id,facts_json,facts_hash,observation_json,observation_hash,checked_at) VALUES(?,?,?,?,?,?,?)`, original.operation, owner, original.facts, original.digest, original.observation, original.observationHash, original.checked)
			if err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("nonhex full-byte owner accepted or silently ignored", verb, err)
			}
		}
	}
	if err := insertSigningIntent(s, original, "INSERT"); err != nil || count(t, s.db, "manual_signing_intent_complete") != 1 {
		t.Fatal("canonical owner compatibility", err)
	}
}

func TestHTTPM3B3WitnessRetainedNULOwnerReopenNoRepair(t *testing.T) {
	d, s, h, r, scope, path, q := signingWitnessFixture(t)
	if err := insertSigningIntent(s, q, "INSERT"); err != nil {
		t.Fatal(err)
	}
	invalidOwner := strings.Repeat("a", 32) + "\x00" + strings.Repeat("a", 31)
	seedSigningWitnessCorruption(t, s, "manual_signing_intents", `UPDATE manual_signing_intents SET owner_id=?`, invalidOwner)
	seedSigningWitnessCorruption(t, s, "canary_runtime_audit", `UPDATE canary_runtime_audit SET details_json=json_set(details_json,'$.owner_id',?) WHERE event_type='MANUAL_SIGNING_INTENT'`, invalidOwner)
	_, s = bindingReopen(t, d, path)
	if count(t, s.db, "manual_signing_intent_033_complete") != 1 {
		t.Fatal("fixture must isolate 034 owner validation, not audit mismatch")
	}
	assertSigningWitnessIncomplete(t, s, h, r, scope)
	before := admittedDatabaseSnapshot(t, s.db)
	for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
		if err := insertSigningIntent(s, q, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("retained owner adopted", verb, err)
		}
	}
}

func TestHTTPM3B3WitnessRetainedAuditAliasesReopenNoRepair(t *testing.T) {
	for _, fields := range []string{"id,event_type", "id,event_type,operation_id", "id", "event_type,operation_id"} {
		t.Run(fields, func(t *testing.T) {
			d, s, h, r, scope, path, q := signingWitnessFixture(t)
			if err := insertSigningIntent(s, q, "INSERT"); err != nil {
				t.Fatal(err)
			}
			var assignments []string
			for _, field := range strings.Split(fields, ",") {
				assignments = append(assignments, field+"=CAST("+field+" AS BLOB)")
			}
			seedSigningWitnessCorruption(t, s, "canary_runtime_audit", `UPDATE canary_runtime_audit SET `+strings.Join(assignments, ",")+` WHERE event_type='MANUAL_SIGNING_INTENT'`)
			_, s = bindingReopen(t, d, path)
			assertSigningWitnessIncomplete(t, s, h, r, scope)
			before := admittedDatabaseSnapshot(t, s.db)
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
				for _, rowid := range []bool{false, true} {
					columns, projection := "", ""
					if rowid {
						columns, projection = "rowid,", "rowid,"
					}
					_, err := s.db.Exec(verb+` INTO canary_runtime_audit(`+columns+`id,event_type,operation_id,attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at)
 SELECT `+projection+`CAST(id AS TEXT),CAST(event_type AS TEXT),CAST(operation_id AS TEXT),attempt_id,authorization_id,authorization_epoch,reason_code,details_json,created_at FROM canary_runtime_audit WHERE hex(id)=hex(?)`, "manual-sign-intent:"+q.operation)
					if err == nil || admittedDatabaseSnapshot(t, s.db) != before {
						t.Fatal("retained audit repaired/adopted", verb, rowid, err)
					}
				}
			}
			assertSigningWitnessIncomplete(t, s, h, r, scope)
		})
	}
}

func TestHTTPM3B3WitnessRetainedAlertAliasesReopenNoRepair(t *testing.T) {
	for _, fields := range []string{"id,audit_id", "id", "audit_id"} {
		t.Run(fields, func(t *testing.T) {
			d, s, h, r, scope, path, q := signingWitnessFixture(t)
			if err := insertSigningIntent(s, q, "INSERT"); err != nil {
				t.Fatal(err)
			}
			var assignments []string
			for _, field := range strings.Split(fields, ",") {
				assignments = append(assignments, field+"=CAST("+field+" AS BLOB)")
			}
			seedSigningWitnessCorruption(t, s, "canary_alert_outbox", `UPDATE canary_alert_outbox SET `+strings.Join(assignments, ",")+` WHERE id LIKE 'manual-sign-intent-alert:%'`)
			_, s = bindingReopen(t, d, path)
			assertSigningWitnessIncomplete(t, s, h, r, scope)
			if _, err := s.db.Exec(`INSERT INTO canary_runtime_audit(id,event_type,reason_code,details_json,created_at) VALUES('unrelated-audit','OTHER','fixture','{}',?);
 INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) VALUES('unrelated-alert','unrelated-audit','INFO','PENDING',?)`, q.checked, q.checked); err != nil {
				t.Fatal(err)
			}
			before := admittedDatabaseSnapshot(t, s.db)
			for _, query := range []string{
				`UPDATE canary_alert_outbox SET id=CAST(id AS TEXT),audit_id=CAST(audit_id AS TEXT) WHERE hex(id)=hex(?)`,
				`UPDATE OR REPLACE canary_alert_outbox SET id=CAST(id AS TEXT),audit_id=CAST(audit_id AS TEXT) WHERE hex(id)=hex(?)`,
				`DELETE FROM canary_alert_outbox WHERE hex(id)=hex(?)`,
				`INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) SELECT CAST(id AS TEXT),CAST(audit_id AS TEXT),severity,state,created_at FROM canary_alert_outbox WHERE hex(id)=hex(?)`,
				`INSERT OR IGNORE INTO canary_alert_outbox(id,audit_id,severity,state,created_at) SELECT CAST(id AS TEXT),CAST(audit_id AS TEXT),severity,state,created_at FROM canary_alert_outbox WHERE hex(id)=hex(?)`,
				`INSERT OR REPLACE INTO canary_alert_outbox(rowid,id,audit_id,severity,state,created_at) SELECT rowid,CAST(id AS TEXT),CAST(audit_id AS TEXT),severity,state,created_at FROM canary_alert_outbox WHERE hex(id)=hex(?)`,
				`INSERT OR REPLACE INTO canary_alert_outbox(rowid,id,audit_id,severity,state,created_at) SELECT rowid,'unrelated-replacement',audit_id,severity,state,created_at FROM canary_alert_outbox WHERE hex(id)=hex(?)`,
				`UPDATE OR REPLACE canary_alert_outbox SET rowid=(SELECT rowid FROM canary_alert_outbox WHERE hex(id)=hex(?)) WHERE id='unrelated-alert'`,
				`UPDATE canary_alert_outbox SET id=CAST((SELECT id FROM canary_alert_outbox WHERE hex(id)=hex(?)) AS TEXT) WHERE id='unrelated-alert'`,
			} {
				if _, err := s.db.Exec(query, "manual-sign-intent-alert:"+q.operation); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("retained alert repaired/deleted/adopted", query, err)
				}
			}
			// Delivery-only mutation remains possible even on retained aliases;
			// it must not turn incomplete financial/signing history into success.
			if _, err := s.db.Exec(`UPDATE canary_alert_outbox SET state='DELIVERED',delivered_at=? WHERE hex(id)=hex(?)`, q.checked, "manual-sign-intent-alert:"+q.operation); err != nil {
				t.Fatal("delivery-only update blocked", err)
			}
			assertSigningWitnessIncomplete(t, s, h, r, scope)
		})
	}
}

func TestHTTPM3B3WitnessExtraAliasAndDeliveryCompatibility(t *testing.T) {
	d, s, h, r, scope, path, q := signingWitnessFixture(t)
	if err := insertSigningIntent(s, q, "INSERT"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE canary_alert_outbox SET state='DELIVERED',delivered_at=? WHERE id=?`, q.checked, "manual-sign-intent-alert:"+q.operation); err != nil {
		t.Fatal(err)
	}
	if v, err := loadManualSigningIntentHistory(context.Background(), s.db, h.identity, r.RequestID, scope); err != nil || v.SigningAuthorized || count(t, s.db, "manual_signing_intent_complete") != 1 {
		t.Fatal("valid delivery broke false-permission history", err)
	}
	seedSigningWitnessCorruption(t, s, "canary_alert_outbox", `INSERT INTO canary_alert_outbox(id,audit_id,severity,state,created_at) SELECT CAST(id AS BLOB),CAST(audit_id AS BLOB),severity,state,created_at FROM canary_alert_outbox WHERE id=?`, "manual-sign-intent-alert:"+q.operation)
	_, s = bindingReopen(t, d, path)
	assertSigningWitnessIncomplete(t, s, h, r, scope)
	before := admittedDatabaseSnapshot(t, s.db)
	if _, err := s.db.Exec(`DELETE FROM canary_alert_outbox WHERE typeof(id)='blob'`); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("extra alert pollution erased", err)
	}
}

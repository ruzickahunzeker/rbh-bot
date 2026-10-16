package tradeapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

func artifactValidationAPI(t *testing.T, wallet string) (*storage.Database, *Store, *API, *validationBackend, string) {
	t.Helper()
	if wallet == "" {
		return validationAPI(t)
	}
	d, s, original, path := fixture(t)
	if _, err := s.db.Exec(`UPDATE dry_run_wallets SET address=? WHERE id=?`, wallet, original.identity.WalletID); err != nil {
		t.Fatal(err)
	}
	original.identity.WalletAddress = wallet
	validationSeedWallet(t, s, validationPolicy{}, wallet)
	b := &validationBackend{wallet: wallet}
	h, err := NewWithOptions(s, original.identity, testCredential, original.policy, nil, Options{ManualIntake: true, ValidationBackend: b, ValidationPolicyVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	h.now, h.validator.now = original.now, original.now
	return d, s, h, b, path
}

type artifactPhaseCommand struct{ operation, owner, facts, digest, envelope, observation, observationHash, checked string }

func insertArtifactCommand(s *Store, m artifactPhaseCommand, verb string) error {
	_, err := s.db.Exec(verb+` INTO manual_signed_artifact_commands VALUES(?,?,?,?,?,?,?,?)`, m.operation, m.owner, m.facts, m.digest, m.envelope, m.observation, m.observationHash, m.checked)
	return err
}

func artifactPhaseFixture(t *testing.T, side string) (*storage.Database, *Store, *API, Record, manualAdmissionScope, string, artifactPhaseCommand, manualartifact.Cipher) {
	return artifactPhaseFixtureNonce(t, side, "23")
}

func artifactPhaseFixtureNonce(t *testing.T, side, nonce string) (*storage.Database, *Store, *API, Record, manualAdmissionScope, string, artifactPhaseCommand, manualartifact.Cipher) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	d, s, h, r, scope, path := manualInputsFixtureBudgetWallet(t, side, 10, "1000000000000000000", strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex()))
	if _, err := atomicWriter(s).admit(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := bindingWriter(s).bind(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := nonceWriter(s, &localNonceObserver{nonce: nonce}).claim(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	q := signingIntentFor(t, s, h, r, scope)
	if err := insertSigningIntent(s, q, "INSERT"); err != nil {
		t.Fatal(err)
	}
	f, err := manualartifact.Decode(q.facts)
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := f.Transaction()
	// Direct ephemeral cryptographic fixture only, NOT signer orchestration.
	// No TransactionSigner dependency or call is introduced into product code.
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(4663)), key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := trade.NewAESGCMCipher("phase-fixture-only", bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	v, err := manualartifact.Seal(raw, f, cipher)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	metadata := manualartifact.EnvelopeMetadata{Version: v.Version, FactsHash: v.FactsHash, TxHash: v.TxHash, RawSHA256: v.RawSHA256, KeyVersion: v.KeyVersion,
		Ciphertext: hex.EncodeToString(v.Ciphertext), EncryptionNonce: hex.EncodeToString(v.EncryptionNonce), CompletedAt: q.checked}
	m := artifactPhaseCommand{q.operation, q.owner, q.facts, q.digest, encode(metadata), q.observation, q.observationHash, q.checked}
	return d, s, h, r, scope, path, m, cipher
}

func TestHTTPM3B3ArtifactPhaseNonceBoundaries(t *testing.T) {
	for _, nonce := range []string{"0", "9223372036854775808", "18446744073709551614"} {
		t.Run(nonce, func(t *testing.T) {
			_, s, h, r, scope, _, m, cipher := artifactPhaseFixtureNonce(t, "buy", nonce)
			if err := insertArtifactCommand(s, m, "INSERT"); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := s.db.QueryRow(`SELECT nonce FROM transaction_attempts`).Scan(&got); err != nil || got != nonce {
				t.Fatal("unsigned nonce truncated or advanced", got, nonce, err)
			}
			if _, err := loadManualSignedArtifactHistory(context.Background(), s.db, h.identity, r.RequestID, scope, cipher); err != nil {
				t.Fatal("exact signed nonce did not verify", err)
			}
		})
	}
}

func TestHTTPM3B3ArtifactPhaseExactAtomicHistory(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			d, s, h, r, scope, path, m, cipher := artifactPhaseFixture(t, side)
			if err := insertArtifactCommand(s, m, "INSERT"); err != nil {
				t.Fatal(err)
			}
			assertArtifactPhase(t, s, r)
			before := admittedDatabaseSnapshot(t, s.db)
			v, err := loadManualSignedArtifactHistory(context.Background(), s.db, h.identity, r.RequestID, scope, cipher)
			if err != nil || v.Status != "HISTORICAL_VERIFIED_ARTIFACT_NOT_PERMISSION" || v.SendAuthorized || v.SigningAuthorized || v.ExecutionAuthorized || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("historical artifact", err)
			}
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
				if err := insertArtifactCommand(s, m, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("artifact re-created or duplicate recharged", verb, err)
				}
			}
			// Old initial/claimed views and APIs MUST NOT gain signed-phase
			// permission via normalization or manufacture a fresh nonce claim.
			if count(t, s.db, "manual_nonce_claim_complete") != 0 || count(t, s.db, "manual_execution_binding_integrity") != 0 {
				t.Fatal("old initial-only views accepted signed phase")
			}
			if _, err := nonceWriter(s, &localNonceObserver{nonce: "24"}).claim(context.Background(), h.identity, r.RequestID, scope); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("signed phase became a new claim", err)
			}
			if _, err := s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'; UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_authorization_operation_usage SET state='FROZEN'`); err != nil {
				t.Fatal(err)
			}
			before = admittedDatabaseSnapshot(t, s.db)
			_, s = bindingReopen(t, d, path)
			got, err := loadManualSignedArtifactHistory(context.Background(), s.db, h.identity, r.RequestID, scope, cipher)
			if err != nil || got != v || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("reopen/stop/revoke changed artifact or permission", err)
			}
			// History has no current clock: completed-at was valid originally;
			// hypothetical post-TTL read is identity verification, not TTL renewal.
			assertArtifactPhase(t, s, r)
		})
	}
}

func assertArtifactPhase(t *testing.T, s *Store, r Record) {
	t.Helper()
	for _, table := range []string{"manual_signed_artifact_commands", "manual_signed_artifact_complete", "manual_artifact_claim_complete", "transaction_attempts", "canary_risk_reservations", "canary_authorization_operation_usage"} {
		if count(t, s.db, table) != 1 {
			t.Fatal("artifact phase/economic ownership incomplete", table)
		}
	}
	for _, table := range []string{"manual_artifact_effect_scopes", "manual_signing_witness_creation_scopes", "execution_reservations", "transaction_submissions", "receipt_observations", "position_effects", "canary_send_permits", "canary_runtime_gate_snapshots"} {
		if count(t, s.db, table) != 0 {
			t.Fatal("prohibited economic effect or residual scope", table)
		}
	}
	var step, lane, attempt, nonce, expiry, amount string
	var usage int
	if err := s.db.QueryRow(`SELECT s.status,l.state,t.status,t.nonce,t.expires_at,u.operation_count,u.total_input FROM manual_nonce_claims q
 JOIN execution_steps s ON s.id=q.execution_step_id JOIN transaction_attempts t ON t.id=q.attempt_id
 JOIN execution_wallet_lanes l ON l.wallet_id=q.wallet_id CROSS JOIN canary_authorization_usage u`).Scan(&step, &lane, &attempt, &nonce, &expiry, &usage, &amount); err != nil || step != "signed" || lane != "signed" || attempt != "signed" || nonce != "23" || expiry != r.ExpiresAt || usage != 1 || amount != r.Validation.Output.Resolution.AmountIn {
		t.Fatal("phase/nonce/expiry/accounting drift", err)
	}
	var audits, alerts int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM canary_runtime_audit WHERE event_type='MANUAL_ARTIFACT_STORED'),(SELECT COUNT(*) FROM canary_alert_outbox WHERE id LIKE 'manual-artifact-alert:%')`).Scan(&audits, &alerts); err != nil || audits != 1 || alerts != 1 {
		t.Fatal("artifact audit not exactly once", err)
	}
}

func TestHTTPM3B3ArtifactPhaseSourceDenials(t *testing.T) {
	_, s, _, r, _, _, original, _ := artifactPhaseFixture(t, "buy")
	before := admittedDatabaseSnapshot(t, s.db)
	for _, kind := range []string{"stop", "revoke", "frozen_risk", "frozen_usage", "expiry_equality", "owner_nul", "facts_nul", "facts_hash", "runtime", "observation_nul", "envelope_nul", "key_nul", "fee_relabel", "wrong_txhash", "nonce_length", "cipher_length", "metadata_time", "contract_deadline"} {
		t.Run(kind, func(t *testing.T) {
			if _, err := s.db.Exec(`SAVEPOINT artifact_source_case`); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := s.db.Exec(`ROLLBACK TO artifact_source_case; RELEASE artifact_source_case`); err != nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("source test leaked state", err)
				}
			}()
			m := original
			var mutationErr error
			var envelope manualartifact.EnvelopeMetadata
			if err := json.Unmarshal([]byte(m.envelope), &envelope); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "stop":
				_, mutationErr = s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1`)
			case "revoke":
				_, mutationErr = s.db.Exec(`UPDATE canary_runtime_authorizations SET state='REVOKED'`)
			case "frozen_risk":
				_, mutationErr = s.db.Exec(`UPDATE canary_risk_reservations SET state='frozen'`)
			case "frozen_usage":
				_, mutationErr = s.db.Exec(`UPDATE canary_authorization_operation_usage SET state='FROZEN'`)
			case "expiry_equality":
				m.checked, envelope.CompletedAt = r.ExpiresAt, r.ExpiresAt
			case "owner_nul":
				m.owner = strings.Repeat("a", 32) + "\x00" + strings.Repeat("a", 31)
			case "facts_nul":
				m.facts += "\x00"
			case "facts_hash":
				m.digest = strings.Repeat("f", 64)
			case "runtime":
				m.observation = strings.Replace(m.observation, `"runtime_code_hash":"0x`, `"runtime_code_hash":"0x0`, 1)
			case "observation_nul":
				m.observation += "\x00"
			case "envelope_nul":
				m.envelope += "\x00"
			case "key_nul":
				envelope.KeyVersion += "\x00"
			case "fee_relabel":
				m.facts = strings.Replace(m.facts, `"gas_tip_cap":"0"`, `"gas_tip_cap":"1"`, 1)
			case "wrong_txhash":
				envelope.TxHash = "0xBAD"
			case "nonce_length":
				envelope.EncryptionNonce = "00"
			case "cipher_length":
				envelope.Ciphertext = "00"
			case "metadata_time":
				envelope.CompletedAt = testNow.Add(time.Nanosecond).Format(time.RFC3339Nano)
			case "contract_deadline":
				m.facts = strings.Replace(m.facts, `"contract_deadline":false`, `"contract_deadline":true`, 1)
			}
			if mutationErr != nil {
				t.Fatal("denial fixture did not apply mutable control", mutationErr)
			}
			if kind != "envelope_nul" {
				m.envelope = encode(envelope)
			}
			snapshot := admittedDatabaseSnapshot(t, s.db)
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
				if err := insertArtifactCommand(s, m, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != snapshot {
					t.Fatal("invalid command accepted or partially changed state", kind, verb, err)
				}
			}
		})
	}
}

func TestHTTPM3B3ArtifactPhaseFaultsRollback(t *testing.T) {
	for _, target := range []string{"attempt", "step", "lane", "audit", "alert", "scope_open", "scope_close"} {
		for _, action := range []string{"IGNORE", "ABORT,'forced artifact effect failure'"} {
			t.Run(target+action, func(t *testing.T) {
				_, s, _, _, _, _, m, _ := artifactPhaseFixture(t, "buy")
				table, event, when := "transaction_attempts", "UPDATE", "NEW.status='signed'"
				switch target {
				case "step":
					table = "execution_steps"
				case "lane":
					table, when = "execution_wallet_lanes", "NEW.state='signed'"
				case "audit":
					table, event, when = "canary_runtime_audit", "INSERT", "NEW.event_type='MANUAL_ARTIFACT_STORED'"
				case "alert":
					table, event, when = "canary_alert_outbox", "INSERT", "NEW.id LIKE 'manual-artifact-alert:%'"
				case "scope_open":
					table, event, when = "manual_artifact_effect_scopes", "INSERT", "1"
				case "scope_close":
					table, event, when = "manual_artifact_effect_scopes", "DELETE", "1"
				}
				if _, err := s.db.Exec(`CREATE TRIGGER forced_artifact_effect BEFORE ` + event + ` ON ` + table + ` WHEN ` + when + ` BEGIN SELECT RAISE(` + action + `); END`); err != nil {
					t.Fatal(err)
				}
				before := admittedDatabaseSnapshot(t, s.db)
				for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
					if err := insertArtifactCommand(s, m, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
						t.Fatal("half artifact effect survived", target, action, verb, err)
					}
				}
			})
		}
	}
}

func TestHTTPM3B3ArtifactPhaseTwoHandleOneOutcome(t *testing.T) {
	_, s, h, r, scope, path, m, cipher := artifactPhaseFixture(t, "buy")
	d2, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	s2, err := NewStore(d2)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			store := s
			if i%2 == 1 {
				store = s2
			}
			results <- insertArtifactCommand(store, m, "INSERT")
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatal("multiple or missing artifact outcome", accepted)
	}
	assertArtifactPhase(t, s, r)
	if _, err := loadManualSignedArtifactHistory(context.Background(), s2.db, h.identity, r.RequestID, scope, cipher); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPM3B3ArtifactPhaseCryptoRequiredZeroOutput(t *testing.T) {
	_, s, h, r, scope, _, m, cipher := artifactPhaseFixture(t, "buy")
	var e manualartifact.EnvelopeMetadata
	if err := json.Unmarshal([]byte(m.envelope), &e); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(e.Ciphertext, "00") {
		e.Ciphertext = "01" + e.Ciphertext[2:]
	} else {
		e.Ciphertext = "00" + e.Ciphertext[2:]
	}
	m.envelope = encode(e)
	if err := insertArtifactCommand(s, m, "INSERT"); err != nil || count(t, s.db, "manual_signed_artifact_complete") != 1 {
		t.Fatal("metadata-only contract fixture", err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	v, err := loadManualSignedArtifactHistory(context.Background(), s.db, h.identity, r.RequestID, scope, cipher)
	if err == nil || !reflect.DeepEqual(v, manualSignedArtifactHistory{}) || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("SQL completeness mistaken for cryptographic validity", err)
	}
}

func TestHTTPM3B3ArtifactPhaseDirectMutationsAndVictims(t *testing.T) {
	_, s, h, r, scope, _, m, cipher := artifactPhaseFixture(t, "buy")
	if err := insertArtifactCommand(s, m, "INSERT"); err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	for _, query := range []string{
		`UPDATE transaction_attempts SET status='prepared'`, `UPDATE transaction_attempts SET nonce='24'`, `UPDATE transaction_attempts SET gas_tip_cap='1'`,
		`UPDATE transaction_attempts SET encrypted_raw_tx=X'00'`, `UPDATE transaction_attempts SET updated_at='later'`, `UPDATE transaction_attempts SET rowid=rowid+100`,
		`DELETE FROM transaction_attempts`, `UPDATE execution_steps SET status='nonce_reserved' WHERE kind='manual_trade_execution'`,
		`UPDATE execution_steps SET parameters_json='{}' WHERE kind='manual_trade_execution'`, `UPDATE execution_wallet_lanes SET state='idle',operation_id=NULL,step_id=NULL,reserved_nonce=NULL`,
		`UPDATE manual_signed_artifact_commands SET envelope_json='{}'`, `DELETE FROM manual_signed_artifact_commands`,
		`UPDATE canary_risk_reservations SET state='released'`, `UPDATE canary_authorization_operation_usage SET state='RELEASED'`,
		`INSERT OR REPLACE INTO transaction_attempts(rowid,id,step_id,nonce,status,created_at) SELECT rowid,'victim-attempt',step_id,nonce,'prepared',created_at FROM transaction_attempts`,
		`INSERT OR REPLACE INTO canary_runtime_audit(rowid,id,event_type,reason_code,details_json,created_at) SELECT rowid,'victim-audit','OTHER','x','{}',created_at FROM canary_runtime_audit WHERE event_type='MANUAL_ARTIFACT_STORED'`,
		`INSERT OR REPLACE INTO canary_alert_outbox(rowid,id,audit_id,severity,state,created_at) SELECT rowid,'victim-alert',audit_id,'INFO','PENDING',created_at FROM canary_alert_outbox WHERE id LIKE 'manual-artifact-alert:%'`,
		`DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-artifact-alert:%'`,
	} {
		if _, err := s.db.Exec(query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("signed phase changed/reset or victim replaced", query, err)
		}
	}
	for _, table := range []string{"transaction_attempts", "execution_steps", "execution_wallet_lanes"} {
		rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			t.Fatal(err)
		}
		var columns []string
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				t.Fatal(err)
			}
			columns = append(columns, column)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		for _, column := range columns {
			query := `UPDATE ` + table + ` SET "` + column + `"=NULL`
			if table == "execution_steps" {
				query += ` WHERE kind='manual_trade_execution'`
			}
			if _, err := s.db.Exec(query); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("full-column immutable contract missed", table, column, err)
			}
		}
	}
	if _, err := s.db.Exec(`UPDATE canary_alert_outbox SET state='DELIVERED',delivered_at=? WHERE id LIKE 'manual-artifact-alert:%'`, m.checked); err != nil {
		t.Fatal("legitimate delivery update", err)
	}
	if _, err := loadManualSignedArtifactHistory(context.Background(), s.db, h.identity, r.RequestID, scope, cipher); err != nil {
		t.Fatal(err)
	}
	assertArtifactPhase(t, s, r)
}

func TestHTTPM3B3ArtifactPhaseRetainedCorruptionReopenNoRepair(t *testing.T) {
	for _, kind := range []string{"attempt_bytes", "attempt_blob_type", "command_owner", "audit_alias", "alert_alias", "missing_audit", "missing_alert"} {
		t.Run(kind, func(t *testing.T) {
			d, s, h, r, scope, path, m, cipher := artifactPhaseFixture(t, "buy")
			if err := insertArtifactCommand(s, m, "INSERT"); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "attempt_bytes":
				seedSigningWitnessCorruption(t, s, "transaction_attempts", `UPDATE transaction_attempts SET encrypted_raw_tx=X'00'`)
			case "attempt_blob_type":
				seedSigningWitnessCorruption(t, s, "transaction_attempts", `UPDATE transaction_attempts SET encrypted_raw_tx=CAST(encrypted_raw_tx AS TEXT)`)
			case "command_owner":
				seedSigningWitnessCorruption(t, s, "manual_signed_artifact_commands", `UPDATE manual_signed_artifact_commands SET owner_id=CAST(owner_id AS BLOB)`)
			case "audit_alias":
				seedSigningWitnessCorruption(t, s, "canary_runtime_audit", `UPDATE canary_runtime_audit SET id=CAST(id AS BLOB),event_type=CAST(event_type AS BLOB) WHERE event_type='MANUAL_ARTIFACT_STORED'`)
			case "alert_alias":
				seedSigningWitnessCorruption(t, s, "canary_alert_outbox", `UPDATE canary_alert_outbox SET id=CAST(id AS BLOB),audit_id=CAST(audit_id AS BLOB) WHERE id LIKE 'manual-artifact-alert:%'`)
			case "missing_audit":
				seedSigningWitnessCorruption(t, s, "canary_runtime_audit", `DELETE FROM canary_runtime_audit WHERE event_type='MANUAL_ARTIFACT_STORED'`)
			case "missing_alert":
				seedSigningWitnessCorruption(t, s, "canary_alert_outbox", `DELETE FROM canary_alert_outbox WHERE id LIKE 'manual-artifact-alert:%'`)
			}
			_, s = bindingReopen(t, d, path)
			before := admittedDatabaseSnapshot(t, s.db)
			v, err := loadManualSignedArtifactHistory(context.Background(), s.db, h.identity, r.RequestID, scope, cipher)
			if err == nil || !reflect.DeepEqual(v, manualSignedArtifactHistory{}) || count(t, s.db, "manual_signed_artifact_complete") != 0 || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("corrupt artifact accepted/repaired", kind, err)
			}
			for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
				if err := insertArtifactCommand(s, m, verb); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("retained artifact command adopted", kind, verb, err)
				}
				if _, err := s.db.Exec(verb+` INTO manual_artifact_effect_scopes VALUES(?,?,?)`, m.operation, m.owner, m.digest); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("retained artifact scope reopened", kind, verb, err)
				}
			}
		})
	}
}

func TestHTTPM3B3ArtifactPhaseHistoryDeniedZeroOutput(t *testing.T) {
	_, s, h, r, scope, _, m, cipher := artifactPhaseFixture(t, "buy")
	if err := insertArtifactCommand(s, m, "INSERT"); err != nil {
		t.Fatal(err)
	}
	wrong, err := trade.NewAESGCMCipher(cipher.KeyVersion(), bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	before := admittedDatabaseSnapshot(t, s.db)
	for _, kind := range []string{"principal", "wallet", "scope", "nil_context", "cancel", "nil_cipher", "wrong_key"} {
		t.Run(kind, func(t *testing.T) {
			id, expected, c, ctx := h.identity, scope, cipher, context.Background()
			switch kind {
			case "principal":
				id.PrincipalID = "different"
			case "wallet":
				id.WalletAddress = testWallet
			case "scope":
				expected.AuthorizationEpoch++
			case "nil_context":
				ctx = nil
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil_cipher":
				c = nil
			case "wrong_key":
				c = wrong
			}
			v, err := loadManualSignedArtifactHistory(ctx, s.db, id, r.RequestID, expected, c)
			if err == nil || !reflect.DeepEqual(v, manualSignedArtifactHistory{}) || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("denied history leaked/repaired/changed", kind, err)
			}
		})
	}
}

func TestHTTPM3B3ArtifactMetadataScalarFullBytesPoolReplacement(t *testing.T) {
	_, s, _, _, _, _, m, _ := artifactPhaseFixture(t, "buy")
	s.db.SetMaxIdleConns(0)
	for repeat := 0; repeat < 3; repeat++ {
		for _, tc := range []struct {
			facts, envelope any
			want            int
		}{{m.facts, m.envelope, 1}, {[]byte(m.facts), m.envelope, 0}, {m.facts, []byte(m.envelope), 0}, {m.facts + "\x00", m.envelope, 0}, {m.facts, m.envelope + "\x00", 0}, {nil, m.envelope, 0}, {m.facts, nil, 0}} {
			var valid int
			if err := s.db.QueryRow(`SELECT manual_artifact_envelope_valid(hex(?),typeof(?),hex(?),typeof(?))`, tc.facts, tc.facts, tc.envelope, tc.envelope).Scan(&valid); err != nil || valid != tc.want {
				t.Fatal("metadata scalar trusted truncated/type-changed bytes", valid, tc.want, err)
			}
		}
	}
}

func TestHTTPM3B3ArtifactPhaseScopeCannotCommitOrAdopt(t *testing.T) {
	_, s, _, _, _, _, m, _ := artifactPhaseFixture(t, "buy")
	s.db.SetMaxOpenConns(1)
	before := admittedDatabaseSnapshot(t, s.db)
	for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
		if _, err := s.db.Exec(verb+` INTO manual_artifact_effect_scopes VALUES(?,?,?)`, m.operation, m.owner, m.digest); err == nil || admittedDatabaseSnapshot(t, s.db) != before {
			t.Fatal("artifact scope standalone commit", err)
		}
	}
	if _, err := s.db.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO manual_artifact_effect_scopes VALUES(?,?,?)`, m.operation, m.owner, m.digest); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"INSERT", "INSERT OR IGNORE", "INSERT OR REPLACE"} {
		if err := insertArtifactCommand(s, m, verb); err == nil {
			t.Fatal("preseeded scope adopted", verb)
		}
	}
	if _, err := s.db.Exec(`COMMIT`); err == nil {
		t.Fatal("scope without command committed")
	}
	if _, err := s.db.Exec(`ROLLBACK`); err != nil || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("scope transaction leaked", err)
	}
}

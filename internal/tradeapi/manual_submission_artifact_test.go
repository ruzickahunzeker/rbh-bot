package tradeapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type handoffDecryptor struct {
	base     manualartifact.Cipher
	calls    int
	mode     string
	cancel   context.CancelFunc
	onOpen   func()
	expected string
}

func (c *handoffDecryptor) KeyVersion() string {
	if c.mode == "key_panic" {
		panic("private injected key error")
	}
	return c.base.KeyVersion()
}
func (c *handoffDecryptor) Decrypt(key string, raw, nonce, aad []byte) ([]byte, error) {
	c.calls++
	if c.expected != "" && string(aad) != c.expected {
		return nil, errors.New("unexpected AAD")
	}
	if c.onOpen != nil {
		c.onOpen()
	}
	if c.mode == "decrypt_panic" {
		panic("private injected decrypt error")
	}
	if c.mode == "decrypt_error" {
		return nil, errors.New("private injected decrypt error")
	}
	v, err := c.base.Decrypt(key, raw, nonce, aad)
	if c.mode == "cancel" {
		c.cancel()
	}
	return v, err
}

func handoffReader(s *Store, cipher manualArtifactDecryptor) *manualSubmissionArtifactReader {
	return &manualSubmissionArtifactReader{store: s, decryptor: cipher}
}

func assertHandoffMetadata(t *testing.T, v manualSubmissionArtifact, r Record, scope manualAdmissionScope) {
	t.Helper()
	if v.Source != "MANUAL" || v.Format != "MANUAL_V1_FULL_FACTS_AAD" || v.Phase != "SIGNED_PREBROADCAST" || v.Status != "VERIFIED_MANUAL_ARTIFACT_CANDIDATE_NOT_PERMISSION" || !v.SnapshotOnly || !v.FreshGateRequired ||
		v.ExecutionAuthorized || v.SigningAuthorized || v.SendAuthorized || v.ReplayAuthorized || v.Facts.OperationID != *r.OperationID || v.Facts.RequestID != r.RequestID ||
		v.Facts.Nonce != "23" || v.Facts.ExpiresAt != r.ExpiresAt || v.Facts.AuthorizationID != scope.AuthorizationID || v.Facts.AuthorizationEpoch != scope.AuthorizationEpoch || v.Facts.DeploymentID != scope.DeploymentID ||
		v.Facts.Data != r.Validation.Output.Data || v.Facts.Value != r.Validation.Output.Value || v.Facts.GasFeeCap != r.Validation.Output.GasFeeCap || v.Facts.GasTipCap != "0" || v.Facts.DeadlineCapability != "APPLICATION_TTL_ONLY" || v.Facts.ContractDeadline {
		t.Fatal("handoff identity/permission drift", v)
	}
	_, digest, err := v.Facts.Canonical()
	if err != nil || digest != v.FactsHash || v.TxHash == "" || v.RawSHA256 == "" || v.KeyVersion == "" || v.CompletedAt == "" {
		t.Fatal("incomplete authenticated metadata", err)
	}
}

func TestHTTPM4AExactReadOnlyReopenAfterControls(t *testing.T) {
	for _, side := range []string{"buy", "sell_amount", "sell_percent"} {
		t.Run(side, func(t *testing.T) {
			d, s, h, r, scope, path, w, signer, observer := localSigningFixture(t, side)
			if _, err := w.sign(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			decryptor := &handoffDecryptor{base: w.cipher}
			before := admittedDatabaseSnapshot(t, s.db)
			v, err := handoffReader(s, decryptor).load(context.Background(), h.identity, r.RequestID, scope)
			if err != nil {
				t.Fatal(err)
			}
			assertHandoffMetadata(t, v, r, scope)
			aad, _ := v.Facts.AAD()
			decryptor.expected = string(aad)
			for i := 0; i < 3; i++ {
				got, err := handoffReader(s, decryptor).load(context.Background(), h.identity, r.RequestID, scope)
				if err != nil || got != v || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("readonly history mutated or drifted", got, err)
				}
			}
			if _, err := s.db.Exec(`UPDATE canary_control_state SET emergency_stopped=1; UPDATE canary_runtime_authorizations SET state='REVOKED'; UPDATE canary_risk_reservations SET state='frozen'; UPDATE canary_authorization_operation_usage SET state='FROZEN'`); err != nil {
				t.Fatal(err)
			}
			before = admittedDatabaseSnapshot(t, s.db)
			_, s = bindingReopen(t, d, path)
			got, err := handoffReader(s, decryptor).load(context.Background(), h.identity, r.RequestID, scope)
			if err != nil || got != v || admittedDatabaseSnapshot(t, s.db) != before || signer.calls.Load() != 1 || observer.calls.Load() != 1 || decryptor.calls != 5 {
				t.Fatal("reopen/control changes renewed action", got, err)
			}
			assertArtifactPhase(t, s, r)
		})
	}
}

func TestHTTPM4AUnsignedAndAmbiguousNeverBecomeArtifact(t *testing.T) {
	for _, stage := range []string{"claimed", "marker"} {
		t.Run(stage, func(t *testing.T) {
			_, s, h, r, scope, _, w, signer, _ := localSigningFixture(t, "buy")
			if stage == "marker" {
				w.checkpoint = func(at string) error {
					if at == "after_marker_commit" {
						return errors.New("controlled interruption")
					}
					return nil
				}
				if _, err := w.sign(context.Background(), h.identity, r.RequestID, scope); err == nil {
					t.Fatal("missing interruption")
				}
			}
			decryptor := &handoffDecryptor{base: w.cipher}
			before := admittedDatabaseSnapshot(t, s.db)
			got, err := handoffReader(s, decryptor).load(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || got != (manualSubmissionArtifact{}) || decryptor.calls != 0 || signer.calls.Load() != 0 || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("unsigned/ambiguous history manufactured artifact", got, err)
			}
			assertNonceClaim(t, s, r, "23")
		})
	}
}

func TestHTTPM4AOwnerAndScopeDenialsBeforeDecrypt(t *testing.T) {
	_, s, h, r, scope, _, w, _, _ := localSigningFixture(t, "buy")
	if _, err := w.sign(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	d := &handoffDecryptor{base: w.cipher}
	before := admittedDatabaseSnapshot(t, s.db)
	for _, field := range []string{"PrincipalID", "WalletID", "WalletAddress", "AuthorizationID", "AuthorizationEpoch", "ManualPolicyID", "Environment", "BuildSHA", "ReleaseID", "DeploymentID"} {
		t.Run(field, func(t *testing.T) {
			id, wrong := h.identity, scope
			target := reflect.ValueOf(&wrong).Elem().FieldByName(field)
			if f := reflect.ValueOf(&id).Elem().FieldByName(field); f.IsValid() {
				target = f
			}
			if target.Kind() == reflect.Uint64 {
				target.SetUint(2)
			} else {
				value := "outside"
				if field == "BuildSHA" {
					value = strings.Repeat("2", 40)
				}
				if field == "WalletAddress" {
					value = "0x" + strings.Repeat("9", 40)
				}
				target.SetString(value)
			}
			got, err := handoffReader(s, d).load(context.Background(), id, r.RequestID, wrong)
			if err == nil || got != (manualSubmissionArtifact{}) || d.calls != 0 || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("caller relabeled durable ownership", field, got, err)
			}
		})
	}
}

func TestHTTPM4ALegacyAADAndCryptoFailuresNeverFallback(t *testing.T) {
	for _, kind := range []string{"legacy_aad", "tampered_ciphertext", "unknown_key"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, scope, _, cmd, cipher := artifactPhaseFixture(t, "buy")
			facts, err := manualartifact.Decode(cmd.facts)
			if err != nil {
				t.Fatal(err)
			}
			var envelope manualartifact.EnvelopeMetadata
			if err := json.Unmarshal([]byte(cmd.envelope), &envelope); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "legacy_aad":
				encoded, _, err := manualartifact.DecodeEnvelopeMetadata(cmd.envelope, facts)
				if err != nil {
					t.Fatal(err)
				}
				aad, _ := facts.AAD()
				raw, err := cipher.Decrypt(encoded.KeyVersion, encoded.Ciphertext, encoded.EncryptionNonce, aad)
				if err != nil {
					t.Fatal(err)
				}
				legacy := []byte("rbh:4663:" + facts.OperationID + ":" + facts.ExecutionStepID + ":" + facts.AttemptID)
				if _, err := cipher.Decrypt(encoded.KeyVersion, encoded.Ciphertext, encoded.EncryptionNonce, legacy); err == nil {
					t.Fatal("MANUAL ciphertext accepted legacy AAD")
				}
				ct, nonce, err := cipher.Encrypt(raw, legacy) // test fixture only
				clear(raw)
				if err != nil {
					t.Fatal(err)
				}
				envelope.Ciphertext, envelope.EncryptionNonce = hex.EncodeToString(ct), hex.EncodeToString(nonce)
			case "tampered_ciphertext":
				ct, _ := hex.DecodeString(envelope.Ciphertext)
				ct[0] ^= 1
				envelope.Ciphertext = hex.EncodeToString(ct)
			case "unknown_key":
				envelope.KeyVersion = "unavailable-key"
			}
			cmd.envelope = encode(envelope)
			if err := insertArtifactCommand(s, cmd, "INSERT"); err != nil {
				t.Fatal(err)
			}
			if count(t, s.db, "manual_signed_artifact_complete") != 1 {
				t.Fatal("fixture must isolate crypto, not SQL shape failure")
			}
			aad, _ := facts.AAD()
			d := &handoffDecryptor{base: cipher, expected: string(aad)}
			before := admittedDatabaseSnapshot(t, s.db)
			got, err := handoffReader(s, d).load(context.Background(), h.identity, r.RequestID, scope)
			wantCalls := 1
			if kind == "unknown_key" {
				wantCalls = 0
			}
			if err == nil || got != (manualSubmissionArtifact{}) || d.calls != wantCalls || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("crypto failure fell back/repaired/exposed output", got, err, d.calls)
			}
		})
	}
}

func TestHTTPM4ADurableLegacyCollisionAndUnsupportedPhase(t *testing.T) {
	for _, kind := range []string{"legacy_text", "legacy_blob", "canary_source", "submission"} {
		t.Run(kind, func(t *testing.T) {
			_, s, h, r, scope, _, w, _, _ := localSigningFixture(t, "buy")
			if _, err := w.sign(context.Background(), h.identity, r.RequestID, scope); err != nil {
				t.Fatal(err)
			}
			query := `INSERT INTO dry_run_results(operation_id,step_id,status,created_at) VALUES(?,?,'fail_closed',?)`
			if kind == "legacy_blob" {
				// Retained BLOB corruption seed only; all guards/FK restored
				// before the tested read. Not a claim this insert is FK-valid.
				seedSigningWitnessCorruption(t, s, "dry_run_results", `INSERT INTO dry_run_results(operation_id,step_id,status,created_at) VALUES(CAST(? AS BLOB),?,'fail_closed',?)`, *r.OperationID, "manual_step_"+r.RequestID, r.CreatedAt)
			} else if kind == "canary_source" {
				sourceQuery := `INSERT INTO canary_admission_sources(operation_id,wallet_id,wallet_address,protocol,direction,token_address,contract_address,contract_role,runtime_code_hash,amount,gas_limit,gas_fee_cap,gas_tip_cap,native_balance,slippage_bps,sell_bps,policy_version,quote_block_number,quote_block_hash,expires_at,source_hash,created_at)
 SELECT operation_id,json_extract(contract_json,'$.wallet_id'),json_extract(contract_json,'$.wallet_address'),'PONS_V2_CURVE','BUY',
 json_extract(contract_json,'$.output.resolution.token.address'),json_extract(contract_json,'$.output.to'),'CURVE',json_extract(contract_json,'$.output.resolution.token.curve_runtime_code_hash'),
 json_extract(contract_json,'$.input.units'),json_extract(contract_json,'$.output.gas_limit'),json_extract(contract_json,'$.output.gas_fee_cap'),json_extract(contract_json,'$.output.gas_tip_cap'),
 '100000000000000000000',300,0,json_extract(contract_json,'$.policy_version'),json_extract(contract_json,'$.output.resolution.token.block.number'),json_extract(contract_json,'$.output.resolution.token.block.hash'),
 json_extract(contract_json,'$.expires_at'),json_extract(contract_json,'$.result_hash'),checked_at FROM manual_admission_commands`
				before := admittedDatabaseSnapshot(t, s.db)
				if _, err := s.db.Exec(sourceQuery); err == nil || !strings.Contains(err.Error(), "manual operation requires typed atomic admission") || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("legacy source adoption guard weakened", err)
				}
				seedSigningWitnessCorruption(t, s, "canary_admission_sources", sourceQuery)
			} else if kind == "submission" {
				query := `INSERT INTO transaction_submissions(id,attempt_id,tx_hash,sequence,state,created_at,updated_at) SELECT 'm4a-unsupported',id,tx_hash,0,'submitting',created_at,updated_at FROM transaction_attempts`
				before := admittedDatabaseSnapshot(t, s.db)
				if _, err := s.db.Exec(query); err == nil || !strings.Contains(err.Error(), "manual submission not connected") || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("existing no-submission guard weakened", err)
				}
				// Unsupported retained downstream pollution is seeded only in
				// the disposable fixture; original guards/FK restored for read.
				seedSigningWitnessCorruption(t, s, "transaction_submissions", query)
			} else if _, err := s.db.Exec(query, *r.OperationID, "manual_step_"+r.RequestID, r.CreatedAt); err != nil {
				t.Fatal(err)
			}
			d := &handoffDecryptor{base: w.cipher}
			before := admittedDatabaseSnapshot(t, s.db)
			got, err := handoffReader(s, d).load(context.Background(), h.identity, r.RequestID, scope)
			if err == nil || got != (manualSubmissionArtifact{}) || d.calls != 0 || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("conflicting source/unsupported phase adopted or repaired", got, err)
			}
		})
	}
}

func TestHTTPM4ACancelPanicAndInvalidCompositionZeroOutput(t *testing.T) {
	_, s, h, r, scope, _, w, _, _ := localSigningFixture(t, "buy")
	if _, err := w.sign(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"key_panic", "decrypt_panic", "decrypt_error", "cancel", "pre_cancel", "deadline", "typed_nil"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d := &handoffDecryptor{base: w.cipher, mode: mode, cancel: cancel}
			if mode == "pre_cancel" {
				cancel()
			}
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
			}
			if mode == "typed_nil" {
				d = nil
			}
			before := admittedDatabaseSnapshot(t, s.db)
			got, err := handoffReader(s, d).load(ctx, h.identity, r.RequestID, scope)
			if err == nil || got != (manualSubmissionArtifact{}) || strings.Contains(err.Error(), "private injected") || admittedDatabaseSnapshot(t, s.db) != before {
				t.Fatal("failure leaked output/secret or mutated store", got, err)
			}
			if (mode == "cancel" || mode == "pre_cancel") && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation cause", err)
			}
			if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("lost deadline cause", err)
			}
		})
	}
	for _, reader := range []*manualSubmissionArtifactReader{nil, {}, {store: s}, {decryptor: w.cipher}} {
		got, err := reader.load(context.Background(), h.identity, r.RequestID, scope)
		if err == nil || got != (manualSubmissionArtifact{}) {
			t.Fatal("invalid composition produced output", got, err)
		}
	}
}

func TestHTTPM4AMissingSourceAndClosedDBFailClosed(t *testing.T) {
	db, s, h, r, scope, _, w, _, _ := localSigningFixture(t, "buy")
	if _, err := w.sign(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	d := &handoffDecryptor{base: w.cipher}
	before := admittedDatabaseSnapshot(t, s.db)
	got, err := handoffReader(s, d).load(context.Background(), h.identity, "missing-request", scope)
	if !errors.Is(err, ErrNotFound) || got != (manualSubmissionArtifact{}) || d.calls != 0 || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("missing source selected an alternate artifact", got, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	got, err = handoffReader(s, d).load(context.Background(), h.identity, r.RequestID, scope)
	if err == nil || got != (manualSubmissionArtifact{}) || d.calls != 0 {
		t.Fatal("unavailable DB produced candidate", got, err)
	}
}

func TestHTTPM4ASnapshotNeverSubstitutesForFreshGate(t *testing.T) {
	_, s, h, r, scope, path, w, _, _ := localSigningFixture(t, "buy")
	if _, err := w.sign(context.Background(), h.identity, r.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	db2, err := storage.Open(context.Background(), storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	s2, err := NewStore(db2)
	if err != nil {
		t.Fatal(err)
	}
	d := &handoffDecryptor{base: w.cipher, onOpen: func() {
		// WAL read snapshot remains coherent even after a second writer
		// commits contradictory legacy ownership. It is not a current gate.
		if _, err := s2.db.Exec(`INSERT INTO dry_run_results(operation_id,step_id,status,created_at) VALUES(?,?,'fail_closed',?)`, *r.OperationID, "manual_step_"+r.RequestID, r.CreatedAt); err != nil {
			t.Fatal(err)
		}
	}}
	got, err := handoffReader(s, d).load(context.Background(), h.identity, r.RequestID, scope)
	if err != nil || d.calls != 1 {
		t.Fatal("coherent historical snapshot unavailable", got, err)
	}
	assertHandoffMetadata(t, got, r, scope)
	d.onOpen = nil
	before := admittedDatabaseSnapshot(t, s.db)
	got, err = handoffReader(s, d).load(context.Background(), h.identity, r.RequestID, scope)
	if err == nil || got != (manualSubmissionArtifact{}) || d.calls != 1 || admittedDatabaseSnapshot(t, s.db) != before {
		t.Fatal("new read reused stale snapshot as permission", got, err)
	}
}

func TestHTTPM4AReadOnlyDependencyAndMetadataContract(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "manual_submission_artifact.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		for _, denied := range []string{"internal/trade\"", "manualsign", "ethclient", "/rpc", "net/", "crypto/ecdsa"} {
			if strings.Contains(imp.Path.Value, denied) {
				t.Fatal("economic dependency", imp.Path.Value)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if selector, ok := n.(*ast.SelectorExpr); ok {
			for _, denied := range []string{"Exec", "ExecContext", "Encrypt", "SignTransaction", "Submit", "ReplayUnknown", "SendRawTransaction", "ReserveExecution", "ConsumeCanaryPermitAfterGate"} {
				if selector.Sel.Name == denied {
					t.Errorf("economic/write call %s", denied)
				}
			}
		}
		return true
	})
	for _, typ := range []reflect.Type{reflect.TypeOf(manualSubmissionArtifact{}), reflect.TypeOf(manualartifact.Facts{})} {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.Type.Kind() == reflect.Slice || field.Name == "Raw" || field.Name == "Ciphertext" || field.Name == "EncryptionNonce" || field.Name == "OwnerID" || field.Name == "PermitID" {
				t.Fatal("private material/capability in result", field.Name)
			}
		}
	}
	root := filepath.Join("..", "..")
	for _, directory := range []string{"cmd", "internal"} {
		if err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "manual_submission_artifact.go" {
				return err
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name == "manualSubmissionArtifactReader" {
					t.Errorf("private handoff reader wired into %s", path)
				}
				return true
			})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := (manualArtifactReadCipher{}).Encrypt(nil, nil); !errors.Is(err, manualartifact.ErrCipher) {
		t.Fatal("readonly adapter encryption did not fail closed", err)
	}
}

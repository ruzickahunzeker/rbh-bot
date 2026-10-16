package tradeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

const manualSigningCrashPrefix = "RBH_MANUAL_B3D_CRASH="

type manualSigningCrashDescription struct {
	Path          string
	Identity      Identity
	Record        Record
	Scope         manualAdmissionScope
	SignerCalls   int64
	ObserverCalls int64
}

// This exits a real test subprocess with SQLite still open, including inside
// the uncommitted artifact transaction. It proves process-exit rediscovery,
// not filesystem power-loss or storage-hardware durability.
func TestHTTPM3B3DProcessCrashRediscovery(t *testing.T) {
	points := []string{"after_marker_commit", "after_sign", "after_artifact_insert", "after_artifact_commit"}
	if point := os.Getenv("RBH_MANUAL_B3D_CRASH_POINT"); point != "" {
		valid := false
		for _, expected := range points {
			valid = valid || point == expected
		}
		if !valid {
			t.Fatal("invalid controlled crash point")
		}
		_, _, h, r, scope, path, w, signer, observer := localSigningFixture(t, "buy")
		w.checkpoint = func(at string) error {
			if at != point {
				return nil
			}
			description := manualSigningCrashDescription{Path: path, Identity: h.identity, Record: r, Scope: scope, SignerCalls: signer.calls.Load(), ObserverCalls: observer.calls.Load()}
			raw, err := json.Marshal(description)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintf(os.Stdout, "%s%s\n", manualSigningCrashPrefix, raw); err != nil {
				t.Fatal(err)
			}
			os.Exit(86) // no Rollback/Close/deferred cleanup can run
			return nil
		}
		_, err := w.sign(context.Background(), h.identity, r.RequestID, scope)
		t.Fatal("controlled crash point was not reached", point, err)
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHTTPM3B3DProcessCrashRediscovery$", "-test.timeout=80s")
			// Give all child test-owned temporary DBs a parent-owned disposable
			// directory; no wallet key is serialized in arguments/env/files.
			for _, item := range os.Environ() {
				if !strings.HasPrefix(item, "TMPDIR=") && !strings.HasPrefix(item, "RBH_MANUAL_B3D_CRASH_POINT=") {
					cmd.Env = append(cmd.Env, item)
				}
			}
			cmd.Env = append(cmd.Env, "TMPDIR="+root, "RBH_MANUAL_B3D_CRASH_POINT="+point)
			out, err := cmd.CombinedOutput()
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != 86 || ctx.Err() != nil {
				t.Fatalf("child did not exit at controlled point %s: %v %s", point, err, out)
			}
			var description manualSigningCrashDescription
			found := false
			for _, line := range bytes.Split(out, []byte{'\n'}) {
				if bytes.HasPrefix(line, []byte(manualSigningCrashPrefix)) {
					if found || json.Unmarshal(line[len(manualSigningCrashPrefix):], &description) != nil {
						t.Fatal("invalid child metadata")
					}
					found = true
				}
			}
			if !found || !strings.HasPrefix(description.Path, root+string(os.PathSeparator)) || description.ObserverCalls != 1 {
				t.Fatal("missing controlled child metadata")
			}
			calls := int64(1)
			if point == "after_marker_commit" {
				calls = 0
			}
			if description.SignerCalls != calls {
				t.Fatal("unexpected child signer count", description.SignerCalls, calls)
			}
			db, err := storage.Open(context.Background(), storage.TradeOwner, description.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			s, err := NewStore(db)
			if err != nil {
				t.Fatal(err)
			}
			cipher, err := trade.NewAESGCMCipher("local-signing-fixture", bytes.Repeat([]byte{7}, 32))
			if err != nil {
				t.Fatal(err)
			}
			// No signer, observer or clock exists in the restarted composition.
			fresh := &manualLocalSigningWriter{store: s, cipher: cipher}
			before := admittedDatabaseSnapshot(t, s.db)
			want := "SIGNING_OUTCOME_UNKNOWN_NOT_PERMISSION"
			wantArtifact := 0
			if point == "after_artifact_commit" {
				wantArtifact = 1
				want = "HISTORICAL_VERIFIED_ARTIFACT_NOT_PERMISSION"
				assertArtifactPhase(t, s, description.Record)
			} else {
				assertNonceClaim(t, s, description.Record, "23")
				if count(t, s.db, "manual_signed_artifact_commands") != 0 {
					t.Fatal("uncommitted artifact escaped process exit")
				}
			}
			for _, table := range []string{"manual_signing_witness_creation_scopes", "manual_artifact_effect_scopes"} {
				if count(t, s.db, table) != 0 {
					t.Fatal("process exit retained transient creation scope", point, table)
				}
			}
			var markerAudits, artifactAudits int
			if err := s.db.QueryRow(`SELECT count(*) FROM canary_runtime_audit WHERE event_type='MANUAL_SIGNING_INTENT'`).Scan(&markerAudits); err != nil || markerAudits != 1 {
				t.Fatal("process exit lost or duplicated marker audit", markerAudits, err)
			}
			if err := s.db.QueryRow(`SELECT count(*) FROM canary_runtime_audit WHERE event_type='MANUAL_ARTIFACT_STORED'`).Scan(&artifactAudits); err != nil || artifactAudits != wantArtifact {
				t.Fatal("process exit retained half artifact audit", artifactAudits, err)
			}
			var original manualLocalSigningResult
			for retry := 0; retry < 3; retry++ {
				got, err := fresh.sign(context.Background(), description.Identity, description.Record.RequestID, description.Scope)
				if err != nil || !got.Duplicate || got.Status != want || got.ExecutionAuthorized || got.SigningAuthorized || got.SendAuthorized || admittedDatabaseSnapshot(t, s.db) != before {
					t.Fatal("restart lost work or acquired fresh action", point, got, err)
				}
				if retry > 0 && got != original {
					t.Fatal("restart changed exact historical identity")
				}
				original = got
			}
		})
	}
}

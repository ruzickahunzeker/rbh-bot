package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
)

func TestManualArtifactFactsScalarCompleteBytesAndPoolReplacement(t *testing.T) {
	d, err := Open(context.Background(), TradeOwner, filepath.Join(t.TempDir(), "artifact-scalars.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	f := manualartifact.Facts{Version: 1, OperationID: "op", RequestID: "request", ExecutionStepID: "manual-exec:op", AttemptID: "manual-nonce:op", WalletID: "wallet", ReservationID: "manual-risk:op",
		ContractHash: strings.Repeat("a", 64), ChainID: 4663, From: "0x" + strings.Repeat("1", 40), To: "0x" + strings.Repeat("2", 40), Value: "0", Data: "0x1234", Nonce: "0", GasLimit: 100000, GasFeeCap: "1000000000", GasTipCap: "0", PolicyVersion: 1, PolicyHash: strings.Repeat("b", 64), QuoteBlockNumber: 1, QuoteBlockHash: "0x" + strings.Repeat("c", 64), RuntimeCodeHash: "0x" + strings.Repeat("d", 64), ExpiresAt: "2026-10-14T00:00:30Z", ClaimedAt: "2026-10-14T00:00:00Z", ClaimObservationHash: strings.Repeat("e", 64), DeadlineCapability: "APPLICATION_TTL_ONLY",
		AuthorizationID: "auth", AuthorizationEpoch: 1, ManualPolicyID: "policy", ManualPolicyHash: strings.Repeat("f", 64), GateEvidenceSetHash: strings.Repeat("1", 64), Environment: "local", BuildSHA: strings.Repeat("2", 40), ReleaseID: "local-release", DeploymentID: "local-deployment"}
	raw, want, err := f.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	d.db.SetMaxIdleConns(0)
	for repeat := 0; repeat < 3; repeat++ {
		var got string
		if err := d.db.QueryRow(`SELECT manual_artifact_facts_hash(hex(?),typeof(?))`, raw, raw).Scan(&got); err != nil || got != want {
			t.Fatal("scalar hash changed on pool replacement", got, err)
		}
		for _, bad := range []any{nil, []byte(raw), raw + "\x00", raw + " ", strings.Replace(raw, `"contract_deadline":false`, `"contract_deadline":true`, 1)} {
			if err := d.db.QueryRow(`SELECT manual_artifact_facts_hash(hex(?),typeof(?))`, bad, bad).Scan(&got); err == nil {
				t.Fatal("malformed facts scalar accepted")
			}
		}
		for _, encoded := range []string{"", "00", "610062", "FF"} {
			bytes, err := hex.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(bytes)
			if err := d.db.QueryRow(`SELECT manual_artifact_bytes_sha256(?)`, encoded).Scan(&got); err != nil || got != hex.EncodeToString(digest[:]) {
				t.Fatal("full byte digest", err)
			}
		}
		if err := d.db.QueryRow(`SELECT manual_artifact_bytes_sha256('not-hex')`).Scan(&got); err == nil {
			t.Fatal("malformed encoded bytes accepted")
		}
		for _, value := range []any{"2026-10-14T00:00:00Z", "2026-10-14T00:00:00Z\x00", []byte("2026-10-14T00:00:00Z"), "2026-10-14T00:00:00+00:00", nil} {
			var valid int
			want := 0
			if text, ok := value.(string); ok && text == "2026-10-14T00:00:00Z" {
				want = 1
			}
			if err := d.db.QueryRow(`SELECT manual_artifact_time_valid(hex(?),typeof(?))`, value, value).Scan(&valid); err != nil || valid != want {
				t.Fatal("full-byte canonical time", valid, want, err)
			}
		}
	}
}

package tradeapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestManualExecutionSnapshotIsOwnedAndReadOnly(t *testing.T) {
	_, store, original, _ := fixture(t)
	h := intakeAPI(t, store, original)
	created := manualRecord(t, call(h, "POST", "/v1/trades", "execution-snapshot", encode(buy())))
	before := count(t, store.db, "canary_runtime_audit")
	for _, path := range []string{"/v1/trades/" + created.RequestID, "/v1/trades/by-idempotency-key"} {
		response := call(h, http.MethodGet, path, "execution-snapshot", "")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"operation_status":"manual_validation_pending"`) || !strings.Contains(response.Body.String(), `"step_status":"validation_pending"`) || strings.Contains(response.Body.String(), "encrypted_raw_tx") {
			t.Fatalf("unsafe or missing execution projection: %d %s", response.Code, response.Body.String())
		}
	}
	if got := count(t, store.db, "canary_runtime_audit"); got != before {
		t.Fatalf("query wrote audit: %d -> %d", before, got)
	}
	other := Identity{PrincipalID: "other-principal", WalletID: "wallet-01", WalletAddress: testWallet}
	if _, err := store.manualExecutionSnapshot(context.Background(), other, created.RequestID); err != ErrNotFound {
		t.Fatalf("cross-principal query: %v", err)
	}
	other = Identity{PrincipalID: "caller-01", WalletID: "other-wallet", WalletAddress: testWallet}
	if _, err := store.manualExecutionSnapshot(context.Background(), other, created.RequestID); err != ErrNotFound {
		t.Fatalf("cross-wallet query: %v", err)
	}
}

func TestManualExecutionSnapshotFindsDurableTradeStep(t *testing.T) {
	_, store, h, order, scope, _, writer, _, _ := localSigningFixture(t, "buy")
	if _, err := writer.sign(context.Background(), h.identity, order.RequestID, scope); err != nil {
		t.Fatal(err)
	}
	before := count(t, store.db, "transaction_submissions")
	got, err := store.Get(context.Background(), h.identity, order.RequestID, false)
	if err != nil || got.Execution == nil || got.Execution.TradeStepStatus != "signed" || got.Execution.AttemptStatus != "signed" || got.Execution.TxHash == "" || got.Execution.SubmissionState != "" || got.Execution.ReceiptState != "" || got.Execution.EffectState != "" {
		t.Fatalf("durable signed state projection: %+v %v", got.Execution, err)
	}
	if count(t, store.db, "transaction_submissions") != before {
		t.Fatal("read-only snapshot created submission")
	}
}

package trade

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func canaryStore(t *testing.T) *Store {
	t.Helper()
	db, e := storage.Open(context.Background(), storage.TradeOwner, filepath.Join(t.TempDir(), "trade.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = db.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	s, e := NewStore(db)
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func seedCanary(t *testing.T, s *Store, stopped int) {
	t.Helper()
	stamp := "2026-01-01T00:00:00Z"
	p := canaryPolicy{Version: 1, Protocol: CanaryProtocol, MaxOperation: "100", MaxWallet: "20000", MaxToken: "20000", MaxTotal: "20000", MaxGas: "1000000", MaxFee: "100", MaxTip: "10", MinBalance: "1", MaxSellBPS: 10000, MaxSlippageBPS: 500, MaxOperations: 20000, WindowSeconds: 3600, MaxUnresolved: 1}
	_, e := s.db.Exec(`INSERT INTO canary_policies VALUES(1,'PONS_V2_CURVE','100','10000','20000','20000','20000',20000,3600,1,'1000000','100','10','500','1',?,?)`, p.hash(), stamp)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec(`INSERT INTO canary_control_state VALUES(1,4663,'CONTROLLED_CANARY',1,?,'test','test',?,?)`, stopped, stamp, stamp)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec(`INSERT INTO canary_wallet_allowlist VALUES('wallet-1','0x1000000000000000000000000000000000000001',4663,1,1,?)`, stamp)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec(`INSERT INTO canary_contract_allowlist VALUES('0x3000000000000000000000000000000000000003','CURVE','PONS_V2_CURVE','0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',1,1,'C02',?)`, stamp)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec(`INSERT INTO canary_token_allowlist VALUES('0x2000000000000000000000000000000000000002','PONS_V2_CURVE',1,1,?)`, stamp)
	if e != nil {
		t.Fatal(e)
	}
}

func canaryRequest(id string, now time.Time) CanaryRequest {
	return CanaryRequest{RequestID: id, OperationID: "op-" + id, WalletID: "wallet-1", WalletAddress: "0x1000000000000000000000000000000000000001", TokenAddress: "0x2000000000000000000000000000000000000002", ContractAddress: "0x3000000000000000000000000000000000000003", ContractRole: "CURVE", RuntimeCodeHash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Protocol: CanaryProtocol, Direction: "BUY", Amount: "1", GasLimit: 100000, GasFeeCap: "10", GasTipCap: "1", NativeBalance: "2000000", SlippageBPS: 100, PolicyVersion: 1, QuoteBlockNumber: 100, QuoteBlockHash: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano)}
}

func persistSource(t *testing.T, s *Store, r CanaryRequest) {
	t.Helper()
	stamp := "2026-01-01T00:00:00Z"
	_, e := s.db.Exec(`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at,updated_at,policy_version,deadline_capability,expires_at) VALUES(?,4663,?,?,?,'swap','dry_run_succeeded',?,?,?,?,?)`, r.OperationID, r.WalletID, r.RequestID, canaryID("source-operation", r.OperationID), stamp, stamp, r.PolicyVersion, "APPLICATION_TTL_ONLY", r.ExpiresAt)
	if e != nil {
		t.Fatal(e)
	}
	src := sourceFromRequest(r)
	_, e = s.db.Exec(`INSERT INTO canary_admission_sources VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, src.OperationID, src.WalletID, src.WalletAddress, src.Protocol, src.Direction, src.TokenAddress, src.ContractAddress, src.ContractRole, src.RuntimeCodeHash, src.Amount, src.GasLimit, src.GasFeeCap, src.GasTipCap, src.NativeBalance, src.SlippageBPS, src.SellBPS, src.PolicyVersion, src.QuoteBlockNumber, src.QuoteBlockHash, src.ExpiresAt, src.hash(), stamp)
	if e != nil {
		t.Fatal(e)
	}
}
func sourceFromRequest(r CanaryRequest) canarySource {
	return canarySource{OperationID: r.OperationID, WalletID: r.WalletID, WalletAddress: r.WalletAddress, Protocol: r.Protocol, Direction: r.Direction, TokenAddress: r.TokenAddress, ContractAddress: r.ContractAddress, ContractRole: r.ContractRole, RuntimeCodeHash: r.RuntimeCodeHash, Amount: r.Amount, GasLimit: fmt.Sprint(r.GasLimit), GasFeeCap: r.GasFeeCap, GasTipCap: r.GasTipCap, NativeBalance: r.NativeBalance, SlippageBPS: r.SlippageBPS, SellBPS: r.SellBPS, PolicyVersion: r.PolicyVersion, QuoteBlockNumber: r.QuoteBlockNumber, QuoteBlockHash: r.QuoteBlockHash, ExpiresAt: r.ExpiresAt}
}

func TestCanaryEmergencyStopHasHighestPriority(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 1)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	r := canaryRequest("stop", now)
	r.Protocol = "PONS_V4"
	r.ExpiresAt = now.Format(time.RFC3339Nano)
	d, e := s.AdmitControlledCanary(context.Background(), r, now)
	if !errors.Is(e, ErrCanaryRejected) || d.ReasonCode != "EMERGENCY_STOPPED" {
		t.Fatalf("priority: %+v %v", d, e)
	}
}

func TestCanaryMissingControlFailsClosedAndAudits(t *testing.T) {
	s := canaryStore(t)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	r := canaryRequest("missing-control", now)
	d, err := s.AdmitControlledCanary(context.Background(), r, now)
	if !errors.Is(err, ErrCanaryRejected) || d.ReasonCode != "CONTROL_STATE_UNAVAILABLE" {
		t.Fatalf("missing control %+v %v", d, err)
	}
	var decisions, attempts int
	s.db.QueryRow(`SELECT COUNT(*) FROM canary_admission_decisions`).Scan(&decisions)
	s.db.QueryRow(`SELECT COUNT(*) FROM canary_admission_attempts`).Scan(&attempts)
	if decisions != 1 || attempts != 1 {
		t.Fatalf("decisions=%d attempts=%d", decisions, attempts)
	}
}

func TestCanaryDurableSourceAndPolicyAreImmutable(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	r := canaryRequest("source", now)
	persistSource(t, s, r)
	tampered := r
	tampered.NativeBalance = "999999999"
	d, e := s.AdmitControlledCanary(context.Background(), tampered, now)
	if !errors.Is(e, ErrCanaryRejected) || d.ReasonCode != "DURABLE_SOURCE_MISMATCH" {
		t.Fatalf("source mismatch: %+v %v", d, e)
	}
	if _, e = s.db.Exec(`UPDATE canary_policies SET max_operation_input='999' WHERE version=1`); e == nil {
		t.Fatal("policy mutation accepted")
	}
	if _, e = s.db.Exec(`UPDATE canary_gate_attestations SET evidence_hash=? WHERE gate='C04'`, requiredCanaryGates["C05"]); e == nil {
		t.Fatal("attestation mutation accepted")
	}
}

func TestCanaryAdmissionAuditMetricsAndRestart(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	r := canaryRequest("one", now)
	persistSource(t, s, r)
	d, e := s.AdmitControlledCanary(context.Background(), r, now)
	if e != nil || d.Decision != "ADMITTED" {
		t.Fatalf("admit %+v %v", d, e)
	}
	d, e = s.AdmitControlledCanary(context.Background(), r, now)
	if e != nil || d.Decision != "DEDUPED" {
		t.Fatalf("dedupe %+v %v", d, e)
	}
	m, e := s.CanaryMetrics(context.Background())
	if e != nil || m.Admitted != 1 || m.Deduped != 1 || m.Reservations != 1 {
		t.Fatalf("metrics %+v %v", m, e)
	}
	var attempts int
	s.db.QueryRow(`SELECT COUNT(*) FROM canary_admission_attempts`).Scan(&attempts)
	if attempts != 2 {
		t.Fatalf("attempts=%d", attempts)
	}
}

func TestCanaryRestartDoesNotMintReservationOrUsage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "restart.db")
	db, err := storage.Open(ctx, storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	first, _ := NewStore(db)
	seedCanary(t, first, 0)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	r := canaryRequest("restart", now)
	persistSource(t, first, r)
	if _, err = first.AdmitControlledCanary(ctx, r, now); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(ctx, storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	restarted, _ := NewStore(db)
	d, err := restarted.AdmitControlledCanary(ctx, r, now)
	if err != nil || d.Decision != "DEDUPED" {
		t.Fatalf("restart %+v %v", d, err)
	}
	var reservations, usage int
	restarted.db.QueryRow(`SELECT COUNT(*) FROM canary_risk_reservations`).Scan(&reservations)
	restarted.db.QueryRow(`SELECT SUM(operation_count) FROM canary_window_usage`).Scan(&usage)
	if reservations != 1 || usage != 1 {
		t.Fatalf("restart reservations=%d usage=%d", reservations, usage)
	}
}

func TestCanaryConcurrentAdmissionsConserveTenThousand(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	total, duplicateRequests := 10000, 5000
	if raceEnabled {
		total, duplicateRequests = 1000, 500
	}
	requests := make([]CanaryRequest, total)
	requests[0] = canaryRequest("concurrent-economic-identity", now)
	persistSource(t, s, requests[0])
	for i := 1; i < duplicateRequests; i++ {
		requests[i] = requests[0]
	}
	for i := duplicateRequests; i < total; i++ {
		requests[i] = canaryRequest(fmt.Sprintf("distinct-%d", i), now)
		persistSource(t, s, requests[i])
	}
	var wg sync.WaitGroup
	wg.Add(total)
	type outcome struct {
		decision CanaryDecision
		err      error
	}
	results := make(chan outcome, total)
	for i := 0; i < total; i++ {
		go func(r CanaryRequest) {
			defer wg.Done()
			d, e := s.AdmitControlledCanary(context.Background(), r, now)
			results <- outcome{d, e}
		}(requests[i])
	}
	wg.Wait()
	close(results)
	counts := map[string]int{}
	for result := range results {
		counts[result.decision.Decision]++
		if result.decision.Decision == "REJECTED" {
			if !errors.Is(result.err, ErrCanaryRejected) || result.decision.ReasonCode != "WALLET_LANE_BUSY" {
				t.Fatalf("unexpected rejection: %+v %v", result.decision, result.err)
			}
		} else if result.err != nil {
			t.Fatal(result.err)
		}
	}
	var attempts, reservations int
	s.db.QueryRow(`SELECT COUNT(*) FROM canary_admission_attempts`).Scan(&attempts)
	s.db.QueryRow(`SELECT COUNT(*) FROM canary_risk_reservations`).Scan(&reservations)
	if counts["ADMITTED"] != 1 || counts["DEDUPED"] != duplicateRequests-1 || counts["REJECTED"] != total-duplicateRequests || attempts != total || reservations != 1 {
		t.Fatalf("counts=%v attempts=%d reservations=%d", counts, attempts, reservations)
	}
}

func TestCanaryRiskAccountingUsesDerivedGasCost(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	r := canaryRequest("gas", now)
	r.NativeBalance = "1000000"
	persistSource(t, s, r)
	d, e := s.AdmitControlledCanary(context.Background(), r, now)
	if !errors.Is(e, ErrCanaryRejected) || d.ReasonCode != "MINIMUM_BALANCE_VIOLATION" {
		t.Fatalf("gas budget bypass: %+v %v", d, e)
	}
}

func TestCanaryAdmissionFaultsRollbackAllRows(t *testing.T) {
	for _, stage := range []string{"after_usage", "after_reservation", "after_decision", "before_commit"} {
		t.Run(stage, func(t *testing.T) {
			s := canaryStore(t)
			seedCanary(t, s, 0)
			now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
			r := canaryRequest(stage, now)
			persistSource(t, s, r)
			s.SetCanaryHookForTest(func(got string) error {
				if got == stage {
					return errors.New("fault")
				}
				return nil
			})
			if _, e := s.AdmitControlledCanary(context.Background(), r, now); e == nil {
				t.Fatal("fault escaped")
			}
			for _, q := range []string{`SELECT COUNT(*) FROM canary_window_usage`, `SELECT COUNT(*) FROM canary_risk_reservations`, `SELECT COUNT(*) FROM canary_admission_decisions`, `SELECT COUNT(*) FROM canary_audit_events`, `SELECT COUNT(*) FROM canary_admission_attempts`} {
				var n int
				if err := s.db.QueryRow(q).Scan(&n); err != nil || n != 0 {
					t.Fatalf("partial state q=%s n=%d err=%v", q, n, err)
				}
			}
		})
	}
}

func TestCanarySchemaRejectsUnrestrictedLive(t *testing.T) {
	s := canaryStore(t)
	_, e := s.db.Exec(`INSERT INTO canary_control_state VALUES(1,4663,'UNRESTRICTED_LIVE',1,1,'x','x','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if e == nil {
		t.Fatal("unrestricted live accepted")
	}
}

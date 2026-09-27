package trade

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

func canaryStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.TradeOwner, filepath.Join(t.TempDir(), "trade.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func seedCanary(t *testing.T, s *Store, stopped int) {
	t.Helper()
	stamp := "2026-01-01T00:00:00Z"
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, g := range []string{"C04", "C05", "C08"} {
		if _, e := s.db.Exec(`INSERT INTO canary_gate_attestations VALUES(?,?,?,?)`, g, "PASS", hash, stamp); e != nil {
			t.Fatal(e)
		}
	}
	_, err := s.db.Exec(`INSERT INTO canary_policies VALUES(1,'PONS_V2_CURVE','100','10000','20000','20000','20000',20000,3600,1,'1000000','100','10','500','1',?,?)`, hash, stamp)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO canary_control_state VALUES(1,4663,'CONTROLLED_CANARY',1,?,'test','test',?,?)`, stopped, stamp, stamp)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO canary_wallet_allowlist VALUES('wallet-1','0x1000000000000000000000000000000000000001',4663,1,1,?)`, stamp)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO canary_contract_allowlist VALUES('0x3000000000000000000000000000000000000003','CURVE','PONS_V2_CURVE','0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',1,1,'C02',?)`, stamp)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO canary_token_allowlist VALUES('0x2000000000000000000000000000000000000002','PONS_V2_CURVE',1,1,?)`, stamp)
	if err != nil {
		t.Fatal(err)
	}
}

func canaryRequest(id string, now time.Time) CanaryRequest {
	return CanaryRequest{RequestID: id, OperationID: "op-" + id, WalletID: "wallet-1", WalletAddress: "0x1000000000000000000000000000000000000001", TokenAddress: "0x2000000000000000000000000000000000000002", ContractAddress: "0x3000000000000000000000000000000000000003", ContractRole: "CURVE", RuntimeCodeHash: "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Protocol: CanaryProtocol, Direction: "BUY", Amount: "1", GasBudget: "1", GasLimit: 100000, GasFeeCap: "10", GasTipCap: "1", NativeBalance: "1000", SlippageBPS: 100, PolicyVersion: 1, ExpiresAt: now.Add(time.Hour)}
}

func TestCanaryFailsClosedWithoutControlAndOnEmergencyStop(t *testing.T) {
	s := canaryStore(t)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	if d, e := s.AdmitControlledCanary(context.Background(), canaryRequest("missing", now), now); e == nil || d.Decision != "REJECTED" {
		t.Fatalf("missing control escaped: %+v %v", d, e)
	}
	seedCanary(t, s, 1)
	if d, e := s.AdmitControlledCanary(context.Background(), canaryRequest("stopped", now), now); e == nil || d.ReasonCode != "EMERGENCY_STOPPED" {
		t.Fatalf("stop escaped: %+v %v", d, e)
	}
}

func TestCanaryAdmissionIsAtomicAndDuplicateDoesNotReserveAgain(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	r := canaryRequest("one", now)
	d, e := s.AdmitControlledCanary(context.Background(), r, now)
	if e != nil || d.Decision != "ADMITTED" {
		t.Fatalf("admit: %+v %v", d, e)
	}
	d, e = s.AdmitControlledCanary(context.Background(), r, now)
	if e != nil || !d.Duplicate || d.Decision != "DEDUPED" {
		t.Fatalf("dedupe: %+v %v", d, e)
	}
	var decisions, reservations, usage, audits int
	for q, p := range map[string]*int{`SELECT COUNT(*) FROM canary_admission_decisions`: &decisions, `SELECT COUNT(*) FROM canary_risk_reservations`: &reservations, `SELECT operation_count FROM canary_window_usage`: &usage, `SELECT COUNT(*) FROM canary_audit_events`: &audits} {
		if e = s.db.QueryRow(q).Scan(p); e != nil {
			t.Fatal(e)
		}
	}
	if decisions != 1 || reservations != 1 || usage != 1 || audits != 1 {
		t.Fatalf("non-atomic counts %d %d %d %d", decisions, reservations, usage, audits)
	}
	metrics, err := s.CanaryMetrics(context.Background())
	if err != nil || metrics.Admitted != 1 || metrics.Rejected != 0 || metrics.Reservations != 1 || metrics.EmergencyStopped != 0 {
		t.Fatalf("metrics mismatch: %+v %v", metrics, err)
	}
}

func TestCanaryConcurrentAdmissionsConserveRequests(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	const total = 10000
	var wg sync.WaitGroup
	wg.Add(total)
	results := make(chan CanaryDecision, total)
	errs := make(chan error, total)
	r := canaryRequest("concurrent-economic-identity", now)
	for i := 0; i < total; i++ {
		go func() {
			defer wg.Done()
			d, e := s.AdmitControlledCanary(context.Background(), r, now)
			results <- d
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var admitted, deduped, queued, rejected int
	for d := range results {
		switch d.Decision {
		case "ADMITTED":
			admitted++
		case "DEDUPED":
			deduped++
		case "QUEUED":
			queued++
		case "REJECTED":
			rejected++
		default:
			t.Fatalf("unexplained decision: %+v", d)
		}
	}
	var decisions, reservations, usage int
	s.db.QueryRow(`SELECT COUNT(*) FROM canary_admission_decisions`).Scan(&decisions)
	s.db.QueryRow(`SELECT COUNT(*) FROM canary_risk_reservations`).Scan(&reservations)
	s.db.QueryRow(`SELECT SUM(operation_count) FROM canary_window_usage`).Scan(&usage)
	if admitted+deduped+queued+rejected != total || admitted != 1 || deduped != total-1 || decisions != 1 || reservations != 1 || usage != 1 {
		t.Fatalf("conservation admitted=%d deduped=%d queued=%d rejected=%d durable=%d reservations=%d usage=%d", admitted, deduped, queued, rejected, decisions, reservations, usage)
	}
}

func TestCanaryAllowlistPolicyAndTTLFailClosedWithAudit(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	cases := []struct {
		id, reason string
		mutate     func(*CanaryRequest)
	}{
		{"protocol", "PROTOCOL_NOT_ALLOWED", func(r *CanaryRequest) { r.Protocol = "PONS_V4" }},
		{"wallet", "WALLET_NOT_ALLOWED", func(r *CanaryRequest) { r.WalletID = "other" }},
		{"contract", "CONTRACT_NOT_ALLOWED", func(r *CanaryRequest) {
			r.RuntimeCodeHash = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		}},
		{"token", "TOKEN_NOT_ALLOWED", func(r *CanaryRequest) { r.TokenAddress = "0x4000000000000000000000000000000000000004" }},
		{"ttl", "TTL_EXPIRED", func(r *CanaryRequest) { r.ExpiresAt = now }},
	}
	for _, tc := range cases {
		r := canaryRequest(tc.id, now)
		tc.mutate(&r)
		d, err := s.AdmitControlledCanary(context.Background(), r, now)
		if !errors.Is(err, ErrCanaryRejected) || d.ReasonCode != tc.reason {
			t.Fatalf("%s: decision=%+v err=%v", tc.id, d, err)
		}
	}
	var decisions, audits, reservations int
	s.db.QueryRow(`SELECT COUNT(*) FROM canary_admission_decisions WHERE decision='REJECTED'`).Scan(&decisions)
	s.db.QueryRow(`SELECT COUNT(*) FROM canary_audit_events`).Scan(&audits)
	s.db.QueryRow(`SELECT COUNT(*) FROM canary_risk_reservations`).Scan(&reservations)
	if decisions != len(cases) || audits != len(cases) || reservations != 0 {
		t.Fatalf("rejection persistence decisions=%d audits=%d reservations=%d", decisions, audits, reservations)
	}
}

func TestCanaryRequiresAllDurableGateAttestations(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	if _, err := s.db.Exec(`DELETE FROM canary_gate_attestations WHERE gate='C05'`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	d, err := s.AdmitControlledCanary(context.Background(), canaryRequest("gate", now), now)
	if !errors.Is(err, ErrCanaryRejected) || d.ReasonCode != "GATE_ATTESTATION_INVALID" {
		t.Fatalf("missing durable gate escaped: %+v %v", d, err)
	}
}

func TestCanaryPolicyVersionMismatchFailsClosed(t *testing.T) {
	s := canaryStore(t)
	seedCanary(t, s, 0)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	r := canaryRequest("policy-version", now)
	r.PolicyVersion = 2
	d, err := s.AdmitControlledCanary(context.Background(), r, now)
	if !errors.Is(err, ErrCanaryRejected) || d.ReasonCode != "POLICY_VERSION_MISMATCH" {
		t.Fatalf("policy mismatch escaped: %+v %v", d, err)
	}
}

func TestCanaryRestartRetainsDecisionAndReservation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "trade.db")
	database, err := storage.Open(ctx, storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	first, _ := NewStore(database)
	seedCanary(t, first, 0)
	now := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	r := canaryRequest("restart", now)
	if _, err = first.AdmitControlledCanary(ctx, r, now); err != nil {
		t.Fatal(err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = storage.Open(ctx, storage.TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	restarted, _ := NewStore(database)
	d, err := restarted.AdmitControlledCanary(ctx, r, now)
	if err != nil || !d.Duplicate {
		t.Fatalf("restart minted new admission: %+v %v", d, err)
	}
	var reservations int
	if err = restarted.db.QueryRow(`SELECT COUNT(*) FROM canary_risk_reservations`).Scan(&reservations); err != nil || reservations != 1 {
		t.Fatalf("restart reservation count=%d err=%v", reservations, err)
	}
}

func TestCanarySchemaRejectsUnrestrictedLive(t *testing.T) {
	s := canaryStore(t)
	_, err := s.db.Exec(`INSERT INTO canary_control_state VALUES(1,4663,'UNRESTRICTED_LIVE',1,1,'x','x','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("schema accepted unrestricted live")
	}
}

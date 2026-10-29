package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	"github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

type recoveryRPCFixture struct {
	mu        sync.Mutex
	calls     map[string]int
	failure   bool
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
	malformed bool
}

func (f *recoveryRPCFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var call struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	f.mu.Lock()
	f.calls[call.Method]++
	fail, entered, release, malformed := f.failure, f.entered, f.release, f.malformed
	f.mu.Unlock()
	if call.Method == "eth_getTransactionReceipt" && entered != nil {
		f.once.Do(func() { close(entered) })
		<-release
	}
	var result any
	switch call.Method {
	case "eth_chainId":
		result = "0x1237"
	case "eth_getTransactionReceipt", "eth_getTransactionByHash":
		result = nil
		if malformed && call.Method == "eth_getTransactionReceipt" {
			result = &types.Receipt{Status: 0, TxHash: common.HexToHash("0x9999"), BlockNumber: big.NewInt(1), BlockHash: common.HexToHash("0x1111"), Logs: []*types.Log{}}
		}
	case "eth_getTransactionCount":
		result = "0x7"
	default:
		fail = true
	}
	response := map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result}
	if fail {
		delete(response, "result")
		response["error"] = map[string]any{"code": -32000, "message": "controlled unavailable"}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (f *recoveryRPCFixture) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func a1Config(t *testing.T, url string) config.Config {
	t.Helper()
	root := t.TempDir()
	return config.Config{Service: config.TradeService, TradeStartupProfile: config.TradeProfileRecoveryOnly,
		DataDir: root, Database: filepath.Join(root, "trade.db"), Socket: filepath.Join(root, "trade.sock"),
		LogLevel: "ERROR", ChainID: 4663, InternalAuthSecret: strings.Repeat("i", 32), RPCURL: url,
		CanaryProductionMode: config.CanaryProductionModeDisabled,
		ArtifactKeyVersion:   "test-v1", ArtifactEncryptionKey: strings.Repeat("07", 32)}
}

func a1DB(t *testing.T, cfg config.Config) (*storage.Database, *sql.DB) {
	t.Helper()
	d, err := storage.Open(context.Background(), storage.TradeOwner, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Migrate(context.Background()); err != nil {
		d.Close()
		t.Fatal(err)
	}
	db, err := d.SQLDB(storage.TradeOwner)
	if err != nil {
		t.Fatal(err)
	}
	return d, db
}

// Synthetic imported LEGACY artifacts, not a MANUAL grant or a production send.
// One ephemeral test signature per row is setup; target app has no signer key.
func seedA1Work(t *testing.T, cfg config.Config) map[string]string {
	t.Helper()
	d, db := a1DB(t, cfg)
	defer d.Close()
	hashes := map[string]string{}
	stamp := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	expires := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	for _, state := range []string{"submitted", "broadcast_unknown"} {
		key, err := crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		from := crypto.PubkeyToAddress(key.PublicKey).Hex()
		to := common.HexToAddress("0x2000000000000000000000000000000000000002")
		tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(4663), Nonce: 7, To: &to, Value: big.NewInt(1), Gas: 21000, GasFeeCap: big.NewInt(2), GasTipCap: big.NewInt(1)}), types.LatestSignerForChainID(big.NewInt(4663)), key)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := tx.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		cipher, _ := trade.NewAESGCMCipher(cfg.ArtifactKeyVersion, bytes.Repeat([]byte{7}, 32))
		op, step, attempt, wallet := "a1-"+state, "step-"+state, "attempt-"+state, "wallet-"+state
		sealed, nonce, err := cipher.Encrypt(raw, []byte("rbh:4663:"+op+":"+step+":"+attempt))
		clear(raw)
		if err != nil {
			t.Fatal(err)
		}
		hashes[state] = tx.Hash().Hex()
		lane, risk := "signed", "signed"
		if state == "broadcast_unknown" {
			lane, risk = "frozen", "frozen"
		}
		queries := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO dry_run_wallets VALUES(?,4663,?,1)`, []any{wallet, from}},
			{`INSERT INTO operations(id,chain_id,wallet_id,idempotency_key,request_fingerprint,kind,status,created_at,policy_version,expires_at) VALUES(?,4663,?,?,'fixture','swap',?,?,1,?)`, []any{op, wallet, op, state, stamp, expires}},
			{`INSERT INTO execution_steps(id,operation_id,step_index,kind,status,created_at,updated_at,wallet_id,policy_version,quote_block_number,quote_block_hash,expires_at) VALUES(?,?,1,'pons_curve_execution',?,?,?,?,1,1,?,?)`, []any{step, op, "signed", stamp, stamp, wallet, common.HexToHash("0x11").Hex(), expires}},
			{`INSERT INTO transaction_attempts(id,step_id,nonce,tx_hash,status,created_at,chain_id,wallet_id,key_version,encryption_nonce,encrypted_raw_tx,raw_tx_hash,from_address,to_address,value,calldata,gas_limit,gas_tip_cap,gas_fee_cap,updated_at,policy_version,quote_block_number,quote_block_hash,expires_at) VALUES(?,?,'7',?,'signed',?,4663,?,?,?,?,?,?,?,'1','0x','21000','1','2',?,1,1,?,?)`, []any{attempt, step, tx.Hash().Hex(), stamp, wallet, cfg.ArtifactKeyVersion, nonce, sealed, tx.Hash().Hex(), from, to.Hex(), stamp, common.HexToHash("0x11").Hex(), expires}},
			{`INSERT INTO execution_wallet_lanes VALUES(?,?,?,?,?,'7',?,?)`, []any{wallet, from, lane, op, step, state, stamp}},
			{`INSERT INTO execution_reservations(operation_id,step_id,wallet_id,input_asset,input_amount,gas_budget,status,created_at,updated_at) VALUES(?,?,?,'native','1','42000',?,?,?)`, []any{op, step, wallet, risk, stamp, stamp}},
			{`INSERT INTO transaction_submissions(id,attempt_id,tx_hash,sequence,state,created_at,updated_at) VALUES(?,?,?,0,?,?,?)`, []any{"sub-" + state, attempt, tx.Hash().Hex(), state, stamp, stamp}},
		}
		for _, q := range queries {
			if _, err = db.Exec(q.q, q.args...); err != nil {
				t.Fatalf("seed %s: %v", q.q, err)
			}
		}
	}
	if _, err := db.Exec(`UPDATE canary_control_state SET emergency_stopped=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	return hashes
}

func a1Wait(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("controlled condition timed out")
}

func a1Client(socket string) *http.Client {
	return &http.Client{Timeout: time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
}

type a1Process struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error // synchronized by closed done
}

func startA1(t *testing.T, cfg config.Config) *a1Process {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p := &a1Process{cancel: cancel, done: make(chan struct{})}
	go func() { defer close(p.done); p.err = runConfigured(ctx, cfg) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-p.done:
		case <-time.After(15 * time.Second):
			t.Error("application did not stop")
		}
	})
	return p
}

func TestA1RecoveryOnlyActualStartupRediscoveryAndRestart(t *testing.T) {
	f := &recoveryRPCFixture{calls: map[string]int{}}
	rpc := httptest.NewServer(f)
	defer rpc.Close()
	cfg := a1Config(t, rpc.URL)
	hashes := seedA1Work(t, cfg)
	for round := 0; round < 2; round++ {
		d, db := a1DB(t, cfg)
		beforeArtifacts := a1ArtifactSnapshot(t, db)
		if round > 0 { // Clock-independent controlled takeover; never production lease deletion.
			if _, err := db.Exec(`UPDATE canary_worker_leases SET expires_at=?,updated_at=? WHERE role='RECOVERY'`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), time.Now().Add(-2*time.Minute).UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
		beforeReceipt, beforeTx := f.count("eth_getTransactionReceipt"), f.count("eth_getTransactionByHash")
		p := startA1(t, cfg)
		a1Wait(t, func() bool {
			return f.count("eth_getTransactionReceipt") >= beforeReceipt+2 && f.count("eth_getTransactionByHash") > beforeTx
		})
		client := a1Client(cfg.Socket)
		defer client.CloseIdleConnections()
		a1Wait(t, func() bool {
			r, e := client.Get("http://unix/ready")
			if e != nil {
				return false
			}
			defer r.Body.Close()
			return r.StatusCode == 200
		})
		for _, path := range []string{"/v1/capabilities", "/v1/trades/test", "/internal/trade/dry-run", "/internal/trade/prepare-execution"} {
			r, err := client.Post("http://unix"+path, "application/json", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			r.Body.Close()
			if r.StatusCode != 404 {
				t.Fatal("action/user route registered", path, r.StatusCode)
			}
		}
		r, err := client.Get("http://unix/metrics")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		for _, line := range []string{"rbh_canary_admission_ready 0", "rbh_submission_send_ready 0", "rbh_recovery_ready 1"} {
			if !strings.Contains(string(body), line) {
				t.Fatal("bad readiness", string(body))
			}
		}
		var epoch int
		if err = db.QueryRow(`SELECT lease_epoch FROM canary_worker_leases WHERE role='RECOVERY' AND environment='production'`).Scan(&epoch); err != nil || epoch != round+1 {
			t.Fatal("wrong epoch", epoch, err)
		}
		p.cancel()
		select {
		case <-p.done:
			if p.err != nil {
				t.Fatal(p.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("drain hung")
		}
		for state, hash := range hashes {
			var actual, nonce, sub string
			if err = db.QueryRow(`SELECT a.tx_hash,a.nonce,s.state FROM transaction_attempts a JOIN transaction_submissions s ON s.attempt_id=a.id WHERE a.id=?`, "attempt-"+state).Scan(&actual, &nonce, &sub); err != nil || actual != hash || nonce != "7" || sub != state {
				t.Fatal("history changed", state, actual, nonce, sub, err)
			}
		}
		if a1ArtifactSnapshot(t, db) != beforeArtifacts {
			t.Fatal("recovery rewrote an original artifact or nonce identity")
		}
		var lane, risk string
		if err = db.QueryRow(`SELECT l.state,r.status FROM execution_wallet_lanes l JOIN execution_reservations r ON r.wallet_id=l.wallet_id WHERE l.wallet_id='wallet-broadcast_unknown'`).Scan(&lane, &risk); err != nil || lane != "frozen" || risk != "frozen" {
			t.Fatal("unknown released", lane, risk, err)
		}
		for _, table := range []string{"canary_runtime_authorizations", "canary_authorization_operation_usage", "canary_send_permits", "manual_nonce_claims", "position_effects"} {
			var n int
			if err = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
				t.Fatal("unexpected economic work", table, n, err)
			}
		}
		var readyAudit int
		if err = db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit WHERE reason_code='BROADCAST_UNKNOWN_REMAINS_FROZEN'`).Scan(&readyAudit); err != nil || readyAudit < round+1 {
			t.Fatal("missing query audit", readyAudit, err)
		}
		d.Close()
	}
	if f.count("eth_sendRawTransaction") != 0 {
		t.Fatal("real send attempted")
	}
}

func TestA1RecoveryCipherRejectsEncryptUnknownVersionAndBadKey(t *testing.T) {
	cfg := a1Config(t, "http://127.0.0.1:1")
	c, err := loadRecoveryCipher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = c.Encrypt([]byte("not an artifact"), nil); err == nil {
		t.Fatal("encryption allowed")
	}
	if _, err = c.Decrypt("other", nil, nil, nil); !errors.Is(err, trade.ErrUnknownKeyVersion) {
		t.Fatal("unknown version accepted", err)
	}
	for _, bad := range []string{"", "not-hex", hex.EncodeToString([]byte{1})} {
		cfg.ArtifactEncryptionKey = bad
		if _, err = loadRecoveryCipher(cfg); err == nil {
			t.Fatal("bad key accepted")
		}
	}
}

func TestA1RecoveryOnlyListenerFailureDrainsAndStopsRenewal(t *testing.T) {
	f := &recoveryRPCFixture{calls: map[string]int{}}
	rpc := httptest.NewServer(f)
	defer rpc.Close()
	cfg := a1Config(t, rpc.URL)
	if err := os.WriteFile(cfg.Socket, []byte("preserve this non-socket file"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runConfigured(context.Background(), cfg) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "non-socket") {
			t.Fatal("wrong listener failure", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pre-listener worker leaked")
	}
	d, db := a1DB(t, cfg)
	defer d.Close()
	var expiry string
	if err := db.QueryRow(`SELECT expires_at FROM canary_worker_leases WHERE role='RECOVERY'`).Scan(&expiry); err != nil {
		t.Fatal("lease not acquired before failure", err)
	}
	time.Sleep(50 * time.Millisecond)
	var after string
	_ = db.QueryRow(`SELECT expires_at FROM canary_worker_leases WHERE role='RECOVERY'`).Scan(&after)
	if after != expiry {
		t.Fatal("failed startup keeps renewing lease")
	}
	data, _ := os.ReadFile(cfg.Socket)
	if string(data) != "preserve this non-socket file" {
		t.Fatal("listener target overwritten")
	}
}

func a1ArtifactSnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	var snapshot string
	err := db.QueryRow(`SELECT json_group_array(json_object('id',id,'step',step_id,'nonce',nonce,'hash',tx_hash,
 'version',key_version,'ciphertext',hex(encrypted_raw_tx),'encryption_nonce',hex(encryption_nonce),
 'policy',policy_version,'quote',quote_block_hash,'expiry',expires_at))
 FROM (SELECT * FROM transaction_attempts ORDER BY id)`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestA1RecoveryOnlyInvalidConfigHasNoSideEffects(t *testing.T) {
	cfg := a1Config(t, "http://127.0.0.1:1")
	cfg.DataDir = filepath.Join(cfg.DataDir, "not-created")
	cfg.ExecutionPrivateKey = "not-a-key"
	if err := runConfigured(context.Background(), cfg); err == nil {
		t.Fatal("execution config accepted")
	}
	if _, err := os.Stat(cfg.DataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid profile created runtime files", err)
	}
}

func TestA1RecoveryOnlyStartupRPCFailureAudited(t *testing.T) {
	f := &recoveryRPCFixture{calls: map[string]int{}, failure: true}
	rpc := httptest.NewServer(f)
	defer rpc.Close()
	cfg := a1Config(t, rpc.URL)
	if err := runConfigured(context.Background(), cfg); err == nil {
		t.Fatal("unhealthy RPC accepted")
	}
	d, db := a1DB(t, cfg)
	defer d.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit a JOIN canary_alert_outbox b ON b.audit_id=a.id WHERE a.reason_code='RECOVERY_HEALTH_UNAVAILABLE'`).Scan(&n); err != nil || n != 1 {
		t.Fatal("missing durable failure", n, err)
	}
	if _, err := os.Stat(cfg.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed startup opened listener", err)
	}
}

func TestA1RecoveryOnlyDrainWaitsForBoundedRead(t *testing.T) {
	f := &recoveryRPCFixture{calls: map[string]int{}, entered: make(chan struct{}), release: make(chan struct{})}
	rpc := httptest.NewServer(f)
	defer rpc.Close()
	cfg := a1Config(t, rpc.URL)
	seedA1Work(t, cfg)
	p := startA1(t, cfg)
	released := false
	defer func() {
		if !released {
			close(f.release)
		}
	}()
	select {
	case <-f.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no recovery read")
	}
	p.cancel()
	select {
	case <-p.done:
		t.Fatal("closed resources before in-flight read drained", p.err)
	case <-time.After(50 * time.Millisecond):
	}
	close(f.release)
	released = true
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatal(p.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("drain did not complete")
	}
	d, db := a1DB(t, cfg)
	defer d.Close()
	var state string
	if err := db.QueryRow(`SELECT state FROM execution_wallet_lanes WHERE wallet_id='wallet-broadcast_unknown'`).Scan(&state); err != nil || state != "frozen" {
		t.Fatal("drain released ambiguity", state, err)
	}
}

func TestA1RecoveryOnlyRuntimeFaultsFailClosed(t *testing.T) {
	for _, kind := range []string{"rpc", "wrong_key", "unknown_version", "lease_loss", "contradictory_receipt"} {
		t.Run(kind, func(t *testing.T) {
			f := &recoveryRPCFixture{calls: map[string]int{}}
			rpc := httptest.NewServer(f)
			defer rpc.Close()
			cfg := a1Config(t, rpc.URL)
			seedA1Work(t, cfg)
			d, db := a1DB(t, cfg)
			defer d.Close()
			if kind == "wrong_key" {
				cfg.ArtifactEncryptionKey = strings.Repeat("08", 32)
			}
			if kind == "unknown_version" {
				cfg.ArtifactKeyVersion = "unavailable-version"
			}
			if kind == "contradictory_receipt" {
				f.malformed = true
			}
			p := startA1(t, cfg)
			defer p.cancel()
			if kind == "rpc" || kind == "lease_loss" {
				a1Wait(t, func() bool { return f.count("eth_getTransactionReceipt") >= 2 })
				if kind == "rpc" {
					f.mu.Lock()
					f.failure = true
					f.mu.Unlock()
				} else {
					if _, err := db.Exec(`UPDATE canary_worker_leases SET holder_id='controlled-takeover',lease_epoch=lease_epoch+1,expires_at=? WHERE role='RECOVERY'`, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)); err != nil {
						t.Fatal(err)
					}
				}
			}
			select {
			case <-p.done:
				if p.err == nil {
					t.Fatal("runtime fault returned success")
				}
			case <-time.After(15 * time.Second):
				t.Fatal("fault did not stop runtime")
			}
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM canary_runtime_audit a JOIN canary_alert_outbox b ON b.audit_id=a.id WHERE a.reason_code IN ('RECOVERY_EVIDENCE_UNAVAILABLE','RECOVERY_RECONCILE_FAILED','RECOVERY_LEASE_LOST','RECOVERY_HEALTH_UNAVAILABLE')`).Scan(&n); err != nil || n < 1 {
				t.Fatal("missing durable fault", n, err)
			}
			var lane, risk string
			if err := db.QueryRow(`SELECT l.state,r.status FROM execution_wallet_lanes l JOIN execution_reservations r ON r.wallet_id=l.wallet_id WHERE l.wallet_id='wallet-broadcast_unknown'`).Scan(&lane, &risk); err != nil || lane != "frozen" || risk != "frozen" {
				t.Fatal("fault released ambiguity", lane, risk, err)
			}
			var effects int
			_ = db.QueryRow(`SELECT COUNT(*) FROM position_effects`).Scan(&effects)
			if effects != 0 || f.count("eth_sendRawTransaction") != 0 {
				t.Fatal("fault caused economic work")
			}
		})
	}
}

func TestA1RecoveryStartAuditFailureDoesNotLeakWorker(t *testing.T) {
	f := &recoveryRPCFixture{calls: map[string]int{}}
	rpc := httptest.NewServer(f)
	defer rpc.Close()
	cfg := a1Config(t, rpc.URL)
	d, db := a1DB(t, cfg)
	defer d.Close()
	if _, err := db.Exec(`CREATE TRIGGER test_a1_audit_failure BEFORE INSERT ON canary_runtime_audit WHEN NEW.reason_code='RECOVERY_STARTED_WITHOUT_SEND_AUTHORIZATION' BEGIN SELECT RAISE(ABORT,'forced startup audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runConfigured(context.Background(), cfg) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("audit failure returned success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker leaked after startup audit failure")
	}
	var leases int
	if err := db.QueryRow(`SELECT COUNT(*) FROM canary_worker_leases WHERE role='RECOVERY'`).Scan(&leases); err != nil || leases != 1 {
		t.Fatal("test missed post-acquisition failure", leases, err)
	}
	if _, err := os.Stat(cfg.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("startup failure registered listener")
	}
}

func TestA1RecoveryOnlyStaticCapabilityBoundary(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "run.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	guarded := 0
	forbidden := map[string]bool{"NewLocalSigner": true, "HexToECDSA": true, "NewExecutionKernel": true,
		"SubmissionService": true, "RawBroadcaster": true, "SendRawTransaction": true,
		"manualSubmissionExecutor": true, "IssueCanarySendPermit": true, "ReserveNonce": true,
		"configureUserTradeAPI": true, "RegisterDryRunWallet": true, "NewEngine": true, "DialRPCBackend": true}
	ast.Inspect(file, func(node ast.Node) bool {
		branch, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		condition, ok := branch.Cond.(*ast.BinaryExpr)
		if !ok || condition.Op != token.EQL {
			return true
		}
		rhs, ok := condition.Y.(*ast.SelectorExpr)
		if !ok || rhs.Sel.Name != "TradeProfileRecoveryOnly" {
			return true
		}
		guarded++
		allowed := map[string]bool{"loadRecoveryCipher": true}
		ast.Inspect(branch.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && !allowed[id.Name] {
				t.Errorf("recovery-only branch gained unreviewed call %s", id.Name)
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				t.Errorf("recovery-only branch gained unreviewed capability %s", selector.Sel.Name)
			}
			return true
		})
		// Exclude ONLY the legacy else. All startup code outside this reviewed
		// profile branch must remain free of the forbidden execution capabilities.
		branch.Else = nil
		return true
	})
	if guarded != 1 {
		t.Fatal("recovery profile no longer has one private capability branch", guarded)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && forbidden[id.Name] {
			t.Errorf("shared startup gained prohibited capability %s", id.Name)
		}
		return true
	})
	// New helper must not acquire signing, sending, allocator or grant symbols.
	helper, err := parser.ParseFile(token.NewFileSet(), "recovery_startup.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(helper, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && forbidden[id.Name] {
			t.Errorf("helper gained prohibited capability %s", id.Name)
		}
		return true
	})
}

func TestA1RecoveryOnlyDatabaseFailureDoesNotStartRPC(t *testing.T) {
	f := &recoveryRPCFixture{calls: map[string]int{}}
	rpc := httptest.NewServer(f)
	defer rpc.Close()
	cfg := a1Config(t, rpc.URL)
	cfg.Database = filepath.Join(cfg.DataDir, "missing-parent", "trade.db")
	if err := runConfigured(context.Background(), cfg); err == nil {
		t.Fatal("unavailable DB accepted")
	}
	if f.count("eth_chainId") != 0 {
		t.Fatal("failed DB startup reached RPC")
	}
	if _, err := os.Stat(cfg.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed DB startup opened listener")
	}
}

func TestA1RecoveryLifetimeReadinessDuringDrainAndFailure(t *testing.T) {
	for _, kind := range []string{"drain", "rpc_failure"} {
		t.Run(kind, func(t *testing.T) {
			f := &recoveryRPCFixture{calls: map[string]int{}, entered: make(chan struct{}), release: make(chan struct{})}
			rpc := httptest.NewServer(f)
			defer rpc.Close()
			cfg := a1Config(t, rpc.URL)
			seedA1Work(t, cfg)
			d, _ := a1DB(t, cfg)
			defer d.Close()
			store, err := trade.NewStore(d)
			if err != nil {
				t.Fatal(err)
			}
			cipher, err := loadRecoveryCipher(cfg)
			if err != nil {
				t.Fatal(err)
			}
			queryRPC, err := trade.DialReadOnlyRecoveryRPC(context.Background(), rpc.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer queryRPC.Close()
			resolver, err := trade.NewPonsCurveEffectResolver(store)
			if err != nil {
				t.Fatal(err)
			}
			recovery, err := trade.NewRecoveryServiceWithCipher(store, cipher, queryRPC, resolver, trade.CanonicalPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			querier, err := trade.NewProductionRecoveryQuerier(store, cipher, queryRPC)
			if err != nil {
				t.Fatal(err)
			}
			composition, err := trade.NewProductionComposition(store, trade.ProductionModeDisabled)
			if err != nil {
				t.Fatal(err)
			}
			if err = composition.ConfigureRecovery(recovery, querier, queryRPC, trade.ProductionRecoveryConfig{Environment: "production", HolderID: "a1-readiness", LeaseTTL: time.Minute, ScanInterval: 10 * time.Millisecond}); err != nil {
				t.Fatal(err)
			}
			loop, err := startRecoveryLifetime(context.Background(), composition)
			if err != nil {
				t.Fatal(err)
			}
			released := false
			defer func() {
				if !released {
					close(f.release)
				}
				loop.stopAndWait()
			}()
			select {
			case <-f.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("read did not start")
			}
			ready := composition.Readiness()
			if !ready.RecoveryReady || ready.CanaryAdmissionReady || ready.SubmissionSendReady {
				t.Fatal("readiness dimensions conflated", ready)
			}
			if kind == "drain" {
				loop.cancel()
				a1Wait(t, func() bool { return !composition.Readiness().RecoveryReady })
				select {
				case <-loop.done:
					t.Fatal("loop exited before drain")
				default:
				}
			} else {
				f.mu.Lock()
				f.failure = true
				f.mu.Unlock()
			}
			close(f.release)
			released = true
			select {
			case <-loop.done:
			case <-time.After(15 * time.Second):
				t.Fatal("loop did not stop")
			}
			if kind == "rpc_failure" && loop.err == nil {
				t.Fatal("failed RPC reported successful loop")
			}
			ready = composition.Readiness()
			if ready.RecoveryReady || ready.CanaryAdmissionReady || ready.SubmissionSendReady {
				t.Fatal("terminal readiness revived", ready)
			}
		})
	}
}

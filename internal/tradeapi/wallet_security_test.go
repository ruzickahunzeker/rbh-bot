package tradeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/bsc"
	"github.com/ruzickahunzeker/rbh-bot/internal/chains/robinhood"
	pancakev2 "github.com/ruzickahunzeker/rbh-bot/internal/protocols/pancakeswap/v2"
	"github.com/ruzickahunzeker/rbh-bot/internal/protocols/ports"
	"github.com/ruzickahunzeker/rbh-bot/internal/registry"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
	tradecore "github.com/ruzickahunzeker/rbh-bot/internal/trade"
)

type walletSecurityFixture struct {
	api, other       *API
	dir, token       string
	a, b, a2, shared tradecore.KeystoreSummary
}

func securityFixture(t *testing.T) walletSecurityFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, storage.TradeOwner, filepath.Join(t.TempDir(), "trade.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	makeWallet := func(id string, owner int64) tradecore.KeystoreSummary {
		w, e := tradecore.CreateGlobalEncryptedWallet(dir, id, owner, "review-test-passphrase")
		if e != nil {
			t.Fatal(e)
		}
		return w
	}
	f := walletSecurityFixture{dir: dir, token: strings.Repeat("s", 32), a: makeWallet("wallet-a", 1001), a2: makeWallet("wallet-a2", 1001), b: makeWallet("wallet-b", 2002), shared: makeWallet("wallet-shared", 0)}
	makeAPI := func(principal string, owner int64, chain uint64) *API {
		policy := DefaultPolicy()
		policy.AllowedChain = "bsc"
		profile := bsc.Profile()
		if chain == 4663 {
			profile = robinhood.Profile()
			policy.AllowedChain = "rbh"
		}
		access := WalletAccessPolicy{AllowedOwnerChatIDs: []int64{owner}, PrimaryCanSign: true, ReadonlyWallets: []ReadonlyWalletGrant{{WalletID: "watch-bsc", Address: "0x0000000000000000000000000000000000000056"}}}
		api, e := NewWithOptions(store, Identity{PrincipalID: principal, WalletID: "primary-" + principal, WalletAddress: common.HexToAddress("0x1234").Hex()}, f.token, policy, nil, Options{WalletAccess: &access})
		if e != nil {
			t.Fatal(e)
		}
		reg, e := registry.New(profile, []ports.ProtocolAdapter{}, map[string]registry.TrustedSpender{pancakev2.ProtocolID: {Address: bsc.PancakeV2Router}})
		if e != nil {
			t.Fatal(e)
		}
		ext, e := NewLocalV2Extension(reg, &recordingWalletProvider{}, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = api.ConfigureWalletResolver(ext, dir); e != nil {
			t.Fatal(e)
		}
		if e = api.AttachLocalV2(ext); e != nil {
			t.Fatal(e)
		}
		return api
	}
	f.api = makeAPI("a", 1001, 56)
	f.other = makeAPI("b", 2002, 4663)
	return f
}
func securityRequest(api *API, token, method, path, body, idem string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	if idem != "" {
		r.Header.Set("Idempotency-Key", idem)
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}
func approvalBody(chain, id string) string {
	return `{"chain":"` + chain + `","wallet_id":"` + id + `","protocol":"pancake-v2","token":"0x55d398326f99059fF775485246999027B3197955","amount_raw":"1000","ttl_seconds":60}`
}

func TestWalletSecurityPrincipalOwnershipAndReadonly(t *testing.T) {
	f := securityFixture(t)
	for _, id := range []string{f.b.WalletID, f.shared.WalletID, "does-not-exist"} {
		w := securityRequest(f.api, f.token, "GET", "/v1/wallets/"+id+"/positions?token=0x55d398326f99059fF775485246999027B3197955", "", "")
		if w.Code != 403 {
			t.Fatalf("foreign/shared wallet %s got %d", id, w.Code)
		}
		w = securityRequest(f.api, f.token, "POST", "/v1/approvals", approvalBody("bsc", id), "deny-"+id)
		if w.Code != 403 {
			t.Fatalf("unauthorized approval got %d", w.Code)
		}
	}
	for _, api := range []*API{f.api, f.other} {
		w := securityRequest(api, f.token, "GET", "/v1/wallets/watch-bsc/positions?token=0x55d398326f99059fF775485246999027B3197955", "", "")
		if w.Code != 200 {
			t.Fatalf("cross-chain readonly position got %d %s", w.Code, w.Body.String())
		}
	}
	w := securityRequest(f.api, f.token, "POST", "/v1/approvals", approvalBody("bsc", "watch-bsc"), "readonly-approval")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "READ_ONLY_WALLET") {
		t.Fatalf("readonly approval=%d %s", w.Code, w.Body.String())
	}
	resolver := NewKeystoreDirWalletResolver(f.dir, WalletAccessGrant{PrincipalID: "only-a", Policy: WalletAccessPolicy{AllowedOwnerChatIDs: []int64{1001}}})
	if _, ok := resolver.ResolveWallet(context.Background(), "other-principal", f.a.WalletID); ok {
		t.Fatal("grant leaked to a different principal")
	}
	sharedResolver := NewKeystoreDirWalletResolver(f.dir, WalletAccessGrant{PrincipalID: "shared-operator", Policy: WalletAccessPolicy{AllowSharedKeystores: true}})
	if _, ok := sharedResolver.ResolveWallet(context.Background(), "shared-operator", f.shared.WalletID); !ok {
		t.Fatal("explicit shared grant rejected")
	}
}

func TestWalletSecurityApprovalRecoveryAndSameKeys(t *testing.T) {
	f := securityFixture(t)
	ids := []string{f.a.WalletID, f.a2.WalletID, "primary-a"}
	records := map[string]ApprovalRecord{}
	for _, id := range ids {
		w := securityRequest(f.api, f.token, "POST", "/v1/approvals", approvalBody("bsc", id), "same-key")
		if w.Code != 202 {
			t.Fatalf("POST=%d %s", w.Code, w.Body.String())
		}
		var record ApprovalRecord
		if e := json.Unmarshal(w.Body.Bytes(), &record); e != nil {
			t.Fatal(e)
		}
		records[id] = record
		got := securityRequest(f.api, f.token, "GET", w.Header().Get("Location"), "", "")
		if got.Code != 200 {
			t.Fatalf("GET location=%d", got.Code)
		}
		got = securityRequest(f.api, f.token, "GET", "/v1/approvals/by-idempotency-key?wallet_id="+id+"&idempotency_key=same-key", "", "")
		if got.Code != 200 || !strings.Contains(got.Body.String(), record.RequestID) {
			t.Fatalf("scoped idempotency lookup=%d %s", got.Code, got.Body.String())
		}
		denied := securityRequest(f.other, f.token, "GET", w.Header().Get("Location"), "", "")
		if denied.Code != 404 {
			t.Fatalf("foreign approval disclosure=%d", denied.Code)
		}
	}
	if records[f.a.WalletID].RequestID == records[f.a2.WalletID].RequestID {
		t.Fatal("same key collided across wallets")
	}
	legacy := securityRequest(f.api, f.token, "GET", "/v1/approvals/by-idempotency-key?idempotency_key=same-key", "", "")
	if legacy.Code != 200 || !strings.Contains(legacy.Body.String(), records["primary-a"].RequestID) {
		t.Fatal("legacy by-key lookup did not select the configured primary")
	}
	denied := securityRequest(f.other, f.token, "GET", "/v1/approvals/by-idempotency-key?wallet_id="+f.a.WalletID+"&idempotency_key=same-key", "", "")
	if denied.Code != 404 {
		t.Fatal("unauthorized by-key lookup should hide the record")
	}
}

func TestWalletSecurityConflictsAndLifecycle(t *testing.T) {
	f := securityFixture(t)
	bad := NewKeystoreDirWalletResolver(f.dir, WalletAccessGrant{PrincipalID: "a", Policy: WalletAccessPolicy{ReadonlyWallets: []ReadonlyWalletGrant{{"same", "0x0000000000000000000000000000000000000001"}, {"same", "0x0000000000000000000000000000000000000002"}}}})
	if bad.Validate() == nil {
		t.Fatal("conflicting mapping accepted")
	}
	bad = NewKeystoreDirWalletResolver(f.dir, WalletAccessGrant{PrincipalID: "a", Policy: WalletAccessPolicy{ReadonlyWallets: []ReadonlyWalletGrant{{f.a.WalletID, "0x0000000000000000000000000000000000000002"}}}})
	if bad.Validate() == nil {
		t.Fatal("mapping/keystore conflict accepted")
	}
	if e := tradecore.WithWalletDirectoryLock(f.dir, func() error { return tradecore.MarkWalletDeletingLocked(f.dir, f.a.WalletID) }); e != nil {
		t.Fatal(e)
	}
	got := securityRequest(f.api, f.token, "GET", "/v1/wallets/"+f.a.WalletID+"/positions?token=0x55d398326f99059fF775485246999027B3197955", "", "")
	if got.Code != 403 {
		t.Fatal("deleting wallet remained resolvable")
	}
	if _, _, e := tradecore.LoadEncryptedWallet(f.a.FilePath, "review-test-passphrase"); e == nil {
		t.Fatal("deleting wallet private key was loaded")
	}
}

func TestWalletSecurityGrantConfigShape(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"wallet_access":{"allowed_owner_chat_ids":[1001,-1002],"readonly_wallets":[{"wallet_id":"watch","address":"0x0000000000000000000000000000000000000001"}],"allow_shared_keystores":false}}`, true},
		{`{"wallet_access":{"allowed_owner_chat_ids":[0]}}`, false},
		{`{"wallet_access":{"allowed_owner_chat_ids":[1],"allowed_owner_chat_ids":[2]}}`, false},
		{`{"wallet_access":{"readonly_wallets":[{"wallet_id":"one","wallet_id":"two","address":"0x1"}]}}`, false},
		{`{"wallet_access":{"caller_supplied_owner":1001}}`, false},
	} {
		d := json.NewDecoder(strings.NewReader(tc.body))
		d.UseNumber()
		if e := validateConfigObject(d, false); (e == nil) != tc.valid {
			t.Fatalf("valid=%v error=%v", tc.valid, e)
		}
	}
}

func TestWalletSecurityReadonlyPrimaryCannotReachAdmission(t *testing.T) {
	f := securityFixture(t)
	api, err := NewWithOptions(f.api.store, Identity{PrincipalID: "watch-only", WalletID: "watch-bsc", WalletAddress: "0x0000000000000000000000000000000000000056"}, f.token, Policy{Version: 1, DefaultTTLSeconds: 30, MaxTTLSeconds: 300, MaxSlippageBPS: 500, MaxFeeGwei: "100", AllowedChain: "bsc"}, nil, Options{ManualIntake: true})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.New(bsc.Profile(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ext, err := NewLocalV2Extension(reg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = api.ConfigureWalletResolver(ext, f.dir); err != nil {
		t.Fatal(err)
	}
	if err = api.AttachLocalV2(ext); err != nil {
		t.Fatal(err)
	}
	w := securityRequest(api, f.token, "POST", "/v1/trades", `{"chain":"bsc","wallet_id":"watch-bsc","token":"0x55d398326f99059fF775485246999027B3197955","side":"buy","amount":"0.01","slippage_percent":"1"}`, "readonly-primary")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "READ_ONLY_WALLET") {
		t.Fatalf("readonly primary reached admission: %d %s", w.Code, w.Body.String())
	}
	var attempts int
	if err = api.store.db.QueryRow(`SELECT COUNT(*) FROM transaction_attempts`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatal("readonly request created signing artifacts")
	}
}

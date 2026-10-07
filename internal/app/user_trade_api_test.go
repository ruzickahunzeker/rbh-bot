package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruzickahunzeker/rbh-bot/internal/config"
	"github.com/ruzickahunzeker/rbh-bot/internal/storage"
)

type testRoutes struct{ handlers map[string]http.Handler }

func (r *testRoutes) Handle(pattern string, h http.Handler) { r.handlers[pattern] = h }

func TestUserTradeAPIStartupDefaultOffAndScopedRegistration(t *testing.T) {
	routes := &testRoutes{handlers: map[string]http.Handler{}}
	if err := configureUserTradeAPI(routes, nil, config.Config{}); err != nil || len(routes.handlers) != 0 {
		t.Fatal("default registered user API")
	}
	d, err := storage.Open(context.Background(), storage.TradeOwner, filepath.Join(t.TempDir(), "trade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, err := d.SQLDB(storage.TradeOwner)
	if err != nil {
		t.Fatal(err)
	}
	const address = "0x1000000000000000000000000000000000000001"
	if _, err = db.Exec(`INSERT INTO dry_run_wallets VALUES('wallet-01',4663,?,1)`, address); err != nil {
		t.Fatal(err)
	}
	const credential = "test-only-user-api-credential-32-characters"
	path := filepath.Join(t.TempDir(), "api.json")
	if err = os.WriteFile(path, []byte(`{"principal_id":"tg-bot","wallet_id":"wallet-01","wallet_address":"`+address+`","token":"`+credential+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = configureUserTradeAPI(routes, d, config.Config{TradeAPIConfigFile: path, InternalAuthSecret: credential}); err == nil || len(routes.handlers) != 0 {
		t.Fatal("reused IPC credential registered API")
	}
	if err = configureUserTradeAPI(routes, d, config.Config{TradeAPIConfigFile: path, InternalAuthSecret: "different-internal-secret"}); err != nil {
		t.Fatal(err)
	}
	if len(routes.handlers) != 1 || routes.handlers["/v1/"] == nil {
		t.Fatal("registered non-user routes")
	}
	r := httptest.NewRequest("GET", "/v1/capabilities", nil)
	w := httptest.NewRecorder()
	routes.handlers["/v1/"].ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("unauthenticated registration")
	}
	r.Header.Set("Authorization", "Bearer "+credential)
	w = httptest.NewRecorder()
	routes.handlers["/v1/"].ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"submission_enabled":false`) || !strings.Contains(w.Body.String(), `"live":false`) {
		t.Fatal("registration enabled sending")
	}
}

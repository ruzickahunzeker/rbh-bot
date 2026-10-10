package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"math/rand"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualrisk"
)

const manualSQLMax = "115792089237316195423570985008687907853269984665640564039457584007913129639935"

func manualMathDB(t *testing.T, path string) *Database {
	t.Helper()
	d, err := Open(context.Background(), TradeOwner, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestManualSchemaMathExactUint256AndAggregatePoison(t *testing.T) {
	d := manualMathDB(t, filepath.Join(t.TempDir(), "math.db"))
	for _, tc := range []struct {
		expr string
		args []any
		want string
	}{
		{"manual_u256_add(?,?)", []any{"0", manualSQLMax}, manualSQLMax},
		{"manual_u256_add(?,?)", []any{"9007199254740993", "1"}, "9007199254740994"},
		{"manual_u256_sum_json(?)", []any{`[]`}, "0"},
		{"manual_u256_sum_json(?)", []any{`["0","9007199254740993","1"]`}, "9007199254740994"},
		{"manual_u256_sum_json(?)", []any{`["` + manualSQLMax + `","0"]`}, manualSQLMax},
	} {
		var got string
		if err := d.db.QueryRow("SELECT "+tc.expr, tc.args...).Scan(&got); err != nil || got != tc.want {
			t.Fatal(tc.expr, got, tc.want, err)
		}
	}
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0", "0", 0}, {manualSQLMax, manualSQLMax, 0}, {"9007199254740993", "9007199254740992", 1}, {"9", "10", -1},
	} {
		var got int
		if err := d.db.QueryRow(`SELECT manual_u256_cmp(?,?)`, tc.a, tc.b).Scan(&got); err != nil || got != tc.want {
			t.Fatal("inexact comparison", got, tc.want, err)
		}
	}
	for _, raw := range []string{
		`null`, `{}`, `"1"`, `[1]`, `[1.0]`, `[true]`, `[null]`, `["1",null]`, `["1","01"]`, `["1","-1"]`, `["1","+1"]`, `["1","1e3"]`,
		`["` + manualSQLMax + `","1"]`, `["115792089237316195423570985008687907853269984665640564039457584007913129639936"]`, `["1"] garbage`,
	} {
		var result any
		if err := d.db.QueryRow(`SELECT manual_u256_sum_json(?)`, raw).Scan(&result); err == nil {
			t.Fatal("invalid/overflow aggregate returned partial result", raw, result)
		}
	}
	for _, value := range []any{nil, 1, 1.0, []byte("1"), "", "00", "01", "-0", "-1", "+1", "1.0", "1e3", " 1", "1 ", strings.Repeat("9", 79), "١"} {
		for _, expr := range []string{`manual_u256_add(?,'0')`, `manual_u256_cmp(?,'0')`, `manual_u256_add('0',?)`, `manual_u256_cmp('0',?)`} {
			var result any
			if err := d.db.QueryRow("SELECT "+expr, value).Scan(&result); err == nil {
				t.Fatal("type/coercion/canonical bypass", expr, value, result)
			}
		}
	}
	var result any
	if err := d.db.QueryRow(`SELECT manual_u256_add(?,'1')`, manualSQLMax).Scan(&result); err == nil {
		t.Fatal("overflow sum accepted")
	}
	if _, err := d.db.Exec(`CREATE TABLE math_amounts(amount TEXT NOT NULL); INSERT INTO math_amounts VALUES('9007199254740993'),('1')`); err != nil {
		t.Fatal(err)
	}
	var total string
	if err := d.db.QueryRow(`SELECT manual_u256_sum_json(json_group_array(amount)) FROM math_amounts`).Scan(&total); err != nil || total != "9007199254740994" {
		t.Fatal("real SQLite aggregate lost precision", total, err)
	}
}

func TestManualSchemaMathDifferentialAgainstUnitAwareGo(t *testing.T) {
	d := manualMathDB(t, filepath.Join(t.TempDir(), "differential.db"))
	rng := rand.New(rand.NewSource(20261010))
	u := manualrisk.Unit{ChainID: 4663, Asset: "native", Decimals: 18, Kind: manualrisk.TradeInput}
	for i := 0; i < 1000; i++ {
		bytesA, bytesB := make([]byte, 32), make([]byte, 32)
		_, _ = rng.Read(bytesA)
		_, _ = rng.Read(bytesB)
		a, b := new(big.Int).SetBytes(bytesA), new(big.Int).SetBytes(bytesB)
		left, right := a.String(), b.String()
		want, wantErr := manualrisk.Add(manualrisk.Quantity{Unit: u, Units: left}, manualrisk.Quantity{Unit: u, Units: right})
		var got string
		err := d.db.QueryRow(`SELECT manual_u256_add(?,?)`, left, right).Scan(&got)
		if (err == nil) != (wantErr == nil) || (err == nil && got != want.Units) {
			t.Fatal("Go/SQLite checked sum divergence", i, got, want, err, wantErr)
		}
		raw, _ := json.Marshal([]string{left, right})
		err = d.db.QueryRow(`SELECT manual_u256_sum_json(?)`, string(raw)).Scan(&got)
		if (err == nil) != (wantErr == nil) || (err == nil && got != want.Units) {
			t.Fatal("Go/SQLite aggregate divergence", i, got, want, err, wantErr)
		}
		var cmp int
		if err = d.db.QueryRow(`SELECT manual_u256_cmp(?,?)`, left, right).Scan(&cmp); err != nil || cmp != a.Cmp(b) {
			t.Fatal("comparison divergence", i, cmp, err)
		}
	}
}

func TestManualSchemaTimeFractionalBoundaryAndCanonicalFailure(t *testing.T) {
	d := manualMathDB(t, filepath.Join(t.TempDir(), "time.db"))
	for _, expiry := range []time.Time{
		time.Date(2026, 10, 10, 12, 0, 1, 0, time.UTC),
		time.Date(2026, 10, 10, 12, 0, 1, 250000000, time.UTC),
	} {
		for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
			now := expiry.Add(offset)
			var cmp, expired int
			if err := d.db.QueryRow(`SELECT manual_time_cmp(?,?),manual_time_cmp(?,?)>=0`, now.Format(time.RFC3339Nano), expiry.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), expiry.Format(time.RFC3339Nano)).Scan(&cmp, &expired); err != nil || cmp != now.Compare(expiry) || expired != boolInt(!now.Before(expiry)) {
				t.Fatal("expiry -1ns/equality/+1ns divergence", expiry, offset, cmp, expired, err)
			}
		}
	}
	for _, bad := range []any{nil, 0, []byte("2026-10-10T12:00:00Z"), "", "bad", "0001-01-01T00:00:00Z", "1969-12-31T23:59:59Z", "9999-01-01T00:00:00Z", "2026-10-10T12:00:00+00:00", "2026-10-10T12:00:00.000Z", "2026-10-10T12:00:00.2500Z"} {
		for _, expr := range []string{`manual_time_cmp(?,'2026-10-10T12:00:00Z')`, `manual_time_cmp('2026-10-10T12:00:00Z',?)`} {
			var got int
			if err := d.db.QueryRow("SELECT "+expr, bad).Scan(&got); err == nil {
				t.Fatal("noncanonical/unverifiable time accepted", bad, got)
			}
		}
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestManualSchemaMathRegistrationConcurrentReconnectAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reconnect.db")
	// This is registration/pool-replacement evidence, NOT a shared-ledger
	// contention test. Separate files avoid conflating WAL reopen lock failures
	// with missing deterministic functions. Atomic two-handle admission proof
	// remains a separate, not-yet-implemented requirement.
	d1, d2 := manualMathDB(t, path), manualMathDB(t, filepath.Join(t.TempDir(), "second-pool.db"))
	// Force pool replacement, not merely several calls on one registered conn.
	d1.db.SetMaxIdleConns(0)
	d2.db.SetMaxIdleConns(0)
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			db := []*sql.DB{d1.db, d2.db}[i%2]
			for n := 0; n < 8; n++ {
				var sum string
				var cmp, stamp int
				err := db.QueryRow(`SELECT manual_u256_add(?,'0'),manual_u256_cmp('10','9'),manual_time_cmp('2026-10-10T12:00:00.25Z','2026-10-10T12:00:00Z')`, manualSQLMax).Scan(&sum, &cmp, &stamp)
				if err != nil || sum != manualSQLMax || cmp != 1 || stamp != 1 {
					errs <- fmt.Errorf("replacement connection registration: %s/%d/%d: %v", sum, cmp, stamp, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if err := d1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d2.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := manualMathDB(t, path)
	var got string
	if err := reopened.db.QueryRow(`SELECT manual_u256_sum_json('["9007199254740993","1"]')`).Scan(&got); err != nil || got != "9007199254740994" {
		t.Fatal("actual close/reopen lost registration", got, err)
	}
}

// Probe tables below are disposable arithmetic fixtures, NOT another economic
// ledger or evidence of real reservation/usage coupling. Existing B1 guards
// remain intact; later atomic admission must supply its own complete proof.
func TestManualSchemaMathStatementAbortAndCallerRollback(t *testing.T) {
	d := manualMathDB(t, filepath.Join(t.TempDir(), "rollback.db"))
	if _, err := d.db.Exec(`CREATE TABLE math_probe(id INTEGER PRIMARY KEY,used TEXT NOT NULL CHECK(manual_u256_cmp(used,'10')<=0));
 CREATE TABLE math_command(amount TEXT NOT NULL);
 INSERT INTO math_probe VALUES(1,'9');
 CREATE TRIGGER math_effect AFTER INSERT ON math_command BEGIN
 UPDATE math_probe SET used=manual_u256_add(used,NEW.amount) WHERE id=1;
 END`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO math_command VALUES('1')`); err != nil {
		t.Fatal("cap equality rejected", err)
	}
	for _, value := range []any{"1", manualSQLMax, "01", nil} {
		if _, err := d.db.Exec(`INSERT INTO math_command VALUES(?)`, value); err == nil {
			t.Fatal("cap +1/overflow/invalid trigger accepted", value)
		}
		var used string
		var commands int
		if err := d.db.QueryRow(`SELECT used,(SELECT COUNT(*) FROM math_command) FROM math_probe`).Scan(&used, &commands); err != nil || used != "10" || commands != 1 {
			t.Fatal("failed command partially changed state", used, commands, err)
		}
	}
	conn, err := d.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if _, err = conn.ExecContext(context.Background(), `UPDATE math_probe SET used='8'`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(context.Background(), `INSERT INTO math_command VALUES('3')`); err == nil {
		t.Fatal("failing command accepted")
	}
	// ABORT is statement-local: never claim it rolled back earlier statements.
	var used string
	if err = conn.QueryRowContext(context.Background(), `SELECT used FROM math_probe`).Scan(&used); err != nil || used != "8" {
		t.Fatal("unexpected transaction semantics", used, err)
	}
	if _, err = conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRowContext(context.Background(), `SELECT used FROM math_probe`).Scan(&used); err != nil || used != "10" {
		t.Fatal("caller rollback did not restore prior transaction", used, err)
	}
}

func TestManualSchemaMathUnregisteredExternalClientFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "external.db")
	d := manualMathDB(t, path)
	if _, err := d.db.Exec(`CREATE TABLE math_external_probe(amount TEXT NOT NULL CHECK(manual_u256_cmp(amount,'10')<=0))`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	// Fresh executable imports only SQLite, NOT storage: no global registration
	// from this test process can accidentally satisfy its connection's schema.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./testdata/manual_math_unregistered", path)
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "unregistered write rejected; rows=0\n" {
		t.Fatal("external driver did not fail closed", string(out), err)
	}
	registered := manualMathDB(t, path)
	if _, err := registered.db.Exec(`INSERT INTO math_external_probe VALUES('10')`); err != nil {
		t.Fatal("registered client cannot use exact schema boundary", err)
	}
}

func TestManualSchemaMathPureCallbacksAndInvalidArity(t *testing.T) {
	for name, call := range map[string]func() error{
		"add":     func() error { _, err := manualSQLAdd(nil, nil); return err },
		"compare": func() error { _, err := manualSQLCompare(nil, nil); return err },
		"sum":     func() error { _, err := manualSQLSum(nil, nil); return err },
		"time":    func() error { _, err := manualSQLTimeCompare(nil, nil); return err },
		"shift":   func() error { _, err := manualSQLTimeShift(nil, nil); return err },
	} {
		if err := call(); err == nil {
			t.Fatal("invalid callback arity did not fail closed", name)
		}
	}
	f, err := parser.ParseFile(token.NewFileSet(), "manual_math.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"database/sql/driver": true, "encoding/json": true, "errors": true, "math/big": true, "time": true, "modernc.org/sqlite": true}
	for _, im := range f.Imports {
		if !allowed[strings.Trim(im.Path.Value, `"`)] {
			t.Fatal("unexpected schema arithmetic dependency", im.Path.Value)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			// big.Int.Sign is canonical integer validation, not a signer call.
			// The import whitelist excludes all signing/execution capabilities.
			for _, forbidden := range []string{"Now", "ExecContext", "QueryContext", "QueryRowContext", "BeginTx", "Commit", "Submit", "ReplayUnknown", "SendRawTransaction", "ReserveNonce", "Resolve", "Complete"} {
				if sel.Sel.Name == forbidden {
					t.Error("deterministic arithmetic acquired state/economic capability", forbidden)
				}
			}
		}
		return true
	})
}

func TestManualSchemaMathExactWindowShift(t *testing.T) {
	d := manualMathDB(t, filepath.Join(t.TempDir(), "window.db"))
	for _, stamp := range []string{"2026-10-10T12:00:00Z", "2026-10-10T12:00:00.000000001Z", "2026-10-10T12:00:00.999999999Z"} {
		base, _ := time.Parse(time.RFC3339Nano, stamp)
		for _, seconds := range []int64{-31536000, -3600, -1, 0, 1, 3600, 31536000} {
			var got string
			if err := d.db.QueryRow(`SELECT manual_time_shift_seconds(?,?)`, stamp, seconds).Scan(&got); err != nil || got != base.Add(time.Duration(seconds)*time.Second).Format(time.RFC3339Nano) {
				t.Fatal("inexact window", stamp, seconds, got, err)
			}
		}
	}
	for _, seconds := range []any{nil, "1", 1.0, -31536001, 31536001} {
		var got any
		if err := d.db.QueryRow(`SELECT manual_time_shift_seconds('2026-10-10T12:00:00Z',?)`, seconds).Scan(&got); err == nil {
			t.Fatal("invalid shift", seconds)
		}
	}
	for _, pair := range [][2]any{{"bad", 0}, {"2026-10-10T12:00:00+00:00", 0}, {"1970-01-01T00:00:00Z", -1}, {"9998-12-31T23:59:59Z", 1}} {
		var got any
		if err := d.db.QueryRow(`SELECT manual_time_shift_seconds(?,?)`, pair[0], pair[1]).Scan(&got); err == nil {
			t.Fatal("invalid time/bounds", pair)
		}
	}
}

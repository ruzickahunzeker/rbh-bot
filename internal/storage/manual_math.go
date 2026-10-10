package storage

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"modernc.org/sqlite"
)

// Pure schema primitives for the MANUAL atomic command. Registration happens
// before any SQLite connection opens, including replacement pool connections.
// No DB access, clocks, financial mutation or authorization lives here. Amounts
// must be canonical TEXT: coercing REAL/INTEGER would lose uint256 precision.
// A client without these functions fails its SQL statement, never falls back
// to SQLite SUM/CAST. These primitives do NOT verify source/policy SHA256.
func init() {
	for _, f := range []struct {
		name string
		n    int32
		fn   func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error)
	}{
		{"manual_u256_add", 2, manualSQLAdd},
		{"manual_u256_cmp", 2, manualSQLCompare},
		{"manual_u256_sum_json", 1, manualSQLSum},
		{"manual_time_cmp", 2, manualSQLTimeCompare},
		{"manual_time_shift_seconds", 2, manualSQLTimeShift},
	} {
		sqlite.MustRegisterDeterministicScalarFunction(f.name, f.n, f.fn)
	}
}

var errManualSQLValue = errors.New("invalid canonical MANUAL schema value")

func manualSQLUint(v driver.Value) (*big.Int, error) {
	s, ok := v.(string)
	if !ok || len(s) == 0 || len(s) > 78 {
		return nil, errManualSQLValue
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 || n.String() != s {
		return nil, errManualSQLValue
	}
	return n, nil
}

func manualSQLAdd(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) != 2 {
		return nil, errManualSQLValue
	}
	a, err := manualSQLUint(args[0])
	if err != nil {
		return nil, err
	}
	b, err := manualSQLUint(args[1])
	if err != nil {
		return nil, err
	}
	a.Add(a, b)
	if a.BitLen() > 256 {
		return nil, errManualSQLValue
	}
	return a.String(), nil
}

func manualSQLCompare(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) != 2 {
		return nil, errManualSQLValue
	}
	a, err := manualSQLUint(args[0])
	if err != nil {
		return nil, err
	}
	b, err := manualSQLUint(args[1])
	if err != nil {
		return nil, err
	}
	return int64(a.Cmp(b)), nil
}

// JSON_GROUP_ARRAY of TEXT values is the only accepted aggregate encoding.
// [] is the exact zero identity. NULL/non-array/non-string input fails closed;
// no element is silently skipped and no partial sum escapes after an error.
func manualSQLSum(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) != 1 {
		return nil, errManualSQLValue
	}
	raw, ok := args[0].(string)
	if !ok {
		return nil, errManualSQLValue
	}
	var items []json.RawMessage
	if json.Unmarshal([]byte(raw), &items) != nil || items == nil {
		return nil, errManualSQLValue
	}
	total := new(big.Int)
	for _, item := range items {
		var s string
		if json.Unmarshal(item, &s) != nil {
			return nil, errManualSQLValue
		}
		n, err := manualSQLUint(s)
		if err != nil {
			return nil, err
		}
		total.Add(total, n)
		if total.BitLen() > 256 {
			return nil, errManualSQLValue
		}
	}
	return total.String(), nil
}

func manualSQLTime(v driver.Value) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, errManualSQLValue
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.IsZero() || t.Year() < 1970 || t.Year() > 9998 || t.UTC().Format(time.RFC3339Nano) != s {
		return time.Time{}, errManualSQLValue
	}
	return t, nil
}

func manualSQLTimeCompare(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) != 2 {
		return nil, errManualSQLValue
	}
	a, err := manualSQLTime(args[0])
	if err != nil {
		return nil, err
	}
	b, err := manualSQLTime(args[1])
	if err != nil {
		return nil, err
	}
	return int64(a.Compare(b)), nil
}

// Exact bounded window arithmetic, preserving nanoseconds. No wall clock or
// SQLite floating-point/julianday conversion is involved.
func manualSQLTimeShift(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) != 2 {
		return nil, errManualSQLValue
	}
	t, err := manualSQLTime(args[0])
	seconds, ok := args[1].(int64)
	if err != nil || !ok || seconds < -31536000 || seconds > 31536000 {
		return nil, errManualSQLValue
	}
	shifted := t.Add(time.Duration(seconds) * time.Second)
	if shifted.Year() < 1970 || shifted.Year() > 9998 {
		return nil, errManualSQLValue
	}
	return shifted.UTC().Format(time.RFC3339Nano), nil
}

package storage

import (
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
	"modernc.org/sqlite"
)

// Pure, deterministic schema validation of canonical observation identity/time/
// uint64 nonce and SHA256. A plain SQL client without it fails closed. It has
// no RPC, clock, database, allocation or authorization capability.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("manual_nonce_observation_bytes_valid", 10,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			var values [5]string
			for i := range values {
				encoded, ok := args[2*i].(string)
				class, classOK := args[2*i+1].(string)
				if !ok || !classOK || class != "text" {
					return int64(0), nil
				}
				raw, err := hex.DecodeString(encoded)
				if err != nil {
					return int64(0), nil
				}
				values[i] = string(raw)
			}
			facts, err := manualnonce.DecodeFacts(values[3])
			if err != nil {
				return int64(0), nil
			}
			if _, err := manualnonce.Verify(values[0], values[1], values[2], facts, values[4]); err != nil {
				return int64(0), nil
			}
			return int64(1), nil
		})
	sqlite.MustRegisterDeterministicScalarFunction("manual_nonce_stored_value_valid", 2,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			encoded, ok := args[0].(string)
			class, classOK := args[1].(string)
			if !ok || !classOK || class != "text" {
				return int64(0), nil
			}
			raw, err := hex.DecodeString(encoded)
			if err != nil {
				return int64(0), nil
			}
			if _, err := manualnonce.Nonce(string(raw)); err != nil {
				return int64(0), nil
			}
			return int64(1), nil
		})
	sqlite.MustRegisterDeterministicScalarFunction("manual_nonce_identity_relation", 4,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			// 1 = exact TEXT identity, 2 = suspicious BLOB bytes matching that
			// identity. 2 is only a denial/relevance witness, never trusted input.
			// No stored value is converted, normalized, repaired or returned.
			// hex() preserves embedded NULs that a SQLite UDF TEXT conversion
			// may truncate. typeof() preserves the original storage class.
			encoded, rawOK := args[0].(string)
			class, classOK := args[1].(string)
			expected, targetOK := args[2].(string)
			fold, modeOK := args[3].(int64)
			value, valueErr := hex.DecodeString(encoded)
			target, targetErr := hex.DecodeString(expected)
			if !rawOK || !classOK || !targetOK || valueErr != nil || targetErr != nil || len(target) == 0 || !modeOK || (fold != 0 && fold != 1) {
				return nil, fmt.Errorf("invalid manual nonce identity comparator")
			}
			var relation int64
			switch class {
			case "text":
				relation = 1
			case "blob":
				relation = 2
			default:
				return int64(0), nil
			}
			if (fold == 0 && string(value) == string(target)) || (fold == 1 && strings.EqualFold(string(value), string(target))) {
				return relation, nil
			}
			return int64(0), nil
		})
	sqlite.MustRegisterDeterministicScalarFunction("manual_nonce_value_valid", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			// SQLite TEXT affinity does not convert BLOB values. Never cast a
			// malformed stored nonce into a seemingly valid nonce identity.
			raw, ok := args[0].(string)
			if !ok {
				return int64(0), nil
			}
			if _, err := manualnonce.Nonce(raw); err != nil {
				return int64(0), nil
			}
			return int64(1), nil
		})
	sqlite.MustRegisterDeterministicScalarFunction("manual_nonce_observation_valid", 5,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			var values [5]string
			for i := range values {
				v, ok := args[i].(string)
				if !ok {
					return int64(0), nil
				}
				values[i] = v
			}
			facts, err := manualnonce.DecodeFacts(values[3])
			if err != nil {
				return int64(0), nil
			}
			if _, err = manualnonce.Verify(values[0], values[1], values[2], facts, values[4]); err != nil {
				return int64(0), nil
			}
			return int64(1), nil
		})
}

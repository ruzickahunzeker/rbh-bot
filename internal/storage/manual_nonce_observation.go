package storage

import (
	"database/sql/driver"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
	"modernc.org/sqlite"
)

// Pure, deterministic schema validation of canonical observation identity/time/
// uint64 nonce and SHA256. A plain SQL client without it fails closed. It has
// no RPC, clock, database, allocation or authorization capability.
func init() {
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

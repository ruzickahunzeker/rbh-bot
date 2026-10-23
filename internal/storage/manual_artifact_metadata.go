package storage

import (
	"database/sql/driver"
	"encoding/hex"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"modernc.org/sqlite"
)

func init() {
	// Pure full-byte metadata validation, NOT decryption or signing permission.
	sqlite.MustRegisterDeterministicScalarFunction("manual_artifact_envelope_valid", 4, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		var decoded [2]string
		for i := 0; i < 2; i++ {
			raw, ok := args[2*i].(string)
			kind, typed := args[2*i+1].(string)
			if !ok || !typed || kind != "text" {
				return int64(0), nil
			}
			b, err := hex.DecodeString(raw)
			if err != nil {
				return int64(0), nil
			}
			decoded[i] = string(b)
		}
		f, err := manualartifact.Decode(decoded[0])
		if err != nil {
			return int64(0), nil
		}
		if _, _, err := manualartifact.DecodeEnvelopeMetadata(decoded[1], f); err != nil {
			return int64(0), nil
		}
		return int64(1), nil
	})
}

package storage

import (
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"errors"

	"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact"
	"github.com/ruzickahunzeker/rbh-bot/internal/manualnonce"
	"modernc.org/sqlite"
)

// Pure complete-byte helpers. No clock, DB callback, key, encryption or signer.
// hex() arguments avoid SQLite TEXT UDF conversion truncating retained NULs.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("manual_artifact_time_valid", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		encoded, ok := args[0].(string)
		kind, typed := args[1].(string)
		if !ok || !typed || kind != "text" {
			return int64(0), nil
		}
		raw, err := hex.DecodeString(encoded)
		if err != nil {
			return int64(0), nil
		}
		if _, err := manualnonce.CanonicalTime(string(raw)); err != nil {
			return int64(0), nil
		}
		return int64(1), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("manual_artifact_facts_hash", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		encoded, ok := args[0].(string)
		kind, typed := args[1].(string)
		if !ok || !typed || kind != "text" {
			return nil, errors.New("invalid MANUAL artifact facts")
		}
		raw, err := hex.DecodeString(encoded)
		if err != nil {
			return nil, errors.New("invalid MANUAL artifact facts")
		}
		f, err := manualartifact.Decode(string(raw))
		if err != nil {
			return nil, errors.New("invalid MANUAL artifact facts")
		}
		_, hash, err := f.Canonical()
		return hash, err
	})
	sqlite.MustRegisterDeterministicScalarFunction("manual_artifact_bytes_sha256", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		encoded, ok := args[0].(string)
		if !ok {
			return nil, errors.New("invalid MANUAL encoded bytes")
		}
		raw, err := hex.DecodeString(encoded)
		if err != nil {
			return nil, errors.New("invalid MANUAL encoded bytes")
		}
		digest := sha256.Sum256(raw)
		return hex.EncodeToString(digest[:]), nil
	})
}

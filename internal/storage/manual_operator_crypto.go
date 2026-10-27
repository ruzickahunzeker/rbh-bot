package storage

import (
	"crypto/ed25519"
	"database/sql/driver"
	"encoding/hex"

	"modernc.org/sqlite"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("manual_operator_signature_valid", 3,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if len(args) != 3 {
				return int64(0), nil
			}
			pubHex, ok1 := args[0].(string)
			payload, ok2 := args[1].(string)
			sigHex, ok3 := args[2].(string)
			if !ok1 || !ok2 || !ok3 {
				return int64(0), nil
			}
			pub, err := hex.DecodeString(pubHex)
			if err != nil || len(pub) != ed25519.PublicKeySize {
				return int64(0), nil
			}
			sig, err := hex.DecodeString(sigHex)
			if err != nil || len(sig) != ed25519.SignatureSize {
				return int64(0), nil
			}
			if ed25519.Verify(ed25519.PublicKey(pub), []byte(payload), sig) {
				return int64(1), nil
			}
			return int64(0), nil
		})
}

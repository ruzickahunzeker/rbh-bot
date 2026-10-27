package storage

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

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
	sqlite.MustRegisterDeterministicScalarFunction("manual_operator_bootstrap_valid", 6,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if len(args) != 6 {
				return int64(0), nil
			}
			secret := os.Getenv("RBH_MANUAL_OPERATOR_BOOTSTRAP_SECRET")
			if secret == "" {
				return int64(0), nil
			}
			parts := make([]string, len(args))
			for i, arg := range args {
				v, ok := arg.(string)
				if !ok {
					if n, numberOK := arg.(int64); numberOK {
						v = fmt.Sprintf("%d", n)
						ok = true
					}
				}
				if !ok || v == "" {
					return int64(0), nil
				}
				parts[i] = v
			}
			mac := hmac.New(sha256.New, []byte(secret))
			_, _ = mac.Write([]byte(strings.Join(parts[:5], "|")))
			want := hex.EncodeToString(mac.Sum(nil))
			if hmac.Equal([]byte(strings.ToLower(parts[5])), []byte(want)) {
				return int64(1), nil
			}
			return int64(0), nil
		})
	sqlite.MustRegisterDeterministicScalarFunction("manual_operator_gate_set_hash", 12,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if len(args) != 12 {
				return "", nil
			}
			parts := make([]string, len(args))
			for i, arg := range args {
				v, ok := arg.(string)
				if !ok || v == "" {
					return "", nil
				}
				parts[i] = v
			}
			digest := sha256.Sum256([]byte(strings.Join(parts, "|")))
			return hex.EncodeToString(digest[:]), nil
		})
}

package config

import (
	"fmt"
	"os"
	"strings"
)

// Resolve aliases without changing process environment or leaking secret values.
type chainEnv struct {
	rbh bool
	err error
}

func newChainEnv() *chainEnv {
	id := os.Getenv("RBH_CHAIN_ID")
	if id == "" {
		id = os.Getenv("EVM_CHAIN_ID")
	}
	r := &chainEnv{rbh: id == "" || id == "4663"}
	if a, b := os.Getenv("RBH_CHAIN_ID"), os.Getenv("EVM_CHAIN_ID"); a != "" && b != "" && a != b {
		r.err = fmt.Errorf("conflicting RBH_CHAIN_ID and EVM_CHAIN_ID")
	}
	return r
}

func (r *chainEnv) pref(primary, legacy, fallback string) string {
	keys := []string{primary}
	if r.rbh && strings.HasPrefix(primary, "EVM_") {
		keys = append([]string{"RBH_" + strings.TrimPrefix(primary, "EVM_")}, keys...)
	}
	if legacy != "" {
		keys = append(keys, legacy)
	}
	var value, used string
	for _, key := range keys {
		v := os.Getenv(key)
		if v == "" {
			continue
		}
		if used != "" && v != value && r.err == nil {
			r.err = fmt.Errorf("conflicting environment variables %s and %s", used, key)
		}
		if used == "" {
			value, used = v, key
		}
	}
	if used == "" {
		return fallback
	}
	return value
}

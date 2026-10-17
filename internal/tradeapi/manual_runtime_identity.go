package tradeapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime/debug"
)

// Disconnected measured-binary implementation. The manifest is operator-owned
// trusted immutable composition, not DB/request state. No production constructor
// reads manifests/credentials or wires this into startup in this slice.
type manualMeasuredRuntimeIdentity struct{ manifest manualRuntimeProcess }

func runtimeManifestDigest(p manualRuntimeProcess) string {
	return hash([]byte(encode(struct {
		Scope                             manualAdmissionScope
		Binary, Network, Holder, Provider string
		Epoch                             int64
		NetworkCheckpoint                 any
	}{p.scope, p.binaryHash, p.networkProfile, p.holder, p.provider, p.leaseEpoch, p.networkCheckpoint})))
}

func (m manualMeasuredRuntimeIdentity) identify(ctx context.Context) (manualRuntimeProcess, error) {
	var zero manualRuntimeProcess
	if ctx == nil || ctx.Err() != nil {
		return zero, ErrInvalid
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return zero, ErrInvalid
	}
	revision, modified := "", ""
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if !manualHex(revision, 40, 64) || modified != "false" {
		return zero, rejectManualInput("RUNTIME_BUILD_UNMEASURED_OR_DIRTY")
	}
	path, err := os.Executable()
	if err != nil {
		return zero, err
	}
	f, err := os.Open(path)
	if err != nil {
		return zero, err
	}
	defer f.Close()
	state, err := f.Stat()
	if err != nil || !state.Mode().IsRegular() || state.Size() > 512<<20 {
		return zero, ErrInvalid
	}
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(f, (512<<20)+1))
	if err != nil || n > 512<<20 || ctx.Err() != nil {
		return zero, ErrInvalid
	}
	p := m.manifest
	p.scope.BuildSHA = revision
	p.binaryHash = hex.EncodeToString(digest.Sum(nil))
	p.dirty = false
	if p.scope.BuildSHA != m.manifest.scope.BuildSHA || p.binaryHash != m.manifest.binaryHash || runtimeManifestDigest(p) != m.manifest.manifestHash {
		return zero, rejectManualInput("RUNTIME_BINARY_MANIFEST_MISMATCH")
	}
	return p, nil
}

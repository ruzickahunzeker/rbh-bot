package tradeapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"runtime"
)

// Hash a handle to the running Linux image, not the replaceable pathname from
// os.Executable. No pathname fallback is safe: unsupported/unavailable running
// image primitives must fail closed. This private helper loads no credentials.
func manualRunningImageHash(ctx context.Context) (string, error) {
	if ctx == nil || ctx.Err() != nil || runtime.GOOS != "linux" {
		return "", errors.New("RUNTIME_RUNNING_IMAGE_UNVERIFIABLE")
	}
	f, err := os.Open("/proc/self/exe")
	if err != nil {
		return "", err
	}
	defer f.Close()
	state, err := f.Stat()
	if err != nil || !state.Mode().IsRegular() || state.Size() <= 0 || state.Size() > 512<<20 {
		return "", errors.New("RUNTIME_RUNNING_IMAGE_UNVERIFIABLE")
	}
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(f, (512<<20)+1))
	if err != nil || n != state.Size() || ctx.Err() != nil {
		return "", errors.New("RUNTIME_RUNNING_IMAGE_UNVERIFIABLE")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

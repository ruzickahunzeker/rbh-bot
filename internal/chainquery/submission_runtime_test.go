package chainquery

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestHTTPM4B22ReadOnlyRuntimeRPCAnchorsAndDenials(t *testing.T) {
	for _, fault := range []string{"", "wrong_chain", "chain_drift", "curve_drift", "http_failure", "rpc_error", "wrong_hash", "wrong_block", "duplicate_header", "reorg", "bad_id", "duplicate_envelope", "timeout", "wrong_checkpoint", "wrong_baseline", "future_quote", "empty_baseline"} {
		t.Run("fault_"+fault, func(t *testing.T) {
			_, f := setup(t)
			profile := SubmissionRuntimeProfile{ID: "local-network", Environment: "test", NetworkCheckpoint: Block{Number: 100, Hash: f.header.Hash().Hex()}}
			quote := profile.NetworkCheckpoint
			baseline := crypto.Keccak256Hash([]byte{4, 5, 6}).Hex()
			switch fault {
			case "wrong_checkpoint":
				profile.NetworkCheckpoint.Hash = "0x" + strings.Repeat("1", 64)
			case "wrong_baseline":
				baseline = "0x" + strings.Repeat("2", 64)
			case "future_quote":
				quote.Number = 101
			case "empty_baseline":
				baseline = ""
			default:
				f.mu.Lock()
				f.fault = fault
				f.mu.Unlock()
			}
			r, err := NewSubmissionRuntimeObserver(f.server.URL, profile)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if fault == "timeout" {
				var shortCancel context.CancelFunc
				ctx, shortCancel = context.WithTimeout(ctx, time.Millisecond)
				defer shortCancel()
			}
			o, err := r.Observe(ctx, curveAddress, baseline, quote)
			if fault == "" {
				if err != nil || o.ChainID != 4663 || o.Anchor != quote || o.Quote != quote || o.NetworkCheckpoint != profile.NetworkCheckpoint || o.EvidenceHash != o.Digest() {
					t.Fatal(o, err)
				}
			} else if err == nil || o != (SubmissionRuntimeObservation{}) {
				t.Fatal("unverifiable RPC accepted", fault, o, err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for method := range f.methods {
				if method != "eth_chainId" && method != "eth_getBlockByNumber" && method != "eth_getCode" {
					t.Fatal("runtime RPC capability widened", method)
				}
			}
		})
	}
}

func TestHTTPM4B22RuntimeRPCConstructorNoNetworkOrUserFallback(t *testing.T) {
	for _, p := range []SubmissionRuntimeProfile{{}, {ID: "x", Environment: "x", NetworkCheckpoint: Block{Number: 1, Hash: "0x" + strings.Repeat("0", 64)}}} {
		if _, err := NewSubmissionRuntimeObserver("http://127.0.0.1:1", p); err == nil {
			t.Fatal("missing profile accepted")
		}
	}
	if _, err := NewSubmissionRuntimeObserver("http://user:secret@127.0.0.1", SubmissionRuntimeProfile{ID: "x", Environment: "x", NetworkCheckpoint: Block{Number: 1, Hash: "0x" + strings.Repeat("1", 64)}}); err == nil {
		t.Fatal("credential URL accepted")
	}
}

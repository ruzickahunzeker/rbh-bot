package chainregistry

import (
	"errors"
	"sync"
	"testing"
)

func TestRegistryRuntimeIsNotMultiChainEnablement(t *testing.T) {
	for _, tc := range []struct {
		id      uint64
		key     string
		runtime RuntimeStatus
		wantErr error
	}{
		{RobinhoodID, "robinhood", LegacyRobinhood, nil},
		{BSCID, "bsc", NotImplemented, ErrRuntimeUnavailable},
		{BaseID, "base", NotImplemented, ErrRuntimeUnavailable},
	} {
		definition, err := Lookup(tc.id)
		if err != nil || definition.ID != tc.id || definition.Key != tc.key || definition.Runtime != tc.runtime {
			t.Fatalf("lookup %d: %+v %v", tc.id, definition, err)
		}
		_, err = RequireRuntime(tc.id)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("runtime %d: %v", tc.id, err)
		}
	}
	for _, id := range []uint64{0, 1, 97, 11155111, ^uint64(0)} {
		if _, err := Lookup(id); !errors.Is(err, ErrUnknownChain) {
			t.Fatalf("unknown chain %d accepted: %v", id, err)
		}
		if _, err := RequireRuntime(id); !errors.Is(err, ErrUnknownChain) {
			t.Fatalf("unknown runtime %d accepted: %v", id, err)
		}
	}
}

func TestRegistryReturnedValuesCannotInstallOrEnableRuntime(t *testing.T) {
	list := List()
	if len(list) != 3 {
		t.Fatal("unexpected chain count")
	}
	list[1].Runtime = LegacyRobinhood
	list[0].ID = BSCID
	list[2].Key = "solana"
	definition, _ := Lookup(BSCID)
	definition.Runtime = LegacyRobinhood
	if _, err := RequireRuntime(definition.ID); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatal("caller mutation enabled BSC")
	}
	again := List()
	if again[0].ID != RobinhoodID || again[1].Runtime != NotImplemented || again[2].Key != "base" {
		t.Fatalf("registry mutated: %+v", again)
	}
}

func TestRegistryConcurrentLookupsRemainIsolated(t *testing.T) {
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			for range 100 {
				list := List()
				list[1].Runtime = LegacyRobinhood
				if _, err := RequireRuntime(BSCID); !errors.Is(err, ErrRuntimeUnavailable) {
					t.Errorf("concurrent BSC runtime enabled: %v", err)
				}
				if definition, err := RequireRuntime(RobinhoodID); err != nil || definition.ID != RobinhoodID {
					t.Errorf("legacy identity changed: %+v %v", definition, err)
				}
			}
		})
	}
	wg.Wait()
}

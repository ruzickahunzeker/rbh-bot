package main

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestLockedSDKAddressBooksMatch(t *testing.T) {
	if !addressBooksMatch() {
		t.Fatal("locked parser and trade SDK address books disagree")
	}
}

func TestPonsBaselineContractsAreUniqueAndNonZero(t *testing.T) {
	seen := make(map[common.Address]string)
	for _, contract := range ponsContracts() {
		if contract.Address == (common.Address{}) {
			t.Fatalf("%s/%s has zero address", contract.Protocol, contract.Role)
		}
		if previous, exists := seen[contract.Address]; exists {
			t.Fatalf("%s/%s duplicates %s at %s", contract.Protocol, contract.Role, previous, contract.Address)
		}
		seen[contract.Address] = contract.Protocol + "/" + contract.Role
	}
}

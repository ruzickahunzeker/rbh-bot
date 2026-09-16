package main

import (
	"encoding/hex"
	"testing"
)

func TestMinimalProxyImplementation(t *testing.T) {
	code, err := hex.DecodeString("363d3d373d3d3d363d7312345678901234567890123456789012345678905af43d82803e903d91602b57fd5bf3")
	if err != nil {
		t.Fatal(err)
	}
	implementation, ok := minimalProxyImplementation(code)
	if !ok || implementation.Hex() != "0x1234567890123456789012345678901234567890" {
		t.Fatalf("unexpected implementation: %s, %v", implementation, ok)
	}
}

func TestOpcodeCountSkipsPushData(t *testing.T) {
	code := []byte{0x60, 0xf4, 0x61, 0xf4, 0xf4, 0xf4}
	if got := opcodeCount(code, 0xf4); got != 1 {
		t.Fatalf("got %d delegatecalls", got)
	}
}

func TestExecutableRuntimeStripsSolidityMetadata(t *testing.T) {
	code := []byte{0x60, 0x00, 0xa1, 0xf4, 0x00, 0x02}
	got := executableRuntime(code)
	if len(got) != 2 || opcodeCount(got, 0xf4) != 0 {
		t.Fatalf("metadata was treated as executable: %x", got)
	}
}

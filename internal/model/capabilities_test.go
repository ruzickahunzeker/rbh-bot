package model

import "testing"

func TestLPExecutionRemainsDisabled(t *testing.T) {
	capabilities := ReservedLPCapabilities()
	if capabilities.Execute != "not_implemented" {
		t.Fatalf("LP execution unexpectedly enabled: %q", capabilities.Execute)
	}
}

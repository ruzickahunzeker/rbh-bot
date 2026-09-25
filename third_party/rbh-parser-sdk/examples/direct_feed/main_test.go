package main

import (
	"context"
	"testing"
)

func TestParseInitialSequence(t *testing.T) {
	if value, err := parseInitialSequence(context.Background(), ""); err != nil || value != nil {
		t.Fatalf("empty sequence: value=%v err=%v", value, err)
	}
	if value, err := parseInitialSequence(context.Background(), "latest"); err != nil || value != nil {
		t.Fatalf("latest sequence: value=%v err=%v", value, err)
	}
	value, err := parseInitialSequence(context.Background(), " 123 ")
	if err != nil || value == nil || *value != 123 {
		t.Fatalf("numeric sequence: value=%v err=%v", value, err)
	}
	if _, err := parseInitialSequence(context.Background(), "invalid"); err == nil {
		t.Fatal("invalid sequence accepted")
	}
}

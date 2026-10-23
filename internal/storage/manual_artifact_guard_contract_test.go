package storage

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestManualArtifactPhasePreservesOldGuardDenyDomains(t *testing.T) {
	names, err := fs.Glob(migrationFiles, "migrations/trade/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	pattern := regexp.MustCompile(`(?s)CREATE TRIGGER (\w+).*?\bEND;`)
	old := map[string]string{}
	current := map[string]string{}
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := migrationFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(raw), -1) {
			if version <= 35 {
				old[match[1]] = match[0]
			} else if version == 36 {
				current[match[1]] = match[0]
			}
		}
	}
	for name, before := range old {
		after, replaced := current[name]
		if !replaced {
			continue
		}
		when, begin := strings.Index(before, "\nWHEN "), strings.Index(before, "\nBEGIN ")
		if when < 0 || begin <= when {
			t.Fatal("unexpected old guard", name)
		}
		prefix := before[:when] + "\nWHEN (" + before[when+6:begin] + ")\n AND NOT EXISTS(SELECT 1 FROM manual_artifact_"
		if !strings.HasPrefix(after, prefix) || !strings.HasSuffix(after, before[begin:]) || !strings.Contains(after, "n.rowid=OLD.rowid AND NEW.rowid=OLD.rowid") {
			t.Fatal("old deny predicate/body or physical row protection changed", name)
		}
	}
	want := []string{"manual_execution_no_attempt_update", "manual_nonce_attempt_update_victim", "manual_nonce_alias_update", "manual_execution_step_no_update", "manual_execution_step_no_adoption", "manual_execution_lane_update", "manual_execution_lane_no_update", "manual_binding_lane_reference_update", "manual_binding_lane_retained_update"}
	count := 0
	for name := range current {
		if _, ok := old[name]; ok {
			count++
		}
	}
	if count != len(want) {
		t.Fatal("unexpected guard replacement surface", count)
	}
	for _, name := range want {
		if _, ok := current[name]; !ok {
			t.Fatal("missing exact replacement", name)
		}
	}
}

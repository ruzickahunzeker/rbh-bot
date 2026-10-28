package tradeapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Inspect product source only, across every cmd/internal entry point. This is
// a source-graph guard, not a claim that transitive legacy SDK dependencies or
// every test signer/send counter are zero.
func TestHTTPM3B4ProductionGraphRemainsDisconnected(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	owners := map[string]map[string]bool{
		"manualLocalSigningWriter": {
			"internal/tradeapi/manual_local_signing.go":    true,
			"internal/tradeapi/manual_trust_composition.go": true,
		},
		"manualNonceClaimWriter": {"internal/tradeapi/manual_nonce_claim.go": true},
	}
	for _, directory := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				name, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				if strings.HasSuffix(name, "/internal/manualsign") && rel != "internal/tradeapi/manual_local_signing.go" && rel != "internal/tradeapi/manual_trust_composition.go" {
					t.Errorf("local signer composed outside its private owner: %s", rel)
				}
				if (strings.HasPrefix(rel, "cmd/") || strings.HasPrefix(rel, "internal/app/")) &&
					(strings.HasSuffix(name, "/internal/manualartifact") || strings.HasSuffix(name, "/internal/manualnonce") || strings.HasSuffix(name, "/internal/manualsign")) {
					t.Errorf("M3 runtime composition in production startup: %s", rel)
				}
				if strings.HasPrefix(rel, "internal/manualartifact/") || strings.HasPrefix(rel, "internal/manualsign/") || rel == "internal/tradeapi/manual_local_signing.go" || rel == "internal/tradeapi/manual_nonce_claim.go" {
					if name == "net" || strings.HasPrefix(name, "net/") || strings.HasSuffix(name, "/ethclient") || strings.HasSuffix(name, "/rpc") || strings.HasSuffix(name, "/internal/trade") || strings.HasSuffix(name, "/internal/config") || strings.HasSuffix(name, "/internal/app") {
						t.Errorf("production dependency injected into controlled-local graph: %s imports %s", rel, name)
					}
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				id, ok := node.(*ast.Ident)
				if !ok {
					return true
				}
				if allowed, private := owners[id.Name]; private && !allowed[rel] {
					t.Errorf("private economic writer referenced outside owner: %s in %s", id.Name, rel)
				}
				if strings.HasPrefix(rel, "internal/manualartifact/") || strings.HasPrefix(rel, "internal/manualsign/") || rel == "internal/tradeapi/manual_local_signing.go" || rel == "internal/tradeapi/manual_nonce_claim.go" {
					for _, denied := range []string{"SubmissionService", "RawBroadcaster", "SendRawTransaction", "ReplayUnknown", "RPCBackend", "ReserveExecution", "IssueCanarySendPermit", "ConsumeCanaryPermitAfterGate"} {
						if id.Name == denied {
							t.Errorf("prohibited capability in %s: %s", rel, denied)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

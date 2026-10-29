package tradeapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Inspect product source, not test fixtures. This guards the current private
// local composition; it is not a general call-graph proof for arbitrary Go or a
// claim that legacy packages elsewhere lack signer/transport implementations.
func TestManualSubmissionPrivateLocalDependencyBoundary(t *testing.T) {
	const owner = "internal/tradeapi/manual_submission_executor.go"
	root := filepath.Clean(filepath.Join("..", ".."))
	if _, err := os.Stat(filepath.Join(root, owner)); os.IsNotExist(err) {
		t.Skip("executor has not yet been integrated into this isolated adapter worktree")
	} else if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, owner), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Freeze the small local dependency surface. A new dependency requires an
	// explicit boundary review rather than silently inheriting broad capabilities.
	allowedImports := map[string]bool{
		"context": true, "database/sql": true, "errors": true, "time": true,
		"github.com/ruzickahunzeker/rbh-bot/internal/manualartifact": true,
		"github.com/ruzickahunzeker/rbh-bot/internal/trade":          true,
	}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || !allowedImports[path] {
			t.Errorf("unexpected executor dependency %s", imp.Path.Value)
		}
	}
	privateSymbols := map[string]bool{"manualSubmissionExecutor": true, "manualFirstTransport": true, "manualTransportResult": true}
	var transport *ast.InterfaceType
	var executor *ast.StructType
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if declaration.Name.IsExported() {
				t.Errorf("executor exposes public function/method %s", declaration.Name.Name)
			}
			if declaration.Recv == nil {
				privateSymbols[declaration.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					privateSymbols[spec.Name.Name] = true
					if spec.Name.IsExported() {
						t.Errorf("executor exposes public type %s", spec.Name.Name)
					}
					switch spec.Name.Name {
					case "manualFirstTransport":
						transport, _ = spec.Type.(*ast.InterfaceType)
					case "manualSubmissionExecutor":
						executor, _ = spec.Type.(*ast.StructType)
					}
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						privateSymbols[name.Name] = true
						if name.IsExported() {
							t.Errorf("executor exposes public value %s", name.Name)
						}
					}
				}
			}
		}
	}
	if transport == nil || transport.Methods == nil || len(transport.Methods.List) != 1 {
		t.Fatal("local transport must declare exactly one private method")
	}
	method := transport.Methods.List[0]
	if len(method.Names) != 1 || method.Names[0].Name != "submitExact" || method.Names[0].IsExported() {
		t.Fatal("transport is no longer a private exact-bytes capability")
	}
	signature, ok := method.Type.(*ast.FuncType)
	if !ok || signature.Params == nil || len(signature.Params.List) != 2 || signature.Results == nil || len(signature.Results.List) != 2 {
		t.Fatal("unexpected private transport signature")
	}
	contextType, contextOK := signature.Params.List[0].Type.(*ast.SelectorExpr)
	contextPackage, contextPackageOK := func() (*ast.Ident, bool) {
		if !contextOK {
			return nil, false
		}
		v, ok := contextType.X.(*ast.Ident)
		return v, ok
	}()
	bytesType, bytesOK := signature.Params.List[1].Type.(*ast.ArrayType)
	if !contextOK || !contextPackageOK || contextPackage.Name != "context" || contextType.Sel.Name != "Context" || !bytesOK || bytesType.Len != nil || !submissionDependencyIdent(bytesType.Elt, "byte") ||
		!submissionDependencyIdent(signature.Results.List[0].Type, "manualTransportResult") || !submissionDependencyIdent(signature.Results.List[1].Type, "error") {
		t.Fatal("transport must accept context and exact raw bytes, returning only local result/error")
	}
	if executor == nil {
		t.Fatal("private executor struct missing")
	}
	allowedFields := map[string]bool{"verifier": true, "store": true, "transport": true, "timeout": true, "checkpoint": true}
	transportFields := 0
	for _, field := range executor.Fields.List {
		if len(field.Names) != 1 || !allowedFields[field.Names[0].Name] {
			t.Fatal("executor gained an unreviewed or embedded capability")
		}
		if field.Names[0].Name == "transport" {
			transportFields++
			if !submissionDependencyIdent(field.Type, "manualFirstTransport") {
				t.Fatal("executor transport is not the private local interface")
			}
		}
	}
	if transportFields != 1 {
		t.Fatal("executor must have exactly one private transport")
	}
	denied := map[string]bool{
		"ExecutionKernel": true, "SubmissionService": true, "RawBroadcaster": true, "SendRawTransaction": true,
		"Signer": true, "LocalSigner": true, "NonceAllocator": true, "NonceManager": true,
		"ReplayUnknown": true, "PermitUnknownReplay": true, "SignTransaction": true, "SignExact": true,
		"manualLocalSigningWriter": true, "manualNonceClaimWriter": true, "ReserveExecution": true,
		"ReserveNonce": true, "AllocateNonce": true, "NewLocalSigner": true, "RPCBackend": true,
		"NewSubmissionService": true, "NewControlledSubmissionWorker": true, "Submit": true,
	}
	invocations := 0
	ast.Inspect(file, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok && denied[identifier.Name] {
			t.Errorf("executor gained forbidden capability %s", identifier.Name)
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "submitExact" {
				invocations++
			}
		}
		if _, ok := node.(*ast.GoStmt); ok {
			t.Error("local executor must not start asynchronous work")
		}
		return true
	})
	if invocations != 1 {
		t.Fatalf("expected one local transport invocation site, got %d", invocations)
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
			if err != nil || filepath.ToSlash(rel) == owner {
				return err
			}
			other, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(other, func(node ast.Node) bool {
				if identifier, ok := node.(*ast.Ident); ok && privateSymbols[identifier.Name] {
					t.Errorf("private submission composition referenced by product file %s: %s", rel, identifier.Name)
				}
				if declaration, ok := node.(*ast.FuncDecl); ok && declaration.Recv != nil && declaration.Name.Name == "submitExact" {
					t.Errorf("local transport implemented in product source %s", rel)
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

func submissionDependencyIdent(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == name
}

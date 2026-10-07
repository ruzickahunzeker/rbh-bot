package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestEVMEntryRejectsIgnoredChainOrLiveArguments(t *testing.T) {
	for _, args := range [][]string{{"--chain=56"}, {"--live"}, {"--service=other"}, {"56"}, {"--chain", "8453"}} {
		if err := run(args); err == nil {
			t.Fatal("unexpected argument reached legacy startup")
		}
	}
}

func TestEVMEntryAliasesOnlyExistingTradeService(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if ok && pkg.Name == "app" && selector.Sel.Name == "Run" {
			starts++
			if len(call.Args) != 1 {
				t.Fatal("unexpected worker startup arguments")
			}
			service, ok := call.Args[0].(*ast.SelectorExpr)
			if !ok || service.Sel.Name != "TradeService" {
				t.Fatal("entry selected another service")
			}
			servicePackage, ok := service.X.(*ast.Ident)
			if !ok || servicePackage.Name != "config" {
				t.Fatal("entry selected caller-controlled service")
			}
		}
		return true
	})
	if starts != 1 {
		t.Fatal("entry must reuse exactly the existing startup")
	}
}

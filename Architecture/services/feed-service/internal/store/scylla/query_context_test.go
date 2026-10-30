package scylla

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Audit L7: every Scylla query in this package runs under its caller's
// context, so a cancelled request or a shutdown interrupts it instead of
// waiting out gocql's per-query timeout. A query executed straight off
// session.Query(...) — .Exec(), .Iter() or .Scan() with no .WithContext in
// between — ignores the context.
func TestEveryQueryCarriesTheContext(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if len(name) > 8 && name[len(name)-8:] == "_test.go" {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "Exec", "Iter", "Scan", "MapScan":
				default:
					return true
				}
				inner, ok := sel.X.(*ast.CallExpr)
				if !ok {
					return true
				}
				innerSel, ok := inner.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch innerSel.Sel.Name {
				case "Query":
					t.Errorf("%s: session.Query(...).%s() runs without the caller's context; add .WithContext(ctx)",
						fset.Position(call.Pos()), sel.Sel.Name)
				case "WithContext":
					checked++
				}
				return true
			})
		}
	}
	if checked < 20 {
		t.Fatalf("only %d context-carrying queries found; is this test still looking at the store?", checked)
	}
}

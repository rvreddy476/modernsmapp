package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

/*
	A private life entry must never become a recommendation reason.

	Proven on dev with a consented fixture on 2026-09-26: a candidate whose
	school entry carried visibility='private' was returned to a stranger
	with SAME_SCHOOL and "Studied at Osmania University". Both candidate-side
	readers now filter on visibility = 'public'; these guards pin that the
	filter is in the SQL, not in a comment, and that the viewer's own reader
	(which may see everything) is not the one the candidate side uses.

	Parsed with mode 0 — no comments — so prose can never satisfy a guard.
*/

func queryLiterals(t *testing.T, fnName string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "postgres.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != fnName || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				out.WriteString(lit.Value)
				out.WriteString("\n")
			}
			return true
		})
		return out.String()
	}
	t.Fatalf("postgres.go: no function named %s — has it been renamed? This guard would otherwise pass while checking nothing", fnName)
	return ""
}

func TestCandidateDiscoveryReadsOnlyPublicLifeEntries(t *testing.T) {
	sql := queryLiterals(t, "GetUsersByLifeEntry")
	if !strings.Contains(sql, "visibility = 'public'") {
		t.Fatal("GetUsersByLifeEntry does not filter visibility = 'public': a user who marked " +
			"a school private would be found — and therefore disclosed — through it")
	}
}

func TestCandidateLifeEntriesAreFilteredAndTheViewerReaderIsNot(t *testing.T) {
	// The shared reader takes publicOnly; the literal must be appended
	// under that switch, so the viewer path (publicOnly=false) sees all.
	shared := queryLiterals(t, "lifeEntries")
	if !strings.Contains(shared, "visibility = 'public'") {
		t.Fatal("lifeEntries never adds the visibility filter; GetPublicLifeEntries would return private entries")
	}
	// Both wrappers must exist and route through the shared reader with the
	// right flag. Parsed, not grepped, so a renamed wrapper fails loudly.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "postgres.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	flags := map[string]string{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Name.Name != "GetUserLifeEntries" && fn.Name.Name != "GetPublicLifeEntries" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "lifeEntries" || len(call.Args) != 3 {
				return true
			}
			if id, ok := call.Args[2].(*ast.Ident); ok {
				flags[fn.Name.Name] = id.Name
			}
			return true
		})
	}
	if flags["GetPublicLifeEntries"] != "true" {
		t.Fatalf("GetPublicLifeEntries must call lifeEntries(…, true); got %q", flags["GetPublicLifeEntries"])
	}
	if flags["GetUserLifeEntries"] != "false" {
		t.Fatalf("GetUserLifeEntries must call lifeEntries(…, false) — the viewer's own private entries still count on their own side; got %q", flags["GetUserLifeEntries"])
	}
}

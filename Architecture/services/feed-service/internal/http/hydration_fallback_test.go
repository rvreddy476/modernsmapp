package http

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Re-review P0-4 — no feed route may answer 200 with raw identifiers when
// hydration fails.
//
// Four active routes had this fallback. Three were fixed and `/v1/feed/watch`
// was missed, while the handover claimed all four were safe. The miss was
// not a subtle bug — it was one of four near-identical blocks, and reading
// them one at a time is exactly how the fourth survives.
//
// So this asserts the property over EVERY handler that hydrates, including
// routes added later. `h.svc` is a concrete *service.Service, so a
// behavioural test would need a live Scylla timeline; this catches the
// omission class without that dependency. The live-dependency behavioural
// test belongs with the feed integration suite.

func TestNoHandlerReturnsRawItemsOnHydrationFailure(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "handler.go", nil, 0)
	if err != nil {
		t.Fatalf("parse handler.go: %v", err)
	}

	var checked int
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if !callsHydratePosts(fn) {
			continue
		}
		checked++

		// The invariant is about the SHAPE of the response, not the exact
		// status. GetHomeFeed answers 502 HYDRATION_FAILED and the four
		// media routes answer 503 FEED_UNAVAILABLE; both are correct
		// because both are errors. What must never happen is a 200 with
		// unhydrated items in it.
		if !errorsInHydrationErrorBranch(fn) {
			t.Errorf("%s hydrates but its failure branch never writes an error "+
				"response; a hydration failure must not be answered as success",
				fn.Name.Name)
		}
		if returnsJSONInHydrationErrorBranch(fn) {
			t.Errorf("%s returns a JSON body on the hydration-failure path — raw "+
				"FeedItems carry post_id and author_id with no hydration-time checks, "+
				"so this leaks identifiers for content the viewer may not be allowed "+
				"to see (including authors who blocked them)", fn.Name.Name)
		}
	}

	// Floor guard: if a rename makes callsHydratePosts match nothing, this
	// test would pass forever while checking zero handlers.
	if checked < 4 {
		t.Fatalf("only %d hydrating handlers were checked, expected at least 4 "+
			"(reels, flicks, videos, watch) — has the call or naming changed?", checked)
	}
}

func callsHydratePosts(fn *ast.FuncDecl) bool { return mentions(fn, "HydratePosts") }

func mentions(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
			found = true
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

// errorsInHydrationErrorBranch reports whether the hydration-failure
// branch writes an error response.
func errorsInHydrationErrorBranch(fn *ast.FuncDecl) bool {
	return hydrationErrorBranchCalls(fn, "api", "ErrorWithContext")
}

// returnsJSONInHydrationErrorBranch looks for the `if err != nil` block
// that directly follows the HydratePosts assignment and reports whether it
// writes a JSON success body.
func returnsJSONInHydrationErrorBranch(fn *ast.FuncDecl) bool {
	return hydrationErrorBranchCalls(fn, "api", "JSON")
}

// hydrationErrorBranchCalls reports whether the `if err != nil` block
// directly following the HydratePosts assignment calls pkg.name.
func hydrationErrorBranchCalls(fn *ast.FuncDecl, pkgName, funcName string) bool {
	stmts := fn.Body.List
	for i, stmt := range stmts {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok || !assignCallsHydrate(assign) {
			continue
		}
		if i+1 >= len(stmts) {
			return false
		}
		ifStmt, ok := stmts[i+1].(*ast.IfStmt)
		if !ok {
			return false
		}
		found := false
		ast.Inspect(ifStmt.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name == funcName {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == pkgName {
					found = true
					return false
				}
			}
			return true
		})
		return found
	}
	return false
}

func assignCallsHydrate(assign *ast.AssignStmt) bool {
	for _, rhs := range assign.Rhs {
		call, ok := rhs.(*ast.CallExpr)
		if !ok {
			continue
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "HydratePosts" {
			return true
		}
	}
	return false
}

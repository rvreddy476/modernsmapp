package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Module 2 P0-6 — every feed surface must resolve block/mute state.
//
// The defect Codex found was not that the filter was wrong; it was that
// four surfaces never called it. GetReelFeed, GetFlickFeed,
// GetLongVideoFeed and GetVideoFeed each read the timeline and returned
// it, so a blocked author's posts reached the person who blocked them
// through any of those tabs while the main home feed looked correct.
//
// A behavioural test would need a live Scylla timeline, and would only
// cover the four surfaces that exist today. The actual risk is the fifth
// surface someone adds next month. So this asserts the property
// structurally: any exported Service method that returns feed items must
// resolve the block scope somewhere in its body.
//
// If this fails for a new method, the fix is to call resolveBlockedSet
// and applyBlockFilter — not to add the method to an exemption list.

func TestEveryFeedSurfaceResolvesBlockScope(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "feed.go", nil, 0)
	if err != nil {
		t.Fatalf("parse feed.go: %v", err)
	}

	// Surfaces that legitimately do not return other people's content.
	exempt := map[string]string{}
	methods := make(map[string]*ast.FuncDecl)
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil && fn.Body != nil {
			methods[fn.Name.Name] = fn
		}
	}

	var checked int
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Body == nil {
			continue
		}
		name := fn.Name.Name
		if !strings.HasPrefix(name, "Get") || !strings.HasSuffix(name, "Feed") {
			continue
		}
		if reason, ok := exempt[name]; ok {
			t.Logf("skipping %s: %s", name, reason)
			continue
		}

		checked++
		if !callsBlockResolution(fn, methods, map[string]bool{}) {
			t.Errorf("%s returns feed content but never resolves block/mute state; "+
				"a blocked author's posts would reach the viewer who blocked them", name)
		}
	}

	// Guard against the check silently matching nothing (a rename would
	// otherwise turn this into a permanently green no-op).
	if checked < 4 {
		t.Fatalf("only %d feed surfaces were checked; expected at least 4 "+
			"(home, reels, flicks, long video) — has the naming changed?", checked)
	}
}

// callsBlockResolution reports whether the function body mentions the
// block-scope resolution helper or the direct graph lookup.
func callsBlockResolution(fn *ast.FuncDecl, methods map[string]*ast.FuncDecl, seen map[string]bool) bool {
	if fn == nil || seen[fn.Name.Name] {
		return false
	}
	seen[fn.Name.Name] = true
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "resolveBlockedSet", "getBlockedAndMuted":
			found = true
			return false
		}
		// A public compatibility method may delegate to a paginated method.
		// Follow that service-method call so adding a wrapper cannot either
		// create a false alarm or hide a real missing safety gate.
		if delegated := methods[sel.Sel.Name]; delegated != nil && callsBlockResolution(delegated, methods, seen) {
			found = true
			return false
		}
		return true
	})
	return found
}

package search

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Re-review v2 closure criterion 1 / re-review v3 §4 — every production
// writer of an ELIGIBLE post document must go through the author-fence
// handshake, and the guard proving it must itself be sound.
//
// The rule:
//
//	eligible write → IndexPostUnlessAuthorErased (check, write, recheck)
//	removal        → ApplyPostProjection         (always safe; no fence
//	                                              is needed to REMOVE)
//
// A direct ApplyPostProjection call is allowed only when its PostProjection
// argument sets a top-level `Removed: true` with the literal identifier
// `true`. A computed expression that happens to evaluate true still fails:
// the guard reasons about what is written down, not about what runs.
//
// v3 corrections applied here:
//
//   - inspect the EXACT projection argument, not "any composite literal in
//     the call" — a context or option argument containing Removed:true
//     could previously whitewash an eligible second argument;
//   - no basename-wide exemptions; the only exempt call site is the single
//     sanctioned one inside IndexPostUnlessAuthorErased, identified by its
//     enclosing function;
//   - parse/walk errors FAIL rather than being skipped — a safety guard
//     must not pass on a file it could not read;
//   - the deprecated unconditional writers (IndexPost, TombstonePost) are
//     banned in production code too, so a future writer cannot simply pick
//     an unguarded API instead.

// sanctionedWrapper is the one function permitted to call
// ApplyPostProjection with a non-removal payload: it is the wrapper that
// performs the fence check and recheck around it.
const sanctionedWrapper = "IndexPostUnlessAuthorErased"

// bannedPostWriters are APIs that write posts_v1 with no revision
// comparison and no fence handshake. Kept exported for out-of-tree
// compatibility, but production code inside this module must not call them.
var bannedPostWriters = map[string]string{
	"IndexPost":     "unconditional write; use IndexPostUnlessAuthorErased",
	"TombstonePost": "unconditional write; use ApplyPostProjection with Removed:true",
}

func TestNoProductionCodeWritesEligiblePostsWithoutTheFenceHandshake(t *testing.T) {
	root := findServiceRoot(t)

	var projectionCalls, sanctionedSkips int
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "vendor" || info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// Fail closed: a file we cannot inspect might contain the very
			// call this guard exists to catch.
			t.Errorf("%s: parse failed, cannot verify post-write safety: %v", path, perr)
			return nil
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			enclosing := fn.Name.Name

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pos := fset.Position(call.Pos())

				// Deprecated unconditional writers are banned outright.
				if reason, banned := bannedPostWriters[sel.Sel.Name]; banned {
					t.Errorf("%s:%d calls the deprecated %s (%s). It writes posts_v1 "+
						"with no revision comparison and no author-fence handshake.",
						pos.Filename, pos.Line, sel.Sel.Name, reason)
					return true
				}

				if sel.Sel.Name != "ApplyPostProjection" {
					return true
				}

				// The single sanctioned non-removal call: the one inside
				// the wrapper that performs the check and the recheck.
				if enclosing == sanctionedWrapper {
					sanctionedSkips++
					return true
				}

				projectionCalls++
				if !projectionArgIsExplicitRemoval(call) {
					t.Errorf("%s:%d calls ApplyPostProjection whose PostProjection "+
						"argument does not set a literal top-level `Removed: true`.\n"+
						"Eligible post writes must use %s so the author-fence check "+
						"and recheck run — otherwise a delayed write can recreate "+
						"content for an account erased in the meantime.",
						pos.Filename, pos.Line, sanctionedWrapper)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// Floor guards: if a rename or refactor stops these from matching, the
	// test would pass while checking nothing.
	if projectionCalls < 4 {
		t.Fatalf("only %d direct ApplyPostProjection call sites were classified; "+
			"expected at least 4 (takedown, post/upload/crosspost deletes, "+
			"reconciler removal). Has the call moved or been renamed?", projectionCalls)
	}
	if sanctionedSkips != 1 {
		t.Fatalf("expected exactly 1 sanctioned call inside %s, found %d — the "+
			"exemption must stay pinned to that single wrapper",
			sanctionedWrapper, sanctionedSkips)
	}
}

// projectionArgIsExplicitRemoval inspects the PostProjection argument
// specifically — the last argument of ApplyPostProjection(ctx, p) — and
// reports whether it is a composite literal with a top-level
// `Removed: true`.
//
// "Top-level" matters: a nested Doc field or a value inside some other
// struct must not satisfy it. "Literal true" matters: a variable that
// happens to be true today is not evidence about tomorrow.
func projectionArgIsExplicitRemoval(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	arg := call.Args[len(call.Args)-1]

	lit, ok := arg.(*ast.CompositeLit)
	if !ok {
		return false // a variable or helper result; not verifiable here
	}
	if !compositeLitIsPostProjection(lit) {
		return false
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue // positional literal; not verifiable
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Removed" {
			continue
		}
		id, ok := kv.Value.(*ast.Ident)
		return ok && id.Name == "true"
	}
	return false
}

// compositeLitIsPostProjection accepts both `PostProjection{…}` (inside
// this package) and `search.PostProjection{…}` (outside it). An untyped
// literal is rejected: we cannot tell what it is.
func compositeLitIsPostProjection(lit *ast.CompositeLit) bool {
	switch t := lit.Type.(type) {
	case *ast.Ident:
		return t.Name == "PostProjection"
	case *ast.SelectorExpr:
		return t.Sel.Name == "PostProjection"
	default:
		return false
	}
}

// findServiceRoot walks up to the search-service module root so the guard
// covers cmd/ as well as internal/.
func findServiceRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate the search-service module root")
	return ""
}

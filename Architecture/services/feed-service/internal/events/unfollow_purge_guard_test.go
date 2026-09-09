package events

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The unfollow purge must never run without first establishing that nothing
// else entitles the ex-follower to the author's fanned-out rows.
//
// Why a source-level test. Consumer holds a concrete *scylla.TimelineStore
// and a concrete *service.Service, so exercising handleUserUnfollowed end to
// end needs a live Scylla and a live graph-service — the same reason
// internal/service/coldstart_narrowing_test.go pins its guard this way. The
// specific failure this defends against is a plausible one: the DELETE was
// dead code from the day it was written (a filtering query Scylla refused),
// so the guard's absence cost nothing and was invisible. Now that the
// statement really deletes, dropping the guard silently destroys the
// timeline rows of everyone who unfollowed someone they are still connected
// to, with nothing that can restore them.

func TestUnfollowPurgeIsGuardedByTheClaimCheck(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "consumer.go", nil, 0)
	if err != nil {
		t.Fatalf("parse consumer.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if ok && d.Name.Name == "handleUserUnfollowed" && d.Body != nil {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatal("handleUserUnfollowed not found in consumer.go — has it been renamed? " +
			"This test would otherwise pass while checking nothing")
	}

	claimPos, retainedVar := claimCall(fn.Body, "FanoutClaimAfterUnfollow")
	purgePos := calleePos(fn.Body, "DeleteHomeTimelineEntriesByAuthorForUser")

	if purgePos == token.NoPos {
		t.Fatal("handleUserUnfollowed no longer calls DeleteHomeTimelineEntriesByAuthorForUser " +
			"— either the purge moved (move this test with it) or unfollows stopped " +
			"purging at all")
	}
	if claimPos == token.NoPos {
		t.Fatal("handleUserUnfollowed purges the follower's timeline without calling " +
			"FanoutClaimAfterUnfollow first. FanoutPost writes home-timeline rows to the " +
			"author's followers UNION their connections (and, for trusted posts, their " +
			"close friends), so an unfollow on its own does not establish that these rows " +
			"may be deleted. See internal/service/unfollow_purge.go")
	}
	if claimPos > purgePos {
		t.Fatal("FanoutClaimAfterUnfollow is called AFTER the purge — the rows are " +
			"already gone by the time the question is asked")
	}

	// And the ANSWER has to be acted on. Not just "some early return exists
	// between them" — the `if err != nil` that already sits there would
	// satisfy that while the answer was assigned to `_` and ignored. The
	// return must be conditional on the retained flag itself.
	if retainedVar == "" || retainedVar == "_" {
		t.Fatal("handleUserUnfollowed discards FanoutClaimAfterUnfollow's answer, so the " +
			"question is asked and ignored — a connection's rows are still deleted")
	}
	if !hasGuardingReturn(fn.Body, retainedVar, claimPos, purgePos) {
		t.Fatalf("nothing between the claim check and the purge returns early on a bare %q "+
			"condition, so a "+
			"follower who is still a connection of (or a close friend listed by) the "+
			"author has their rows deleted anyway, with no way to restore them", retainedVar)
	}
}

// claimCall finds the call to `name` and the identifier its FIRST result is
// assigned to — the retained flag. Returns (NoPos, "") when absent.
func claimCall(root ast.Node, name string) (token.Pos, string) {
	pos, variable := token.NoPos, ""
	ast.Inspect(root, func(n ast.Node) bool {
		if pos != token.NoPos {
			return false
		}
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != name {
			return true
		}
		pos = call.Pos()
		if len(assign.Lhs) > 0 {
			if id, ok := assign.Lhs[0].(*ast.Ident); ok {
				variable = id.Name
			}
		}
		return false
	})
	if pos == token.NoPos {
		// Called but not assigned (or assigned in a shape this does not
		// read): report the position so the caller can distinguish "never
		// asked" from "asked and thrown away".
		return calleePos(root, name), ""
	}
	return pos, variable
}

// calleePos returns the position of the first call to a method with this
// name, or token.NoPos.
func calleePos(root ast.Node, name string) token.Pos {
	pos := token.NoPos
	ast.Inspect(root, func(n ast.Node) bool {
		if pos != token.NoPos {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == name {
			pos = call.Pos()
			return false
		}
		return true
	})
	return pos
}

// hasGuardingReturn reports whether some if-statement strictly between the
// two positions tests `variable` and returns instead of falling through to
// the purge.
func hasGuardingReturn(root ast.Node, variable string, after, before token.Pos) bool {
	found := false
	ast.Inspect(root, func(n ast.Node) bool {
		if found || n == nil {
			return !found
		}
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		if ifStmt.Pos() <= after || ifStmt.Pos() >= before {
			return true
		}
		if !isBareIdent(ifStmt.Cond, variable) {
			return true
		}
		ast.Inspect(ifStmt.Body, func(m ast.Node) bool {
			if _, ok := m.(*ast.ReturnStmt); ok {
				found = true
				return false
			}
			return true
		})
		return true
	})
	return found
}

// isBareIdent reports whether an expression IS the named identifier, and
// nothing more.
//
// The looser "does this condition mention `retained` anywhere" version of
// this check passed `if retained && false`, which keeps every shape the test
// looks for while neutering the gate completely. Requiring the bare
// identifier is the only form that cannot be watered down in place.
//
// It also rejects `if retained || somethingNew`, and that is intended: a
// further reason to keep the rows belongs in
// service.fanoutClaimSurvivesUnfollow, which is a total switch over the
// relationship with every reason named in one place, not bolted onto the
// consumer where the next reader will not find it.
func isBareIdent(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}

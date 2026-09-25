package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

/*
	Admission re-checks what was true at invitation.

	AcceptInvite verified the invite belonged to the caller, was pending and
	had not expired — and then wrote the membership. It never asked whether
	the invitee had been banned since, nor whether the group still existed.
	The store is concrete (*store.Store), so this is pinned structurally: the
	two checks must be present in the function body and must come BEFORE the
	membership write. Parsed with mode 0 — no comments — so the names in the
	explanatory comment above the function can never satisfy the guard.
*/

func acceptInviteDecl(t *testing.T) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "group.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "AcceptInvite" && fn.Body != nil {
			return fn
		}
	}
	t.Fatal("AcceptInvite not found in group.go — renamed? This guard would otherwise pass while checking nothing")
	return nil
}

// firstSelectorCall returns the position of the first call whose method is
// `name` (s.store.CheckBanned(...) etc.), or an invalid position.
func firstSelectorCall(fn *ast.FuncDecl, name string) token.Pos {
	var pos token.Pos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if pos.IsValid() {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
				pos = call.Pos()
			}
		}
		return true
	})
	return pos
}

func TestAcceptInviteRechecksBanAndGroupBeforeAdmitting(t *testing.T) {
	fn := acceptInviteDecl(t)

	add := firstSelectorCall(fn, "AddMemberWithInviter")
	if !add.IsValid() {
		t.Fatal("AcceptInvite no longer calls AddMemberWithInviter — re-anchor this guard, do not delete it")
	}
	ban := firstSelectorCall(fn, "CheckBanned")
	if !ban.IsValid() {
		t.Fatal("AcceptInvite does not call CheckBanned: an invite issued before a ban admits the banned user the moment they accept")
	}
	if ban > add {
		t.Fatal("AcceptInvite checks the ban AFTER writing the membership; the check must come first")
	}
	group := firstSelectorCall(fn, "GetGroupByID")
	if !group.IsValid() {
		t.Fatal("AcceptInvite does not load the group before admitting: an invite to a since-deleted group still writes a membership row")
	}
	if group > add {
		t.Fatal("AcceptInvite loads the group only AFTER writing the membership (the old chat-sync lookup); the status check must come first")
	}

	// The status refusal must exist as a literal comparison, not only as a
	// nil check — GetGroupByID hides 'deleted' but not 'archived'.
	sawArchived := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"archived"` {
			sawArchived = true
		}
		return true
	})
	if !sawArchived {
		t.Fatal(`AcceptInvite never compares the group's status to "archived"; an invite to an archived group would still admit`)
	}
}

// The accept-side refusal must be the shared generic error, so the response
// cannot be used to learn WHICH condition applied.
func TestAcceptInviteRefusalIsGeneric(t *testing.T) {
	fn := acceptInviteDecl(t)
	count := 0
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if ret, ok := n.(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
			if id, ok := ret.Results[0].(*ast.Ident); ok && id.Name == "ErrInviteNoLongerAcceptable" {
				count++
			}
		}
		return true
	})
	if count < 2 {
		t.Fatalf("expected the ban and the group-state refusals to both return ErrInviteNoLongerAcceptable, found %d such returns", count)
	}
}

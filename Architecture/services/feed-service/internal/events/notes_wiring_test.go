package events

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Echo notes are wired into the consumer at four points. The handlers hold a
// concrete service and Scylla store, so — as unfollow_purge_guard_test.go
// explains — the wiring is pinned at the source level; the behaviour behind
// each call is tested in internal/service/notes_test.go.

func handlerDecl(t *testing.T, file, name string) *ast.FuncDecl {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name && fn.Body != nil {
			return fn
		}
	}
	t.Fatalf("%s not found in %s", name, file)
	return nil
}

// A note must reach FanoutNote (which knows reply_to_id), never FanoutPost,
// on creation: FanoutPost has no reply_to_id and would treat a reply as
// whatever echo_notes says, which on creation is nothing yet.
func TestPostCreatedRoutesNotesToFanoutNote(t *testing.T) {
	fn := handlerDecl(t, "consumer.go", "handlePostCreated")
	note, post := calleePos(fn.Body, "FanoutNote"), calleePos(fn.Body, "FanoutPost")
	if note == token.NoPos {
		t.Fatal("handlePostCreated never calls FanoutNote: notes would not reach notes_timeline")
	}
	if post != token.NoPos && note > post {
		t.Fatal("handlePostCreated reaches FanoutPost before FanoutNote")
	}
	var guarded bool
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		if sel, ok := ifs.Cond.(*ast.BinaryExpr); ok {
			if x, ok := sel.Y.(*ast.SelectorExpr); ok && x.Sel.Name == "NoteContentType" && calleePos(ifs.Body, "FanoutNote") != token.NoPos {
				guarded = true
			}
		}
		return true
	})
	if !guarded {
		t.Fatal("FanoutNote is not under a content_type == NoteContentType check")
	}
}

func TestUnfollowPurgesNotesAndFollowBackfillsThem(t *testing.T) {
	if calleePos(handlerDecl(t, "consumer.go", "handleUserUnfollowed").Body, "PurgeNotesAfterUnfollow") == token.NoPos {
		t.Fatal("handleUserUnfollowed does not purge the notes timeline")
	}
	if calleePos(handlerDecl(t, "consumer.go", "handleUserFollowed").Body, "BackfillNotesOnFollow") == token.NoPos {
		t.Fatal("handleUserFollowed does not backfill the notes timeline")
	}
}

func TestDeleteAndRestoreTrackNoteState(t *testing.T) {
	if calleePos(handlerDecl(t, "post_lifecycle.go", "handlePostDeleted").Body, "MarkNoteDeleted") == token.NoPos {
		t.Fatal("handlePostDeleted leaves a deleted note as a pull / For You candidate")
	}
	if calleePos(handlerDecl(t, "post_lifecycle.go", "handlePostRestored").Body, "MarkNoteDeleted") == token.NoPos {
		t.Fatal("handlePostRestored leaves a restored note marked deleted")
	}
}

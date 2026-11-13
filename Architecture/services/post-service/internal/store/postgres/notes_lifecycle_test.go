package postgres

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Echo delete / restore (2026-11-13): a reply's parent counts it once while
// it is live, on every delete path, and a restore counts it again exactly
// once. The behaviour against a real database is
// notes_lifecycle_integration_test.go (CI); these pin the pure rule, the SQL
// and that both transactions call it on their transition.

func TestNoteReplyCountDelta(t *testing.T) {
	parent := uuid.New()
	reply := NoteLinks{ReplyToID: &parent}
	quote := NoteLinks{QuoteOfID: &parent}
	for _, tc := range []struct {
		name        string
		contentType string
		links       NoteLinks
		restore     bool
		want        int
	}{
		{"reply deleted", NoteContentType, reply, false, -1},
		{"reply restored", NoteContentType, reply, true, 1},
		{"top-level note deleted", NoteContentType, NoteLinks{}, false, 0},
		{"top-level note restored", NoteContentType, NoteLinks{}, true, 0},
		{"quote restored", NoteContentType, quote, true, 0},
		{"post with links restored", "post", reply, true, 0},
		{"post with links deleted", "post", reply, false, 0},
	} {
		if got := noteReplyCountDelta(tc.contentType, tc.links, tc.restore); got != tc.want {
			t.Errorf("%s: delta %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestNoteReplyCountAdjustSQL(t *testing.T) {
	sql := strings.Join(strings.Fields(noteReplyCountAdjustSQL), " ")
	for _, want := range []string{
		"reply_count = GREATEST(reply_count + $2, 0)", // never below zero
		"WHERE id = $1 AND content_type = 'note'",     // only a note's count
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("reply_count adjust SQL lacks %q:\n%s", want, sql)
		}
	}
}

func TestNoteLinksSQL(t *testing.T) {
	sql := strings.Join(strings.Fields(noteLinksSQL), " ")
	for _, want := range []string{
		"LEFT JOIN posts t ON t.id = COALESCE(n.reply_to_id, n.quote_of_id)",
		"n.content_type = 'note'",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("note links SQL lacks %q:\n%s", want, sql)
		}
	}
}

// Both transactions move the count on their own transition, in the
// transaction, with the right direction; the delete names the target on
// PostDeleted.
func TestDeleteAndRestoreMoveTheReplyCountInTheirTransaction(t *testing.T) {
	del := funcSource(t, "DeleteUploadCascade")
	for _, want := range []string{
		"noteDeleteOrRestoreTx(ctx, tx, postID, contentType, false)",
		"ReplyToID: uuidString(links.ReplyToID)",
		"QuoteOfID: uuidString(links.QuoteOfID)",
		"TargetAuthorID: uuidString(links.TargetAuthorID)",
	} {
		if !strings.Contains(del, want) {
			t.Errorf("DeleteUploadCascade lacks %q", want)
		}
	}
	if i, j := strings.Index(del, "deleted_at IS NULL"), strings.Index(del, "noteDeleteOrRestoreTx"); i < 0 || j < i {
		t.Errorf("DeleteUploadCascade must move the count only after the live-row transition")
	}
	res := funcSource(t, "RestorePost")
	if !strings.Contains(res, "noteDeleteOrRestoreTx(ctx, tx, postID, contentType, true)") {
		t.Errorf("RestorePost does not count a restored reply again")
	}
	if i, j := strings.Index(res, "ErrPostNotDeleted"), strings.Index(res, "noteDeleteOrRestoreTx"); i < 0 || j < i {
		t.Errorf("RestorePost must move the count only after refusing a live row")
	}
}

func TestFollowedHashtagNotesSQL(t *testing.T) {
	sql := strings.Join(strings.Fields(followedHashtagNotesSQL()), " ")
	for _, want := range []string{
		"p.content_type = 'note'",
		"p.reply_to_id IS NULL -- top-level notes only",
		"p.hashtags && $1::text[]",
		"p.visibility = 'public'",
		ViewerEligibleSQL("p"),
		"p.author_id <> $2",
		"p.created_at >= $3",
		"LIMIT $4",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("followed-hashtag notes SQL lacks %q:\n%s", want, sql)
		}
	}
}

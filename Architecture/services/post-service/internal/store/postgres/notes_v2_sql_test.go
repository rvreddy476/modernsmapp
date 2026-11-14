package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Echo v2 store rules pinned on the SQL (no database here; CI's
// notes_v2_integration_test.go runs them against Postgres).

func mustContain(t *testing.T, name, sql string, parts ...string) {
	t.Helper()
	flat := strings.Join(strings.Fields(sql), " ")
	for _, p := range parts {
		if !strings.Contains(flat, strings.Join(strings.Fields(p), " ")) {
			t.Errorf("%s lacks %q:\n%s", name, p, flat)
		}
	}
}

// Scheduled notes are in no read: GetNotes (by id, replies' parents,
// conversation, reply / quote targets) reads published notes only.
func TestNoteV2SQL_GetNotesIsPublishedOnly(t *testing.T) {
	mustContain(t, "getNotesSQL", getNotesSQL, "content_type = 'note'", "deleted_at IS NULL", "publish_at IS NULL")
}

// Every v2 listing keeps notes, public, published (ViewerEligibleSQL holds
// publish_at IS NULL).
func TestNoteV2SQL_DiscoveryIsPublicPublishedNotesOnly(t *testing.T) {
	quote := uuid.New()
	cursor := encodeKeysetCursor(time.Now(), uuid.NewString())
	for _, q := range []NoteDiscoverQuery{
		{QuoteOfID: &quote, Limit: 20},
		{QuoteOfID: &quote, Limit: 20, Cursor: cursor},
		{Hashtag: "rain", Limit: 20, Top: true, Cursor: "o20"},
		{Hashtag: "rain", Limit: 20},
		{Search: "monsoon", Limit: 20, Top: true},
		{Search: "monsoon", Limit: 20, Cursor: cursor},
	} {
		sql, args := noteDiscoverSQL(q)
		mustContain(t, "discover", sql, "p.content_type = 'note'", "p.visibility = 'public'", ViewerEligibleSQL("p"))
		if strings.Count(sql, "$") < len(args) {
			t.Fatalf("%+v: %d args for %s", q, len(args), sql)
		}
	}
	sql, _ := noteDiscoverSQL(NoteDiscoverQuery{QuoteOfID: &quote, Limit: 20})
	mustContain(t, "quotes", sql, "p.quote_of_id = $2", "ORDER BY p.created_at DESC")
	sql, args := noteDiscoverSQL(NoteDiscoverQuery{Hashtag: "Rain", Limit: 20})
	mustContain(t, "hashtag", sql, "p.hashtags @> $2::text[]")
	if tags := args[1].([]string); tags[0] != "rain" {
		t.Fatalf("hashtag not lower-cased: %v", tags)
	}
	sql, _ = noteDiscoverSQL(NoteDiscoverQuery{Search: "x", Limit: 20})
	mustContain(t, "search", sql, "to_tsvector('simple'::regconfig, p.text) @@ websearch_to_tsquery('simple'::regconfig, $2)")
	if decodeNoteOffset("o5000") != noteTopMaxOffset || decodeNoteOffset("junk") != 0 || decodeNoteOffset(EncodeNoteOffset(40)) != 40 {
		t.Fatal("top offsets")
	}
}

// Trends: notes only, top-level, public, published, in the window, at
// least $2 distinct authors, ranked by authors then notes.
func TestNoteV2SQL_Trends(t *testing.T) {
	mustContain(t, "noteTrendsSQL", noteTrendsSQL,
		"p.content_type = 'note'", "p.reply_to_id IS NULL", "p.visibility = 'public'",
		ViewerEligibleSQL("p"), "p.created_at >= $1",
		"HAVING COUNT(DISTINCT p.author_id) >= $2", "ORDER BY authors DESC, note_count DESC")
}

// Views: published notes only, never the author's own, once per rolling
// 24 hours, and the counter moves only for the rows counted.
func TestNoteV2SQL_Views(t *testing.T) {
	mustContain(t, "recordNoteViewsSQL", recordNoteViewsSQL,
		"p.content_type = 'note'", "p.author_id <> $2", ViewerEligibleSQL("p"),
		"ON CONFLICT (note_id, viewer_id) DO UPDATE SET counted_at = EXCLUDED.counted_at",
		"WHERE note_views.counted_at <= EXCLUDED.counted_at - INTERVAL '24 hours'",
		"UPDATE posts SET note_view_count = note_view_count + 1 WHERE id IN (SELECT note_id FROM counted)")
}

// Bookmarks are private: the list is keyed on the caller alone.
func TestNoteV2SQL_BookmarksAreTheCallers(t *testing.T) {
	sql, extra := noteBookmarksSQL(encodeKeysetCursor(time.Now(), uuid.NewString()))
	mustContain(t, "noteBookmarksSQL", sql, "si.user_id = $2", "si.target_type = 'post'",
		"p.content_type = 'note'", ViewerEligibleSQL("p"), "(si.created_at, p.id) < ($3, $4)")
	if len(extra) != 2 {
		t.Fatalf("cursor args %v", extra)
	}
	mustContain(t, "noteCountsSQL", noteCountsSQL, "q.quote_of_id = x.id", ViewerEligibleSQL("q"),
		"si.target_type = 'post' AND si.target_id = x.id")
}

// The publisher: SKIP LOCKED claim with a lease, failed notes never
// claimed, the flip a compare-and-set on a due publish_at.
func TestNoteV2SQL_Publisher(t *testing.T) {
	mustContain(t, "claimDueNotesSQL", claimDueNotesSQL,
		"p.content_type = 'note'", "p.publish_at IS NOT NULL AND p.publish_at <= $1 AND p.deleted_at IS NULL",
		"s.failed_at IS NULL", "s.next_attempt_at <= $1", "FOR UPDATE OF p SKIP LOCKED",
		"SET next_attempt_at = EXCLUDED.next_attempt_at")
	mustContain(t, "recordNotePublishFailureSQL", recordNotePublishFailureSQL,
		"first_failed_at = COALESCE(note_schedules.first_failed_at, EXCLUDED.first_failed_at)",
		"<= $5 THEN $3 ELSE NULL END")
	src := funcSource(t, "PublishScheduledNote")
	mustContain(t, "PublishScheduledNote", src,
		"publish_at IS NOT NULL AND publish_at <= $2 AND deleted_at IS NULL FOR UPDATE",
		"DELETE FROM note_schedules")
}

// The ordinary scheduled-post machinery never takes a note, and ordinary
// trending never counts one.
func TestNoteV2SQL_PostSurfacesStayNoteFree(t *testing.T) {
	for _, fn := range []string{"ListDueScheduledPosts", "ListScheduledPostsByAuthor", "GetTrendingHashtags24h"} {
		if src := funcSource(t, fn); !strings.Contains(src, "NotNoteSQL(") {
			t.Errorf("%s lets notes through", fn)
		}
	}
	if src := funcSource(t, "GetTrendingHashtags24h"); !strings.Contains(src, "p.publish_at IS NULL") {
		t.Error("GetTrendingHashtags24h counts scheduled posts")
	}
}

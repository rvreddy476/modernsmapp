package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Echo read-path audit (docs/designs/echo-contract.md: notes "appear in no
// existing list"). One test per listing that could otherwise return a
// note. The builders that are pure are asserted on the SQL they produce;
// the rest are asserted on their printed source (funcSource, the same
// source-scan style as feed_reads_guard_test.go). The behaviour against a
// real database is CI's integration suite.

const notNote = "content_type <> 'note'"

func TestNotNoteSQL(t *testing.T) {
	if got := NotNoteSQL(""); got != notNote {
		t.Fatalf("NotNoteSQL(\"\") = %q", got)
	}
	if got := NotNoteSQL("p"); got != "p."+notNote {
		t.Fatalf("NotNoteSQL(\"p\") = %q", got)
	}
}

// GET /v1/posts/by-author/:authorId — every sort, the author's own view and
// an explicit ?content_type=note.
func TestNotesExcluded_ByAuthor(t *testing.T) {
	for _, q := range []AuthorPostsQuery{
		{AuthorID: uuid.New(), Sort: AuthorSortLatest, Limit: 20},
		{AuthorID: uuid.New(), Sort: AuthorSortPopular, Limit: 20, IncludeNonApproved: true},
		{AuthorID: uuid.New(), Sort: AuthorSortOldest, Limit: 20, ContentType: "note"},
		{AuthorID: uuid.New(), Sort: AuthorSortLatest, Limit: 20, MembersOnly: true},
	} {
		sql, _ := authorPostsSQL(q, nil)
		if !strings.Contains(sql, notNote) {
			t.Fatalf("by-author SQL for %+v lets notes through:\n%s", q, sql)
		}
	}
}

// GET /v1/posts/by-author/:authorId/counts (and the admin creator counts).
func TestNotesExcluded_AuthorCounts(t *testing.T) {
	if src := funcSource(t, "GetPostCountsByAuthor"); !strings.Contains(src, `NotNoteSQL("")`) {
		t.Fatalf("GetPostCountsByAuthor counts notes:\n%s", src)
	}
}

// GET /v1/posts/recent (explore, Tube discovery fill).
func TestNotesExcluded_Recent(t *testing.T) {
	if src := funcSource(t, "GetRecentPosts"); !strings.Contains(src, `NotNoteSQL("")`) {
		t.Fatalf("GetRecentPosts lists notes:\n%s", src)
	}
}

// GET /v1/posts/bookmarks (the saved list the Home and You pages read).
func TestNotesExcluded_Bookmarks(t *testing.T) {
	if src := funcSource(t, "GetBookmarks"); !strings.Contains(src, `NotNoteSQL("p")`) {
		t.Fatalf("GetBookmarks lists notes:\n%s", src)
	}
}

// GET /v1/posts/trending, first page and a continued scroll.
func TestNotesExcluded_Trending(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	for _, types := range [][]string{nil, {"post"}, {"note"}} {
		q, _, _ := trendingQuery(types, 20, "", now)
		if !strings.Contains(q, "p."+notNote) {
			t.Fatalf("trending SQL (types %v) ranks notes:\n%s", types, q)
		}
	}
}

// GET /v1/hashtags/:tag/posts (recent and top) and its total, which share
// hashtagPostsFilter.
func TestNotesExcluded_HashtagPageAndTotal(t *testing.T) {
	for _, types := range [][]string{nil, {"post"}} {
		where, _ := hashtagPostsFilter([]any{"tag", 21}, types, []uuid.UUID{uuid.New()})
		if !strings.Contains(where, "p."+notNote) {
			t.Fatalf("hashtag filter (types %v) lists notes: %s", types, where)
		}
	}
	for _, fn := range []string{"getPostsByHashtagRecent", "getPostsByHashtagTop", "CountPostsByHashtag"} {
		if src := funcSource(t, fn); !strings.Contains(src, "hashtagPostsFilter(") {
			t.Fatalf("%s no longer uses the shared hashtag filter", fn)
		}
	}
}

// GET /v1/hashtags/followed/posts.
func TestNotesExcluded_FollowedHashtagPosts(t *testing.T) {
	if src := funcSource(t, "GetPostsByHashtags"); !strings.Contains(src, `NotNoteSQL("p")`) {
		t.Fatalf("GetPostsByHashtags lists notes:\n%s", src)
	}
}

// Reels, PostTube, liked reels, channel feeds, sound uses, the creator's
// uploads and the creator summary are closed sets of video / post kinds:
// each names its kinds and none of them is 'note'.
func TestNotesExcluded_TypeScopedSurfaces(t *testing.T) {
	for _, fn := range []string{
		"GetReelCandidates",            // GET /v1/reels (legacy)
		"GetFlickCandidates",           // Reels feed
		"GetLongVideoCandidates",       // PostTube feed
		"ListLikedShortFormPosts",      // GET /v1/reels/liked
		"CountCreatorContent",          // GET /v1/posts/me/summary
		"CountChannelContent",          // channel counts
		"GetUploadsByContentTypesOpts", // my uploads (callers pass video / reel / post kinds)
	} {
		src := funcSource(t, fn)
		if !strings.Contains(src, "content_type IN") && !strings.Contains(src, "content_type = ANY") &&
			!strings.Contains(src, "content_type = 'reel'") {
			t.Errorf("%s is not scoped to named kinds; a note could reach it", fn)
		}
		if strings.Contains(src, "'note'") {
			t.Errorf("%s names 'note'", fn)
		}
	}
	for name, sql := range map[string]string{
		"channelFeedWhere": channelFeedWhere, "soundUseCandidateSQL": soundUseCandidateSQL(),
	} {
		if !strings.Contains(sql, "content_type IN") || strings.Contains(sql, "'note'") {
			t.Errorf("%s is not scoped to video kinds: %s", name, sql)
		}
	}
}

// The note listings themselves keep content_type = 'note' on every branch,
// so a reply list or a profile tab can never return an ordinary post.
func TestNoteListSQLIsNotesOnly(t *testing.T) {
	parent, author := uuid.New(), uuid.New()
	cursor := encodeKeysetCursor(time.Now(), uuid.NewString())
	for _, q := range []NoteListQuery{
		{ParentID: &parent, Limit: 20},
		{ParentID: &parent, Limit: 20, Cursor: cursor},
		{AuthorID: &author, Tab: NoteTabNotes, Limit: 20},
		{AuthorID: &author, Tab: NoteTabReplies, Limit: 20, IncludeOwn: true},
		{AuthorID: &author, Tab: NoteTabReposts, Limit: 20, Cursor: cursor},
	} {
		sql, args := noteListSQL(q)
		if !strings.Contains(sql, "p.content_type = 'note'") {
			t.Fatalf("note listing %+v is not notes-only:\n%s", q, sql)
		}
		if !q.IncludeOwn && !strings.Contains(sql, ViewerEligibleSQL("p")) {
			t.Fatalf("note listing %+v skips viewer eligibility:\n%s", q, sql)
		}
		if strings.Count(sql, "$") < len(args) {
			t.Fatalf("note listing %+v: %d args for %s", q, len(args), sql)
		}
	}
	notes, _ := noteListSQL(NoteListQuery{AuthorID: &author, Tab: NoteTabNotes, Limit: 20})
	replies, _ := noteListSQL(NoteListQuery{AuthorID: &author, Tab: NoteTabReplies, Limit: 20})
	if !strings.Contains(notes, "reply_to_id IS NULL") || !strings.Contains(replies, "reply_to_id IS NOT NULL") {
		t.Fatalf("notes / replies tabs do not split on reply_to_id:\n%s\n%s", notes, replies)
	}
}

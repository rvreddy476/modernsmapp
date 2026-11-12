package search

import (
	"context"
	"testing"
)

// Echo (2026-11-12, echo-contract "search-service"): notes stay out of
// ordinary post search. The exclusion lives in encodeQuery, which every
// posts_v1 query passes through keyed on author_id.

// hasNoteExclusion reports whether the top-level bool must_not carries
// {"term":{"content_type":"note"}}.
func hasNoteExclusion(t *testing.T, encoded map[string]interface{}) bool {
	t.Helper()
	for _, c := range mustNotClauses(t, encoded) {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		term, ok := m["term"].(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := term["content_type"]; ok && v == "note" {
			return true
		}
	}
	return false
}

// Every viewer, resolved or not, gets the exclusion on a posts query.
func TestEncodeQuery_NotesExcludedFromPostSearchForEveryScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"unscoped", context.Background()},
		{"anonymous", WithViewerScope(context.Background(), "", nil, nil)},
		{"signed in with blocks", WithViewerScope(context.Background(), "me", []string{"b"}, []string{"f"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := map[string]interface{}{"query": map[string]interface{}{"bool": map[string]interface{}{}}}
			if !hasNoteExclusion(t, decodeQuery(t, tc.ctx, q, ownerFieldForIndex(IndexPosts))) {
				t.Fatal("posts query must exclude content_type note")
			}
		})
	}
}

// The real post-search body (the one SearchPostsFiltered sends), with and
// without a content_type narrowing, keeps notes out.
func TestBuildPostSearchQuery_EncodedExcludesNotes(t *testing.T) {
	for _, types := range [][]string{nil, {"post"}, {"long_video", "video"}} {
		q := BuildPostSearchQuery("hello", types, CreditFilters{}, 20)
		if !hasNoteExclusion(t, decodeQuery(t, context.Background(), q, "author_id")) {
			t.Fatalf("post search with types %v does not exclude notes", types)
		}
	}
}

// A function_score posts query (the ranked multi-entity path) is wrapped and
// still excludes notes.
func TestEncodeQuery_NotesExcludedFromNonBoolPostsQuery(t *testing.T) {
	q := map[string]interface{}{"query": map[string]interface{}{"function_score": map[string]interface{}{"query": map[string]interface{}{"match_all": map[string]interface{}{}}}}}
	if !hasNoteExclusion(t, decodeQuery(t, context.Background(), q, "author_id")) {
		t.Fatal("wrapped posts query lost the note exclusion")
	}
}

// Users, channels, products and hashtags have no content_type of "note"; the
// clause is a posts rule only.
func TestEncodeQuery_NoteExclusionOnlyOnPosts(t *testing.T) {
	for _, field := range []string{"user_id", "owner_id", "seller_id", ""} {
		q := map[string]interface{}{"query": map[string]interface{}{"bool": map[string]interface{}{}}}
		if hasNoteExclusion(t, decodeQuery(t, context.Background(), q, field)) {
			t.Fatalf("note exclusion applied to idField %q", field)
		}
	}
}

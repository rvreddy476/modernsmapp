package http

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/google/uuid"
)

// Page-scoped search rows (2026-09-05): the shape Android keys on, and the
// nil-safety of the hydration (no store, no media client = plain rows).

func TestPostResultsShapeWithoutHydrators(t *testing.T) {
	h := &Handler{} // no store, no media client
	docs := []search.PostDoc{{
		PostID: "p1", AuthorID: "a1", Text: "Tube long video test", Title: "Tube test: landscape 12s",
		ContentType: "long_video", DurationMs: 12000, MediaID: "m1", MediaKind: "video",
	}}
	rows := h.postResults(context.Background(), uuid.Nil, docs)
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	raw, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "post_id", "author", "title", "text", "content_type", "created_at", "duration_ms", "media_id", "media_kind", "thumbnail_url", "playback_url"} {
		if _, ok := m[key]; !ok {
			t.Errorf("row lacks %q: %s", key, raw)
		}
	}
	if m["id"] != "p1" || m["content_type"] != "long_video" || m["duration_ms"].(float64) != 12000 {
		t.Fatalf("row = %s", raw)
	}
	author, _ := m["author"].(map[string]any)
	if author["id"] != "a1" {
		t.Fatalf("author = %v", m["author"])
	}
	if m["thumbnail_url"] != nil || m["playback_url"] != nil {
		t.Fatalf("unresolved media must be null, got %s", raw)
	}
	if h.postResults(context.Background(), uuid.Nil, nil) == nil {
		t.Fatal("empty page must be an empty array, not null")
	}
}

func TestContentTypesForKind(t *testing.T) {
	cases := map[string][]string{
		"": nil, "all": nil, "posts": nil,
		"videos": {"long_video", "video"}, "VIDEO": {"long_video", "video"},
		"flicks": {"flick", "reel"}, "reels": {"flick", "reel"},
	}
	for kind, want := range cases {
		got, ok := search.ContentTypesForKind(kind)
		if !ok {
			t.Errorf("%q rejected", kind)
			continue
		}
		if len(got) != len(want) {
			t.Errorf("%q = %v want %v", kind, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q = %v want %v", kind, got, want)
			}
		}
	}
	if _, ok := search.ContentTypesForKind("profiles"); ok {
		t.Fatal("unknown kind must be rejected")
	}
}

package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// Tube discovery fill (2026-09-05): /v1/feed/videos tops a short FIRST page
// up from recent public long videos. The merge is pure; this pins its rules.
func TestMergeDiscoveryFill(t *testing.T) {
	now := time.Now()
	item := func(ct string) FeedItem {
		return FeedItem{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: now, ContentType: ct, Source: sourceTimeline}
	}
	followed := item("long_video")

	t.Run("fills up to the limit and marks fill rows as discovery", func(t *testing.T) {
		fill := []FeedItem{item("long_video"), item("long_video"), item("long_video")}
		got := mergeDiscoveryFill([]FeedItem{followed}, fill, 3)
		if len(got) != 3 {
			t.Fatalf("len=%d want 3", len(got))
		}
		if got[0].PostID != followed.PostID || got[0].Source != sourceTimeline {
			t.Fatalf("timeline row must keep its place and source: %+v", got[0])
		}
		for _, g := range got[1:] {
			if g.Source != sourceColdStart {
				t.Fatalf("fill row source=%q want %q", g.Source, sourceColdStart)
			}
		}
	})

	t.Run("a fill row already on the page is not shown twice", func(t *testing.T) {
		dup := followed
		got := mergeDiscoveryFill([]FeedItem{followed}, []FeedItem{dup, item("long_video")}, 5)
		if len(got) != 2 {
			t.Fatalf("len=%d want 2 (duplicate dropped)", len(got))
		}
	})

	t.Run("only long-form content is accepted from the fill", func(t *testing.T) {
		got := mergeDiscoveryFill(nil, []FeedItem{item("flick"), item("post"), item("video")}, 5)
		if len(got) != 1 || got[0].ContentType != "video" {
			t.Fatalf("got=%+v want the one legacy long-form row", got)
		}
	})

	t.Run("a full page is left alone", func(t *testing.T) {
		page := []FeedItem{item("long_video"), item("long_video")}
		got := mergeDiscoveryFill(page, []FeedItem{item("long_video")}, 2)
		if len(got) != 2 || got[0].PostID != page[0].PostID {
			t.Fatalf("full page changed: %+v", got)
		}
	})
}

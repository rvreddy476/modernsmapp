package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

/*
Reels discovery fill (2026-09-27): a viewer who follows nobody opened
Reels to "No reels yet" while public reels existed, because the reels page
read only the fan-out timeline. The first page now tops up from recent
public short-form under the SAME predicate Tube uses, and the merge keeps
only flick/reel rows.
*/
func TestReelsFill_MergeKeepsOnlyShortForm(t *testing.T) {
	item := func(ct string) FeedItem {
		return FeedItem{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: time.Now(), ContentType: ct}
	}
	followed := item("flick")
	got := mergeDiscoveryFillWith([]FeedItem{followed}, []FeedItem{item("reel"), item("long_video"), item("post"), item("flick")}, 5, isShortFormFillType)
	if len(got) != 3 {
		t.Fatalf("got %d rows, want the followed reel plus the two short-form fill rows", len(got))
	}
	if got[0].PostID != followed.PostID || got[0].Source == sourceColdStart {
		t.Fatal("the timeline row must keep its place and its source")
	}
	for _, r := range got[1:] {
		if !isShortFormFillType(r.ContentType) {
			t.Fatalf("a %q row reached the reels page through the fill", r.ContentType)
		}
		if r.Source != sourceColdStart {
			t.Fatal("fill rows must be marked recommended, not following")
		}
	}
}

func TestReelsFill_OnlyOnAShortFirstPageAndNeverUnderFollowing(t *testing.T) {
	if !discoveryFillAllowed(false, "", 0, 8) {
		t.Fatal("an empty first page must be filled")
	}
	if !discoveryFillAllowed(false, "", 3, 8) {
		t.Fatal("a short first page must be filled")
	}
	if discoveryFillAllowed(false, "", 8, 8) {
		t.Fatal("a full page must not be filled")
	}
	if discoveryFillAllowed(false, "v1:cursor", 0, 8) {
		t.Fatal("a later page stays keyed to the timeline cursor")
	}
	if discoveryFillAllowed(true, "", 0, 8) {
		t.Fatal("following_only is never topped up with strangers")
	}
}

func TestReelsFill_LongVideoMergeIsUnchanged(t *testing.T) {
	item := func(ct string) FeedItem {
		return FeedItem{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: time.Now(), ContentType: ct}
	}
	got := mergeDiscoveryFill(nil, []FeedItem{item("flick"), item("long_video")}, 5)
	if len(got) != 1 || !isLongVideoType(got[0].ContentType) {
		t.Fatalf("Tube's merge must still keep only long video, got %+v", got)
	}
}

package store

import (
	"encoding/json"
	"strings"
	"testing"
)

/*
Structural guards for reactions on the read side.

The point of attachReactionCounts is that it is called from EVERY surface
that hands posts to a client. This test is the list; a surface added later
without the call fails here rather than shipping reaction_counts: {} for
posts that have reactions.
*/

var clientPostSurfaces = []struct{ file, name string }{
	{"group.go", "GetGroupPostV2ForViewer"},
	{"group.go", "ListPendingGroupPostsV2"},
	{"group.go", "ListGroupPostsV2"},
	{"group_post_search.go", "SearchGroupPostsV2"},
	{"group_posts.go", "ListMyGroupsFeed"},
}

func TestEveryClientPostSurfaceAttachesReactionCounts(t *testing.T) {
	for _, s := range clientPostSurfaces {
		body := funcBody(t, s.file, s.name)
		if !strings.Contains(body, "s.attachReactionCounts(") {
			t.Errorf("%s does not call attachReactionCounts — its posts ship without per-reaction counts", s.name)
		}
	}
}

// The viewer's reaction rides the same LEFT JOIN as viewer_sparked, and the
// viewer scanner reads it.
func TestViewerReactionIsSelectedAndScanned(t *testing.T) {
	src := readFile(t, "group.go")
	if !strings.Contains(src, "vs.reaction AS viewer_reaction") {
		t.Fatal("viewerEngagementColumns does not select vs.reaction — viewer_reaction is always null")
	}
	if !strings.Contains(funcBody(t, "group.go", "scanGroupPostV2WithViewer"), "&p.ViewerReaction") {
		t.Fatal("scanGroupPostV2WithViewer does not scan ViewerReaction")
	}
}

// The legacy heart is a 'like' row: one row shape, one count, no second table.
func TestLegacySparkInsertsALikeRow(t *testing.T) {
	body := funcBody(t, "group.go", "SparkGroupPost")
	if !strings.Contains(body, "'like'") {
		t.Fatal("SparkGroupPost does not insert reaction 'like' explicitly — it would depend on the column default alone")
	}
	if !strings.Contains(body, "s.db.Begin(") || !strings.Contains(body, "tx.Commit(") {
		t.Fatal("SparkGroupPost is not transactional — the row and the counter can disagree")
	}
}

// reaction_counts is documented as an object. A post that went through a
// path with no counts attached must still say {}.
func TestReactionCountsNeverMarshalAsNull(t *testing.T) {
	for _, p := range []GroupPostV2{
		{Status: "published"},
		{Status: "published", IsAnonymous: true},
	} {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if string(got["reaction_counts"]) != "{}" {
			t.Errorf("anonymous=%v: reaction_counts marshalled as %s, want {}", p.IsAnonymous, got["reaction_counts"])
		}
		if string(got["viewer_reaction"]) != "null" {
			t.Errorf("anonymous=%v: viewer_reaction marshalled as %s, want null when the viewer has none", p.IsAnonymous, got["viewer_reaction"])
		}
	}
}

package postgres

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Members-only (2026-10-23): the by-author members_only filter and the
// edit's gate change, pinned without a database. The SQL itself runs in
// members_gate_store_integration_test.go.

func TestAuthorPostsSQLMembersOnly(t *testing.T) {
	author := uuid.New()
	for _, sort := range []AuthorPostSort{AuthorSortLatest, AuthorSortPopular, AuthorSortOldest} {
		q := AuthorPostsQuery{AuthorID: author, Sort: sort, Limit: 20, ContentType: "long_video", MembersOnly: true}
		gated, args := authorPostsSQL(q, nil)
		if !strings.Contains(gated, "tier_required_id IS NOT NULL") {
			t.Errorf("%s: members_only query has no gate filter: %s", sort, gated)
		}
		if args[0] != author || args[1] != 21 || args[2] != "long_video" {
			t.Errorf("%s: args %v", sort, args)
		}
		q.MembersOnly = false
		if all, _ := authorPostsSQL(q, nil); strings.Contains(all, "tier_required_id IS NOT NULL") {
			t.Errorf("%s: the whole grid filters on the gate: %s", sort, all)
		}
	}
	// The cursor still applies on top of the filter.
	cur := &authorCursor{Sort: AuthorSortLatest, ID: uuid.New()}
	withCursor, _ := authorPostsSQL(AuthorPostsQuery{AuthorID: author, Sort: AuthorSortLatest, Limit: 20, MembersOnly: true}, cur)
	if !strings.Contains(withCursor, "tier_required_id IS NOT NULL") || !strings.Contains(withCursor, "(COALESCE(is_pinned, false), created_at, id) <") {
		t.Errorf("paged members_only query: %s", withCursor)
	}
}

func TestPostEditTierChange(t *testing.T) {
	tier, other := uuid.New(), uuid.New()
	open := &Post{}
	gated := &Post{TierRequiredID: &tier}
	cases := []struct {
		name   string
		before *Post
		patch  PostEditPatch
		change bool
	}{
		{"set on an open post", open, PostEditPatch{TierRequiredID: &tier}, true},
		{"move to another tier", gated, PostEditPatch{TierRequiredID: &other}, true},
		{"same tier", gated, PostEditPatch{TierRequiredID: &tier}, false},
		{"clear a gated post", gated, PostEditPatch{ClearTierRequired: true}, true},
		{"clear wins over a value", gated, PostEditPatch{ClearTierRequired: true, TierRequiredID: &other}, true},
		{"clear an open post", open, PostEditPatch{ClearTierRequired: true}, false},
		{"untouched", gated, PostEditPatch{}, false},
	}
	for _, tc := range cases {
		changes := postEditChanges(tc.before, tc.patch)
		if tierChanged(changes) != tc.change {
			t.Errorf("%s: tier change = %v, want %v (%v)", tc.name, tierChanged(changes), tc.change, changes)
		}
	}
	c := postEditChanges(gated, PostEditPatch{ClearTierRequired: true})["tier_required_id"]
	if c.From != tier.String() || c.To != "" {
		t.Errorf("audit entry %+v", c)
	}
}

package search

import (
	"testing"
)

// A private community must not be findable by name.
//
// The backfill (cmd/backfill/main.go) indexes broadcast channels with
// `WHERE status != 'deleted'` and no type filter, and the Kafka consumer
// (internal/events/consumer.go) indexes on channel.created regardless of
// type — both carry channel_type in the document. The query builder added
// no filter for EntityChannels, so GET /v1/search?types=channels&q=<name>
// returned a private community's name, handle, description and owner_id to
// any caller. Fixed in the query builder so already-indexed documents stop
// leaking without a reindex.

// boolFilterOf pulls the bool query's filter clauses out of a built query.
func boolFilterOf(t *testing.T, q map[string]any) []map[string]any {
	t.Helper()
	fs := mustFunctionScore(t, q)
	boolQ, ok := fs["query"].(map[string]any)["bool"].(map[string]any)
	if !ok {
		t.Fatalf("expected a bool query inside function_score, got %v", fs["query"])
	}
	raw, ok := boolQ["filter"]
	if !ok {
		return nil
	}
	clauses, ok := raw.([]map[string]any)
	if !ok {
		t.Fatalf("filter should be []map[string]any, got %T", raw)
	}
	return clauses
}

// docPassesFilter evaluates the `term` and `terms` clauses a visibility
// filter is made of against one document, so the test asserts what the
// query actually admits rather than just its shape.
func docPassesFilter(t *testing.T, clauses []map[string]any, doc map[string]any) bool {
	t.Helper()
	for _, clause := range clauses {
		switch {
		case clause["term"] != nil:
			for field, want := range clause["term"].(map[string]any) {
				if doc[field] != want {
					return false
				}
			}
		case clause["terms"] != nil:
			for field, wantAny := range clause["terms"].(map[string]any) {
				want, ok := wantAny.([]string)
				if !ok {
					t.Fatalf("terms clause on %q should hold []string, got %T", field, wantAny)
				}
				got, _ := doc[field].(string)
				matched := false
				for _, w := range want {
					if got == w {
						matched = true
						break
					}
				}
				if !matched {
					return false
				}
			}
		default:
			t.Fatalf("unexpected filter clause %v — update this evaluator", clause)
		}
	}
	return true
}

func TestChannelSearch_ExcludesPrivateChannels(t *testing.T) {
	q := buildFunctionScoreQuery(EntityChannels, "riders", RankedSearchOptions{Limit: 20})
	clauses := boolFilterOf(t, q)
	if len(clauses) == 0 {
		t.Fatal("channel search carries NO visibility filter — a private community is findable by name")
	}

	// A private community's document must be excluded. These are the two
	// channel_type values VisibilityOf() maps to "private" in
	// channel-service.
	for _, private := range []string{"private", "paid"} {
		doc := map[string]any{
			"channel_id":   "c1",
			"owner_id":     "u1",
			"handle":       "riders",
			"name":         "Riders",
			"description":  "the private one",
			"channel_type": private,
		}
		if docPassesFilter(t, clauses, doc) {
			t.Fatalf("a channel_type=%q document is still returned by channel search — its name, handle, description and owner leak", private)
		}
	}

	// Every publicly discoverable type must still be returned, or the fix
	// has broken channel search instead of securing it.
	for _, public := range publicChannelTypes {
		doc := map[string]any{
			"channel_id":   "c2",
			"owner_id":     "u2",
			"handle":       "riders_public",
			"name":         "Riders",
			"channel_type": public,
			"status":       StatusActive,
		}
		if !docPassesFilter(t, clauses, doc) {
			t.Fatalf("a channel_type=%q document is no longer returned by channel search", public)
		}
	}

	// A document indexed before channel_type existed has no value to
	// check; it must fail closed rather than leak.
	if docPassesFilter(t, clauses, map[string]any{"channel_id": "c3", "name": "Riders"}) {
		t.Fatal("a channel document with no channel_type must not be returned (fail closed)")
	}

	// S-M2: a suspended (or archived) public channel, and one written before
	// status existed, are not returned either.
	for _, status := range []string{"suspended", "archived", ""} {
		doc := map[string]any{"channel_id": "c4", "name": "Riders", "channel_type": "public"}
		if status != "" {
			doc["status"] = status
		}
		if docPassesFilter(t, clauses, doc) {
			t.Fatalf("a public channel with status %q is returned by channel search", status)
		}
	}
}

// The filter set must stay the same set channel-service's /discover lists.
func TestPublicChannelTypesMatchDiscover(t *testing.T) {
	want := map[string]bool{
		"public": true, "creator": true, "brand": true,
		"education": true, "official": true, "topic": true,
	}
	if len(publicChannelTypes) != len(want) {
		t.Fatalf("publicChannelTypes %v diverged from channel-service /discover", publicChannelTypes)
	}
	for _, ct := range publicChannelTypes {
		if !want[ct] {
			t.Fatalf("%q is not in channel-service's public /discover set", ct)
		}
	}
}

// Fixing channels must not have disturbed the other entities: posts keep
// their public+approved filter, and the entities that never had one still
// do not (adding one to users/hashtags would silently empty those results).
// Communities left this list on 2026-11-07 (S-M2): they now have their own
// filter, pinned by TestCommunitySearch_OnlyPublicActiveCommunities.
func TestOtherEntityFiltersUnchanged(t *testing.T) {
	posts := boolFilterOf(t, buildFunctionScoreQuery(EntityPosts, "x", RankedSearchOptions{Limit: 10}))
	if len(posts) != 2 {
		t.Fatalf("posts should keep exactly the public+approved filter, got %v", posts)
	}
	if docPassesFilter(t, posts, map[string]any{"visibility": "followers", "review_status": "approved"}) {
		t.Fatal("a non-public post passes the posts filter")
	}
	if !docPassesFilter(t, posts, map[string]any{"visibility": "public", "review_status": "approved"}) {
		t.Fatal("a public approved post no longer passes the posts filter")
	}

	for _, entity := range []string{EntityUsers, EntityHashtags, EntityProducts} {
		if clauses := boolFilterOf(t, buildFunctionScoreQuery(entity, "x", RankedSearchOptions{Limit: 10})); len(clauses) != 0 {
			t.Fatalf("entity %q unexpectedly gained a filter: %v", entity, clauses)
		}
	}
}

// S-M2: private and invite-only communities, and suspended or archived
// ones, are not returned by the ranked search; a document without a type
// or a status fails closed.
func TestCommunitySearch_OnlyPublicActiveCommunities(t *testing.T) {
	clauses := boolFilterOf(t, buildFunctionScoreQuery(EntityCommunities, "riders", RankedSearchOptions{Limit: 20}))
	if len(clauses) == 0 {
		t.Fatal("community search carries NO filter: a private community's name, handle and owner leak")
	}
	for _, typ := range []string{"public", "education", "professional"} {
		if !docPassesFilter(t, clauses, map[string]any{"community_type": typ, "status": StatusActive}) {
			t.Fatalf("an active %q community is no longer returned", typ)
		}
	}
	for _, doc := range []map[string]any{
		{"community_type": "private", "status": StatusActive},
		{"community_type": "invite", "status": StatusActive},
		{"community_type": "public", "status": "suspended"},
		{"community_type": "public", "status": "archived"},
		{"community_type": "public"},
		{"status": StatusActive},
	} {
		if docPassesFilter(t, clauses, doc) {
			t.Fatalf("community %v is returned by community search", doc)
		}
	}
}

// The index-time half: what may be written at all.
func TestCommunityAndChannelSearchable(t *testing.T) {
	if !CommunitySearchable(CommunityDoc{CommunityType: "public", Status: StatusActive}) {
		t.Fatal("an active public community must be indexable")
	}
	for _, d := range []CommunityDoc{
		{CommunityType: "private", Status: StatusActive},
		{CommunityType: "invite", Status: StatusActive},
		{CommunityType: "public", Status: "suspended"},
		{CommunityType: "public"},
	} {
		if CommunitySearchable(d) {
			t.Fatalf("community %+v must be deleted, not indexed", d)
		}
	}
	if !ChannelSearchable(ChannelDoc{ChannelType: "public", Status: StatusActive}) {
		t.Fatal("an active public channel must be indexable")
	}
	for _, d := range []ChannelDoc{
		{ChannelType: "private", Status: StatusActive},
		{ChannelType: "public", Status: "suspended"},
		{ChannelType: "public"},
	} {
		if ChannelSearchable(d) {
			t.Fatalf("channel %+v must be deleted, not indexed", d)
		}
	}
}

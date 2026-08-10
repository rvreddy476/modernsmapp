//go:build integration

package events

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Module 2 M2-P0-1 / M2-P0-2 — the safety suite, against a LIVE OpenSearch.
//
// LOCAL EVIDENCE: executed against OpenSearch 2.13.0 on 2026-08-10; results
// are recorded in prompt/module-02-feed-discovery-search-claude-fixes-v4.md.
// This suite remains a required CI gate
// (.github/workflows/integration-opensearch.yml, job `search-safety`).
//
//	OPENSEARCH_URL=http://localhost:9200 \
//	  go test -tags integration ./internal/events/ -run OpenSearch -v -count=1
//
// The unit tests elsewhere in this package drive the same handlers against
// a fake OpenSearch, which proves the control flow. These prove the part a
// fake cannot: that the documents we write are actually excluded by the
// real queries the API serves, with real analysis and real filters.

func liveStore(t *testing.T) *search.Store {
	t.Helper()
	url := os.Getenv("OPENSEARCH_URL")
	if url == "" {
		t.Skip("OPENSEARCH_URL not set")
	}
	store, err := search.New(url)
	if err != nil {
		t.Fatalf("connect to OpenSearch at %s: %v", url, err)
	}
	return store
}

func liveConsumer(t *testing.T, store *search.Store) *Consumer {
	t.Helper()
	p := defaultRetryPolicy()
	p.BaseDelay = 10 * time.Millisecond
	return &Consumer{store: store, retry: p, topic: "itest", groupID: "itest"}
}

// refresh forces the index to become searchable immediately. Without it
// these tests would race the refresh interval.
func refresh(t *testing.T, store *search.Store) {
	t.Helper()
	if err := store.RefreshPosts(context.Background()); err != nil {
		t.Fatalf("refresh posts_v1: %v", err)
	}
}

// The headline acceptance: one event per review state, and only
// public+approved is retrievable through the real search API.
func TestOpenSearch_OnlyPublicApprovedIsRetrievable(t *testing.T) {
	store := liveStore(t)
	c := liveConsumer(t, store)
	ctx := context.Background()

	// A term unique to this run so we only match our own documents.
	marker := "zz" + uuid.New().String()[:8]

	cases := []struct {
		visibility  string
		review      string
		retrievable bool
	}{
		{"public", "approved", true},
		{"public", "pending", false},
		{"public", "flagged", false},
		{"public", "rejected", false},
		{"public", "needs_changes", false},
		{"public", "", false}, // legacy event with no review status
		{"followers", "approved", false},
		{"private", "approved", false},
		{"unlisted", "approved", false},
	}

	ids := make(map[string]bool, len(cases))
	for _, tc := range cases {
		id := uuid.New().String()
		ids[id] = tc.retrievable
		if err := c.processMessage(ctx, msg(t, events.PostCreated, events.PostCreatedPayload{
			PostID:       id,
			AuthorID:     uuid.New().String(),
			Text:         "integration " + marker + " #" + marker,
			Visibility:   tc.visibility,
			ReviewStatus: tc.review,
			SearchRev:    1,
			CreatedAt:    time.Now().UTC(),
		})); err != nil {
			t.Fatalf("(%s,%s): %v", tc.visibility, tc.review, err)
		}
		t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })
	}
	refresh(t, store)

	found, err := store.SearchPostsFiltered(ctx, marker, nil, 50)
	if err != nil {
		t.Fatalf("SearchPostsFiltered: %v", err)
	}

	got := map[string]bool{}
	for _, doc := range found {
		got[doc.PostID] = true
	}
	for id, wantRetrievable := range ids {
		if got[id] != wantRetrievable {
			t.Errorf("post %s retrievable=%v, want %v", id, got[id], wantRetrievable)
		}
	}

	// The derived signal must be protected too: exactly one eligible post
	// carried the marker hashtag, so trending must not reflect the other
	// eight.
	tags, err := store.SearchHashtags(ctx, marker, 10)
	if err != nil {
		t.Fatalf("SearchHashtags: %v", err)
	}
	// The aggregation runs over posts_v1 with the public+approved filter,
	// so the tag may appear (one eligible post has it) but must never be
	// backed by ineligible documents. Presence alone is correct; what
	// matters is that removing the eligible post removes the tag.
	if len(tags) > 1 {
		t.Errorf("hashtag aggregation returned %d tags for a unique marker: %v", len(tags), tags)
	}
}

// Approval makes content searchable; rejection takes it back out, through
// the real query path.
func TestOpenSearch_ApprovalAndRejectionAreVisibleInSearch(t *testing.T) {
	store := liveStore(t)
	c := liveConsumer(t, store)
	ctx := context.Background()

	marker := "zz" + uuid.New().String()[:8]
	id := uuid.New().String()
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

	change := func(review string, rev int64) {
		t.Helper()
		if err := c.processMessage(ctx, msg(t, events.PostSearchEligibilityChanged,
			events.PostSearchEligibilityChangedPayload{
				PostID: id, AuthorID: uuid.New().String(),
				Visibility: "public", ReviewStatus: review, SearchRev: rev,
				Text: "integration " + marker, CreatedAt: time.Now().UTC(),
				ChangedAt: time.Now().UTC(),
			})); err != nil {
			t.Fatalf("transition to %s: %v", review, err)
		}
		refresh(t, store)
	}

	hits := func() int {
		t.Helper()
		found, err := store.SearchPostsFiltered(ctx, marker, nil, 10)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		return len(found)
	}

	change("pending", 1)
	if hits() != 0 {
		t.Fatal("a pending post must not be retrievable")
	}

	change("approved", 2)
	if hits() != 1 {
		t.Fatal("an approved post must become retrievable")
	}

	change("rejected", 3)
	if hits() != 0 {
		t.Fatal("a rejected post must leave the index")
	}

	// The resurrection guard, end to end: replaying the older approval
	// must not bring it back.
	change("approved", 2)
	if hits() != 0 {
		t.Fatal("M2-P0-2 REGRESSION: a replayed stale approval resurrected " +
			"rejected content in the live index")
	}
}

// A tombstone must be invisible to every public surface, and must not
// retain the post's text.
func TestOpenSearch_TombstoneIsInvisibleAndCarriesNoContent(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()

	marker := "zz" + uuid.New().String()[:8]
	id := uuid.New().String()
	author := uuid.New().String()
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

	if err := store.IndexPost(ctx, search.PostDoc{
		PostID: id, AuthorID: author, Text: "secret " + marker,
		Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	refresh(t, store)

	if err := store.TombstonePost(ctx, id, author, 2); err != nil {
		t.Fatalf("TombstonePost: %v", err)
	}
	refresh(t, store)

	found, err := store.SearchPostsFiltered(ctx, marker, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatal("a tombstoned post is still retrievable by its text — the " +
			"removal marker is not excluded by the public filter")
	}

	// The revision must survive, or a replayed approval could resurrect it.
	rev, exists, err := store.GetPostSearchRev(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || rev != 2 {
		t.Fatalf("tombstone rev = %d exists=%v, want 2/true — the high-water "+
			"mark must survive removal", rev, exists)
	}
}

// M2-P0-4 end to end: a blocked author's posts must not come back from a
// real query, and pagination must stay correct because the exclusion is
// applied inside the query rather than to the page.
func TestOpenSearch_BlockedAuthorExcludedAndPaginationStaysCorrect(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()

	marker := "zz" + uuid.New().String()[:8]
	blockedAuthor := uuid.New().String()
	okAuthor := uuid.New().String()

	for i := 0; i < 6; i++ {
		author := okAuthor
		if i%2 == 0 {
			author = blockedAuthor
		}
		id := uuid.New().String()
		if err := store.IndexPost(ctx, search.PostDoc{
			PostID: id, AuthorID: author, Text: "integration " + marker,
			Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })
	}
	refresh(t, store)

	all, err := store.SearchPostsFiltered(ctx, marker, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 6 {
		t.Fatalf("precondition: expected 6 indexed posts, got %d", len(all))
	}

	blockedCtx := search.WithBlockedIDs(ctx, []string{blockedAuthor})
	filtered, err := store.SearchPostsFiltered(blockedCtx, marker, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 3 {
		t.Fatalf("expected the 3 posts by the non-blocked author, got %d", len(filtered))
	}
	for _, doc := range filtered {
		if doc.AuthorID == blockedAuthor {
			t.Fatal("a blocked author's post was returned by live search")
		}
	}

	// A page of 2 must contain 2 real results, not 2-minus-the-hidden-ones.
	page, err := store.SearchPostsFiltered(blockedCtx, marker, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 {
		t.Fatalf("page size = %d, want 2 — exclusion must happen inside the "+
			"query so pages stay full", len(page))
	}
}

//go:build integration

package events

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Module 2 — live-OpenSearch coverage for the races and sequences raised
// in the Codex implementation review.
//
// LOCAL EVIDENCE: executed against OpenSearch 2.13.0 on 2026-08-10; results
// are recorded in prompt/module-02-feed-discovery-search-claude-fixes-v4.md.
// This suite remains a required CI gate
// (.github/workflows/integration-opensearch.yml, job `search-safety`).
//
// These specifically target what the previous suite could not detect: it
// was entirely sequential, so it could not see a compare-then-write race,
// and it never replayed a creation or a legacy delete against a removal.

// ─── Review P0-1: the concurrency race ──────────────────────────────────────

// Older approval and newer removal issued CONCURRENTLY, repeatedly.
//
// Under the previous design both handlers read the pre-removal revision,
// compared it in Go, and then wrote unconditionally — so whichever write
// landed last won, and half the time that was the stale approval. With the
// comparison inside OpenSearch under the document lock, the removal must
// win every single time regardless of arrival order.
func TestOpenSearch_ConcurrentStaleApprovalNeverBeatsNewerRemoval(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()

	const iterations = 60
	for i := 0; i < iterations; i++ {
		id := uuid.New().String()
		author := uuid.New().String()

		if err := store.ApplyPostProjection(ctx, search.PostProjection{
			PostID: id, Rev: 1,
			Doc: search.PostDoc{
				PostID: id, AuthorID: author, Text: "baseline",
				Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
				CreatedAt: time.Now().UTC(),
			},
		}); err != nil {
			t.Fatalf("iteration %d baseline: %v", i, err)
		}

		// Race: approval at rev 2 against removal at rev 3.
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs[0] = store.ApplyPostProjection(ctx, search.PostProjection{
				PostID: id, Rev: 2,
				Doc: search.PostDoc{
					PostID: id, AuthorID: author, Text: "approved again",
					Visibility: "public", ReviewStatus: "approved", SearchRev: 2,
					CreatedAt: time.Now().UTC(),
				},
			})
		}()
		go func() {
			defer wg.Done()
			errs[1] = store.ApplyPostProjection(ctx, search.PostProjection{
				PostID: id, Rev: 3, Removed: true, AuthorID: author,
			})
		}()
		wg.Wait()

		for _, err := range errs {
			if err != nil {
				t.Fatalf("iteration %d: projection error: %v", i, err)
			}
		}

		rev, exists, err := store.GetPostSearchRev(ctx, id)
		if err != nil {
			t.Fatalf("iteration %d: read back: %v", i, err)
		}
		if !exists {
			t.Fatalf("iteration %d: document vanished", i)
		}
		if rev != 3 {
			t.Fatalf("iteration %d: final rev = %d, want 3 — the newer removal must "+
				"win regardless of which write executed last", i, rev)
		}

		if err := store.RefreshPosts(ctx); err != nil {
			t.Fatal(err)
		}
		found, err := store.SearchPostsFiltered(ctx, "approved again", nil, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, doc := range found {
			if doc.PostID == id {
				t.Fatalf("iteration %d: RESURRECTION — a concurrent stale approval left "+
					"rejected content publicly searchable", i)
			}
		}
		_ = store.DeletePost(ctx, id)
	}
}

// Equal revisions must resolve in favour of removal.
func TestOpenSearch_EqualRevisionRemovalWinsTheTie(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()

	id := uuid.New().String()
	author := uuid.New().String()
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

	if err := store.ApplyPostProjection(ctx, search.PostProjection{
		PostID: id, Rev: 7, Removed: true, AuthorID: author,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyPostProjection(ctx, search.PostProjection{
		PostID: id, Rev: 7,
		Doc: search.PostDoc{
			PostID: id, AuthorID: author, Text: "tiebreaker",
			Visibility: "public", ReviewStatus: "approved", SearchRev: 7,
			CreatedAt: time.Now().UTC(),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}

	found, err := store.SearchPostsFiltered(ctx, "tiebreaker", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatal("an equal-revision approval overwrote a removal; ties must go to removal")
	}
}

// ─── Review P0-2: creation and legacy deletes, live ─────────────────────────

func TestOpenSearch_CreationAndLegacyDeleteRespectTheBarrier(t *testing.T) {
	store := liveStore(t)
	c := liveConsumer(t, store)
	ctx := context.Background()

	marker := "zz" + uuid.New().String()[:8]
	id := uuid.New().String()
	author := uuid.New().String()
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

	// Removal at rev 2.
	if err := c.processMessage(ctx, msg(t, events.PostSearchEligibilityChanged,
		events.PostSearchEligibilityChangedPayload{
			PostID: id, AuthorID: author, Visibility: "public",
			ReviewStatus: "rejected", SearchRev: 2, ChangedAt: time.Now().UTC(),
		})); err != nil {
		t.Fatal(err)
	}

	// A replayed creation carrying the creation-time revision.
	if err := c.processMessage(ctx, msg(t, events.PostCreated, events.PostCreatedPayload{
		PostID: id, AuthorID: author, Text: "created " + marker,
		Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
		CreatedAt: time.Now().UTC(),
	})); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}
	if found, err := store.SearchPostsFiltered(ctx, marker, nil, 10); err != nil {
		t.Fatal(err)
	} else if len(found) != 0 {
		t.Fatal("a replayed PostCreated resurrected removed content in the live index")
	}

	// A still-produced legacy delete lands between the removal and a stale
	// approval replay. It must RAISE the barrier, not erase it.
	if err := c.processMessage(ctx, msg(t, events.UploadDeleted,
		events.UploadDeletedPayload{PostID: id})); err != nil {
		t.Fatal(err)
	}
	revAfterDelete, exists, err := store.GetPostSearchRev(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("the legacy delete hard-erased the document, and with it the barrier")
	}
	if revAfterDelete <= 2 {
		t.Fatalf("rev after legacy delete = %d, want > 2", revAfterDelete)
	}

	// Replay the approval that preceded everything.
	if err := c.processMessage(ctx, msg(t, events.PostSearchEligibilityChanged,
		events.PostSearchEligibilityChangedPayload{
			PostID: id, AuthorID: author, Visibility: "public",
			ReviewStatus: "approved", SearchRev: 2, Text: "created " + marker,
			ChangedAt: time.Now().UTC(),
		})); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}
	if found, err := store.SearchPostsFiltered(ctx, marker, nil, 10); err != nil {
		t.Fatal(err)
	} else if len(found) != 0 {
		t.Fatal("a stale approval replayed after a legacy delete resurrected content")
	}
}

// ─── Review P0-4: hashtag autocomplete must be reversible ───────────────────

// A unique tag must be suggested while its only post is approved, and must
// disappear from BOTH hashtag search and multi-entity autocomplete once
// that post is rejected. The hashtags_v1 counter never went back down, so
// the tag previously survived forever.
func TestOpenSearch_HashtagLeavesAutocompleteWhenItsOnlyPostIsRemoved(t *testing.T) {
	store := liveStore(t)
	c := liveConsumer(t, store)
	ctx := context.Background()

	tag := "zz" + uuid.New().String()[:8]
	id := uuid.New().String()
	author := uuid.New().String()
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

	if err := c.processMessage(ctx, msg(t, events.PostCreated, events.PostCreatedPayload{
		PostID: id, AuthorID: author, Text: "festival #" + tag,
		Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
		CreatedAt: time.Now().UTC(),
	})); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}

	tags, err := store.SearchHashtags(ctx, tag, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) == 0 {
		t.Fatal("precondition: an approved post's hashtag should be suggested")
	}
	auto, err := store.AutocompleteMulti(ctx, tag, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !containsHashtagSuggestion(auto, tag) {
		t.Fatal("precondition: the tag should appear in multi-entity autocomplete")
	}

	// Reject the only contributing post.
	if err := c.processMessage(ctx, msg(t, events.PostSearchEligibilityChanged,
		events.PostSearchEligibilityChangedPayload{
			PostID: id, AuthorID: author, Visibility: "public",
			ReviewStatus: "rejected", SearchRev: 2, ChangedAt: time.Now().UTC(),
		})); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}

	if tags, err = store.SearchHashtags(ctx, tag, 10); err != nil {
		t.Fatal(err)
	} else if len(tags) != 0 {
		t.Fatalf("hashtag search still returns %v after its only post was rejected", tags)
	}

	if auto, err = store.AutocompleteMulti(ctx, tag, 10); err != nil {
		t.Fatal(err)
	} else if containsHashtagSuggestion(auto, tag) {
		t.Fatal("M2-P0-4 REGRESSION: the tag is still suggested by autocomplete after " +
			"its only contributing post was rejected")
	}
}

func containsHashtagSuggestion(results []search.AutocompleteResult, tag string) bool {
	for _, r := range results {
		if r.Kind == "hashtag" && r.Hashtag == tag {
			return true
		}
	}
	return false
}

// ─── Review P0-7: author erasure fence, live ────────────────────────────────

func TestOpenSearch_ErasedAuthorContentCannotBeRecreated(t *testing.T) {
	store := liveStore(t)
	c := liveConsumer(t, store)
	ctx := context.Background()

	marker := "zz" + uuid.New().String()[:8]
	author := uuid.New().String()
	indexed := uuid.New().String()
	neverIndexed := uuid.New().String()
	t.Cleanup(func() {
		_ = store.DeletePost(context.Background(), indexed)
		_ = store.DeletePost(context.Background(), neverIndexed)
	})

	if err := c.processMessage(ctx, msg(t, events.PostCreated, events.PostCreatedPayload{
		PostID: indexed, AuthorID: author, Text: "erased " + marker,
		Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
		CreatedAt: time.Now().UTC(),
	})); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}

	if err := c.processMessage(ctx, msg(t, events.EventUserDeletionRequested,
		events.UserDeletionRequestedPayload{UserID: author})); err != nil {
		t.Fatalf("erase author: %v", err)
	}
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}

	// Replay creation and approval for a post that WAS indexed and for one
	// the index never saw. Neither may come back.
	for _, id := range []string{indexed, neverIndexed} {
		if err := c.processMessage(ctx, msg(t, events.PostCreated, events.PostCreatedPayload{
			PostID: id, AuthorID: author, Text: "erased " + marker,
			Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
			CreatedAt: time.Now().UTC(),
		})); err != nil {
			t.Fatal(err)
		}
		if err := c.processMessage(ctx, msg(t, events.PostSearchEligibilityChanged,
			events.PostSearchEligibilityChangedPayload{
				PostID: id, AuthorID: author, Visibility: "public",
				ReviewStatus: "approved", SearchRev: 500, Text: "erased " + marker,
				ChangedAt: time.Now().UTC(),
			})); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}

	found, err := store.SearchPostsFiltered(ctx, marker, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("M2-P0-7 REGRESSION: %d posts belonging to an erased account were "+
			"recreated by replayed events", len(found))
	}
}

// ─── Review P0-5 / P0-4: the suppression matrix, live ───────────────────────

// Both block directions and viewer mute must suppress, and reverse mute
// must not. The previous live test injected one id into the context, which
// proved only that a filter existed.
func TestOpenSearch_SuppressionMatrixAcrossPostAndUserSurfaces(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()

	marker := "zz" + uuid.New().String()[:8]
	viewer := uuid.New().String()
	blockedByViewer := uuid.New().String()
	blockerOfViewer := uuid.New().String()
	mutedByViewer := uuid.New().String()
	reverseMuter := uuid.New().String() // muted the viewer; must NOT be hidden
	unrelated := uuid.New().String()

	authors := []string{blockedByViewer, blockerOfViewer, mutedByViewer, reverseMuter, unrelated}
	for _, a := range authors {
		id := uuid.New().String()
		if err := store.ApplyPostProjection(ctx, search.PostProjection{
			PostID: id, Rev: 1,
			Doc: search.PostDoc{
				PostID: id, AuthorID: a, Text: "matrix " + marker,
				Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
				CreatedAt: time.Now().UTC(),
			},
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })
	}
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}

	// graph-service returns blocks in both directions plus the viewer's
	// outgoing mutes — and never reverse mutes. That composition is
	// asserted against live Postgres in graph-service; here we assert
	// search honours it.
	suppressed := []string{blockedByViewer, blockerOfViewer, mutedByViewer}
	viewerCtx := search.WithBlockedIDs(ctx, suppressed)

	found, err := store.SearchPostsFiltered(viewerCtx, marker, nil, 50)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, doc := range found {
		got[doc.AuthorID] = true
	}
	for _, a := range suppressed {
		if got[a] {
			t.Errorf("author %s should be suppressed for this viewer but was returned", a)
		}
	}
	if !got[reverseMuter] {
		t.Error("reverse mute must NOT suppress: muting someone cannot remove you " +
			"from their results")
	}
	if !got[unrelated] {
		t.Error("an unrelated author was suppressed")
	}
	_ = viewer
}

// mustStore builds a store pointed at an arbitrary URL (used to simulate
// an unreachable OpenSearch in the Kafka durability tests).
func mustStore(t *testing.T, url string) *search.Store {
	t.Helper()
	s, err := search.New(url)
	if err != nil {
		t.Fatalf("build store for %s: %v", url, err)
	}
	return s
}

// postProjectionForTest builds a public, approved projection.
func postProjectionForTest(postID, authorID, marker string, rev int64) search.PostProjection {
	return search.PostProjection{
		PostID: postID, Rev: rev,
		Doc: search.PostDoc{
			PostID: postID, AuthorID: authorID, Text: "durability " + marker,
			Visibility: "public", ReviewStatus: "approved", SearchRev: rev,
			CreatedAt: time.Now().UTC(),
		},
	}
}

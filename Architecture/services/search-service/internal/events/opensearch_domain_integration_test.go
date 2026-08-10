//go:build integration

package events

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Re-review §9 — live coverage for the revision domain, the missing-document
// script cases, and the author-fence race.
//
// LOCAL EVIDENCE: executed against OpenSearch 2.13.0 on 2026-08-10; results
// are recorded in prompt/module-02-feed-discovery-search-claude-fixes-v4.md.
// This suite remains a required CI gate
// (.github/workflows/integration-opensearch.yml, job `search-safety`).
//
// Everything here exists because the Go fake reimplements the Painless
// rather than executing it. These are the cases where "my Go mirror agrees
// with my Go code" proves nothing at all.

// ─── §9: missing-document script behaviour ──────────────────────────────────

// The four missing-document cases from the review, checked against both
// the update response and the resulting document.
func TestOpenSearch_MissingDocumentProjectionCases(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()

	t.Run("eligible_rev_1_creates_document", func(t *testing.T) {
		id := uuid.New().String()
		t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

		if err := store.ApplyPostProjection(ctx, search.PostProjection{
			PostID: id, Rev: 1,
			Doc: search.PostDoc{
				PostID: id, AuthorID: uuid.New().String(), Text: "created",
				Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
				CreatedAt: time.Now().UTC(),
			},
		}); err != nil {
			t.Fatal(err)
		}
		rev, exists, err := store.GetPostSearchRev(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !exists || rev != 1 {
			t.Fatalf("rev=%d exists=%v, want 1/true", rev, exists)
		}
	})

	t.Run("autorev_removal_creates_tombstone_at_1", func(t *testing.T) {
		id := uuid.New().String()
		t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

		if err := store.ApplyPostProjection(ctx, search.PostProjection{
			PostID: id, AutoRev: true, Removed: true, AuthorID: uuid.New().String(),
		}); err != nil {
			t.Fatal(err)
		}
		rev, exists, err := store.GetPostSearchRev(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !exists || rev != 1 {
			t.Fatalf("rev=%d exists=%v, want 1/true (stored 0 + 1)", rev, exists)
		}
	})

	t.Run("explicit_removal_rev_0_wins_the_equal_tie", func(t *testing.T) {
		id := uuid.New().String()
		t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

		// stored is 0 for a missing document, so rev 0 is an equal-revision
		// removal and must apply.
		if err := store.ApplyPostProjection(ctx, search.PostProjection{
			PostID: id, Rev: 0, Removed: true, AuthorID: uuid.New().String(),
		}); err != nil {
			t.Fatal(err)
		}
		_, exists, err := store.GetPostSearchRev(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatal("an equal-revision removal on a missing document must create a tombstone")
		}
	})

	t.Run("eligible_rev_0_is_refused_and_creates_nothing", func(t *testing.T) {
		id := uuid.New().String()
		t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

		err := store.ApplyPostProjection(ctx, search.PostProjection{
			PostID: id, Rev: 0,
			Doc: search.PostDoc{PostID: id, Visibility: "public", ReviewStatus: "approved"},
		})
		if !errors.Is(err, search.ErrRevisionOutOfDomain) {
			t.Fatalf("expected ErrRevisionOutOfDomain, got %v", err)
		}
		if _, exists, err := store.GetPostSearchRev(ctx, id); err != nil {
			t.Fatal(err)
		} else if exists {
			t.Fatal("a refused projection must not create an empty document")
		}
	})
}

// ─── §9: the revision domain boundary ───────────────────────────────────────

// fenceRev-1 is ordinary and must work; fenceRev, fenceRev+1 and MaxInt64
// are reserved and must fail closed for eligible writes.
func TestOpenSearch_RevisionDomainBoundaryFailsClosed(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()
	fence := search.FenceRevision()

	t.Run("just_below_the_fence_is_ordinary", func(t *testing.T) {
		id := uuid.New().String()
		t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })
		if err := store.ApplyPostProjection(ctx, search.PostProjection{
			PostID: id, Rev: fence - 1,
			Doc: search.PostDoc{
				PostID: id, AuthorID: uuid.New().String(), Text: "boundary",
				Visibility: "public", ReviewStatus: "approved", SearchRev: fence - 1,
				CreatedAt: time.Now().UTC(),
			},
		}); err != nil {
			t.Fatalf("fenceRev-1 must be a valid ordinary revision: %v", err)
		}
	})

	for name, rev := range map[string]int64{
		"at_the_fence":    fence,
		"above_the_fence": fence + 1,
		"max_int64":       math.MaxInt64,
	} {
		t.Run(name+"_eligible_is_refused", func(t *testing.T) {
			id := uuid.New().String()
			t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

			// Erase this post first, then try to resurrect it from above
			// the fence. This is the attack the domain exists to stop.
			if err := store.FencePostForErasedAuthor(ctx, id, uuid.New().String()); err != nil {
				t.Fatal(err)
			}
			err := store.ApplyPostProjection(ctx, search.PostProjection{
				PostID: id, Rev: rev,
				Doc: search.PostDoc{
					PostID: id, AuthorID: uuid.New().String(), Text: "resurrect",
					Visibility: "public", ReviewStatus: "approved", SearchRev: rev,
					CreatedAt: time.Now().UTC(),
				},
			})
			if err == nil {
				t.Fatalf("revision %d was accepted for an eligible write", rev)
			}
			if err := store.RefreshPosts(ctx); err != nil {
				t.Fatal(err)
			}
			found, ferr := store.SearchPostsFiltered(ctx, "resurrect", nil, 10)
			if ferr != nil {
				t.Fatal(ferr)
			}
			for _, doc := range found {
				if doc.PostID == id {
					t.Fatalf("revision %d overwrote a permanent erasure tombstone", rev)
				}
			}
		})
	}
}

// AutoRev on a document at the reserved boundary must not overflow into a
// negative revision, which would turn a safety removal into a no-op.
func TestOpenSearch_AutoRevDoesNotOverflowOnFencedDocument(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()

	id := uuid.New().String()
	author := uuid.New().String()
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

	if err := store.FencePostForErasedAuthor(ctx, id, author); err != nil {
		t.Fatal(err)
	}

	// A takedown arriving after erasure. It must not wrap the revision.
	if err := store.ApplyPostProjection(ctx, search.PostProjection{
		PostID: id, AutoRev: true, Removed: true,
	}); err != nil {
		t.Fatalf("AutoRev removal on a fenced document must not error: %v", err)
	}

	rev, exists, err := store.GetPostSearchRev(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("the document disappeared")
	}
	if rev < 0 {
		t.Fatalf("AutoRev overflowed to %d — a later safety removal would now "+
			"be treated as stale and become a no-op", rev)
	}
	if rev < search.FenceRevision() {
		t.Fatalf("rev = %d dropped below the fence; the erasure barrier was weakened", rev)
	}
}

// author_erased must be absolute — and the STICKY MARKER, not the numeric
// comparison, must be what decides.
//
// Re-review v2 §5: the previous version of this test stamped the erasure
// at fenceRev and then applied an approval at revision 5. Five is lower
// than fenceRev, so `incoming < stored` rejected it on its own; deleting
// the author_erased guard entirely would not have failed the test. Its
// intervening AutoRev removal was also an equal-revision no-op, because
// the stored document was already removed.
//
// This version puts the erasure marker at a LOW ordinary revision, so
// every later write is numerically NEWER and would apply if the marker
// were not consulted. Only the sticky-marker branch can produce the
// expected result.
func TestOpenSearch_ErasedMarkerRejectsNumericallyNewerApprovals(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()

	marker := "zz" + uuid.New().String()[:8]
	id := uuid.New().String()
	author := uuid.New().String()
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

	// Erasure recorded at revision 5 — deliberately inside the ordinary
	// domain, so it cannot win on magnitude.
	if err := store.ApplyPostProjection(ctx, search.PostProjection{
		PostID: id, Rev: 5, Removed: true, AuthorID: author, AuthorErased: true,
	}); err != nil {
		t.Fatalf("seed erasure: %v", err)
	}

	// A NUMERICALLY NEWER approval. incoming(10) > stored(5), so the
	// revision comparison alone would apply it. Only author_erased stops it.
	if err := store.ApplyPostProjection(ctx, search.PostProjection{
		PostID: id, Rev: 10,
		Doc: search.PostDoc{
			PostID: id, AuthorID: author, Text: "back " + marker,
			Visibility: "public", ReviewStatus: "approved", SearchRev: 10,
			CreatedAt: time.Now().UTC(),
		},
	}); err != nil {
		t.Fatalf("the write should be accepted by Go and rejected by the script: %v", err)
	}
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}
	if found, err := store.SearchPostsFiltered(ctx, marker, nil, 10); err != nil {
		t.Fatal(err)
	} else if len(found) != 0 {
		t.Fatal("RE-REVIEW v2 REGRESSION: a numerically newer approval resurrected an " +
			"erased post. The author_erased guard is not deciding the outcome")
	}
	if rev, _, err := store.GetPostSearchRev(ctx, id); err != nil {
		t.Fatal(err)
	} else if rev != 5 {
		t.Fatalf("rev = %d, want 5: a rejected write must not advance the revision", rev)
	}

	// The marker must survive a removal that ACTUALLY APPLIES (rev 20 > 5),
	// not merely a no-op one.
	if err := store.ApplyPostProjection(ctx, search.PostProjection{
		PostID: id, Rev: 20, Removed: true, AuthorID: author,
	}); err != nil {
		t.Fatal(err)
	}
	if rev, _, err := store.GetPostSearchRev(ctx, id); err != nil {
		t.Fatal(err)
	} else if rev != 20 {
		t.Fatalf("rev = %d, want 20: the later removal should have applied", rev)
	}

	// And a still-newer approval must STILL be refused, proving the marker
	// was carried through that applied removal rather than dropped.
	if err := store.ApplyPostProjection(ctx, search.PostProjection{
		PostID: id, Rev: 30,
		Doc: search.PostDoc{
			PostID: id, AuthorID: author, Text: "back again " + marker,
			Visibility: "public", ReviewStatus: "approved", SearchRev: 30,
			CreatedAt: time.Now().UTC(),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}
	if found, err := store.SearchPostsFiltered(ctx, marker, nil, 10); err != nil {
		t.Fatal(err)
	} else if len(found) != 0 {
		t.Fatal("the erasure marker was lost when a later removal applied; a " +
			"subsequent approval then resurrected the post")
	}
}

// ─── §9: the author fence check/sweep race ──────────────────────────────────

// The interleaving the sequential test could not see: a writer that passes
// its fence check while the eraser is running must not leave a public
// document behind, whether or not the post existed when the sweep ran.
func TestOpenSearch_ConcurrentWriterCannotSurviveAuthorErasure(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()

	const iterations = 25
	for i := 0; i < iterations; i++ {
		author := uuid.New().String()
		marker := "zz" + uuid.New().String()[:8]

		// A post that already exists (sweep must catch it) and one that
		// does not yet exist (only the recheck can catch it).
		existing := uuid.New().String()
		fresh := uuid.New().String()

		if err := store.IndexPostUnlessAuthorErased(ctx, search.PostProjection{
			PostID: existing, Rev: 1,
			Doc: search.PostDoc{
				PostID: existing, AuthorID: author, Text: "race " + marker,
				Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
				CreatedAt: time.Now().UTC(),
			},
		}); err != nil {
			t.Fatalf("iteration %d seed: %v", i, err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		var writerErr, eraseErr error
		go func() {
			defer wg.Done()
			writerErr = store.IndexPostUnlessAuthorErased(ctx, search.PostProjection{
				PostID: fresh, Rev: 1,
				Doc: search.PostDoc{
					PostID: fresh, AuthorID: author, Text: "race " + marker,
					Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
					CreatedAt: time.Now().UTC(),
				},
			})
		}()
		go func() {
			defer wg.Done()
			eraseErr = store.EraseAuthorContent(ctx, author)
		}()
		wg.Wait()

		if eraseErr != nil {
			t.Fatalf("iteration %d: erase failed: %v", i, eraseErr)
		}
		if writerErr != nil {
			t.Logf("iteration %d: writer errored (acceptable, it fails closed): %v", i, writerErr)
		}

		if err := store.RefreshPosts(ctx); err != nil {
			t.Fatal(err)
		}
		found, err := store.SearchPostsFiltered(ctx, marker, nil, 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 0 {
			t.Fatalf("iteration %d: %d public documents survived account erasure — "+
				"the fence check/sweep race is still open", i, len(found))
		}

		_ = store.DeletePost(ctx, existing)
		_ = store.DeletePost(ctx, fresh)
	}
}

// A sweep that reports version conflicts must be retried, not accepted.
// Driving a real conflict deterministically is impractical, so this asserts
// the observable guarantee instead: concurrent projection churn during
// erasure still ends with nothing public.
func TestOpenSearch_SweepSurvivesConcurrentProjectionChurn(t *testing.T) {
	store := liveStore(t)
	ctx := context.Background()

	author := uuid.New().String()
	marker := "zz" + uuid.New().String()[:8]

	ids := make([]string, 8)
	for i := range ids {
		ids[i] = uuid.New().String()
		if err := store.ApplyPostProjection(ctx, search.PostProjection{
			PostID: ids[i], Rev: 1,
			Doc: search.PostDoc{
				PostID: ids[i], AuthorID: author, Text: "churn " + marker,
				Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
				CreatedAt: time.Now().UTC(),
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, id := range ids {
			_ = store.DeletePost(context.Background(), id)
		}
	})
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}

	// Hammer the same documents while the sweep runs, to provoke conflicts.
	stop := make(chan struct{})
	var churn sync.WaitGroup
	churn.Add(1)
	go func() {
		defer churn.Done()
		rev := int64(2)
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, id := range ids {
				_ = store.ApplyPostProjection(ctx, search.PostProjection{
					PostID: id, Rev: rev,
					Doc: search.PostDoc{
						PostID: id, AuthorID: author, Text: "churn " + marker,
						Visibility: "public", ReviewStatus: "approved", SearchRev: rev,
						CreatedAt: time.Now().UTC(),
					},
				})
			}
			rev++
		}
	}()

	eraseErr := store.EraseAuthorContent(ctx, author)
	// Stop the churn BEFORE asserting, but do NOT run another sweep.
	//
	// Re-review v2 P0-3: the earlier version ran a second clean
	// SweepAuthorPosts here and only then asserted. That sweep could
	// repair whatever the first one missed, so the test passed whether or
	// not the response was ever inspected. The assertion now observes the
	// state EraseAuthorContent left behind, with nothing in between.
	close(stop)
	churn.Wait()

	// Under UNBOUNDED concurrent writes the sweep may legitimately never
	// win, and returning an error is the correct outcome: the consumer
	// then refuses to commit and Kafka redelivers the deletion. The thing
	// that must never happen is erasure reporting SUCCESS while public
	// documents survive.
	if eraseErr != nil {
		t.Logf("erasure returned an error under unbounded churn (correct fail-closed "+
			"behaviour; the deletion will be redelivered): %v", eraseErr)
		return
	}

	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}

	// Documents written by the churn AFTER erasure returned are not this
	// test's subject — in production the consumer's fence recheck handles
	// those, and TestOpenSearch_ConcurrentWriterCannotSurviveAuthorErasure
	// covers it. What must hold here is that everything present when the
	// sweep ran was swept, conflicts included. Every surviving document
	// must therefore carry the erasure marker.
	found, err := store.SearchPostsFiltered(ctx, marker, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("%d public documents survived EraseAuthorContent under concurrent "+
			"churn; version_conflicts were accepted instead of retried", len(found))
	}
	for _, id := range ids {
		rev, exists, err := store.GetPostSearchRev(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("post %s vanished instead of being tombstoned", id)
		}
		if rev < search.FenceRevision() {
			t.Fatalf("post %s is at rev %d, below the fence: the sweep did not reach "+
				"it and the conflict was silently accepted", id, rev)
		}
	}
}

// Sanity: the erasure path still works through the consumer end to end.
func TestOpenSearch_ConsumerErasureStillRemovesEverything(t *testing.T) {
	store := liveStore(t)
	c := liveConsumer(t, store)
	ctx := context.Background()

	author := uuid.New().String()
	marker := "zz" + uuid.New().String()[:8]
	id := uuid.New().String()
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), id) })

	if err := c.processMessage(ctx, msg(t, events.PostCreated, events.PostCreatedPayload{
		PostID: id, AuthorID: author, Text: "consumer " + marker,
		Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
		CreatedAt: time.Now().UTC(),
	})); err != nil {
		t.Fatal(err)
	}
	if err := c.processMessage(ctx, msg(t, events.EventUserDeletionRequested,
		events.UserDeletionRequestedPayload{UserID: author})); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshPosts(ctx); err != nil {
		t.Fatal(err)
	}
	found, err := store.SearchPostsFiltered(ctx, marker, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatal("erasure through the consumer left public content behind")
	}
}

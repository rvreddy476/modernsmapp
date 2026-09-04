package events

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/shared/events"
)

// Soft delete → restore (2026-09-04).
//
// post-service emits PostDeleted and PostSearchEligibilityChanged(deleted)
// in ONE transaction with the SAME search_rev, and a restore is that rev+1.
// The consumer must apply PostDeleted at the canonical revision: an
// AutoRev barrier lands one above it and the restore would be dropped as
// stale, leaving a restored post permanently missing from search.
func TestSoftDeleteThenRestore_ReindexesAtTheCanonicalRevision(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	// Live, approved, indexed at rev 3.
	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 3)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") {
		t.Fatal("precondition: indexed at rev 3")
	}

	// Soft delete: both events at rev 4, in either order.
	if err := c.processMessage(ctx, msg(t, events.PostDeleted, events.PostDeletedPayload{
		PostID: "p1", AuthorID: "author-1", DeletedAt: time.Now().UTC(), SearchRev: 4,
	})); err != nil {
		t.Fatal(err)
	}
	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", true, 4)); err != nil {
		t.Fatal(err)
	}
	if f.indexed("p1") {
		t.Fatal("soft-deleted post is still searchable")
	}
	if !f.exists("p1") {
		t.Fatal("soft delete hard-erased the document and its revision barrier")
	}
	if got := f.rev("p1"); got != 4 {
		t.Fatalf("rev after soft delete = %d, want the canonical 4 (an auto barrier would block the restore)", got)
	}

	// Restore within the window: canonical rev 5 re-indexes the body.
	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 5)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") {
		t.Fatal("restored post did not come back into search — the delete barrier was raised past the canonical revision")
	}
	if got := f.rev("p1"); got != 5 {
		t.Fatalf("rev after restore = %d, want 5", got)
	}

	// PostPurged is terminal: removed, and the barrier goes above anything
	// the soft-delete lifecycle could still replay.
	if err := c.processMessage(ctx, msg(t, events.PostPurged, events.PostPurgedPayload{
		PostID: "p1", AuthorID: "author-1", PurgedAt: time.Now().UTC(),
	})); err != nil {
		t.Fatal(err)
	}
	if f.indexed("p1") {
		t.Fatal("purged post is still searchable")
	}
	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 5)); err != nil {
		t.Fatal(err)
	}
	if f.indexed("p1") {
		t.Fatal("a replayed restore resurrected a purged post")
	}
}

// A legacy PostDeleted with no search_rev keeps the AutoRev barrier.
func TestPostDeleted_WithoutRevStillRaisesABarrier(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()
	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 2)); err != nil {
		t.Fatal(err)
	}
	if err := c.processMessage(ctx, msg(t, events.PostDeleted, events.PostDeletedPayload{PostID: "p1"})); err != nil {
		t.Fatal(err)
	}
	if f.indexed("p1") || f.rev("p1") <= 2 {
		t.Fatalf("legacy delete: indexed=%v rev=%d, want removed at rev > 2", f.indexed("p1"), f.rev("p1"))
	}
}

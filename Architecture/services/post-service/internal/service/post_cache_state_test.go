package service

import (
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Body-cache revalidation (P-8 d): every field a read gate consults for "may
// this be seen at all" is overwritten from the canonical row on a cache hit.
// A body cached while the post was live must come back scheduled when the
// row was rescheduled after the cache drop was lost — and vice versa.
func TestApplyPostAccessStateCoversEveryRevocableField(t *testing.T) {
	cached := &postgres.Post{
		ID: uuid.New(), Visibility: "public", ReviewStatus: "approved",
		AgeRestricted: false, PublishAt: nil, IsScheduled: false,
	}
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	applyPostAccessState(cached, &postgres.PostAccessState{
		ReviewStatus: "flagged", Visibility: "private", AgeRestricted: true, PublishAt: &at,
	})
	if cached.ReviewStatus != "flagged" {
		t.Errorf("review status not revalidated: %q", cached.ReviewStatus)
	}
	if cached.Visibility != "private" {
		t.Errorf("visibility not revalidated: %q", cached.Visibility)
	}
	if !cached.AgeRestricted {
		t.Error("age_restricted not revalidated")
	}
	if cached.PublishAt == nil || !cached.PublishAt.Equal(at) {
		t.Errorf("publish_at not revalidated: %v", cached.PublishAt)
	}
	if !cached.IsScheduled {
		t.Error("is_scheduled not derived from the canonical publish_at")
	}

	// The other direction: a body cached while scheduled reads live once the
	// worker has cleared publish_at.
	stale := &postgres.Post{ID: uuid.New(), Visibility: "public", ReviewStatus: "approved", PublishAt: &at, IsScheduled: true}
	applyPostAccessState(stale, &postgres.PostAccessState{ReviewStatus: "approved", Visibility: "public"})
	if stale.PublishAt != nil || stale.IsScheduled {
		t.Errorf("a published post stayed scheduled from the cache: %v %v", stale.PublishAt, stale.IsScheduled)
	}
}

// The revalidated body is what hiddenWhileScheduled reads, so a stale
// "live" cache entry cannot show a rescheduled post to a stranger.
func TestRevalidatedScheduleHidesTheCachedBody(t *testing.T) {
	author, stranger := uuid.New(), uuid.New()
	cached := &postgres.Post{ID: uuid.New(), AuthorID: author, Visibility: "public", ReviewStatus: "approved"}
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	applyPostAccessState(cached, &postgres.PostAccessState{ReviewStatus: "approved", Visibility: "public", PublishAt: &at})
	if !hiddenFromViewer(cached, &stranger) {
		t.Fatal("a rescheduled post read as live from the cache")
	}
	if hiddenFromViewer(cached, &author) {
		t.Fatal("the author lost their own scheduled post")
	}
}

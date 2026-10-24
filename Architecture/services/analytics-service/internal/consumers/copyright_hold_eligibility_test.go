package consumers

import (
	"context"
	"testing"
	"time"

	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Copyright quick wins (2026-10-24): a held or blocked post earns nothing
// for the held period, and a cleared one earns from the clear onward.

// A copyright hold arrives as PostSearchEligibilityChanged with the
// EFFECTIVE status "restricted": ineligible from the moment of the hold.
func TestCopyrightHoldMakesContentIneligible(t *testing.T) {
	store := &recordingStore{}
	c := NewContentOwnershipConsumer(store)
	post, author := uuid.New(), uuid.New()
	heldAt := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	if err := c.apply(context.Background(), envelopeFor(t, events.PostSearchEligibilityChanged, events.PostSearchEligibilityChangedPayload{
		PostID: post.String(), AuthorID: author.String(), Visibility: "public", ReviewStatus: "restricted",
		BaseReviewStatus: "approved", Restricted: true, SearchRev: 4, ChangedAt: heldAt,
	})); err != nil {
		t.Fatal(err)
	}
	if len(store.eligibility) != 1 || store.eligibility[0].State != pgstore.EligibilityIneligible || !store.eligibility[0].EffectiveFrom.Equal(heldAt) {
		t.Fatalf("hold: %+v, want ineligible from %v", store.eligibility, heldAt)
	}
	clearedAt := heldAt.Add(72 * time.Hour)
	if err := c.apply(context.Background(), envelopeFor(t, events.PostSearchEligibilityChanged, events.PostSearchEligibilityChangedPayload{
		PostID: post.String(), AuthorID: author.String(), Visibility: "public", ReviewStatus: "approved",
		BaseReviewStatus: "approved", SearchRev: 5, ChangedAt: clearedAt,
	})); err != nil {
		t.Fatal(err)
	}
	if len(store.eligibility) != 2 || store.eligibility[1].State != pgstore.EligibilityEligible || !store.eligibility[1].EffectiveFrom.Equal(clearedAt) {
		t.Fatalf("clear: %+v, want eligible from %v (the store sets the floor on the transition)", store.eligibility, clearedAt)
	}
}

// The PostCreated withheld through a title-review hold carries
// eligible_from: the ownership row starts with that floor. An ordinary
// PostCreated has none.
func TestDeferredPostCreatedCarriesTheFloor(t *testing.T) {
	store := &recordingStore{}
	c := NewContentOwnershipConsumer(store)
	created := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	cleared := created.Add(48 * time.Hour)
	if err := c.apply(context.Background(), envelopeFor(t, events.PostCreated, events.PostCreatedPayload{
		PostID: uuid.NewString(), AuthorID: uuid.NewString(), ContentType: "long_video", CreatedAt: created, EligibleFrom: &cleared,
	})); err != nil {
		t.Fatal(err)
	}
	if err := c.apply(context.Background(), envelopeFor(t, events.PostCreated, events.PostCreatedPayload{
		PostID: uuid.NewString(), AuthorID: uuid.NewString(), ContentType: "long_video", CreatedAt: created,
	})); err != nil {
		t.Fatal(err)
	}
	if len(store.upserts) != 2 {
		t.Fatalf("upserts = %d", len(store.upserts))
	}
	if f := store.upserts[0].EligibleFrom; f == nil || !f.Equal(cleared) || !store.upserts[0].CreatedAt.Equal(created) {
		t.Fatalf("deferred: eligible_from=%v created=%v", f, store.upserts[0].CreatedAt)
	}
	if store.upserts[1].EligibleFrom != nil {
		t.Fatalf("an ordinary PostCreated set a floor: %v", store.upserts[1].EligibleFrom)
	}
}

// A post a rights owner TRACKS (post-service migration 075) stays public,
// but its eligibility events carry earnings_excluded: it earns nothing from
// then on, and a later ordinary transition that still carries the flag
// does not re-grant earnings.
func TestOwnerTrackedPostEarnsNothing(t *testing.T) {
	store := &recordingStore{}
	c := NewContentOwnershipConsumer(store)
	post, author := uuid.New(), uuid.New()
	at := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	for i, excluded := range []bool{true, true, false} {
		if err := c.apply(context.Background(), envelopeFor(t, events.PostSearchEligibilityChanged, events.PostSearchEligibilityChangedPayload{
			PostID: post.String(), AuthorID: author.String(), Visibility: "public", ReviewStatus: "approved",
			BaseReviewStatus: "approved", SearchRev: int64(10 + i), EarningsExcluded: excluded, ChangedAt: at.Add(time.Duration(i) * time.Hour),
		})); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.eligibility) != 3 {
		t.Fatalf("eligibility writes = %d", len(store.eligibility))
	}
	for i, want := range []string{pgstore.EligibilityIneligible, pgstore.EligibilityIneligible, pgstore.EligibilityEligible} {
		if store.eligibility[i].State != want {
			t.Fatalf("write %d = %s, want %s", i, store.eligibility[i].State, want)
		}
	}
}

//go:build integration

package aggregation

import (
	"context"
	"testing"
	"time"

	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/google/uuid"
)

// Copyright quick wins (migration 014): a post held for copyright review
// earns nothing for the held days and earns from the clear onward.
//
//   - a live post a copyright case held two days ago and released
//     yesterday at 15:00 (ineligible, then eligible again through
//     ApplyContentEligibility): the day of the hold earns nothing, the
//     day of the clear earns;
//   - a title-review hold whose PostCreated was withheld until yesterday's
//     clear (UpsertContentOwnership with EligibleFrom): the publication
//     day earns nothing;
//   - a post that never was held keeps every day.
func TestHeldContentEarnsFromTheClearOnward(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.Pool(t, "aggregation")
	ensurePlaybackSessions(t, pool)
	truncateAggregation(t, pool)
	store := pgstore.New(pool)

	creator := uuid.New()
	caseHeld, titleHeld, open := uuid.New(), uuid.New(), uuid.New()
	twoDaysAgo, yesterday := dayAgo(2), dayAgo(1)
	for _, content := range []uuid.UUID{caseHeld, titleHeld, open} {
		insertHourlyRow(t, pool, content, creator, "long_video", twoDaysAgo.Add(4*time.Hour), 3)
		insertHourlyRow(t, pool, content, creator, "long_video", yesterday.Add(18*time.Hour), 3)
	}
	created := twoDaysAgo.Add(-24 * time.Hour)
	for _, content := range []uuid.UUID{caseHeld, open} {
		if err := store.UpsertContentOwnership(ctx, pgstore.ContentOwnership{ContentID: content, CreatorID: creator, ContentType: "long_video", CreatedAt: created}); err != nil {
			t.Fatal(err)
		}
	}
	// The case hold and its release, as the eligibility consumer applies them.
	if _, err := store.ApplyContentEligibility(ctx, pgstore.ContentEligibility{ContentID: caseHeld, CreatorID: creator,
		State: pgstore.EligibilityIneligible, EffectiveFrom: twoDaysAgo.Add(time.Hour), Rev: 2, AllowEqualRev: true}); err != nil {
		t.Fatal(err)
	}
	clearedAt := yesterday.Add(15 * time.Hour)
	if _, err := store.ApplyContentEligibility(ctx, pgstore.ContentEligibility{ContentID: caseHeld, CreatorID: creator,
		State: pgstore.EligibilityEligible, EffectiveFrom: clearedAt, Rev: 3, AllowEqualRev: true}); err != nil {
		t.Fatal(err)
	}
	// An eligible re-affirmation does not move the floor.
	if _, err := store.ApplyContentEligibility(ctx, pgstore.ContentEligibility{ContentID: open, CreatorID: creator,
		State: pgstore.EligibilityEligible, EffectiveFrom: yesterday.Add(20 * time.Hour), Rev: 2, AllowEqualRev: true}); err != nil {
		t.Fatal(err)
	}
	// The withheld PostCreated, written at the clear.
	if err := store.UpsertContentOwnership(ctx, pgstore.ContentOwnership{ContentID: titleHeld, CreatorID: creator, ContentType: "long_video",
		CreatedAt: created, EligibleFrom: &clearedAt}); err != nil {
		t.Fatal(err)
	}

	rollup := NewDailyRollup(pool, nil)
	for _, day := range []time.Time{twoDaysAgo, yesterday} {
		if err := rollup.RollupDay(ctx, day); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name    string
		content uuid.UUID
		day     time.Time
		want    int64
	}{
		{"case hold, the held day", caseHeld, twoDaysAgo, 0},
		{"case hold, the day of the clear", caseHeld, yesterday, 1},
		{"title hold, the publication day", titleHeld, twoDaysAgo, 0},
		{"title hold, the day of the clear", titleHeld, yesterday, 1},
		{"never held, day one", open, twoDaysAgo, 1},
		{"never held, day two", open, yesterday, 1},
	} {
		if rows, _, _, _ := summaryViews(t, pool, c.content, c.day); rows != c.want {
			t.Errorf("%s: %d summary rows, want %d", c.name, rows, c.want)
		}
	}
}

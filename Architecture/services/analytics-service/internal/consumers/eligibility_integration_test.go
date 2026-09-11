//go:build integration

package consumers

import (
	"context"
	"testing"
	"time"

	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/google/uuid"
)

// The revision gate lives in SQL, where it is atomic. This is the live
// counterpart of TestStaleEligibilityRevIsDropped: a write whose
// revision does not advance leaves the row exactly as it was.
func TestLiveStaleEligibilityRevIsDropped(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.Pool(t, "consumers")
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.ingest_receipts, analytics.content_ownership CASCADE`); err != nil {
		t.Fatal(err)
	}
	store := pgstore.New(pool)
	content, creator := uuid.New(), uuid.New()
	if err := store.UpsertContentOwnership(ctx, pgstore.ContentOwnership{
		ContentID: content, CreatorID: creator, ContentType: "flick", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	read := func() (state string, rev int64) {
		t.Helper()
		if err := pool.QueryRow(ctx, `
			SELECT eligibility_state, eligibility_rev FROM analytics.content_ownership WHERE content_id = $1`,
			content).Scan(&state, &rev); err != nil {
			t.Fatal(err)
		}
		return
	}
	if state, rev := read(); state != pgstore.EligibilityEligible || rev != 0 {
		t.Fatalf("fresh row = %s/%d, want eligible/0", state, rev)
	}

	at := time.Now().UTC()
	applied, err := store.ApplyContentEligibility(ctx, pgstore.ContentEligibility{
		ContentID: content, CreatorID: creator, State: pgstore.EligibilityIneligible, EffectiveFrom: at, Rev: 5,
	})
	if err != nil || !applied {
		t.Fatalf("rev 5: applied=%v err=%v", applied, err)
	}
	if state, rev := read(); state != pgstore.EligibilityIneligible || rev != 5 {
		t.Fatalf("after rev 5 = %s/%d, want ineligible/5", state, rev)
	}

	// Stale approval: dropped, row untouched.
	applied, err = store.ApplyContentEligibility(ctx, pgstore.ContentEligibility{
		ContentID: content, CreatorID: creator, State: pgstore.EligibilityEligible, EffectiveFrom: at, Rev: 3,
	})
	if err != nil || applied {
		t.Fatalf("rev 3: applied=%v err=%v, want dropped", applied, err)
	}
	if state, rev := read(); state != pgstore.EligibilityIneligible || rev != 5 {
		t.Fatalf("a stale approval changed the row to %s/%d", state, rev)
	}

	// Equal revision: dropped unless the caller is the authoritative
	// event, which is idempotent at the same revision.
	applied, err = store.ApplyContentEligibility(ctx, pgstore.ContentEligibility{
		ContentID: content, CreatorID: creator, State: pgstore.EligibilityEligible, EffectiveFrom: at, Rev: 5,
	})
	if err != nil || applied {
		t.Fatalf("equal rev, non-authoritative: applied=%v err=%v, want dropped", applied, err)
	}
	applied, err = store.ApplyContentEligibility(ctx, pgstore.ContentEligibility{
		ContentID: content, CreatorID: creator, State: pgstore.EligibilityEligible, EffectiveFrom: at, Rev: 5, AllowEqualRev: true,
	})
	if err != nil || !applied {
		t.Fatalf("equal rev, authoritative: applied=%v err=%v, want applied", applied, err)
	}
	if state, rev := read(); state != pgstore.EligibilityEligible || rev != 5 {
		t.Fatalf("after authoritative equal rev = %s/%d, want eligible/5", state, rev)
	}

	// A delete with no revision is honoured and never lowers the
	// revision.
	applied, err = store.ApplyContentEligibility(ctx, pgstore.ContentEligibility{
		ContentID: content, CreatorID: creator, State: pgstore.EligibilityDeleted, EffectiveFrom: at, Rev: 0, IgnoreRev: true,
	})
	if err != nil || !applied {
		t.Fatalf("rev 0 delete: applied=%v err=%v, want applied", applied, err)
	}
	if state, rev := read(); state != pgstore.EligibilityDeleted || rev != 5 {
		t.Fatalf("after rev-0 delete = %s/%d, want deleted/5", state, rev)
	}

	// The wrong creator never touches the row.
	applied, err = store.ApplyContentEligibility(ctx, pgstore.ContentEligibility{
		ContentID: content, CreatorID: uuid.New(), State: pgstore.EligibilityEligible, EffectiveFrom: at, Rev: 9,
	})
	if err != nil || applied {
		t.Fatalf("wrong creator: applied=%v err=%v, want not applied", applied, err)
	}
}

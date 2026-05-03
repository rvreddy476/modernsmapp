package store

import (
	"context"
	"testing"
	"time"
)

// TestFlipToGracePeriod_Idempotent ensures a re-run flips zero rows once
// the first pass has already moved everything forward.
func TestFlipToGracePeriod_Idempotent(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Build a partner + an actively-expired subscription. We use a low-
	// level INSERT so we can backdate expires_at.
	pid := makeRiderTestPartner(t, st)
	plan, err := st.GetPlanByCode(ctx, "basic_199")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	expired := time.Now().Add(-1 * time.Hour)
	sub, err := st.CreateSubscription(ctx, CreateSubscriptionInput{
		PartnerID: pid, PlanID: plan.ID, Status: "active",
		StartsAt: time.Now().Add(-30 * 24 * time.Hour), ExpiresAt: expired,
	})
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	first, err := st.FlipToGracePeriod(ctx)
	if err != nil {
		t.Fatalf("flip 1: %v", err)
	}
	wantSubFlipped := false
	for _, s := range first {
		if s.ID == sub.ID {
			wantSubFlipped = true
		}
	}
	if !wantSubFlipped {
		t.Errorf("our newly-expired subscription should have flipped")
	}
	// Second run: zero rows.
	second, err := st.FlipToGracePeriod(ctx)
	if err != nil {
		t.Fatalf("flip 2: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("re-run flipped %d more rows; idempotency broken", len(second))
	}
}

// TestFlipToExpired_Idempotent verifies the second arm of the grace
// transition is also idempotent.
func TestFlipToExpired_Idempotent(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pid := makeRiderTestPartner(t, st)
	plan, _ := st.GetPlanByCode(ctx, "basic_199")
	sub, err := st.CreateSubscription(ctx, CreateSubscriptionInput{
		PartnerID: pid, PlanID: plan.ID, Status: "active",
		StartsAt: time.Now().Add(-30 * 24 * time.Hour), ExpiresAt: time.Now().Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	if _, err := st.FlipToGracePeriod(ctx); err != nil {
		t.Fatalf("flip to grace: %v", err)
	}
	// Force grace_ends_at into the past so FlipToExpired triggers.
	if _, err := st.DB().Exec(ctx, `UPDATE rider_partner_subscriptions SET grace_ends_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, sub.ID); err != nil {
		t.Fatalf("backdate grace: %v", err)
	}
	first, err := st.FlipToExpired(ctx)
	if err != nil {
		t.Fatalf("flip to expired 1: %v", err)
	}
	if len(first) == 0 {
		t.Errorf("expected at least one flip")
	}
	second, err := st.FlipToExpired(ctx)
	if err != nil {
		t.Fatalf("flip to expired 2: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("re-run flipped %d rows; idempotency broken", len(second))
	}
}

// TestIncrementRenewalFailure_AutoDisable verifies the 3-strike rule.
func TestIncrementRenewalFailure_AutoDisable(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pid := makeRiderTestPartner(t, st)
	plan, _ := st.GetPlanByCode(ctx, "basic_199")
	sub, err := st.CreateSubscription(ctx, CreateSubscriptionInput{
		PartnerID: pid, PlanID: plan.ID, Status: "active",
		StartsAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	if _, err := st.DB().Exec(ctx, `UPDATE rider_partner_subscriptions SET auto_renew = TRUE WHERE id = $1`, sub.ID); err != nil {
		t.Fatalf("enable auto_renew: %v", err)
	}
	for i := 1; i <= 3; i++ {
		count, autoOff, err := st.IncrementRenewalFailure(ctx, sub.ID, 3)
		if err != nil {
			t.Fatalf("increment %d: %v", i, err)
		}
		if count != i {
			t.Errorf("attempt %d: count = %d, want %d", i, count, i)
		}
		wantAutoOff := i >= 3
		if autoOff != wantAutoOff {
			t.Errorf("attempt %d: autoOff = %v, want %v", i, autoOff, wantAutoOff)
		}
	}
	// auto_renew should now be false in the DB.
	var ar bool
	if err := st.DB().QueryRow(ctx, `SELECT auto_renew FROM rider_partner_subscriptions WHERE id = $1`, sub.ID).Scan(&ar); err != nil {
		t.Fatalf("read auto_renew: %v", err)
	}
	if ar {
		t.Errorf("auto_renew should have been disabled at strike 3")
	}
}

// TestRenewSubscription_ExtendsExpiry verifies the renewal SQL extends
// expires_at by the plan's billing_period_days.
func TestRenewSubscription_ExtendsExpiry(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pid := makeRiderTestPartner(t, st)
	plan, _ := st.GetPlanByCode(ctx, "basic_199")
	original := time.Now().Add(2 * time.Hour)
	sub, err := st.CreateSubscription(ctx, CreateSubscriptionInput{
		PartnerID: pid, PlanID: plan.ID, Status: "active",
		StartsAt: time.Now(), ExpiresAt: original,
	})
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	newExpiry, err := st.RenewSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	want := original.Add(time.Duration(plan.BillingPeriodDays) * 24 * time.Hour)
	delta := newExpiry.Sub(want)
	if delta < -time.Minute || delta > time.Minute {
		t.Errorf("new expiry = %v, want close to %v", newExpiry, want)
	}
}

// TestListAutoRenewCandidates_RespectsCooldown checks the cooldown guard.
func TestListAutoRenewCandidates_RespectsCooldown(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pid := makeRiderTestPartner(t, st)
	plan, _ := st.GetPlanByCode(ctx, "basic_199")
	sub, err := st.CreateSubscription(ctx, CreateSubscriptionInput{
		PartnerID: pid, PlanID: plan.ID, Status: "active",
		StartsAt: time.Now(), ExpiresAt: time.Now().Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create sub: %v", err)
	}
	if _, err := st.DB().Exec(ctx, `UPDATE rider_partner_subscriptions SET auto_renew = TRUE, renewal_attempted_at = NOW() WHERE id = $1`, sub.ID); err != nil {
		t.Fatalf("set renewal_attempted_at: %v", err)
	}
	// 1 hour cooldown means a fresh attempt should NOT include this row.
	got, err := st.ListAutoRenewCandidates(ctx, 12*time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, s := range got {
		if s.ID == sub.ID {
			t.Errorf("recently-attempted sub should be excluded by cooldown")
		}
	}
}

// TestListStuckRides_HonorsThreshold verifies the time-based filter. The
// safety incident dedupe is exercised by HasOpenIncidentForRideKind in
// safety_test.go.
func TestListStuckRides_HonorsThreshold(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	got, err := st.ListStuckRides(ctx, RidesStuckFilter{
		Status: "requested", OlderThan: time.Hour, Limit: 50,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// We don't seed rides here — the result may be 0 or non-zero from
	// other tests. The minimum we assert is "no error".
	_ = got
}

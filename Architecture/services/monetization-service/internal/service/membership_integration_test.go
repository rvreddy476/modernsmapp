//go:build integration

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The membership SQL against a scratch database (openTestPool →
// requireTestDSN refuses any database whose name does not end in _test).
// Written 23 Oct 2026; run with MONETIZATION_POSTGRES_DSN pointing at e.g.
// monetization_it_test.

type membershipFixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	store   *postgres.Store
	svc     *Service
	creator uuid.UUID
	fans    []uuid.UUID
}

func newMembershipFixture(t *testing.T, fans int, balance int64) *membershipFixture {
	t.Helper()
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	f := &membershipFixture{ctx: ctx, pool: pool, store: store, svc: New(store, nil), creator: uuid.New()}
	users := []uuid.UUID{f.creator}
	for i := 0; i < fans; i++ {
		f.fans = append(f.fans, uuid.New())
	}
	users = append(users, f.fans...)
	for _, u := range users {
		if _, err := store.EnsureWallet(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	for _, fan := range f.fans {
		if _, err := pool.Exec(ctx, `UPDATE creator_ledger SET balance = $2 WHERE user_id = $1`, fan, balance); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM subscription_renewals WHERE subscription_id IN (SELECT id FROM subscriptions WHERE creator_id = $1)`, f.creator)
		_, _ = pool.Exec(bg, `DELETE FROM subscription_events WHERE subscription_id IN (SELECT id FROM subscriptions WHERE creator_id = $1)`, f.creator)
		_, _ = pool.Exec(bg, `DELETE FROM subscriptions WHERE creator_id = $1`, f.creator)
		_, _ = pool.Exec(bg, `DELETE FROM creator_tiers WHERE creator_id = $1`, f.creator)
		for _, u := range users {
			_, _ = pool.Exec(bg, `DELETE FROM transactions WHERE wallet_id = $1`, u)
			_, _ = pool.Exec(bg, `DELETE FROM creator_ledger WHERE user_id = $1`, u)
		}
	})
	return f
}

func (f *membershipFixture) balance(t *testing.T, u uuid.UUID) int64 {
	t.Helper()
	b, _ := ledgerState(f.ctx, t, f.pool, u)
	return b
}

func TestMembershipIntegrationConcurrentSubscribesOneWins(t *testing.T) {
	f := newMembershipFixture(t, 1, 100_000)
	tier, err := f.svc.CreateTier(f.ctx, f.creator, TierInput{Name: "Fan", PricePaise: 4_900})
	if err != nil {
		t.Fatal(err)
	}
	const callers = 4
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, "")
		}(i)
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, postgres.ErrAlreadySubscribed):
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d subscribes succeeded, want 1", ok)
	}
	if n := countRows(f.ctx, t, f.pool, `SELECT count(*) FROM subscriptions WHERE subscriber_id = $1 AND creator_id = $2`, f.fans[0], f.creator); n != 1 {
		t.Fatalf("%d subscription rows", n)
	}
	if b := f.balance(t, f.fans[0]); b != 100_000-4_900 {
		t.Fatalf("fan balance %d: charged more than once", b)
	}
}

func TestMembershipIntegrationIdempotencyKeyPerSubscriber(t *testing.T) {
	f := newMembershipFixture(t, 2, 100_000)
	tier, err := f.svc.CreateTier(f.ctx, f.creator, TierInput{Name: "Fan", PricePaise: 4_900})
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, "shared-key")
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.svc.Subscribe(f.ctx, f.fans[1], f.creator, tier.ID, "shared-key")
	if err != nil {
		t.Fatalf("second subscriber, same key: %v", err)
	}
	if a.ID == b.ID {
		t.Fatal("the same subscription was handed to two subscribers")
	}
	again, err := f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, "shared-key")
	if err != nil || again.ID != a.ID {
		t.Fatalf("replay: %v", err)
	}
	if bal := f.balance(t, f.fans[0]); bal != 100_000-4_900 {
		t.Fatalf("replay charged again: %d", bal)
	}
}

func TestMembershipIntegrationTierPatchAndLimit(t *testing.T) {
	f := newMembershipFixture(t, 0, 0)
	tier, err := f.svc.CreateTier(f.ctx, f.creator, TierInput{Name: "Gold", PricePaise: 9_900, Perks: []string{"Badge"}})
	if err != nil {
		t.Fatal(err)
	}
	price := int64(14_900)
	got, err := f.svc.UpdateTier(f.ctx, f.creator, tier.ID, TierPatch{PricePaise: &price})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Gold" || string(got.Perks) != `["Badge"]` || !got.IsActive || got.PricePaise != 14_900 {
		t.Fatalf("partial update: %+v %s", got, got.Perks)
	}
	for i := 0; i < MaxActiveTiers-1; i++ {
		if _, err := f.svc.CreateTier(f.ctx, f.creator, TierInput{Name: "T", PricePaise: 4_900}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.CreateTier(f.ctx, f.creator, TierInput{Name: "T", PricePaise: 4_900}); !errors.Is(err, ErrTierLimit) {
		t.Fatalf("fourth: %v", err)
	}
}

func TestMembershipIntegrationRenewalOncePerPeriod(t *testing.T) {
	f := newMembershipFixture(t, 1, 100_000)
	tier, err := f.svc.CreateTier(f.ctx, f.creator, TierInput{Name: "Fan", PricePaise: 4_900})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.store.GetSubscriptionByID(f.ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := postgres.RenewalInput{
		SubscriptionID: sub.ID, SubscriberID: f.fans[0], CreatorID: f.creator,
		ExpectedPeriodEnd: stored.CurrentPeriodEnd,
		NewPeriodStart:    stored.CurrentPeriodEnd, NewPeriodEnd: stored.CurrentPeriodEnd.AddDate(0, 1, 0),
		TierID: tier.ID, TierName: tier.Name, PricePaise: 4_900, Currency: "INR",
		IdempotencyKey: postgres.RenewalIdempotencyKey(sub.ID, stored.CurrentPeriodEnd),
		Description:    "Subscription renewal: Fan",
	}
	if err := f.store.RenewSubscription(f.ctx, in); err != nil {
		t.Fatal(err)
	}
	// Same period again: the row no longer ends there, and the key is taken.
	if err := f.store.RenewSubscription(f.ctx, in); !errors.Is(err, postgres.ErrSubscriptionStateChanged) && !errors.Is(err, postgres.ErrRenewalAlreadyApplied) {
		t.Fatalf("second renewal of one period: %v", err)
	}
	if b := f.balance(t, f.fans[0]); b != 100_000-2*4_900 {
		t.Fatalf("fan balance %d, want subscribe + one renewal", b)
	}
	if n := countRows(f.ctx, t, f.pool, `SELECT count(*) FROM subscription_renewals WHERE subscription_id = $1`, sub.ID); n != 1 {
		t.Fatalf("%d renewal rows", n)
	}

	// A short balance rolls the whole renewal back: no key, no extension.
	if _, err := f.pool.Exec(f.ctx, `UPDATE creator_ledger SET balance = 0 WHERE user_id = $1`, f.fans[0]); err != nil {
		t.Fatal(err)
	}
	next := in
	next.ExpectedPeriodEnd = in.NewPeriodEnd
	next.NewPeriodStart, next.NewPeriodEnd = in.NewPeriodEnd, in.NewPeriodEnd.AddDate(0, 1, 0)
	next.IdempotencyKey = postgres.RenewalIdempotencyKey(sub.ID, in.NewPeriodEnd)
	if err := f.store.RenewSubscription(f.ctx, next); !errors.Is(err, postgres.ErrInsufficientFunds) {
		t.Fatalf("short balance: %v", err)
	}
	if n := countRows(f.ctx, t, f.pool, `SELECT count(*) FROM subscription_renewals WHERE subscription_id = $1`, sub.ID); n != 1 {
		t.Fatalf("a failed renewal left its key behind (%d rows)", n)
	}
	after, _ := f.store.GetSubscriptionByID(f.ctx, sub.ID)
	if !after.CurrentPeriodEnd.Equal(in.NewPeriodEnd) {
		t.Fatalf("a failed renewal moved the period end to %v", after.CurrentPeriodEnd)
	}
}

func TestMembershipIntegrationCancelledAtPeriodEndExpires(t *testing.T) {
	f := newMembershipFixture(t, 1, 100_000)
	tier, err := f.svc.CreateTier(f.ctx, f.creator, TierInput{Name: "Fan", PricePaise: 4_900})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.CancelAtPeriodEnd(f.ctx, f.fans[0], sub.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.ExpireEndedCancellations(f.ctx, time.Now()); len(got) != 0 {
		t.Fatalf("expired before the period ended: %d", len(got))
	}
	got, err := f.store.ExpireEndedCancellations(f.ctx, sub.CurrentPeriodEnd.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range got {
		if s.ID == sub.ID && s.Status == "expired" {
			found = true
		}
	}
	if !found {
		t.Fatal("cancelled_at_period_end did not expire at period end")
	}
	// Expired is not live: the fan may subscribe again.
	if _, err := f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, ""); err != nil {
		t.Fatalf("resubscribe after expiry: %v", err)
	}
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/service/membershiptest"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Creator-membership correctness (23 Oct 2026). Every rule runs against
// the in-memory store in membershiptest, through the real Service methods.

var _ MembershipStore = (*membershiptest.Store)(nil)
var _ MembershipStore = (*postgres.Store)(nil)

func newMembershipRig(now time.Time) (*Service, *membershiptest.Store, *membershiptest.Publisher) {
	fake := membershiptest.New()
	pub := &membershiptest.Publisher{}
	svc := New(nil, nil).WithMembershipStore(fake).WithEntitlementPublisher(pub).WithClock(func() time.Time { return now })
	return svc, fake, pub
}

func ptr[T any](v T) *T { return &v }

// ---------------------------------------------------------------------------
// Tiers: validation, limit, partial update
// ---------------------------------------------------------------------------

func TestCreateTierValidatesNamePricePerksAndCurrency(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	ten := make([]string, 10)
	for i := range ten {
		ten[i] = "perk"
	}
	cases := []struct {
		name string
		in   TierInput
		want error
	}{
		{"floor price ₹29", TierInput{Name: "Fan", PricePaise: 2_900}, nil},
		{"ceiling price ₹2,999", TierInput{Name: "Fan", PricePaise: 299_900}, nil},
		{"below ₹29", TierInput{Name: "Fan", PricePaise: 2_899}, ErrTierPriceOutOfRange},
		{"above ₹2,999", TierInput{Name: "Fan", PricePaise: 299_901}, ErrTierPriceOutOfRange},
		{"zero price", TierInput{Name: "Fan", PricePaise: 0}, ErrTierPriceOutOfRange},
		{"negative price", TierInput{Name: "Fan", PricePaise: -4_900}, ErrTierPriceOutOfRange},
		{"empty name", TierInput{Name: "", PricePaise: 4_900}, ErrTierNameInvalid},
		{"blank name", TierInput{Name: "   ", PricePaise: 4_900}, ErrTierNameInvalid},
		{"60-character name", TierInput{Name: long(60), PricePaise: 4_900}, nil},
		{"61-character name", TierInput{Name: long(61), PricePaise: 4_900}, ErrTierNameInvalid},
		{"60 multibyte characters", TierInput{Name: strings.Repeat("ā", 60), PricePaise: 4_900}, nil},
		{"ten perks", TierInput{Name: "Fan", PricePaise: 4_900, Perks: ten}, nil},
		{"eleven perks", TierInput{Name: "Fan", PricePaise: 4_900, Perks: append(ten, "one more")}, ErrTierPerksInvalid},
		{"100-character perk", TierInput{Name: "Fan", PricePaise: 4_900, Perks: []string{long(100)}}, nil},
		{"101-character perk", TierInput{Name: "Fan", PricePaise: 4_900, Perks: []string{long(101)}}, ErrTierPerksInvalid},
		{"USD", TierInput{Name: "Fan", PricePaise: 4_900, Currency: "USD"}, ErrTierCurrencyInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newMembershipRig(time.Now())
			tier, err := svc.CreateTier(context.Background(), uuid.New(), tc.in)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want ok, got %v", err)
				}
				if tier.Currency != "INR" || !tier.IsActive || tier.BillingPeriod != "monthly" {
					t.Fatalf("tier not normalised: %+v", tier)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestCreateTierAllowsAtMostThreeActive(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newMembershipRig(time.Now())
	creator := uuid.New()
	var tiers []*postgres.CreatorTier
	for i := 0; i < MaxActiveTiers; i++ {
		tier, err := svc.CreateTier(ctx, creator, TierInput{Name: "T", PricePaise: 4_900})
		if err != nil {
			t.Fatalf("tier %d: %v", i+1, err)
		}
		tiers = append(tiers, tier)
	}
	if _, err := svc.CreateTier(ctx, creator, TierInput{Name: "Fourth", PricePaise: 4_900}); !errors.Is(err, ErrTierLimit) {
		t.Fatalf("fourth active tier: want ErrTierLimit, got %v", err)
	}
	// Another creator is unaffected.
	if _, err := svc.CreateTier(ctx, uuid.New(), TierInput{Name: "T", PricePaise: 4_900}); err != nil {
		t.Fatalf("other creator: %v", err)
	}
	// Retire one, then a new one fits; reactivating the retired one does not.
	if _, err := svc.UpdateTier(ctx, creator, tiers[0].ID, TierPatch{IsActive: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateTier(ctx, creator, TierInput{Name: "Replacement", PricePaise: 4_900}); err != nil {
		t.Fatalf("after retiring one: %v", err)
	}
	if _, err := svc.UpdateTier(ctx, creator, tiers[0].ID, TierPatch{IsActive: ptr(true)}); !errors.Is(err, ErrTierLimit) {
		t.Fatalf("reactivating a fourth: want ErrTierLimit, got %v", err)
	}
}

func TestUpdateTierIsPartial(t *testing.T) {
	ctx := context.Background()
	svc, fake, _ := newMembershipRig(time.Now())
	creator := uuid.New()
	tier, err := svc.CreateTier(ctx, creator, TierInput{Name: "Gold", PricePaise: 9_900, Perks: []string{"Badge", "Early access"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateTier(ctx, creator, tier.ID, TierPatch{IsActive: ptr(false)}); err != nil {
		t.Fatal(err)
	}

	got, err := svc.UpdateTier(ctx, creator, tier.ID, TierPatch{PricePaise: ptr(int64(14_900))})
	if err != nil {
		t.Fatal(err)
	}
	if got.PricePaise != 14_900 {
		t.Fatalf("price not changed: %d", got.PricePaise)
	}
	if got.Name != "Gold" || got.IsActive || got.Currency != "INR" {
		t.Fatalf("a price-only PATCH changed other fields: %+v", got)
	}
	var perks []string
	_ = json.Unmarshal(got.Perks, &perks)
	if !reflect.DeepEqual(perks, []string{"Badge", "Early access"}) {
		t.Fatalf("a price-only PATCH changed perks: %s", got.Perks)
	}
	p := fake.LastTierPatch
	if p.Name != nil || p.Currency != nil || p.Perks != nil || p.IsActive != nil {
		t.Fatalf("service sent fields the caller did not: %+v", p)
	}

	// Name-only, perks-only.
	got, _ = svc.UpdateTier(ctx, creator, tier.ID, TierPatch{Name: ptr("  Platinum ")})
	if got.Name != "Platinum" || got.PricePaise != 14_900 {
		t.Fatalf("name-only PATCH: %+v", got)
	}
	got, _ = svc.UpdateTier(ctx, creator, tier.ID, TierPatch{Perks: &[]string{}})
	if string(got.Perks) != "[]" || got.Name != "Platinum" {
		t.Fatalf("perks-only PATCH: %+v", got)
	}

	// Another creator's tier is not found.
	if _, err := svc.UpdateTier(ctx, uuid.New(), tier.ID, TierPatch{Name: ptr("Mine")}); !errors.Is(err, ErrTierNotFound) {
		t.Fatalf("foreign tier: %v", err)
	}
}

func TestUpdateTierValidatesProvidedFields(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newMembershipRig(time.Now())
	creator := uuid.New()
	tier, _ := svc.CreateTier(ctx, creator, TierInput{Name: "Gold", PricePaise: 9_900})
	for _, tc := range []struct {
		p    TierPatch
		want error
	}{
		{TierPatch{PricePaise: ptr(int64(100))}, ErrTierPriceOutOfRange},
		{TierPatch{PricePaise: ptr(int64(-1))}, ErrTierPriceOutOfRange},
		{TierPatch{PricePaise: ptr(int64(300_000))}, ErrTierPriceOutOfRange},
		{TierPatch{Name: ptr("")}, ErrTierNameInvalid},
		{TierPatch{Perks: &[]string{strings.Repeat("x", 101)}}, ErrTierPerksInvalid},
		{TierPatch{Currency: ptr("EUR")}, ErrTierCurrencyInvalid},
	} {
		if _, err := svc.UpdateTier(ctx, creator, tier.ID, tc.p); !errors.Is(err, tc.want) {
			t.Errorf("%+v: want %v, got %v", tc.p, tc.want, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Entitlement
// ---------------------------------------------------------------------------

type entRig struct {
	svc                  *Service
	fake                 *membershiptest.Store
	creator, fan         uuid.UUID
	bronze, silver, gold *postgres.CreatorTier
	now                  time.Time
}

func newEntRig() *entRig {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	svc, fake, _ := newMembershipRig(now)
	r := &entRig{svc: svc, fake: fake, creator: uuid.New(), fan: uuid.New(), now: now}
	r.bronze = fake.AddTier(r.creator, "Bronze", 4_900)
	r.silver = fake.AddTier(r.creator, "Silver", 9_900)
	r.gold = fake.AddTier(r.creator, "Gold", 19_900)
	return r
}

func (r *entRig) subscribe(tier *postgres.CreatorTier, lockedPrice int64, status string) *postgres.Subscription {
	return r.fake.AddSubscription(postgres.Subscription{
		SubscriberID: r.fan, CreatorID: r.creator, TierID: tier.ID, TierName: tier.Name,
		PricePaise: lockedPrice, Status: status,
		CurrentPeriodStart: r.now.Add(-10 * 24 * time.Hour), CurrentPeriodEnd: r.now.Add(20 * 24 * time.Hour),
	})
}

func (r *entRig) allowed(t *testing.T, required *postgres.CreatorTier) (bool, string) {
	t.Helper()
	req := EntitlementCheckRequest{SubscriberID: r.fan, CreatorID: r.creator}
	if required != nil {
		req.RequiredTierID = &required.ID
	}
	ent, err := r.svc.CheckEntitlement(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return ent.Allowed, ent.Reason
}

func TestEntitlementByTierLevel(t *testing.T) {
	r := newEntRig()
	r.subscribe(r.silver, 9_900, "active")
	for _, tc := range []struct {
		required *postgres.CreatorTier
		want     bool
	}{
		{nil, true},
		{r.bronze, true},
		{r.silver, true},
		{r.gold, false},
	} {
		name := "any"
		if tc.required != nil {
			name = tc.required.Name
		}
		if got, reason := r.allowed(t, tc.required); got != tc.want {
			t.Errorf("silver member, %s content: allowed=%v (%s), want %v", name, got, reason, tc.want)
		}
	}
}

// The price a subscription locked at checkout never decides its level: a
// Bronze member who paid a high early price does not unlock Silver.
func TestEntitlementIgnoresLockedPrice(t *testing.T) {
	r := newEntRig()
	r.subscribe(r.bronze, 19_900, "active")
	if got, reason := r.allowed(t, r.silver); got {
		t.Fatalf("bronze member with a high locked price unlocked silver (%s)", reason)
	}
	if got, _ := r.allowed(t, r.bronze); !got {
		t.Fatalf("bronze member lost bronze")
	}
}

// A creator raising a tier's price must not lock that tier's own members
// out — and raising it above a higher tier lifts its members with it.
func TestEntitlementSurvivesTierPriceRaise(t *testing.T) {
	r := newEntRig()
	r.subscribe(r.silver, 9_900, "active")
	if _, err := r.svc.UpdateTier(context.Background(), r.creator, r.silver.ID, TierPatch{PricePaise: ptr(int64(29_900))}); err != nil {
		t.Fatal(err)
	}
	if got, reason := r.allowed(t, r.silver); !got {
		t.Fatalf("silver member lost silver after the price rise (%s)", reason)
	}
	if got, reason := r.allowed(t, r.gold); !got {
		t.Fatalf("silver now prices above gold; its member should reach gold (%s)", reason)
	}
	// And a raise on the REQUIRED tier above the member's level denies.
	r2 := newEntRig()
	r2.subscribe(r2.silver, 9_900, "active")
	if _, err := r2.svc.UpdateTier(context.Background(), r2.creator, r2.bronze.ID, TierPatch{PricePaise: ptr(int64(14_900))}); err != nil {
		t.Fatal(err)
	}
	if got, _ := r2.allowed(t, r2.bronze); got {
		t.Fatalf("bronze now prices above silver; a silver member should not reach it")
	}
}

func TestEntitlementByStatusAndPeriodEnd(t *testing.T) {
	future, past := 5*24*time.Hour, -time.Hour
	cases := []struct {
		status     string
		periodEnd  time.Duration
		graceEnd   *time.Duration
		want       bool
		wantReason string
	}{
		{"active", future, nil, true, ""},
		{"active", past, nil, false, "access_period_ended"},
		{"cancelled_at_period_end", future, nil, true, ""},
		{"cancelled_at_period_end", past, nil, false, "access_period_ended"},
		{"paused", future, nil, true, ""},
		{"paused", past, nil, false, "access_period_ended"},
		{"past_due", future, nil, true, ""},
		{"past_due", past, nil, false, "access_period_ended"},
		{"grace", past, ptr(future), true, ""},
		{"grace", past, ptr(past), false, "access_period_ended"},
		{"cancelled", future, nil, false, "no_active_subscription"},
		{"expired", future, nil, false, "no_active_subscription"},
	}
	for _, tc := range cases {
		t.Run(tc.status+"/"+tc.periodEnd.String(), func(t *testing.T) {
			r := newEntRig()
			sub := postgres.Subscription{
				SubscriberID: r.fan, CreatorID: r.creator, TierID: r.silver.ID, PricePaise: 9_900, Status: tc.status,
				CurrentPeriodStart: r.now.Add(-30 * 24 * time.Hour), CurrentPeriodEnd: r.now.Add(tc.periodEnd),
			}
			if tc.graceEnd != nil {
				g := r.now.Add(*tc.graceEnd)
				sub.GracePeriodEnd = &g
			}
			r.fake.AddSubscription(sub)
			got, reason := r.allowed(t, r.silver)
			if got != tc.want || (tc.wantReason != "" && reason != tc.wantReason) {
				t.Fatalf("allowed=%v reason=%q, want %v %q", got, reason, tc.want, tc.wantReason)
			}
		})
	}
}

func TestSubscriptionAccessUntil(t *testing.T) {
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	grace := end.Add(7 * 24 * time.Hour)
	for _, tc := range []struct {
		sub    postgres.Subscription
		until  time.Time
		grants bool
	}{
		{postgres.Subscription{Status: "active", CurrentPeriodEnd: end}, end, true},
		{postgres.Subscription{Status: "paused", CurrentPeriodEnd: end}, end, true},
		{postgres.Subscription{Status: "grace", CurrentPeriodEnd: end, GracePeriodEnd: &grace}, grace, true},
		{postgres.Subscription{Status: "cancelled", CurrentPeriodEnd: end}, time.Time{}, false},
		{postgres.Subscription{Status: "expired", CurrentPeriodEnd: end}, time.Time{}, false},
		{postgres.Subscription{Status: "payment_failed", CurrentPeriodEnd: end}, time.Time{}, false},
	} {
		until, ok := SubscriptionAccessUntil(&tc.sub)
		if ok != tc.grants || !until.Equal(tc.until) {
			t.Errorf("%s: (%v, %v), want (%v, %v)", tc.sub.Status, until, ok, tc.until, tc.grants)
		}
	}
}

// Pause does not move the paid period: once it ends, a paused member has
// no access until a renewal pays for the next one.
func TestPauseDoesNotExtendAccess(t *testing.T) {
	r := newEntRig()
	sub := r.subscribe(r.silver, 9_900, "active")
	if err := r.svc.PauseSubscription(context.Background(), r.fan, sub.ID, r.now.Add(60*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := r.fake.Sub(sub.ID); !got.CurrentPeriodEnd.Equal(sub.CurrentPeriodEnd) || got.Status != "paused" {
		t.Fatalf("pause moved the period or did not pause: %+v", got)
	}
	if ok, _ := r.allowed(t, r.silver); !ok {
		t.Fatal("paused member lost access inside the paid period")
	}
	r.svc.WithClock(func() time.Time { return sub.CurrentPeriodEnd.Add(time.Minute) })
	if ok, _ := r.allowed(t, r.silver); ok {
		t.Fatal("paused member kept access past the paid period")
	}
	if err := r.svc.PauseSubscription(context.Background(), r.fan, sub.ID, r.now.Add(-time.Hour)); err == nil {
		t.Fatal("pausing an already paused subscription should fail")
	}
}

// ---------------------------------------------------------------------------
// Subscribe: idempotency, live set, concurrency
// ---------------------------------------------------------------------------

func TestSubscribeIdempotencyKeyIsScopedToTheSubscriber(t *testing.T) {
	ctx := context.Background()
	svc, fake, _ := newMembershipRig(time.Now())
	creator, a, b := uuid.New(), uuid.New(), uuid.New()
	tier := fake.AddTier(creator, "Fan", 4_900)
	fake.Balances[a], fake.Balances[b] = 100_000, 100_000

	subA, err := svc.Subscribe(ctx, a, creator, tier.ID, "key-1")
	if err != nil {
		t.Fatal(err)
	}
	// B, same key: B's own subscription — never A's.
	subB, err := svc.Subscribe(ctx, b, creator, tier.ID, "key-1")
	if err != nil {
		t.Fatalf("second subscriber with the same key: %v", err)
	}
	if subB.ID == subA.ID || subB.SubscriberID != b {
		t.Fatalf("subscriber B was handed subscriber A's subscription: %+v", subB)
	}
	// A again, same key: a replay, no second charge.
	replay, err := svc.Subscribe(ctx, a, creator, tier.ID, "key-1")
	if err != nil || replay.ID != subA.ID {
		t.Fatalf("replay: %v %+v", err, replay)
	}
	if len(fake.Charges) != 2 {
		t.Fatalf("charges = %v, want one each for A and B", fake.Charges)
	}
	// A, same key, different creator: refused, not replayed.
	other := uuid.New()
	otherTier := fake.AddTier(other, "Fan", 4_900)
	if _, err := svc.Subscribe(ctx, a, other, otherTier.ID, "key-1"); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("key reused for another creator: %v", err)
	}
}

func TestSubscribeAlreadySubscribedUsesTheLiveSet(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   error
	}{
		{"active", postgres.ErrAlreadySubscribed},
		{"cancelled_at_period_end", postgres.ErrAlreadySubscribed},
		{"paused", postgres.ErrAlreadySubscribed},
		{"past_due", postgres.ErrAlreadySubscribed},
		{"grace", postgres.ErrAlreadySubscribed},
		{"cancelled", nil},
		{"expired", nil},
	} {
		t.Run(tc.status, func(t *testing.T) {
			svc, fake, _ := newMembershipRig(time.Now())
			creator, fan := uuid.New(), uuid.New()
			tier := fake.AddTier(creator, "Fan", 4_900)
			fake.Balances[fan] = 100_000
			fake.AddSubscription(postgres.Subscription{SubscriberID: fan, CreatorID: creator, TierID: tier.ID, Status: tc.status,
				CurrentPeriodEnd: time.Now().Add(time.Hour)})
			_, err := svc.Subscribe(context.Background(), fan, creator, tier.ID, "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("existing %s: got %v, want %v", tc.status, err, tc.want)
			}
			// Refused by the live-set read before the insert-and-charge is
			// even attempted (the unique index is the backstop, not the gate).
			if tc.want != nil && fake.SubscribeCalls != 0 {
				t.Fatalf("existing %s: the store's subscribe was reached", tc.status)
			}
		})
	}
}

// Two subscribes that both pass the "already subscribed?" read: the store's
// unique index lets one win; the other is ALREADY_SUBSCRIBED and charged
// nothing.
func TestSubscribeConcurrentOneWins(t *testing.T) {
	svc, fake, _ := newMembershipRig(time.Now())
	creator, fan := uuid.New(), uuid.New()
	tier := fake.AddTier(creator, "Fan", 4_900)
	fake.Balances[fan] = 100_000

	var arrived sync.WaitGroup
	arrived.Add(2)
	fake.AfterGetLive = func() { arrived.Done(); arrived.Wait() } // both read "none" before either writes

	errs := make([]error, 2)
	var done sync.WaitGroup
	for i := 0; i < 2; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			_, errs[i] = svc.Subscribe(context.Background(), fan, creator, tier.ID, "")
		}(i)
	}
	done.Wait()

	ok, conflict := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, postgres.ErrAlreadySubscribed):
			conflict++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || conflict != 1 || len(fake.Charges) != 1 {
		t.Fatalf("ok=%d conflict=%d charges=%v, want 1/1/one charge", ok, conflict, fake.Charges)
	}
}

// The same key sent twice at once: the loser is a replay of the winner.
func TestSubscribeConcurrentSameKeyReplays(t *testing.T) {
	svc, fake, _ := newMembershipRig(time.Now())
	creator, fan := uuid.New(), uuid.New()
	tier := fake.AddTier(creator, "Fan", 4_900)
	fake.Balances[fan] = 100_000

	var arrived sync.WaitGroup
	arrived.Add(2)
	fake.AfterGetLive = func() { arrived.Done(); arrived.Wait() }
	subs := make([]*postgres.Subscription, 2)
	errs := make([]error, 2)
	var done sync.WaitGroup
	for i := 0; i < 2; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			subs[i], errs[i] = svc.Subscribe(context.Background(), fan, creator, tier.ID, "same-key")
		}(i)
	}
	done.Wait()
	if errs[0] != nil || errs[1] != nil || subs[0].ID != subs[1].ID || len(fake.Charges) != 1 {
		t.Fatalf("errs=%v ids=%v/%v charges=%v", errs, subs[0], subs[1], fake.Charges)
	}
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

func TestLifecycleChangesPublishEntitlementChanged(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	svc, fake, pub := newMembershipRig(now)
	creator, fan := uuid.New(), uuid.New()
	bronze := fake.AddTier(creator, "Bronze", 4_900)
	silver := fake.AddTier(creator, "Silver", 9_900)
	fake.Balances[fan] = 1_000_000

	sub, err := svc.Subscribe(ctx, fan, creator, bronze.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		name   string
		do     func() error
		action string
	}{
		{"pause", func() error { return svc.PauseSubscription(ctx, fan, sub.ID, now.Add(48*time.Hour)) }, EntitlementPaused},
		{"resume", func() error { return svc.ResumeSubscription(ctx, fan, sub.ID) }, EntitlementResumed},
		{"upgrade", func() error { _, err := svc.UpgradeSubscription(ctx, fan, sub.ID, silver.ID); return err }, EntitlementUpgraded},
		{"cancel at period end", func() error { return svc.CancelAtPeriodEnd(ctx, fan, sub.ID, "") }, EntitlementCancelPlanned},
		{"cancel now", func() error { return svc.CancelImmediately(ctx, fan, sub.ID, "") }, EntitlementRevoked},
	}
	want := []string{EntitlementGranted}
	if got := pub.Actions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after subscribe: %v", got)
	}
	for _, st := range steps {
		if err := st.do(); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		want = append(want, st.action)
		if got := pub.Actions(); !reflect.DeepEqual(got, want) {
			t.Fatalf("after %s: published %v, want %v", st.name, got, want)
		}
	}
	for _, e := range pub.Events {
		if e.SubscriberID != fan || e.CreatorID != creator || e.SubscriptionID != sub.ID {
			t.Fatalf("event for the wrong pair: %+v", e)
		}
	}

	// DELETE /subscribe/:creatorId on a fresh subscription.
	sub2, err := svc.Subscribe(ctx, fan, creator, bronze.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Unsubscribe(ctx, fan, creator); err != nil {
		t.Fatal(err)
	}
	last := pub.Events[len(pub.Events)-1]
	if last.Action != EntitlementRevoked || last.SubscriptionID != sub2.ID {
		t.Fatalf("unsubscribe published %+v", last)
	}
}

// ---------------------------------------------------------------------------
// Upgrade / downgrade
// ---------------------------------------------------------------------------

func TestProrateUpgradePaise(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end30 := start.AddDate(0, 0, 30)
	end31 := start.AddDate(0, 0, 31)
	day := 24 * time.Hour
	cases := []struct {
		name       string
		paid, newP int64
		start, end time.Time
		now        time.Time
		want       int64
	}{
		{"half of a 30-day period", 4_900, 9_900, start, end30, start.Add(15 * day), 2_500},
		{"a third left, rounds down", 4_900, 9_900, start, end30, start.Add(20 * day), 1_666},           // 1666.67
		{"two thirds left, rounds down", 4_900, 9_900, start, end30, start.Add(10 * day), 3_333},        // 3333.33
		{"31-day month, 7.5 days left", 4_900, 9_900, start, end31, end31.Add(-180 * time.Hour), 1_209}, // 1209.68
		{"one paisa short of a paisa", 2_900, 2_901, start, end30, start.Add(time.Second), 0},           // 0.99999
		{"whole period left", 4_900, 9_900, start, end30, start, 5_000},
		{"before the period starts: capped at the whole", 4_900, 9_900, start, end30, start.Add(-day), 5_000},
		{"period over", 4_900, 9_900, start, end30, end30, 0},
		{"after the period", 4_900, 9_900, start, end30, end30.Add(day), 0},
		{"new price not higher than paid", 9_900, 9_900, start, end30, start.Add(day), 0},
		{"new price lower than paid", 19_900, 9_900, start, end30, start.Add(day), 0},
		{"degenerate period", 4_900, 9_900, start, start, start, 0},
		{"largest prices, 1ns left", 2_900, 299_900, start, end31, end31.Add(-time.Nanosecond), 0},
		{"largest prices, all left", 2_900, 299_900, start, end31, start, 297_000},
	}
	for _, tc := range cases {
		if got := ProrateUpgradePaise(tc.paid, tc.newP, tc.start, tc.end, tc.now); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

type tierChangeRig struct {
	svc                  *Service
	fake                 *membershiptest.Store
	pub                  *membershiptest.Publisher
	creator, fan         uuid.UUID
	bronze, silver, gold *postgres.CreatorTier
	sub                  *postgres.Subscription
	now                  time.Time
}

func newTierChangeRig(onTier func(r *tierChangeRig) *postgres.CreatorTier, paid int64) *tierChangeRig {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	svc, fake, pub := newMembershipRig(now)
	r := &tierChangeRig{svc: svc, fake: fake, pub: pub, creator: uuid.New(), fan: uuid.New(), now: now}
	r.bronze = fake.AddTier(r.creator, "Bronze", 4_900)
	r.silver = fake.AddTier(r.creator, "Silver", 9_900)
	r.gold = fake.AddTier(r.creator, "Gold", 19_900)
	tier := onTier(r)
	r.sub = fake.AddSubscription(postgres.Subscription{
		SubscriberID: r.fan, CreatorID: r.creator, TierID: tier.ID, TierName: tier.Name, PricePaise: paid, Status: "active",
		CurrentPeriodStart: now.Add(-15 * 24 * time.Hour), CurrentPeriodEnd: now.Add(15 * 24 * time.Hour),
	})
	fake.Balances[r.fan] = 100_000
	fake.Balances[r.creator] = 0
	return r
}

func TestUpgradeChargesProRataAndKeepsPeriodEnd(t *testing.T) {
	r := newTierChangeRig(func(r *tierChangeRig) *postgres.CreatorTier { return r.bronze }, 4_900)
	change, err := r.svc.UpgradeSubscription(context.Background(), r.fan, r.sub.ID, r.silver.ID)
	if err != nil {
		t.Fatal(err)
	}
	if change.Result != TierChangeUpgraded || change.ChargedPaise != 2_500 {
		t.Fatalf("change = %+v, want upgraded, 2500 (half of 5000)", change)
	}
	got := r.fake.Sub(r.sub.ID)
	if got.TierID != r.silver.ID || got.PricePaise != 9_900 || !got.CurrentPeriodEnd.Equal(r.sub.CurrentPeriodEnd) {
		t.Fatalf("subscription after upgrade: %+v", got)
	}
	if r.fake.Balances[r.fan] != 100_000-2_500 || r.fake.Balances[r.creator] != 2_500 {
		t.Fatalf("balances: fan %d creator %d", r.fake.Balances[r.fan], r.fake.Balances[r.creator])
	}
	if !reflect.DeepEqual(r.fake.Charges, []int64{2_500}) {
		t.Fatalf("charges = %v", r.fake.Charges)
	}
}

func TestUpgradeShortBalanceChangesNothing(t *testing.T) {
	r := newTierChangeRig(func(r *tierChangeRig) *postgres.CreatorTier { return r.bronze }, 4_900)
	r.fake.Balances[r.fan] = 100
	if _, err := r.svc.UpgradeSubscription(context.Background(), r.fan, r.sub.ID, r.gold.ID); err == nil || err.Error() != "INSUFFICIENT_BALANCE_FOR_UPGRADE" {
		t.Fatalf("got %v", err)
	}
	if got := r.fake.Sub(r.sub.ID); got.TierID != r.bronze.ID || got.PricePaise != 4_900 {
		t.Fatalf("tier changed without payment: %+v", got)
	}
	if len(r.pub.Events) != 0 {
		t.Fatalf("published on a failed upgrade: %v", r.pub.Actions())
	}
}

func TestDowngradeIsDeferredToTheNextPeriod(t *testing.T) {
	r := newTierChangeRig(func(r *tierChangeRig) *postgres.CreatorTier { return r.gold }, 19_900)
	change, err := r.svc.UpgradeSubscription(context.Background(), r.fan, r.sub.ID, r.bronze.ID)
	if err != nil {
		t.Fatal(err)
	}
	if change.Result != TierChangeDowngradeScheduled || change.ChargedPaise != 0 || !change.EffectiveAt.Equal(r.sub.CurrentPeriodEnd) {
		t.Fatalf("change = %+v", change)
	}
	got := r.fake.Sub(r.sub.ID)
	if got.TierID != r.gold.ID || got.PricePaise != 19_900 {
		t.Fatalf("downgrade applied immediately: %+v", got)
	}
	if got.PendingTierID == nil || *got.PendingTierID != r.bronze.ID || got.PendingPricePaise == nil || *got.PendingPricePaise != 4_900 {
		t.Fatalf("pending tier not stored: %+v", got)
	}
	if len(r.fake.Charges) != 0 || r.fake.Balances[r.fan] != 100_000 {
		t.Fatalf("a downgrade moved money: %v", r.fake.Charges)
	}
	// Still gold until the period ends.
	ent, _ := r.svc.CheckEntitlement(context.Background(), EntitlementCheckRequest{SubscriberID: r.fan, CreatorID: r.creator, RequiredTierID: &r.gold.ID})
	if !ent.Allowed {
		t.Fatalf("scheduled downgrade took gold away early: %s", ent.Reason)
	}
	// Choosing the current tier again cancels the scheduled downgrade.
	if c, err := r.svc.UpgradeSubscription(context.Background(), r.fan, r.sub.ID, r.gold.ID); err != nil || c.Result != TierChangeUnchanged {
		t.Fatalf("re-select current tier: %v %+v", err, c)
	}
	if got := r.fake.Sub(r.sub.ID); got.PendingTierID != nil {
		t.Fatalf("pending downgrade not cleared: %+v", got)
	}
}

// Up or down is decided by the tiers' current prices, not by what was paid:
// a Silver member who paid an old low price moving to Bronze is a downgrade.
func TestTierChangeDirectionIsByLevel(t *testing.T) {
	r := newTierChangeRig(func(r *tierChangeRig) *postgres.CreatorTier { return r.silver }, 2_900)
	change, err := r.svc.UpgradeSubscription(context.Background(), r.fan, r.sub.ID, r.bronze.ID)
	if err != nil {
		t.Fatal(err)
	}
	if change.Result != TierChangeDowngradeScheduled {
		t.Fatalf("silver → bronze was treated as %s", change.Result)
	}
}

func TestSubscriptionEventsOnlyForSubscriberAndCreator(t *testing.T) {
	svc, fake, _ := newMembershipRig(time.Now())
	subscriber, creator, stranger := uuid.New(), uuid.New(), uuid.New()
	sub := fake.AddSubscription(postgres.Subscription{SubscriberID: subscriber, CreatorID: creator, Status: "active"})

	for _, viewer := range []uuid.UUID{subscriber, creator} {
		if _, err := svc.GetSubscriptionEvents(context.Background(), viewer, sub.ID, 20, 0); err != nil {
			t.Fatalf("viewer %s: want history, got %v", viewer, err)
		}
	}
	if _, err := svc.GetSubscriptionEvents(context.Background(), stranger, sub.ID, 20, 0); err == nil || err.Error() != "SUBSCRIPTION_NOT_FOUND" {
		t.Fatalf("stranger: want SUBSCRIPTION_NOT_FOUND, got %v", err)
	}
}

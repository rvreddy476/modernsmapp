//go:build integration

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/monetization-service/internal/service/membershiptest"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Paid memberships against PostgreSQL (migration 025), through the real
// store and service with a fake payments-service. openTestPool →
// requireTestDSN REFUSES any database whose name does not end in _test
// (and "app"), before a connection is opened. Written 23 Oct 2026, NOT run in
// this change (no local database): run with MONETIZATION_POSTGRES_DSN
// pointing at e.g. monetization_it_test.

type membershipFixture struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	store    *postgres.Store
	svc      *Service
	payments *membershiptest.Payments
	clock    time.Time
	creator  uuid.UUID
	fans     []uuid.UUID
}

func newMembershipFixture(t *testing.T, fans int) *membershipFixture {
	t.Helper()
	ctx, pool := openTestPool(t)
	store := postgres.New(pool)
	f := &membershipFixture{ctx: ctx, pool: pool, store: store, payments: membershiptest.NewPayments(),
		clock: time.Now().UTC().Truncate(time.Microsecond), creator: uuid.New()}
	elig := membershiptest.Eligible()
	f.svc = New(store, nil).WithMembershipPayments(f.payments, false).
		WithEligibilitySources(EligibilitySources{BirthDates: elig, Followers: elig, Standing: elig}).
		WithClock(func() time.Time { return f.clock })
	for i := 0; i < fans; i++ {
		f.fans = append(f.fans, uuid.New())
	}
	if _, err := pool.Exec(ctx, `INSERT INTO creator_tax_profiles (user_id, pan_encrypted, tax_residency, tds_exempt, verified_at)
		VALUES ($1, 'enc:pan', 'IN', false, NOW())`, f.creator); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		subs := `(SELECT id FROM subscriptions WHERE creator_id = $1)`
		_, _ = pool.Exec(bg, `DELETE FROM membership_payment_inbox WHERE payment_id IN (SELECT id FROM membership_payments WHERE creator_id = $1)`, f.creator)
		_, _ = pool.Exec(bg, `DELETE FROM membership_renewal_reminders WHERE subscription_id IN `+subs, f.creator)
		_, _ = pool.Exec(bg, `DELETE FROM membership_payments WHERE creator_id = $1`, f.creator)
		_, _ = pool.Exec(bg, `DELETE FROM subscription_events WHERE subscription_id IN `+subs, f.creator)
		_, _ = pool.Exec(bg, `DELETE FROM subscriptions WHERE creator_id = $1`, f.creator)
		_, _ = pool.Exec(bg, `DELETE FROM creator_tiers WHERE creator_id = $1`, f.creator)
		_, _ = pool.Exec(bg, `DELETE FROM creator_tax_profiles WHERE user_id = $1`, f.creator)
		for _, u := range append([]uuid.UUID{f.creator}, f.fans...) {
			_, _ = pool.Exec(bg, `DELETE FROM transactions WHERE wallet_id = $1`, u)
			_, _ = pool.Exec(bg, `DELETE FROM creator_ledger WHERE user_id = $1`, u)
		}
	})
	return f
}

func (f *membershipFixture) tier(t *testing.T, name string, price int64) *postgres.CreatorTier {
	t.Helper()
	tier, err := f.svc.CreateTier(f.ctx, f.creator, TierInput{Name: name, PricePaise: price})
	if err != nil {
		t.Fatal(err)
	}
	return tier
}

func (f *membershipFixture) succeed(t *testing.T, paymentID uuid.UUID, eventID string) membership.Applied {
	t.Helper()
	p, err := f.store.GetMembershipPayment(f.ctx, paymentID)
	if err != nil || p == nil {
		t.Fatalf("payment %s: %v", paymentID, err)
	}
	a, err := f.svc.ApplyMembershipPaymentEvent(f.ctx, membership.Event{EventID: eventID, EventType: events.EventPaymentSucceeded,
		IntentID: p.IntentID, PaymentID: p.ID, PayerID: p.PayerID, AmountMinor: p.AmountPaise, Currency: "INR", Status: "succeeded"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *membershipFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	return countRows(f.ctx, t, f.pool, sql, args...)
}

func TestMembershipIntegrationConcurrentJoinsOpenOnePayment(t *testing.T) {
	f := newMembershipFixture(t, 1)
	tier := f.tier(t, "Fan", 4_900)
	const callers = 4
	cos := make([]*Checkout, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			cos[i], errs[i] = f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, "")
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if cos[i].PaymentID != cos[0].PaymentID {
			t.Fatal("concurrent joins opened different payments")
		}
	}
	if n := f.count(t, `SELECT count(*) FROM subscriptions WHERE subscriber_id = $1 AND creator_id = $2`, f.fans[0], f.creator); n != 1 {
		t.Fatalf("%d subscription rows", n)
	}
	if n := f.count(t, `SELECT count(*) FROM membership_payments WHERE creator_id = $1`, f.creator); n != 1 {
		t.Fatalf("%d payment rows", n)
	}
	if n := f.count(t, `SELECT count(*) FROM transactions WHERE wallet_id = $1`, f.fans[0]); n != 0 {
		t.Fatal("a join moved creator_ledger money")
	}
}

func TestMembershipIntegrationIdempotencyKeyPerSubscriber(t *testing.T) {
	f := newMembershipFixture(t, 2)
	tier := f.tier(t, "Fan", 4_900)
	a, err := f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, "shared-key")
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.svc.Subscribe(f.ctx, f.fans[1], f.creator, tier.ID, "shared-key")
	if err != nil || b.SubscriptionID == a.SubscriptionID {
		t.Fatalf("second subscriber: %v", err)
	}
	again, err := f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, "shared-key")
	if err != nil || again.PaymentID != a.PaymentID {
		t.Fatalf("replay: %v", err)
	}
}

func TestMembershipIntegrationEventAppliedOnceWithEarnings(t *testing.T) {
	f := newMembershipFixture(t, 1)
	tier := f.tier(t, "Fan", 4_900)
	co, err := f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT count(*) FROM subscriptions WHERE id = $1 AND status = 'pending_payment' AND NOT counted`, co.SubscriptionID); n != 1 {
		t.Fatal("join is not pending_payment")
	}
	if a := f.succeed(t, co.PaymentID, "evt-"+uuid.NewString()); a.Decision.Outcome != membership.OutcomeActivated {
		t.Fatalf("outcome %s", a.Decision.Outcome)
	}
	ev := "evt-" + uuid.NewString()
	f.succeed(t, co.PaymentID, ev)
	if a := f.succeed(t, co.PaymentID, ev); a.Decision.Outcome != membership.OutcomeDuplicate {
		t.Fatalf("redelivery: %s", a.Decision.Outcome)
	}
	if n := f.count(t, `SELECT count(*) FROM transactions WHERE wallet_id = $1 AND type = 'earning' AND reference_type = 'subscription' AND reference_id = $2`,
		f.creator, co.PaymentID.String()); n != 1 {
		t.Fatalf("%d earning rows", n)
	}
	if b, _ := ledgerState(f.ctx, t, f.pool, f.creator); b != 4_900 {
		t.Fatalf("creator balance %d", b)
	}
	if n := f.count(t, `SELECT subscriber_count FROM creator_tiers WHERE id = $1`, tier.ID); n != 1 {
		t.Fatalf("subscriber_count %d", n)
	}
	ent, err := f.svc.CheckEntitlement(f.ctx, EntitlementCheckRequest{SubscriberID: f.fans[0], CreatorID: f.creator})
	if err != nil || !ent.Allowed {
		t.Fatalf("entitlement %v %+v", err, ent)
	}
}

func TestMembershipIntegrationMismatchAndLateCapture(t *testing.T) {
	f := newMembershipFixture(t, 1)
	tier := f.tier(t, "Fan", 4_900)
	co, _ := f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, "")
	p, _ := f.store.GetMembershipPayment(f.ctx, co.PaymentID)
	a, err := f.svc.ApplyMembershipPaymentEvent(f.ctx, membership.Event{EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentSucceeded,
		IntentID: p.IntentID, PaymentID: p.ID, PayerID: p.PayerID, AmountMinor: 100, Currency: "INR"})
	if err != nil || a.Decision.Outcome != membership.OutcomeMismatch {
		t.Fatalf("%v %s", err, a.Decision.Outcome)
	}
	if n := f.count(t, `SELECT count(*) FROM membership_payment_inbox WHERE payment_id = $1 AND outcome = 'mismatch'`, co.PaymentID); n != 1 {
		t.Fatal("mismatch not recorded in the inbox")
	}
	// Swept, then captured: refunded under membership-late:{id}, no earnings.
	if _, err := f.store.SweepStalePendingJoins(f.ctx, f.clock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if a := f.succeed(t, co.PaymentID, "evt-"+uuid.NewString()); a.Decision.Outcome != membership.OutcomeLateCapture {
		t.Fatalf("outcome %s", a.Decision.Outcome)
	}
	if n := f.count(t, `SELECT count(*) FROM membership_payments WHERE id = $1 AND late_capture AND refund_key = $2 AND refund_status = 'pending'`,
		co.PaymentID, "membership-late:"+co.PaymentID.String()); n != 1 {
		t.Fatal("late capture refund not recorded")
	}
	if n := f.count(t, `SELECT count(*) FROM transactions WHERE wallet_id = $1`, f.creator); n != 0 {
		t.Fatal("a late capture earned")
	}
}

func TestMembershipIntegrationRenewalOncePerPeriodAndRefund(t *testing.T) {
	f := newMembershipFixture(t, 1)
	tier := f.tier(t, "Fan", 4_900)
	co, _ := f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, "")
	f.succeed(t, co.PaymentID, "evt-"+uuid.NewString())
	sub, _ := f.store.GetSubscriptionByID(f.ctx, co.SubscriptionID)
	end := sub.CurrentPeriodEnd
	rc, err := f.svc.Renew(f.ctx, f.fans[0], co.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := f.svc.Renew(f.ctx, f.fans[0], co.SubscriptionID); again.PaymentID != rc.PaymentID {
		t.Fatal("two renewal rows for one period")
	}
	f.succeed(t, rc.PaymentID, "evt-"+uuid.NewString())
	sub, _ = f.store.GetSubscriptionByID(f.ctx, co.SubscriptionID)
	if !sub.CurrentPeriodEnd.Equal(end.AddDate(0, 1, 0)) {
		t.Fatalf("period end %v, want %v", sub.CurrentPeriodEnd, end.AddDate(0, 1, 0))
	}
	// Support refunds the renewal; payments reports it settled.
	if _, err := f.svc.RefundMembershipPayment(f.ctx, uuid.New(), rc.PaymentID, "support"); err != nil {
		t.Fatal(err)
	}
	rp, _ := f.store.GetMembershipPayment(f.ctx, rc.PaymentID)
	if _, err := f.svc.ApplyMembershipPaymentEvent(f.ctx, membership.Event{EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentRefunded,
		IntentID: rp.IntentID, PaymentID: rp.ID, AmountMinor: 4_900, Status: "refunded"}); err != nil {
		t.Fatal(err)
	}
	sub, _ = f.store.GetSubscriptionByID(f.ctx, co.SubscriptionID)
	if !sub.CurrentPeriodEnd.Equal(end) || sub.Status != "active" {
		t.Fatalf("after refund: %s %v, want active %v", sub.Status, sub.CurrentPeriodEnd, end)
	}
	if n := f.count(t, `SELECT count(*) FROM transactions WHERE wallet_id = $1 AND type = 'earning' AND reference_id = $2 AND status = 'reversed'`,
		f.creator, rc.PaymentID.String()); n != 1 {
		t.Fatal("renewal earning not reversed")
	}
	if b, _ := ledgerState(f.ctx, t, f.pool, f.creator); b != 4_900 {
		t.Fatalf("creator balance %d after the refund", b)
	}
	// The refunded period can be bought again (the period index excludes it).
	if again, err := f.svc.Renew(f.ctx, f.fans[0], co.SubscriptionID); err != nil || again.PaymentID == rc.PaymentID {
		t.Fatalf("re-buy: %v", err)
	}
}

func TestMembershipIntegrationGraceExpiryCountAndReminders(t *testing.T) {
	f := newMembershipFixture(t, 1)
	tier := f.tier(t, "Fan", 4_900)
	co, _ := f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, "")
	f.succeed(t, co.PaymentID, "evt-"+uuid.NewString())
	sub, _ := f.store.GetSubscriptionByID(f.ctx, co.SubscriptionID)
	end := sub.CurrentPeriodEnd

	due, err := f.store.DueRenewalReminders(f.ctx, end.Add(-70*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range due {
		if r.ID == co.SubscriptionID && r.DaysLeft == 3 {
			found = true
		}
	}
	if !found {
		t.Fatal("3-day reminder not due")
	}
	if ok, _ := f.store.ClaimRenewalReminder(f.ctx, co.SubscriptionID, end, 3); !ok {
		t.Fatal("first claim refused")
	}
	if ok, _ := f.store.ClaimRenewalReminder(f.ctx, co.SubscriptionID, end, 3); ok {
		t.Fatal("second claim for the same period accepted")
	}

	// Both edges of the window, through the real query: before the 72 h lead
	// nothing is due, and inside the last 24 h the "ends today" reminder is due
	// even though the 3-day one for the same period is already claimed.
	early, err := f.store.DueRenewalReminders(f.ctx, end.Add(-73*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range early {
		if r.ID == co.SubscriptionID {
			t.Fatalf("reminder due %d day(s) out, before the 72 h lead", r.DaysLeft)
		}
	}
	today, err := f.store.DueRenewalReminders(f.ctx, end.Add(-12*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	foundToday := false
	for _, r := range today {
		if r.ID == co.SubscriptionID {
			if r.DaysLeft != 0 {
				t.Fatalf("12 h before the end: days_left %d, want 0", r.DaysLeft)
			}
			foundToday = true
		}
	}
	if !foundToday {
		t.Fatal("ends-today reminder not due 12 h before the period end")
	}

	graced, err := f.store.StartGraceForEndedPeriods(f.ctx, end.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var g *postgres.Subscription
	for i := range graced {
		if graced[i].ID == co.SubscriptionID {
			g = &graced[i]
		}
	}
	if g == nil || g.Status != "grace" || g.GracePeriodEnd == nil || !g.GracePeriodEnd.Equal(end.Add(72*time.Hour)) {
		t.Fatalf("grace: %+v", g)
	}
	expired, err := f.store.ExpireEndedMemberships(f.ctx, end.Add(73*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, e := range expired {
		ok = ok || (e.ID == co.SubscriptionID && e.From == "grace" && e.WasCounted)
	}
	if !ok {
		t.Fatalf("not expired from grace: %+v", expired)
	}
	if n := f.count(t, `SELECT subscriber_count FROM creator_tiers WHERE id = $1`, tier.ID); n != 0 {
		t.Fatalf("subscriber_count %d after expiry", n)
	}
}

func TestMembershipIntegrationMigrationGuards(t *testing.T) {
	f := newMembershipFixture(t, 1)
	tier := f.tier(t, "Fan", 4_900)
	if _, err := f.svc.Subscribe(f.ctx, f.fans[0], f.creator, tier.ID, ""); err != nil {
		t.Fatal(err)
	}
	// A second pending join for the pair is refused by the partial index.
	_, err := f.pool.Exec(f.ctx, `INSERT INTO subscriptions (subscriber_id, creator_id, tier_id, tier_name, price, currency, status)
		VALUES ($1, $2, $3, 'Fan', 4900, 'INR', 'pending_payment')`, f.fans[0], f.creator, tier.ID)
	if err == nil {
		t.Fatal("two pending joins for one pair")
	}
	// fee + share must equal the amount.
	_, err = f.pool.Exec(f.ctx, `INSERT INTO membership_payments (id, subscription_id, payer_id, creator_id, kind, tier_id, tier_name,
		tier_price_paise, amount_paise, fee_paise, creator_share_paise, period_start, period_end, idempotency_key)
		SELECT gen_random_uuid(), id, subscriber_id, creator_id, 'renewal', tier_id, 'Fan', 4900, 4900, 0, 4000, NOW(), NOW(), gen_random_uuid()::text
		FROM subscriptions WHERE creator_id = $1 LIMIT 1`, f.creator)
	if err == nil {
		t.Fatal("fee + share != amount accepted")
	}
}

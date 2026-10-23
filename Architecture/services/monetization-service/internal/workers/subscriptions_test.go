package workers

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The subscription workers against an in-memory store that keeps the
// Postgres store's contract: RenewSubscription is one transaction (charge,
// key, extension land together or not at all) and refuses a key it has
// already recorded; the due query returns active rows and past_due rows
// whose last failure is at or before retryBefore.

type fakeSubRow struct {
	postgres.SubscriptionForRenewal
	paymentFailedAt *time.Time
	graceEnd        *time.Time
	autoRenew       bool
}

type fakeSubStore struct {
	mu       sync.Mutex
	rows     map[uuid.UUID]*fakeSubRow
	tiers    map[uuid.UUID]*postgres.CreatorTier
	balances map[uuid.UUID]int64
	keys     map[string]bool
	charges  []int64
	events   []string
	renewals []postgres.RenewalInput

	// failExtendOnce makes the next RenewSubscription fail after charging,
	// inside its transaction — so the charge is rolled back with it.
	failExtendOnce bool
	// staleDue, when set, is returned by the due query instead of the live rows.
	staleDue []postgres.SubscriptionForRenewal
	// lastRetryBefore is what the worker passed as retryBefore.
	lastRetryBefore time.Time
}

func newFakeSubStore() *fakeSubStore {
	return &fakeSubStore{
		rows:     map[uuid.UUID]*fakeSubRow{},
		tiers:    map[uuid.UUID]*postgres.CreatorTier{},
		balances: map[uuid.UUID]int64{},
		keys:     map[string]bool{},
	}
}

func (f *fakeSubStore) GetSubscriptionsDueForRenewal(_ context.Context, before, retryBefore time.Time) ([]postgres.SubscriptionForRenewal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRetryBefore = retryBefore
	if f.staleDue != nil {
		return f.staleDue, nil
	}
	var out []postgres.SubscriptionForRenewal
	for _, r := range f.rows {
		if !r.autoRenew || !r.CurrentPeriodEnd.Before(before) {
			continue
		}
		due := r.Status == "active" ||
			(r.Status == "past_due" && (r.paymentFailedAt == nil || !r.paymentFailedAt.After(retryBefore)))
		if due {
			out = append(out, r.SubscriptionForRenewal)
		}
	}
	return out, nil
}

func (f *fakeSubStore) GetCreatorTierWithBillingPeriod(_ context.Context, id uuid.UUID) (*postgres.CreatorTier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tiers[id]
	if !ok {
		return nil, nil
	}
	c := *t
	return &c, nil
}

func (f *fakeSubStore) RenewSubscription(_ context.Context, in postgres.RenewalInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.keys[in.IdempotencyKey] {
		return postgres.ErrRenewalAlreadyApplied
	}
	r, ok := f.rows[in.SubscriptionID]
	if !ok || (r.Status != "active" && r.Status != "past_due") {
		return postgres.ErrSubscriptionStateChanged
	}
	// --- transaction begins ---
	if f.balances[in.SubscriberID] < in.PricePaise {
		return postgres.ErrInsufficientFunds
	}
	f.balances[in.SubscriberID] -= in.PricePaise
	f.balances[in.CreatorID] += in.PricePaise
	if f.failExtendOnce {
		f.failExtendOnce = false
		// Rollback: the charge does not survive the failed extension.
		f.balances[in.SubscriberID] += in.PricePaise
		f.balances[in.CreatorID] -= in.PricePaise
		return errors.New("extend: connection reset")
	}
	f.keys[in.IdempotencyKey] = true
	f.charges = append(f.charges, in.PricePaise)
	f.renewals = append(f.renewals, in)
	r.CurrentPeriodEnd = in.NewPeriodEnd
	r.Status, r.RetryCount, r.paymentFailedAt, r.graceEnd = "active", 0, nil, nil
	r.TierID, r.TierName, r.PricePaise = in.TierID, in.TierName, in.PricePaise
	r.PendingTierID, r.PendingPricePaise = nil, nil
	return nil
}

func (f *fakeSubStore) MarkRenewalPastDue(_ context.Context, id uuid.UUID, end time.Time, retries int, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.rows[id]
	if r == nil || !r.CurrentPeriodEnd.Equal(end) || r.RetryCount != retries-1 {
		return postgres.ErrSubscriptionStateChanged
	}
	r.Status, r.RetryCount = "past_due", retries
	n := now
	r.paymentFailedAt = &n
	return nil
}

func (f *fakeSubStore) EnterRenewalGrace(_ context.Context, id uuid.UUID, end time.Time, retries int, now, graceEnd time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.rows[id]
	if r == nil || !r.CurrentPeriodEnd.Equal(end) || r.RetryCount != retries-1 {
		return postgres.ErrSubscriptionStateChanged
	}
	r.Status, r.RetryCount = "grace", retries
	n, g := now, graceEnd
	r.paymentFailedAt, r.graceEnd = &n, &g
	return nil
}

func (f *fakeSubStore) CancelAtPeriodEnd(_ context.Context, id uuid.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[id].Status = "cancelled_at_period_end"
	return nil
}

func (f *fakeSubStore) InsertSubscriptionEvent(_ context.Context, _ uuid.UUID, eventType, _, _ string, _ json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, eventType)
	return nil
}

func (f *fakeSubStore) asSub(r *fakeSubRow) postgres.Subscription {
	return postgres.Subscription{ID: r.ID, SubscriberID: r.SubscriberID, CreatorID: r.CreatorID, TierID: r.TierID,
		Status: r.Status, CurrentPeriodEnd: r.CurrentPeriodEnd, GracePeriodEnd: r.graceEnd}
}

func (f *fakeSubStore) GetGracePeriodExpired(_ context.Context, now time.Time) ([]postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.Subscription
	for _, r := range f.rows {
		if r.Status == "grace" && r.graceEnd != nil && !r.graceEnd.After(now) {
			out = append(out, f.asSub(r))
		}
	}
	return out, nil
}

func (f *fakeSubStore) SetSubscriptionStatus(_ context.Context, id uuid.UUID, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[id].Status = status
	return nil
}

func (f *fakeSubStore) ExpireEndedCancellations(_ context.Context, now time.Time) ([]postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.Subscription
	for _, r := range f.rows {
		if r.Status == "cancelled_at_period_end" && !r.CurrentPeriodEnd.After(now) {
			r.Status = "expired"
			out = append(out, f.asSub(r))
		}
	}
	return out, nil
}

func (f *fakeSubStore) GetPausedSubscriptionsToResume(_ context.Context, _ time.Time) ([]postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.Subscription
	for _, r := range f.rows {
		if r.Status == "paused" {
			out = append(out, f.asSub(r))
		}
	}
	return out, nil
}

func (f *fakeSubStore) ResumeSubscription(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[id].Status = "active"
	return nil
}

func (f *fakeSubStore) status(id uuid.UUID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[id].Status
}

type published struct {
	kind, action string
	subID        uuid.UUID
}

type fakePublisher struct {
	mu  sync.Mutex
	got []published
}

func (p *fakePublisher) add(kind, action string, id uuid.UUID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, published{kind, action, id})
}
func (p *fakePublisher) PublishSubscriptionRenewed(_ context.Context, id, _, _ uuid.UUID, _ time.Time, _ int64, _ string) error {
	p.add("renewed", "", id)
	return nil
}
func (p *fakePublisher) PublishSubscriptionGraceStarted(_ context.Context, id, _, _ uuid.UUID, _ time.Time, _ int) error {
	p.add("grace_started", "", id)
	return nil
}
func (p *fakePublisher) PublishSubscriptionExpired(_ context.Context, id, _, _ uuid.UUID, reason string) error {
	p.add("expired", reason, id)
	return nil
}
func (p *fakePublisher) PublishSubscriptionResumed(_ context.Context, id, _, _ uuid.UUID) error {
	p.add("resumed", "", id)
	return nil
}
func (p *fakePublisher) PublishEntitlementChanged(_ context.Context, id, _, _ uuid.UUID, action string) error {
	p.add("entitlement.changed", action, id)
	return nil
}

func (p *fakePublisher) entitlementActions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, e := range p.got {
		if e.kind == "entitlement.changed" {
			out = append(out, e.action)
		}
	}
	return out
}

var _ subscriptionStore = (*fakeSubStore)(nil)
var _ subscriptionStore = (*postgres.Store)(nil)

type renewRig struct {
	st      *fakeSubStore
	pub     *fakePublisher
	sub     *fakeSubRow
	tier    *postgres.CreatorTier
	now     time.Time
	fan     uuid.UUID
	creator uuid.UUID
}

func newRenewRig(balance int64) *renewRig {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	st := newFakeSubStore()
	creator, fan := uuid.New(), uuid.New()
	tier := &postgres.CreatorTier{ID: uuid.New(), CreatorID: creator, Name: "Silver", PricePaise: 9_900, IsActive: true, BillingPeriod: "monthly"}
	st.tiers[tier.ID] = tier
	row := &fakeSubRow{autoRenew: true, SubscriptionForRenewal: postgres.SubscriptionForRenewal{
		ID: uuid.New(), SubscriberID: fan, CreatorID: creator, TierID: tier.ID, TierName: tier.Name,
		PricePaise: 9_900, Currency: "INR", Status: "active", CurrentPeriodEnd: now.Add(12 * time.Hour),
	}}
	st.rows[row.ID] = row
	st.balances[fan] = balance
	return &renewRig{st: st, pub: &fakePublisher{}, sub: row, tier: tier, now: now, fan: fan, creator: creator}
}

func (r *renewRig) tick(at time.Time) { renewSubscriptions(context.Background(), r.st, r.pub, at) }

// A renewal whose extension fails after the charge must not leave the
// charge behind, and a later retry of the same period charges once.
func TestRenewalChargesOncePerPeriodUnderRetry(t *testing.T) {
	r := newRenewRig(100_000)
	oldEnd := r.sub.CurrentPeriodEnd
	r.st.failExtendOnce = true

	r.tick(r.now) // charge + extend fail together
	if len(r.st.charges) != 0 || r.st.balances[r.fan] != 100_000 || !r.sub.CurrentPeriodEnd.Equal(oldEnd) {
		t.Fatalf("failed renewal left state behind: charges=%v balance=%d end=%v", r.st.charges, r.st.balances[r.fan], r.sub.CurrentPeriodEnd)
	}
	if r.st.status(r.sub.ID) != "active" {
		t.Fatalf("a database failure was counted as a payment failure: %s", r.st.status(r.sub.ID))
	}

	r.tick(r.now.Add(time.Hour)) // the retry
	if !reflect.DeepEqual(r.st.charges, []int64{9_900}) || r.st.balances[r.fan] != 100_000-9_900 {
		t.Fatalf("retry: charges=%v balance=%d", r.st.charges, r.st.balances[r.fan])
	}
	if want := oldEnd.AddDate(0, 1, 0); !r.sub.CurrentPeriodEnd.Equal(want) {
		t.Fatalf("period end %v, want %v (one month on from the old end)", r.sub.CurrentPeriodEnd, want)
	}

	// A second worker (or a stale read) tries the same period again: the
	// per-period key refuses it before any money moves.
	r.st.staleDue = []postgres.SubscriptionForRenewal{{
		ID: r.sub.ID, SubscriberID: r.fan, CreatorID: r.creator, TierID: r.tier.ID, TierName: r.tier.Name,
		PricePaise: 9_900, Currency: "INR", Status: "active", CurrentPeriodEnd: oldEnd,
	}}
	r.st.rows[r.sub.ID].CurrentPeriodEnd = oldEnd // as the stale reader saw it
	r.tick(r.now.Add(2 * time.Hour))
	if len(r.st.charges) != 1 {
		t.Fatalf("the same period was charged again: %v", r.st.charges)
	}
	if got := r.st.renewals[0].IdempotencyKey; got != postgres.RenewalIdempotencyKey(r.sub.ID, oldEnd) {
		t.Fatalf("renewal key %q is not the per-period key", got)
	}
}

func TestRenewalPastDueIsRetriedThenGraceThenCancelled(t *testing.T) {
	r := newRenewRig(0)
	id := r.sub.ID
	at := r.now

	r.tick(at)
	if r.st.status(id) != "past_due" || r.sub.RetryCount != 1 {
		t.Fatalf("first failure: %s retries=%d", r.st.status(id), r.sub.RetryCount)
	}
	if want := at.Add(-renewalRetryInterval); !r.st.lastRetryBefore.Equal(want) {
		t.Fatalf("retryBefore = %v, want now - %v", r.st.lastRetryBefore, renewalRetryInterval)
	}

	r.tick(at.Add(time.Hour)) // too soon: not retried
	if r.sub.RetryCount != 1 {
		t.Fatalf("retried before the interval: retries=%d", r.sub.RetryCount)
	}

	at = at.Add(renewalRetryInterval)
	r.tick(at)
	if r.st.status(id) != "past_due" || r.sub.RetryCount != 2 {
		t.Fatalf("second failure: %s retries=%d", r.st.status(id), r.sub.RetryCount)
	}

	at = at.Add(renewalRetryInterval)
	r.tick(at)
	if r.st.status(id) != "grace" || r.sub.RetryCount != maxRenewalRetries {
		t.Fatalf("third failure: %s retries=%d, want grace", r.st.status(id), r.sub.RetryCount)
	}
	if r.sub.graceEnd == nil || !r.sub.graceEnd.Equal(at.Add(gracePeriodDuration)) {
		t.Fatalf("grace end %v", r.sub.graceEnd)
	}

	r.tick(at.Add(renewalRetryInterval)) // grace is not charged again
	if r.st.status(id) != "grace" || len(r.st.charges) != 0 {
		t.Fatalf("grace row was retried: %s %v", r.st.status(id), r.st.charges)
	}

	expireGracePeriods(context.Background(), r.st, r.pub, at.Add(gracePeriodDuration))
	if r.st.status(id) != "cancelled" {
		t.Fatalf("grace expiry: %s", r.st.status(id))
	}
	if got := r.pub.entitlementActions(); !reflect.DeepEqual(got, []string{"grace_started", "revoked"}) {
		t.Fatalf("entitlement events %v", got)
	}
	if want := []string{"payment_failed", "payment_failed", "grace_started", "grace_expired"}; !reflect.DeepEqual(r.st.events, want) {
		t.Fatalf("subscription events %v, want %v", r.st.events, want)
	}
}

// A past_due subscription that pays on retry is active again, and the
// failures are forgotten.
func TestRenewalPastDueRecoversOnRetry(t *testing.T) {
	r := newRenewRig(0)
	r.tick(r.now)
	r.st.balances[r.fan] = 100_000
	r.tick(r.now.Add(renewalRetryInterval))
	if r.st.status(r.sub.ID) != "active" || r.sub.RetryCount != 0 || len(r.st.charges) != 1 {
		t.Fatalf("recovery: %s retries=%d charges=%v", r.st.status(r.sub.ID), r.sub.RetryCount, r.st.charges)
	}
	if got := r.pub.entitlementActions(); !reflect.DeepEqual(got, []string{"renewed"}) {
		t.Fatalf("entitlement events %v", got)
	}
}

func TestRenewalSkipsAnInactiveTier(t *testing.T) {
	r := newRenewRig(100_000)
	r.tier.IsActive = false
	r.tick(r.now)
	if len(r.st.charges) != 0 {
		t.Fatalf("a retired tier was charged: %v", r.st.charges)
	}
	if r.st.status(r.sub.ID) != "cancelled_at_period_end" {
		t.Fatalf("status %s, want cancelled_at_period_end", r.st.status(r.sub.ID))
	}
	// And at period end it expires.
	expireEndedCancellations(context.Background(), r.st, r.pub, r.sub.CurrentPeriodEnd)
	if r.st.status(r.sub.ID) != "expired" {
		t.Fatalf("status %s, want expired", r.st.status(r.sub.ID))
	}
}

func TestRenewalRefusesANonMonthlyTier(t *testing.T) {
	r := newRenewRig(100_000)
	r.tier.BillingPeriod = "yearly"
	r.tick(r.now)
	if len(r.st.charges) != 0 || r.st.status(r.sub.ID) != "active" {
		t.Fatalf("non-monthly tier: charges=%v status=%s", r.st.charges, r.st.status(r.sub.ID))
	}
}

func TestRenewalAppliesAScheduledDowngrade(t *testing.T) {
	r := newRenewRig(100_000)
	bronze := &postgres.CreatorTier{ID: uuid.New(), CreatorID: r.creator, Name: "Bronze", PricePaise: 4_900, IsActive: true, BillingPeriod: "monthly"}
	r.st.tiers[bronze.ID] = bronze
	price := int64(4_900)
	r.sub.PendingTierID, r.sub.PendingPricePaise = &bronze.ID, &price

	r.tick(r.now)
	if !reflect.DeepEqual(r.st.charges, []int64{4_900}) {
		t.Fatalf("charged %v, want the pending tier's 4900", r.st.charges)
	}
	if r.sub.TierID != bronze.ID || r.sub.PendingTierID != nil {
		t.Fatalf("downgrade not applied: tier=%v pending=%v", r.sub.TierID, r.sub.PendingTierID)
	}
	if got := r.pub.entitlementActions(); !reflect.DeepEqual(got, []string{"downgraded"}) {
		t.Fatalf("entitlement events %v", got)
	}
}

func TestNextMonthlyPeriod(t *testing.T) {
	end := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	// Renewing early: the next period starts at the old end.
	if s, e := nextMonthlyPeriod(end, end.Add(-12*time.Hour)); !s.Equal(end) || !e.Equal(end.AddDate(0, 1, 0)) {
		t.Fatalf("early: %v → %v", s, e)
	}
	// Lapsed (paused or past_due beyond the end): starts now, the gap is not billed.
	now := end.Add(40 * 24 * time.Hour)
	if s, e := nextMonthlyPeriod(end, now); !s.Equal(now) || !e.Equal(now.AddDate(0, 1, 0)) {
		t.Fatalf("lapsed: %v → %v", s, e)
	}
}

func TestCancelledAtPeriodEndExpiresAtPeriodEnd(t *testing.T) {
	st := newFakeSubStore()
	pub := &fakePublisher{}
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	ended := &fakeSubRow{SubscriptionForRenewal: postgres.SubscriptionForRenewal{ID: uuid.New(), Status: "cancelled_at_period_end", CurrentPeriodEnd: now.Add(-time.Minute)}}
	running := &fakeSubRow{SubscriptionForRenewal: postgres.SubscriptionForRenewal{ID: uuid.New(), Status: "cancelled_at_period_end", CurrentPeriodEnd: now.Add(time.Hour)}}
	st.rows[ended.ID], st.rows[running.ID] = ended, running

	expireEndedCancellations(context.Background(), st, pub, now)
	if st.status(ended.ID) != "expired" || st.status(running.ID) != "cancelled_at_period_end" {
		t.Fatalf("ended=%s running=%s", st.status(ended.ID), st.status(running.ID))
	}
	if got := pub.entitlementActions(); !reflect.DeepEqual(got, []string{"revoked"}) {
		t.Fatalf("entitlement events %v", got)
	}
	if !reflect.DeepEqual(st.events, []string{"period_ended"}) {
		t.Fatalf("subscription events %v", st.events)
	}
}

func TestAutoResumePublishesEntitlementChanged(t *testing.T) {
	st := newFakeSubStore()
	pub := &fakePublisher{}
	row := &fakeSubRow{SubscriptionForRenewal: postgres.SubscriptionForRenewal{ID: uuid.New(), Status: "paused"}}
	st.rows[row.ID] = row
	resumePausedSubscriptions(context.Background(), st, pub, time.Now())
	if st.status(row.ID) != "active" {
		t.Fatalf("status %s", st.status(row.ID))
	}
	if got := pub.entitlementActions(); !reflect.DeepEqual(got, []string{"resumed"}) {
		t.Fatalf("entitlement events %v", got)
	}
}

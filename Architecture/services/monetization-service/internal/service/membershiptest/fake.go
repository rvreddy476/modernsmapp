// Package membershiptest is an in-memory service.MembershipStore (and the
// membership workers' store) for unit tests of the membership rules, plus a
// fake payments-service, recording publishers and eligibility sources. It
// mirrors the Postgres store's contract: partial tier updates, the
// active-tier limit, one live subscription per (subscriber, creator), one
// pending join per pair, idempotency keys scoped to the subscriber, one open
// payment per (subscription, kind, period), and the signed payment event
// applied exactly once through the SAME decision table (membership.Decide)
// the Postgres store uses. Not for production use.
package membershiptest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/atpost/monetization-service/internal/events"
	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/monetization-service/internal/payments"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Event is one subscription_events row the fake recorded.
type Event struct {
	SubscriptionID uuid.UUID
	Type           string
	OldStatus      string
	NewStatus      string
}

// EarningRow is one creator earning (or refund) the fake recorded.
type EarningRow struct {
	CreatorID uuid.UUID
	PaymentID uuid.UUID
	Amount    int64
	Kind      string // earning | refund
	Reversed  bool
}

type reminderKey struct {
	sub      uuid.UUID
	end      int64
	daysLeft int
}

// Store is the fake. The zero value is not usable; call New.
type Store struct {
	mu       sync.Mutex
	Tiers    map[uuid.UUID]*postgres.CreatorTier
	Subs     map[uuid.UUID]*postgres.Subscription
	Payments map[uuid.UUID]*membership.Payment
	// Inbox is event id → outcome.
	Inbox map[string]membership.Outcome
	// Balances is the creator ledger balance per user.
	Balances map[uuid.UUID]int64
	Earnings []EarningRow
	Events   []Event
	Tax      map[uuid.UUID]*postgres.CreatorTaxProfile
	// AllPANVerified answers a verified PAN for every user without a row.
	AllPANVerified bool

	reminders map[reminderKey]bool
	// RefundAttempts counts Mark* calls per payment.
	RefundAttempts map[uuid.UUID]int
	// CommandIDs records accepted refund commands per payment.
	CommandIDs map[uuid.UUID]uuid.UUID

	// AfterGetLive, if set, runs (unlocked) at the end of GetLiveSubscription.
	AfterGetLive func()
	// LastTierPatch is the patch UpdateTierPartial was last given.
	LastTierPatch *postgres.TierPatch
	// JoinCalls counts calls to CreateMembershipJoin.
	JoinCalls int
}

// New returns an empty fake.
func New() *Store {
	return &Store{
		Tiers:          map[uuid.UUID]*postgres.CreatorTier{},
		Subs:           map[uuid.UUID]*postgres.Subscription{},
		Payments:       map[uuid.UUID]*membership.Payment{},
		Inbox:          map[string]membership.Outcome{},
		Balances:       map[uuid.UUID]int64{},
		Tax:            map[uuid.UUID]*postgres.CreatorTaxProfile{},
		reminders:      map[reminderKey]bool{},
		RefundAttempts: map[uuid.UUID]int{},
		CommandIDs:     map[uuid.UUID]uuid.UUID{},
	}
}

// AddTier stores a tier (active, monthly) and returns it.
func (f *Store) AddTier(creatorID uuid.UUID, name string, pricePaise int64) *postgres.CreatorTier {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &postgres.CreatorTier{
		ID: uuid.New(), CreatorID: creatorID, Name: name, PricePaise: pricePaise,
		Currency: "INR", Perks: json.RawMessage(`[]`), IsActive: true, BillingPeriod: "monthly",
	}
	f.Tiers[t.ID] = t
	return t
}

// AddSubscription stores a subscription as given (ID assigned if nil). A
// live one with no ActivatedAt is given one, and is counted unless the test
// says otherwise through the returned pointer.
func (f *Store) AddSubscription(sub postgres.Subscription) *postgres.Subscription {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sub.ID == uuid.Nil {
		sub.ID = uuid.New()
	}
	if sub.Currency == "" {
		sub.Currency = "INR"
	}
	if sub.ActivatedAt == nil && sub.Status != membership.StatusPendingPayment {
		t := sub.CurrentPeriodStart
		sub.ActivatedAt = &t
	}
	s := sub
	f.Subs[s.ID] = &s
	c := s
	return &c
}

// SetCounted marks a stored subscription counted (and bumps its tier).
func (f *Store) SetCounted(subID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.Subs[subID]
	if !s.Counted {
		s.Counted = true
		if t, ok := f.Tiers[s.TierID]; ok {
			t.SubscriberCount++
		}
	}
}

// Sub returns a copy of a stored subscription.
func (f *Store) Sub(id uuid.UUID) postgres.Subscription {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.Subs[id]
}

// Payment returns a copy of a stored payment.
func (f *Store) Payment(id uuid.UUID) membership.Payment {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.Payments[id]
}

// TierCount returns a tier's subscriber_count.
func (f *Store) TierCount(id uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Tiers[id].SubscriberCount
}

// PaymentsFor returns the subscription's payments, oldest first.
func (f *Store) PaymentsFor(subID uuid.UUID) []membership.Payment {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []membership.Payment
	for _, p := range f.Payments {
		if p.SubscriptionID == subID {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// EventTypes returns the recorded event types in order.
func (f *Store) EventTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.Events))
	for _, e := range f.Events {
		out = append(out, e.Type)
	}
	return out
}

// SetVerifiedPAN records a verified PAN for the creator.
func (f *Store) SetVerifiedPAN(userID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pan := "enc:pan"
	now := time.Now()
	f.Tax[userID] = &postgres.CreatorTaxProfile{UserID: userID, PANEncrypted: &pan, TaxResidency: "IN", VerifiedAt: &now}
}

func cloneTier(t *postgres.CreatorTier) *postgres.CreatorTier  { c := *t; return &c }
func cloneSub(s *postgres.Subscription) *postgres.Subscription { c := *s; return &c }
func clonePay(p *membership.Payment) *membership.Payment       { c := *p; return &c }

// --- tiers ----------------------------------------------------------------

func (f *Store) GetCreatorTiers(_ context.Context, creatorID uuid.UUID) ([]postgres.CreatorTier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.CreatorTier
	for _, t := range f.Tiers {
		if t.CreatorID == creatorID {
			out = append(out, *t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PricePaise < out[j].PricePaise })
	return out, nil
}

func (f *Store) GetCreatorTier(_ context.Context, tierID uuid.UUID) (*postgres.CreatorTier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.Tiers[tierID]
	if !ok {
		return nil, nil
	}
	return cloneTier(t), nil
}

func (f *Store) activeTiers(creatorID, exclude uuid.UUID) int {
	n := 0
	for _, t := range f.Tiers {
		if t.CreatorID == creatorID && t.IsActive && t.ID != exclude {
			n++
		}
	}
	return n
}

func (f *Store) CreateTierLimited(_ context.Context, t *postgres.CreatorTier, maxActive int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t.IsActive && f.activeTiers(t.CreatorID, uuid.Nil) >= maxActive {
		return postgres.ErrTierLimit
	}
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	f.Tiers[t.ID] = cloneTier(t)
	return nil
}

func (f *Store) UpdateTierPartial(_ context.Context, creatorID, tierID uuid.UUID, p postgres.TierPatch, maxActive int) (*postgres.CreatorTier, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := p
	f.LastTierPatch = &cp
	t, ok := f.Tiers[tierID]
	if !ok || t.CreatorID != creatorID {
		return nil, postgres.ErrTierNotFound
	}
	if p.IsActive != nil && *p.IsActive && !t.IsActive && f.activeTiers(creatorID, tierID) >= maxActive {
		return nil, postgres.ErrTierLimit
	}
	if p.Name != nil {
		t.Name = *p.Name
	}
	if p.PricePaise != nil {
		t.PricePaise = *p.PricePaise
	}
	if p.Currency != nil {
		t.Currency = *p.Currency
	}
	if p.Perks != nil {
		t.Perks = append(json.RawMessage(nil), p.Perks...)
	}
	if p.IsActive != nil {
		t.IsActive = *p.IsActive
	}
	return cloneTier(t), nil
}

func (f *Store) GetCreatorTaxProfile(_ context.Context, userID uuid.UUID) (*postgres.CreatorTaxProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.Tax[userID]
	if !ok {
		if f.AllPANVerified {
			pan, now := "enc:pan", time.Now()
			return &postgres.CreatorTaxProfile{UserID: userID, PANEncrypted: &pan, TaxResidency: "IN", VerifiedAt: &now}, nil
		}
		return nil, nil
	}
	c := *p
	return &c, nil
}

// --- subscriptions --------------------------------------------------------

func (f *Store) liveLocked(subscriberID, creatorID uuid.UUID) *postgres.Subscription {
	for _, s := range f.Subs {
		if s.SubscriberID == subscriberID && s.CreatorID == creatorID && postgres.IsLiveSubscriptionStatus(s.Status) {
			return s
		}
	}
	return nil
}

func (f *Store) otherLiveLocked(s *postgres.Subscription) bool {
	for _, o := range f.Subs {
		if o.ID != s.ID && o.SubscriberID == s.SubscriberID && o.CreatorID == s.CreatorID && postgres.IsLiveSubscriptionStatus(o.Status) {
			return true
		}
	}
	return false
}

func (f *Store) GetLiveSubscription(_ context.Context, subscriberID, creatorID uuid.UUID) (*postgres.Subscription, error) {
	f.mu.Lock()
	s := f.liveLocked(subscriberID, creatorID)
	var out *postgres.Subscription
	if s != nil {
		out = cloneSub(s)
	}
	f.mu.Unlock()
	if f.AfterGetLive != nil {
		f.AfterGetLive()
	}
	return out, nil
}

func (f *Store) GetSubscriptionByID(_ context.Context, subID uuid.UUID) (*postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.Subs[subID]
	if !ok {
		return nil, nil
	}
	return cloneSub(s), nil
}

func (f *Store) cancelNowLocked(s *postgres.Subscription) {
	s.Status = membership.StatusCancelled
	if s.Counted {
		s.Counted = false
		if t, ok := f.Tiers[s.TierID]; ok && t.SubscriberCount > 0 {
			t.SubscriberCount--
		}
	}
}

func (f *Store) Unsubscribe(_ context.Context, subscriberID, creatorID uuid.UUID) (*postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.liveLocked(subscriberID, creatorID)
	if s == nil {
		return nil, errors.New("SUBSCRIPTION_NOT_FOUND")
	}
	before := cloneSub(s)
	f.cancelNowLocked(s)
	return before, nil
}

func (f *Store) CancelSubscriptionNow(_ context.Context, subID uuid.UUID) (*postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.Subs[subID]
	if !ok {
		return nil, errors.New("SUBSCRIPTION_NOT_FOUND")
	}
	if s.Status == membership.StatusCancelled || s.Status == membership.StatusExpired {
		return nil, postgres.ErrSubscriptionStateChanged
	}
	before := cloneSub(s)
	f.cancelNowLocked(s)
	return before, nil
}

func (f *Store) PauseSubscription(_ context.Context, subID uuid.UUID, pauseUntil time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.Subs[subID]
	if !ok || s.Status != "active" {
		return postgres.ErrSubscriptionStateChanged
	}
	s.Status = "paused"
	u := pauseUntil
	s.PauseUntil = &u
	return nil
}

func (f *Store) ResumeSubscription(_ context.Context, subID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.Subs[subID]
	if !ok || s.Status != "paused" {
		return postgres.ErrSubscriptionStateChanged
	}
	s.Status = "active"
	s.PauseUntil = nil
	return nil
}

func (f *Store) CancelAtPeriodEnd(_ context.Context, subID uuid.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.Subs[subID]; ok {
		s.Status = "cancelled_at_period_end"
		s.PendingTierID, s.PendingPricePaise = nil, nil
	}
	return nil
}

func (f *Store) moveCountLocked(from, to uuid.UUID) {
	if t, ok := f.Tiers[from]; ok && t.SubscriberCount > 0 {
		t.SubscriberCount--
	}
	if t, ok := f.Tiers[to]; ok {
		t.SubscriberCount++
	}
}

func (f *Store) UpgradeSubscription(_ context.Context, in postgres.UpgradeInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.Subs[in.SubscriptionID]
	if !ok || s.TierID != in.ExpectedTierID || !s.CurrentPeriodEnd.Equal(in.ExpectedPeriodEnd) || s.Status != "active" {
		return postgres.ErrSubscriptionStateChanged
	}
	if s.Counted {
		f.moveCountLocked(s.TierID, in.NewTierID)
	}
	s.TierID, s.TierName, s.PricePaise = in.NewTierID, in.NewTierName, in.NewPricePaise
	s.PendingTierID, s.PendingPricePaise = nil, nil
	return nil
}

func (f *Store) SetPendingTier(_ context.Context, subID, expectedTierID uuid.UUID, pendingTierID *uuid.UUID, pendingPricePaise *int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.Subs[subID]
	if !ok || s.TierID != expectedTierID || s.Status != "active" {
		return postgres.ErrSubscriptionStateChanged
	}
	s.PendingTierID, s.PendingPricePaise = pendingTierID, pendingPricePaise
	return nil
}

func (f *Store) InsertSubscriptionEvent(_ context.Context, subID uuid.UUID, eventType, oldStatus, newStatus string, _ json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Events = append(f.Events, Event{SubscriptionID: subID, Type: eventType, OldStatus: oldStatus, NewStatus: newStatus})
	return nil
}

func (f *Store) GetSubscriptionEvents(_ context.Context, subID uuid.UUID, _, _ int) ([]postgres.SubscriptionEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.SubscriptionEvent
	for _, e := range f.Events {
		if e.SubscriptionID == subID {
			out = append(out, postgres.SubscriptionEvent{SubscriptionID: subID, EventType: e.Type})
		}
	}
	return out, nil
}

func (f *Store) GetPausedSubscriptionsToResume(_ context.Context, now time.Time) ([]postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.Subscription
	for _, s := range f.Subs {
		if s.Status == "paused" && s.PauseUntil != nil && !s.PauseUntil.After(now) {
			out = append(out, *s)
		}
	}
	return out, nil
}

// --- membership payments ----------------------------------------------------

func (f *Store) newPaymentLocked(p membership.Payment) *membership.Payment {
	p.ID = uuid.New()
	if p.Currency == "" {
		p.Currency = membership.Currency
	}
	p.FeePaise, p.CreatorSharePaise = membership.Split(p.AmountPaise)
	p.Status = membership.PayCreated
	np := p
	f.Payments[np.ID] = &np
	return clonePay(&np)
}

// holdsPeriod: the row occupies its (subscription, kind, period) slot (the
// partial unique index in migration 025).
func holdsPeriod(p *membership.Payment) bool {
	return p.Status != membership.PayRefunded && !p.LateCapture
}

func (f *Store) latestJoinLocked(subID uuid.UUID) *membership.Payment {
	var best *membership.Payment
	for _, p := range f.Payments {
		if p.SubscriptionID == subID && p.Kind == membership.KindJoin && (best == nil || p.CreatedAt.After(best.CreatedAt)) {
			best = p
		}
	}
	return best
}

func reopen(p *membership.Payment) {
	if p.Status == membership.PayFailed {
		p.Status = membership.PayCreated
	}
}

func (f *Store) CreateMembershipJoin(_ context.Context, in postgres.JoinInput) (*postgres.MembershipCheckout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.JoinCalls++
	now := membership.Truncate(in.Now)
	replay := func(s *postgres.Subscription) (*postgres.MembershipCheckout, error) {
		p := f.latestJoinLocked(s.ID)
		if p == nil {
			return nil, fmt.Errorf("no join payment for %s", s.ID)
		}
		if s.Status == membership.StatusPendingPayment {
			reopen(p)
		}
		return &postgres.MembershipCheckout{Sub: *cloneSub(s), Payment: *clonePay(p), Replayed: true}, nil
	}
	if in.ClientKey != "" {
		for _, s := range f.Subs {
			if s.SubscriberID == in.SubscriberID && s.IdempotencyKey == in.ClientKey {
				if s.CreatorID != in.CreatorID || s.TierID != in.Tier.ID {
					return nil, postgres.ErrIdempotencyKeyTaken
				}
				return replay(s)
			}
		}
	}
	if f.liveLocked(in.SubscriberID, in.CreatorID) != nil {
		return nil, postgres.ErrAlreadySubscribed
	}
	for _, s := range f.Subs {
		if s.SubscriberID != in.SubscriberID || s.CreatorID != in.CreatorID || s.Status != membership.StatusPendingPayment {
			continue
		}
		if s.TierID == in.Tier.ID && s.CreatedAt.After(now.Add(-membership.PendingTTL)) {
			return replay(s)
		}
		s.Status = membership.StatusExpired
		for _, p := range f.Payments {
			if p.SubscriptionID == s.ID && (p.Status == membership.PayCreated || p.Status == membership.PayConfirming) {
				p.Status = membership.PayFailed
			}
		}
	}
	end := membership.Truncate(membership.PeriodEnd(now))
	s := &postgres.Subscription{
		ID: uuid.New(), SubscriberID: in.SubscriberID, CreatorID: in.CreatorID, TierID: in.Tier.ID, TierName: in.Tier.Name,
		PricePaise: in.Tier.PricePaise, Currency: "INR", Status: membership.StatusPendingPayment,
		CurrentPeriodStart: now, CurrentPeriodEnd: end, CreatedAt: now, IdempotencyKey: in.ClientKey,
	}
	f.Subs[s.ID] = s
	p := f.newPaymentLocked(membership.Payment{
		SubscriptionID: s.ID, PayerID: in.SubscriberID, CreatorID: in.CreatorID, Kind: membership.KindJoin,
		TierID: in.Tier.ID, TierName: in.Tier.Name, TierPricePaise: in.Tier.PricePaise, AmountPaise: in.Tier.PricePaise,
		PeriodStart: now, PeriodEnd: end, CreatedAt: now,
	})
	return &postgres.MembershipCheckout{Sub: *cloneSub(s), Payment: *p}, nil
}

func (f *Store) CreateMembershipRenewal(_ context.Context, in postgres.RenewalInput) (*postgres.MembershipCheckout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := membership.Truncate(in.Now)
	s, ok := f.Subs[in.SubscriptionID]
	if !ok {
		return nil, postgres.ErrSubscriptionNotFound
	}
	if s.SubscriberID != in.SubscriberID {
		return nil, postgres.ErrSubscriptionNotOwned
	}
	if !membership.CanRenew(s.Status, s.ActivatedAt != nil, s.CurrentPeriodEnd, now) {
		return nil, postgres.ErrNotRenewable
	}
	if s.Status == membership.StatusExpired && f.otherLiveLocked(s) {
		return nil, postgres.ErrAlreadySubscribed
	}
	tierID, price := s.TierID, s.PricePaise
	if s.PendingTierID != nil && s.PendingPricePaise != nil {
		tierID, price = *s.PendingTierID, *s.PendingPricePaise
	}
	tier, ok := f.Tiers[tierID]
	if !ok || !tier.IsActive {
		return nil, postgres.ErrTierInactive
	}
	base := s.CurrentPeriodEnd
	for _, p := range f.Payments {
		if p.SubscriptionID == s.ID && p.Kind == membership.KindRenewal && p.PeriodStart.Equal(base) && holdsPeriod(p) {
			if p.Status == membership.PayPaid {
				return nil, postgres.ErrRenewalAlreadyPaid
			}
			reopen(p)
			return &postgres.MembershipCheckout{Sub: *cloneSub(s), Payment: *clonePay(p), Replayed: true}, nil
		}
	}
	p := f.newPaymentLocked(membership.Payment{
		SubscriptionID: s.ID, PayerID: s.SubscriberID, CreatorID: s.CreatorID, Kind: membership.KindRenewal,
		TierID: tierID, TierName: tier.Name, TierPricePaise: price, AmountPaise: price,
		PeriodStart: base, PeriodEnd: membership.Truncate(membership.PeriodEnd(membership.RenewalStart(base, now))), CreatedAt: now,
	})
	return &postgres.MembershipCheckout{Sub: *cloneSub(s), Payment: *p}, nil
}

func (f *Store) CreateMembershipUpgrade(_ context.Context, in postgres.UpgradeCheckoutInput) (*postgres.MembershipCheckout, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.ChargePaise <= 0 {
		return nil, errors.New("upgrade charge must be positive")
	}
	now := membership.Truncate(in.Now)
	s, ok := f.Subs[in.SubscriptionID]
	if !ok {
		return nil, postgres.ErrSubscriptionNotFound
	}
	if s.SubscriberID != in.SubscriberID {
		return nil, postgres.ErrSubscriptionNotOwned
	}
	if s.Status != membership.StatusActive || s.TierID != in.ExpectedTierID || !s.CurrentPeriodEnd.Equal(in.ExpectedPeriodEnd) {
		return nil, postgres.ErrSubscriptionStateChanged
	}
	for _, p := range f.Payments {
		open := p.SubscriptionID == s.ID && p.Kind == membership.KindUpgrade && !p.LateCapture &&
			(p.Status == membership.PayCreated || p.Status == membership.PayConfirming || p.Status == membership.PayFailed)
		if open && p.TierID == in.NewTier.ID && p.FromTierID != nil && *p.FromTierID == in.ExpectedTierID &&
			p.PeriodEnd.Equal(in.ExpectedPeriodEnd) && p.CreatedAt.After(now.Add(-membership.PendingTTL)) {
			reopen(p)
			return &postgres.MembershipCheckout{Sub: *cloneSub(s), Payment: *clonePay(p), Replayed: true}, nil
		}
	}
	for _, p := range f.Payments {
		if p.SubscriptionID == s.ID && p.Kind == membership.KindUpgrade && (p.Status == membership.PayCreated || p.Status == membership.PayConfirming) {
			p.Status = membership.PayFailed
		}
	}
	from := in.ExpectedTierID
	p := f.newPaymentLocked(membership.Payment{
		SubscriptionID: s.ID, PayerID: s.SubscriberID, CreatorID: s.CreatorID, Kind: membership.KindUpgrade,
		TierID: in.NewTier.ID, TierName: in.NewTier.Name, TierPricePaise: in.NewTier.PricePaise,
		FromTierID: &from, FromTierName: s.TierName, FromPricePaise: s.PricePaise,
		AmountPaise: in.ChargePaise, PeriodStart: now, PeriodEnd: s.CurrentPeriodEnd, CreatedAt: now,
	})
	return &postgres.MembershipCheckout{Sub: *cloneSub(s), Payment: *p}, nil
}

func (f *Store) SetMembershipPaymentIntent(_ context.Context, paymentID, intentID uuid.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.Payments[paymentID]
	if !ok {
		return postgres.ErrIntentMismatch
	}
	if p.IntentID != "" && p.IntentID != intentID.String() {
		return postgres.ErrIntentMismatch
	}
	p.IntentID = intentID.String()
	return nil
}

func (f *Store) MarkMembershipPaymentConfirming(_ context.Context, paymentID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.Payments[paymentID]; ok && p.Status == membership.PayCreated {
		p.Status = membership.PayConfirming
	}
	return nil
}

func (f *Store) GetMembershipPayment(_ context.Context, paymentID uuid.UUID) (*membership.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.Payments[paymentID]
	if !ok {
		return nil, nil
	}
	return clonePay(p), nil
}

func (f *Store) ListSubscriberMemberships(_ context.Context, subscriberID uuid.UUID, since time.Time) ([]postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.Subscription
	for _, s := range f.Subs {
		if s.SubscriberID != subscriberID {
			continue
		}
		if postgres.IsLiveSubscriptionStatus(s.Status) || s.Status == membership.StatusPendingPayment ||
			(s.Status == membership.StatusExpired && s.ActivatedAt != nil && s.CurrentPeriodEnd.After(since)) {
			out = append(out, *s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CurrentPeriodEnd.After(out[j].CurrentPeriodEnd) })
	return out, nil
}

// ListCreatorMembers mirrors the store: the creator's activated, live
// subscriptions, newest first by (activated_at, id), after the cursor.
func (f *Store) ListCreatorMembers(_ context.Context, creatorID uuid.UUID, after *postgres.MemberCursor, limit int) ([]postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	before := func(aAt time.Time, aID uuid.UUID, bAt time.Time, bID uuid.UUID) bool { // (a) < (b)
		if !aAt.Equal(bAt) {
			return aAt.Before(bAt)
		}
		return strings.Compare(aID.String(), bID.String()) < 0
	}
	var out []postgres.Subscription
	for _, s := range f.Subs {
		if s.CreatorID != creatorID || s.ActivatedAt == nil || !postgres.IsLiveSubscriptionStatus(s.Status) {
			continue
		}
		if after != nil && !before(*s.ActivatedAt, s.ID, after.ActivatedAt, after.ID) {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		return before(*out[j].ActivatedAt, out[j].ID, *out[i].ActivatedAt, out[i].ID)
	})
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func writeSub(dst *postgres.Subscription, s membership.Sub) {
	dst.Status = s.Status
	dst.CurrentPeriodStart, dst.CurrentPeriodEnd = s.CurrentPeriodStart, s.CurrentPeriodEnd
	dst.TierID, dst.TierName, dst.PricePaise = s.TierID, s.TierName, s.PricePaise
	dst.GracePeriodEnd = s.GracePeriodEnd
	dst.PendingTierID, dst.PendingPricePaise = s.PendingTierID, s.PendingPricePaise
	dst.Counted, dst.ActivatedAt = s.Counted, s.ActivatedAt
}

// ApplyMembershipPaymentEvent mirrors the Postgres store: the inbox claim,
// membership.Decide and every write it implies, under one lock.
func (f *Store) ApplyMembershipPaymentEvent(_ context.Context, ev membership.Event, now time.Time) (membership.Applied, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var applied membership.Applied
	if ev.EventID == "" {
		return applied, errors.New("payment event has no event_id")
	}
	if _, seen := f.Inbox[ev.EventID]; seen {
		applied.Decision = membership.Decision{Outcome: membership.OutcomeDuplicate}
		return applied, nil
	}
	p, ok := f.Payments[ev.PaymentID]
	if !ok {
		applied.Decision = membership.Decision{Outcome: membership.OutcomePaymentNotFound}
		f.Inbox[ev.EventID] = applied.Decision.Outcome
		return applied, nil
	}
	s := f.Subs[p.SubscriptionID]
	msub := postgres.ToMembershipSub(s)
	d := membership.Decide(*p, msub, f.otherLiveLocked(s), ev, now)
	applied.Decision, applied.Payment, applied.Sub = d, *p, msub
	if d.Payment != nil {
		*p = *d.Payment
		applied.Payment = *d.Payment
	}
	if d.Sub != nil {
		writeSub(s, *d.Sub)
		applied.Sub = *d.Sub
	}
	for _, td := range d.Tiers {
		if t, ok := f.Tiers[td.TierID]; ok {
			t.SubscriberCount += td.Delta
			if t.SubscriberCount < 0 {
				t.SubscriberCount = 0
			}
		}
	}
	if d.CreditCreatorPaise > 0 {
		f.Balances[p.CreatorID] += d.CreditCreatorPaise
		f.Earnings = append(f.Earnings, EarningRow{CreatorID: p.CreatorID, PaymentID: p.ID, Amount: d.CreditCreatorPaise, Kind: "earning"})
	}
	if d.ReverseCreatorPaise > 0 {
		f.Balances[p.CreatorID] -= d.ReverseCreatorPaise
		f.Earnings = append(f.Earnings, EarningRow{CreatorID: p.CreatorID, PaymentID: p.ID, Amount: d.ReverseCreatorPaise, Kind: "refund"})
	}
	if d.ReverseEarningRow {
		for i := range f.Earnings {
			if f.Earnings[i].PaymentID == p.ID && f.Earnings[i].Kind == "earning" {
				f.Earnings[i].Reversed = true
			}
		}
	}
	f.Events = append(f.Events, Event{SubscriptionID: s.ID, Type: "payment_" + string(d.Outcome), OldStatus: msub.Status, NewStatus: s.Status})
	f.Inbox[ev.EventID] = d.Outcome
	return applied, nil
}

func (f *Store) RequestMembershipSupportRefund(_ context.Context, in postgres.SupportRefundInput) (*membership.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.Payments[in.PaymentID]
	if !ok {
		return nil, postgres.ErrMembershipPaymentNotFound
	}
	if p.RefundCause == membership.CauseSupport && (p.RefundStatus == membership.RefundRequested || p.RefundStatus == membership.RefundPending) {
		return clonePay(p), nil
	}
	if p.Status != membership.PayPaid || p.LateCapture || p.IntentID == "" ||
		(p.RefundStatus != "" && p.RefundStatus != membership.RefundFailed) {
		return nil, postgres.ErrRefundNotAllowed
	}
	amount := p.AmountPaise - p.RefundedPaise
	if amount <= 0 {
		return nil, postgres.ErrRefundNotAllowed
	}
	p.RefundStatus, p.RefundCause, p.RefundKey, p.RefundPaise, p.RefundError =
		membership.RefundRequested, membership.CauseSupport, membership.SupportRefundKey(p.ID), amount, ""
	return clonePay(p), nil
}

func (f *Store) DueMembershipRefunds(_ context.Context, _ time.Time, limit int) ([]postgres.MembershipRefundDue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.MembershipRefundDue
	for _, p := range f.Payments {
		if p.RefundStatus != membership.RefundRequested || p.IntentID == "" {
			continue
		}
		id, _ := uuid.Parse(p.IntentID)
		out = append(out, postgres.MembershipRefundDue{PaymentID: p.ID, IntentID: id, Key: p.RefundKey,
			AmountPaise: p.RefundPaise, Cause: p.RefundCause, Attempts: f.RefundAttempts[p.ID]})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *Store) MarkMembershipRefundSubmitted(_ context.Context, paymentID, commandID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.Payments[paymentID]; ok && p.RefundStatus == membership.RefundRequested {
		p.RefundStatus = membership.RefundPending
		f.CommandIDs[paymentID] = commandID
		f.RefundAttempts[paymentID]++
	}
	return nil
}

func (f *Store) MarkMembershipRefundRetry(_ context.Context, paymentID uuid.UUID, _ time.Time, lastError string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.Payments[paymentID]; ok && p.RefundStatus == membership.RefundRequested {
		p.RefundError = lastError
		f.RefundAttempts[paymentID]++
	}
	return nil
}

func (f *Store) MarkMembershipRefundFailed(_ context.Context, paymentID uuid.UUID, lastError string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.Payments[paymentID]; ok && p.RefundStatus == membership.RefundRequested {
		p.RefundStatus = membership.RefundFailed
		p.RefundError = lastError
		f.RefundAttempts[paymentID]++
	}
	return nil
}

// --- workers --------------------------------------------------------------

func (f *Store) SweepStalePendingJoins(_ context.Context, cutoff time.Time) ([]postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.Subscription
	for _, s := range f.Subs {
		if s.Status == membership.StatusPendingPayment && !s.CreatedAt.After(cutoff) {
			s.Status = membership.StatusExpired
			for _, p := range f.Payments {
				if p.SubscriptionID == s.ID && (p.Status == membership.PayCreated || p.Status == membership.PayConfirming) {
					p.Status = membership.PayFailed
				}
			}
			out = append(out, *s)
		}
	}
	return out, nil
}

func (f *Store) StartGraceForEndedPeriods(_ context.Context, now time.Time) ([]postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.Subscription
	for _, s := range f.Subs {
		if (s.Status == membership.StatusActive || s.Status == membership.StatusPastDue) && !s.CurrentPeriodEnd.After(now) {
			s.Status = membership.StatusGrace
			g := s.CurrentPeriodEnd.Add(membership.GracePeriod)
			s.GracePeriodEnd = &g
			out = append(out, *s)
		}
	}
	return out, nil
}

func (f *Store) ExpireEndedMemberships(_ context.Context, now time.Time) ([]postgres.ExpiredMembership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.ExpiredMembership
	for _, s := range f.Subs {
		graceEnd := s.CurrentPeriodEnd
		if s.GracePeriodEnd != nil {
			graceEnd = *s.GracePeriodEnd
		}
		due := (s.Status == membership.StatusGrace && !graceEnd.After(now)) ||
			((s.Status == membership.StatusCancelledAtPeriodEnd || s.Status == membership.StatusPaused) && !s.CurrentPeriodEnd.After(now))
		if !due {
			continue
		}
		from, was := s.Status, s.Counted
		s.Status = membership.StatusExpired
		s.GracePeriodEnd = nil
		s.PendingTierID, s.PendingPricePaise = nil, nil
		s.Counted = false
		if was {
			if t, ok := f.Tiers[s.TierID]; ok && t.SubscriberCount > 0 {
				t.SubscriberCount--
			}
		}
		out = append(out, postgres.ExpiredMembership{Subscription: *s, From: from, WasCounted: was})
	}
	return out, nil
}

func (f *Store) DueRenewalReminders(_ context.Context, now time.Time) ([]postgres.ReminderDue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []postgres.ReminderDue
	for _, s := range f.Subs {
		if s.ActivatedAt == nil || (s.Status != membership.StatusActive && s.Status != membership.StatusPastDue && s.Status != membership.StatusGrace) {
			continue
		}
		if s.CurrentPeriodEnd.After(now.Add(membership.ReminderLead)) || !s.CurrentPeriodEnd.After(now.Add(-membership.ReminderLead)) {
			continue
		}
		days := 3
		if !s.CurrentPeriodEnd.After(now.Add(membership.ReminderDay)) {
			days = 0
		}
		if f.reminders[reminderKey{s.ID, s.CurrentPeriodEnd.UnixMicro(), days}] {
			continue
		}
		price := s.PricePaise
		if s.PendingPricePaise != nil {
			price = *s.PendingPricePaise
		}
		out = append(out, postgres.ReminderDue{Subscription: *s, DaysLeft: days, RenewalPricePaise: price})
	}
	return out, nil
}

func (f *Store) ClaimRenewalReminder(_ context.Context, subID uuid.UUID, periodEnd time.Time, daysLeft int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := reminderKey{subID, periodEnd.UnixMicro(), daysLeft}
	if f.reminders[k] {
		return false, nil
	}
	f.reminders[k] = true
	return true, nil
}

func (f *Store) ReleaseRenewalReminder(_ context.Context, subID uuid.UUID, periodEnd time.Time, daysLeft int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.reminders, reminderKey{subID, periodEnd.UnixMicro(), daysLeft})
	return nil
}

// --- payments-service -------------------------------------------------------

// Payments is a fake payments-service (payments.API). Intents are keyed by
// the deterministic idempotency key, exactly as payments-service does.
type Payments struct {
	mu       sync.Mutex
	byKey    map[string]*payments.Intent
	Creates  int
	Refunds  []RefundCall
	Verifies int
	// NoSession: intents come back without a checkout session (stub gateway).
	NoSession bool
	// CreateErr / RefundErr are returned by the next call when set.
	CreateErr error
	RefundErr error
	// Verdict is what VerifyCallback answers (nil = verified, echoing the intent).
	Verdict *payments.Verdict
	payers  map[uuid.UUID]uuid.UUID
}

// RefundCall is one Refund call.
type RefundCall struct {
	IntentID uuid.UUID
	Amount   int64
	Key      string
}

// NewPayments returns an empty fake payments-service.
func NewPayments() *Payments {
	return &Payments{byKey: map[string]*payments.Intent{}, payers: map[uuid.UUID]uuid.UUID{}}
}

var _ payments.API = (*Payments)(nil)

func (p *Payments) CreateIntent(_ context.Context, in payments.IntentInput) (*payments.Intent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.CreateErr; err != nil {
		p.CreateErr = nil
		return nil, err
	}
	key := membership.IntentKey(in.PaymentID)
	if i, ok := p.byKey[key]; ok {
		c := *i
		return &c, nil
	}
	p.Creates++
	i := &payments.Intent{ID: uuid.New(), Status: "created", AmountMinor: in.AmountMinor, Currency: "INR",
		ProviderRef: "order_" + in.PaymentID.String()[:8], ReferenceID: in.PaymentID}
	if !p.NoSession {
		i.ClientSession = &payments.ClientSession{Provider: "razorpay", OrderID: i.ProviderRef, KeyID: "rzp_test_key", MerchantDisplayName: "Momentum Merchant"}
	}
	p.byKey[key] = i
	p.payers[i.ID] = in.PayerID
	c := *i
	return &c, nil
}

// Intent returns the intent opened for a payment row, or nil.
func (p *Payments) Intent(paymentID uuid.UUID) *payments.Intent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.byKey[membership.IntentKey(paymentID)]
}

func (p *Payments) VerifyCallback(_ context.Context, intentID uuid.UUID, in payments.Callback) (*payments.Verdict, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Verifies++
	if p.Verdict != nil {
		v := *p.Verdict
		return &v, nil
	}
	for _, i := range p.byKey {
		if i.ID == intentID {
			return &payments.Verdict{Verified: true, AmountMinor: i.AmountMinor, PayerID: p.payers[i.ID],
				ReferenceType: membership.RefType, ReferenceID: i.ReferenceID, ApplicationID: membership.ApplicationID}, nil
		}
	}
	return &payments.Verdict{Verified: false}, nil
}

func (p *Payments) Refund(_ context.Context, intentID uuid.UUID, amountMinor int64, _ string, key string) (*payments.RefundAccepted, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.RefundErr; err != nil {
		p.RefundErr = nil
		return nil, err
	}
	p.Refunds = append(p.Refunds, RefundCall{IntentID: intentID, Amount: amountMinor, Key: key})
	return &payments.RefundAccepted{CommandID: uuid.New(), IntentID: intentID, AmountMinor: amountMinor, Status: "requested"}, nil
}

// RefundKeys returns the keys refunds were filed under, in order.
func (p *Payments) RefundKeys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.Refunds))
	for _, r := range p.Refunds {
		out = append(out, r.Key)
	}
	return out
}

// --- publishers -------------------------------------------------------------

// Published is one entitlement.changed the Publisher recorded.
type Published struct {
	SubscriptionID uuid.UUID
	SubscriberID   uuid.UUID
	CreatorID      uuid.UUID
	Action         string
}

// MembershipPublished is one membership.* event the Publisher recorded.
type MembershipPublished struct {
	Type    string
	Key     string
	Payload events.MembershipPayload
}

// Publisher records entitlement.changed and membership.* events
// (the entitlement and membership publishers, and the workers' publisher).
type Publisher struct {
	mu          sync.Mutex
	Events      []Published
	Memberships []MembershipPublished
	Resumed     int
	// FailMembership makes the next n membership publishes fail.
	FailMembership int
}

func (p *Publisher) PublishEntitlementChanged(_ context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, action string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Events = append(p.Events, Published{subscriptionID, subscriberID, creatorID, action})
	return nil
}

func (p *Publisher) PublishMembershipEvent(_ context.Context, eventType, key string, payload events.MembershipPayload) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.FailMembership > 0 {
		p.FailMembership--
		return errors.New("kafka down")
	}
	p.Memberships = append(p.Memberships, MembershipPublished{eventType, key, payload})
	return nil
}

func (p *Publisher) PublishSubscriptionResumed(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Resumed++
	return nil
}

// Actions returns the recorded entitlement actions in order.
func (p *Publisher) Actions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.Events))
	for _, e := range p.Events {
		out = append(out, e.Action)
	}
	return out
}

// MembershipTypes returns the recorded membership event types in order.
func (p *Publisher) MembershipTypes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.Memberships))
	for _, e := range p.Memberships {
		out = append(out, e.Type)
	}
	return out
}

// --- eligibility --------------------------------------------------------------

// Eligibility is a set of fake eligibility sources. Err* makes a source
// answer an error (unknown).
type Eligibility struct {
	DoB         *time.Time
	Found       bool
	Followers   int
	StandingIs  string
	Strikes     int
	ErrAge      error
	ErrFollower error
	ErrStanding error
}

// Eligible returns sources that pass: 30 years old, 1,000 followers, ok.
func Eligible() *Eligibility {
	dob := time.Now().AddDate(-30, 0, 0)
	return &Eligibility{DoB: &dob, Found: true, Followers: 1000, StandingIs: "ok"}
}

func (e *Eligibility) BirthDate(context.Context, uuid.UUID) (*time.Time, bool, error) {
	return e.DoB, e.Found, e.ErrAge
}

func (e *Eligibility) FollowerCount(context.Context, uuid.UUID) (int, error) {
	return e.Followers, e.ErrFollower
}

func (e *Eligibility) Standing(context.Context, uuid.UUID) (membership.StandingAnswer, error) {
	return membership.StandingAnswer{Standing: e.StandingIs, ActiveStrikes: e.Strikes}, e.ErrStanding
}

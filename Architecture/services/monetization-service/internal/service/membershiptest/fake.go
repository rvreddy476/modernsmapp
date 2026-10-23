// Package membershiptest is an in-memory service.MembershipStore for unit
// tests of the membership rules (tiers, subscriptions, entitlement), and a
// recording entitlement publisher. It mirrors the Postgres store's
// contract: partial tier updates, the active-tier limit, one live
// subscription per (subscriber, creator), idempotency keys scoped to the
// subscriber, and upgrade charge + tier change applied together or not at
// all. Not for production use.
package membershiptest

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

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

// Store is the fake. The zero value is not usable; call New.
type Store struct {
	mu       sync.Mutex
	Tiers    map[uuid.UUID]*postgres.CreatorTier
	Subs     map[uuid.UUID]*postgres.Subscription
	Balances map[uuid.UUID]int64
	Events   []Event
	// Charges records every amount moved by Subscribe or UpgradeSubscription.
	Charges []int64

	// BeforeSubscribeInsert, if set, runs (unlocked) at the start of
	// Subscribe — lets a test hold two concurrent subscribes at the line.
	BeforeSubscribeInsert func()
	// AfterGetLive, if set, runs (unlocked) at the end of GetLiveSubscription.
	AfterGetLive func()
	// LastTierPatch is the patch UpdateTierPartial was last given.
	LastTierPatch *postgres.TierPatch
	// SubscribeCalls counts calls to Subscribe (the insert + charge).
	SubscribeCalls int
}

// New returns an empty fake.
func New() *Store {
	return &Store{
		Tiers:    map[uuid.UUID]*postgres.CreatorTier{},
		Subs:     map[uuid.UUID]*postgres.Subscription{},
		Balances: map[uuid.UUID]int64{},
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

// AddSubscription stores a subscription as given (ID assigned if nil).
func (f *Store) AddSubscription(sub postgres.Subscription) *postgres.Subscription {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sub.ID == uuid.Nil {
		sub.ID = uuid.New()
	}
	if sub.Currency == "" {
		sub.Currency = "INR"
	}
	s := sub
	f.Subs[s.ID] = &s
	return &s
}

// Sub returns a copy of a stored subscription.
func (f *Store) Sub(id uuid.UUID) postgres.Subscription {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.Subs[id]
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

func cloneTier(t *postgres.CreatorTier) *postgres.CreatorTier  { c := *t; return &c }
func cloneSub(s *postgres.Subscription) *postgres.Subscription { c := *s; return &c }

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

// --- wallets --------------------------------------------------------------

func (f *Store) EnsureWallet(_ context.Context, userID uuid.UUID) (*postgres.Wallet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Balances[userID]; !ok {
		f.Balances[userID] = 0
	}
	return &postgres.Wallet{UserID: userID, BalancePaise: f.Balances[userID], Currency: "INR", HasActivity: true}, nil
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

func (f *Store) GetSubscriptionByIdempotencyKey(_ context.Context, subscriberID uuid.UUID, key string) (*postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.Subs {
		if s.SubscriberID == subscriberID && s.IdempotencyKey == key {
			return cloneSub(s), nil
		}
	}
	return nil, nil
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

// moveLocked is the wallet leg: all or nothing.
func (f *Store) moveLocked(from, to uuid.UUID, amount int64) error {
	if f.Balances[from] < amount {
		return postgres.ErrInsufficientFunds
	}
	f.Balances[from] -= amount
	f.Balances[to] += amount
	f.Charges = append(f.Charges, amount)
	return nil
}

func (f *Store) Subscribe(_ context.Context, subscriberID, creatorID, tierID uuid.UUID, tierName string, pricePaise int64, currency string, idempotencyKey string) (*postgres.Subscription, error) {
	if f.BeforeSubscribeInsert != nil {
		f.BeforeSubscribeInsert()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SubscribeCalls++
	// The two unique indexes.
	if f.liveLocked(subscriberID, creatorID) != nil {
		return nil, postgres.ErrAlreadySubscribed
	}
	if idempotencyKey != "" {
		for _, s := range f.Subs {
			if s.SubscriberID == subscriberID && s.IdempotencyKey == idempotencyKey {
				return nil, postgres.ErrIdempotencyKeyTaken
			}
		}
	}
	if err := f.moveLocked(subscriberID, creatorID, pricePaise); err != nil {
		return nil, errors.New("INSUFFICIENT_BALANCE_OR_FROZEN")
	}
	now := time.Now()
	s := &postgres.Subscription{
		ID: uuid.New(), SubscriberID: subscriberID, CreatorID: creatorID, TierID: tierID, TierName: tierName,
		PricePaise: pricePaise, Currency: currency, Status: "active",
		CurrentPeriodStart: now, CurrentPeriodEnd: now.AddDate(0, 1, 0), CreatedAt: now, IdempotencyKey: idempotencyKey,
	}
	f.Subs[s.ID] = s
	if t, ok := f.Tiers[tierID]; ok {
		t.SubscriberCount++
	}
	return cloneSub(s), nil
}

func (f *Store) Unsubscribe(_ context.Context, subscriberID, creatorID uuid.UUID) (*postgres.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.liveLocked(subscriberID, creatorID)
	if s == nil {
		return nil, errors.New("SUBSCRIPTION_NOT_FOUND")
	}
	before := cloneSub(s)
	s.Status = "cancelled"
	return before, nil
}

func (f *Store) SetSubscriptionStatus(_ context.Context, subID uuid.UUID, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.Subs[subID]; ok {
		s.Status = status
	}
	return nil
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

func (f *Store) UpgradeSubscription(_ context.Context, in postgres.UpgradeInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.Subs[in.SubscriptionID]
	if !ok || s.TierID != in.ExpectedTierID || !s.CurrentPeriodEnd.Equal(in.ExpectedPeriodEnd) || s.Status != "active" {
		return postgres.ErrSubscriptionStateChanged
	}
	if in.ChargePaise > 0 {
		if err := f.moveLocked(in.SubscriberID, in.CreatorID, in.ChargePaise); err != nil {
			return err
		}
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

// --- publisher ------------------------------------------------------------

// Published is one entitlement.changed the Publisher recorded.
type Published struct {
	SubscriptionID uuid.UUID
	SubscriberID   uuid.UUID
	CreatorID      uuid.UUID
	Action         string
}

// Publisher records entitlement.changed events (service.EntitlementPublisher).
type Publisher struct {
	mu     sync.Mutex
	Events []Published
}

func (p *Publisher) PublishEntitlementChanged(_ context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, action string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Events = append(p.Events, Published{subscriptionID, subscriberID, creatorID, action})
	return nil
}

// Actions returns the recorded actions in order.
func (p *Publisher) Actions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.Events))
	for _, e := range p.Events {
		out = append(out, e.Action)
	}
	return out
}

// Package premiumtest is an in-memory service.PremiumStore for unit tests of
// the Momentum Premium rules, plus a fake payments client, a recording role
// client and a recording publisher. It mirrors the Postgres store's contract:
// one open checkout per (buyer, plan) reused within the TTL at the same
// price, the intent bound once, and the signed payment event applied exactly
// once through the SAME decision table (premium.Decide) the Postgres store
// uses. Not for production use.
package premiumtest

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/atpost/monetization-service/internal/events"
	"github.com/atpost/monetization-service/internal/payments"
	"github.com/atpost/monetization-service/internal/premium"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

type reminderKey struct {
	user     uuid.UUID
	ends     int64
	daysLeft int
}

// Store is the fake. Use New.
type Store struct {
	mu        sync.Mutex
	Plans     []premium.PlanRow
	Periods   map[uuid.UUID]*premium.Period
	Ents      map[uuid.UUID]*Ent
	Inbox     map[string]premium.Outcome
	Reminders map[reminderKey]bool
	// FailApply makes ApplyPremiumPaymentEvent fail (a database outage).
	FailApply error
}

// Ent is an entitlement with the role-sync bookkeeping the fake keeps.
type Ent struct {
	premium.Entitlement
	NextAttempt time.Time
	LastError   string
}

// New seeds the two plans migration 027 seeds.
func New() *Store {
	return &Store{
		Plans: []premium.PlanRow{
			{Code: premium.Plan30d, Name: "Momentum Premium, 30 days", DurationDays: 30, Active: true, SortOrder: 1},
			{Code: premium.Plan365d, Name: "Momentum Premium, 365 days", DurationDays: 365, Active: true, SortOrder: 2},
		},
		Periods:   map[uuid.UUID]*premium.Period{},
		Ents:      map[uuid.UUID]*Ent{},
		Inbox:     map[string]premium.Outcome{},
		Reminders: map[reminderKey]bool{},
	}
}

func (s *Store) ListPremiumPlans(context.Context) ([]premium.PlanRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]premium.PlanRow(nil), s.Plans...), nil
}

func (s *Store) OpenPremiumCheckout(_ context.Context, userID uuid.UUID, plan premium.PlanRow, amount int64, now time.Time) (*premium.Period, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now = premium.Truncate(now)
	for _, p := range s.Periods {
		if p.UserID == userID && p.PlanCode == plan.Code && p.Status == premium.StatusCreated {
			if p.AmountPaise == amount && p.CreatedAt.After(now.Add(-premium.CheckoutTTL)) {
				c := *p
				return &c, nil
			}
			p.Status = premium.StatusAbandoned
		}
	}
	p := &premium.Period{ID: uuid.New(), UserID: userID, PlanCode: plan.Code, DurationDays: plan.DurationDays,
		AmountPaise: amount, Currency: premium.Currency, Status: premium.StatusCreated, CreatedAt: now}
	s.Periods[p.ID] = p
	c := *p
	return &c, nil
}

func (s *Store) SetPremiumPeriodIntent(_ context.Context, periodID, intentID uuid.UUID, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.Periods[periodID]
	if !ok {
		return errors.New("no such pass")
	}
	if p.IntentID != "" && p.IntentID != intentID.String() {
		return postgres.ErrPremiumIntentMismatch
	}
	p.IntentID = intentID.String()
	return nil
}

func (s *Store) GetPremiumPeriod(_ context.Context, id uuid.UUID) (*premium.Period, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.Periods[id]; ok {
		c := *p
		return &c, nil
	}
	return nil, nil
}

func (s *Store) GetPremiumEntitlement(_ context.Context, userID uuid.UUID) (*premium.Entitlement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.Ents[userID]; ok {
		c := e.Entitlement
		return &c, nil
	}
	return nil, nil
}

func (s *Store) ApplyPremiumPaymentEvent(_ context.Context, ev premium.Event, now time.Time) (premium.Applied, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var applied premium.Applied
	if s.FailApply != nil {
		return applied, s.FailApply
	}
	if ev.EventID == "" {
		return applied, errors.New("payment event has no event_id")
	}
	if _, seen := s.Inbox[ev.EventID]; seen {
		applied.Decision = premium.Decision{Outcome: premium.OutcomeDuplicate}
		return applied, nil
	}
	p, ok := s.Periods[ev.PeriodID]
	if !ok {
		applied.Decision = premium.Decision{Outcome: premium.OutcomePeriodNotFound}
		s.Inbox[ev.EventID] = premium.OutcomePeriodNotFound
		return applied, nil
	}
	var ent *premium.Entitlement
	if e, ok := s.Ents[p.UserID]; ok {
		c := e.Entitlement
		ent = &c
	}
	var chain []premium.Period
	for _, c := range s.Periods {
		if c.UserID == p.UserID && c.Status == premium.StatusPaid && c.ID != p.ID {
			chain = append(chain, *c)
		}
	}
	sort.Slice(chain, func(i, j int) bool { return chain[i].PeriodStart.Before(*chain[j].PeriodStart) })

	d := premium.Decide(premium.Input{Period: *p, Entitlement: ent, Chain: chain, Event: ev, Now: now})
	applied.Decision, applied.Period, applied.Entitlement = d, *p, ent
	if d.Period != nil {
		c := *d.Period
		s.Periods[p.ID] = &c
		applied.Period = c
	}
	for _, sh := range d.Shifted {
		c := sh
		s.Periods[sh.ID] = &c
	}
	if d.Entitlement != nil {
		e, ok := s.Ents[p.UserID]
		if !ok {
			e = &Ent{Entitlement: premium.Entitlement{UserID: p.UserID}}
			s.Ents[p.UserID] = e
		}
		e.EndsAt, e.AccessUntil = d.Entitlement.EndsAt, d.Entitlement.AccessUntil
		e.RoleAttempts, e.NextAttempt, e.LastError = 0, premium.Truncate(now), ""
		applied.Entitlement = d.Entitlement
	}
	s.Inbox[ev.EventID] = d.Outcome
	return applied, nil
}

func roleDue(e premium.Entitlement, now time.Time) bool {
	a, _ := premium.RoleSync(e, now)
	return a != premium.RoleNone
}

func (s *Store) DuePremiumRoleSyncs(_ context.Context, now time.Time, limit int) ([]premium.Entitlement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []premium.Entitlement
	for _, e := range s.Ents {
		if !e.NextAttempt.After(now) && roleDue(e.Entitlement, now) {
			out = append(out, e.Entitlement)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) MarkPremiumRoleSynced(_ context.Context, userID uuid.UUID, accessUntil time.Time, synced *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.Ents[userID]; ok && e.AccessUntil.Equal(accessUntil) {
		e.RoleSyncedUntil, e.RoleAttempts, e.LastError = synced, 0, ""
	}
	return nil
}

func (s *Store) MarkPremiumRoleSyncRetry(_ context.Context, userID uuid.UUID, next time.Time, cause string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.Ents[userID]; ok {
		e.RoleAttempts++
		e.NextAttempt, e.LastError = next, cause
	}
	return nil
}

func (s *Store) DuePremiumReminders(_ context.Context, now time.Time) ([]premium.ReminderDue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []premium.ReminderDue
	for _, e := range s.Ents {
		d := premium.ReminderDaysLeft(e.EndsAt, now)
		if d < 0 || !e.AccessUntil.After(e.EndsAt) || !e.AccessUntil.After(now) {
			continue
		}
		if s.Reminders[reminderKey{e.UserID, e.EndsAt.UnixNano(), d}] {
			continue
		}
		out = append(out, premium.ReminderDue{Entitlement: e.Entitlement, DaysLeft: d})
	}
	return out, nil
}

func (s *Store) ClaimPremiumReminder(_ context.Context, userID uuid.UUID, endsAt time.Time, daysLeft int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := reminderKey{userID, endsAt.UnixNano(), daysLeft}
	if s.Reminders[k] {
		return false, nil
	}
	s.Reminders[k] = true
	return true, nil
}

func (s *Store) ReleasePremiumReminder(_ context.Context, userID uuid.UUID, endsAt time.Time, daysLeft int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Reminders, reminderKey{userID, endsAt.UnixNano(), daysLeft})
	return nil
}

func (s *Store) ClaimPremiumEndedNotices(_ context.Context, now time.Time) ([]premium.Entitlement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []premium.Entitlement
	for _, e := range s.Ents {
		if e.AccessUntil.After(now) || !e.AccessUntil.After(now.Add(-7*24*time.Hour)) {
			continue
		}
		if e.EndedNotifiedUntil != nil && e.EndedNotifiedUntil.Equal(e.AccessUntil) {
			continue
		}
		u := e.AccessUntil
		e.EndedNotifiedUntil = &u
		out = append(out, e.Entitlement)
	}
	return out, nil
}

func (s *Store) ReleasePremiumEndedNotice(_ context.Context, userID uuid.UUID, accessUntil time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.Ents[userID]; ok && e.EndedNotifiedUntil != nil && e.EndedNotifiedUntil.Equal(accessUntil) {
		e.EndedNotifiedUntil = nil
	}
	return nil
}

func (s *Store) AbandonStalePremiumCheckouts(_ context.Context, cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, p := range s.Periods {
		if p.Status == premium.StatusCreated && p.CreatedAt.Before(cutoff) {
			p.Status = premium.StatusAbandoned
			n++
		}
	}
	return n, nil
}

// Payments is a fake payments-service for application premium.
type Payments struct {
	mu      sync.Mutex
	Intents map[uuid.UUID]*payments.Intent // by period id
	Calls   []payments.PremiumIntentInput
	Fail    error
	// NoSession answers intents without a checkout session (the stub gateway).
	NoSession bool
}

// NewPayments builds the fake.
func NewPayments() *Payments { return &Payments{Intents: map[uuid.UUID]*payments.Intent{}} }

func (f *Payments) CreatePremiumIntent(_ context.Context, in payments.PremiumIntentInput) (*payments.Intent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, in)
	if f.Fail != nil {
		return nil, f.Fail
	}
	if i, ok := f.Intents[in.PeriodID]; ok {
		return i, nil
	}
	i := &payments.Intent{ID: uuid.New(), Status: "pending", AmountMinor: in.AmountMinor, Currency: in.Currency,
		ProviderRef: "order_" + in.PeriodID.String()[:8], ReferenceID: in.PeriodID}
	if !f.NoSession {
		i.ClientSession = &payments.ClientSession{Provider: "razorpay", OrderID: i.ProviderRef, KeyID: "rzp_test_key"}
	}
	f.Intents[in.PeriodID] = i
	return i, nil
}

// IntentFor answers the intent opened for a pass.
func (f *Payments) IntentFor(periodID uuid.UUID) *payments.Intent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Intents[periodID]
}

// RoleCall is one call to identity.
type RoleCall struct {
	Op        string // grant | revoke
	UserID    string
	Role      string
	ExpiresAt time.Time
}

// Roles records identity calls.
type Roles struct {
	mu    sync.Mutex
	Calls []RoleCall
	Fail  error
}

func (r *Roles) GrantUntil(_ context.Context, userID, role, _ string, expiresAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Calls = append(r.Calls, RoleCall{Op: "grant", UserID: userID, Role: role, ExpiresAt: expiresAt})
	return r.Fail
}

func (r *Roles) Revoke(_ context.Context, userID, role, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Calls = append(r.Calls, RoleCall{Op: "revoke", UserID: userID, Role: role})
	return r.Fail
}

// Snapshot answers the calls so far.
func (r *Roles) Snapshot() []RoleCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RoleCall(nil), r.Calls...)
}

// Published is one premium.* event.
type Published struct {
	Type    string
	Key     string
	Payload events.PremiumPayload
}

// Publisher records premium.* events.
type Publisher struct {
	mu   sync.Mutex
	Sent []Published
	Fail error
}

func (p *Publisher) PublishPremiumEvent(_ context.Context, eventType, key string, payload events.PremiumPayload) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Fail != nil {
		return p.Fail
	}
	p.Sent = append(p.Sent, Published{Type: eventType, Key: key, Payload: payload})
	return nil
}

// OfType answers the events of one type.
func (p *Publisher) OfType(t string) []Published {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Published
	for _, e := range p.Sent {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// Package premium is the pure core of Momentum Premium (short-posts design
// 3.1-3.3; founder took every default on 11 Nov 2026): the catalogue, the
// price configuration, the deterministic keys, and the decision table that
// turns one signed payment event into the effect the store must write.
//
// The rules, in one place:
//
//   - One platform plan sold as one-off passes of 30 and 365 days. No
//     auto-renew: renewal is a reminder with a pay button.
//   - Prices are configuration (PREMIUM_PRICE_30D_PAISE,
//     PREMIUM_PRICE_365D_PAISE). A plan with no price cannot be bought:
//     checkout fails closed with PREMIUM_PRICE_NOT_SET. Nothing is hard-coded.
//   - Prices EXCLUDE GST (founder, 13 Nov 2026): the configured price is the
//     taxable value and GST is added on top (tax.go). The pass stores the
//     split, and its amount is the total: the intent amount and the amount
//     the signed event must carry.
//   - Money moves only through payments-service (application `premium`,
//     reference type platform_premium). A pass is granted ONLY from the
//     signed payment.succeeded event, applied once (paymentevents.ApplyOnce),
//     after CheckCapture. There is no client-confirm path.
//   - Stacking: a pass starts at the buyer's current paid end when that is
//     still ahead, otherwise now (a lapsed buyer does not pay for the gap).
//   - Grace: the identity role `premium` is held until the paid end plus
//     GracePeriod (3 days, as memberships).
//   - Refund: a FULL refund removes the pass. Passes stacked after it move
//     earlier by the time it still had to run; if no paid coverage is left,
//     access ends at once (no grace after a refund). A partial refund is
//     recorded and leaves the pass in place.
//
// Nothing here does I/O: the Postgres store and the test store both lock the
// rows, call Decide, and persist what it returns.
package premium

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/shared/paymentevents"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// Payments coordinates.
const (
	// ApplicationID is the payments application every premium intent belongs
	// to (payments migration 019).
	ApplicationID = "premium"
	// RefType is the payments reference type: one Premium pass.
	RefType = servicetoken.RefPlatformPremium
	// Currency is the only currency Premium is priced in.
	Currency = "INR"
	// Role is the identity role a pass grants (shared/identityroles).
	Role = "premium"
)

// Plan codes. The catalogue rows are seeded by migration 027.
const (
	Plan30d  = "premium_30d"
	Plan365d = "premium_365d"
)

// Price configuration.
const (
	EnvPrice30d  = "PREMIUM_PRICE_30D_PAISE"
	EnvPrice365d = "PREMIUM_PRICE_365D_PAISE"
	// MinPricePaise is the smallest price accepted (₹1, the provider minimum).
	MinPricePaise int64 = 100
	// MaxPricePaise refuses a price that is almost certainly a unit mistake.
	MaxPricePaise int64 = 10_000_000 // ₹1,00,000
)

// Timings.
const (
	// GracePeriod: the role outlives the paid end by this (founder default 9).
	GracePeriod = 72 * time.Hour
	// CheckoutTTL: an unpaid checkout older than this is abandoned and the
	// next checkout for the plan opens a new one.
	CheckoutTTL = 30 * time.Minute
	// ReminderLead and ReminderDay: premium.renewal_due 3 days before the
	// paid end (days_left 3) and on the last day (days_left 0).
	ReminderLead = 72 * time.Hour
	ReminderDay  = 24 * time.Hour
)

// premium_periods.status.
const (
	StatusCreated   = "created"
	StatusAbandoned = "abandoned"
	StatusPaid      = "paid"
	StatusFailed    = "failed"
	StatusRefunded  = "refunded"
)

// Notification events (monetization.events, consumed by notification-service).
const (
	EventActivated  = "premium.activated"
	EventRenewalDue = "premium.renewal_due"
	EventEnded      = "premium.ended"
)

// Errors.
var (
	// ErrPriceNotSet: the plan has no configured price — checkout fails closed.
	ErrPriceNotSet = errors.New("PREMIUM_PRICE_NOT_SET")
	// ErrUnknownPlan: no such active plan.
	ErrUnknownPlan = errors.New("PREMIUM_UNKNOWN_PLAN")
)

// Prices is plan code → price in paise. A missing or zero entry is unset.
type Prices map[string]int64

// PriceEnv maps each plan code to its price variable.
var PriceEnv = map[string]string{Plan30d: EnvPrice30d, Plan365d: EnvPrice365d}

// PricesFromEnv reads the price of every plan. Unset (blank) is allowed and
// means "cannot be bought". A value that is not a whole number of paise
// within [MinPricePaise, MaxPricePaise] is a configuration error: the process
// refuses to start rather than sell at a price nobody meant.
func PricesFromEnv(getenv func(string) string) (Prices, error) {
	out := Prices{}
	for code, key := range PriceEnv {
		raw := strings.TrimSpace(getenv(key))
		if raw == "" {
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s must be a whole number of paise: %q", key, raw)
		}
		if n < MinPricePaise || n > MaxPricePaise {
			return nil, fmt.Errorf("%s = %d paise is outside [%d, %d]", key, n, MinPricePaise, MaxPricePaise)
		}
		out[code] = n
	}
	return out, nil
}

// Price answers the configured price of a plan, or ErrPriceNotSet.
func (p Prices) Price(code string) (int64, error) {
	if n, ok := p[code]; ok && n >= MinPricePaise {
		return n, nil
	}
	return 0, ErrPriceNotSet
}

// PlanRow is a premium_plans row.
type PlanRow struct {
	Code         string
	Name         string
	DurationDays int
	Active       bool
	SortOrder    int
}

// Plan is a plan as clients see it. Prices EXCLUDE GST (founder, 13 Nov
// 2026): PricePaise is the configured price, which is the taxable value
// (equal to TaxablePaise); GSTPaise is added on top and TotalPaise is what
// the buyer pays. The money fields are nil while the founder has not set
// the price (or its tax cannot be computed), and then Available is false.
type Plan struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	DurationDays     int    `json:"duration_days"`
	PricePaise       *int64 `json:"price_paise"`
	TaxablePaise     *int64 `json:"taxable_paise"`
	GSTPaise         *int64 `json:"gst_paise"`
	TotalPaise       *int64 `json:"total_paise"`
	GSTRateBP        *int   `json:"gst_rate_bp"`
	PricesExcludeGST bool   `json:"prices_exclude_gst"`
	TaxProvisional   bool   `json:"tax_provisional"`
	Currency         string `json:"currency"`
	Available        bool   `json:"available"`
}

// Catalogue joins the active plan rows with the prices and their GST at
// `at`, in display order. A plan whose tax cannot be computed is not
// available: it is never shown at the pre-tax price.
func Catalogue(rows []PlanRow, prices Prices, tax *Tax, at time.Time) []Plan {
	sorted := append([]PlanRow(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].SortOrder < sorted[j].SortOrder })
	out := make([]Plan, 0, len(sorted))
	for _, r := range sorted {
		if !r.Active {
			continue
		}
		p := Plan{Code: r.Code, Name: r.Name, DurationDays: r.DurationDays, Currency: Currency, PricesExcludeGST: true}
		if n, err := prices.Price(r.Code); err == nil {
			if b, err := tax.Compute(n, "", at); err == nil {
				price, taxable, gstPaise, total, rate := n, b.TaxablePaise, b.TaxPaise, b.TotalPaise, b.RateBP
				p.PricePaise, p.TaxablePaise, p.GSTPaise, p.TotalPaise, p.GSTRateBP = &price, &taxable, &gstPaise, &total, &rate
				p.TaxProvisional, p.Available = b.Provisional, true
			}
		}
		out = append(out, p)
	}
	return out
}

// Period is a premium_periods row: one pass.
type Period struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	PlanCode     string
	DurationDays int
	// AmountPaise is what the buyer pays: the GST-inclusive total. It is the
	// payments intent amount and what the signed event must carry.
	AmountPaise int64
	// Tax is the GST split stored with the pass (migration 028); nil on a
	// pass opened before prices excluded GST.
	Tax           *Breakdown
	Currency      string
	Status        string
	IntentID      string
	PeriodStart   *time.Time
	PeriodEnd     *time.Time
	RefundedPaise int64
	CreatedAt     time.Time
}

// Entitlement is a premium_entitlements row: what one buyer holds.
type Entitlement struct {
	UserID      uuid.UUID
	EndsAt      time.Time
	AccessUntil time.Time
	// RoleSyncedUntil is the expiry identity last accepted; nil = no role.
	RoleSyncedUntil *time.Time
	RoleAttempts    int
	// EndedNotifiedUntil is the access_until premium.ended was sent for.
	EndedNotifiedUntil *time.Time
}

// Event is one signed payments event for a premium pass.
type Event struct {
	EventID     string
	EventType   string
	IntentID    string
	PeriodID    uuid.UUID
	PayerID     uuid.UUID
	AmountMinor int64
	Currency    string
}

// Outcome is what one event did.
type Outcome string

const (
	OutcomeActivated      Outcome = "activated"
	OutcomeFailed         Outcome = "failed"
	OutcomeRefunded       Outcome = "refunded"
	OutcomePartialRefund  Outcome = "partial_refund"
	OutcomeIgnored        Outcome = "ignored"
	OutcomeMismatch       Outcome = "mismatch"
	OutcomeDuplicate      Outcome = "duplicate"
	OutcomePeriodNotFound Outcome = "period_not_found"
)

// Input is everything Decide reads, loaded and locked by the store.
type Input struct {
	Period Period
	// Entitlement is the buyer's row; nil when they never held Premium.
	Entitlement *Entitlement
	// Chain is the buyer's OTHER paid periods (status paid, id <> Period.ID).
	Chain []Period
	Event Event
	Now   time.Time
}

// Decision is what the store must write. A nil pointer is "unchanged".
type Decision struct {
	Outcome Outcome
	Detail  string
	// Period is the event's period after the event.
	Period *Period
	// Shifted are chain periods whose start and end moved (a refund).
	Shifted []Period
	// Entitlement is the buyer's row after the event (upserted); writing it
	// makes the role sync due now.
	Entitlement *Entitlement
	// Notify is the event to publish after commit, if any.
	Notify string
}

// Truncate is the precision every stored time has (PostgreSQL microseconds).
func Truncate(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// IntentKey is the payments idempotency key of a pass, and its row's
// idempotency_key.
func IntentKey(periodID uuid.UUID) string { return RefType + ":" + periodID.String() }

// PassEnd is durationDays after start.
func PassEnd(start time.Time, durationDays int) time.Time { return start.AddDate(0, 0, durationDays) }

// StackStart is where a newly paid pass starts: the current paid end when it
// is still ahead, otherwise now.
func StackStart(ent *Entitlement, now time.Time) time.Time {
	if ent != nil && ent.EndsAt.After(now) {
		return ent.EndsAt
	}
	return now
}

// Decide applies one event to one period. It never errors: a refusal is an
// outcome, recorded with no effect.
func Decide(in Input) Decision {
	now := Truncate(in.Now)
	p := in.Period
	switch in.Event.EventType {
	case "payment.succeeded":
		return decideCapture(in, p, now)
	case "payment.failed":
		if p.Status == StatusCreated || p.Status == StatusAbandoned {
			p.Status = StatusFailed
			return Decision{Outcome: OutcomeFailed, Period: &p}
		}
		return Decision{Outcome: OutcomeIgnored, Detail: "payment.failed for a " + p.Status + " pass"}
	case "payment.refunded":
		return decideRefund(in, p, now)
	}
	return Decision{Outcome: OutcomeIgnored, Detail: "event type " + in.Event.EventType}
}

func decideCapture(in Input, p Period, now time.Time) Decision {
	switch p.Status {
	case StatusPaid, StatusRefunded:
		return Decision{Outcome: OutcomeIgnored, Detail: "pass already " + p.Status}
	}
	if err := paymentevents.CheckCapture(
		paymentevents.Expected{AmountMinor: p.AmountPaise, Currency: p.Currency, PayerID: p.UserID, IntentID: p.IntentID},
		paymentevents.Observed{AmountMinor: in.Event.AmountMinor, Currency: in.Event.Currency, PayerID: in.Event.PayerID, IntentID: in.Event.IntentID},
		paymentevents.RequireStated,
	); err != nil {
		return Decision{Outcome: OutcomeMismatch, Detail: err.Error()}
	}
	start := Truncate(StackStart(in.Entitlement, now))
	end := Truncate(PassEnd(start, p.DurationDays))
	p.Status, p.PeriodStart, p.PeriodEnd = StatusPaid, &start, &end
	ent := Entitlement{UserID: p.UserID}
	if in.Entitlement != nil {
		ent = *in.Entitlement
	}
	ent.EndsAt, ent.AccessUntil = end, Truncate(end.Add(GracePeriod))
	return Decision{Outcome: OutcomeActivated, Period: &p, Entitlement: &ent, Notify: EventActivated}
}

func decideRefund(in Input, p Period, now time.Time) Decision {
	remaining := p.AmountPaise - p.RefundedPaise
	if p.Status == StatusRefunded || remaining <= 0 {
		return Decision{Outcome: OutcomeIgnored, Detail: "pass already fully refunded"}
	}
	if err := paymentevents.CheckRefund(
		paymentevents.Expected{AmountMinor: remaining, IntentID: p.IntentID},
		paymentevents.Observed{AmountMinor: in.Event.AmountMinor, IntentID: in.Event.IntentID},
	); err != nil {
		return Decision{Outcome: OutcomeMismatch, Detail: err.Error()}
	}
	p.RefundedPaise += in.Event.AmountMinor
	if p.RefundedPaise < p.AmountPaise {
		return Decision{Outcome: OutcomePartialRefund, Period: &p,
			Detail: fmt.Sprintf("refunded %d of %d paise; the pass stays", p.RefundedPaise, p.AmountPaise)}
	}
	if p.Status != StatusPaid || p.PeriodStart == nil || p.PeriodEnd == nil {
		// Money back for a pass that never activated: nothing to take away.
		p.Status = StatusRefunded
		return Decision{Outcome: OutcomeRefunded, Period: &p, Detail: "refund of a pass that was never active"}
	}

	// What the refunded pass still had to run moves every later pass earlier.
	from := *p.PeriodStart
	if now.After(from) {
		from = now
	}
	shift := p.PeriodEnd.Sub(from)
	if shift < 0 {
		shift = 0
	}
	var shifted []Period
	var coverage time.Time
	for _, c := range in.Chain {
		if c.PeriodStart == nil || c.PeriodEnd == nil {
			continue
		}
		if shift > 0 && !c.PeriodStart.Before(*p.PeriodEnd) {
			s, e := Truncate(c.PeriodStart.Add(-shift)), Truncate(c.PeriodEnd.Add(-shift))
			c.PeriodStart, c.PeriodEnd = &s, &e
			shifted = append(shifted, c)
		}
		if c.PeriodEnd.After(coverage) {
			coverage = *c.PeriodEnd
		}
	}
	p.Status = StatusRefunded

	ent := Entitlement{UserID: p.UserID}
	if in.Entitlement != nil {
		ent = *in.Entitlement
	}
	if coverage.After(now) {
		ent.EndsAt, ent.AccessUntil = coverage, Truncate(coverage.Add(GracePeriod))
	} else {
		// No paid coverage left: access ends now, without grace.
		ent.EndsAt, ent.AccessUntil = now, now
	}
	return Decision{Outcome: OutcomeRefunded, Period: &p, Shifted: shifted, Entitlement: &ent}
}

// Status is a buyer's Premium as they see it.
type Status struct {
	// Active: the role should be held now (paid, or in grace).
	Active bool `json:"active"`
	// InGrace: the paid period has ended; the grace runs to GraceEndsAt.
	InGrace bool `json:"in_grace"`
	// EndsAt: end of the paid coverage (null if never bought).
	EndsAt *time.Time `json:"ends_at"`
	// GraceEndsAt: when access stops if nothing more is bought.
	GraceEndsAt *time.Time `json:"grace_ends_at"`
}

// StatusOf answers the buyer's status at now.
func StatusOf(ent *Entitlement, now time.Time) Status {
	if ent == nil {
		return Status{}
	}
	ends, until := ent.EndsAt.UTC(), ent.AccessUntil.UTC()
	s := Status{EndsAt: &ends, GraceEndsAt: &until}
	s.Active = now.Before(until)
	s.InGrace = s.Active && !now.Before(ends)
	return s
}

// RoleAction is what the role sync must tell identity.
type RoleAction int

const (
	RoleNone RoleAction = iota
	// RoleGrant: GrantUntil(access_until).
	RoleGrant
	// RoleRevoke: Revoke.
	RoleRevoke
)

// RoleSync decides what identity must be told for one entitlement at now.
func RoleSync(ent Entitlement, now time.Time) (RoleAction, time.Time) {
	if ent.AccessUntil.After(now) {
		if ent.RoleSyncedUntil == nil || !ent.RoleSyncedUntil.Equal(ent.AccessUntil) {
			return RoleGrant, ent.AccessUntil
		}
		return RoleNone, time.Time{}
	}
	if ent.RoleSyncedUntil != nil {
		return RoleRevoke, time.Time{}
	}
	return RoleNone, time.Time{}
}

// Backoff is the role-sync retry delay after attempts failures: 1 minute
// doubling to 1 hour.
func Backoff(attempts int) time.Duration {
	d := time.Minute
	for i := 0; i < attempts && d < time.Hour; i++ {
		d *= 2
	}
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

// ReminderDaysLeft is the reminder due for a paid end at now: 3 inside the
// last three days, 0 on the last day, -1 for none.
func ReminderDaysLeft(endsAt, now time.Time) int {
	switch {
	case !endsAt.After(now.Add(-ReminderLead)) || endsAt.After(now.Add(ReminderLead)):
		return -1
	case !endsAt.After(now.Add(ReminderDay)):
		return 0
	}
	return 3
}

// EventKey keys the notification events: activated — the period id;
// renewal_due — user + ends_at + days_left; ended — user + access_until.
func EventKey(parts ...string) string { return strings.Join(parts, "|") }

// TimeKey is a time as it appears in an event key.
func TimeKey(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Applied is what ApplyPremiumPaymentEvent did: the decision, the period as
// it stands after it, and the buyer's entitlement after it (nil if none).
type Applied struct {
	Decision    Decision
	Period      Period
	Entitlement *Entitlement
}

// ReminderDue is one renewal reminder to send.
type ReminderDue struct {
	Entitlement Entitlement
	DaysLeft    int
}

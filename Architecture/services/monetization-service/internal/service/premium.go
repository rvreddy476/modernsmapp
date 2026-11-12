package service

// Momentum Premium (short-posts design 3.1-3.3, phase 0; founder took every
// default on 11 Nov 2026). Switched off by default: PREMIUM_ENABLED=false
// closes the routes (503 PREMIUM_NOT_ENABLED), the payments consumer and the
// workers.
//
// A checkout opens ONE premium_periods row and ONE payments intent
// (application premium, reference platform_premium:<period id>). The buyer
// pays in the Razorpay sheet. The pass is granted ONLY by
// ApplyPremiumPaymentEvent, from the signed payment event; there is no
// client-confirm route. After the commit, the role sync tells identity
// (`premium` with expires_at = paid end + 3 days of grace), and revokes it
// when the access ends or a refund takes it away. Nothing here charges
// anybody: renewal is a reminder, the buyer taps Pay.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/events"
	"github.com/atpost/monetization-service/internal/payments"
	"github.com/atpost/monetization-service/internal/premium"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	// ErrPremiumPaymentsUnavailable: no payments client (token key/kid unset)
	// or payments could not open the intent — 503.
	ErrPremiumPaymentsUnavailable = errors.New("PREMIUM_PAYMENTS_UNAVAILABLE")
	// ErrPremiumPassNotFound: no such pass, or not the caller's — 404.
	ErrPremiumPassNotFound = errors.New("PREMIUM_PASS_NOT_FOUND")
)

// PremiumStore is the storage Premium uses. *postgres.Store in production;
// premiumtest.Store in tests.
type PremiumStore interface {
	ListPremiumPlans(ctx context.Context) ([]premium.PlanRow, error)
	OpenPremiumCheckout(ctx context.Context, userID uuid.UUID, plan premium.PlanRow, tax premium.Breakdown, now time.Time) (*premium.Period, error)
	SetPremiumPeriodIntent(ctx context.Context, periodID, intentID uuid.UUID, orderID string) error
	GetPremiumPeriod(ctx context.Context, periodID uuid.UUID) (*premium.Period, error)
	GetPremiumEntitlement(ctx context.Context, userID uuid.UUID) (*premium.Entitlement, error)
	ApplyPremiumPaymentEvent(ctx context.Context, ev premium.Event, now time.Time, sup premium.Supplier) (premium.Applied, error)
	PremiumInvoiceStore
	DuePremiumRoleSyncs(ctx context.Context, now time.Time, limit int) ([]premium.Entitlement, error)
	MarkPremiumRoleSynced(ctx context.Context, userID uuid.UUID, accessUntil time.Time, synced *time.Time) error
	MarkPremiumRoleSyncRetry(ctx context.Context, userID uuid.UUID, next time.Time, cause string) error
	DuePremiumReminders(ctx context.Context, now time.Time) ([]premium.ReminderDue, error)
	ClaimPremiumReminder(ctx context.Context, userID uuid.UUID, endsAt time.Time, daysLeft int) (bool, error)
	ReleasePremiumReminder(ctx context.Context, userID uuid.UUID, endsAt time.Time, daysLeft int) error
	ClaimPremiumEndedNotices(ctx context.Context, now time.Time) ([]premium.Entitlement, error)
	ReleasePremiumEndedNotice(ctx context.Context, userID uuid.UUID, accessUntil time.Time) error
	AbandonStalePremiumCheckouts(ctx context.Context, cutoff time.Time) (int64, error)
}

// PremiumRoles is identity's role API (shared/identityroles.Client).
type PremiumRoles interface {
	GrantUntil(ctx context.Context, userID, role, reason string, expiresAt time.Time) error
	Revoke(ctx context.Context, userID, role, reason string) error
}

// PremiumPublisher publishes premium.* events (events.Producer).
type PremiumPublisher interface {
	PublishPremiumEvent(ctx context.Context, eventType, key string, p events.PremiumPayload) error
	PublishPremiumInvoiceEvent(ctx context.Context, eventType string, p events.PremiumInvoicePayload) error
}

// Premium is the Momentum Premium service.
type Premium struct {
	store    PremiumStore
	prices   premium.Prices
	tax      *premium.Tax
	payments payments.PremiumAPI
	localEnv bool
	roles    PremiumRoles
	pub      PremiumPublisher
	supplier premium.Supplier
	profiles ProfileSource
	now      func() time.Time
}

// NewPremium builds the service over its store and the configured prices
// (premium.PricesFromEnv). A plan without a price cannot be bought.
func NewPremium(store PremiumStore, prices premium.Prices) *Premium {
	if prices == nil {
		prices = premium.Prices{}
	}
	return &Premium{store: store, prices: prices, tax: premium.EstimatedTax(), now: time.Now}
}

// WithTax installs the GST computer (premium.TaxFromEnv). The default is the
// dev-only estimate; production boot refuses Premium without a platform GSTIN.
func (p *Premium) WithTax(t *premium.Tax) *Premium {
	if t != nil {
		p.tax = t
	}
	return p
}

// WithPayments installs the payments client. localEnv: outside local/dev an
// intent without a checkout session is refused rather than shown as payable.
func (p *Premium) WithPayments(api payments.PremiumAPI, localEnv bool) *Premium {
	p.payments, p.localEnv = api, localEnv
	return p
}

// WithRoles installs identity's role client. nil: the role sync does nothing
// and says so (no role is granted, so no access opens).
func (p *Premium) WithRoles(r PremiumRoles) *Premium {
	p.roles = r
	return p
}

// WithPublisher installs the premium.* publisher.
func (p *Premium) WithPublisher(pub PremiumPublisher) *Premium {
	p.pub = pub
	return p
}

// WithClock replaces time.Now. Tests only.
func (p *Premium) WithClock(now func() time.Time) *Premium {
	p.now = now
	return p
}

func (p *Premium) clock() time.Time { return premium.Truncate(p.now()) }

// Plans answers the catalogue with the configured prices.
func (p *Premium) Plans(ctx context.Context) ([]premium.Plan, error) {
	rows, err := p.store.ListPremiumPlans(ctx)
	if err != nil {
		return nil, err
	}
	return premium.Catalogue(rows, p.prices, p.tax, p.clock()), nil
}

// ErrPremiumAmountMismatch: the pass about to be charged does not carry the
// GST-inclusive total it was priced at. Nothing is charged — 503.
var ErrPremiumAmountMismatch = errors.New("PREMIUM_AMOUNT_MISMATCH")

// PremiumTaxView is the GST split of a pass as clients see it (prices
// exclude GST, founder 13 Nov 2026). Every field is null on a pass opened
// before that, and prices_exclude_gst is then false.
type PremiumTaxView struct {
	TaxablePaise     *int64  `json:"taxable_paise"`
	GSTPaise         *int64  `json:"gst_paise"`
	CGSTPaise        *int64  `json:"cgst_paise"`
	SGSTPaise        *int64  `json:"sgst_paise"`
	IGSTPaise        *int64  `json:"igst_paise"`
	TotalPaise       *int64  `json:"total_paise"`
	GSTRateBP        *int    `json:"gst_rate_bp"`
	SAC              *string `json:"sac"`
	TaxProvisional   *bool   `json:"tax_provisional"`
	PricesExcludeGST bool    `json:"prices_exclude_gst"`
}

func premiumTaxView(b *premium.Breakdown) PremiumTaxView {
	if b == nil {
		return PremiumTaxView{}
	}
	c := *b
	return PremiumTaxView{TaxablePaise: &c.TaxablePaise, GSTPaise: &c.TaxPaise, CGSTPaise: &c.CGSTPaise, SGSTPaise: &c.SGSTPaise,
		IGSTPaise: &c.IGSTPaise, TotalPaise: &c.TotalPaise, GSTRateBP: &c.RateBP, SAC: &c.SAC, TaxProvisional: &c.Provisional,
		PricesExcludeGST: true}
}

// PremiumCheckout is what POST /premium/checkout answers: the pass to collect
// and the public checkout values. Nothing is granted until the signed event.
// AmountPaise is the GST-inclusive total (= total_paise), the intent amount.
type PremiumCheckout struct {
	PassID        uuid.UUID               `json:"pass_id"`
	Plan          string                  `json:"plan"`
	DurationDays  int                     `json:"duration_days"`
	Status        string                  `json:"status"`
	AmountPaise   int64                   `json:"amount_paise"`
	Currency      string                  `json:"currency"`
	IntentID      uuid.UUID               `json:"intent_id"`
	ClientSession *payments.ClientSession `json:"client_session"`
	PremiumTaxView
}

// Checkout opens (or, for a retry within 30 minutes, reopens) the buyer's
// checkout for one plan. Fails closed: an unknown or retired plan, a plan
// with no configured price, a price whose GST cannot be computed, or no
// payments client — nothing is opened. The configured price is the taxable
// value; the intent is opened for the total.
func (p *Premium) Checkout(ctx context.Context, userID uuid.UUID, planCode string) (*PremiumCheckout, error) {
	rows, err := p.store.ListPremiumPlans(ctx)
	if err != nil {
		return nil, err
	}
	var plan *premium.PlanRow
	for i := range rows {
		if rows[i].Code == planCode && rows[i].Active {
			plan = &rows[i]
		}
	}
	if plan == nil {
		return nil, premium.ErrUnknownPlan
	}
	price, err := p.prices.Price(plan.Code)
	if err != nil {
		return nil, err
	}
	tax, err := p.tax.Compute(price, premiumBuyerState, p.clock())
	if err != nil {
		slog.ErrorContext(ctx, "premium: GST could not be computed; nothing sold", "plan", plan.Code, "error", err)
		return nil, err
	}
	if p.payments == nil {
		return nil, ErrPremiumPaymentsUnavailable
	}
	period, err := p.store.OpenPremiumCheckout(ctx, userID, *plan, tax, p.clock())
	if err != nil {
		return nil, err
	}
	// The intent is opened for the pass's own amount, which must be the
	// GST-inclusive total of the split it carries.
	if period.Tax == nil || period.AmountPaise != period.Tax.TotalPaise || period.AmountPaise != tax.TotalPaise {
		slog.ErrorContext(ctx, "premium: pass amount is not its GST-inclusive total; nothing charged", "pass_id", period.ID)
		return nil, ErrPremiumAmountMismatch
	}
	intent, err := p.payments.CreatePremiumIntent(ctx, payments.PremiumIntentInput{
		PeriodID: period.ID, PayerID: userID, AmountMinor: period.AmountPaise, Currency: period.Currency,
	})
	if err != nil {
		slog.ErrorContext(ctx, "premium: create intent failed", "pass_id", period.ID, "error", err)
		return nil, fmt.Errorf("%w: %v", ErrPremiumPaymentsUnavailable, err)
	}
	orderID := intent.ProviderRef
	if intent.ClientSession != nil && intent.ClientSession.OrderID != "" {
		orderID = intent.ClientSession.OrderID
	}
	if err := p.store.SetPremiumPeriodIntent(ctx, period.ID, intent.ID, orderID); err != nil {
		return nil, err
	}
	p.recordBuyerName(ctx, userID, period.ID)
	if intent.ClientSession == nil && !p.localEnv {
		return nil, ErrPaymentSessionUnavailable
	}
	return &PremiumCheckout{
		PassID: period.ID, Plan: period.PlanCode, DurationDays: period.DurationDays, Status: "pending_payment",
		AmountPaise: period.AmountPaise, Currency: period.Currency, IntentID: intent.ID, ClientSession: intent.ClientSession,
		PremiumTaxView: premiumTaxView(period.Tax),
	}, nil
}

// premiumBuyerState is the place of supply the checkout passes: "" because
// the platform keeps no address for the buyer, so the place of supply is
// the supplier's state (IGST Act s.12(2)(b); adviser item, premium/tax.go).
const premiumBuyerState = ""

// PremiumStatusView is GET /premium/me.
type PremiumStatusView struct {
	premium.Status
	// RenewalPlans are the plans the buyer can pay for now (renewal stacks).
	RenewalPlans []premium.Plan `json:"renewal_plans"`
}

// Status answers the buyer's Premium.
func (p *Premium) Status(ctx context.Context, userID uuid.UUID) (*PremiumStatusView, error) {
	ent, err := p.store.GetPremiumEntitlement(ctx, userID)
	if err != nil {
		return nil, err
	}
	plans, err := p.Plans(ctx)
	if err != nil {
		return nil, err
	}
	return &PremiumStatusView{Status: premium.StatusOf(ent, p.clock()), RenewalPlans: plans}, nil
}

// PremiumPassView is a pass as its buyer sees it (the app's poll after the
// sheet closes).
type PremiumPassView struct {
	PassID      uuid.UUID  `json:"pass_id"`
	Plan        string     `json:"plan"`
	Status      string     `json:"status"`
	AmountPaise int64      `json:"amount_paise"`
	Currency    string     `json:"currency"`
	StartsAt    *time.Time `json:"starts_at"`
	EndsAt      *time.Time `json:"ends_at"`
	// InvoiceID is the pass's tax invoice once issued (on the signed
	// capture), else null.
	InvoiceID *uuid.UUID `json:"invoice_id"`
	PremiumTaxView
}

// Pass answers one of the buyer's passes; anyone else gets not found.
// Status: confirming (open, not yet paid) | paid | failed | refunded.
func (p *Premium) Pass(ctx context.Context, userID, passID uuid.UUID) (*PremiumPassView, error) {
	period, err := p.store.GetPremiumPeriod(ctx, passID)
	if err != nil {
		return nil, err
	}
	if period == nil || period.UserID != userID {
		return nil, ErrPremiumPassNotFound
	}
	status := period.Status
	switch status {
	case premium.StatusCreated:
		status = "confirming"
	case premium.StatusAbandoned:
		status = premium.StatusFailed
	}
	invoiceID, err := p.store.PremiumInvoiceIDForPass(ctx, period.ID)
	if err != nil {
		return nil, err
	}
	return &PremiumPassView{PassID: period.ID, Plan: period.PlanCode, Status: status, AmountPaise: period.AmountPaise,
		Currency: period.Currency, StartsAt: period.PeriodStart, EndsAt: period.PeriodEnd, InvoiceID: invoiceID,
		PremiumTaxView: premiumTaxView(period.Tax)}, nil
}

// ApplyPremiumPaymentEvent applies one signed payments event (the consumer's
// Applier) and, after it committed, publishes the receipt and syncs the
// buyer's role at once. A publish or role failure never undoes the commit:
// the role sync worker retries.
func (p *Premium) ApplyPremiumPaymentEvent(ctx context.Context, ev premium.Event) (premium.Outcome, error) {
	applied, err := p.store.ApplyPremiumPaymentEvent(ctx, ev, p.clock(), p.supplier)
	if err != nil {
		return "", err
	}
	if applied.Document != nil {
		p.publishDocument(ctx, *applied.Document)
	}
	d := applied.Decision
	if d.Notify == premium.EventActivated && applied.Entitlement != nil {
		pass := applied.Period
		p.publish(ctx, premium.EventActivated, pass.ID.String(), events.PremiumPayload{
			UserID: pass.UserID.String(), PeriodID: pass.ID.String(), PlanCode: pass.PlanCode,
			AmountPaise: pass.AmountPaise, Currency: pass.Currency,
			EndsAt: premium.TimeKey(applied.Entitlement.EndsAt), GraceEndsAt: premium.TimeKey(applied.Entitlement.AccessUntil),
		})
	}
	if d.Entitlement != nil {
		if ent, err := p.store.GetPremiumEntitlement(ctx, applied.Period.UserID); err == nil && ent != nil {
			p.syncRole(ctx, *ent)
		}
	}
	return d.Outcome, nil
}

func (p *Premium) publish(ctx context.Context, eventType, key string, payload events.PremiumPayload) {
	if p.pub == nil {
		return
	}
	if err := p.pub.PublishPremiumEvent(ctx, eventType, key, payload); err != nil {
		slog.WarnContext(ctx, "premium: publish event failed", "event_type", eventType, "user_id", payload.UserID, "error", err)
	}
}

const maxRoleError = 300

// syncRole makes identity agree with one entitlement: grant `premium` until
// access_until, or revoke it once access has ended. Failures are retried with
// backoff by the worker; a permanent refusal (identity does not accept the
// role yet) keeps retrying at the cap so it heals once identity ships.
func (p *Premium) syncRole(ctx context.Context, ent premium.Entitlement) {
	if p.roles == nil {
		return
	}
	action, until := premium.RoleSync(ent, p.clock())
	var err error
	switch action {
	case premium.RoleNone:
		return
	case premium.RoleGrant:
		err = p.roles.GrantUntil(ctx, ent.UserID.String(), premium.Role, "Momentum Premium pass", until)
	case premium.RoleRevoke:
		err = p.roles.Revoke(ctx, ent.UserID.String(), premium.Role, "Momentum Premium ended")
	}
	if err == nil {
		var synced *time.Time
		if action == premium.RoleGrant {
			u := until
			synced = &u
		}
		if merr := p.store.MarkPremiumRoleSynced(ctx, ent.UserID, ent.AccessUntil, synced); merr != nil {
			slog.ErrorContext(ctx, "premium: record role sync", "user_id", ent.UserID, "error", merr)
		}
		return
	}
	cause := err.Error()
	if len(cause) > maxRoleError {
		cause = cause[:maxRoleError]
	}
	slog.ErrorContext(ctx, "premium: role sync failed; will retry", "user_id", ent.UserID, "action", action, "attempts", ent.RoleAttempts, "error", err)
	if merr := p.store.MarkPremiumRoleSyncRetry(ctx, ent.UserID, p.clock().Add(premium.Backoff(ent.RoleAttempts)), cause); merr != nil {
		slog.ErrorContext(ctx, "premium: record role sync retry", "user_id", ent.UserID, "error", merr)
	}
}

// SyncRoles is the role sync worker's pass: every entitlement identity does
// not yet agree with (a new or extended pass, a refund, a lapse past grace).
func (p *Premium) SyncRoles(ctx context.Context) (int, error) {
	if p.roles == nil {
		return 0, nil
	}
	due, err := p.store.DuePremiumRoleSyncs(ctx, p.clock(), 100)
	if err != nil {
		return 0, err
	}
	for _, ent := range due {
		p.syncRole(ctx, ent)
	}
	return len(due), nil
}

// SendReminders is the reminder pass: premium.renewal_due 3 days before the
// paid end and on the day, once each per end. A failed publish releases the
// claim so the next pass sends it.
func (p *Premium) SendReminders(ctx context.Context) (int, error) {
	due, err := p.store.DuePremiumReminders(ctx, p.clock())
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, r := range due {
		e := r.Entitlement
		claimed, err := p.store.ClaimPremiumReminder(ctx, e.UserID, e.EndsAt, r.DaysLeft)
		if err != nil || !claimed {
			if err != nil {
				slog.ErrorContext(ctx, "premium: claim reminder", "user_id", e.UserID, "error", err)
			}
			continue
		}
		days := r.DaysLeft
		payload := events.PremiumPayload{UserID: e.UserID.String(), EndsAt: premium.TimeKey(e.EndsAt),
			GraceEndsAt: premium.TimeKey(e.AccessUntil), DaysLeft: &days}
		if p.pub == nil {
			continue
		}
		key := premium.EventKey(e.UserID.String(), premium.TimeKey(e.EndsAt), fmt.Sprint(days))
		if err := p.pub.PublishPremiumEvent(ctx, premium.EventRenewalDue, key, payload); err != nil {
			slog.WarnContext(ctx, "premium: publish renewal_due; will retry", "user_id", e.UserID, "error", err)
			if rerr := p.store.ReleasePremiumReminder(ctx, e.UserID, e.EndsAt, r.DaysLeft); rerr != nil {
				slog.ErrorContext(ctx, "premium: release reminder claim", "user_id", e.UserID, "error", rerr)
			}
			continue
		}
		sent++
	}
	return sent, nil
}

// SendEndedNotices announces every access that ended (grace over, or a refund
// that left no coverage): premium.ended, once per access_until.
func (p *Premium) SendEndedNotices(ctx context.Context) (int, error) {
	if p.pub == nil {
		return 0, nil
	}
	ended, err := p.store.ClaimPremiumEndedNotices(ctx, p.clock())
	if err != nil {
		return 0, err
	}
	for _, e := range ended {
		key := premium.EventKey(e.UserID.String(), premium.TimeKey(e.AccessUntil))
		payload := events.PremiumPayload{UserID: e.UserID.String(), EndsAt: premium.TimeKey(e.EndsAt), GraceEndsAt: premium.TimeKey(e.AccessUntil)}
		if err := p.pub.PublishPremiumEvent(ctx, premium.EventEnded, key, payload); err != nil {
			slog.WarnContext(ctx, "premium: publish ended; will retry", "user_id", e.UserID, "error", err)
			if rerr := p.store.ReleasePremiumEndedNotice(ctx, e.UserID, e.AccessUntil); rerr != nil {
				slog.ErrorContext(ctx, "premium: release ended notice", "user_id", e.UserID, "error", rerr)
			}
		}
	}
	return len(ended), nil
}

// SweepAbandonedCheckouts marks checkouts unpaid for 30 minutes abandoned.
func (p *Premium) SweepAbandonedCheckouts(ctx context.Context) (int64, error) {
	return p.store.AbandonStalePremiumCheckouts(ctx, p.clock().Add(-premium.CheckoutTTL))
}

var _ PremiumStore = (*postgres.Store)(nil)

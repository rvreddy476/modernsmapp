package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/atpost/monetization-service/internal/service/premiumtest"
	"github.com/atpost/shared/identityroles"
	"github.com/google/uuid"
)

// Momentum Premium against the in-memory store, which applies events through
// the same premium.Decide as the Postgres store.

type premiumRig struct {
	ctx   context.Context
	store *premiumtest.Store
	pay   *premiumtest.Payments
	roles *premiumtest.Roles
	pub   *premiumtest.Publisher
	svc   *Premium
	now   time.Time
	buyer uuid.UUID
	seq   int
}

func newPremiumRig(t *testing.T, prices premium.Prices) *premiumRig {
	t.Helper()
	r := &premiumRig{ctx: context.Background(), store: premiumtest.New(), pay: premiumtest.NewPayments(),
		roles: &premiumtest.Roles{}, pub: &premiumtest.Publisher{},
		now: time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC), buyer: uuid.New()}
	r.svc = NewPremium(r.store, prices).WithPayments(r.pay, false).WithRoles(r.roles).WithPublisher(r.pub).
		WithClock(func() time.Time { return r.now })
	return r
}

func priced() premium.Prices { return premium.Prices{premium.Plan30d: 14900, premium.Plan365d: 149900} }

func (r *premiumRig) checkout(t *testing.T, plan string) *PremiumCheckout {
	t.Helper()
	co, err := r.svc.Checkout(r.ctx, r.buyer, plan)
	if err != nil {
		t.Fatalf("checkout %s: %v", plan, err)
	}
	return co
}

// event is the signed payments event for a pass, as payments-service would
// stamp it (payer, amount, currency, the bound intent).
func (r *premiumRig) event(t *testing.T, co *PremiumCheckout, eventType string) premium.Event {
	t.Helper()
	r.seq++
	return premium.Event{EventID: uuid.NewString(), EventType: eventType, IntentID: co.IntentID.String(),
		PeriodID: co.PassID, PayerID: r.buyer, AmountMinor: co.AmountPaise, Currency: "INR"}
}

func (r *premiumRig) apply(t *testing.T, ev premium.Event) premium.Outcome {
	t.Helper()
	out, err := r.svc.ApplyPremiumPaymentEvent(r.ctx, ev)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return out
}

func (r *premiumRig) status(t *testing.T) *PremiumStatusView {
	t.Helper()
	s, err := r.svc.Status(r.ctx, r.buyer)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func grants(calls []premiumtest.RoleCall) []premiumtest.RoleCall {
	var out []premiumtest.RoleCall
	for _, c := range calls {
		if c.Op == "grant" {
			out = append(out, c)
		}
	}
	return out
}

// Checkout opens a payment and grants nothing: no entitlement, no role call,
// status inactive. Only the signed event activates, and the role is granted
// with expires_at = paid end + 3 days.
func TestPremiumActivatesOnlyFromTheSignedEvent(t *testing.T) {
	r := newPremiumRig(t, priced())
	co := r.checkout(t, premium.Plan30d)
	if co.AmountPaise != 14900 || co.Status != "pending_payment" || co.ClientSession == nil {
		t.Fatalf("checkout %+v", co)
	}
	if s := r.status(t); s.Active || s.EndsAt != nil {
		t.Fatalf("checkout must grant nothing: %+v", s)
	}
	if len(r.roles.Snapshot()) != 0 {
		t.Fatalf("no role before the event: %+v", r.roles.Snapshot())
	}
	if n, _ := r.svc.SyncRoles(r.ctx); n != 0 {
		t.Fatal("nothing to sync before the event")
	}
	if pass, _ := r.svc.Pass(r.ctx, r.buyer, co.PassID); pass.Status != "confirming" {
		t.Fatalf("pass %+v", pass)
	}

	if out := r.apply(t, r.event(t, co, "payment.succeeded")); out != premium.OutcomeActivated {
		t.Fatalf("outcome %s", out)
	}
	s := r.status(t)
	wantEnd := r.now.AddDate(0, 0, 30)
	if !s.Active || s.InGrace || !s.EndsAt.Equal(wantEnd) || !s.GraceEndsAt.Equal(wantEnd.Add(72*time.Hour)) {
		t.Fatalf("status %+v", s)
	}
	g := grants(r.roles.Snapshot())
	if len(g) != 1 || g[0].Role != identityroles.RolePremium || g[0].UserID != r.buyer.String() ||
		!g[0].ExpiresAt.Equal(wantEnd.Add(72*time.Hour)) {
		t.Fatalf("role grant %+v, want premium until %v", g, wantEnd.Add(72*time.Hour))
	}
	if got := r.pub.OfType(premium.EventActivated); len(got) != 1 || got[0].Payload.PeriodID != co.PassID.String() {
		t.Fatalf("receipt %+v", got)
	}
	if pass, _ := r.svc.Pass(r.ctx, r.buyer, co.PassID); pass.Status != premium.StatusPaid || pass.EndsAt == nil {
		t.Fatalf("pass %+v", pass)
	}
	// Synced: the worker has nothing more to do.
	if n, _ := r.svc.SyncRoles(r.ctx); n != 0 {
		t.Fatalf("already in sync, %d due", n)
	}
}

// A replayed event (same id) changes nothing; a second success for the same
// pass under a new id does not extend it either.
func TestPremiumReplayIsIdempotent(t *testing.T) {
	r := newPremiumRig(t, priced())
	co := r.checkout(t, premium.Plan30d)
	ev := r.event(t, co, "payment.succeeded")
	r.apply(t, ev)
	first := r.status(t)
	if out := r.apply(t, ev); out != premium.OutcomeDuplicate {
		t.Fatalf("replay outcome %s", out)
	}
	if out := r.apply(t, r.event(t, co, "payment.succeeded")); out != premium.OutcomeIgnored {
		t.Fatalf("second capture outcome %s", out)
	}
	if s := r.status(t); !s.EndsAt.Equal(*first.EndsAt) {
		t.Fatalf("replay extended the pass: %v -> %v", first.EndsAt, s.EndsAt)
	}
	if g := grants(r.roles.Snapshot()); len(g) != 1 {
		t.Fatalf("one grant only, got %+v", g)
	}
	if got := r.pub.OfType(premium.EventActivated); len(got) != 1 {
		t.Fatalf("one receipt only, got %d", len(got))
	}
}

// A second pass stacks on the current end; the role is re-granted with the
// later expiry.
func TestPremiumStackingExtendsFromTheCurrentEnd(t *testing.T) {
	r := newPremiumRig(t, priced())
	r.apply(t, r.event(t, r.checkout(t, premium.Plan30d), "payment.succeeded"))
	firstEnd := r.now.AddDate(0, 0, 30)
	r.now = r.now.Add(5 * 24 * time.Hour)
	co := r.checkout(t, premium.Plan365d)
	if co.AmountPaise != 149900 {
		t.Fatalf("365d amount %d", co.AmountPaise)
	}
	r.apply(t, r.event(t, co, "payment.succeeded"))
	wantEnd := firstEnd.AddDate(0, 0, 365)
	s := r.status(t)
	if !s.EndsAt.Equal(wantEnd) {
		t.Fatalf("ends %v, want %v", s.EndsAt, wantEnd)
	}
	g := grants(r.roles.Snapshot())
	if len(g) != 2 || !g[1].ExpiresAt.Equal(wantEnd.Add(premium.GracePeriod)) {
		t.Fatalf("grants %+v", g)
	}
	pass, _ := r.svc.Pass(r.ctx, r.buyer, co.PassID)
	if !pass.StartsAt.Equal(firstEnd) {
		t.Fatalf("second pass starts %v, want %v", pass.StartsAt, firstEnd)
	}
}

// After the paid end the buyer is in grace (still active); after the grace
// the role is revoked and premium.ended is sent once.
func TestPremiumGraceThenExpiryRevokes(t *testing.T) {
	r := newPremiumRig(t, priced())
	r.apply(t, r.event(t, r.checkout(t, premium.Plan30d), "payment.succeeded"))
	end := r.now.AddDate(0, 0, 30)

	r.now = end.Add(time.Hour)
	if s := r.status(t); !s.Active || !s.InGrace {
		t.Fatalf("in grace: %+v", s)
	}
	if n, _ := r.svc.SyncRoles(r.ctx); n != 0 {
		t.Fatal("no revoke during grace")
	}

	r.now = end.Add(premium.GracePeriod)
	if s := r.status(t); s.Active || s.InGrace {
		t.Fatalf("expired: %+v", s)
	}
	if n, _ := r.svc.SyncRoles(r.ctx); n != 1 {
		t.Fatalf("one revoke due, got %d", n)
	}
	calls := r.roles.Snapshot()
	if last := calls[len(calls)-1]; last.Op != "revoke" || last.Role != "premium" {
		t.Fatalf("calls %+v", calls)
	}
	if n, _ := r.svc.SyncRoles(r.ctx); n != 0 {
		t.Fatal("revoked once")
	}
	if n, _ := r.svc.SendEndedNotices(r.ctx); n != 1 {
		t.Fatalf("ended notices %d", n)
	}
	if n, _ := r.svc.SendEndedNotices(r.ctx); n != 0 {
		t.Fatal("ended announced once")
	}
}

// A full refund ends access at once (no grace) and revokes the role; a
// partial refund leaves the pass alone.
func TestPremiumRefundRevokes(t *testing.T) {
	r := newPremiumRig(t, priced())
	co := r.checkout(t, premium.Plan30d)
	r.apply(t, r.event(t, co, "payment.succeeded"))
	r.now = r.now.Add(48 * time.Hour)

	partial := r.event(t, co, "payment.refunded")
	partial.AmountMinor, partial.PayerID, partial.Currency = 4900, uuid.Nil, ""
	if out := r.apply(t, partial); out != premium.OutcomePartialRefund {
		t.Fatalf("partial: %s", out)
	}
	if s := r.status(t); !s.Active {
		t.Fatal("a partial refund keeps the pass")
	}

	rest := r.event(t, co, "payment.refunded")
	rest.AmountMinor, rest.PayerID, rest.Currency = 10000, uuid.Nil, ""
	if out := r.apply(t, rest); out != premium.OutcomeRefunded {
		t.Fatalf("full: %s", out)
	}
	if s := r.status(t); s.Active || s.InGrace {
		t.Fatalf("refunded buyer must have no access: %+v", s)
	}
	calls := r.roles.Snapshot()
	if last := calls[len(calls)-1]; last.Op != "revoke" {
		t.Fatalf("refund must revoke at once: %+v", calls)
	}
	if pass, _ := r.svc.Pass(r.ctx, r.buyer, co.PassID); pass.Status != premium.StatusRefunded {
		t.Fatalf("pass %+v", pass)
	}
}

// A refund of the earlier of two stacked passes re-grants with the shorter
// expiry: the later pass moves up to start at the refund.
func TestPremiumRefundOfStackedPassShortensTheRole(t *testing.T) {
	r := newPremiumRig(t, priced())
	first := r.checkout(t, premium.Plan30d)
	r.apply(t, r.event(t, first, "payment.succeeded"))
	r.apply(t, r.event(t, r.checkout(t, premium.Plan30d), "payment.succeeded"))
	r.now = r.now.Add(10 * 24 * time.Hour)
	ref := r.event(t, first, "payment.refunded")
	ref.PayerID, ref.Currency = uuid.Nil, ""
	r.apply(t, ref)
	wantEnd := r.now.AddDate(0, 0, 30)
	if s := r.status(t); !s.EndsAt.Equal(wantEnd) {
		t.Fatalf("ends %v, want %v", s.EndsAt, wantEnd)
	}
	g := grants(r.roles.Snapshot())
	if last := g[len(g)-1]; !last.ExpiresAt.Equal(wantEnd.Add(premium.GracePeriod)) {
		t.Fatalf("last grant %+v", last)
	}
}

// Price unset: checkout fails closed before any row or intent.
func TestPremiumPriceUnsetFailsClosed(t *testing.T) {
	r := newPremiumRig(t, premium.Prices{premium.Plan365d: 149900})
	if _, err := r.svc.Checkout(r.ctx, r.buyer, premium.Plan30d); !errors.Is(err, premium.ErrPriceNotSet) {
		t.Fatalf("err = %v, want ErrPriceNotSet", err)
	}
	if len(r.store.Periods) != 0 || len(r.pay.Calls) != 0 {
		t.Fatal("nothing may be opened without a price")
	}
	plans, _ := r.svc.Plans(r.ctx)
	if plans[0].Available || plans[0].PricePaise != nil || !plans[1].Available {
		t.Fatalf("plans %+v", plans)
	}
	r = newPremiumRig(t, nil)
	if _, err := r.svc.Checkout(r.ctx, r.buyer, premium.Plan365d); !errors.Is(err, premium.ErrPriceNotSet) {
		t.Fatalf("no prices at all: %v", err)
	}
}

func TestPremiumCheckoutRefusals(t *testing.T) {
	r := newPremiumRig(t, priced())
	if _, err := r.svc.Checkout(r.ctx, r.buyer, "premium_7d"); !errors.Is(err, premium.ErrUnknownPlan) {
		t.Fatalf("unknown plan: %v", err)
	}
	r.store.Plans[0].Active = false
	if _, err := r.svc.Checkout(r.ctx, r.buyer, premium.Plan30d); !errors.Is(err, premium.ErrUnknownPlan) {
		t.Fatalf("retired plan: %v", err)
	}
	r = newPremiumRig(t, priced())
	r.svc.payments = nil
	if _, err := r.svc.Checkout(r.ctx, r.buyer, premium.Plan30d); !errors.Is(err, ErrPremiumPaymentsUnavailable) {
		t.Fatalf("no payments client: %v", err)
	}
	r = newPremiumRig(t, priced())
	r.pay.Fail = errors.New("payments down")
	if _, err := r.svc.Checkout(r.ctx, r.buyer, premium.Plan30d); !errors.Is(err, ErrPremiumPaymentsUnavailable) {
		t.Fatalf("payments down: %v", err)
	}
	r = newPremiumRig(t, priced())
	r.pay.NoSession = true
	if _, err := r.svc.Checkout(r.ctx, r.buyer, premium.Plan30d); !errors.Is(err, ErrPaymentSessionUnavailable) {
		t.Fatalf("no session outside dev: %v", err)
	}
}

// A double tap reuses the open checkout (one intent); after the TTL a new one
// is opened and the old one is abandoned.
func TestPremiumCheckoutReuseWithinTTL(t *testing.T) {
	r := newPremiumRig(t, priced())
	a := r.checkout(t, premium.Plan30d)
	b := r.checkout(t, premium.Plan30d)
	if a.PassID != b.PassID || a.IntentID != b.IntentID {
		t.Fatal("a retry must reuse the open checkout")
	}
	r.now = r.now.Add(premium.CheckoutTTL + time.Minute)
	c := r.checkout(t, premium.Plan30d)
	if c.PassID == a.PassID {
		t.Fatal("a stale checkout must not be reused")
	}
	if r.store.Periods[a.PassID].Status != premium.StatusAbandoned {
		t.Fatalf("old checkout %s", r.store.Periods[a.PassID].Status)
	}
	// A capture that still arrives for the abandoned one activates it.
	if out := r.apply(t, r.event(t, a, "payment.succeeded")); out != premium.OutcomeActivated {
		t.Fatalf("late capture: %s", out)
	}
}

// Someone else's payment for this pass, or the wrong amount, grants nothing.
func TestPremiumMismatchGrantsNothing(t *testing.T) {
	r := newPremiumRig(t, priced())
	co := r.checkout(t, premium.Plan30d)
	ev := r.event(t, co, "payment.succeeded")
	ev.PayerID = uuid.New()
	if out := r.apply(t, ev); out != premium.OutcomeMismatch {
		t.Fatalf("foreign payer: %s", out)
	}
	ev = r.event(t, co, "payment.succeeded")
	ev.AmountMinor = 100
	if out := r.apply(t, ev); out != premium.OutcomeMismatch {
		t.Fatalf("short amount: %s", out)
	}
	if s := r.status(t); s.Active || len(r.roles.Snapshot()) != 0 {
		t.Fatalf("mismatch granted something: %+v %+v", s, r.roles.Snapshot())
	}
}

// Identity refusing (it does not accept `premium` yet) is retried with
// backoff, never dropped; the grant goes out once identity agrees.
func TestPremiumRoleSyncRetriesUntilIdentityAccepts(t *testing.T) {
	r := newPremiumRig(t, priced())
	r.roles.Fail = identityroles.ErrNotGrantable
	r.apply(t, r.event(t, r.checkout(t, premium.Plan30d), "payment.succeeded"))
	ent := r.store.Ents[r.buyer]
	if ent.RoleSyncedUntil != nil || ent.RoleAttempts != 1 || ent.LastError == "" {
		t.Fatalf("after a refusal: %+v", ent)
	}
	if n, _ := r.svc.SyncRoles(r.ctx); n != 0 {
		t.Fatal("the retry waits for its backoff")
	}
	r.now = r.now.Add(premium.Backoff(1) + time.Second)
	r.roles.Fail = nil
	if n, _ := r.svc.SyncRoles(r.ctx); n != 1 {
		t.Fatal("due after backoff")
	}
	if ent.RoleSyncedUntil == nil || !ent.RoleSyncedUntil.Equal(ent.AccessUntil) {
		t.Fatalf("synced %+v", ent)
	}
}

// A store failure is an error for the consumer to retry: nothing published.
func TestPremiumApplyErrorIsRetried(t *testing.T) {
	r := newPremiumRig(t, priced())
	co := r.checkout(t, premium.Plan30d)
	r.store.FailApply = errors.New("db down")
	if _, err := r.svc.ApplyPremiumPaymentEvent(r.ctx, r.event(t, co, "payment.succeeded")); err == nil {
		t.Fatal("want an error")
	}
	if len(r.pub.Sent) != 0 || len(r.roles.Snapshot()) != 0 {
		t.Fatal("nothing may happen on a failed apply")
	}
}

// Reminders: 3 days before the end and on the day, once each; none after a
// second pass moved the end.
func TestPremiumRenewalReminders(t *testing.T) {
	r := newPremiumRig(t, priced())
	r.apply(t, r.event(t, r.checkout(t, premium.Plan30d), "payment.succeeded"))
	end := r.now.AddDate(0, 0, 30)

	r.now = end.Add(-5 * 24 * time.Hour)
	if n, _ := r.svc.SendReminders(r.ctx); n != 0 {
		t.Fatal("too early")
	}
	r.now = end.Add(-60 * time.Hour)
	if n, _ := r.svc.SendReminders(r.ctx); n != 1 {
		t.Fatal("3-day reminder")
	}
	if n, _ := r.svc.SendReminders(r.ctx); n != 0 {
		t.Fatal("3-day reminder once")
	}
	r.now = end.Add(-2 * time.Hour)
	if n, _ := r.svc.SendReminders(r.ctx); n != 1 {
		t.Fatal("day-of reminder")
	}
	due := r.pub.OfType(premium.EventRenewalDue)
	if len(due) != 2 || *due[0].Payload.DaysLeft != 3 || *due[1].Payload.DaysLeft != 0 {
		t.Fatalf("reminders %+v", due)
	}

	// A failed publish is released and sent next time.
	r2 := newPremiumRig(t, priced())
	r2.apply(t, r2.event(t, r2.checkout(t, premium.Plan30d), "payment.succeeded"))
	r2.now = r2.now.AddDate(0, 0, 30).Add(-60 * time.Hour)
	r2.pub.Fail = errors.New("kafka down")
	if n, _ := r2.svc.SendReminders(r2.ctx); n != 0 {
		t.Fatal("nothing sent while kafka is down")
	}
	r2.pub.Fail = nil
	if n, _ := r2.svc.SendReminders(r2.ctx); n != 1 {
		t.Fatal("the released reminder must be sent")
	}
}

func TestPremiumPassIsTheBuyersOnly(t *testing.T) {
	r := newPremiumRig(t, priced())
	co := r.checkout(t, premium.Plan30d)
	if _, err := r.svc.Pass(r.ctx, uuid.New(), co.PassID); !errors.Is(err, ErrPremiumPassNotFound) {
		t.Fatalf("someone else's pass: %v", err)
	}
	if _, err := r.svc.Pass(r.ctx, r.buyer, uuid.New()); !errors.Is(err, ErrPremiumPassNotFound) {
		t.Fatalf("no such pass: %v", err)
	}
}

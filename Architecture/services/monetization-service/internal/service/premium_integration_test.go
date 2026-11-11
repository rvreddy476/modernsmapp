//go:build integration

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/atpost/monetization-service/internal/service/premiumtest"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Momentum Premium against PostgreSQL (migration 027), through the real store
// and service with a fake payments-service and a recording identity.
// openTestPool → requireTestDSN REFUSES any database whose name does not end
// in _test (and "app") before a connection is opened. Written 11 Nov 2026,
// NOT run in this change (no local database): CI runs it.

type premiumDBRig struct {
	ctx   context.Context
	pool  *pgxpool.Pool
	svc   *Premium
	pay   *premiumtest.Payments
	roles *premiumtest.Roles
	pub   *premiumtest.Publisher
	now   time.Time
	buyer uuid.UUID
}

func newPremiumDBRig(t *testing.T) *premiumDBRig {
	t.Helper()
	ctx, pool := openTestPool(t)
	r := &premiumDBRig{ctx: ctx, pool: pool, pay: premiumtest.NewPayments(), roles: &premiumtest.Roles{},
		pub: &premiumtest.Publisher{}, now: time.Now().UTC().Truncate(time.Microsecond), buyer: uuid.New()}
	r.svc = NewPremium(postgres.New(pool), premium.Prices{premium.Plan30d: 14900, premium.Plan365d: 149900}).
		WithPayments(r.pay, false).WithRoles(r.roles).WithPublisher(r.pub).WithClock(func() time.Time { return r.now })
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM premium_payment_inbox WHERE period_id IN (SELECT id FROM premium_periods WHERE user_id = $1)`, r.buyer)
		_, _ = pool.Exec(bg, `DELETE FROM premium_renewal_reminders WHERE user_id = $1`, r.buyer)
		_, _ = pool.Exec(bg, `DELETE FROM premium_periods WHERE user_id = $1`, r.buyer)
		_, _ = pool.Exec(bg, `DELETE FROM premium_entitlements WHERE user_id = $1`, r.buyer)
	})
	return r
}

func (r *premiumDBRig) paid(t *testing.T, plan string) (*PremiumCheckout, premium.Event) {
	t.Helper()
	co, err := r.svc.Checkout(r.ctx, r.buyer, plan)
	if err != nil {
		t.Fatal(err)
	}
	ev := premium.Event{EventID: uuid.NewString(), EventType: "payment.succeeded", IntentID: co.IntentID.String(),
		PeriodID: co.PassID, PayerID: r.buyer, AmountMinor: co.AmountPaise, Currency: "INR"}
	return co, ev
}

func TestPremiumIntegrationActivateReplayStackRefund(t *testing.T) {
	r := newPremiumDBRig(t)
	co, ev := r.paid(t, premium.Plan30d)
	if s, _ := r.svc.Status(r.ctx, r.buyer); s.Active {
		t.Fatal("checkout must grant nothing")
	}
	if out, err := r.svc.ApplyPremiumPaymentEvent(r.ctx, ev); err != nil || out != premium.OutcomeActivated {
		t.Fatalf("activate: %s %v", out, err)
	}
	if out, err := r.svc.ApplyPremiumPaymentEvent(r.ctx, ev); err != nil || out != premium.OutcomeDuplicate {
		t.Fatalf("replay: %s %v", out, err)
	}
	firstEnd := r.now.AddDate(0, 0, 30)
	s, _ := r.svc.Status(r.ctx, r.buyer)
	if !s.Active || !s.EndsAt.Equal(firstEnd) {
		t.Fatalf("status %+v", s)
	}
	if g := grants(r.roles.Snapshot()); len(g) != 1 || !g[0].ExpiresAt.Equal(firstEnd.Add(premium.GracePeriod)) {
		t.Fatalf("grants %+v", g)
	}

	_, ev2 := r.paid(t, premium.Plan365d)
	if out, _ := r.svc.ApplyPremiumPaymentEvent(r.ctx, ev2); out != premium.OutcomeActivated {
		t.Fatalf("second pass: %s", out)
	}
	if s, _ = r.svc.Status(r.ctx, r.buyer); !s.EndsAt.Equal(firstEnd.AddDate(0, 0, 365)) {
		t.Fatalf("stacked end %v", s.EndsAt)
	}

	// Refund the first pass: the second moves up to start now.
	ref := premium.Event{EventID: uuid.NewString(), EventType: "payment.refunded", IntentID: co.IntentID.String(),
		PeriodID: co.PassID, AmountMinor: co.AmountPaise}
	if out, _ := r.svc.ApplyPremiumPaymentEvent(r.ctx, ref); out != premium.OutcomeRefunded {
		t.Fatalf("refund: %s", out)
	}
	if s, _ = r.svc.Status(r.ctx, r.buyer); !s.EndsAt.Equal(r.now.AddDate(0, 0, 365)) {
		t.Fatalf("after refund end %v, want %v", s.EndsAt, r.now.AddDate(0, 0, 365))
	}
}

// Two passes paid at the same moment stack (the advisory lock), never both
// starting at the same end.
func TestPremiumIntegrationConcurrentCapturesStack(t *testing.T) {
	r := newPremiumDBRig(t)
	_, a := r.paid(t, premium.Plan30d)
	_, b := r.paid(t, premium.Plan365d)
	var wg sync.WaitGroup
	for _, ev := range []premium.Event{a, b} {
		wg.Add(1)
		go func(ev premium.Event) {
			defer wg.Done()
			if _, err := r.svc.ApplyPremiumPaymentEvent(r.ctx, ev); err != nil {
				t.Error(err)
			}
		}(ev)
	}
	wg.Wait()
	s, _ := r.svc.Status(r.ctx, r.buyer)
	want := r.now.AddDate(0, 0, 30).AddDate(0, 0, 365)
	alt := r.now.AddDate(0, 0, 365).AddDate(0, 0, 30)
	if !s.EndsAt.Equal(want) && !s.EndsAt.Equal(alt) {
		t.Fatalf("concurrent passes did not stack: ends %v", s.EndsAt)
	}
}

func TestPremiumIntegrationExpiryRevokesAndReminds(t *testing.T) {
	r := newPremiumDBRig(t)
	_, ev := r.paid(t, premium.Plan30d)
	if _, err := r.svc.ApplyPremiumPaymentEvent(r.ctx, ev); err != nil {
		t.Fatal(err)
	}
	end := r.now.AddDate(0, 0, 30)
	r.now = end.Add(-60 * time.Hour)
	if n, err := r.svc.SendReminders(r.ctx); err != nil || n < 1 {
		t.Fatalf("reminder: %d %v", n, err)
	}
	r.now = end.Add(premium.GracePeriod + time.Minute)
	if _, err := r.svc.SyncRoles(r.ctx); err != nil {
		t.Fatal(err)
	}
	calls := r.roles.Snapshot()
	if last := calls[len(calls)-1]; last.Op != "revoke" || last.UserID != r.buyer.String() {
		t.Fatalf("calls %+v", calls)
	}
	if n, err := r.svc.SendEndedNotices(r.ctx); err != nil || n < 1 {
		t.Fatalf("ended: %d %v", n, err)
	}
}

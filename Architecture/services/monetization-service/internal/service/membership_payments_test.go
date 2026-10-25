package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/monetization-service/internal/payments"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Paid memberships on payments-service (phase 1 contract, 23 Oct 2026),
// through the real Service and the in-memory store, which applies events
// with the same membership.Decide as Postgres.

var t0 = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

type joinRig struct {
	*memRig
	creator, fan uuid.UUID
	tier         *postgres.CreatorTier
	evN          int
}

func newJoinRig(t *testing.T) *joinRig {
	t.Helper()
	r := &joinRig{memRig: newRig(t0), creator: uuid.New(), fan: uuid.New()}
	r.tier = r.fake.AddTier(r.creator, "Fan", 4_900)
	return r
}

func (r *joinRig) advance(d time.Duration) { *r.clock = r.clock.Add(d) }

func (r *joinRig) join(t *testing.T, key string) *Checkout {
	t.Helper()
	co, err := r.svc.Subscribe(context.Background(), r.fan, r.creator, r.tier.ID, key)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return co
}

func (r *joinRig) nextEventID() string {
	r.evN++
	return "evt-" + uuid.NewString()
}

// event is the signed payments event for a payment row, as payments-service
// would state it (amount, currency, payer, intent all correct).
func (r *joinRig) event(t *testing.T, typ string, paymentID uuid.UUID) membership.Event {
	t.Helper()
	p := r.fake.Payment(paymentID)
	ev := membership.Event{EventID: r.nextEventID(), EventType: typ, IntentID: p.IntentID, PaymentID: p.ID,
		PayerID: p.PayerID, AmountMinor: p.AmountPaise, Currency: "INR", Status: "succeeded"}
	switch typ {
	case events.EventPaymentFailed:
		ev.Status = "failed"
	case events.EventPaymentRefunded:
		ev.Status = "refunded"
		ev.AmountMinor = p.AmountPaise - p.RefundedPaise
		ev.PayerID, ev.Currency = uuid.Nil, ""
	}
	return ev
}

func (r *joinRig) apply(t *testing.T, ev membership.Event) membership.Applied {
	t.Helper()
	a, err := r.svc.ApplyMembershipPaymentEvent(context.Background(), ev)
	if err != nil {
		t.Fatalf("apply %s: %v", ev.EventType, err)
	}
	return a
}

func (r *joinRig) succeed(t *testing.T, paymentID uuid.UUID) membership.Applied {
	t.Helper()
	return r.apply(t, r.event(t, events.EventPaymentSucceeded, paymentID))
}

func (r *joinRig) allowed(t *testing.T) bool {
	t.Helper()
	ent, err := r.svc.CheckEntitlement(context.Background(), EntitlementCheckRequest{SubscriberID: r.fan, CreatorID: r.creator})
	if err != nil {
		t.Fatal(err)
	}
	return ent.Allowed
}

// ---------------------------------------------------------------------------
// Join
// ---------------------------------------------------------------------------

func TestJoinCreatesPendingPaymentAndOneIntent(t *testing.T) {
	r := newJoinRig(t)
	co := r.join(t, "key-1")
	if co.Status != membership.StatusPendingPayment || co.Kind != membership.KindJoin || co.AmountPaise != 4_900 || co.Currency != "INR" {
		t.Fatalf("checkout = %+v", co)
	}
	if co.ClientSession == nil || co.ClientSession.OrderID == "" || co.ClientSession.KeyID == "" || co.ClientSession.MerchantDisplayName == "" {
		t.Fatalf("checkout has no usable client_session: %+v", co.ClientSession)
	}
	sub := r.fake.Sub(co.SubscriptionID)
	if sub.Status != membership.StatusPendingPayment || sub.ActivatedAt != nil || sub.Counted {
		t.Fatalf("subscription = %+v", sub)
	}
	p := r.fake.Payment(co.PaymentID)
	if p.Kind != membership.KindJoin || p.Status != membership.PayCreated || p.AmountPaise != 4_900 ||
		p.FeePaise != 0 || p.CreatorSharePaise != 4_900 || p.IntentID == "" {
		t.Fatalf("payment row = %+v", p)
	}
	if r.allowed(t) || r.fake.TierCount(r.tier.ID) != 0 || len(r.fake.Earnings) != 0 {
		t.Fatal("a pending join granted access, counted, or earned")
	}
	// Same key again: the same checkout and the same intent.
	again := r.join(t, "key-1")
	if again.PaymentID != co.PaymentID || again.SubscriptionID != co.SubscriptionID || r.payments.Creates != 1 {
		t.Fatalf("replay = %+v, intents = %d", again, r.payments.Creates)
	}
	// No key, same tier, while the join is fresh: also the same checkout.
	if c := r.join(t, ""); c.PaymentID != co.PaymentID {
		t.Fatalf("fresh pending join was not replayed: %+v", c)
	}
	// The key belongs to the subscriber: another fan with it gets their own.
	other, err := r.svc.Subscribe(context.Background(), uuid.New(), r.creator, r.tier.ID, "key-1")
	if err != nil || other.SubscriptionID == co.SubscriptionID {
		t.Fatalf("another subscriber's key: %v %+v", err, other)
	}
	// The key for another creator is refused, not replayed.
	otherCreator := uuid.New()
	ot := r.fake.AddTier(otherCreator, "Fan", 4_900)
	if _, err := r.svc.Subscribe(context.Background(), r.fan, otherCreator, ot.ID, "key-1"); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("key reused for another creator: %v", err)
	}
}

func TestJoinRefusedWhileALiveSubscriptionExists(t *testing.T) {
	for _, status := range []string{"active", "cancelled_at_period_end", "paused", "past_due", "grace"} {
		t.Run(status, func(t *testing.T) {
			r := newJoinRig(t)
			r.fake.AddSubscription(postgres.Subscription{SubscriberID: r.fan, CreatorID: r.creator, TierID: r.tier.ID,
				Status: status, CurrentPeriodStart: t0.Add(-time.Hour), CurrentPeriodEnd: t0.Add(time.Hour)})
			if _, err := r.svc.Subscribe(context.Background(), r.fan, r.creator, r.tier.ID, ""); !errors.Is(err, postgres.ErrAlreadySubscribed) {
				t.Fatalf("existing %s: %v", status, err)
			}
			if r.payments.Creates != 0 || len(r.fake.Payments) != 0 {
				t.Fatal("a payment was opened")
			}
		})
	}
}

func TestStalePendingJoinIsReplaced(t *testing.T) {
	r := newJoinRig(t)
	first := r.join(t, "")
	r.advance(membership.PendingTTL + time.Minute)
	second := r.join(t, "")
	if second.SubscriptionID == first.SubscriptionID {
		t.Fatal("stale pending join was replayed instead of replaced")
	}
	if s := r.fake.Sub(first.SubscriptionID); s.Status != membership.StatusExpired {
		t.Fatalf("old pending = %s, want expired", s.Status)
	}
	if p := r.fake.Payment(first.PaymentID); p.Status != membership.PayFailed {
		t.Fatalf("old join payment = %s, want failed", p.Status)
	}
	// A fresh pending for ANOTHER tier is replaced too (a member picking again).
	gold := r.fake.AddTier(r.creator, "Gold", 19_900)
	third, err := r.svc.Subscribe(context.Background(), r.fan, r.creator, gold.ID, "")
	if err != nil || third.SubscriptionID == second.SubscriptionID || r.fake.Sub(second.SubscriptionID).Status != membership.StatusExpired {
		t.Fatalf("different tier: %v %+v", err, third)
	}
}

func TestJoinWithoutPaymentsClientIsUnavailable(t *testing.T) {
	r := newJoinRig(t)
	r.svc.membershipPayments = nil
	if _, err := r.svc.Subscribe(context.Background(), r.fan, r.creator, r.tier.ID, ""); !errors.Is(err, ErrMembershipPaymentsUnavailable) {
		t.Fatalf("got %v", err)
	}
	if len(r.fake.Payments) != 0 {
		t.Fatal("a row was created without a payments client")
	}
}

func TestJoinWithoutCheckoutSessionOutsideDevIsRefused(t *testing.T) {
	r := newJoinRig(t)
	r.payments.NoSession = true
	if _, err := r.svc.Subscribe(context.Background(), r.fan, r.creator, r.tier.ID, ""); !errors.Is(err, ErrPaymentSessionUnavailable) {
		t.Fatalf("got %v", err)
	}
	r2 := newJoinRig(t)
	r2.payments.NoSession = true
	r2.svc.membershipLocalEnv = true
	if co, err := r2.svc.Subscribe(context.Background(), r2.fan, r2.creator, r2.tier.ID, ""); err != nil || co.ClientSession != nil {
		t.Fatalf("dev stub gateway: %v %+v", err, co)
	}
}

// ---------------------------------------------------------------------------
// Access only from the signed event
// ---------------------------------------------------------------------------

func TestAccessOnlyAfterTheSignedSucceededEvent(t *testing.T) {
	r := newJoinRig(t)
	co := r.join(t, "k")
	ctx := context.Background()

	// The client's callback verifies: confirming, still no access.
	v, err := r.svc.VerifyMembershipPayment(ctx, r.fan, co.PaymentID, MembershipCallback{OrderID: "o", PaymentID: "p", Signature: "s"})
	if err != nil || v.Status != membership.PayConfirming || r.payments.Verifies != 1 {
		t.Fatalf("verify: %v %+v", err, v)
	}
	if got := r.fake.Payment(co.PaymentID).Status; got != membership.PayConfirming {
		t.Fatalf("row after verify = %s", got)
	}
	if r.allowed(t) || r.fake.Sub(co.SubscriptionID).Status != membership.StatusPendingPayment || len(r.fake.Earnings) != 0 {
		t.Fatal("a verified callback granted access")
	}
	if pv, _ := r.svc.GetMembershipPayment(ctx, r.fan, co.PaymentID); pv.Status != "confirming" {
		t.Fatalf("poll after verify = %+v", pv)
	}
	// A verdict for another payment does not even move it to confirming.
	r2 := newJoinRig(t)
	co2 := r2.join(t, "")
	r2.payments.Verdict = &payments.Verdict{Verified: true, AmountMinor: 4_900, PayerID: r2.fan, ReferenceType: membership.RefType, ReferenceID: uuid.New()}
	if v, _ := r2.svc.VerifyMembershipPayment(ctx, r2.fan, co2.PaymentID, MembershipCallback{}); v.Status != "confirming" || r2.fake.Payment(co2.PaymentID).Status != membership.PayCreated {
		t.Fatalf("foreign verdict moved the row: %+v", v)
	}

	a := r.succeed(t, co.PaymentID)
	if a.Decision.Outcome != membership.OutcomeActivated {
		t.Fatalf("outcome %s %s", a.Decision.Outcome, a.Decision.Detail)
	}
	sub := r.fake.Sub(co.SubscriptionID)
	if sub.Status != membership.StatusActive || !sub.CurrentPeriodStart.Equal(t0) || !sub.CurrentPeriodEnd.Equal(t0.AddDate(0, 1, 0)) ||
		sub.ActivatedAt == nil || !sub.Counted {
		t.Fatalf("subscription after capture = %+v", sub)
	}
	if !r.allowed(t) || r.fake.TierCount(r.tier.ID) != 1 {
		t.Fatal("capture did not grant access or count")
	}
	if r.fake.Balances[r.creator] != 4_900 || len(r.fake.Earnings) != 1 || r.fake.Earnings[0].PaymentID != co.PaymentID {
		t.Fatalf("creator earnings: %d %+v", r.fake.Balances[r.creator], r.fake.Earnings)
	}
	if pv, _ := r.svc.GetMembershipPayment(ctx, r.fan, co.PaymentID); pv.Status != "paid" || pv.RefundStatus != "" {
		t.Fatalf("poll after capture = %+v", pv)
	}
	if got := r.pub.MembershipTypes(); !reflect.DeepEqual(got, []string{membership.EventStarted}) {
		t.Fatalf("membership events = %v", got)
	}
	if got := r.pub.Actions(); !reflect.DeepEqual(got, []string{membership.EntitlementGranted}) {
		t.Fatalf("entitlement events = %v", got)
	}
	m := r.pub.Memberships[0]
	if m.Key != co.PaymentID.String() || m.Payload.SubscriptionID != co.SubscriptionID.String() || m.Payload.AmountPaise != 4_900 ||
		m.Payload.TierName != "Fan" || m.Payload.PeriodEnd != t0.AddDate(0, 1, 0).Format(time.RFC3339) || m.Payload.DaysLeft != nil {
		t.Fatalf("membership.started = %+v", m)
	}
	// Someone else polling the payment gets 404.
	if _, err := r.svc.GetMembershipPayment(ctx, uuid.New(), co.PaymentID); !errors.Is(err, ErrMembershipPaymentNotFound) {
		t.Fatalf("stranger poll: %v", err)
	}
}

func TestReplayedEventsApplyOnce(t *testing.T) {
	r := newJoinRig(t)
	co := r.join(t, "")
	ev := r.event(t, events.EventPaymentSucceeded, co.PaymentID)
	r.apply(t, ev)
	end := r.fake.Sub(co.SubscriptionID).CurrentPeriodEnd

	// The same delivery again (same event id).
	if a := r.apply(t, ev); a.Decision.Outcome != membership.OutcomeDuplicate {
		t.Fatalf("redelivery: %s", a.Decision.Outcome)
	}
	// Another event id for the same capture.
	r.advance(time.Hour)
	if a := r.succeed(t, co.PaymentID); a.Decision.Outcome != membership.OutcomeAlreadyApplied {
		t.Fatalf("second event: %s", a.Decision.Outcome)
	}
	if got := r.fake.Sub(co.SubscriptionID).CurrentPeriodEnd; !got.Equal(end) {
		t.Fatalf("period moved on a replay: %v → %v", end, got)
	}
	if r.fake.Balances[r.creator] != 4_900 || r.fake.TierCount(r.tier.ID) != 1 || len(r.pub.MembershipTypes()) != 1 || len(r.payments.Refunds) != 0 {
		t.Fatalf("replay re-applied: balance %d count %d events %v refunds %v",
			r.fake.Balances[r.creator], r.fake.TierCount(r.tier.ID), r.pub.MembershipTypes(), r.payments.Refunds)
	}
}

func TestCaptureThatDoesNotMatchIsRefused(t *testing.T) {
	cases := []struct {
		name string
		mut  func(ev *membership.Event)
	}{
		{"wrong amount", func(ev *membership.Event) { ev.AmountMinor = 100 }},
		{"wrong currency", func(ev *membership.Event) { ev.Currency = "USD" }},
		{"currency not stated", func(ev *membership.Event) { ev.Currency = "" }},
		{"another payer", func(ev *membership.Event) { ev.PayerID = uuid.New() }},
		{"payer not stated", func(ev *membership.Event) { ev.PayerID = uuid.Nil }},
		{"another intent", func(ev *membership.Event) { ev.IntentID = uuid.NewString() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newJoinRig(t)
			co := r.join(t, "")
			ev := r.event(t, events.EventPaymentSucceeded, co.PaymentID)
			tc.mut(&ev)
			if a := r.apply(t, ev); a.Decision.Outcome != membership.OutcomeMismatch {
				t.Fatalf("outcome %s", a.Decision.Outcome)
			}
			if r.allowed(t) || r.fake.Sub(co.SubscriptionID).Status != membership.StatusPendingPayment ||
				r.fake.Payment(co.PaymentID).Status != membership.PayCreated || len(r.fake.Earnings) != 0 || len(r.pub.MembershipTypes()) != 0 {
				t.Fatal("a mismatched capture applied something")
			}
		})
	}
}

func TestPaymentFailedLeavesTheJoinPendingAndCanBeRetried(t *testing.T) {
	r := newJoinRig(t)
	co := r.join(t, "k")
	a := r.apply(t, r.event(t, events.EventPaymentFailed, co.PaymentID))
	if a.Decision.Outcome != membership.OutcomeMarkedFailed {
		t.Fatalf("outcome %s", a.Decision.Outcome)
	}
	if r.fake.Payment(co.PaymentID).Status != membership.PayFailed || r.fake.Sub(co.SubscriptionID).Status != membership.StatusPendingPayment || r.allowed(t) {
		t.Fatal("failed payment state wrong")
	}
	if got := r.pub.MembershipTypes(); !reflect.DeepEqual(got, []string{membership.EventPaymentFailed}) {
		t.Fatalf("events %v", got)
	}
	if pv, _ := r.svc.GetMembershipPayment(context.Background(), r.fan, co.PaymentID); pv.Status != "failed" {
		t.Fatalf("poll = %+v", pv)
	}
	// Retry: the same row and intent, reopened.
	again := r.join(t, "k")
	if again.PaymentID != co.PaymentID || r.payments.Creates != 1 || r.fake.Payment(co.PaymentID).Status != membership.PayCreated {
		t.Fatalf("retry = %+v creates %d", again, r.payments.Creates)
	}
	// A capture of the same intent after the failure still activates.
	if a := r.succeed(t, co.PaymentID); a.Decision.Outcome != membership.OutcomeActivated || !r.allowed(t) {
		t.Fatalf("capture after failure: %s", a.Decision.Outcome)
	}
}

// ---------------------------------------------------------------------------
// Late capture
// ---------------------------------------------------------------------------

func TestLateCaptureIsRefundedOnce(t *testing.T) {
	r := newJoinRig(t)
	co := r.join(t, "")
	// The join goes stale and is swept (as the worker does) before capture.
	r.advance(membership.PendingTTL + time.Minute)
	if _, err := r.fake.SweepStalePendingJoins(context.Background(), r.clock.Add(-membership.PendingTTL)); err != nil {
		t.Fatal(err)
	}
	a := r.succeed(t, co.PaymentID)
	if a.Decision.Outcome != membership.OutcomeLateCapture {
		t.Fatalf("outcome %s", a.Decision.Outcome)
	}
	if r.allowed(t) || r.fake.TierCount(r.tier.ID) != 0 || len(r.fake.Earnings) != 0 {
		t.Fatal("a late capture granted, counted or earned")
	}
	wantKey := "membership-late:" + co.PaymentID.String()
	if got := r.payments.RefundKeys(); !reflect.DeepEqual(got, []string{wantKey}) || r.payments.Refunds[0].Amount != 4_900 {
		t.Fatalf("refunds = %+v", r.payments.Refunds)
	}
	p := r.fake.Payment(co.PaymentID)
	if !p.LateCapture || p.RefundStatus != membership.RefundPending {
		t.Fatalf("payment = %+v", p)
	}
	if pv, _ := r.svc.GetMembershipPayment(context.Background(), r.fan, co.PaymentID); pv.Status != "failed" || pv.RefundStatus != "pending" || !pv.LateCapture {
		t.Fatalf("poll = %+v", pv)
	}
	// A second event for the same capture refunds nothing more, and the
	// refund worker finds nothing queued.
	r.succeed(t, co.PaymentID)
	if n, err := r.svc.SubmitDueMembershipRefunds(context.Background()); err != nil || n != 0 || len(r.payments.Refunds) != 1 {
		t.Fatalf("second refund: n=%d %v %+v", n, err, r.payments.Refunds)
	}
	// payment.refunded closes it.
	r.apply(t, r.event(t, events.EventPaymentRefunded, co.PaymentID))
	if p := r.fake.Payment(co.PaymentID); p.Status != membership.PayRefunded || p.RefundStatus != membership.RefundRefunded {
		t.Fatalf("after refund settled: %+v", p)
	}
	if pv, _ := r.svc.GetMembershipPayment(context.Background(), r.fan, co.PaymentID); pv.RefundStatus != "refunded" {
		t.Fatalf("poll = %+v", pv)
	}
}

func TestLateCaptureRefundRetriedWhenPaymentsIsDown(t *testing.T) {
	r := newJoinRig(t)
	co := r.join(t, "")
	if err := r.svc.CancelImmediately(context.Background(), r.fan, co.SubscriptionID, ""); err != nil {
		t.Fatal(err)
	}
	r.payments.RefundErr = payments.ErrUnavailable
	r.succeed(t, co.PaymentID)
	if len(r.payments.Refunds) != 0 || r.fake.Payment(co.PaymentID).RefundStatus != membership.RefundRequested {
		t.Fatalf("refund should be queued: %+v", r.fake.Payment(co.PaymentID))
	}
	if n, _ := r.svc.SubmitDueMembershipRefunds(context.Background()); n != 1 || len(r.payments.Refunds) != 1 {
		t.Fatalf("worker retry: n=%d refunds=%v", n, r.payments.Refunds)
	}
	if r.fake.Payment(co.PaymentID).RefundStatus != membership.RefundPending {
		t.Fatal("refund not marked pending after the retry")
	}
}

// ---------------------------------------------------------------------------
// Renewal
// ---------------------------------------------------------------------------

func activeMember(t *testing.T, r *joinRig) *Checkout {
	t.Helper()
	co := r.join(t, "")
	r.succeed(t, co.PaymentID)
	return co
}

func TestRenewalExtendsFromThePeriodEndWhenEarly(t *testing.T) {
	r := newJoinRig(t)
	co := activeMember(t, r)
	end := r.fake.Sub(co.SubscriptionID).CurrentPeriodEnd
	r.advance(28 * 24 * time.Hour) // three days left
	rc, err := r.svc.Renew(context.Background(), r.fan, co.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if rc.Kind != membership.KindRenewal || rc.AmountPaise != 4_900 || rc.SubscriptionID != co.SubscriptionID || rc.ClientSession == nil {
		t.Fatalf("renew checkout = %+v", rc)
	}
	// Asking again for the same period: the same payment.
	if again, _ := r.svc.Renew(context.Background(), r.fan, co.SubscriptionID); again.PaymentID != rc.PaymentID {
		t.Fatal("second renew opened another payment for the same period")
	}
	if got := r.fake.Sub(co.SubscriptionID).CurrentPeriodEnd; !got.Equal(end) {
		t.Fatal("renew moved the period before payment")
	}
	a := r.succeed(t, rc.PaymentID)
	sub := r.fake.Sub(co.SubscriptionID)
	if a.Decision.Outcome != membership.OutcomeRenewed || !sub.CurrentPeriodEnd.Equal(end.AddDate(0, 1, 0)) || !sub.CurrentPeriodStart.Equal(end) {
		t.Fatalf("after renewal: %s %+v", a.Decision.Outcome, sub)
	}
	if r.fake.Balances[r.creator] != 9_800 || r.fake.TierCount(r.tier.ID) != 1 {
		t.Fatalf("earnings %d count %d", r.fake.Balances[r.creator], r.fake.TierCount(r.tier.ID))
	}
	if got := r.pub.MembershipTypes(); !reflect.DeepEqual(got, []string{membership.EventStarted, membership.EventRenewed}) {
		t.Fatalf("events %v", got)
	}
	// The paid period cannot be bought twice: a renew now is for the NEW end.
	next, _ := r.svc.Renew(context.Background(), r.fan, co.SubscriptionID)
	if next.PaymentID == rc.PaymentID || !r.fake.Payment(next.PaymentID).PeriodStart.Equal(sub.CurrentPeriodEnd) {
		t.Fatal("next renewal is not keyed on the new period end")
	}
}

func TestRenewalAfterExpiryStartsNow(t *testing.T) {
	r := newJoinRig(t)
	co := activeMember(t, r)
	end := r.fake.Sub(co.SubscriptionID).CurrentPeriodEnd
	*r.clock = end.Add(5 * 24 * time.Hour)
	ctx := context.Background()
	if _, err := r.fake.StartGraceForEndedPeriods(ctx, *r.clock); err != nil {
		t.Fatal(err)
	}
	if _, err := r.fake.ExpireEndedMemberships(ctx, *r.clock); err != nil {
		t.Fatal(err)
	}
	if s := r.fake.Sub(co.SubscriptionID); s.Status != membership.StatusExpired || s.Counted || r.fake.TierCount(r.tier.ID) != 0 {
		t.Fatalf("expected expired and uncounted: %+v", s)
	}
	rc, err := r.svc.Renew(ctx, r.fan, co.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	r.succeed(t, rc.PaymentID)
	s := r.fake.Sub(co.SubscriptionID)
	if s.Status != membership.StatusActive || !s.CurrentPeriodStart.Equal(*r.clock) || !s.CurrentPeriodEnd.Equal(r.clock.AddDate(0, 1, 0)) {
		t.Fatalf("renewed from expiry: %+v", s)
	}
	if !s.Counted || r.fake.TierCount(r.tier.ID) != 1 || !r.allowed(t) {
		t.Fatal("renewal from expiry did not count or grant")
	}
}

func TestRenewalWindowAndStates(t *testing.T) {
	ctx := context.Background()
	r := newJoinRig(t)
	co := activeMember(t, r)
	end := r.fake.Sub(co.SubscriptionID).CurrentPeriodEnd
	// Expired more than 30 days ago: not renewable.
	*r.clock = end.Add(31 * 24 * time.Hour)
	if _, err := r.fake.StartGraceForEndedPeriods(ctx, *r.clock); err != nil {
		t.Fatal(err)
	}
	if _, err := r.fake.ExpireEndedMemberships(ctx, *r.clock); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Renew(ctx, r.fan, co.SubscriptionID); !errors.Is(err, postgres.ErrNotRenewable) {
		t.Fatalf("31 days after expiry: %v", err)
	}
	// A join never paid can never be renewed.
	r2 := newJoinRig(t)
	pending := r2.join(t, "")
	if _, err := r2.svc.Renew(ctx, r2.fan, pending.SubscriptionID); !errors.Is(err, postgres.ErrNotRenewable) {
		t.Fatalf("pending join: %v", err)
	}
	// Somebody else's subscription.
	co3 := activeMember(t, r2)
	_ = co3
	if _, err := r2.svc.Renew(ctx, uuid.New(), pending.SubscriptionID); !errors.Is(err, postgres.ErrSubscriptionNotOwned) {
		t.Fatalf("stranger: %v", err)
	}
	// cancelled_at_period_end: paying re-enables renewal.
	r4 := newJoinRig(t)
	c4 := activeMember(t, r4)
	if err := r4.svc.CancelAtPeriodEnd(ctx, r4.fan, c4.SubscriptionID, ""); err != nil {
		t.Fatal(err)
	}
	rc, err := r4.svc.Renew(ctx, r4.fan, c4.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	r4.succeed(t, rc.PaymentID)
	if s := r4.fake.Sub(c4.SubscriptionID); s.Status != membership.StatusActive {
		t.Fatalf("renewing a cancelled-at-period-end membership: %s", s.Status)
	}
}

func TestSecondCaptureForTheSamePeriodIsRefunded(t *testing.T) {
	r := newJoinRig(t)
	co := activeMember(t, r)
	rc, _ := r.svc.Renew(context.Background(), r.fan, co.SubscriptionID)
	r.succeed(t, rc.PaymentID)
	// A stale renewal row for the OLD period end (another device's checkout)
	// captured now: not stacked, refunded.
	stale := r.fake.Payment(rc.PaymentID)
	staleRow := stale
	staleRow.ID, staleRow.Status, staleRow.GrantedStart, staleRow.GrantedEnd = uuid.New(), membership.PayCreated, nil, nil
	staleRow.IntentID = uuid.NewString()
	r.fake.Payments[staleRow.ID] = &staleRow
	end := r.fake.Sub(co.SubscriptionID).CurrentPeriodEnd
	a := r.succeed(t, staleRow.ID)
	if a.Decision.Outcome != membership.OutcomeLateCapture || !r.fake.Sub(co.SubscriptionID).CurrentPeriodEnd.Equal(end) {
		t.Fatalf("duplicate period: %s", a.Decision.Outcome)
	}
	if got := r.payments.RefundKeys(); len(got) != 1 || got[0] != "membership-late:"+staleRow.ID.String() {
		t.Fatalf("refunds %v", got)
	}
}

// ---------------------------------------------------------------------------
// Upgrade
// ---------------------------------------------------------------------------

func TestUpgradeChargesTheDifferenceThroughAnIntentAndAppliesOnTheEvent(t *testing.T) {
	r := newTierChangeRig(func(r *tierChangeRig) *postgres.CreatorTier { return r.bronze }, 4_900)
	r.fake.SetCounted(r.sub.ID)
	change, err := r.svc.UpgradeSubscription(context.Background(), r.fan, r.sub.ID, r.silver.ID)
	if err != nil {
		t.Fatal(err)
	}
	if change.Result != TierChangePaymentRequired || change.Checkout == nil || change.Checkout.AmountPaise != 2_500 || change.Checkout.Kind != membership.KindUpgrade {
		t.Fatalf("change = %+v %+v", change, change.Checkout)
	}
	// Nothing changes before the event.
	if got := r.fake.Sub(r.sub.ID); got.TierID != r.bronze.ID || got.PricePaise != 4_900 {
		t.Fatalf("tier changed before payment: %+v", got)
	}
	// Asking again: the same payment.
	again, _ := r.svc.UpgradeSubscription(context.Background(), r.fan, r.sub.ID, r.silver.ID)
	if again.Checkout.PaymentID != change.Checkout.PaymentID || r.payments.Creates != 1 {
		t.Fatal("second upgrade request opened another payment")
	}
	jr := &joinRig{memRig: &memRig{svc: r.svc, fake: r.fake, pub: r.pub, payments: r.payments}}
	a := jr.succeed(t, change.Checkout.PaymentID)
	got := r.fake.Sub(r.sub.ID)
	if a.Decision.Outcome != membership.OutcomeUpgraded || got.TierID != r.silver.ID || got.PricePaise != 9_900 || !got.CurrentPeriodEnd.Equal(r.sub.CurrentPeriodEnd) {
		t.Fatalf("after upgrade event: %s %+v", a.Decision.Outcome, got)
	}
	if r.fake.TierCount(r.bronze.ID) != 0 || r.fake.TierCount(r.silver.ID) != 1 || r.fake.Balances[r.creator] != 2_500 {
		t.Fatalf("counts %d/%d earnings %d", r.fake.TierCount(r.bronze.ID), r.fake.TierCount(r.silver.ID), r.fake.Balances[r.creator])
	}
	if acts := r.pub.Actions(); len(acts) != 1 || acts[0] != membership.EntitlementUpgraded {
		t.Fatalf("entitlement events %v", acts)
	}
}

func TestUpgradeCapturedAfterTheTierChangedIsRefunded(t *testing.T) {
	r := newTierChangeRig(func(r *tierChangeRig) *postgres.CreatorTier { return r.bronze }, 4_900)
	change, _ := r.svc.UpgradeSubscription(context.Background(), r.fan, r.sub.ID, r.silver.ID)
	// The member cancels before paying.
	if err := r.svc.CancelImmediately(context.Background(), r.fan, r.sub.ID, ""); err != nil {
		t.Fatal(err)
	}
	jr := &joinRig{memRig: &memRig{svc: r.svc, fake: r.fake, pub: r.pub, payments: r.payments}}
	if a := jr.succeed(t, change.Checkout.PaymentID); a.Decision.Outcome != membership.OutcomeLateCapture {
		t.Fatalf("outcome %s", a.Decision.Outcome)
	}
	if got := r.payments.RefundKeys(); len(got) != 1 {
		t.Fatalf("refunds %v", got)
	}
}

func TestUpgradeWithNoDifferenceAppliesAtOnce(t *testing.T) {
	// Paid 9,900 on Bronze (its price since cut to 4,900): Silver at 9,900
	// is up by level, but costs nothing more for this period.
	r := newTierChangeRig(func(r *tierChangeRig) *postgres.CreatorTier { return r.bronze }, 9_900)
	change, err := r.svc.UpgradeSubscription(context.Background(), r.fan, r.sub.ID, r.silver.ID)
	if err != nil || change.Result != TierChangeUpgraded || change.Checkout != nil {
		t.Fatalf("%v %+v", err, change)
	}
	if got := r.fake.Sub(r.sub.ID); got.TierID != r.silver.ID || r.payments.Creates != 0 {
		t.Fatalf("zero-difference upgrade: %+v intents=%d", got, r.payments.Creates)
	}
}

func TestRenewalAppliesAScheduledDowngrade(t *testing.T) {
	r := newTierChangeRig(func(r *tierChangeRig) *postgres.CreatorTier { return r.gold }, 19_900)
	r.fake.SetCounted(r.sub.ID)
	if c, err := r.svc.UpgradeSubscription(context.Background(), r.fan, r.sub.ID, r.bronze.ID); err != nil || c.Result != TierChangeDowngradeScheduled {
		t.Fatalf("%v %+v", err, c)
	}
	rc, err := r.svc.Renew(context.Background(), r.fan, r.sub.ID)
	if err != nil || rc.AmountPaise != 4_900 {
		t.Fatalf("renewal of a scheduled downgrade: %v %+v", err, rc)
	}
	jr := &joinRig{memRig: &memRig{svc: r.svc, fake: r.fake, pub: r.pub, payments: r.payments}}
	jr.succeed(t, rc.PaymentID)
	got := r.fake.Sub(r.sub.ID)
	if got.TierID != r.bronze.ID || got.PricePaise != 4_900 || got.PendingTierID != nil {
		t.Fatalf("after renewal: %+v", got)
	}
	if r.fake.TierCount(r.gold.ID) != 0 || r.fake.TierCount(r.bronze.ID) != 1 {
		t.Fatal("count did not move with the downgrade")
	}
}

// ---------------------------------------------------------------------------
// subscriber_count
// ---------------------------------------------------------------------------

func TestSubscriberCountMovesOnActivationCancelAndExpiry(t *testing.T) {
	ctx := context.Background()
	r := newJoinRig(t)
	co := activeMember(t, r)
	if r.fake.TierCount(r.tier.ID) != 1 {
		t.Fatal("activation did not count")
	}
	if err := r.svc.CancelImmediately(ctx, r.fan, co.SubscriptionID, ""); err != nil {
		t.Fatal(err)
	}
	if r.fake.TierCount(r.tier.ID) != 0 {
		t.Fatal("immediate cancel did not uncount")
	}
	// Cancelling a pending join never touches the count.
	fan2 := uuid.New()
	pend, _ := r.svc.Subscribe(ctx, fan2, r.creator, r.tier.ID, "")
	if err := r.svc.CancelImmediately(ctx, fan2, pend.SubscriptionID, ""); err != nil || r.fake.TierCount(r.tier.ID) != 0 {
		t.Fatalf("pending cancel: %v count %d", err, r.fake.TierCount(r.tier.ID))
	}
	// Unsubscribe (DELETE /subscribe) uncounts too.
	fan3 := uuid.New()
	c3, _ := r.svc.Subscribe(ctx, fan3, r.creator, r.tier.ID, "")
	r.succeed(t, c3.PaymentID)
	if r.fake.TierCount(r.tier.ID) != 1 {
		t.Fatal("second member not counted")
	}
	if err := r.svc.Unsubscribe(ctx, fan3, r.creator); err != nil || r.fake.TierCount(r.tier.ID) != 0 {
		t.Fatalf("unsubscribe: %v count %d", err, r.fake.TierCount(r.tier.ID))
	}
	if got := r.pub.MembershipTypes(); got[len(got)-1] != membership.EventEnded {
		t.Fatalf("unsubscribe did not publish membership.ended: %v", got)
	}
}

// ---------------------------------------------------------------------------
// Support refund
// ---------------------------------------------------------------------------

func TestSupportRefundRemovesThePeriodItBought(t *testing.T) {
	ctx := context.Background()
	r := newJoinRig(t)
	co := activeMember(t, r)
	admin := uuid.New()
	v, err := r.svc.RefundMembershipPayment(ctx, admin, co.PaymentID, "double charge")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.payments.RefundKeys(); !reflect.DeepEqual(got, []string{"membership-refund:" + co.PaymentID.String()}) || r.payments.Refunds[0].Amount != 4_900 {
		t.Fatalf("refunds %+v", r.payments.Refunds)
	}
	if v.RefundStatus != "pending" || !r.allowed(t) {
		t.Fatal("access must stand until payments says the refund settled")
	}
	// Asking again files nothing new.
	if _, err := r.svc.RefundMembershipPayment(ctx, admin, co.PaymentID, "again"); err != nil || len(r.payments.Refunds) != 1 {
		t.Fatalf("second refund request: %v %v", err, r.payments.Refunds)
	}
	r.advance(time.Hour)
	a := r.apply(t, r.event(t, events.EventPaymentRefunded, co.PaymentID))
	if a.Decision.Outcome != membership.OutcomeRefunded {
		t.Fatalf("outcome %s", a.Decision.Outcome)
	}
	sub := r.fake.Sub(co.SubscriptionID)
	if sub.Status != membership.StatusExpired || r.allowed(t) || r.fake.TierCount(r.tier.ID) != 0 {
		t.Fatalf("join refund must end access: %+v", sub)
	}
	if r.fake.Balances[r.creator] != 0 || !r.fake.Earnings[0].Reversed {
		t.Fatalf("earnings not reversed: %d %+v", r.fake.Balances[r.creator], r.fake.Earnings)
	}
	if got := r.pub.MembershipTypes(); got[len(got)-1] != membership.EventEnded {
		t.Fatalf("events %v", got)
	}
	if acts := r.pub.Actions(); acts[len(acts)-1] != membership.EntitlementRevoked {
		t.Fatalf("entitlement %v", acts)
	}
	// Refunding an unpaid or late payment is refused.
	r2 := newJoinRig(t)
	p2 := r2.join(t, "")
	if _, err := r2.svc.RefundMembershipPayment(ctx, admin, p2.PaymentID, "x"); !errors.Is(err, postgres.ErrRefundNotAllowed) {
		t.Fatalf("unpaid: %v", err)
	}
}

func TestRefundOfAnEarlyRenewalRemovesOnlyThatPeriod(t *testing.T) {
	r := newJoinRig(t)
	co := activeMember(t, r)
	end := r.fake.Sub(co.SubscriptionID).CurrentPeriodEnd
	r.advance(10 * 24 * time.Hour)
	rc, _ := r.svc.Renew(context.Background(), r.fan, co.SubscriptionID)
	r.succeed(t, rc.PaymentID)
	if _, err := r.svc.RefundMembershipPayment(context.Background(), uuid.New(), rc.PaymentID, "support"); err != nil {
		t.Fatal(err)
	}
	r.apply(t, r.event(t, events.EventPaymentRefunded, rc.PaymentID))
	s := r.fake.Sub(co.SubscriptionID)
	if s.Status != membership.StatusActive || !s.CurrentPeriodEnd.Equal(end) || !r.allowed(t) {
		t.Fatalf("the first period must stand: %+v (want end %v)", s, end)
	}
	if r.fake.Balances[r.creator] != 4_900 {
		t.Fatalf("earnings after renewal refund = %d", r.fake.Balances[r.creator])
	}
	// The removed period can be bought again.
	again, err := r.svc.Renew(context.Background(), r.fan, co.SubscriptionID)
	if err != nil || again.PaymentID == rc.PaymentID {
		t.Fatalf("re-buy after refund: %v %+v", err, again)
	}
}

// ---------------------------------------------------------------------------
// Eligibility
// ---------------------------------------------------------------------------

func TestEligibilityFailsClosedPerItem(t *testing.T) {
	ctx := context.Background()
	young := t0.AddDate(-17, 0, 0)
	cases := []struct {
		name string
		set  func(r *memRig)
		want []string
	}{
		{"all present", func(r *memRig) {}, []string{}},
		{"under 18", func(r *memRig) { r.elig.DoB = &young }, []string{"age"}},
		{"no date of birth", func(r *memRig) { r.elig.DoB = nil }, []string{"age"}},
		{"no such account", func(r *memRig) { r.elig.Found = false }, []string{"age"}},
		{"identity down", func(r *memRig) { r.elig.ErrAge = errors.New("down") }, []string{"age"}},
		{"identity not configured", func(r *memRig) { r.svc.eligibility.BirthDates = nil }, []string{"age"}},
		{"no PAN", func(r *memRig) { r.fake.AllPANVerified = false }, []string{"pan"}},
		{"PAN not verified", func(r *memRig) {
			r.fake.AllPANVerified = false
			pan := "enc"
			r.fake.Tax[creatorUnderTest] = &postgres.CreatorTaxProfile{UserID: creatorUnderTest, PANEncrypted: &pan}
		}, []string{"pan"}},
		{"499 followers", func(r *memRig) { r.elig.Followers = 499 }, []string{"followers"}},
		{"graph down", func(r *memRig) { r.elig.ErrFollower = errors.New("down") }, []string{"followers"}},
		{"graph not configured", func(r *memRig) { r.svc.eligibility.Followers = nil }, []string{"followers"}},
		{"an active strike", func(r *memRig) { r.elig.Strikes = 1 }, []string{"strikes"}},
		{"restricted", func(r *memRig) { r.elig.StandingIs = "restricted" }, []string{"strikes"}},
		{"trust-safety down", func(r *memRig) { r.elig.ErrStanding = errors.New("403") }, []string{"strikes"}},
		{"trust-safety not configured", func(r *memRig) { r.svc.eligibility.Standing = nil }, []string{"strikes"}},
		{"nothing configured", func(r *memRig) {
			r.svc.eligibility = EligibilitySources{}
			r.fake.AllPANVerified = false
		}, []string{"age", "pan", "followers", "strikes"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t0)
			tc.set(r)
			el, err := r.svc.MembershipEligibility(ctx, creatorUnderTest)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(el.Missing, tc.want) || el.Eligible != (len(tc.want) == 0) {
				t.Fatalf("eligibility = %+v, want missing %v", el, tc.want)
			}
		})
	}
}

var creatorUnderTest = uuid.MustParse("6a3f0f2e-4f55-4a9c-9a55-0d5c2c1e7a11")

func TestAgeIsExactlyEighteenOnTheBirthday(t *testing.T) {
	dob := time.Date(2008, 10, 23, 0, 0, 0, 0, time.UTC)
	if membership.AgeYears(dob, t0) != 18 || membership.AgeYears(dob, t0.Add(-24*time.Hour)) != 17 {
		t.Fatal("age boundary wrong")
	}
}

func TestFirstTierNeedsEligibilityLaterTiersDoNot(t *testing.T) {
	ctx := context.Background()
	r := newRig(t0)
	r.elig.Followers = 10
	_, err := r.svc.CreateTier(ctx, creatorUnderTest, TierInput{Name: "Fan", PricePaise: 4_900})
	var ne *NotEligibleError
	if !errors.As(err, &ne) || !errors.Is(err, ErrNotEligible) || !reflect.DeepEqual(ne.Missing, []string{"followers"}) {
		t.Fatalf("first tier: %v", err)
	}
	if len(r.fake.Tiers) != 0 {
		t.Fatal("tier created while not eligible")
	}
	// A creator who already offers a tier may add another.
	r.fake.AddTier(creatorUnderTest, "Existing", 4_900)
	if _, err := r.svc.CreateTier(ctx, creatorUnderTest, TierInput{Name: "Second", PricePaise: 9_900}); err != nil {
		t.Fatalf("second tier: %v", err)
	}
}

// ---------------------------------------------------------------------------
// My memberships
// ---------------------------------------------------------------------------

func TestListMyMemberships(t *testing.T) {
	r := newJoinRig(t)
	co := activeMember(t, r)
	list, err := r.svc.ListMyMemberships(context.Background(), r.fan)
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %+v", err, list)
	}
	m := list[0]
	if m.SubscriptionID != co.SubscriptionID || m.Creator.ID != r.creator || m.Tier.ID != r.tier.ID || m.Tier.PricePaise != 4_900 ||
		m.Status != membership.StatusActive || m.AccessUntil == nil || !m.RenewAvailable {
		t.Fatalf("view = %+v", m)
	}
}

// payments refusing a refund (4xx) is not retried forever: the refund is
// marked failed for a person.
func TestRefusedRefundIsMarkedFailedNotRetried(t *testing.T) {
	r := newJoinRig(t)
	co := r.join(t, "")
	if err := r.svc.CancelImmediately(context.Background(), r.fan, co.SubscriptionID, ""); err != nil {
		t.Fatal(err)
	}
	r.payments.RefundErr = payments.ErrRefused
	r.succeed(t, co.PaymentID)
	if p := r.fake.Payment(co.PaymentID); p.RefundStatus != membership.RefundFailed || p.RefundError == "" {
		t.Fatalf("refused refund: %+v", p)
	}
	if n, _ := r.svc.SubmitDueMembershipRefunds(context.Background()); n != 0 {
		t.Fatal("a refused refund was queued again")
	}
}

func TestRenewWithoutPaymentsClientOpensNothing(t *testing.T) {
	r := newJoinRig(t)
	co := activeMember(t, r)
	r.svc.membershipPayments = nil
	if _, err := r.svc.Renew(context.Background(), r.fan, co.SubscriptionID); !errors.Is(err, ErrMembershipPaymentsUnavailable) {
		t.Fatalf("got %v", err)
	}
	if n := len(r.fake.PaymentsFor(co.SubscriptionID)); n != 1 {
		t.Fatalf("%d payment rows, want only the join", n)
	}
}

// A key whose join expired unpaid does not reopen it: CHECKOUT_EXPIRED,
// start again with a new key.
func TestExpiredJoinKeyIsNotReopened(t *testing.T) {
	r := newJoinRig(t)
	co := r.join(t, "k")
	r.advance(membership.PendingTTL + time.Minute)
	if _, err := r.fake.SweepStalePendingJoins(context.Background(), r.clock.Add(-membership.PendingTTL)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Subscribe(context.Background(), r.fan, r.creator, r.tier.ID, "k"); !errors.Is(err, ErrCheckoutExpired) {
		t.Fatalf("got %v", err)
	}
	if p := r.fake.Payment(co.PaymentID); p.Status != membership.PayFailed {
		t.Fatalf("expired join reopened: %+v", p)
	}
	if c := r.join(t, "k2"); c.SubscriptionID == co.SubscriptionID {
		t.Fatal("a new key reused the expired join")
	}
}

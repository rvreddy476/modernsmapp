package store

// Payments lane store tests (TEST_PG_DSN on rider_it_test): the signed
// payment events applied once through rider_payment_inbox, refunds, the
// switch to cash, the admin lists, fare windows and the stats counts.

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/payments"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// onlinePayment seeds a partner, a ride and an unpaid upi payment row with
// an intent bound (confirming), and returns the payment and the customer.
func onlinePayment(t *testing.T, s *Store, amount int64) (*RidePayment, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	pid, rid := makePartnerWithRide(t, s)
	ride, err := s.GetRide(ctx, rid)
	if err != nil {
		t.Fatal(err)
	}
	pay, err := s.CreateRidePayment(ctx, CreateRidePaymentInput{RideID: rid, PartnerID: pid, AmountPaise: amount, PaymentMethod: "upi"})
	if err != nil {
		t.Fatalf("create payment: %v", err)
	}
	intent := uuid.New()
	bound, err := s.BindRidePaymentIntent(ctx, pay.ID, intent, "order_1", "upi")
	if err != nil {
		t.Fatalf("bind intent: %v", err)
	}
	if bound.Status != "confirming" || bound.IntentID == nil || *bound.IntentID != intent {
		t.Fatalf("bound = %+v", bound)
	}
	return bound, ride.CustomerUserID, intent
}

func captureEvent(pay *RidePayment, customer, intent uuid.UUID, amount int64) payments.Event {
	return payments.Event{
		EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentSucceeded, IntentID: intent.String(),
		ReferenceID: pay.RideID, PayerID: customer, AmountMinor: amount, Currency: "INR", Status: "succeeded", ProviderRef: "pay_1",
	}
}

func TestApplyRidePaymentEvent_CapturePaysOnceAndDuplicateIsNoop(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	pay, cust, intent := onlinePayment(t, s, 12451)
	ev := captureEvent(pay, cust, intent, 12451)

	applied, err := s.ApplyRidePaymentEvent(ctx, ev)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.Decision.Outcome != payments.OutcomePaid || applied.Target != payments.TargetRide || applied.RideID != pay.RideID || applied.Status != "succeeded" {
		t.Fatalf("applied = %+v", applied)
	}
	got, _ := s.GetRidePayment(ctx, pay.ID)
	if got.Status != "succeeded" || got.SettledAt == nil || got.ProviderReference == nil || *got.ProviderReference != "pay_1" {
		t.Fatalf("row = %+v", got)
	}
	if oc, _ := s.PaymentInboxOutcome(ctx, ev.EventID); oc != string(payments.OutcomePaid) {
		t.Fatalf("inbox outcome = %q", oc)
	}
	// Mutation guard: the same event id again is a duplicate and applies
	// nothing; a fresh capture for a paid row is already_paid.
	again, err := s.ApplyRidePaymentEvent(ctx, ev)
	if err != nil || again.Decision.Outcome != payments.OutcomeDuplicate {
		t.Fatalf("duplicate: %+v %v", again, err)
	}
	fresh := captureEvent(pay, cust, intent, 12451)
	again, err = s.ApplyRidePaymentEvent(ctx, fresh)
	if err != nil || again.Decision.Outcome != payments.OutcomeAlreadyPaid {
		t.Fatalf("second capture: %+v %v", again, err)
	}
	if _, err := s.ApplyRidePaymentEvent(ctx, payments.Event{EventType: events.EventPaymentSucceeded}); err == nil {
		t.Fatal("an event without an id must be refused")
	}
}

// Mutation guard: a capture with a different amount (or payer, or intent)
// never marks the row paid; it is recorded once and queued for
// reconciliation.
func TestApplyRidePaymentEvent_MismatchNeverPays(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	pay, cust, intent := onlinePayment(t, s, 12451)
	wrongAmount := captureEvent(pay, cust, intent, 12450)
	wrongPayer := captureEvent(pay, uuid.New(), intent, 12451)
	wrongIntent := captureEvent(pay, cust, uuid.New(), 12451)
	for name, ev := range map[string]payments.Event{"amount": wrongAmount, "payer": wrongPayer, "intent": wrongIntent} {
		applied, err := s.ApplyRidePaymentEvent(ctx, ev)
		if err != nil || applied.Decision.Outcome != payments.OutcomeMismatch {
			t.Fatalf("%s: %+v %v", name, applied, err)
		}
		if oc, _ := s.PaymentInboxOutcome(ctx, ev.EventID); oc != string(payments.OutcomeMismatch) {
			t.Fatalf("%s: inbox outcome = %q", name, oc)
		}
	}
	got, _ := s.GetRidePayment(ctx, pay.ID)
	if got.Status != "confirming" || got.SettledAt != nil {
		t.Fatalf("row after mismatches = %+v", got)
	}
	if n, _ := s.CountReconciliationRequired(ctx, pay.RideID); n != 3 {
		t.Fatalf("reconciliation rows = %d, want 3", n)
	}
}

func TestApplyRidePaymentEvent_FailedThenRetriedCapture(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	pay, cust, intent := onlinePayment(t, s, 9000)
	failed := payments.Event{EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentFailed, IntentID: intent.String(),
		ReferenceID: pay.RideID, PayerID: cust, AmountMinor: 9000, Currency: "INR", Status: "failed", Reason: "upi declined"}
	applied, err := s.ApplyRidePaymentEvent(ctx, failed)
	if err != nil || applied.Decision.Outcome != payments.OutcomeMarkedFailed {
		t.Fatalf("failed: %+v %v", applied, err)
	}
	got, _ := s.GetRidePayment(ctx, pay.ID)
	if got.Status != "failed" || got.FailureReason == nil || *got.FailureReason != "upi declined" {
		t.Fatalf("row = %+v", got)
	}
	// The customer retried the same intent and it captured.
	applied, err = s.ApplyRidePaymentEvent(ctx, captureEvent(pay, cust, intent, 9000))
	if err != nil || applied.Decision.Outcome != payments.OutcomePaid {
		t.Fatalf("retry: %+v %v", applied, err)
	}
	// A failure after capture changes nothing.
	late := failed
	late.EventID = "evt-" + uuid.NewString()
	applied, _ = s.ApplyRidePaymentEvent(ctx, late)
	got, _ = s.GetRidePayment(ctx, pay.ID)
	if applied.Decision.Outcome != payments.OutcomeFailedIgnored || got.Status != "succeeded" {
		t.Fatalf("late failure: %+v row %s", applied, got.Status)
	}
}

func refundEvent(pay *RidePayment, intent uuid.UUID, amount int64, status string) payments.Event {
	return payments.Event{EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentRefunded, IntentID: intent.String(),
		ReferenceID: pay.RideID, AmountMinor: amount, Status: status, ProviderRef: "rfnd_" + uuid.NewString()[:8]}
}

func TestRefunds_RulesAndRefundedEvents(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	admin := uuid.New()
	pay, cust, intent := onlinePayment(t, s, 12451)

	// Not captured yet: not refundable.
	if _, _, err := s.CreateRideRefund(ctx, CreateRideRefundInput{RideID: pay.RideID, AmountPaise: 100, Reason: "x", RequestedBy: admin}); err != ErrRefundNotRefundable {
		t.Fatalf("unpaid refund: %v", err)
	}
	if _, err := s.ApplyRidePaymentEvent(ctx, captureEvent(pay, cust, intent, 12451)); err != nil {
		t.Fatal(err)
	}
	// Mutation guard: more than the remaining amount is refused.
	if _, _, err := s.CreateRideRefund(ctx, CreateRideRefundInput{RideID: pay.RideID, AmountPaise: 12452, Reason: "x", RequestedBy: admin}); err != ErrRefundExceedsRemaining {
		t.Fatalf("over refund: %v", err)
	}
	r1, _, err := s.CreateRideRefund(ctx, CreateRideRefundInput{RideID: pay.RideID, AmountPaise: 5000, Reason: "early end", RequestedBy: admin})
	if err != nil || r1.Status != RefundRequested || r1.IntentID != intent || r1.RequestedBy != admin {
		t.Fatalf("refund 1: %+v %v", r1, err)
	}
	// In-flight refunds count against the remaining amount.
	if _, _, err := s.CreateRideRefund(ctx, CreateRideRefundInput{RideID: pay.RideID, AmountPaise: 7452, Reason: "x", RequestedBy: admin}); err != ErrRefundExceedsRemaining {
		t.Fatalf("over remaining with in-flight: %v", err)
	}
	if _, err := s.MarkRideRefundAccepted(ctx, r1.ID, "cmd_1"); err != nil {
		t.Fatal(err)
	}
	// The PSP settled it: partially refunded, the refund row refunded.
	applied, err := s.ApplyRidePaymentEvent(ctx, refundEvent(pay, intent, 5000, "partially_refunded"))
	if err != nil || applied.Decision.Outcome != payments.OutcomePartiallyRefunded || applied.Status != "partially_refunded" {
		t.Fatalf("partial: %+v %v", applied, err)
	}
	got, _ := s.GetRidePayment(ctx, pay.ID)
	if got.Status != "partially_refunded" || got.RefundedPaise != 5000 {
		t.Fatalf("row = %+v", got)
	}
	if rr, _ := s.GetRideRefund(ctx, r1.ID); rr.Status != RefundRefunded || rr.ProviderReference == nil {
		t.Fatalf("refund row = %+v", rr)
	}
	// Full amount 0 = the rest; a refund over the rest is a mismatch.
	r2, _, err := s.CreateRideRefund(ctx, CreateRideRefundInput{RideID: pay.RideID, Reason: "rest", RequestedBy: admin})
	if err != nil || r2.AmountPaise != 7451 {
		t.Fatalf("refund 2: %+v %v", r2, err)
	}
	applied, _ = s.ApplyRidePaymentEvent(ctx, refundEvent(pay, intent, 7452, "refunded"))
	if applied.Decision.Outcome != payments.OutcomeMismatch {
		t.Fatalf("over-refund event: %+v", applied)
	}
	applied, _ = s.ApplyRidePaymentEvent(ctx, refundEvent(pay, intent, 7451, "refunded"))
	got, _ = s.GetRidePayment(ctx, pay.ID)
	if applied.Decision.Outcome != payments.OutcomeRefunded || got.Status != "refunded" || got.RefundedPaise != 12451 {
		t.Fatalf("full: %+v row %+v", applied, got)
	}
	list, err := s.ListRideRefunds(ctx, RefundRefunded, &pay.RideID, 10, 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d %v", len(list), err)
	}
	// A cash payment can never be refunded through here.
	pid, rid := makePartnerWithRide(t, s)
	if _, err := s.CreateRidePayment(ctx, CreateRidePaymentInput{RideID: rid, PartnerID: pid, AmountPaise: 500, PaymentMethod: "cash", Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateRideRefund(ctx, CreateRideRefundInput{RideID: rid, AmountPaise: 100, Reason: "x", RequestedBy: admin}); err != ErrRefundCashPayment {
		t.Fatalf("cash refund: %v", err)
	}
}

func TestSwitchRidePaymentToCash_OnlyWhileUnpaid(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	pay, cust, intent := onlinePayment(t, s, 8000)
	switched, err := s.SwitchRidePaymentToCash(ctx, pay.RideID)
	if err != nil || switched.PaymentMethod != "cash" || switched.Status != "pending_cash_confirmation" {
		t.Fatalf("switch: %+v %v", switched, err)
	}
	ride, _ := s.GetRide(ctx, pay.RideID)
	if ride.PaymentMethod == nil || *ride.PaymentMethod != "cash" {
		t.Fatalf("ride method = %v", ride.PaymentMethod)
	}
	// A late capture for the old intent is a mismatch, never paid.
	applied, _ := s.ApplyRidePaymentEvent(ctx, captureEvent(pay, cust, intent, 8000))
	got, _ := s.GetRidePayment(ctx, pay.ID)
	if applied.Decision.Outcome != payments.OutcomeMismatch || got.Status != "pending_cash_confirmation" {
		t.Fatalf("late capture on cash: %+v row %s", applied, got.Status)
	}
	if _, err := s.SwitchRidePaymentToCash(ctx, pay.RideID); err != ErrPaymentNotSwitchable {
		t.Fatalf("switch a cash row: %v", err)
	}
	// Mutation guard: never after paid.
	pay2, cust2, intent2 := onlinePayment(t, s, 8000)
	if _, err := s.ApplyRidePaymentEvent(ctx, captureEvent(pay2, cust2, intent2, 8000)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SwitchRidePaymentToCash(ctx, pay2.RideID); err != ErrPaymentNotSwitchable {
		t.Fatalf("switch after paid: %v", err)
	}
}

func TestApplyRidePaymentEvent_OutstandingSettlesAndUnknownTarget(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	_, rid := makePartnerWithRide(t, s)
	ride, _ := s.GetRide(ctx, rid)
	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateOutstandingTx(ctx, tx, ride.CustomerUserID, rid, 2500, "cancellation_fee"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ListPendingOutstanding(ctx, ride.CustomerUserID)
	if len(rows) != 1 {
		t.Fatalf("outstanding rows = %d", len(rows))
	}
	o := rows[0]
	intent := uuid.New()
	if _, err := s.BindOutstandingIntent(ctx, o.ID, ride.CustomerUserID, intent, "card"); err != nil {
		t.Fatal(err)
	}
	// Wrong amount: mismatch, still pending.
	ev := payments.Event{EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentSucceeded, IntentID: intent.String(),
		ReferenceID: o.ID, PayerID: ride.CustomerUserID, AmountMinor: 2400, Currency: "INR", Status: "succeeded"}
	applied, _ := s.ApplyRidePaymentEvent(ctx, ev)
	if applied.Decision.Outcome != payments.OutcomeMismatch || applied.Target != payments.TargetOutstanding {
		t.Fatalf("mismatch: %+v", applied)
	}
	ev.EventID, ev.AmountMinor = "evt-"+uuid.NewString(), 2500
	applied, err = s.ApplyRidePaymentEvent(ctx, ev)
	if err != nil || applied.Decision.Outcome != payments.OutcomeSettled || applied.RideID != rid {
		t.Fatalf("settle: %+v %v", applied, err)
	}
	got, _ := s.GetOutstanding(ctx, o.ID)
	if got.Status != OutstandingSettled || got.SettledIntentID == nil || *got.SettledIntentID != intent || got.SettledByRideID != nil || got.SettledAt == nil {
		t.Fatalf("outstanding = %+v", got)
	}
	// A reference that is neither a ride nor a fee is recorded, not retried.
	ev.EventID, ev.ReferenceID = "evt-"+uuid.NewString(), uuid.New()
	applied, err = s.ApplyRidePaymentEvent(ctx, ev)
	if err != nil || applied.Decision.Outcome != payments.OutcomeTargetNotFound {
		t.Fatalf("unknown: %+v %v", applied, err)
	}
}

func TestListRidePaymentsAdmin_FiltersAndCursor(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		onlinePayment(t, s, 1000+int64(i))
	}
	pid, rid := makePartnerWithRide(t, s)
	if _, err := s.CreateRidePayment(ctx, CreateRidePaymentInput{RideID: rid, PartnerID: pid, AmountPaise: 500, PaymentMethod: "cash", Status: "pending_cash_confirmation"}); err != nil {
		t.Fatal(err)
	}
	page1, next, err := s.ListRidePaymentsAdmin(ctx, RidePaymentFilter{Status: "confirming", Limit: 2})
	if err != nil || len(page1) != 2 || next == "" {
		t.Fatalf("page 1: %d %q %v", len(page1), next, err)
	}
	page2, next2, err := s.ListRidePaymentsAdmin(ctx, RidePaymentFilter{Status: "confirming", Limit: 2, Cursor: next})
	if err != nil || len(page2) != 1 || next2 != "" {
		t.Fatalf("page 2: %d %q %v", len(page2), next2, err)
	}
	if page1[0].ID == page2[0].ID || page1[1].ID == page2[0].ID {
		t.Fatal("cursor repeated a row")
	}
	cash, _, _ := s.ListRidePaymentsAdmin(ctx, RidePaymentFilter{Method: "cash"})
	if len(cash) != 1 {
		t.Fatalf("cash = %d", len(cash))
	}
	future := time.Now().Add(time.Hour)
	none, _, _ := s.ListRidePaymentsAdmin(ctx, RidePaymentFilter{From: &future})
	if len(none) != 0 {
		t.Fatalf("from future = %d", len(none))
	}
	if _, _, err := s.ListRidePaymentsAdmin(ctx, RidePaymentFilter{Cursor: "garbage"}); err == nil {
		t.Fatal("a malformed cursor must be refused")
	}
}

func TestFareWindows_AdminCRUD(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	cs, _ := s.ListActiveCities(ctx)
	if len(cs) == 0 {
		t.Skip("no seeded cities")
	}
	vt := "auto"
	w, err := s.CreateFareWindow(ctx, FareWindowInput{CityID: cs[0].ID, VehicleType: &vt, Name: "Lunch test", DaysOfWeek: 31, StartMinute: 720, EndMinute: 840, MultiplierBPS: 11000, Priority: 3, IsActive: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	name := "Lunch peak"
	mult := int64(12000)
	up, err := s.UpdateFareWindow(ctx, w.ID, FareWindowPatch{Name: &name, MultiplierBPS: &mult, ClearVehicleType: true})
	if err != nil || up.Name != name || up.MultiplierBPS != 12000 || up.VehicleType != nil {
		t.Fatalf("update: %+v %v", up, err)
	}
	off := false
	de, err := s.UpdateFareWindow(ctx, w.ID, FareWindowPatch{IsActive: &off})
	if err != nil || de.IsActive {
		t.Fatalf("deactivate: %+v %v", de, err)
	}
	all, err := s.ListFareWindowsAdmin(ctx, &cs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, x := range all {
		if x.ID == w.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("inactive window missing from the admin list")
	}
	// The pricing list only sees active windows.
	active, _ := s.ListFareWindows(ctx, cs[0].ID, "auto")
	for _, x := range active {
		if x.ID == w.ID {
			t.Fatal("deactivated window still prices")
		}
	}
	if _, err := s.UpdateFareWindow(ctx, uuid.New(), FareWindowPatch{Name: &name}); err != ErrFareWindowNotFound {
		t.Fatalf("unknown: %v", err)
	}
	// The table CHECK is the last line: an out-of-range multiplier is refused.
	if _, err := s.CreateFareWindow(ctx, FareWindowInput{CityID: cs[0].ID, Name: "bad", DaysOfWeek: 1, StartMinute: 0, EndMinute: 60, MultiplierBPS: 40000, IsActive: true}); err == nil {
		t.Fatal("multiplier 40000 accepted")
	}
}

func TestAdminStats_PaymentsLaneCounts(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	pay, cust, intent := onlinePayment(t, s, 3000)
	onlinePayment(t, s, 4000)
	before, err := s.AdminStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.PaymentsConfirming != 2 || before.RefundsRequested != 0 {
		t.Fatalf("before = %+v", before)
	}
	if _, err := s.ApplyRidePaymentEvent(ctx, captureEvent(pay, cust, intent, 3000)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateRideRefund(ctx, CreateRideRefundInput{RideID: pay.RideID, AmountPaise: 1000, Reason: "x", RequestedBy: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	tx, _ := s.BeginTx(ctx)
	if err := CreateOutstandingTx(ctx, tx, cust, pay.RideID, 2500, "cancellation_fee"); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)
	after, _ := s.AdminStats(ctx)
	if after.PaymentsConfirming != 1 || after.RefundsRequested != 1 || after.OutstandingPendingPaise != 2500 {
		t.Fatalf("after = %+v", after)
	}
}

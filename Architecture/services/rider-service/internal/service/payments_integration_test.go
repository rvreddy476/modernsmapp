package service

// Payments lane service tests (TEST_PG_DSN on rider_it_test): the customer
// intent / status / switch-to-cash / callback flow over a fake payments
// client, the signed events applied through the store, admin refunds, the
// outstanding-fee intent, fare windows and the surge state.

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/payments"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// fakePayments is a payments-service that echoes intents (one id per
// idempotency key), verifies every callback and accepts every refund.
type fakePayments struct {
	intents    map[string]uuid.UUID
	created    []payments.CreateIntentInput
	subCreated []payments.CreateIntentInput
	refunds    []string
	// refundCalls records every Refund command (intent, amount, reason).
	refundCalls []fakeRefundCall
	verdict     payments.CallbackVerdict
	refundErr   error
}

type fakeRefundCall struct {
	IntentID    uuid.UUID
	AmountMinor int64
	Reason      string
	Key         string
}

func newFakePayments() *fakePayments {
	return &fakePayments{intents: map[string]uuid.UUID{}}
}

func (f *fakePayments) CreateIntent(_ context.Context, in payments.CreateIntentInput) (*payments.Intent, error) {
	f.created = append(f.created, in)
	id, ok := f.intents[in.IdempotencyKey]
	if !ok {
		id = uuid.New()
		f.intents[in.IdempotencyKey] = id
	}
	return &payments.Intent{
		ID: id, Status: "pending", AmountMinor: in.AmountMinor, Currency: "INR", Method: in.Method, ProviderRef: "order_" + id.String()[:8],
		ReferenceType: payments.RefTypeMopeduRide, ReferenceID: in.ReferenceID, PayerID: in.PayerID,
		ClientSession: map[string]string{"provider": "razorpay", "order_id": "order_" + id.String()[:8], "key_id": "rzp_test_pub"},
	}, nil
}

// CreateSubscriptionIntent echoes a mopedu_subscription intent (one id per
// idempotency key), recorded in subCreated.
func (f *fakePayments) CreateSubscriptionIntent(_ context.Context, in payments.CreateIntentInput) (*payments.Intent, error) {
	f.subCreated = append(f.subCreated, in)
	id, ok := f.intents[in.IdempotencyKey]
	if !ok {
		id = uuid.New()
		f.intents[in.IdempotencyKey] = id
	}
	return &payments.Intent{
		ID: id, Status: "pending", AmountMinor: in.AmountMinor, Currency: "INR", Method: in.Method, ProviderRef: "order_" + id.String()[:8],
		ReferenceType: payments.RefTypeMopeduSubscription, ReferenceID: in.ReferenceID, PayerID: in.PayerID,
		ClientSession: map[string]string{"provider": "razorpay", "order_id": "order_" + id.String()[:8], "key_id": "rzp_test_pub"},
	}, nil
}

func (f *fakePayments) VerifyCallback(_ context.Context, intentID uuid.UUID, in payments.CallbackRequest) (*payments.CallbackVerdict, error) {
	v := f.verdict
	v.Verified, v.Advisory, v.AmountMinor = true, true, in.ExpectedAmountMinor
	return &v, nil
}

func (f *fakePayments) Refund(_ context.Context, intentID uuid.UUID, amountMinor int64, reason, key string) (*payments.RefundAccepted, error) {
	if f.refundErr != nil {
		return nil, f.refundErr
	}
	f.refunds = append(f.refunds, key)
	f.refundCalls = append(f.refundCalls, fakeRefundCall{IntentID: intentID, AmountMinor: amountMinor, Reason: reason, Key: key})
	return &payments.RefundAccepted{CommandID: uuid.New(), IntentID: intentID, AmountMinor: amountMinor, Status: "requested"}, nil
}

// completedOnlineRide books a quote with method, drives it to completed
// and returns the ride, its partner and the payment row.
func completedOnlineRide(t *testing.T, svc *Service, cust uuid.UUID, method string) (*store.Ride, *store.Partner, *store.RidePayment) {
	t.Helper()
	ctx := context.Background()
	q := mustEstimate(t, svc, fridayNoonIST, &cust, "auto", "")
	partner, seedRide := makeApprovedPartnerWithVehicle(t, svc)
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_rides SET status = 'expired' WHERE id = $1`, seedRide); err != nil {
		t.Fatal(err)
	}
	ride, err := bookQuote(t, svc, cust, q.QuoteID, "auto", method)
	if err != nil {
		t.Fatalf("book: %v", err)
	}
	now := svc.now()
	arrived, started := now.Add(-10*time.Minute), now.Add(-8*time.Minute)
	if _, err := svc.Store().DB().Exec(ctx, `
		UPDATE rider_rides SET status = 'in_progress', partner_id = $2, assigned_at = $3, arrived_at = $4, started_at = $5
		WHERE id = $1`, ride.ID, partner.ID, now.Add(-30*time.Minute), arrived, started); err != nil {
		t.Fatal(err)
	}
	r, _ := svc.Store().GetRide(ctx, ride.ID)
	pay, err := svc.CompleteRide(ctx, partner.UserID, r.ID, CompleteRideRequest{
		FinalDistanceKM: 6, FinalDurationMin: 18, IdempotencyKey: "complete-" + r.ID.String(), ExpectedRevision: r.Revision,
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	done, _ := svc.Store().GetRide(ctx, ride.ID)
	return done, partner, pay
}

func payCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	pe, ok := AsPaymentError(err)
	if !ok {
		t.Fatalf("want PaymentError %d %s, got %v", status, code, err)
	}
	if pe.Status != status || pe.Code != code {
		t.Fatalf("payment error = %d %s (%s), want %d %s", pe.Status, pe.Code, pe.Message, status, code)
	}
}

func capturedEvent(ride *store.Ride, intent uuid.UUID, amount int64) payments.Event {
	return payments.Event{EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentSucceeded, IntentID: intent.String(),
		ReferenceID: ride.ID, PayerID: ride.CustomerUserID, AmountMinor: amount, Currency: "INR", Status: "succeeded"}
}

func TestRidePayment_IntentCallbackAndSignedCapture(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	fake := newFakePayments()
	svc.SetPayments(fake, "dev")
	cust := uuid.New()
	ride, partner, pay := completedOnlineRide(t, svc, cust, "upi")
	if pay.Status != "pending" || pay.PaymentMethod != "upi" {
		t.Fatalf("payment after completion = %+v", pay)
	}
	st, _ := svc.GetRidePaymentStatus(ctx, cust, ride.ID)
	if st.Status != payments.PublicPending {
		t.Fatalf("status before intent = %+v", st)
	}

	// Another customer, a bad method, the captain: refused before payments.
	if _, err := svc.CreateRidePaymentIntent(ctx, uuid.New(), ride.ID, "upi"); err == nil || !contains(err.Error(), "forbidden") {
		t.Fatalf("other customer: %v", err)
	}
	_, err := svc.CreateRidePaymentIntent(ctx, cust, ride.ID, "cash")
	payCode(t, err, http.StatusBadRequest, CodePaymentMethodInvalid)
	if len(fake.created) != 0 {
		t.Fatal("payments called for a refused intent")
	}

	out, err := svc.CreateRidePaymentIntent(ctx, cust, ride.ID, "upi")
	if err != nil {
		t.Fatalf("intent: %v", err)
	}
	if out.AmountPaise != pay.AmountPaise || out.Currency != "INR" || out.Status != "pending" || out.ClientSession == nil || out.ClientSession.KeyID != "rzp_test_pub" {
		t.Fatalf("intent response = %+v", out)
	}
	if in := fake.created[0]; in.ReferenceID != ride.ID || in.PayerID != cust || in.IdempotencyKey != "ride:"+ride.ID.String()+":upi" {
		t.Fatalf("payments request = %+v", in)
	}
	// Idempotent: the same ride and method re-opens the same intent.
	again, err := svc.CreateRidePaymentIntent(ctx, cust, ride.ID, "upi")
	if err != nil || again.IntentID != out.IntentID {
		t.Fatalf("re-open: %+v %v", again, err)
	}
	st, _ = svc.GetRidePaymentStatus(ctx, cust, ride.ID)
	if st.Status != payments.PublicConfirming || st.IntentID == nil || *st.IntentID != out.IntentID {
		t.Fatalf("status after intent = %+v", st)
	}
	// The assigned captain can read it too.
	if _, err := svc.GetRidePaymentStatus(ctx, partner.UserID, ride.ID); err != nil {
		t.Fatalf("captain status: %v", err)
	}

	// Mutation guard: a verified callback is advisory and marks nothing paid.
	fake.verdict = payments.CallbackVerdict{ReferenceID: ride.ID, PayerID: cust, ApplicationID: payments.ApplicationID, Status: "succeeded"}
	cb, err := svc.VerifyRidePaymentCallback(ctx, cust, ride.ID, RidePaymentCallback{ProviderOrderID: "order_1", ProviderPaymentID: "pay_1", Signature: "sig"})
	if err != nil || !cb.Verified || !cb.Advisory || cb.Status != payments.PublicConfirming {
		t.Fatalf("callback: %+v %v", cb, err)
	}
	st, _ = svc.GetRidePaymentStatus(ctx, cust, ride.ID)
	if st.Status != payments.PublicConfirming {
		t.Fatalf("callback verdict changed the status to %s", st.Status)
	}
	receipt, _ := svc.GetRideReceipt(ctx, cust, ride.ID)
	if receipt.Payment == nil || receipt.Payment.Status != payments.PublicConfirming {
		t.Fatalf("receipt payment = %+v", receipt.Payment)
	}
	// A genuine signature for another ride is refused.
	fake.verdict.ReferenceID = uuid.New()
	_, err = svc.VerifyRidePaymentCallback(ctx, cust, ride.ID, RidePaymentCallback{ProviderOrderID: "o", ProviderPaymentID: "p", Signature: "s"})
	payCode(t, err, http.StatusUnprocessableEntity, CodeCallbackMismatch)

	// Only the signed event pays.
	applied, err := svc.Store().ApplyRidePaymentEvent(ctx, capturedEvent(ride, out.IntentID, pay.AmountPaise))
	if err != nil || applied.Decision.Outcome != payments.OutcomePaid {
		t.Fatalf("capture: %+v %v", applied, err)
	}
	st, _ = svc.GetRidePaymentStatus(ctx, cust, ride.ID)
	if st.Status != payments.PublicPaid || st.Method != "upi" {
		t.Fatalf("status after capture = %+v", st)
	}
	receipt, _ = svc.GetRideReceipt(ctx, cust, ride.ID)
	if receipt.Payment.Status != payments.PublicPaid || receipt.Payment.AmountPaise != pay.AmountPaise || len(receipt.Refunds) != 0 {
		t.Fatalf("receipt after capture = %+v", receipt.Payment)
	}
	// Mutation guard: never switch to cash after paid; no new intent either.
	_, err = svc.SwitchRidePaymentToCash(ctx, cust, ride.ID)
	payCode(t, err, http.StatusConflict, CodePaymentAlreadyPaid)
	_, err = svc.CreateRidePaymentIntent(ctx, cust, ride.ID, "card")
	payCode(t, err, http.StatusConflict, CodePaymentNotPending)
}

func TestRidePayment_SwitchToCashThenCaptainConfirms(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	fake := newFakePayments()
	svc.SetPayments(fake, "dev")
	cust := uuid.New()
	ride, partner, _ := completedOnlineRide(t, svc, cust, "card")
	if _, err := svc.CreateRidePaymentIntent(ctx, cust, ride.ID, "card"); err != nil {
		t.Fatal(err)
	}
	st, err := svc.SwitchRidePaymentToCash(ctx, cust, ride.ID)
	if err != nil || st.Method != "cash" || st.Status != payments.PublicCashPending {
		t.Fatalf("switch: %+v %v", st, err)
	}
	// Already cash: refused; an intent on a cash ride: refused.
	_, err = svc.SwitchRidePaymentToCash(ctx, cust, ride.ID)
	payCode(t, err, http.StatusConflict, CodePaymentNotOnline)
	_, err = svc.CreateRidePaymentIntent(ctx, cust, ride.ID, "upi")
	payCode(t, err, http.StatusConflict, CodePaymentNotOnline)
	// The captain confirms cash through the existing route.
	r, _ := svc.Store().GetRide(ctx, ride.ID)
	if err := svc.ConfirmCashPayment(ctx, partner.UserID, ride.ID, r.Revision); err != nil {
		t.Fatalf("cash confirm: %v", err)
	}
	st, _ = svc.GetRidePaymentStatus(ctx, cust, ride.ID)
	if st.Status != payments.PublicCashConfirmed {
		t.Fatalf("after cash confirm = %+v", st)
	}
}

func TestRidePayment_NoClientAnswers503(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	cust := uuid.New()
	ride, _, _ := completedOnlineRide(t, svc, cust, "upi")
	_, err := svc.CreateRidePaymentIntent(context.Background(), cust, ride.ID, "upi")
	payCode(t, err, http.StatusServiceUnavailable, CodePaymentsUnavailable)
	_, err = svc.RefundRidePayment(context.Background(), uuid.New(), ride.ID, 100, "x")
	payCode(t, err, http.StatusServiceUnavailable, CodePaymentsUnavailable)
}

func TestRidePayment_AdminRefunds(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	fake := newFakePayments()
	svc.SetPayments(fake, "dev")
	admin := uuid.New()
	cust := uuid.New()
	ride, _, pay := completedOnlineRide(t, svc, cust, "upi")
	out, err := svc.CreateRidePaymentIntent(ctx, cust, ride.ID, "upi")
	if err != nil {
		t.Fatal(err)
	}
	// Not captured yet.
	_, err = svc.RefundRidePayment(ctx, admin, ride.ID, 100, "early")
	payCode(t, err, http.StatusConflict, CodeRefundNotRefundable)
	if _, err := svc.Store().ApplyRidePaymentEvent(ctx, capturedEvent(ride, out.IntentID, pay.AmountPaise)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RefundRidePayment(ctx, admin, ride.ID, 100, "  "); err == nil || !contains(err.Error(), "reason required") {
		t.Fatalf("no reason: %v", err)
	}
	// Mutation guard: more than the remaining amount.
	_, err = svc.RefundRidePayment(ctx, admin, ride.ID, pay.AmountPaise+1, "too much")
	payCode(t, err, http.StatusUnprocessableEntity, CodeRefundExceedsRemaining)
	if len(fake.refunds) != 0 {
		t.Fatal("payments called for a refused refund")
	}
	refund, err := svc.RefundRidePayment(ctx, admin, ride.ID, 5000, "captain ended the ride early")
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if refund.Status != store.RefundAccepted || refund.RequestedBy != admin || refund.AmountPaise != 5000 || refund.ProviderReference == nil {
		t.Fatalf("refund = %+v", refund)
	}
	if len(fake.refunds) != 1 || fake.refunds[0] != "refund:"+refund.ID.String() {
		t.Fatalf("refund keys = %v", fake.refunds)
	}
	stats, _ := svc.AdminStats(ctx)
	if stats.RefundsRequested != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	// Money moved: the signed refunded event.
	applied, err := svc.Store().ApplyRidePaymentEvent(ctx, payments.Event{EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentRefunded,
		IntentID: out.IntentID.String(), ReferenceID: ride.ID, AmountMinor: 5000, Status: "partially_refunded", ProviderRef: "rfnd_1"})
	if err != nil || applied.Decision.Outcome != payments.OutcomePartiallyRefunded {
		t.Fatalf("refunded event: %+v %v", applied, err)
	}
	st, _ := svc.GetRidePaymentStatus(ctx, cust, ride.ID)
	if st.Status != payments.PublicPartiallyRefunded || st.RefundedPaise != 5000 {
		t.Fatalf("status = %+v", st)
	}
	receipt, _ := svc.GetRideReceipt(ctx, cust, ride.ID)
	if receipt.Payment.RefundedPaise != 5000 || len(receipt.Refunds) != 1 || receipt.Refunds[0].Status != store.RefundRefunded {
		t.Fatalf("receipt = %+v refunds %+v", receipt.Payment, receipt.Refunds)
	}
	list, _ := svc.ListRideRefunds(ctx, "", &ride.ID, 10, 0)
	if len(list) != 1 || list[0].Status != store.RefundRefunded {
		t.Fatalf("list = %+v", list)
	}
	// A refused refund command is recorded as failed and answered 502.
	fake.refundErr = payments.ErrRefused
	_, err = svc.RefundRidePayment(ctx, admin, ride.ID, 1000, "again")
	payCode(t, err, http.StatusBadGateway, CodePaymentsRefused)
	failed, _ := svc.ListRideRefunds(ctx, store.RefundFailed, &ride.ID, 10, 0)
	if len(failed) != 1 {
		t.Fatalf("failed refunds = %d", len(failed))
	}
	// Mutation guard: a cash ride is never refunded here.
	cashRide, _, _ := completedOnlineRide(t, svc, cust, "cash")
	_, err = svc.RefundRidePayment(ctx, admin, cashRide.ID, 100, "dispute")
	payCode(t, err, http.StatusUnprocessableEntity, CodeRefundCashPayment)
	items, next, err := svc.ListRidePaymentsAdmin(ctx, store.RidePaymentFilter{Method: "cash"})
	if err != nil || len(items) != 1 || next != "" {
		t.Fatalf("admin list = %d %q %v", len(items), next, err)
	}
}

func TestOutstanding_PaidDirectly(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	fake := newFakePayments()
	svc.SetPayments(fake, "dev")
	cust := uuid.New()
	q := mustEstimate(t, svc, fridayNoonIST, &cust, "auto", "")
	ride, err := bookQuote(t, svc, cust, q.QuoteID, "auto", "upi")
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := svc.Store().BeginTx(ctx)
	if err := store.CreateOutstandingTx(ctx, tx, cust, ride.ID, 2500, "cancellation_fee"); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)
	rows, err := svc.ListMyOutstanding(ctx, cust)
	if err != nil || len(rows) != 1 {
		t.Fatalf("outstanding = %d %v", len(rows), err)
	}
	if _, err := svc.CreateOutstandingPaymentIntent(ctx, uuid.New(), rows[0].ID, "upi"); err == nil || !contains(err.Error(), "forbidden") {
		t.Fatalf("other customer: %v", err)
	}
	out, err := svc.CreateOutstandingPaymentIntent(ctx, cust, rows[0].ID, "upi")
	if err != nil || out.AmountPaise != 2500 {
		t.Fatalf("intent: %+v %v", out, err)
	}
	if in := fake.created[0]; in.ReferenceID != rows[0].ID || in.IdempotencyKey != "outstanding:"+rows[0].ID.String()+":upi" {
		t.Fatalf("payments request = %+v", in)
	}
	applied, err := svc.Store().ApplyRidePaymentEvent(ctx, payments.Event{EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentSucceeded,
		IntentID: out.IntentID.String(), ReferenceID: rows[0].ID, PayerID: cust, AmountMinor: 2500, Currency: "INR", Status: "succeeded"})
	if err != nil || applied.Decision.Outcome != payments.OutcomeSettled {
		t.Fatalf("settle: %+v %v", applied, err)
	}
	rows, _ = svc.ListMyOutstanding(ctx, cust)
	if len(rows) != 0 {
		t.Fatalf("still outstanding: %+v", rows)
	}
	_, err = svc.CreateOutstandingPaymentIntent(ctx, cust, applied.TargetID, "upi")
	payCode(t, err, http.StatusConflict, CodeOutstandingNotPayable)
	// The next quote no longer charges it.
	q2 := mustEstimate(t, svc, fridayNoonIST, &cust, "auto", "")
	if q2.OutstandingPaise != 0 {
		t.Fatalf("settled fee still quoted: %d", q2.OutstandingPaise)
	}
}

func TestFareWindows_ServiceValidationAndSurge(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	admin := uuid.New()
	blr := pickBangaloreCity(t, svc)
	bad := []FareWindowRequest{
		{Name: "no city", DaysOfWeek: 1, StartMinute: 0, EndMinute: 60, MultiplierBPS: 11000},
		{CityID: blr.ID, Name: "", DaysOfWeek: 1, StartMinute: 0, EndMinute: 60, MultiplierBPS: 11000},
		{CityID: blr.ID, Name: "days", DaysOfWeek: 128, StartMinute: 0, EndMinute: 60, MultiplierBPS: 11000},
		{CityID: blr.ID, Name: "start", DaysOfWeek: 1, StartMinute: 1440, EndMinute: 60, MultiplierBPS: 11000},
		{CityID: blr.ID, Name: "end", DaysOfWeek: 1, StartMinute: 0, EndMinute: 1441, MultiplierBPS: 11000},
		{CityID: blr.ID, Name: "mult", DaysOfWeek: 1, StartMinute: 0, EndMinute: 60, MultiplierBPS: 30001},
	}
	vt := "boat"
	bad = append(bad, FareWindowRequest{CityID: blr.ID, VehicleType: &vt, Name: "vt", DaysOfWeek: 1, StartMinute: 0, EndMinute: 60, MultiplierBPS: 11000})
	for _, req := range bad {
		if _, err := svc.CreateFareWindow(ctx, admin, req); err == nil || !contains(err.Error(), "invalid:") {
			t.Fatalf("%s accepted: %v", req.Name, err)
		}
	}
	auto := "auto"
	w, err := svc.CreateFareWindow(ctx, admin, FareWindowRequest{CityID: blr.ID, VehicleType: &auto, Name: "Lunch", DaysOfWeek: 31, StartMinute: 720, EndMinute: 840, MultiplierBPS: 11000, Priority: 20})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Friday 12:30 is inside the new window; it outranks nothing else then.
	est := mustEstimate(t, svc, fridayNoonIST.Add(30*time.Minute), nil, "auto", "")
	if est.SurgeBPS != 1000 || est.WindowName != "Lunch" {
		t.Fatalf("estimate in new window: %d %q", est.SurgeBPS, est.WindowName)
	}
	end := 1500
	if _, err := svc.UpdateFareWindow(ctx, admin, w.ID, FareWindowPatchRequest{EndMinute: &end}); err == nil {
		t.Fatal("end_minute 1500 accepted")
	}
	clear := ""
	up, err := svc.UpdateFareWindow(ctx, admin, w.ID, FareWindowPatchRequest{VehicleType: &clear})
	if err != nil || up.VehicleType != nil {
		t.Fatalf("clear vehicle type: %+v %v", up, err)
	}
	off, err := svc.DeactivateFareWindow(ctx, admin, w.ID, "done")
	if err != nil || off.IsActive {
		t.Fatalf("deactivate: %+v %v", off, err)
	}
	est = mustEstimate(t, svc, fridayNoonIST.Add(30*time.Minute), nil, "auto", "")
	if est.SurgeBPS != 0 {
		t.Fatalf("deactivated window still prices: %d", est.SurgeBPS)
	}
	list, _ := svc.ListFareWindowsAdmin(ctx, &blr.ID)
	if len(list) < 5 {
		t.Fatalf("list = %d", len(list))
	}
	if _, err := svc.UpdateFareWindow(ctx, admin, uuid.New(), FareWindowPatchRequest{}); err == nil || !contains(err.Error(), "not_found") {
		t.Fatalf("unknown window: %v", err)
	}

	// Surge state: ten open auto requests and no partners -> the cap.
	for i := 0; i < 10; i++ {
		if _, err := svc.Store().CreateRide(ctx, store.CreateRideInput{
			CustomerUserID: uuid.New(), CityID: &blr.ID, VehicleType: "auto",
			PickupAddress: "P", PickupLat: 12.97, PickupLng: 77.59, DropAddress: "D", DropLat: 12.93, DropLng: 77.62,
		}); err != nil {
			t.Fatal(err)
		}
	}
	state, err := svc.SurgeState(ctx, blr.ID)
	if err != nil || len(state) == 0 {
		t.Fatalf("surge: %v %v", state, err)
	}
	var seen bool
	for _, s := range state {
		if s.VehicleType == "auto" {
			seen = true
			if s.Requested != 10 || s.Online != 0 || s.DemandBPS != svc.cfg.SurgeCapBPS || s.CapBPS != svc.cfg.SurgeCapBPS {
				t.Fatalf("auto state = %+v", s)
			}
		}
	}
	if !seen {
		t.Fatal("auto missing from the surge state")
	}
	if _, err := svc.SurgeState(ctx, uuid.Nil); err == nil {
		t.Fatal("nil city accepted")
	}
}

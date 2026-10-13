package itest

import (
	"context"
	"fmt"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"sync"
	"testing"
	"time"
)

func autoQuoteAddress(t *testing.T, dr *dsRig, user uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	status, body := dr.user(user, "POST", "/quotes", fmt.Sprintf(`{"service_id":"%s","option_id":"%s","lat":%f,"lng":%f}`, dr.world.anyService, dr.world.anyOption, dr.world.lat, dr.world.lng))
	want(t, "admin-priced quote", status, body, 201)
	var q model.Quote
	data(t, body, &q)
	if q.AssignmentMode != "automatic" || q.ProID != uuid.Nil || q.TotalPaise != dr.world.pricePaise {
		t.Fatalf("wrong automatic quote: %+v", q)
	}
	status, body = dr.user(user, "POST", "/addresses", fmt.Sprintf(`{"label":"Home","line1":"Test address","locality":"HITEC City","pincode":"500081","lat":%f,"lng":%f}`, dr.world.lat, dr.world.lng))
	want(t, "address", status, body, 201)
	var a model.Address
	data(t, body, &a)
	return q.ID, a.ID
}

func TestAutomaticBookingFirstAcceptanceAndPayment(t *testing.T) {
	dr := newDispatchRig(t)
	dr.svc.WithAutomaticAssignment(true)
	a, au := dr.addPro(t, "female")
	b, bu := dr.addPro(t, "female")
	// Pro prices deliberately disagree with the administrator's fixed price.
	adminTx(t, dr.p, func(exec func(string, ...any)) {
		exec(`UPDATE doorstep.pro_service_prices SET price_paise=123400 WHERE pro_id=ANY($1)`, []uuid.UUID{a, b})
	})
	user := uuid.New()
	q, addr := autoQuoteAddress(t, dr, user)
	start := dr.tomorrowAt(10, 0)
	status, body := dr.book(user, q, addr, start, "automatic-test")
	want(t, "matching booking", status, body, 201)
	var out model.BookingCreated
	data(t, body, &out)
	id := out.Booking.ID
	if out.Booking.Status != "matching" || len(out.PaymentIntent.Checkout) != 0 || out.Booking.Professional != nil {
		t.Fatalf("charged or selected before acceptance: %+v", out)
	}
	if len(dr.pay.intents) != 0 {
		t.Fatal("payment intent created before acceptance")
	}
	status, body = dr.user(user, "POST", "/bookings/"+id.String()+"/payment/intent", `{}`)
	want(t, "cannot pay matching request", status, body, 409)
	offers := map[uuid.UUID]uuid.UUID{}
	for _, p := range []itPro{{id: a, user: au}, {id: b, user: bu}} {
		status, body = dr.pro(p.user, "GET", "/offers", "")
		want(t, "partner offers", status, body, 200)
		var list struct {
			Items []model.Offer `json:"items"`
		}
		data(t, body, &list)
		if len(list.Items) > 1 {
			t.Fatalf("unexpected offers: %s", body)
		}
		if len(list.Items) == 1 {
			offers[p.user] = list.Items[0].ID
		}
	}
	if len(offers) != 1 {
		t.Fatalf("one booking must only alert one partner, got %d", len(offers))
	}
	// A duplicate app tap/retry is idempotent; exactly one calendar/assignment.
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for user, offer := range offers {
		for range 2 {
			wg.Add(1)
			go func(u, o uuid.UUID) {
				defer wg.Done()
				code, _ := dr.pro(u, "POST", "/offers/"+o.String()+"/accept", `{}`)
				codes <- code
			}(user, offer)
		}
	}
	wg.Wait()
	close(codes)
	wins, loses := 0, 0
	for code := range codes {
		if code == 200 {
			wins++
		} else if code == 409 {
			loses++
		} else {
			t.Fatalf("unexpected accept status %d", code)
		}
	}
	if wins != 2 || loses != 0 {
		t.Fatalf("accept race: %d wins / %d losses", wins, loses)
	}
	if dr.status(t, id) != "pending_payment" || len(dr.activeBlocks(t, id)) != 1 {
		t.Fatal("winner not held awaiting payment")
	}
	if len(dr.pay.intents) != 0 {
		t.Fatal("acceptance charged the customer")
	}
	status, body = dr.user(user, "POST", "/bookings/"+id.String()+"/payment/intent", `{}`)
	want(t, "pay after acceptance", status, body, 200)
	var intent model.PaymentIntent
	data(t, body, &intent)
	if intent.AmountPaise != dr.world.pricePaise {
		t.Fatal("partner changed customer price")
	}
	dr.captured(t, id, "auto-payment-once", nil)
	dr.captured(t, id, "auto-payment-once", nil)
	if dr.status(t, id) != "assigned" {
		t.Fatalf("paid booking = %s", dr.status(t, id))
	}
	if len(dr.events(t, id, "doorstep.booking.assigned")) != 1 {
		t.Fatal("assignment notified more than once")
	}
	var count int
	if err := dr.p.QueryRow(context.Background(), `SELECT count(*) FROM doorstep.booking_assignments WHERE booking_id=$1 AND status='accepted'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("accepted count=%d: %v", count, err)
	}
}

func TestAutomaticMatchingExpiresWithoutPayment(t *testing.T) {
	dr := newDispatchRig(t)
	dr.svc.WithAutomaticAssignment(true)
	dr.addPro(t, "female")
	user := uuid.New()
	q, a := autoQuoteAddress(t, dr, user)
	status, body := dr.book(user, q, a, dr.tomorrowAt(10, 0), "expiry-test")
	want(t, "booking", status, body, 201)
	var out model.BookingCreated
	data(t, body, &out)
	dr.now = dr.now.Add(11 * time.Minute)
	if err := dr.st.MatchAutomatic(context.Background(), out.Booking.ID, dr.now); err != nil {
		t.Fatal(err)
	}
	if dr.status(t, out.Booking.ID) != "expired" || len(dr.pay.intents) != 0 {
		t.Fatal("matching did not expire without charging")
	}
}

func automaticRequest(t *testing.T, dr *dsRig, key string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	u := uuid.New()
	q, a := autoQuoteAddress(t, dr, u)
	code, raw := dr.book(u, q, a, dr.tomorrowAt(10, 0), key)
	want(t, "request", code, raw, 201)
	var out model.BookingCreated
	data(t, raw, &out)
	return out.Booking.ID, u
}

func automaticOffer(t *testing.T, dr *dsRig, booking, pro uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := dr.p.QueryRow(context.Background(), `SELECT id FROM doorstep.booking_assignments WHERE booking_id=$1 AND pro_id=$2 AND status='offered'`, booking, pro).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Deterministic first recipient, exercising the fairness tie-break rather than UUID luck.
func preferAutomaticPro(t *testing.T, dr *dsRig, pro uuid.UUID) {
	adminTx(t, dr.p, func(exec func(string, ...any)) {
		exec(`UPDATE doorstep.professionals SET offers_received=CASE WHEN id=$1 THEN 0 ELSE 10 END`, pro)
	})
}

func TestAutomaticSequentialDeadlineAndStaleAccept(t *testing.T) {
	dr := newDispatchRig(t)
	dr.svc.WithAutomaticAssignment(true)
	a, au := dr.addPro(t, "female")
	b, bu := dr.addPro(t, "female")
	preferAutomaticPro(t, dr, a)
	id, _ := automaticRequest(t, dr, "sequential-expiry")
	first := automaticOffer(t, dr, id, a)
	var expires time.Time
	if err := dr.p.QueryRow(context.Background(), `SELECT offer_expires_at FROM doorstep.booking_assignments WHERE id=$1`, first).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires.Sub(dr.now) != store.AutomaticOfferWindow {
		t.Fatal("incorrect offer deadline")
	}
	// Concurrent worker ticks must not create multiple live offers.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := dr.st.MatchAutomatic(context.Background(), id, dr.now); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if v := dr.one(t, `SELECT count(*) FROM doorstep.booking_assignments WHERE booking_id=$1 AND status='offered'`, id); v.(int64) != 1 {
		t.Fatal("parallel offers created")
	}
	dr.now = expires
	code, raw := dr.pro(au, "POST", "/offers/"+first.String()+"/accept", `{}`)
	want(t, "deadline is exclusive", code, raw, 410)
	if err := dr.st.MatchAutomatic(context.Background(), id, dr.now); err != nil {
		t.Fatal(err)
	}
	automaticOffer(t, dr, id, b)
	if v := dr.one(t, `SELECT count(*) FROM doorstep.booking_assignments WHERE booking_id=$1 AND status='offered'`, id); v.(int64) != 1 {
		t.Fatal("expired offer still live")
	}
	if len(dr.events(t, id, "doorstep.pro.offer_closed")) != 1 {
		t.Fatal("expiry not emitted")
	}
	automaticAccept(t, dr, id, b, bu)
	code, raw = dr.pro(au, "POST", "/offers/"+first.String()+"/accept", `{}`)
	if code != 409 && code != 410 {
		t.Fatalf("stale offer accepted: %d %s", code, raw)
	}
}

func automaticAccept(t *testing.T, dr *dsRig, booking, pro, user uuid.UUID) {
	t.Helper()
	offer := automaticOffer(t, dr, booking, pro)
	code, raw := dr.pro(user, "POST", "/offers/"+offer.String()+"/accept", `{}`)
	want(t, "accept", code, raw, 200)
}

func automaticPay(t *testing.T, dr *dsRig, booking, customer uuid.UUID) {
	t.Helper()
	code, raw := dr.user(customer, "POST", "/bookings/"+booking.String()+"/payment/intent", `{}`)
	want(t, "payment intent", code, raw, 200)
	dr.captured(t, booking, "capture-"+booking.String(), nil)
}

func TestAutomaticReplacementKeepsPriceAndDoesNotChargeAgain(t *testing.T) {
	dr := newDispatchRig(t)
	dr.svc.WithAutomaticAssignment(true)
	a, au := dr.addPro(t, "female")
	b, bu := dr.addPro(t, "female")
	preferAutomaticPro(t, dr, a)
	id, customer := automaticRequest(t, dr, "replace-paid")
	automaticAccept(t, dr, id, a, au)
	automaticPay(t, dr, id, customer)
	code, raw := dr.pro(au, "POST", "/jobs/"+id.String()+"/cancel", `{"reason":"Unavailable for the visit"}`)
	want(t, "partner cancellation", code, raw, 204)
	if dr.status(t, id) != "matching" {
		t.Fatal("replacement not queued")
	}
	// Customers cannot bypass matching to pick a professional.
	start := dr.tomorrowAt(10, 0)
	code, raw = dr.changePro(customer, id, b, &start, "cannot-pick")
	want(t, "no customer pick", code, raw, 409)
	automaticAccept(t, dr, id, b, bu)
	if dr.status(t, id) != "assigned" || len(dr.pay.intents) != 1 {
		t.Fatal("replacement reopened payment")
	}
	if v := dr.one(t, `SELECT total_paise FROM doorstep.bookings WHERE id=$1`, id); v.(int64) != dr.world.pricePaise {
		t.Fatal("replacement changed price")
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.refunds WHERE booking_id=$1`, id); v.(int64) != 0 {
		t.Fatal("replacement incorrectly refunded")
	}
}

func TestAutomaticReplacementTimeoutRefunds(t *testing.T) {
	dr := newDispatchRig(t)
	dr.svc.WithAutomaticAssignment(true)
	p, u := dr.addPro(t, "female")
	id, customer := automaticRequest(t, dr, "no-replacement")
	automaticAccept(t, dr, id, p, u)
	automaticPay(t, dr, id, customer)
	code, raw := dr.pro(u, "POST", "/jobs/"+id.String()+"/cancel", `{"reason":"Cannot attend"}`)
	want(t, "cancel", code, raw, 204)
	dr.now = dr.now.Add(11 * time.Minute)
	if err := dr.st.MatchAutomatic(context.Background(), id, dr.now); err != nil {
		t.Fatal(err)
	}
	if dr.status(t, id) != "cancelled" || len(dr.activeBlocks(t, id)) != 0 {
		t.Fatal("unserved replacement was not cancelled")
	}
	if v := dr.one(t, `SELECT amount_paise FROM doorstep.refunds WHERE booking_id=$1`, id); v.(int64) != dr.world.pricePaise {
		t.Fatal("missing full refund")
	}
	settleAutomaticRefund(t, dr, id, payments.CauseUnassigned)
}

func TestAutomaticLatePaymentRefundsAndReleasesAcceptance(t *testing.T) {
	dr := newDispatchRig(t)
	dr.svc.WithAutomaticAssignment(true)
	p, u := dr.addPro(t, "female")
	id, customer := automaticRequest(t, dr, "late-payment")
	automaticAccept(t, dr, id, p, u)
	code, raw := dr.user(customer, "POST", "/bookings/"+id.String()+"/payment/intent", `{}`)
	want(t, "intent", code, raw, 200)
	dr.now = dr.now.Add(11 * time.Minute)
	// Delayed capture arrives before the hold sweeper runs.
	dr.captured(t, id, "late-auto", nil)
	if dr.status(t, id) != "expired" || len(dr.activeBlocks(t, id)) != 0 {
		t.Fatal("late capture resurrected an expired acceptance")
	}
	if dr.inboxOutcome(t, "late-auto") != string(payments.OutcomeLateCaptureRefund) {
		t.Fatal("late payment was not refunded")
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.booking_assignments WHERE booking_id=$1 AND status='accepted'`, id); v.(int64) != 0 {
		t.Fatal("stale accepted assignment")
	}
	settleAutomaticRefund(t, dr, id, payments.CauseLateCapture)
}

func settleAutomaticRefund(t *testing.T, dr *dsRig, id uuid.UUID, cause string) {
	t.Helper()
	if _, err := dr.svc.ResubmitPendingRefunds(context.Background()); err != nil {
		t.Fatal(err)
	}
	key := payments.RefundKey(id, cause)
	cmd, ok := dr.pay.refunds[key]
	if !ok {
		t.Fatal("refund command was not submitted")
	}
	i := dr.intentFor(t, id)
	dr.event(t, "refund-auto-"+id.String(), events.EventPaymentRefunded, map[string]any{"id": i.ID.String(), "intent_id": i.ID.String(), "amount_minor": dr.world.pricePaise, "status": "refunded", "reference_type": payments.RefBooking, "reference_id": id.String(), "command_id": cmd.String(), "application_id": payments.ApplicationID})
	if v := dr.one(t, `SELECT refunded_paise FROM doorstep.bookings WHERE id=$1`, id); v.(int64) != dr.world.pricePaise {
		t.Fatal("refund not settled")
	}
}

func TestAutomaticOffersRespectOwnershipAndChangedEligibility(t *testing.T) {
	dr := newDispatchRig(t)
	dr.svc.WithAutomaticAssignment(true)
	p, u := dr.addPro(t, "female")
	_, stranger := dr.addPro(t, "female")
	preferAutomaticPro(t, dr, p)
	id, _ := automaticRequest(t, dr, "ownership")
	offer := automaticOffer(t, dr, id, p)
	code, raw := dr.pro(stranger, "POST", "/offers/"+offer.String()+"/accept", `{}`)
	want(t, "foreign offer", code, raw, 404)
	adminTx(t, dr.p, func(exec func(string, ...any)) {
		exec(`UPDATE doorstep.professionals SET incident_suspended=true WHERE id=$1`, p)
	})
	code, raw = dr.pro(u, "POST", "/offers/"+offer.String()+"/accept", `{}`)
	want(t, "changed eligibility", code, raw, 409)
	if len(dr.activeBlocks(t, id)) != 0 {
		t.Fatal("ineligible partner got a hold")
	}
}

func TestAutomaticFairnessAndDeclineTryTheNextAvailablePartner(t *testing.T) {
	dr := newDispatchRig(t)
	dr.svc.WithAutomaticAssignment(true)
	a, au := dr.addPro(t, "female")
	b, bu := dr.addPro(t, "female")
	preferAutomaticPro(t, dr, a)
	first, customer := automaticRequest(t, dr, "fairness-first")
	automaticAccept(t, dr, first, a, au)
	automaticPay(t, dr, first, customer)

	user := uuid.New()
	q, addr := autoQuoteAddress(t, dr, user)
	code, raw := dr.book(user, q, addr, dr.tomorrowAt(14, 0), "fairness-next")
	want(t, "next request", code, raw, 201)
	var next model.BookingCreated
	data(t, raw, &next)
	id := next.Booking.ID
	if v := dr.one(t, `SELECT count(*) FROM doorstep.booking_assignments WHERE booking_id=$1 AND status='offered'`, id); v.(int64) != 1 {
		t.Fatal("offer must go to the lower-workload partner first")
	}
	offer := automaticOffer(t, dr, id, b)
	code, raw = dr.pro(bu, "POST", "/offers/"+offer.String()+"/decline", `{"reason":"busy"}`)
	want(t, "decline", code, raw, 204)
	automaticAccept(t, dr, id, a, au)
	if dr.status(t, id) != "pending_payment" {
		t.Fatal("declining the initial offer prevented matching another available partner")
	}
}

func TestAutomaticWorkerRecoversMatchingWithoutLiveOffers(t *testing.T) {
	dr := newDispatchRig(t)
	dr.svc.WithAutomaticAssignment(true)
	p, _ := dr.addPro(t, "female")
	id, _ := automaticRequest(t, dr, "worker-recovery")
	// Simulate withdrawal before an interrupted dispatch can send the next cohort.
	dr.exec(t, `UPDATE doorstep.booking_assignments SET status='cancelled' WHERE booking_id=$1`, id)
	queued, err := dr.st.AutomaticQueue(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range queued {
		found = found || candidate == id
	}
	if !found {
		t.Fatal("matching booking missing from the persisted retry queue")
	}
	if _, err := dr.svc.DispatchTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	automaticOffer(t, dr, id, p)
	if _, err := dr.svc.DispatchTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.booking_assignments WHERE booking_id=$1 AND status='offered'`, id); v.(int64) != 1 {
		t.Fatal("worker retry duplicated the live offer")
	}
	if len(dr.pay.intents) != 0 || len(dr.activeBlocks(t, id)) != 0 {
		t.Fatal("dispatch retry charged or reserved before acceptance")
	}
}

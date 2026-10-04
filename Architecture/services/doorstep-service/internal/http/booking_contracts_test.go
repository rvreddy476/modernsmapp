package http

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/doorstep-service/internal/config"
	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/google/uuid"
)

// Booking contract fixtures (A3): one customer books kitchen deep cleaning
// for Monday 5 Oct 14:00 IST, pays (the signed event's effect), reschedules
// once and cancels; plus the refusals and the admin pages. Same rules as
// contracts_test.go: real route table, pinned clock (Sunday 4 Oct 12:00
// IST) and request id, deterministic ids (devseed.ID("fixture", ...)).

type bookingRig struct {
	*rig
	bk  *fakeBookingStore
	pay *fakePaymentsAPI
}

func newBookingRig(t *testing.T, devStub bool) *bookingRig {
	t.Helper()
	rg := newRig(t)
	br := &bookingRig{rig: rg, bk: newFakeBookingStore(), pay: newFakePaymentsAPI()}
	crypto, err := propii.New(context.Background(), []config.PIIKey{{Version: 1, Key: bytes.Repeat([]byte{0x24}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	tc, err := tax.NewGST(nil, testGSTIN(t))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	svc := service.New(rg.store, tc, 15*time.Minute).
		WithClock(func() time.Time { return fixtureNow }, func() uuid.UUID { return fixtureQuoteID }).
		WithBookings(service.BookingDeps{Store: br.bk, Payments: br.pay, PII: crypto, DevStubPayments: devStub,
			NewID: func() uuid.UUID { n++; return devseed.ID("fixture", fmt.Sprintf("booking-flow-%d", n)) }})
	rg.r = mount(svc, testInternalKey, rg.v)
	return br
}

func (br *bookingRig) as(user uuid.UUID, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	h := map[string]string{"X-User-Id": user.String()}
	for i := 0; i+1 < len(hdr); i += 2 {
		h[hdr[i]] = hdr[i+1]
	}
	return br.do(req{method: method, path: "/v1/doorstep" + path, body: body, headers: h})
}

// quote prices kitchen deep cleaning (occupied + chimney) through the real
// quote route and registers it with the booking store under id.
func (br *bookingRig) quote(t *testing.T, id uuid.UUID) {
	t.Helper()
	w := br.as(fixtureUser, "POST", "/quotes", quoteBody(idOf("service", kitchen), idOf("option", kitchen+"/occupied"),
		[]string{idOf("addon", kitchen+"/appliances/chimney")}, inZoneLat, inZoneLng, 0))
	mustStatus(t, "quote", w, 201)
	q := *br.store.quotes[fixtureQuoteID]
	q.ID = id
	br.bk.addQuote(q, inZoneLat, inZoneLng)
}

const addressBody = `{"label":"Home","line1":"Flat 4B, Cyber Residency","line2":"Road No. 2","landmark":"Opposite Cyber Towers","locality":"HITEC City","pincode":"500081","lat":17.4504,"lng":78.3808}`

func bookBody(quote, address uuid.UUID, start string) string {
	return fmt.Sprintf(`{"quote_id":"%s","address_id":"%s","slot_start":"%s"}`, quote, address, start)
}

var (
	q1        = devseed.ID("fixture", "quote-1")
	q2        = devseed.ID("fixture", "quote-2")
	q3        = devseed.ID("fixture", "quote-3")
	mon14     = "2026-10-05T08:30:00Z" // Monday 5 Oct 14:00 IST
	tue10     = "2026-10-06T04:30:00Z" // Tuesday 6 Oct 10:00 IST
	debtor    = uuid.MustParse("4f1a2b3c-4d5e-4f60-8172-839405a6b7c8")
	newBookID = func(n int) uuid.UUID { return devseed.ID("fixture", fmt.Sprintf("booking-flow-%d", n)) }
)

func addressID(t *testing.T, w *httptest.ResponseRecorder) uuid.UUID {
	t.Helper()
	var a struct {
		ID uuid.UUID `json:"id"`
	}
	decodeData(t, w, &a)
	return a.ID
}

func TestContract_BookingJourney(t *testing.T) {
	br := newBookingRig(t, true)
	br.quote(t, q1)

	// Addresses: sealed lines answered to the owner, serviceability on save.
	w := br.as(fixtureUser, "POST", "/addresses", addressBody)
	assertFixture(t, "address_post_201.json", w, 201)
	addr := addressID(t, w)
	assertFixture(t, "addresses_get_200.json", br.as(fixtureUser, "GET", "/addresses", ""), 200)
	assertFixture(t, "address_post_422_outside_area.json", br.as(fixtureUser, "POST", "/addresses",
		strings.Replace(strings.Replace(addressBody, "17.4504", "17.4399", 1), "78.3808", "78.4983", 1)), 422)

	// Slots for the quote at that address.
	w = br.as(fixtureUser, "GET", "/slots?quote_id="+q1.String()+"&address_id="+addr.String(), "")
	assertFixture(t, "slots_get_200.json", w, 200)
	if !strings.Contains(w.Body.String(), `"start":"2026-10-05T08:30:00Z","end":"2026-10-05T12:00:00Z","available":true`) {
		t.Fatalf("Monday 14:00 not offered: %s", w.Body.String())
	}

	// Book: Idempotency-Key required; the hold goes to the best professional.
	mustCode(t, "no key", br.as(fixtureUser, "POST", "/bookings", bookBody(q1, addr, mon14)), 400, "DOORSTEP_INVALID_REQUEST")
	assertFixture(t, "booking_post_400_idempotency_key.json", br.as(fixtureUser, "POST", "/bookings", bookBody(q1, addr, mon14)), 400)
	w = br.as(fixtureUser, "POST", "/bookings", bookBody(q1, addr, mon14), "Idempotency-Key", "fixture-booking-1")
	assertFixture(t, "booking_post_201.json", w, 201)
	first := w.Body.String()
	var created struct {
		Booking struct {
			ID uuid.UUID `json:"id"`
		} `json:"booking"`
	}
	decodeData(t, w, &created)
	b := created.Booking.ID
	if b2 := br.bk.bookings[b]; b2 == nil || b2.pro != fakePro1 {
		t.Fatal("the hold did not go to the best-ranked professional")
	}
	// Replay answers the same bytes; the quote is consumed for a new key.
	if replay := br.as(fixtureUser, "POST", "/bookings", bookBody(q1, addr, mon14), "Idempotency-Key", "fixture-booking-1"); replay.Body.String() != first {
		t.Fatalf("replay differs:\n%s\n%s", replay.Body.String(), first)
	}
	assertFixture(t, "booking_post_410_quote_expired.json",
		br.as(fixtureUser, "POST", "/bookings", bookBody(q1, addr, mon14), "Idempotency-Key", "fixture-booking-other"), 410)
	assertFixture(t, "booking_payment_intent_post_200.json", br.as(fixtureUser, "POST", "/bookings/"+b.String()+"/payment/intent", ""), 200)
	assertFixture(t, "booking_payment_get_200_pending.json", br.as(fixtureUser, "GET", "/bookings/"+b.String()+"/payment", ""), 200)
	assertFixture(t, "booking_get_200_pending_payment.json", br.as(fixtureUser, "GET", "/bookings/"+b.String(), ""), 200)

	// The signed payment.succeeded applied (the consumer's effect).
	br.bk.confirm(b, fixtureNow)
	assertFixture(t, "booking_get_200.json", br.as(fixtureUser, "GET", "/bookings/"+b.String(), ""), 200)
	assertFixture(t, "booking_payment_get_200.json", br.as(fixtureUser, "GET", "/bookings/"+b.String()+"/payment", ""), 200)
	mustCode(t, "intent once paid", br.as(fixtureUser, "POST", "/bookings/"+b.String()+"/payment/intent", ""), 409, "DOORSTEP_PAYMENT_ALREADY_SETTLED")
	assertFixture(t, "bookings_get_200.json", br.as(fixtureUser, "GET", "/bookings?status=all", ""), 200)
	assertFixture(t, "booking_get_404.json", br.as(otherUser, "GET", "/bookings/"+b.String(), ""), 404)
	assertFixture(t, "cancel_preview_get_200.json", br.as(fixtureUser, "GET", "/bookings/"+b.String()+"/cancel-preview", ""), 200)

	// Move once to Tuesday 10:00; a second move is refused.
	assertFixture(t, "booking_reschedule_post_200.json",
		br.as(fixtureUser, "POST", "/bookings/"+b.String()+"/reschedule", `{"slot_start":"`+tue10+`"}`), 200)
	assertFixture(t, "booking_reschedule_post_409.json",
		br.as(fixtureUser, "POST", "/bookings/"+b.String()+"/reschedule", `{"slot_start":"2026-10-06T05:30:00Z"}`), 409)

	// Cancel before assignment: free, full refund submitted with its key.
	assertFixture(t, "booking_cancel_post_200.json",
		br.as(fixtureUser, "POST", "/bookings/"+b.String()+"/cancel", `{"reason":"Plans changed"}`), 200)
	if _, ok := br.pay.refunds[payments.RefundKey(b, payments.CauseCustomerCancel)]; !ok {
		t.Fatal("cancel refund not submitted with doorstep:refund:{booking}:customer_cancel")
	}
	assertFixture(t, "booking_payment_get_200_refund.json", br.as(fixtureUser, "GET", "/bookings/"+b.String()+"/payment", ""), 200)
	mustCode(t, "cancel twice", br.as(fixtureUser, "POST", "/bookings/"+b.String()+"/cancel", `{"reason":"again"}`), 409, "DOORSTEP_INVALID_TRANSITION")
}

func TestContract_BookingRefusals(t *testing.T) {
	br := newBookingRig(t, false)
	w := br.as(fixtureUser, "POST", "/addresses", addressBody)
	mustStatus(t, "address", w, 201)
	addr := addressID(t, w)

	// Off the grid (13:15), inside the lead time, and on Sunday (no hours).
	br.quote(t, q1)
	assertFixture(t, "booking_post_422_slot_unavailable.json",
		br.as(fixtureUser, "POST", "/bookings", bookBody(q1, addr, "2026-10-05T07:45:00Z"), "Idempotency-Key", "r-1"), 422)
	mustCode(t, "lead time", br.as(fixtureUser, "POST", "/bookings", bookBody(q1, addr, "2026-10-04T07:00:00Z"), "Idempotency-Key", "r-2"),
		422, "DOORSTEP_SLOT_UNAVAILABLE")
	mustCode(t, "sunday", br.as(fixtureUser, "POST", "/bookings", bookBody(q1, addr, "2026-10-04T12:30:00Z"), "Idempotency-Key", "r-3"),
		422, "DOORSTEP_SLOT_UNAVAILABLE")

	// Every candidate lost the race for the calendar.
	br.bk.loseRace = true
	assertFixture(t, "booking_post_409_slot_taken.json",
		br.as(fixtureUser, "POST", "/bookings", bookBody(q1, addr, mon14), "Idempotency-Key", "r-4"), 409)
	br.bk.loseRace = false

	// Outstanding extras block slots and bookings.
	br.bk.outstanding[debtor] = []uuid.UUID{devseed.ID("fixture", "extras-bill-1")}
	w = br.as(debtor, "POST", "/addresses", addressBody)
	mustStatus(t, "debtor address", w, 201)
	daddr := addressID(t, w)
	br.quote(t, q2)
	assertFixture(t, "slots_get_409_outstanding.json", br.as(debtor, "GET", "/slots?quote_id="+q2.String(), ""), 409)
	assertFixture(t, "booking_post_409_outstanding.json",
		br.as(debtor, "POST", "/bookings", bookBody(q2, daddr, mon14), "Idempotency-Key", "d-1"), 409)

	// The dev stub confirm does not exist outside development.
	w = br.as(fixtureUser, "POST", "/bookings", bookBody(q1, addr, mon14), "Idempotency-Key", "r-5")
	mustStatus(t, "book", w, 201)
	var created struct {
		Booking struct {
			ID uuid.UUID `json:"id"`
		} `json:"booking"`
	}
	decodeData(t, w, &created)
	assertFixture(t, "booking_payment_stub_confirm_404.json",
		br.as(fixtureUser, "POST", "/bookings/"+created.Booking.ID.String()+"/payment/stub-confirm", ""), 404)

	// The hold lapsed: the intent is gone.
	past := fixtureNow.Add(-time.Minute)
	br.bk.bookings[created.Booking.ID].hold = &past
	assertFixture(t, "booking_payment_intent_410_hold_expired.json",
		br.as(fixtureUser, "POST", "/bookings/"+created.Booking.ID.String()+"/payment/intent", ""), 410)
}

// The dev stub confirm asks payments-service to settle the stub intent and
// reports the payment rows; it never marks the booking paid itself, and it
// refuses when payments has a real provider.
func TestBookingStubConfirm(t *testing.T) {
	br := newBookingRig(t, true)
	w := br.as(fixtureUser, "POST", "/addresses", addressBody)
	addr := addressID(t, w)
	br.quote(t, q3)
	w = br.as(fixtureUser, "POST", "/bookings", bookBody(q3, addr, mon14), "Idempotency-Key", "stub-1")
	mustStatus(t, "book", w, 201)
	var created struct {
		Booking struct {
			ID uuid.UUID `json:"id"`
		} `json:"booking"`
	}
	decodeData(t, w, &created)
	path := "/bookings/" + created.Booking.ID.String() + "/payment/stub-confirm"
	mustCode(t, "real provider", br.as(fixtureUser, "POST", path, ""), 409, "DOORSTEP_STUB_UNAVAILABLE")
	for _, i := range br.pay.intents {
		i.ClientSession.Provider = payments.StubProvider
	}
	w = br.as(fixtureUser, "POST", path, "")
	mustStatus(t, "stub confirm", w, 200)
	if !strings.Contains(w.Body.String(), `"status":"pending"`) || br.bk.bookings[created.Booking.ID].status != "pending_payment" {
		t.Fatalf("stub confirm marked the booking paid itself: %s", w.Body.String())
	}
	mustCode(t, "another customer", br.as(otherUser, "POST", path, ""), 404, "DOORSTEP_BOOKING_NOT_FOUND")
}

func TestContract_AdminBookings(t *testing.T) {
	br := newBookingRig(t, true)
	w := br.as(fixtureUser, "POST", "/addresses", addressBody)
	addr := addressID(t, w)
	br.quote(t, q1)
	w = br.as(fixtureUser, "POST", "/bookings", bookBody(q1, addr, mon14), "Idempotency-Key", "adm-1")
	mustStatus(t, "book", w, 201)
	b := newBookID(2) // 1 = the address
	br.bk.confirm(b, fixtureNow)
	admin := func(perm, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
		h := br.adminHeaders(perm)
		for i := 0; i+1 < len(hdr); i += 2 {
			h[hdr[i]] = hdr[i+1]
		}
		return br.do(req{method: method, path: InternalAdminPrefix + path, body: body, headers: h, noKey: true})
	}
	assertFixture(t, "admin_bookings_list_200.json", admin(PermBookingsRead, "GET", "/bookings?city=hyd", ""), 200)
	w = admin(PermBookingsRead, "GET", "/bookings/"+b.String(), "")
	assertFixture(t, "admin_booking_get_200.json", w, 200)
	if !strings.Contains(w.Body.String(), `"start_otp":null`) || !strings.Contains(w.Body.String(), `"end_otp":null`) {
		t.Fatalf("admin booking shows an OTP: %s", w.Body.String())
	}
	assertFixture(t, "admin_booking_refund_422_exceeds.json",
		admin(PermRefundsIssue, "POST", "/bookings/"+b.String()+"/refund", `{"amount_paise":999999,"reason":"Goodwill"}`, "Idempotency-Key", "console-rf-0"), 422)
	mustCode(t, "refund without a key", admin(PermRefundsIssue, "POST", "/bookings/"+b.String()+"/refund", `{"amount_paise":10000,"reason":"Goodwill"}`),
		400, "DOORSTEP_INVALID_REQUEST")
	w = admin(PermRefundsIssue, "POST", "/bookings/"+b.String()+"/refund", `{"amount_paise":10000,"reason":"Goodwill"}`, "Idempotency-Key", "console-rf-1")
	assertFixture(t, "admin_booking_refund_201.json", w, 201)
	if again := admin(PermRefundsIssue, "POST", "/bookings/"+b.String()+"/refund", `{"amount_paise":10000,"reason":"Goodwill"}`,
		"Idempotency-Key", "console-rf-1"); again.Body.String() != w.Body.String() {
		t.Fatal("refund replay answered another refund")
	}
	mustCode(t, "cancel needs bookings.cancel", admin(PermBookingsRead, "POST", "/bookings/"+b.String()+"/cancel", `{"reason":"x"}`),
		403, CodeAdminPermissionScope)
	assertFixture(t, "admin_booking_cancel_200.json",
		admin(PermBookingsCancel, "POST", "/bookings/"+b.String()+"/cancel", `{"reason":"Professional unwell; customer informed"}`), 200)
	assertFixture(t, "admin_stats_200.json", admin(PermStatsRead, "GET", "/stats", ""), 200)
	for _, a := range br.bk.audits {
		if a.UserID != br.actor {
			t.Fatalf("audited actor %+v", a)
		}
	}
	if len(br.bk.audits) != 2 {
		t.Fatalf("audits %d, want the refund and the cancel", len(br.bk.audits))
	}
	// The admin cancel refunded what was left after the ops refund.
	var total int64
	for _, r := range br.bk.refunds {
		total += r.r.AmountPaise
	}
	if total != br.bk.bookings[b].paid {
		t.Fatalf("refunded %d of %d", total, br.bk.bookings[b].paid)
	}
	_ = model.Booking{}
}

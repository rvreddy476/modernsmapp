package http

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/google/uuid"
)

// B1 contract fixtures (4 Oct 2026): the customer's professionals list
// (scheduled, ASAP, ASAP with nobody), quoting and booking the picked
// professional, a dearer change of professional on a pro_unavailable
// booking, the professional's own prices and the admin price review. Same
// rules as contracts_test.go: real route table, pinned clock (Sunday 4 Oct
// 12:00 IST), request id "fixture", deterministic ids.

func professionalsPath(service, option string, addons []string, addr uuid.UUID, extra string) string {
	q := url.Values{}
	q.Set("option_id", option)
	for _, a := range addons {
		q.Add("addon_id", a)
	}
	q.Set("address_id", addr.String())
	s := "/services/" + service + "/professionals?" + q.Encode()
	if extra != "" {
		s += "&" + extra
	}
	return s
}

func TestContract_ProfessionalsList(t *testing.T) {
	br := newBookingRig(t, true)
	w := br.as(fixtureUser, "POST", "/addresses", addressBody)
	mustStatus(t, "address", w, 201)
	addr := addressID(t, w)
	kitchenList := func(extra string) string {
		return professionalsPath(idOf("service", kitchen), idOf("option", kitchen+"/occupied"),
			[]string{idOf("addon", kitchen+"/appliances/chimney")}, addr, extra)
	}

	// Scheduled, Monday: Ravi (cheaper) then Asha, each with their own
	// price, a distance band and the next free starts; never an exact
	// distance or location.
	w = br.as(fixtureUser, "GET", kitchenList("date=2026-10-05"), "")
	assertFixture(t, "service_professionals_get_200.json", w, 200)
	s := w.Body.String()
	if strings.Index(s, fakePro2.String()) > strings.Index(s, fakePro1.String()) || strings.Index(s, fakePro1.String()) < 0 {
		t.Fatalf("not sorted by price: %s", s)
	}
	for _, leak := range []string{`"distance_m"`, `"lat"`, `"lng"`, `"home`, `"live_distance`, `"last_fix`} {
		if strings.Contains(s, leak) {
			t.Fatalf("the list leaks %q: %s", leak, s)
		}
	}
	// Sorted by rating instead: Asha (rated) first.
	w = br.as(fixtureUser, "GET", kitchenList("date=2026-10-05&sort=rating"), "")
	mustStatus(t, "by rating", w, 200)
	if s := w.Body.String(); strings.Index(s, fakePro1.String()) > strings.Index(s, fakePro2.String()) {
		t.Fatalf("not sorted by rating: %s", s)
	}
	// A woman professional required: Asha only.
	w = br.as(fixtureUser, "GET", kitchenList("date=2026-10-05&require_female_pro=true"), "")
	if s := w.Body.String(); w.Code != 200 || strings.Contains(s, fakePro2.String()) || !strings.Contains(s, fakePro1.String()) {
		t.Fatalf("woman required: %d %s", w.Code, s)
	}
	// Women's salon: Ravi prices it but is never listed (gender rule).
	w = br.as(fixtureUser, "GET", professionalsPath(idOf("service", facial), idOf("option", facial+"/gold"),
		[]string{idOf("addon", facial+"/mask/charcoal")}, addr, "date=2026-10-05"), "")
	if s := w.Body.String(); w.Code != 200 || strings.Contains(s, fakePro2.String()) || !strings.Contains(s, fakePro1.String()) {
		t.Fatalf("women's salon: %d %s", w.Code, s)
	}

	// ASAP: Ravi is on duty 3.2 km away and takes same-day jobs: an ETA.
	assertFixture(t, "service_professionals_get_200_asap.json", br.as(fixtureUser, "GET", kitchenList("asap=true"), ""), 200)
	// Nobody on duty: said so, with the scheduled alternatives (founder:
	// "include schedule as well in case no worker found").
	br.bk.pros[1].OnDuty = false
	w = br.as(fixtureUser, "GET", kitchenList("asap=true"), "")
	assertFixture(t, "service_professionals_get_200_asap_none.json", w, 200)
	if s := w.Body.String(); !strings.Contains(s, `"no_professional":true`) || !strings.Contains(s, `"no_professional_reason":"none_available_now"`) ||
		!strings.Contains(s, `"scheduled_alternatives":[{`) {
		t.Fatalf("asap none: %s", s)
	}
	br.bk.pros[1].OnDuty = true
	// A stale fix is not "on duty in range".
	stale := fixtureNow.Add(-10 * time.Minute)
	br.bk.pros[1].LastFixAt = &stale
	if w := br.as(fixtureUser, "GET", kitchenList("asap=true"), ""); !strings.Contains(w.Body.String(), `"no_professional":true`) {
		t.Fatalf("stale fix listed: %s", w.Body.String())
	}
	fresh := fixtureNow.Add(-time.Minute)
	br.bk.pros[1].LastFixAt = &fresh

	assertFixture(t, "service_professionals_get_400_address.json", br.as(fixtureUser, "GET",
		"/services/"+idOf("service", kitchen)+"/professionals?option_id="+idOf("option", kitchen+"/occupied"), ""), 400)
	mustCode(t, "date and asap", br.as(fixtureUser, "GET", kitchenList("asap=true&date=2026-10-05"), ""), 400, "DOORSTEP_INVALID_REQUEST")
	mustCode(t, "date outside the horizon", br.as(fixtureUser, "GET", kitchenList("date=2026-12-01"), ""), 400, "DOORSTEP_INVALID_REQUEST")
	mustCode(t, "another customer's address", br.as(otherUser, "GET", kitchenList(""), ""), 404, "DOORSTEP_ADDRESS_NOT_FOUND")
}

func TestContract_QuotePickedProfessional(t *testing.T) {
	rg := newRig(t)
	// No professional picked: refused.
	body := strings.Replace(quoteBody(idOf("service", kitchen), idOf("option", kitchen+"/occupied"), nil, inZoneLat, inZoneLng, 0),
		`"pro_id":"`+fakePro1.String()+`",`, "", 1)
	assertFixture(t, "quote_post_400_pro_required.json", rg.do(req{method: "POST", path: "/v1/doorstep/quotes", headers: rg.user(), body: body}), 400)
	// A professional with no approved price for the selection: refused, no
	// city price fills the gap.
	nobody := devseed.ID("fixture", "pro-without-prices")
	assertFixture(t, "quote_post_422_price_unavailable.json", rg.do(req{method: "POST", path: "/v1/doorstep/quotes", headers: rg.user(),
		body: quoteBodyPro(nobody, idOf("service", kitchen), idOf("option", kitchen+"/occupied"), nil, inZoneLat, inZoneLng, 0)}), 422)
	// Ravi's quote is Ravi's prices.
	w := rg.do(req{method: "POST", path: "/v1/doorstep/quotes", headers: rg.user(),
		body: quoteBodyPro(fakePro2, idOf("service", kitchen), idOf("option", kitchen+"/occupied"),
			[]string{idOf("addon", kitchen+"/appliances/chimney")}, inZoneLat, inZoneLng, 0)})
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"total_paise":209800`) || !strings.Contains(w.Body.String(), fakePro2.String()) {
		t.Fatalf("Ravi's quote: %d %s", w.Code, w.Body.String())
	}
}

// bookAs prices kitchen deep cleaning with pro and registers the quote.
func (br *bookingRig) quoteFor(t *testing.T, pro, id uuid.UUID) {
	t.Helper()
	w := br.as(fixtureUser, "POST", "/quotes", quoteBodyPro(pro, idOf("service", kitchen), idOf("option", kitchen+"/occupied"),
		[]string{idOf("addon", kitchen+"/appliances/chimney")}, inZoneLat, inZoneLng, 0))
	mustStatus(t, "quote", w, 201)
	q := *br.store.quotes[fixtureQuoteID]
	q.ID = id
	br.bk.addQuote(q, inZoneLat, inZoneLng)
}

func TestContract_BookingASAPAndChange(t *testing.T) {
	br := newBookingRig(t, true)
	w := br.as(fixtureUser, "POST", "/addresses", addressBody)
	addr := addressID(t, w)

	// ASAP with Ravi: the block runs from now, the job starts at the ETA.
	qa := devseed.ID("fixture", "quote-asap")
	br.quoteFor(t, fakePro2, qa)
	mustCode(t, "slot and asap", br.as(fixtureUser, "POST", "/bookings",
		fmt.Sprintf(`{"quote_id":"%s","address_id":"%s","slot_start":"%s","asap":true}`, qa, addr, mon14), "Idempotency-Key", "asap-0"),
		400, "DOORSTEP_INVALID_REQUEST")
	w = br.as(fixtureUser, "POST", "/bookings", fmt.Sprintf(`{"quote_id":"%s","address_id":"%s","asap":true}`, qa, addr),
		"Idempotency-Key", "asap-1")
	assertFixture(t, "booking_post_201_asap.json", w, 201)
	var created struct {
		Booking struct {
			ID uuid.UUID `json:"id"`
		} `json:"booking"`
	}
	decodeData(t, w, &created)
	asapID := created.Booking.ID
	ab := br.bk.bookings[asapID]
	if ab.pro != fakePro2 || !ab.nb.Asap || !ab.block.Start.Equal(fixtureNow) || !ab.nb.SlotStart.Equal(fixtureNow.Add(15*time.Minute)) {
		t.Fatalf("asap booking %+v block %+v", ab.nb, ab.block)
	}
	if !ab.hold.Equal(fixtureNow.Add(5 * time.Minute)) {
		t.Fatalf("asap hold until %v", ab.hold)
	}

	// Scheduled with Ravi (the hold on him, nobody else); paid; then Ravi
	// is gone (the dispatch side puts it in pro_unavailable).
	qb := devseed.ID("fixture", "quote-ravi")
	br.quoteFor(t, fakePro2, qb)
	w = br.as(fixtureUser, "POST", "/bookings", bookBody(qb, addr, tue10), "Idempotency-Key", "ravi-1")
	mustStatus(t, "book Ravi", w, 201)
	decodeData(t, w, &created)
	b := created.Booking.ID
	if br.bk.bookings[b].pro != fakePro2 {
		t.Fatal("the hold did not go on the picked professional")
	}
	br.bk.confirm(b, fixtureNow)
	deadline, cause := fixtureNow.Add(30*time.Minute), "declined"
	rb := br.bk.bookings[b]
	rb.status, rb.pro, rb.deadline, rb.cause, rb.excluded = "pro_unavailable", uuid.Nil, &deadline, &cause, []uuid.UUID{fakePro2}

	// Asha costs more: she is held while the difference is paid (a
	// doorstep_extras intent); the booking stays pro_unavailable.
	body := fmt.Sprintf(`{"pro_id":"%s","slot_start":"%s"}`, fakePro1, tue10)
	w = br.as(fixtureUser, "POST", "/bookings/"+b.String()+"/change-professional", body, "Idempotency-Key", "change-asha")
	assertFixture(t, "booking_change_professional_post_200_charge.json", w, 200)
	s := w.Body.String()
	if !strings.Contains(s, `"status":"pending_payment"`) || !strings.Contains(s, `"difference_paise":15000`) ||
		!strings.Contains(s, `"reference_type":"doorstep_extras"`) || br.bk.bookings[b].status != "pro_unavailable" {
		t.Fatalf("dearer change: %s", s)
	}
	if _, ok := br.pay.intents["doorstep:extras:"+br.bk.bookings[b].changes[0].in.BillID.String()]; !ok {
		t.Fatal("the difference's intent was not opened on doorstep:extras:{bill}")
	}
	assertFixture(t, "booking_get_200_pending_change.json", br.as(fixtureUser, "GET", "/bookings/"+b.String(), ""), 200)
	// The window closes: a change is refused, a full refund follows (the
	// worker; pinned by internal/itest).
	late := fixtureNow.Add(-time.Minute)
	br.bk.bookings[b].deadline = &late
	assertFixture(t, "booking_change_professional_409_window.json", br.as(fixtureUser, "POST", "/bookings/"+b.String()+"/change-professional",
		fmt.Sprintf(`{"pro_id":"%s","asap":true}`, fakePro1), "Idempotency-Key", "change-late"), 409)
	assertFixture(t, "booking_professionals_get_409.json", br.as(fixtureUser, "GET", "/bookings/"+asapID.String()+"/professionals", ""), 409)
}

func TestContract_ProPricesAndReview(t *testing.T) {
	dr := newDispatchFixtureRig(t)
	mustStatus(t, "apply", dr.call(proUser, "POST", "/apply", `{"display_name":"Lakshmi Devi","city_code":"HYD","category_ids":["`+
		idOf("category", "home-cleaning")+`"]}`), 201)
	k := "home-cleaning/kitchen-deep-cleaning"
	assertFixture(t, "pro_prices_get_200.json", dr.call(proUser, "GET", "/me/prices", ""), 200)
	price := func(item string, paise int) string {
		return fmt.Sprintf(`{"service_id":"%s","item_kind":"option","item_id":"%s","price_paise":%d}`, idOf("service", k), idOf("option", k+"/"+item), paise)
	}
	assertFixture(t, "pro_price_post_409_unchanged.json", dr.call(proUser, "POST", "/me/prices", price("occupied", 179900)), 409)
	w := dr.call(proUser, "POST", "/me/prices", price("occupied", 189900))
	assertFixture(t, "pro_price_post_201.json", w, 201)
	var sub struct {
		ID uuid.UUID `json:"id"`
	}
	decodeData(t, w, &sub)
	assertFixture(t, "pro_price_post_403_skill.json", dr.call(proUser, "POST", "/me/prices",
		fmt.Sprintf(`{"service_id":"%s","item_kind":"option","item_id":"%s","price_paise":129900}`, idOf("service", facial), idOf("option", facial+"/gold"))), 403)
	assertFixture(t, "pro_price_post_400.json", dr.call(proUser, "POST", "/me/prices", price("occupied", 50)), 400)
	assertFixture(t, "pro_same_day_put_200.json", dr.call(proUser, "PUT", "/me/services/"+idOf("service", k)+"/same-day", `{"enabled":true}`), 200)
	// The live price stays live while the new one waits.
	if w := dr.call(proUser, "GET", "/me/prices", ""); !strings.Contains(w.Body.String(), `"price_paise":179900,"status":"approved"`) ||
		!strings.Contains(w.Body.String(), `"price_paise":189900,"status":"pending"`) {
		t.Fatalf("prices after submit: %s", w.Body.String())
	}

	// Admin review: the queue, a reject needs a reason, approve (audited),
	// a decided price cannot be decided again, the permission is its own.
	assertFixture(t, "admin_pro_prices_get_200.json", dr.admin(PermPricesReview, "GET", "/pro-prices?status=pending", ""), 200)
	assertFixture(t, "admin_pro_price_reject_400_reason.json", dr.admin(PermPricesReview, "POST", "/pro-prices/"+sub.ID.String()+"/reject", `{}`), 400)
	assertFixture(t, "admin_pro_price_403_scope.json", dr.admin(PermProsApprove, "POST", "/pro-prices/"+sub.ID.String()+"/approve", `{}`), 403)
	assertFixture(t, "admin_pro_price_approve_200.json", dr.admin(PermPricesReview, "POST", "/pro-prices/"+sub.ID.String()+"/approve",
		`{"reason":"in line with the market"}`), 200)
	assertFixture(t, "admin_pro_price_approve_409.json", dr.admin(PermPricesReview, "POST", "/pro-prices/"+sub.ID.String()+"/approve", `{}`), 409)
	if n := len(dr.bk.prices.audits); n != 1 || dr.bk.prices.audits[0].UserID != dr.actor || dr.bk.prices.audits[0].Permission != PermPricesReview {
		t.Fatalf("price audits %+v", dr.bk.prices.audits)
	}
	// Reject a second submission with a reason; withdraw a third.
	w = dr.call(proUser, "POST", "/me/prices", price("empty", 159900))
	decodeData(t, w, &sub)
	assertFixture(t, "admin_pro_price_reject_200.json", dr.admin(PermPricesReview, "POST", "/pro-prices/"+sub.ID.String()+"/reject",
		`{"reason":"far above the city's suggested price"}`), 200)
	w = dr.call(proUser, "POST", "/me/prices", price("empty", 139900))
	decodeData(t, w, &sub)
	assertFixture(t, "pro_price_withdraw_200.json", dr.call(proUser, "POST", "/me/prices/"+sub.ID.String()+"/withdraw", ""), 200)
	assertFixture(t, "pro_price_withdraw_409.json", dr.call(proUser, "POST", "/me/prices/"+sub.ID.String()+"/withdraw", ""), 409)
}

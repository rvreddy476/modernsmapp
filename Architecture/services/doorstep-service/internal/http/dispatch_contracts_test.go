package http

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/doorstep-service/internal/config"
	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/google/uuid"
)

// Dispatch, presence and realtime contract fixtures (A4): Lakshmi (call_a,
// approved through the real onboarding routes) is the professional nearest
// a kitchen deep cleaning booked for Monday 5 Oct 14:00 IST; the paid
// booking is offered to her, she reads the offer (locality only), goes on
// duty, accepts (the address appears), lists her jobs; ops redispatch it.
// Same rules as contracts_test.go: real route table, pinned clock (Sunday
// 4 Oct 12:00 IST), request id "fixture", deterministic ids.

type dispatchRig struct {
	*proRig
	bk     *fakeBookingStore
	ds     *fakeDispatchStore
	frames *fakeFrames
	svc    *service.Service
}

var lakshmiPro = devseed.ID("professional", proUser.String())

func newDispatchFixtureRig(t *testing.T) *dispatchRig {
	t.Helper()
	bk := newFakeBookingStore()
	// Lakshmi joins the slot candidates, nearest of the three.
	l := fakeSlotPros()[1]
	l.ID, l.UserID, l.Gender, l.DistanceM, l.Blocks, l.JobsByDay = lakshmiPro, proUser, "female", 600, nil, map[string]int{}
	bk.pros = append(bk.pros, l)
	crypto, err := propii.New(context.Background(), []config.PIIKey{{Version: 1, Key: bytes.Repeat([]byte{0x24}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	pros := newFakeProStore()
	dr := &dispatchRig{bk: bk, frames: &fakeFrames{}}
	dr.ds = newFakeDispatchStore(bk, pros)
	n := 0
	pr := newProRigExt(t, func(svc *service.Service) *service.Service {
		dr.svc = svc.WithBookings(service.BookingDeps{Store: bk, Payments: newFakePaymentsAPI(), PII: crypto,
			NewID: func() uuid.UUID { n++; return devseed.ID("fixture", fmt.Sprintf("dispatch-flow-%d", n)) }}).
			WithDispatch(service.DispatchDeps{Store: dr.ds, Realtime: dr.frames, Signer: fixtureSigner{}})
		return dr.svc
	}, func(d *service.ProDeps) { d.Store = pros })
	pr.pro = pros
	dr.proRig = pr
	return dr
}

func (dr *dispatchRig) customer(user uuid.UUID, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	h := map[string]string{"X-User-Id": user.String()}
	for i := 0; i+1 < len(hdr); i += 2 {
		h[hdr[i]] = hdr[i+1]
	}
	return dr.do(req{method: method, path: "/v1/doorstep" + path, body: body, headers: h})
}

// paidBooking: Lakshmi approved; the customer books Monday 14:00 and pays;
// the payment event's after-hook dispatches.
func (dr *dispatchRig) paidBooking(t *testing.T) uuid.UUID {
	t.Helper()
	mustStatus(t, "apply", dr.call(proUser, "POST", "/apply", `{"display_name":"Lakshmi Devi","city_code":"HYD","category_ids":["`+
		idOf("category", "home-cleaning")+`"]}`), 201)
	completeSteps(t, dr.proRig, proUser, "mock-female")
	mustStatus(t, "approve", dr.admin(PermProsApprove, "POST", "/professionals/"+lakshmiPro.String()+"/approve", ""), 200)

	w := dr.customer(fixtureUser, "POST", "/quotes", quoteBody(idOf("service", kitchen), idOf("option", kitchen+"/occupied"),
		[]string{idOf("addon", kitchen+"/appliances/chimney")}, inZoneLat, inZoneLng, 0))
	mustStatus(t, "quote", w, 201)
	var qv struct {
		ID uuid.UUID `json:"id"`
	}
	decodeData(t, w, &qv)
	q := *dr.store.quotes[qv.ID]
	q.ID = q1
	dr.bk.addQuote(q, inZoneLat, inZoneLng)
	w = dr.customer(fixtureUser, "POST", "/addresses", addressBody)
	mustStatus(t, "address", w, 201)
	addr := addressID(t, w)
	w = dr.customer(fixtureUser, "POST", "/bookings", bookBody(q1, addr, mon14), "Idempotency-Key", "fixture-dispatch-1")
	mustStatus(t, "book", w, 201)
	var created struct {
		Booking struct {
			ID uuid.UUID `json:"id"`
		} `json:"booking"`
	}
	decodeData(t, w, &created)
	b := created.Booking.ID
	if dr.bk.bookings[b].pro != lakshmiPro {
		t.Fatal("the hold did not go to the nearest professional")
	}
	dr.bk.confirm(b, fixtureNow)
	dr.svc.AfterPaymentEvent(context.Background(), payments.Applied{BookingID: b,
		Decision: payments.Decision{Outcome: payments.OutcomeConfirmed}})
	return b
}

func TestContract_DispatchJourney(t *testing.T) {
	dr := newDispatchFixtureRig(t)
	b := dr.paidBooking(t)
	if len(dr.ds.assigns) != 1 || dr.ds.assigns[0].pro != lakshmiPro || dr.ds.assigns[0].status != "offered" {
		t.Fatalf("offer: %+v", dr.ds.assigns)
	}
	offer := dr.ds.assigns[0].id

	// Offers: locality only, never the address.
	w := dr.call(proUser, "GET", "/offers", "")
	assertFixture(t, "pro_offers_get_200.json", w, 200)
	if strings.Contains(w.Body.String(), "Cyber") || !strings.Contains(w.Body.String(), `"expires_at":"2026-10-04T08:30:00Z"`) {
		t.Fatalf("offers: %s", w.Body.String())
	}
	assertFixture(t, "pro_offer_get_200.json", dr.call(proUser, "GET", "/offers/"+offer.String(), ""), 200)
	assertFixture(t, "pro_offer_get_404.json", dr.call(proUser, "GET", "/offers/"+uuid.NewString(), ""), 404)
	w = dr.call(proUser, "GET", "/jobs/"+b.String(), "")
	assertFixture(t, "pro_job_get_200_offered.json", w, 200)
	if !strings.Contains(w.Body.String(), `"address":null`) || strings.Contains(w.Body.String(), "Cyber") {
		t.Fatalf("job before accept: %s", w.Body.String())
	}
	assertFixture(t, "pro_offer_accept_404.json", dr.call(proUser, "POST", "/offers/"+devseed.ID("fixture", "nobody").String()+"/accept", ""), 404)
	assertFixture(t, "pro_offer_decline_400_reason.json", dr.call(proUser, "POST", "/offers/"+offer.String()+"/decline", `{"reason":"tired"}`), 400)

	// An earlier, lapsed offer cannot be accepted.
	lapsed := devseed.ID("fixture", "offer-lapsed")
	dr.ds.assigns = append(dr.ds.assigns, &fakeAssign{id: lapsed, booking: b, pro: lakshmiPro, status: "expired", expires: fixtureNow})
	assertFixture(t, "pro_offer_accept_410_expired.json", dr.call(proUser, "POST", "/offers/"+lapsed.String()+"/accept", ""), 410)

	// Duty and location.
	assertFixture(t, "pro_location_409_not_on_duty.json", dr.call(proUser, "POST", "/location", `{"lat":17.44,"lng":78.37}`), 409)
	assertFixture(t, "pro_duty_on_200.json", dr.call(proUser, "POST", "/duty/on", `{"lat":17.44,"lng":78.37,"accuracy_m":15}`), 200)
	assertFixture(t, "pro_me_duty_get_200.json", dr.call(proUser, "GET", "/me/duty", ""), 200)
	mustStatus(t, "location", dr.call(proUser, "POST", "/location", `{"lat":17.441,"lng":78.371}`), 204)
	assertFixture(t, "pro_location_400.json", dr.call(proUser, "POST", "/location", `{"lat":97,"lng":78.37}`), 400)

	// Accept: the job now carries the address and the chat.
	w = dr.call(proUser, "POST", "/offers/"+offer.String()+"/accept", "")
	assertFixture(t, "pro_offer_accept_200.json", w, 200)
	if !strings.Contains(w.Body.String(), "Flat 4B, Cyber Residency") || !strings.Contains(w.Body.String(), `"status":"assigned"`) {
		t.Fatalf("accepted: %s", w.Body.String())
	}
	assertFixture(t, "pro_job_get_200.json", dr.call(proUser, "GET", "/jobs/"+b.String(), ""), 200)
	assertFixture(t, "pro_jobs_get_200.json", dr.call(proUser, "GET", "/jobs?status=upcoming&date=2026-10-05", ""), 200)
	assertFixture(t, "pro_jobs_get_400_date.json", dr.call(proUser, "GET", "/jobs?date=05-10-2026", ""), 400)

	// Realtime tokens: the customer's booking only; the professional's own
	// topic plus the accepted booking.
	w = dr.customer(fixtureUser, "POST", "/realtime/token", fmt.Sprintf(`{"booking_id":"%s"}`, b))
	assertFixture(t, "realtime_token_post_200.json", w, 200)
	assertFixture(t, "realtime_token_post_404.json", dr.customer(otherUser, "POST", "/realtime/token", fmt.Sprintf(`{"booking_id":"%s"}`, b)), 404)
	w = dr.call(proUser, "POST", "/realtime/token", "")
	assertFixture(t, "pro_realtime_token_post_200.json", w, 200)
	if !strings.Contains(w.Body.String(), "doorstep.booking."+b.String()) || !strings.Contains(w.Body.String(), "doorstep.pro."+proUser.String()) {
		t.Fatalf("pro token topics: %s", w.Body.String())
	}

	// Her profile reads.
	assertFixture(t, "pro_zones_get_200.json", dr.call(proUser, "GET", "/zones", ""), 200)
	assertFixture(t, "pro_me_skills_get_200.json", dr.call(proUser, "GET", "/me/skills", ""), 200)
	assertFixture(t, "pro_me_area_get_200.json", dr.call(proUser, "GET", "/me/area", ""), 200)
	assertFixture(t, "pro_me_bank_get_200.json", dr.call(proUser, "GET", "/me/bank", ""), 200)
	assertFixture(t, "pro_me_documents_get_200.json", dr.call(proUser, "GET", "/me/documents", ""), 200)
	assertFixture(t, "pro_duty_off_200.json", dr.call(proUser, "POST", "/duty/off", ""), 200)

	// Ops redispatch excluding Asha: Lakshmi's job goes back to confirmed
	// and Ravi is offered it. Ops never name him.
	w = dr.admin(PermBookingsRedispatch, "POST", "/bookings/"+b.String()+"/redispatch",
		fmt.Sprintf(`{"reason":"Customer asked for someone else","exclude_pro_ids":["%s"]}`, fakePro1))
	assertFixture(t, "admin_booking_redispatch_200.json", w, 200)
	if last := dr.ds.assigns[len(dr.ds.assigns)-1]; last.pro != fakePro2 || last.status != "offered" {
		t.Fatalf("redispatch offered %+v", last)
	}
	assertFixture(t, "admin_booking_redispatch_400_pick.json", dr.admin(PermBookingsRedispatch, "POST", "/bookings/"+b.String()+"/redispatch",
		fmt.Sprintf(`{"reason":"pick","pro_id":"%s"}`, fakePro2)), 400)
	assertFixture(t, "pro_offer_accept_409_taken.json", dr.call(proUser, "POST", "/offers/"+offer.String()+"/accept", ""), 409)

	// Not a professional: no duty, no token.
	mustCode(t, "stranger duty", dr.call(otherUser, "POST", "/duty/on", ""), 404, "DOORSTEP_PRO_NOT_FOUND")

	// No frame anywhere carries the address, an OTP or a phone number.
	for _, f := range dr.frames.frames {
		low := strings.ToLower(f)
		for _, bad := range []string{"cyber", "flat 4b", "500081", "otp", "phone", "line1", "landmark"} {
			if strings.Contains(low, bad) {
				t.Fatalf("frame carries %q: %s", bad, f)
			}
		}
	}
	if len(dr.frames.frames) == 0 {
		t.Fatal("no realtime frames published")
	}
}

// A professional not yet approved cannot go on duty.
func TestContract_DutyRefusedBeforeApproval(t *testing.T) {
	dr := newDispatchFixtureRig(t)
	mustStatus(t, "apply", dr.call(otherUser, "POST", "/apply", `{"display_name":"Ravi Kumar","city_code":"HYD"}`), 201)
	assertFixture(t, "pro_duty_on_403_not_approved.json", dr.call(otherUser, "POST", "/duty/on", ""), 403)
}

var _ = slots.IST

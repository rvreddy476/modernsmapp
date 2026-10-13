package itest

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/google/uuid"
)

func TestCustomerContactLimitedToAcceptedActiveJob(t *testing.T) {
	lookups := 0
	var expectedCustomer uuid.UUID
	br, booking, partner, customer := newVisitRig(t, func(deps *service.ProDeps) {
		deps.VerifiedPhone = func(_ context.Context, id uuid.UUID) (string, error) {
			lookups++
			if id != expectedCustomer {
				t.Fatal("wrong customer's contact requested")
			}
			return "+919876543210", nil
		}
	})
	expectedCustomer = customer
	path := "/pro/jobs/" + booking.String() + "/customer-contact"
	_, other := br.addPro(t, "female")
	for _, user := range []uuid.UUID{other, customer, uuid.New()} {
		code, body := br.user(user, "GET", path, "")
		want(t, "unrelated user denied", code, body, 404)
	}
	if lookups != 0 {
		t.Fatal("identity lookup before authorization")
	}
	for _, status := range []string{"assigned", "en_route", "arrived"} {
		br.exec(t, `UPDATE doorstep.bookings SET status=$2 WHERE id=$1`, booking, status)
		code, body := br.user(partner, "GET", path, "")
		want(t, "active contact", code, body, 200)
		var result model.ProCustomerContact
		data(t, body, &result)
		if result.Phone != "+919876543210" {
			t.Fatal("missing verified phone")
		}
	}
	// Enter and complete the visit through the real OTP/photo gates.
	facts, err := br.st.VisitFacts(context.Background(), booking, br.now)
	if err != nil {
		t.Fatal(err)
	}
	visitPhotos(t, br, partner, booking, "before", facts.MinBefore)
	code, body := br.user(customer, "GET", "/bookings/"+booking.String(), "")
	want(t, "start code", code, body, 200)
	var visit model.Booking
	data(t, body, &visit)
	visitCall(t, br, partner, booking, "start", fmt.Sprintf(`{"otp":%q}`, *visit.StartOTP), 200)
	code, body = br.user(partner, "GET", path, "")
	want(t, "in progress contact", code, body, 200)
	visitPhotos(t, br, partner, booking, "after", facts.MinAfter)
	visitCall(t, br, partner, booking, "finish", "", 200)
	code, body = br.user(customer, "GET", "/bookings/"+booking.String(), "")
	want(t, "end code", code, body, 200)
	data(t, body, &visit)
	br.exec(t, `UPDATE doorstep.bookings SET status='awaiting_extras_payment' WHERE id=$1`, booking)
	code, body = br.user(partner, "GET", path, "")
	want(t, "extras contact", code, body, 200)
	visitCall(t, br, partner, booking, "complete", fmt.Sprintf(`{"otp":%q}`, *visit.EndOTP), 200)
	code, body = br.user(partner, "GET", path, "")
	want(t, "completed contact denied", code, body, 404)
	br.exec(t, `UPDATE doorstep.booking_assignments SET status='accepted' WHERE booking_id=$1`, booking)
	for _, status := range []string{"matching", "pending_payment", "confirmed", "cancelled", "expired", "pro_unavailable", "pro_no_show", "customer_no_show"} {
		if status == "pro_unavailable" {
			br.exec(t, `UPDATE doorstep.bookings SET status=$2, reserved_pro_id=NULL, choice_deadline=now()+interval '15 minutes', unavailable_cause='no_professional' WHERE id=$1`, booking, status)
		} else {
			br.exec(t, `UPDATE doorstep.bookings SET status=$2 WHERE id=$1`, booking, status)
		}
		code, body := br.user(partner, "GET", path, "")
		want(t, "inactive contact denied", code, body, 404)
	}
	br.exec(t, `UPDATE doorstep.bookings SET status='assigned' WHERE id=$1`, booking)
	for _, status := range []string{"offered", "declined", "cancelled", "completed", "expired"} {
		br.exec(t, `UPDATE doorstep.booking_assignments SET status=$2 WHERE booking_id=$1`, booking, status)
		code, body := br.user(partner, "GET", path, "")
		want(t, "unaccepted or previous assignment denied", code, body, 404)
	}
	if lookups != 5 {
		t.Fatal("denied contact called identity service")
	}
	br.exec(t, `UPDATE doorstep.booking_assignments SET status='accepted' WHERE booking_id=$1`, booking)
	for _, suffix := range []string{"/pro/jobs", "/pro/jobs/" + booking.String(), "/pro/offers"} {
		code, body := br.user(partner, "GET", suffix, "")
		want(t, "safe job/offer response", code, body, 200)
		if strings.Contains(string(body), "9876543210") || strings.Contains(string(body), `"phone"`) {
			t.Fatal("phone leaked into job or offer")
		}
	}
	req := httptest.NewRequest("GET", "/v1/doorstep"+path, nil)
	req.Header.Set("X-User-Id", partner.String())
	req.Header.Set("X-Internal-Service-Key", itKey)
	w := httptest.NewRecorder()
	br.r.ServeHTTP(w, req)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("contact response cache control: %d %v", w.Code, w.Header())
	}
	br.exec(t, `UPDATE doorstep.professionals SET status='suspended' WHERE user_id=$1`, partner)
	code, body = br.user(partner, "GET", path, "")
	want(t, "suspended partner denied", code, body, 404)
}

package itest

import (
	"context"
	"fmt"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func newVisitRig(t *testing.T) (*bkRig, uuid.UUID, uuid.UUID, uuid.UUID) {
	br := newBookingRig(t, func(s *service.Service, st *store.Store, pii *propii.Crypto) *service.Service {
		return s.WithPro(service.ProDeps{Store: st, PII: pii, Media: ownAll{}, Images: ownAll{}}).WithDispatch(service.DispatchDeps{Store: st}).WithVisit(st).WithAftercare(st).WithAdminAftercare(st)
	})
	pro, user := br.addPro(t, "female")
	customer := uuid.New()
	q, a := br.quoteAndAddress(t, customer, false)
	code, body := br.book(customer, q, a, br.tomorrowAt(14, 0), uuid.NewString())
	want(t, "book", code, body, 201)
	var out bookingOut
	data(t, body, &out)
	b := out.Booking.ID
	br.captured(t, b, uuid.NewString(), nil)
	_ = pro
	offer := uuid.UUID(br.one(t, `SELECT id FROM doorstep.booking_assignments WHERE booking_id=$1 AND status='offered'`, b).([16]byte))
	code, body = br.user(user, "POST", "/pro/offers/"+offer.String()+"/accept", "")
	want(t, "accept", code, body, 200)
	return br, b, user, customer
}

func visitCall(t *testing.T, br *bkRig, u, b uuid.UUID, action, body string, status int) []byte {
	t.Helper()
	code, raw := br.user(u, "POST", "/pro/jobs/"+b.String()+"/"+action, body)
	want(t, action, code, raw, status)
	return raw
}

func visitPhotos(t *testing.T, br *bkRig, u, b uuid.UUID, phase string, n int) string {
	t.Helper()
	var media string
	for i := 0; i < n; i++ {
		media = uuid.NewString()
		visitCall(t, br, u, b, "photos", fmt.Sprintf(`{"phase":%q,"media_id":%q}`, phase, media), 201)
	}
	return media
}

func TestVisitJourneyOTPPhotosAndInvoice(t *testing.T) {
	br, b, u, c := newVisitRig(t)
	// Wrong professional and far-away arrival cannot advance the booking.
	visitCall(t, br, uuid.New(), b, "en-route", "", 404)
	visitCall(t, br, u, b, "en-route", "", 200)
	visitCall(t, br, u, b, "arrived", `{"lat":18,"lng":78}`, 422)
	f, err := br.st.VisitFacts(context.Background(), b, br.now)
	if err != nil {
		t.Fatal(err)
	}
	visitCall(t, br, u, b, "arrived", fmt.Sprintf(`{"lat":%f,"lng":%f}`, f.Lat, f.Lng), 200)
	code, raw := br.user(c, "GET", "/bookings/"+b.String(), "")
	want(t, "customer code", code, raw, 200)
	var booking model.Booking
	data(t, raw, &booking)
	if booking.StartOTP == nil {
		t.Fatal("customer has no start code")
	}
	start := *booking.StartOTP
	visitCall(t, br, u, b, "start", fmt.Sprintf(`{"otp":%q}`, start), 422)
	media := visitPhotos(t, br, u, b, "before", f.MinBefore)
	// A scoped photo cannot be fetched by another user.
	code, raw = br.user(uuid.New(), "GET", "/bookings/"+b.String()+"/photos/"+media, "")
	want(t, "photo privacy", code, raw, 404)
	// Five wrong codes count and lock; even the right code is then refused.
	wrong := "0000"
	if start == wrong {
		wrong = "9999"
	}
	for i := 0; i < 5; i++ {
		status := 422
		if i == 4 {
			status = 423
		}
		visitCall(t, br, u, b, "start", fmt.Sprintf(`{"otp":%q}`, wrong), status)
	}
	visitCall(t, br, u, b, "start", fmt.Sprintf(`{"otp":%q}`, start), 423)
	br.now = br.now.Add(15*time.Minute + time.Second)
	visitCall(t, br, u, b, "start", fmt.Sprintf(`{"otp":%q}`, start), 200)
	visitCall(t, br, u, b, "finish", "", 422)
	visitPhotos(t, br, u, b, "after", f.MinAfter)
	visitCall(t, br, u, b, "finish", "", 200)
	code, raw = br.user(c, "GET", "/bookings/"+b.String(), "")
	want(t, "end code", code, raw, 200)
	data(t, raw, &booking)
	if booking.EndOTP == nil {
		t.Fatal("finished paid visit has no end code")
	}
	visitCall(t, br, u, b, "complete", fmt.Sprintf(`{"otp":%q}`, *booking.EndOTP), 200)
	if got := br.one(t, `SELECT status FROM doorstep.bookings WHERE id=$1`, b); got != "completed" {
		t.Fatal(got)
	}
	if got := br.one(t, `SELECT count(*) FROM doorstep.earning_lines WHERE booking_id=$1`, b); got.(int64) != 2 {
		t.Fatal("earnings missing", got)
	}
	invoice := br.one(t, `SELECT invoice_snapshot::text FROM doorstep.bookings WHERE id=$1`, b).(string)
	if !strings.Contains(invoice, "HOME_CLEANING_REGISTERED") {
		t.Fatal("wrong completion invoice", invoice)
	}
}

func TestVisitResetDropsCodesAndOldExtras(t *testing.T) {
	br, b, u, _ := newVisitRig(t)
	visitCall(t, br, u, b, "en-route", "", 200)
	f, err := br.st.VisitFacts(context.Background(), b, br.now)
	if err != nil {
		t.Fatal(err)
	}
	visitCall(t, br, u, b, "arrived", fmt.Sprintf(`{"lat":%f,"lng":%f}`, f.Lat, f.Lng), 200)
	if err := br.st.EnsureOTP(context.Background(), b, "start", "testhash", []byte("sealed"), br.now); err != nil {
		t.Fatal(err)
	}
	_, err = br.st.MakeProUnavailable(context.Background(), store.Unavailable{BookingID: b, From: []string{"arrived"}, Cause: "pro_cancel", ActorKind: "pro", ActorID: &u, Deadline: br.now.Add(30 * time.Minute), At: br.now})
	if err != nil {
		t.Fatal(err)
	}
	if got := br.one(t, `SELECT count(*) FROM doorstep.booking_otps WHERE booking_id=$1`, b); got.(int64) != 0 {
		t.Fatal("old OTP survived", got)
	}
	if err := br.st.EnsureOTP(context.Background(), b, "start", "testhash", []byte("sealed"), br.now); err == nil {
		t.Fatal("code issued without an accepted professional")
	}
}

package itest

import (
	"context"
	"fmt"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"testing"
	"time"
)

func startVisitForExtras(t *testing.T) (*bkRig, uuid.UUID, uuid.UUID, uuid.UUID, *store.VisitFacts) {
	br, b, u, c := newVisitRig(t)
	visitCall(t, br, u, b, "en-route", "", 200)
	f, e := br.st.VisitFacts(context.Background(), b, br.now)
	if e != nil {
		t.Fatal(e)
	}
	visitCall(t, br, u, b, "arrived", fmt.Sprintf(`{"lat":%f,"lng":%f}`, f.Lat, f.Lng), 200)
	visitPhotos(t, br, u, b, "before", f.MinBefore)
	code, raw := br.user(c, "GET", "/bookings/"+b.String(), "")
	want(t, "start code", code, raw, 200)
	var booking model.Booking
	data(t, raw, &booking)
	visitCall(t, br, u, b, "start", fmt.Sprintf(`{"otp":%q}`, *booking.StartOTP), 200)
	return br, b, u, c, f
}
func TestVisitExtrasSplitThresholdAndSignedPayment(t *testing.T) {
	br, b, u, c, f := startVisitForExtras(t)
	card := uuid.New()
	br.exec(t, `INSERT INTO doorstep.rate_cards(id,city_code,category_id,code,name,unit,price_paise,max_quantity,active) VALUES($1,'HYD',$2,'extra_cleaning','Extra cleaning','per_item',200000,2,true)`, card, f.CategoryID)
	for i := 0; i < 2; i++ {
		raw := visitCall(t, br, u, b, "extras", fmt.Sprintf(`{"rate_card_id":%q,"quantity":1,"evidence_media_id":%q}`, card, uuid.New()), 201)
		var e model.Extra
		data(t, raw, &e)
		code, raw := br.user(c, "POST", "/bookings/"+b.String()+"/extras/"+e.ID.String()+"/approve", "")
		want(t, "approve", code, raw, 200)
	}
	if br.one(t, `SELECT count(*) FROM doorstep.extras_bills WHERE booking_id=$1`, b).(int64) != 1 {
		t.Fatal("split approval bypassed payment threshold")
	}
	bill := uuid.UUID(br.one(t, `SELECT id FROM doorstep.extras_bills WHERE booking_id=$1`, b).([16]byte))
	code, raw := br.user(c, "POST", "/extras-bills/"+bill.String()+"/payment/intent", "")
	want(t, "extras intent", code, raw, 200)
	intent := br.pay.intents[payments.ExtrasIntentKey(bill)]
	if intent == nil {
		t.Fatal("no extras intent")
	}
	frame := map[string]any{"id": intent.ID.String(), "payer_id": c.String(), "payee_id": payments.PayeeID.String(), "reference_type": payments.RefExtras, "reference_id": bill.String(), "amount_minor": intent.AmountMinor - 1, "currency": "INR", "status": "succeeded", "application_id": payments.ApplicationID}
	br.event(t, uuid.NewString(), events.EventPaymentSucceeded, frame)
	if br.one(t, `SELECT status FROM doorstep.extras_bills WHERE id=$1`, bill) != "payment_pending" {
		t.Fatal("mismatched capture paid bill")
	}
	frame["amount_minor"] = intent.AmountMinor
	br.event(t, uuid.NewString(), events.EventPaymentSucceeded, frame)
	if br.one(t, `SELECT status FROM doorstep.extras_bills WHERE id=$1`, bill) != "paid" {
		t.Fatal("signed capture did not pay bill")
	}
	visitPhotos(t, br, u, b, "after", f.MinAfter)
	visitCall(t, br, u, b, "finish", "", 200)
	code, raw = br.user(c, "GET", "/bookings/"+b.String(), "")
	want(t, "end code", code, raw, 200)
	var booking model.Booking
	data(t, raw, &booking)
	visitCall(t, br, u, b, "complete", fmt.Sprintf(`{"otp":%q}`, *booking.EndOTP), 200)
	code, raw = br.user(c, "POST", "/bookings/"+b.String()+"/rating", `{"stars":5,"tags":[],"comment":"Excellent"}`)
	want(t, "rate", code, raw, 201)
	code, raw = br.user(c, "POST", "/bookings/"+b.String()+"/rating", `{"stars":5}`)
	want(t, "duplicate rating", code, raw, 409)
	br.exec(t, `UPDATE doorstep.services SET rework_days=7 WHERE id=$1`, f.ServiceID)
	slot := br.tomorrowAt(16, 0)
	code, raw = br.user(c, "POST", "/bookings/"+b.String()+"/rework", fmt.Sprintf(`{"reason":"One area needs redoing","slot_start":%q}`, slot.Format(time.RFC3339)))
	want(t, "rework", code, raw, 201)
	var rework model.ReworkRequest
	data(t, raw, &rework)
	if rework.ChildBookingID == nil {
		t.Fatal("rework has no child booking")
	}
	child := *rework.ChildBookingID
	if br.one(t, `SELECT total_paise FROM doorstep.bookings WHERE id=$1`, child).(int64) != 0 {
		t.Fatal("rework charged")
	}
	if br.one(t, `SELECT count(*) FROM doorstep.payments WHERE booking_id=$1`, child).(int64) != 0 {
		t.Fatal("rework opened payment")
	}
	if br.one(t, `SELECT count(*) FROM doorstep.pro_calendar_blocks WHERE booking_id=$1 AND active`, child).(int64) != 1 {
		t.Fatal("rework did not reserve slot")
	}
}

func TestExtraWithdrawDecisionGuard(t *testing.T) {
	br, b, u, c, f := startVisitForExtras(t)
	card := uuid.New()
	br.exec(t, `INSERT INTO doorstep.rate_cards(id,city_code,category_id,code,name,unit,price_paise,max_quantity,active) VALUES($1,'HYD',$2,'withdraw_extra','Extra cleaning','per_item',10000,2,true)`, card, f.CategoryID)
	propose := func() model.Extra {
		raw := visitCall(t, br, u, b, "extras", fmt.Sprintf(`{"rate_card_id":%q,"quantity":1,"evidence_media_id":%q}`, card, uuid.New()), 201)
		var e model.Extra
		data(t, raw, &e)
		return e
	}
	e := propose()
	path := "/pro/jobs/" + b.String() + "/extras/" + e.ID.String()
	code, raw := br.user(uuid.New(), "DELETE", path, "")
	want(t, "outsider withdraw", code, raw, 404)
	code, raw = br.user(u, "DELETE", path, "")
	want(t, "withdraw", code, raw, 204)
	if br.one(t, `SELECT status FROM doorstep.booking_extras WHERE id=$1`, e.ID) != "withdrawn" {
		t.Fatal("withdraw not recorded")
	}
	e = propose()
	code, raw = br.user(c, "POST", "/bookings/"+b.String()+"/extras/"+e.ID.String()+"/approve", "")
	want(t, "approve", code, raw, 200)
	code, raw = br.user(u, "DELETE", "/pro/jobs/"+b.String()+"/extras/"+e.ID.String(), "")
	want(t, "approved extra cannot be withdrawn", code, raw, 409)
	code, raw = br.user(u, "DELETE", "/pro/jobs/"+b.String()+"/extras/not-a-uuid", "")
	want(t, "invalid extra id", code, raw, 400)
}

func TestVisitOutstandingOnlyAfterGracePeriod(t *testing.T) {
	br, b, u, c, f := startVisitForExtras(t)
	card := uuid.New()
	br.exec(t, `INSERT INTO doorstep.rate_cards(id,city_code,category_id,code,name,unit,price_paise,max_quantity,active) VALUES($1,'HYD',$2,'unpaid_extra','Extra cleaning','per_item',10000,2,true)`, card, f.CategoryID)
	raw := visitCall(t, br, u, b, "extras", fmt.Sprintf(`{"rate_card_id":%q,"quantity":1,"evidence_media_id":%q}`, card, uuid.New()), 201)
	var e model.Extra
	data(t, raw, &e)
	code, raw := br.user(c, "POST", "/bookings/"+b.String()+"/extras/"+e.ID.String()+"/approve", "")
	want(t, "approve", code, raw, 200)
	visitPhotos(t, br, u, b, "after", f.MinAfter)
	visitCall(t, br, u, b, "finish", "", 200)
	if _, err := br.st.ExpireVisitBills(context.Background(), br.now.Add(14*time.Minute), 100); err != nil {
		t.Fatal(err)
	}
	if br.one(t, `SELECT count(*) FROM doorstep.outstanding WHERE booking_id=$1`, b).(int64) != 0 {
		t.Fatal("early outstanding")
	}
	br.now = br.now.Add(15 * time.Minute)
	if _, err := br.st.ExpireVisitBills(context.Background(), br.now, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := br.st.ExpireVisitBills(context.Background(), br.now, 100); err != nil {
		t.Fatal(err)
	}
	if br.one(t, `SELECT count(*) FROM doorstep.outstanding WHERE booking_id=$1 AND status='open'`, b).(int64) != 1 {
		t.Fatal("missing or duplicate outstanding")
	}
	code, raw = br.user(c, "GET", "/bookings/"+b.String(), "")
	want(t, "end code after outstanding", code, raw, 200)
	var booking model.Booking
	data(t, raw, &booking)
	if booking.EndOTP == nil {
		t.Fatal("outstanding visit cannot complete")
	}
	visitCall(t, br, u, b, "complete", fmt.Sprintf(`{"otp":%q}`, *booking.EndOTP), 200)
	code, raw = br.user(c, "GET", "/me/outstanding", "")
	want(t, "outstanding after completion", code, raw, 200)
}

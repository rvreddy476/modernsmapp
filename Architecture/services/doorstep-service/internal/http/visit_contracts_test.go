package http

import (
	"bytes"
	"context"
	"fmt"
	"github.com/atpost/doorstep-service/internal/config"
	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"testing"
	"time"
)

func fixtureVisitPII(t *testing.T) *propii.Crypto {
	v, e := propii.New(context.Background(), []config.PIIKey{{Version: 1, Key: bytes.Repeat([]byte{0x24}, 32)}})
	if e != nil {
		t.Fatal(e)
	}
	return v
}

// Real handlers and service decisions, deterministic store responses.
// Database behavior is separately exercised in internal/itest.
type visitFixtureStore struct {
	service.VisitStore
	service.AftercareStore
	service.AdminAftercareStore
	facts    *store.VisitFacts
	dr       *dispatchRig
	extras   []model.Extra
	messages []model.Message
	rating   *model.Rating
	reworks  []model.ReworkRequest
	contact  *model.TrustedContact
	bill     model.ExtrasBill
	ticket   *model.Ticket
	incident *model.Incident
	tax      *model.TaxRegistration
}

func (v *visitFixtureStore) TaxRegistration(_ context.Context, id uuid.UUID) (*model.TaxRegistration, error) {
	if v.tax == nil {
		v.tax = &model.TaxRegistration{ProID: id, UpdatedAt: fixtureNow}
	}
	return v.tax, nil
}
func (v *visitFixtureStore) SetTaxRegistration(_ context.Context, _ store.Actor, id uuid.UUID, gst *string, _ string, at time.Time) (*model.TaxRegistration, error) {
	v.tax = &model.TaxRegistration{ProID: id, GSTIN: gst, UpdatedAt: at}
	return v.tax, nil
}
func (v *visitFixtureStore) WithdrawVisitExtra(_ context.Context, _ uuid.UUID, id uuid.UUID, _ time.Time, g func(*store.VisitFacts) error) error {
	if err := g(v.facts); err != nil {
		return err
	}
	for i := range v.extras {
		if v.extras[i].ID == id && v.extras[i].Status == "proposed" {
			v.extras[i].Status = "withdrawn"
			return nil
		}
	}
	return store.ErrStale
}

func vfID(name string) uuid.UUID { return devseed.ID("visit-fixture", name) }
func (v *visitFixtureStore) VisitFacts(context.Context, uuid.UUID, time.Time) (*store.VisitFacts, error) {
	return v.facts, nil
}
func (v *visitFixtureStore) setStatus(status string) {
	v.facts.Status = status
	v.dr.bk.bookings[v.facts.BookingID].status = status
}
func (v *visitFixtureStore) MoveVisit(_ context.Context, in store.StepInput, g func(*store.VisitFacts) error) (*store.VisitFacts, error) {
	if e := g(v.facts); e != nil {
		return nil, e
	}
	v.setStatus(in.To)
	if in.To == "arrived" {
		v.facts.ArrivedAt = &in.At
	}
	return v.facts, nil
}
func (v *visitFixtureStore) AddPhoto(_ context.Context, in store.PhotoWrite, g func(*store.VisitFacts) error) (*model.Photo, error) {
	if e := g(v.facts); e != nil {
		return nil, e
	}
	switch in.Phase {
	case "before":
		v.facts.Photos.Before++
	case "after":
		v.facts.Photos.After++
	case "kit_seal":
		v.facts.Photos.KitSeal++
	}
	return &model.Photo{ID: in.ID, BookingID: in.BookingID, Phase: in.Phase, MediaID: in.MediaID, CreatedAt: in.At}, nil
}
func (v *visitFixtureStore) StartVisit(_ context.Context, in store.OTPStep, g func(*store.VisitFacts) error) (*store.VisitFacts, error) {
	if e := g(v.facts); e != nil {
		return nil, e
	}
	v.setStatus("in_progress")
	v.facts.StartedAt = &in.At
	return v.facts, nil
}
func (v *visitFixtureStore) FinishVisit(_ context.Context, in store.FinishInput, g func(*store.VisitFacts) error) (*store.FinishResult, error) {
	if e := g(v.facts); e != nil {
		return nil, e
	}
	v.facts.FinishedAt = &in.At
	v.setStatus("awaiting_extras_payment")
	v.facts.UnpaidBills = 1
	due := in.At.Add(15 * time.Minute)
	v.bill.DueAt = &due
	return &store.FinishResult{Facts: v.facts, Status: v.facts.Status}, nil
}
func (v *visitFixtureStore) CompleteVisit(_ context.Context, in store.OTPStep, _ store.CompleteSpec, g func(*store.VisitFacts) error) (*store.VisitFacts, error) {
	if e := g(v.facts); e != nil {
		return nil, e
	}
	v.setStatus("completed")
	v.facts.CompletedAt = &in.At
	return v.facts, nil
}
func (v *visitFixtureStore) MaxCancellationFee(context.Context, string, uuid.UUID) (int64, error) {
	return 14900, nil
}
func (v *visitFixtureStore) CustomerNoShow(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ time.Time, g func(*store.VisitFacts) (*store.NoShowDecision, error)) ([]uuid.UUID, error) {
	if _, e := g(v.facts); e != nil {
		return nil, e
	}
	v.setStatus("customer_no_show")
	return nil, nil
}
func (v *visitFixtureStore) VisitExtraOptions(context.Context, uuid.UUID, time.Time) ([]store.VisitExtraOption, error) {
	id := vfID("rate")
	return []store.VisitExtraOption{{ExtraOption: model.ExtraOption{Kind: "rate_card", RateCardID: &id, Name: "Extra cleaning", UnitPricePaise: 14900, MaxQuantity: 2}, Unit: "per_item"}}, nil
}
func (v *visitFixtureStore) ProposeVisitExtra(ctx context.Context, in store.NewExtra, g func(*store.VisitFacts, store.VisitExtraOption) (int64, int64, error)) (*model.Extra, error) {
	options, _ := v.VisitExtraOptions(ctx, in.BookingID, in.At)
	if _, _, e := g(v.facts, options[0]); e != nil {
		return nil, e
	}
	o := options[0]
	x := model.Extra{ID: in.ID, BookingID: in.BookingID, Kind: o.Kind, RateCardID: o.RateCardID, Name: o.Name, Quantity: *in.Input.Quantity, UnitPricePaise: o.UnitPricePaise, TotalPaise: o.UnitPricePaise * int64(*in.Input.Quantity), Status: "proposed", EvidenceMediaID: in.Input.EvidenceMediaID, CreatedAt: in.At}
	v.extras = append(v.extras, x)
	return &x, nil
}
func (v *visitFixtureStore) DecideVisitExtra(_ context.Context, _ uuid.UUID, id uuid.UUID, status string, _ time.Time, _ uuid.UUID, _ uuid.UUID, _ string, g func(*store.VisitFacts) error) (*model.Extra, error) {
	if e := g(v.facts); e != nil {
		return nil, e
	}
	for i := range v.extras {
		if v.extras[i].ID == id {
			v.extras[i].Status = status
			return &v.extras[i], nil
		}
	}
	return nil, store.ErrNotFound
}
func (v *visitFixtureStore) VisitBill(context.Context, uuid.UUID) (*model.ExtrasBill, error) {
	return &v.bill, nil
}
func (v *visitFixtureStore) VisitBillPayment(context.Context, uuid.UUID, uuid.UUID) (*store.PaymentRow, error) {
	return &v.dr.bk.bookings[v.facts.BookingID].extra[0], nil
}
func (v *visitFixtureStore) VisitOutstanding(context.Context, uuid.UUID) (*model.Outstanding, error) {
	return &model.Outstanding{Bills: []model.ExtrasBill{}}, nil
}
func (v *visitFixtureStore) RateVisit(_ context.Context, id, u uuid.UUID, kind string, in model.RatingInput, at time.Time, g func(*store.VisitFacts) error) (*model.Rating, error) {
	if e := g(v.facts); e != nil {
		return nil, e
	}
	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}
	v.rating = &model.Rating{ID: vfID("rating"), BookingID: id, RaterKind: kind, Stars: *in.Stars, Tags: tags, Comment: in.Comment, CreatedAt: at}
	return v.rating, nil
}
func (v *visitFixtureStore) VisitMessages(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string, _ *uuid.UUID, at time.Time, g func(*store.VisitFacts) error) (*model.MessagePage, error) {
	if e := g(v.facts); e != nil {
		return nil, e
	}
	items := v.messages
	if items == nil {
		items = []model.Message{}
	}
	return &model.MessagePage{Items: items, Open: true}, nil
}
func (v *visitFixtureStore) SendVisitMessage(_ context.Context, id, u uuid.UUID, kind, body string, at time.Time, g func(*store.VisitFacts) error) (*model.Message, error) {
	if e := g(v.facts); e != nil {
		return nil, e
	}
	m := model.Message{ID: vfID(kind + "-message"), BookingID: id, SenderKind: kind, Body: body, CreatedAt: at}
	v.messages = append(v.messages, m)
	return &m, nil
}
func (v *visitFixtureStore) SaveVisitShare(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string, _ time.Time, _ time.Time, g func(*store.VisitFacts) error) error {
	return g(v.facts)
}
func (v *visitFixtureStore) SaveTrustedContact(_ context.Context, _ uuid.UUID, name string, _ []byte, last string, at time.Time) (*model.TrustedContact, error) {
	v.contact = &model.TrustedContact{Name: name, PhoneMasked: "••••••" + last, UpdatedAt: at}
	return v.contact, nil
}
func (v *visitFixtureStore) TrustedContact(context.Context, uuid.UUID) (*model.TrustedContact, error) {
	return v.contact, nil
}
func (v *visitFixtureStore) RaiseVisitIncident(_ context.Context, id, u uuid.UUID, kind, action string, in model.SOSInput, at time.Time, g func(*store.VisitFacts) error) (*store.IncidentResult, error) {
	if e := g(v.facts); e != nil {
		return nil, e
	}
	return &store.IncidentResult{Incident: &model.Incident{ID: vfID(action + kind), BookingID: &id, RaisedByKind: kind, Kind: action, Severity: "critical", Status: "open", Description: in.Note, CreatedAt: at}}, nil
}
func (v *visitFixtureStore) VisitReworks(context.Context, uuid.UUID, uuid.UUID) ([]model.ReworkRequest, error) {
	return v.reworks, nil
}
func (v *visitFixtureStore) RequestVisitRework(_ context.Context, in store.ReworkSpec, g func(*store.VisitFacts) error) (*model.ReworkRequest, error) {
	if e := g(v.facts); e != nil {
		return nil, e
	}
	r := model.ReworkRequest{ID: in.ID, BookingID: in.BookingID, Status: "requested", Reason: in.Reason, CreatedAt: in.At}
	v.reworks = append(v.reworks, r)
	return &r, nil
}
func (v *visitFixtureStore) VisitEarnings(context.Context, uuid.UUID, time.Time, time.Time) (*model.Earnings, error) {
	return &model.Earnings{TotalPaise: 40000, Lines: []model.EarningLine{{ID: vfID("earning"), BookingID: &v.facts.BookingID, Kind: "job", AmountPaise: 50000, CreatedAt: fixtureNow}, {ID: vfID("commission"), BookingID: &v.facts.BookingID, Kind: "commission", AmountPaise: -10000, CreatedAt: fixtureNow}}}, nil
}

func (v *visitFixtureStore) OpenVisitTicket(_ context.Context, _ uuid.UUID, _ string, in model.TicketInput, at time.Time) (*model.Ticket, error) {
	v.ticket = &model.Ticket{ID: vfID("ticket"), BookingID: in.BookingID, Category: *in.Category, Subject: *in.Subject, Body: *in.Body, Status: "open", CreatedAt: at, UpdatedAt: at}
	return v.ticket, nil
}
func (v *visitFixtureStore) VisitTickets(context.Context, uuid.UUID) ([]model.Ticket, error) {
	return []model.Ticket{*v.ticket}, nil
}
func (v *visitFixtureStore) VisitTicket(context.Context, uuid.UUID, uuid.UUID) (*model.Ticket, error) {
	return v.ticket, nil
}
func (v *visitFixtureStore) AdminCareList(_ context.Context, kind, _, _ string) ([]any, error) {
	switch kind {
	case "incidents":
		return []any{*v.incident}, nil
	case "tickets":
		return []any{*v.ticket}, nil
	case "ratings":
		return []any{*v.rating}, nil
	case "settlements":
		return []any{store.Settlement{ID: vfID("settlement"), ProID: lakshmiPro, PeriodStart: "2026-10-03", PeriodEnd: "2026-10-04", Gross: 50000, Commission: 10000, Net: 40000, Status: "computed"}}, nil
	}
	return nil, store.ErrInvalid
}
func (v *visitFixtureStore) AdminIncidentDecision(_ context.Context, _ store.Actor, _ uuid.UUID, status string, _ *string, _ bool, _ time.Time) (*model.Incident, error) {
	v.incident.Status = status
	return v.incident, nil
}
func (v *visitFixtureStore) AdminTicketStatus(_ context.Context, _ store.Actor, _ uuid.UUID, status string, _ *string, _ time.Time) (*model.Ticket, error) {
	v.ticket.Status = status
	return v.ticket, nil
}
func (v *visitFixtureStore) AdminHideVisitRating(_ context.Context, _ store.Actor, _ uuid.UUID, _ string, _ time.Time) (*model.Rating, error) {
	v.rating.Hidden = true
	return v.rating, nil
}

type visitBookingFixture struct {
	*fakeBookingStore
	v *visitFixtureStore
}

func (b *visitBookingFixture) BookingExtras(context.Context, uuid.UUID) ([]model.Extra, error) {
	return b.v.extras, nil
}

type visitDispatchFixture struct {
	*fakeDispatchStore
	v *visitFixtureStore
}

func (d *visitDispatchFixture) ProJobRecord(ctx context.Context, p, id uuid.UUID, at time.Time) (*store.ProJobRecord, error) {
	r, e := d.fakeDispatchStore.ProJobRecord(ctx, p, id, at)
	if e == nil {
		r.Uploaded = d.v.facts.Photos
		r.ArrivedAt = d.v.facts.ArrivedAt
		r.FinishedAt = d.v.facts.FinishedAt
		r.CompletedAt = d.v.facts.CompletedAt
	}
	return r, e
}

func TestContract_VisitAndAftercare(t *testing.T) {
	dr := newDispatchFixtureRig(t)
	b := dr.paidBooking(t)
	mustStatus(t, "accept", dr.call(proUser, "POST", "/offers/"+dr.ds.assigns[0].id.String()+"/accept", ""), 200)
	rec, e := dr.bk.BookingRecord(context.Background(), b, nil)
	if e != nil {
		t.Fatal(e)
	}
	v := &visitFixtureStore{dr: dr, facts: &store.VisitFacts{BookingID: b, Customer: fixtureUser, Status: "assigned", CityCode: "HYD", Family: "HOME_CLEANING", StateCode: "36", ServiceID: rec.Booking.ServiceID, CategoryID: rec.CategoryID, Lat: inZoneLat, Lng: inZoneLng, MinBefore: 2, MinAfter: 2, ReworkDays: 7, CommissionBPS: 2000, TotalPaise: rec.Booking.TotalPaise, Pro: &store.VisitPro{ProID: lakshmiPro, UserID: proUser, Status: "accepted", AccountStatus: "approved"}}}
	// Retain the real dependencies; only the persistence seams are replaced.
	n := 0
	dr.svc.WithVisit(v).WithAftercare(v).WithAdminAftercare(v)
	// Extras reads use the same booking seam as customer booking details.
	deps := service.BookingDeps{Store: &visitBookingFixture{dr.bk, v}, NewID: func() uuid.UUID { n++; return vfID(fmt.Sprint(n)) }}
	// These routes never reopen a booking payment or an address. A pre-opened
	// extras intent tests the public response without a second payment fake.
	dr.svc.WithBookings(deps).WithDispatch(service.DispatchDeps{Store: &visitDispatchFixture{dr.ds, v}, Realtime: dr.frames, Signer: fixtureSigner{}})
	billID := vfID("bill")
	intentID := "fixture-extras-intent"
	p := store.PaymentRow{ID: vfID("payment"), BookingID: b, ReferenceID: billID, ReferenceType: "doorstep_extras", AmountPaise: 14900, Status: "pending", IntentID: &intentID, Checkout: map[string]string{"provider": "stub", "order_id": "fixture-order"}}
	dr.bk.bookings[b].extra = append(dr.bk.bookings[b].extra, p)
	v.bill = model.ExtrasBill{ID: billID, BookingID: b, AmountPaise: 14900, TaxablePaise: 14190, TaxPaise: 710, Status: "payment_pending"}
	// proJob decrypts its address: keep the existing address keyring.
	// Wired explicitly below by the rig's existing sealed address fixture.
	dr.svc.WithBookings(service.BookingDeps{Store: &visitBookingFixture{dr.bk, v}, NewID: deps.NewID, PII: fixtureVisitPII(t)})
	cust := func(name, method, path, body string, status int) {
		assertFixture(t, name+".json", dr.customer(fixtureUser, method, path, body), status)
	}
	for id := range dr.store.quotes {
		cust("quote_get_200", "GET", "/quotes/"+id.String(), "", 200)
		break
	}
	pro := func(name, method, path, body string, status int) {
		assertFixture(t, name+".json", dr.call(proUser, method, path, body), status)
	}
	bp := "/bookings/" + b.String()
	jp := "/jobs/" + b.String()
	cust("share_post_201", "POST", bp+"/share", "", 201)
	cust("trusted_contact_get_200_empty", "GET", "/trusted-contact", "", 200)
	cust("trusted_contact_put_200", "PUT", "/trusted-contact", `{"name":"Sister","phone":"+919876543210"}`, 200)
	cust("trusted_contact_get_200", "GET", "/trusted-contact", "", 200)
	cust("message_post_201", "POST", bp+"/messages", `{"body":"Please ring the bell."}`, 201)
	cust("messages_get_200", "GET", bp+"/messages", "", 200)
	pro("pro_message_post_201", "POST", jp+"/messages", `{"body":"On my way."}`, 201)
	pro("pro_messages_get_200", "GET", jp+"/messages", "", 200)
	cust("sos_post_201", "POST", bp+"/sos", `{"note":"Please contact me"}`, 201)
	pro("pro_sos_201", "POST", jp+"/sos", `{"note":"Need help"}`, 201)
	pro("pro_en_route_200", "POST", jp+"/en-route", "", 200)
	pro("pro_arrived_200", "POST", jp+"/arrived", fmt.Sprintf(`{"lat":%f,"lng":%f}`, inZoneLat, inZoneLng), 200)
	pro("pro_start_422_photos", "POST", jp+"/start", `{"otp":"1234"}`, 422)
	pro("pro_start_400_otp", "POST", jp+"/start", `{"otp":"abc"}`, 400)
	photo := fmt.Sprintf(`{"phase":"before","media_id":%q}`, mediaOf(proUser, "photo"))
	pro("pro_photo_201", "POST", jp+"/photos", photo, 201)
	v.facts.Photos.Before = 2
	pro("pro_start_200", "POST", jp+"/start", `{"otp":"1234"}`, 200)
	pro("pro_extra_options_get_200", "GET", jp+"/extras/options", "", 200)
	pro("pro_extra_post_201", "POST", jp+"/extras", fmt.Sprintf(`{"rate_card_id":%q,"quantity":1,"evidence_media_id":%q}`, vfID("rate"), mediaOf(proUser, "photo")), 201)
	ex := v.extras[0].ID.String()
	cust("extras_get_200", "GET", bp+"/extras", "", 200)
	pro("pro_extras_get_200", "GET", jp+"/extras", "", 200)
	cust("extra_approve_post_200", "POST", bp+"/extras/"+ex+"/approve", "", 200)
	v.extras[0].Status = "proposed"
	cust("extra_decline_post_200", "POST", bp+"/extras/"+ex+"/decline", "", 200)
	v.facts.Photos.After = 2
	pro("pro_finish_200", "POST", jp+"/finish", "", 200)
	pro("pro_complete_409_extras", "POST", jp+"/complete", `{"otp":"1234"}`, 409)
	cust("extras_bill_get_200", "GET", bp+"/extras-bill", "", 200)
	cust("extras_payment_intent_post_200", "POST", "/extras-bills/"+billID.String()+"/payment/intent", "", 200)
	cust("outstanding_get_200", "GET", "/me/outstanding", "", 200)
	v.facts.UnpaidBills = 0
	pro("pro_complete_200", "POST", jp+"/complete", `{"otp":"1234"}`, 200)
	cust("rating_post_201", "POST", bp+"/rating", `{"stars":5,"tags":["Clean work"],"comment":"Excellent"}`, 201)
	pro("pro_rating_201", "POST", jp+"/rating", `{"stars":5,"tags":[]}`, 201)
	cust("rework_post_201", "POST", bp+"/rework", `{"reason":"Please redo the sink area"}`, 201)
	cust("rework_get_200", "GET", bp+"/rework", "", 200)
	pro("pro_earnings_get_200", "GET", "/earnings", "", 200)
	cust("ticket_post_201", "POST", "/tickets", fmt.Sprintf(`{"booking_id":%q,"category":"quality","subject":"Please review the visit","body":"The sink area needs another look."}`, b), 201)
	cust("tickets_get_200", "GET", "/tickets", "", 200)
	cust("ticket_get_200", "GET", "/tickets/"+v.ticket.ID.String(), "", 200)
	v.incident = &model.Incident{ID: vfID("incident"), BookingID: &b, RaisedByKind: "customer", Kind: "sos", Severity: "critical", Status: "open", CreatedAt: fixtureNow}
	admin := func(name, perm, method, path, body string, status int) {
		assertFixture(t, name+".json", dr.admin(perm, method, path, body), status)
	}
	admin("admin_incidents_get_200", PermIncidentsRead, "GET", "/incidents", "", 200)
	admin("admin_incident_ack_200", PermIncidentsAct, "POST", "/incidents/"+v.incident.ID.String()+"/acknowledge", "", 200)
	admin("admin_incident_resolve_200", PermIncidentsAct, "POST", "/incidents/"+v.incident.ID.String()+"/resolve", `{"resolution":"Verified safe with both participants","lift_suspension":false}`, 200)
	admin("admin_tickets_get_200", PermTicketsAct, "GET", "/tickets", "", 200)
	admin("admin_ticket_status_200", PermTicketsAct, "POST", "/tickets/"+v.ticket.ID.String()+"/status", `{"status":"in_progress","note":"Reviewing the visit details"}`, 200)
	admin("admin_ratings_get_200", PermRatingsModerate, "GET", "/ratings", "", 200)
	admin("admin_rating_hide_200", PermRatingsModerate, "POST", "/ratings/"+v.rating.ID.String()+"/hide", `{"reason":"Confirmed duplicate content during review"}`, 200)
	admin("admin_settlements_get_200", PermSettlementsRead, "GET", "/settlements", "", 200)
	admin("admin_incidents_403_scope", PermProsRead, "GET", "/incidents", "", 403)
	taxPath := "/professionals/" + lakshmiPro.String() + "/tax-registration"
	admin("admin_tax_registration_get_200", PermProsApprove, "GET", taxPath, "", 200)
	admin("admin_tax_registration_post_200", PermProsApprove, "POST", taxPath, `{"gstin":"29ZZZPZ0000Z1Z6","verified":true,"reason":"Registration verified against reviewed documents"}`, 200)
	admin("admin_tax_registration_403_scope", PermProsRead, "GET", taxPath, "", 403)
	admin("admin_tax_registration_400_unverified", PermProsApprove, "POST", taxPath, `{"gstin":"29ZZZPZ0000Z1Z6","reason":"Registration verified against reviewed documents"}`, 400)
	v.setStatus("arrived")
	arrived := fixtureNow.Add(-15 * time.Minute)
	v.facts.ArrivedAt = &arrived
	pro("pro_unsafe_exit_201", "POST", jp+"/unsafe-exit", `{"note":"I feel unsafe"}`, 201)
	pro("pro_no_show_200", "POST", jp+"/no-show", "", 200)
}

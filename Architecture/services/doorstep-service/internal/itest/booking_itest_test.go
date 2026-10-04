package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/doorstep-service/internal/config"
	doorstephttp "github.com/atpost/doorstep-service/internal/http"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentsclient"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Bookings and payments (A3) on the real store: the calendar exclusion
// constraint decides holds, a booking is paid only from the signed event
// applied once, late captures are confirmed or refunded, refunds carry one
// key per cause.

// fakePayments is payments-service for the suite: intents keyed by the
// idempotency key, refunds keyed too (a resubmission with the same key is
// the same command), and switches to fail.
type fakePayments struct {
	mu          sync.Mutex
	intents     map[string]*paymentsclient.Intent // by idempotency key
	byID        map[uuid.UUID]*paymentsclient.Intent
	refunds     map[string]uuid.UUID // key -> command
	refundCalls []string
	failRefund  error
	provider    string
}

func newFakePayments() *fakePayments {
	return &fakePayments{intents: map[string]*paymentsclient.Intent{}, byID: map[uuid.UUID]*paymentsclient.Intent{},
		refunds: map[string]uuid.UUID{}, provider: payments.StubProvider}
}

func (f *fakePayments) CreateIntent(_ context.Context, in paymentsclient.CreateIntentRequest) (*paymentsclient.Intent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i, ok := f.intents[in.IdempotencyKey]; ok {
		return i, nil
	}
	i := &paymentsclient.Intent{ID: uuid.New(), Status: "pending", AmountMinor: in.AmountMinor, Currency: in.Currency,
		ReferenceType: payments.RefBooking, ReferenceID: in.ReferenceID, PayerID: in.PayerID, PayeeID: in.PayeeID,
		ProviderRef: "order_stub_" + in.ReferenceID.String()[:8], ApplicationID: in.ApplicationID,
		ClientSession: &paymentsclient.ClientSession{Provider: f.provider, OrderID: "order_stub_" + in.ReferenceID.String()[:8],
			MerchantDisplayName: "Doorstep"}}
	f.intents[in.IdempotencyKey], f.byID[i.ID] = i, i
	return i, nil
}

func (f *fakePayments) GetIntent(_ context.Context, id uuid.UUID) (*paymentsclient.Intent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i, ok := f.byID[id]; ok {
		return i, nil
	}
	return nil, &paymentsclient.Error{Kind: paymentsclient.KindRefused, StatusCode: 404}
}

func (f *fakePayments) VerifyCallback(_ context.Context, id uuid.UUID, in paymentsclient.CallbackRequest) (*paymentsclient.CallbackVerdict, error) {
	i, err := f.GetIntent(context.Background(), id)
	if err != nil {
		return nil, err
	}
	return &paymentsclient.CallbackVerdict{Verified: true, Advisory: true, Status: "pending", AmountMinor: i.AmountMinor,
		PayerID: i.PayerID, PayeeID: i.PayeeID, ReferenceType: i.ReferenceType, ReferenceID: i.ReferenceID}, nil
}

func (f *fakePayments) Refund(_ context.Context, intent uuid.UUID, in paymentsclient.RefundRequest) (*paymentsclient.RefundAccepted, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refundCalls = append(f.refundCalls, in.IdempotencyKey)
	if f.failRefund != nil {
		return nil, f.failRefund
	}
	cmd, ok := f.refunds[in.IdempotencyKey]
	if !ok {
		cmd = uuid.New()
		f.refunds[in.IdempotencyKey] = cmd
	}
	return &paymentsclient.RefundAccepted{CommandID: cmd, IntentID: intent, AmountMinor: in.AmountMinor, Status: "submitted"}, nil
}

func (f *fakePayments) calls(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, k := range f.refundCalls {
		if k == key {
			n++
		}
	}
	return n
}

// bkRig is the booking suite's rig: real store, real routes, fake payments.
type bkRig struct {
	*itRig
	p     *pgxpool.Pool
	st    *store.Store
	svc   *service.Service
	pay   *fakePayments
	cons  *payments.Consumer
	world *bkWorld
}

// bkWorld is the suite's own catalogue slice: a skill, two categories
// (any and women-only) with one priced service each, so no professional
// from another test qualifies.
type bkWorld struct {
	skill           string
	anyService      uuid.UUID
	anyOption       uuid.UUID
	womenService    uuid.UUID
	womenOption     uuid.UUID
	zone            uuid.UUID
	lat, lng        float64
	durationMinutes int
	pricePaise      int64
	womenCategory   uuid.UUID
	anyCategory     uuid.UUID
	anyCategorySlug string
}

func newBookingRig(t *testing.T) *bkRig {
	t.Helper()
	rg := newRig(t, time.Now().UTC().Truncate(time.Minute))
	p := pool(t)
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                     "admin-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":    "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":    strings.Join(doorstephttp.AdminPermissions, ","),
	}
	v, err := doorstephttp.ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if rg.admin, err = servicetoken.NewSignerFromBase64(doorstephttp.IssuerAdminService, "a1", priv); err != nil {
		t.Fatal(err)
	}
	crypto, err := propii.New(context.Background(), []config.PIIKey{{Version: 1, Key: bytes.Repeat([]byte{0x6b}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	br := &bkRig{itRig: rg, p: p, pay: newFakePayments()}
	br.st = store.New(p).WithClock(func() time.Time { return br.now })
	tc, _ := tax.NewGST(nil, gstin(t))
	br.svc = service.New(br.st, tc, 15*time.Minute).WithClock(func() time.Time { return br.now }, uuid.New).
		WithBookings(service.BookingDeps{Store: br.st, Payments: br.pay, PII: crypto, DevStubPayments: true})
	br.cons = payments.NewHandler(br.st, br.svc.AfterPaymentEvent)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	doorstephttp.New(br.svc, itKey).WithServiceAuth(v).RegisterRoutes(r)
	rg.r = r
	br.world = br.seedWorld(t)
	return br
}

func (br *bkRig) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := br.p.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", strings.Fields(sql)[0], err)
	}
}

func (br *bkRig) seedWorld(t *testing.T) *bkWorld {
	t.Helper()
	tag := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	w := &bkWorld{skill: "it_a3_" + tag, zone: uuid.MustParse(id("zone", "west-hitec-gachibowli")), lat: 17.4504, lng: 78.3808,
		durationMinutes: 60, pricePaise: 59900, anyCategory: uuid.New(), womenCategory: uuid.New(),
		anyService: uuid.New(), anyOption: uuid.New(), womenService: uuid.New(), womenOption: uuid.New(),
		anyCategorySlug: "it-any-" + tag}
	br.exec(t, `INSERT INTO doorstep.skills (code, name, requires_certificate) VALUES ($1, 'IT A3 skill', FALSE)`, w.skill)
	for _, c := range []struct {
		id      uuid.UUID
		slug    string
		family  string
		rule    string
		policy  string
		service uuid.UUID
		option  uuid.UUID
	}{
		{w.anyCategory, w.anyCategorySlug, "HOME_CLEANING", "any", "rate_card", w.anyService, w.anyOption},
		{w.womenCategory, "it-women-" + tag, "BEAUTY_SALON", "female_pros_only", "catalogue_addons_only", w.womenService, w.womenOption},
	} {
		br.exec(t, `INSERT INTO doorstep.categories (id, slug, name, family, gender_rule, extras_policy, active)
			VALUES ($1, $2, $2, $3, $4, $5, TRUE)`, c.id, c.slug, c.family, c.rule, c.policy)
		br.exec(t, `INSERT INTO doorstep.services (id, category_id, slug, name, duration_minutes, required_skill, active)
			VALUES ($1, $2, 'svc', 'IT service', 60, $3, TRUE)`, c.service, c.id, w.skill)
		br.exec(t, `INSERT INTO doorstep.service_options (id, service_id, name, duration_minutes, max_quantity, is_default)
			VALUES ($1, $2, 'Standard', 60, 1, TRUE)`, c.option, c.service)
		br.exec(t, `INSERT INTO doorstep.city_prices (city_code, item_kind, option_id, price_paise, effective_from)
			VALUES ('HYD', 'option', $1, $2, NOW() - INTERVAL '1 day')`, c.option, w.pricePaise)
	}
	// The catalogue suite counts the seed's active categories: leave none
	// of ours active behind.
	t.Cleanup(func() {
		_, _ = br.p.Exec(context.Background(), `UPDATE doorstep.categories SET active = FALSE WHERE id = ANY($1)`,
			[]uuid.UUID{w.anyCategory, w.womenCategory})
	})
	return w
}

// addPro inserts an approved, verified professional who works every day
// 08:00-20:00 IST in the suite's zone, with a clear background check.
func (br *bkRig) addPro(t *testing.T, gender string) (proID, userID uuid.UUID) {
	t.Helper()
	proID, userID = uuid.New(), uuid.New()
	br.exec(t, `INSERT INTO doorstep.professionals (id, user_id, status, display_name, city_code, gender, gender_source, home_point,
		service_radius_m, approved_at) VALUES ($1, $2, 'approved', 'Asha Rao', 'HYD', $3, 'digilocker',
		ST_SetSRID(ST_MakePoint($5, $4), 4326)::geography, 15000, NOW())`, proID, userID, gender, br.world.lat+0.01, br.world.lng)
	br.exec(t, `INSERT INTO doorstep.pro_skills (pro_id, skill_code, status, verified_at) VALUES ($1, $2, 'verified', NOW())`, proID, br.world.skill)
	br.exec(t, `INSERT INTO doorstep.pro_zones (pro_id, zone_id) VALUES ($1, $2)`, proID, br.world.zone)
	for d := 0; d < 7; d++ {
		br.exec(t, `INSERT INTO doorstep.pro_weekly_hours (pro_id, weekday, start_time, end_time) VALUES ($1, $2, '08:00', '20:00')`, proID, d)
	}
	doc := uuid.New()
	br.exec(t, `INSERT INTO doorstep.pro_documents (id, pro_id, kind, media_id, status, issued_on) VALUES ($1, $2, 'police_certificate', $3, 'approved', CURRENT_DATE - 10)`,
		doc, proID, uuid.NewString())
	br.exec(t, `INSERT INTO doorstep.background_checks (pro_id, source, document_id, status, valid_from, valid_until)
		VALUES ($1, 'uploaded_document', $2, 'clear', CURRENT_DATE - 10, CURRENT_DATE + 355)`, proID, doc)
	return proID, userID
}

func (br *bkRig) user(u uuid.UUID, method, path, body string, hdr ...string) (int, []byte) {
	h := map[string]string{"X-User-Id": u.String()}
	for i := 0; i+1 < len(hdr); i += 2 {
		h[hdr[i]] = hdr[i+1]
	}
	return br.call(method, "/v1/doorstep"+path, body, h)
}

// quoteAndAddress prices the service for a new customer and saves their
// address at the zone's point.
func (br *bkRig) quoteAndAddress(t *testing.T, customer uuid.UUID, women bool) (quote, address uuid.UUID) {
	t.Helper()
	svc, opt := br.world.anyService, br.world.anyOption
	if women {
		svc, opt = br.world.womenService, br.world.womenOption
	}
	status, body := br.user(customer, "POST", "/quotes", fmt.Sprintf(`{"service_id":"%s","option_id":"%s","lat":%f,"lng":%f}`,
		svc, opt, br.world.lat, br.world.lng))
	want(t, "quote", status, body, 201)
	var q struct {
		ID uuid.UUID `json:"id"`
	}
	data(t, body, &q)
	status, body = br.user(customer, "POST", "/addresses", fmt.Sprintf(
		`{"label":"Home","line1":"Flat 4B, Cyber Residency","landmark":"Opp. Cyber Towers","locality":"HITEC City","pincode":"500081","lat":%f,"lng":%f}`,
		br.world.lat, br.world.lng))
	want(t, "address", status, body, 201)
	var a struct {
		ID uuid.UUID `json:"id"`
	}
	data(t, body, &a)
	return q.ID, a.ID
}

// tomorrowAt is tomorrow (IST) at hh:mm.
func (br *bkRig) tomorrowAt(hh, mm int) time.Time {
	n := br.now.In(slots.IST).AddDate(0, 0, 1)
	return time.Date(n.Year(), n.Month(), n.Day(), hh, mm, 0, 0, slots.IST).UTC()
}

type bookingOut struct {
	Booking struct {
		ID            uuid.UUID `json:"id"`
		Status        string    `json:"status"`
		HoldExpiresAt *string   `json:"hold_expires_at"`
		TotalPaise    int64     `json:"total_paise"`
		Address       struct {
			Line1 string `json:"line1"`
		} `json:"address"`
	} `json:"booking"`
	PaymentIntent struct {
		PaymentID   uuid.UUID         `json:"payment_id"`
		Status      string            `json:"status"`
		AmountPaise int64             `json:"amount_paise"`
		Checkout    map[string]string `json:"checkout"`
	} `json:"payment_intent"`
}

func (br *bkRig) book(customer, quote, address uuid.UUID, start time.Time, key string) (int, []byte) {
	return br.user(customer, "POST", "/bookings",
		fmt.Sprintf(`{"quote_id":"%s","address_id":"%s","slot_start":"%s"}`, quote, address, start.Format(time.RFC3339)),
		"Idempotency-Key", key)
}

// event delivers a payment event through the real consumer handler.
func (br *bkRig) event(t *testing.T, eventID, eventType string, payload map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	env := &events.EventEnvelope{EventID: eventID, EventType: eventType, OccurredAt: time.Now(), Payload: raw}
	if err := br.cons.Handle(context.Background(), env); err != nil {
		t.Fatalf("consume %s: %v", eventType, err)
	}
}

func (br *bkRig) intentFor(t *testing.T, booking uuid.UUID) *paymentsclient.Intent {
	t.Helper()
	i, ok := br.pay.intents[payments.IntentKey(booking)]
	if !ok {
		t.Fatalf("no intent opened for %s", booking)
	}
	return i
}

func (br *bkRig) captured(t *testing.T, booking uuid.UUID, eventID string, mutate func(m map[string]any)) {
	t.Helper()
	i := br.intentFor(t, booking)
	m := map[string]any{"id": i.ID.String(), "payer_id": i.PayerID.String(), "payee_id": i.PayeeID.String(),
		"reference_type": payments.RefBooking, "reference_id": booking.String(), "amount_minor": i.AmountMinor,
		"currency": "INR", "method": "upi", "status": "succeeded", "provider_ref": "pay_it", "application_id": payments.ApplicationID}
	if mutate != nil {
		mutate(m)
	}
	br.event(t, eventID, events.EventPaymentSucceeded, m)
}

func (br *bkRig) one(t *testing.T, sql string, args ...any) any {
	t.Helper()
	var v any
	if err := br.p.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("query: %v", err)
	}
	return v
}

func (br *bkRig) status(t *testing.T, booking uuid.UUID) string {
	return br.one(t, `SELECT status FROM doorstep.bookings WHERE id = $1`, booking).(string)
}

func (br *bkRig) inboxOutcome(t *testing.T, eventID string) string {
	return br.one(t, `SELECT outcome FROM doorstep.payment_inbox WHERE event_id = $1`, eventID).(string)
}

// ---------------------------------------------------------------- tests

func TestBookingLifecycleOnTheDatabase(t *testing.T) {
	br := newBookingRig(t)
	pro, _ := br.addPro(t, "female")
	customer := uuid.New()
	quote, address := br.quoteAndAddress(t, customer, false)

	// The saved address answers its sealed lines to its owner only; the
	// database holds no plaintext street line.
	if v := br.one(t, `SELECT count(*) FROM doorstep.customer_addresses WHERE id = $1 AND line1 IS NULL AND lines_sealed IS NOT NULL`, address); v.(int64) != 1 {
		t.Fatal("address lines stored in clear")
	}
	status, body := br.user(customer, "GET", "/addresses", "")
	want(t, "addresses", status, body, 200)
	if !strings.Contains(string(body), "Flat 4B, Cyber Residency") {
		t.Fatalf("address lines not opened for the owner: %s", body)
	}
	// Serviceability on save.
	status, body = br.user(customer, "POST", "/addresses", `{"label":"Far","line1":"x","locality":"Secunderabad","pincode":"500003","lat":17.4399,"lng":78.4983}`)
	if status != 422 || errCode(body) != "DOORSTEP_OUTSIDE_SERVICE_AREA" {
		t.Fatalf("outside address saved: %d %s", status, body)
	}

	// Slots: tomorrow 10:00 is offered.
	start := br.tomorrowAt(10, 0)
	status, body = br.user(customer, "GET", "/slots?quote_id="+quote.String()+"&address_id="+address.String(), "")
	want(t, "slots", status, body, 200)
	if !slotAvailable(t, body, start) {
		t.Fatalf("tomorrow 10:00 not offered: %s", body)
	}

	// Book: Idempotency-Key required.
	status, body = br.user(customer, "POST", "/bookings", fmt.Sprintf(`{"quote_id":"%s","address_id":"%s","slot_start":"%s"}`,
		quote, address, start.Format(time.RFC3339)))
	if status != 400 || errCode(body) != "DOORSTEP_INVALID_REQUEST" {
		t.Fatalf("booking without an Idempotency-Key: %d %s", status, body)
	}
	status, body = br.book(customer, quote, address, start, "k-1")
	want(t, "book", status, body, 201)
	var out bookingOut
	data(t, body, &out)
	b := out.Booking.ID
	if out.Booking.Status != "pending_payment" || out.PaymentIntent.Status != "pending" || out.PaymentIntent.AmountPaise != br.world.pricePaise ||
		out.PaymentIntent.Checkout["provider"] != "stub" || out.Booking.Address.Line1 != "Flat 4B, Cyber Residency" {
		t.Fatalf("created: %s", body)
	}
	if v := br.one(t, `SELECT count(*) FROM doorstep.pro_calendar_blocks WHERE booking_id = $1 AND pro_id = $2 AND kind = 'hold' AND active
		AND upper(during) = $3`, b, pro, start.Add(time.Hour+30*time.Minute)); v.(int64) != 1 {
		t.Fatal("no hold [start, end + travel buffer) for the reserved professional")
	}
	// Replay: the same booking; a different body under the key is refused.
	status, body = br.book(customer, quote, address, start, "k-1")
	want(t, "replay", status, body, 201)
	var again bookingOut
	data(t, body, &again)
	if again.Booking.ID != b || len(br.pay.intents) != 1 {
		t.Fatalf("replay made another booking or intent: %s", body)
	}
	status, body = br.book(customer, quote, address, start.Add(time.Hour), "k-1")
	if status != 400 {
		t.Fatalf("key reused for another slot: %d %s", status, body)
	}
	// The quote is consumed: a second key on it is gone.
	status, body = br.book(customer, quote, address, start.Add(time.Hour), "k-2")
	if status != 410 || errCode(body) != "DOORSTEP_QUOTE_EXPIRED" {
		t.Fatalf("consumed quote: %d %s", status, body)
	}
	// Not paid until the signed event: the payment route says pending.
	status, body = br.user(customer, "GET", "/bookings/"+b.String()+"/payment", "")
	want(t, "payment", status, body, 200)
	if !strings.Contains(string(body), `"status":"pending"`) {
		t.Fatalf("paid before the event: %s", body)
	}

	// The signed capture confirms; the hold becomes the booking block.
	br.captured(t, b, "evt-cap-"+b.String(), nil)
	if br.status(t, b) != "confirmed" || br.inboxOutcome(t, "evt-cap-"+b.String()) != string(payments.OutcomeConfirmed) {
		t.Fatalf("not confirmed: %s", br.status(t, b))
	}
	if v := br.one(t, `SELECT kind FROM doorstep.pro_calendar_blocks WHERE booking_id = $1 AND active`, b); v.(string) != "booking" {
		t.Fatalf("block kind %v", v)
	}
	// Duplicate: a no-op.
	br.captured(t, b, "evt-cap-"+b.String(), nil)
	if v := br.one(t, `SELECT count(*) FROM doorstep.booking_status_history WHERE booking_id = $1 AND to_status = 'confirmed'`, b); v.(int64) != 1 {
		t.Fatal("duplicate event applied twice")
	}
	// The detail carries the timeline, a null end OTP and no photos yet.
	status, body = br.user(customer, "GET", "/bookings/"+b.String(), "")
	want(t, "booking", status, body, 200)
	for _, s := range []string{`"end_otp":null`, `"start_otp":null`, `"photos":[]`, `"to_status":"confirmed"`, `"can_cancel":true`} {
		if !strings.Contains(string(body), s) {
			t.Fatalf("booking lacks %s: %s", s, body)
		}
	}
	// Another customer never sees it.
	status, _ = br.user(uuid.New(), "GET", "/bookings/"+b.String(), "")
	if status != 404 {
		t.Fatalf("another customer read the booking: %d", status)
	}
	status, body = br.user(customer, "GET", "/bookings?status=upcoming", "")
	want(t, "list", status, body, 200)
	if !strings.Contains(string(body), b.String()) {
		t.Fatalf("upcoming list: %s", body)
	}

	// Reschedule once to 12:00 (same professional, block moved).
	newStart := br.tomorrowAt(12, 0)
	status, body = br.user(customer, "POST", "/bookings/"+b.String()+"/reschedule", `{"slot_start":"`+newStart.Format(time.RFC3339)+`"}`)
	want(t, "reschedule", status, body, 200)
	if v := br.one(t, `SELECT count(*) FROM doorstep.pro_calendar_blocks WHERE booking_id = $1 AND active AND lower(during) = $2 AND pro_id = $3`,
		b, newStart, pro); v.(int64) != 1 {
		t.Fatal("block not moved to the new slot")
	}
	status, body = br.user(customer, "POST", "/bookings/"+b.String()+"/reschedule", `{"slot_start":"`+br.tomorrowAt(14, 0).Format(time.RFC3339)+`"}`)
	if status != 409 || errCode(body) != "DOORSTEP_RESCHEDULE_NOT_ALLOWED" {
		t.Fatalf("second reschedule: %d %s", status, body)
	}

	// Cancel before assignment: free, full refund with the cause's key.
	status, body = br.user(customer, "GET", "/bookings/"+b.String()+"/cancel-preview", "")
	want(t, "preview", status, body, 200)
	if !strings.Contains(string(body), `"rule":"free_before_assignment"`) || !strings.Contains(string(body), fmt.Sprintf(`"refund_paise":%d`, br.world.pricePaise)) {
		t.Fatalf("preview: %s", body)
	}
	status, body = br.user(customer, "POST", "/bookings/"+b.String()+"/cancel", `{"reason":"plans changed"}`)
	want(t, "cancel", status, body, 200)
	key := payments.RefundKey(b, payments.CauseCustomerCancel)
	if br.pay.calls(key) != 1 || br.one(t, `SELECT status FROM doorstep.refunds WHERE idempotency_key = $1`, key).(string) != "pending" {
		t.Fatalf("refund not submitted with %s", key)
	}
	if v := br.one(t, `SELECT count(*) FROM doorstep.pro_calendar_blocks WHERE booking_id = $1 AND active`, b); v.(int64) != 0 {
		t.Fatal("cancelled booking still holds the calendar")
	}
	// payment.refunded settles it.
	cmd := br.pay.refunds[key]
	i := br.intentFor(t, b)
	br.event(t, "evt-ref-"+b.String(), events.EventPaymentRefunded, map[string]any{"id": i.ID.String(), "intent_id": i.ID.String(),
		"amount_minor": br.world.pricePaise, "status": "refunded", "reference_type": payments.RefBooking, "reference_id": b.String(),
		"command_id": cmd.String(), "application_id": payments.ApplicationID})
	if v := br.one(t, `SELECT refunded_paise FROM doorstep.bookings WHERE id = $1`, b); v.(int64) != br.world.pricePaise {
		t.Fatalf("refunded_paise %v", v)
	}
	if v := br.one(t, `SELECT status FROM doorstep.refunds WHERE idempotency_key = $1`, key); v.(string) != "succeeded" {
		t.Fatalf("refund %v", v)
	}
	// Events, in order, with the customer id on every one.
	rows, err := br.p.Query(context.Background(), `SELECT event_type, payload->>'customer_user_id' FROM doorstep.outbox_events
		WHERE partition_key = $1 ORDER BY id`, b.String())
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for rows.Next() {
		var et string
		var cust *string
		if err := rows.Scan(&et, &cust); err != nil {
			t.Fatal(err)
		}
		if cust == nil || *cust != customer.String() {
			t.Fatalf("%s without the customer id", et)
		}
		types = append(types, et)
	}
	rows.Close()
	if got := strings.Join(types, ","); got != "doorstep.booking.created,doorstep.booking.confirmed,doorstep.booking.rescheduled,doorstep.booking.cancelled,doorstep.booking.refunded" {
		t.Fatalf("events %s", got)
	}
}

func slotAvailable(t *testing.T, body []byte, start time.Time) bool {
	t.Helper()
	var d struct {
		Days []struct {
			Slots []struct {
				Start     time.Time `json:"start"`
				Available bool      `json:"available"`
			} `json:"slots"`
		} `json:"days"`
	}
	data(t, body, &d)
	for _, day := range d.Days {
		for _, s := range day.Slots {
			if s.Start.Equal(start) {
				return s.Available
			}
		}
	}
	t.Fatalf("slot %s not on the grid", start)
	return false
}

// Two customers race for the only professional at the same time: the
// exclusion constraint lets exactly one hold win.
func TestTwoHoldsSameProfessionalExactlyOneWins(t *testing.T) {
	br := newBookingRig(t)
	pro, _ := br.addPro(t, "male")
	start := br.tomorrowAt(15, 0)
	type attempt struct {
		customer, quote, address uuid.UUID
	}
	var as []attempt
	for i := 0; i < 2; i++ {
		c := uuid.New()
		q, a := br.quoteAndAddress(t, c, false)
		as = append(as, attempt{c, q, a})
	}
	// Straight at the store, both with the same single candidate, so both
	// reach the INSERT: the constraint, not the slot answer, decides.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, a := range as {
		wg.Add(1)
		go func(i int, a attempt) {
			defer wg.Done()
			f, err := br.st.QuoteFacts(context.Background(), a.customer, a.quote)
			if err != nil {
				errs[i] = err
				return
			}
			bid := uuid.New()
			_, errs[i] = br.st.CreateBooking(context.Background(), store.NewBooking{
				ID: bid, Customer: a.customer, IdempotencyKey: "race", QuoteID: a.quote, CityCode: "HYD", ZoneID: br.world.zone,
				CategoryID: f.CategoryID, CategorySlug: f.CategorySlug, ServiceID: f.Quote.ServiceID,
				Address: store.AddressSnapshot{AddressID: a.address, Label: "Home", Locality: "HITEC City", CityCode: "HYD", Pincode: "500081",
					Lat: br.world.lat, Lng: br.world.lng, ZoneID: br.world.zone},
				SlotStart: start, SlotEnd: start.Add(time.Hour), BlockEnd: start.Add(90 * time.Minute), Duration: 60, GenderRule: "any",
				TotalPaise: f.Quote.TotalPaise, TaxablePaise: f.Quote.TaxablePaise, TaxPaise: f.Quote.TaxPaise, Items: f.Quote.Lines,
				HoldExpiresAt: br.now.Add(10 * time.Minute), Candidates: []uuid.UUID{pro}, PaymentID: uuid.New(),
				IntentKey: payments.IntentKey(bid), At: br.now,
			})
		}(i, a)
	}
	wg.Wait()
	won, lost := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, store.ErrSlotTaken):
			lost++
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("won %d lost %d, want exactly one of each", won, lost)
	}
	if v := br.one(t, `SELECT count(*) FROM doorstep.pro_calendar_blocks WHERE pro_id = $1 AND active`, pro); v.(int64) != 1 {
		t.Fatalf("%v active blocks for the professional", v)
	}
	// The loser rolled back whole: no booking, its quote still open.
	if v := br.one(t, `SELECT count(*) FROM doorstep.bookings WHERE customer_user_id = ANY($1)`, []uuid.UUID{as[0].customer, as[1].customer}); v.(int64) != 1 {
		t.Fatalf("%v bookings exist", v)
	}
	// Through the API, the loser is told the slot is gone.
	status, body := br.book(as[0].customer, as[0].quote, as[0].address, start, "api-race")
	if errs[0] == nil {
		status, body = br.book(as[1].customer, as[1].quote, as[1].address, start, "api-race")
	}
	if status != 422 || errCode(body) != "DOORSTEP_SLOT_UNAVAILABLE" {
		t.Fatalf("taken slot booked: %d %s", status, body)
	}
}

func TestPaymentMismatchNeverConfirms(t *testing.T) {
	br := newBookingRig(t)
	br.addPro(t, "female")
	customer := uuid.New()
	quote, address := br.quoteAndAddress(t, customer, false)
	status, body := br.book(customer, quote, address, br.tomorrowAt(11, 0), "mm")
	want(t, "book", status, body, 201)
	var out bookingOut
	data(t, body, &out)
	b := out.Booking.ID
	for _, c := range []struct {
		id     string
		mutate func(map[string]any)
	}{
		{"evt-mm-amount", func(m map[string]any) { m["amount_minor"] = br.world.pricePaise - 100 }},
		{"evt-mm-payer", func(m map[string]any) { m["payer_id"] = uuid.NewString() }},
		{"evt-mm-currency", func(m map[string]any) { m["currency"] = "USD" }},
		{"evt-mm-intent", func(m map[string]any) { m["id"] = uuid.NewString() }},
	} {
		br.captured(t, b, c.id+b.String(), c.mutate)
		if br.status(t, b) != "pending_payment" || br.inboxOutcome(t, c.id+b.String()) != string(payments.OutcomeMismatch) {
			t.Fatalf("%s: booking %s outcome %s", c.id, br.status(t, b), br.inboxOutcome(t, c.id+b.String()))
		}
	}
	if v := br.one(t, `SELECT needs_attention FROM doorstep.bookings WHERE id = $1`, b); v.(bool) != true {
		t.Fatal("mismatch not flagged for attention")
	}
	if v := br.one(t, `SELECT paid_paise FROM doorstep.bookings WHERE id = $1`, b); v.(int64) != 0 {
		t.Fatal("mismatched money recorded as paid")
	}
	// Another application's event, or one stating none, never reaches the booking.
	br.captured(t, b, "evt-other-app"+b.String(), func(m map[string]any) { m["application_id"] = "feast" })
	br.captured(t, b, "evt-no-app"+b.String(), func(m map[string]any) { delete(m, "application_id") })
	if v := br.one(t, `SELECT count(*) FROM doorstep.payment_inbox WHERE event_id IN ($1, $2)`, "evt-other-app"+b.String(), "evt-no-app"+b.String()); v.(int64) != 0 {
		t.Fatal("foreign event claimed")
	}
	// The matching capture still confirms.
	br.captured(t, b, "evt-ok-"+b.String(), nil)
	if br.status(t, b) != "confirmed" {
		t.Fatal("a matching capture after a mismatch did not confirm")
	}
}

func TestLateCaptureConfirmsOrRefunds(t *testing.T) {
	br := newBookingRig(t)
	pro, _ := br.addPro(t, "female")
	start := br.tomorrowAt(16, 0)

	// 1. Hold lapses, the professional is still free: confirmed.
	c1 := uuid.New()
	q1, a1 := br.quoteAndAddress(t, c1, false)
	status, body := br.book(c1, q1, a1, start, "late-1")
	want(t, "book 1", status, body, 201)
	var o1 bookingOut
	data(t, body, &o1)
	base := br.now
	br.now = base.Add(11 * time.Minute)
	if n, err := br.svc.ExpireHolds(context.Background()); err != nil || n < 1 {
		t.Fatalf("sweeper: %d %v", n, err)
	}
	if br.status(t, o1.Booking.ID) != "expired" {
		t.Fatal("hold did not expire the booking")
	}
	status, body = br.user(c1, "POST", "/bookings/"+o1.Booking.ID.String()+"/payment/intent", "")
	if status != 410 || errCode(body) != "DOORSTEP_HOLD_EXPIRED" {
		t.Fatalf("intent on an expired booking: %d %s", status, body)
	}
	br.captured(t, o1.Booking.ID, "evt-late1-"+o1.Booking.ID.String(), nil)
	if br.status(t, o1.Booking.ID) != "confirmed" || br.inboxOutcome(t, "evt-late1-"+o1.Booking.ID.String()) != string(payments.OutcomeLateCaptureConfirmed) {
		t.Fatalf("late capture with a free professional: %s", br.status(t, o1.Booking.ID))
	}

	// 2. Hold lapses and someone else takes the professional: full refund.
	br.now = base
	start2 := br.tomorrowAt(18, 0)
	c2 := uuid.New()
	q2, a2 := br.quoteAndAddress(t, c2, false)
	status, body = br.book(c2, q2, a2, start2, "late-2")
	want(t, "book 2", status, body, 201)
	var o2 bookingOut
	data(t, body, &o2)
	br.now = base.Add(11 * time.Minute)
	if _, err := br.svc.ExpireHolds(context.Background()); err != nil {
		t.Fatal(err)
	}
	c3 := uuid.New()
	q3, a3 := br.quoteAndAddress(t, c3, false)
	status, body = br.book(c3, q3, a3, start2, "late-3")
	want(t, "book 3 takes the freed professional", status, body, 201)
	br.captured(t, o2.Booking.ID, "evt-late2-"+o2.Booking.ID.String(), nil)
	if br.status(t, o2.Booking.ID) != "expired" || br.inboxOutcome(t, "evt-late2-"+o2.Booking.ID.String()) != string(payments.OutcomeLateCaptureRefund) {
		t.Fatalf("late capture on a taken professional: %s", br.status(t, o2.Booking.ID))
	}
	key := payments.RefundKey(o2.Booking.ID, payments.CauseLateCapture)
	if br.pay.calls(key) != 1 {
		t.Fatalf("late-capture refund submitted %d times", br.pay.calls(key))
	}
	if v := br.one(t, `SELECT amount_paise FROM doorstep.refunds WHERE idempotency_key = $1`, key); v.(int64) != br.world.pricePaise {
		t.Fatalf("late-capture refund %v", v)
	}
	if v := br.one(t, `SELECT count(*) FROM doorstep.pro_calendar_blocks WHERE pro_id = $1 AND active AND lower(during) = $2`, pro, start2); v.(int64) != 1 {
		t.Fatal("the refunded booking took the calendar from the new one")
	}
}

func TestRefundRetryKeepsTheKey(t *testing.T) {
	br := newBookingRig(t)
	br.addPro(t, "female")
	customer := uuid.New()
	quote, address := br.quoteAndAddress(t, customer, false)
	status, body := br.book(customer, quote, address, br.tomorrowAt(9, 0), "rr")
	want(t, "book", status, body, 201)
	var out bookingOut
	data(t, body, &out)
	b := out.Booking.ID
	br.captured(t, b, "evt-rr-"+b.String(), nil)

	// payments is down: the refund stays requested.
	br.pay.failRefund = &paymentsclient.Error{Kind: paymentsclient.KindServer, StatusCode: 503}
	status, body = br.user(customer, "POST", "/bookings/"+b.String()+"/cancel", `{"reason":"not needed"}`)
	want(t, "cancel", status, body, 200)
	key := payments.RefundKey(b, payments.CauseCustomerCancel)
	if v := br.one(t, `SELECT status FROM doorstep.refunds WHERE idempotency_key = $1`, key); v.(string) != "requested" {
		t.Fatalf("refund %v after a failed submission", v)
	}
	// Back up: the worker resubmits with the SAME key, once due.
	br.pay.failRefund = nil
	if n, _ := br.svc.ResubmitPendingRefunds(context.Background()); n != 0 {
		t.Fatal("resubmitted before the backoff elapsed")
	}
	br.now = br.now.Add(2 * time.Minute)
	if n, err := br.svc.ResubmitPendingRefunds(context.Background()); err != nil || n != 1 {
		t.Fatalf("resubmit: %d %v", n, err)
	}
	if br.pay.calls(key) != 2 || len(br.pay.refunds) != 1 {
		t.Fatalf("calls %d commands %d: the retry must reuse the key", br.pay.calls(key), len(br.pay.refunds))
	}
	if v := br.one(t, `SELECT status FROM doorstep.refunds WHERE idempotency_key = $1`, key); v.(string) != "pending" {
		t.Fatalf("refund %v after resubmission", v)
	}
	// payment.refund_failed: failed, flagged, never resubmitted.
	i := br.intentFor(t, b)
	br.event(t, "evt-rf-"+b.String(), events.EventPaymentRefundFailed, map[string]any{"id": i.ID.String(), "command_id": br.pay.refunds[key].String(),
		"reference_type": payments.RefBooking, "reference_id": b.String(), "amount_minor": br.world.pricePaise, "reason_code": "BAD_REQUEST",
		"reason": "refund window closed", "status": "needs_attention", "application_id": payments.ApplicationID})
	if v := br.one(t, `SELECT status FROM doorstep.refunds WHERE idempotency_key = $1`, key); v.(string) != "failed" {
		t.Fatalf("refund %v after refund_failed", v)
	}
	if v := br.one(t, `SELECT needs_attention FROM doorstep.bookings WHERE id = $1`, b); v.(bool) != true {
		t.Fatal("refund failure not flagged")
	}
	br.now = br.now.Add(time.Hour)
	if n, _ := br.svc.ResubmitPendingRefunds(context.Background()); n != 0 {
		t.Fatal("a failed refund was resubmitted")
	}
}

func TestOutstandingDueBlocksBooking(t *testing.T) {
	br := newBookingRig(t)
	br.addPro(t, "female")
	customer := uuid.New()
	quote, address := br.quoteAndAddress(t, customer, false)
	// A past booking with an unpaid extras bill.
	status, body := br.book(customer, quote, address, br.tomorrowAt(8, 0), "od-1")
	want(t, "book", status, body, 201)
	var out bookingOut
	data(t, body, &out)
	bill := uuid.New()
	br.exec(t, `INSERT INTO doorstep.extras_bills (id, booking_id, amount_paise, status) VALUES ($1, $2, 45000, 'outstanding')`, bill, out.Booking.ID)
	br.exec(t, `INSERT INTO doorstep.outstanding (customer_user_id, booking_id, extras_bill_id, amount_paise) VALUES ($1, $2, $3, 45000)`,
		customer, out.Booking.ID, bill)
	q2, _ := br.quoteAndAddress(t, customer, false)
	status, body = br.user(customer, "GET", "/slots?quote_id="+q2.String(), "")
	if status != 409 || errCode(body) != "DOORSTEP_OUTSTANDING_DUE" || !strings.Contains(string(body), bill.String()) {
		t.Fatalf("slots with outstanding: %d %s", status, body)
	}
	status, body = br.book(customer, q2, address, br.tomorrowAt(13, 0), "od-2")
	if status != 409 || errCode(body) != "DOORSTEP_OUTSTANDING_DUE" {
		t.Fatalf("booking with outstanding: %d %s", status, body)
	}
	br.exec(t, `UPDATE doorstep.outstanding SET status = 'paid' WHERE extras_bill_id = $1`, bill)
	status, body = br.book(customer, q2, address, br.tomorrowAt(13, 0), "od-2")
	want(t, "book once paid", status, body, 201)
}

func TestSlotsApplyGenderRules(t *testing.T) {
	br := newBookingRig(t)
	br.addPro(t, "male")
	start := br.tomorrowAt(10, 30)
	customer := uuid.New()
	qWomen, address := br.quoteAndAddress(t, customer, true)
	slotsOf := func(q uuid.UUID, extra string) []byte {
		status, body := br.user(customer, "GET", "/slots?quote_id="+q.String()+"&address_id="+address.String()+extra, "")
		want(t, "slots", status, body, 200)
		return body
	}
	if slotAvailable(t, slotsOf(qWomen, ""), start) {
		t.Fatal("women's salon offered with only a male professional")
	}
	status, body := br.book(customer, qWomen, address, start, "g-1")
	if status != 422 || errCode(body) != "DOORSTEP_SLOT_UNAVAILABLE" {
		t.Fatalf("women's salon booked with a male professional: %d %s", status, body)
	}
	qAny, _ := br.quoteAndAddress(t, customer, false)
	if !slotAvailable(t, slotsOf(qAny, ""), start) {
		t.Fatal("any-gender service not offered")
	}
	if slotAvailable(t, slotsOf(qAny, "&require_female_pro=true"), start) {
		t.Fatal("woman-professional preference ignored")
	}
	br.addPro(t, "female")
	if !slotAvailable(t, slotsOf(qWomen, ""), start) {
		t.Fatal("women's salon not offered with a woman professional")
	}
}

func TestAdminBookingsOnTheDatabase(t *testing.T) {
	br := newBookingRig(t)
	br.addPro(t, "female")
	customer := uuid.New()
	quote, address := br.quoteAndAddress(t, customer, false)
	status, body := br.book(customer, quote, address, br.tomorrowAt(17, 0), "adm")
	want(t, "book", status, body, 201)
	var out bookingOut
	data(t, body, &out)
	b := out.Booking.ID
	br.captured(t, b, "evt-adm-"+b.String(), nil)

	status, body = br.adminCall("GET", "/bookings/"+b.String(), doorstephttp.PermBookingsRead, "")
	want(t, "admin get", status, body, 200)
	for _, s := range []string{`"start_otp":null`, `"end_otp":null`, `"actor_kind":"payment_event"`, customer.String()} {
		if !strings.Contains(string(body), s) {
			t.Fatalf("admin booking lacks %s: %s", s, body)
		}
	}
	status, body = br.adminCall("GET", "/bookings?city=HYD", doorstephttp.PermBookingsRead, "")
	want(t, "admin list", status, body, 200)
	if strings.Contains(string(body), "Cyber Residency") {
		t.Fatal("address in an admin list row")
	}
	refund := func(key string, amount int64) (int, []byte) {
		tok, _ := br.admin.Mint(doorstephttp.AudienceDoorstep, "admin-console", []string{doorstephttp.PermRefundsIssue}, nil, time.Minute,
			servicetoken.WithActor(br.actor.String()))
		return br.call("POST", doorstephttp.InternalAdminPrefix+"/bookings/"+b.String()+"/refund",
			fmt.Sprintf(`{"amount_paise":%d,"reason":"goodwill"}`, amount),
			map[string]string{doorstephttp.ServiceAuthHeader: "Bearer " + tok, "Idempotency-Key": key})
	}
	status, body = refund("rf-1", br.world.pricePaise+1)
	if status != 422 || errCode(body) != "DOORSTEP_REFUND_EXCEEDS_PAID" {
		t.Fatalf("over-refund: %d %s", status, body)
	}
	status, body = refund("rf-1", 10000)
	want(t, "refund", status, body, 201)
	var r1 struct {
		ID uuid.UUID `json:"id"`
	}
	data(t, body, &r1)
	status, body = refund("rf-1", 10000)
	want(t, "refund replay", status, body, 201)
	var r2 struct {
		ID uuid.UUID `json:"id"`
	}
	data(t, body, &r2)
	if r1.ID != r2.ID {
		t.Fatal("replayed Idempotency-Key made a second refund")
	}
	// The partial refund settles; the same event delivered again is a no-op
	// (a re-applied refund would count the money twice).
	rfKey := payments.RefundKey(b, payments.CauseAdminPrefix+store.ShortHash("rf-1"))
	i := br.intentFor(t, b)
	for n := 0; n < 2; n++ {
		br.event(t, "evt-prf-"+b.String(), events.EventPaymentRefunded, map[string]any{"id": i.ID.String(), "amount_minor": 10000,
			"status": "partially_refunded", "reference_type": payments.RefBooking, "reference_id": b.String(),
			"command_id": br.pay.refunds[rfKey].String(), "application_id": payments.ApplicationID})
	}
	if v := br.one(t, `SELECT refunded_paise FROM doorstep.bookings WHERE id = $1`, b); v.(int64) != 10000 {
		t.Fatalf("refunded_paise %v after a duplicate refund event, want 10000", v)
	}
	if v := br.one(t, `SELECT count(*) FROM doorstep.refunds WHERE booking_id = $1`, b); v.(int64) != 1 {
		t.Fatalf("%v refund rows after a duplicate refund event", v)
	}
	// What is left: captured minus non-failed refunds.
	status, body = refund("rf-2", br.world.pricePaise-10000+1)
	if status != 422 {
		t.Fatalf("refund beyond the remainder: %d %s", status, body)
	}
	if v := br.one(t, `SELECT count(*) FROM doorstep.admin_audit_log WHERE entity = 'booking' AND entity_id = $1 AND action = 'booking.refund'`, b.String()); v.(int64) != 1 {
		t.Fatalf("refund audit rows %v", v)
	}
	// Ops cancel: full refund of what remains, audited.
	tok, _ := br.admin.Mint(doorstephttp.AudienceDoorstep, "admin-console", []string{doorstephttp.PermBookingsCancel}, nil, time.Minute,
		servicetoken.WithActor(br.actor.String()))
	status, body = br.call("POST", doorstephttp.InternalAdminPrefix+"/bookings/"+b.String()+"/cancel", `{"reason":"professional unwell"}`,
		map[string]string{doorstephttp.ServiceAuthHeader: "Bearer " + tok})
	want(t, "admin cancel", status, body, 200)
	if v := br.one(t, `SELECT amount_paise FROM doorstep.refunds WHERE idempotency_key = $1`, payments.RefundKey(b, payments.CauseAdminCancel)); v.(int64) != br.world.pricePaise-10000 {
		t.Fatalf("ops cancel refunded %v", v)
	}
	status, body = br.adminCall("GET", "/stats", doorstephttp.PermStatsRead, "")
	want(t, "stats", status, body, 200)
	if !strings.Contains(string(body), `"pros_approved"`) {
		t.Fatalf("stats: %s", body)
	}
}

func TestCancelFeeTierOnTheDatabase(t *testing.T) {
	br := newBookingRig(t)
	pro, proUser := br.addPro(t, "female")
	_ = proUser
	customer := uuid.New()
	quote, address := br.quoteAndAddress(t, customer, false)
	start := br.tomorrowAt(19, 0)
	status, body := br.book(customer, quote, address, start, "fee")
	want(t, "book", status, body, 201)
	var out bookingOut
	data(t, body, &out)
	b := out.Booking.ID
	br.captured(t, b, "evt-fee-"+b.String(), nil)
	// Accepted by the professional (A4's write, by hand here).
	br.exec(t, `INSERT INTO doorstep.booking_assignments (booking_id, pro_id, status, offer_expires_at, responded_at)
		VALUES ($1, $2, 'accepted', NOW(), NOW())`, b, pro)
	br.exec(t, `UPDATE doorstep.bookings SET status = 'assigned', assigned_at = NOW() WHERE id = $1`, b)
	// No cancel after the start OTP.
	br.exec(t, `UPDATE doorstep.bookings SET status = 'in_progress' WHERE id = $1`, b)
	status, body = br.user(customer, "POST", "/bookings/"+b.String()+"/cancel", `{"reason":"changed my mind"}`)
	if status != 409 || errCode(body) != "DOORSTEP_CANCEL_NOT_ALLOWED" {
		t.Fatalf("cancel in progress: %d %s", status, body)
	}
	br.exec(t, `UPDATE doorstep.bookings SET status = 'assigned' WHERE id = $1`, b)
	br.now = start.Add(-100 * time.Minute) // < 3 h, > 1 h
	status, body = br.user(customer, "GET", "/bookings/"+b.String()+"/cancel-preview", "")
	want(t, "preview", status, body, 200)
	if !strings.Contains(string(body), `"fee_paise":7500`) || !strings.Contains(string(body), `"rule":"lt_3h"`) {
		t.Fatalf("assigned < 3 h preview: %s", body)
	}
	status, body = br.user(customer, "POST", "/bookings/"+b.String()+"/cancel", `{"reason":"late"}`)
	want(t, "cancel", status, body, 200)
	if v := br.one(t, `SELECT amount_paise FROM doorstep.refunds WHERE booking_id = $1`, b); v.(int64) != br.world.pricePaise-7500 {
		t.Fatalf("refund %v, want paid minus the ₹75 fee", v)
	}
	// The cancelled event names the accepted professional (they are told).
	if v := br.one(t, `SELECT payload->>'pro_user_id' FROM doorstep.outbox_events WHERE partition_key = $1 AND event_type = 'doorstep.booking.cancelled'`,
		b.String()); v == nil || v.(string) != proUser.String() {
		t.Fatalf("cancelled event pro_user_id %v", v)
	}
}

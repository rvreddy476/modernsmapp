package itest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/doorstep-service/internal/dispatch"
	doorstephttp "github.com/atpost/doorstep-service/internal/http"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Dispatch, presence and realtime (A4) on the real store: offers, accept,
// decline with the block moved in one transaction, the workers (expiry,
// T-2 h alert, T-45 cancel and refund, late, no-show, not on duty, stale
// GPS), reschedule re-offer, ops redispatch and realtime token scope.
//
// The workers scan every booking in the database, so each rig first closes
// whatever earlier tests left live (the suite runs its tests one by one).

type frameRec struct {
	topic, typ string
	raw        string
}

type frameLog struct {
	mu     sync.Mutex
	frames []frameRec
}

func (l *frameLog) Publish(_ context.Context, topic, typ string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.frames = append(l.frames, frameRec{topic, typ, string(raw)})
	return nil
}

func (l *frameLog) count(topic, typ string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, f := range l.frames {
		if f.topic == topic && f.typ == typ {
			n++
		}
	}
	return n
}

func (l *frameLog) all() []frameRec {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]frameRec(nil), l.frames...)
}

// testSigner makes a readable token: tok|user|topics.
type testSigner struct{}

func (testSigner) Sign(user string, topics []string) (string, error) {
	return "tok|" + user + "|" + strings.Join(topics, ","), nil
}

type dsRig struct {
	*bkRig
	frames *frameLog
}

func newDispatchRig(t *testing.T) *dsRig {
	t.Helper()
	frames := &frameLog{}
	br := newBookingRig(t, func(svc *service.Service, st *store.Store, _ *propii.Crypto) *service.Service {
		return svc.WithPro(service.ProDeps{Store: st}).
			WithDispatch(service.DispatchDeps{Store: st, Realtime: frames, Signer: testSigner{}})
	})
	dr := &dsRig{bkRig: br, frames: frames}
	// Close what earlier tests left live so the workers act on ours only.
	dr.exec(t, `UPDATE doorstep.booking_assignments SET status = 'cancelled' WHERE status IN ('offered', 'accepted')`)
	dr.exec(t, `UPDATE doorstep.pro_calendar_blocks SET active = FALSE WHERE active AND booking_id IS NOT NULL`)
	dr.exec(t, `UPDATE doorstep.bookings SET status = 'cancelled', cancelled_by_kind = 'system', rescue_until = NULL, rescue_cause = NULL
		WHERE status IN ('pending_payment', 'confirmed', 'assigned', 'en_route', 'arrived', 'in_progress', 'awaiting_extras_payment')`)
	dr.exec(t, `UPDATE doorstep.professionals SET on_duty = FALSE, on_duty_since = NULL WHERE on_duty`)
	return dr
}

// confirmed books the suite's service at start for a new customer and
// applies the signed capture: the booking is confirmed and dispatched.
func (dr *dsRig) confirmed(t *testing.T, start time.Time) (booking, customer uuid.UUID) {
	t.Helper()
	customer = uuid.New()
	q, a := dr.quoteAndAddress(t, customer, false)
	status, body := dr.book(customer, q, a, start, "k-"+uuid.NewString())
	want(t, "book", status, body, 201)
	var out bookingOut
	data(t, body, &out)
	dr.captured(t, out.Booking.ID, "ev-"+uuid.NewString(), nil)
	if s := dr.status(t, out.Booking.ID); s != "confirmed" {
		t.Fatalf("booking %s after capture", s)
	}
	return out.Booking.ID, customer
}

// liveOffer is the booking's open offer: its id and professional.
func (dr *dsRig) liveOffer(t *testing.T, booking uuid.UUID) (offer, pro uuid.UUID) {
	t.Helper()
	if err := dr.p.QueryRow(context.Background(), `SELECT id, pro_id FROM doorstep.booking_assignments
		WHERE booking_id = $1 AND status = 'offered'`, booking).Scan(&offer, &pro); err != nil {
		t.Fatalf("no open offer for %s: %v", booking, err)
	}
	return offer, pro
}

func (dr *dsRig) activeBlocks(t *testing.T, booking uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := dr.p.Query(context.Background(), `SELECT pro_id FROM doorstep.pro_calendar_blocks WHERE booking_id = $1 AND active`, booking)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var p uuid.UUID
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func (dr *dsRig) events(t *testing.T, booking uuid.UUID, eventType string) []string {
	t.Helper()
	rows, err := dr.p.Query(context.Background(), `SELECT payload::text FROM doorstep.outbox_events
		WHERE event_type = $1 AND payload->>'booking_id' = $2 ORDER BY id`, eventType, booking.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		c, _ := json.Marshal(v)
		out = append(out, string(c))
	}
	return out
}

func (dr *dsRig) tick(t *testing.T) service.DispatchReport {
	t.Helper()
	r, err := dr.svc.DispatchTick(context.Background())
	if err != nil {
		t.Logf("tick errors (other suites' rows): %v", err)
	}
	return r
}

func (dr *dsRig) pro(u uuid.UUID, method, path, body string) (int, []byte) {
	return dr.user(u, method, "/pro"+path, body)
}

// noPII: no frame carries the address, an OTP or a phone number.
func (dr *dsRig) noPII(t *testing.T) {
	t.Helper()
	for _, f := range dr.frames.all() {
		low := strings.ToLower(f.raw)
		for _, bad := range []string{"cyber", "flat 4b", "500081", "otp", "phone", "line1", "landmark"} {
			if strings.Contains(low, bad) {
				t.Fatalf("frame %s on %s carries %q: %s", f.typ, f.topic, bad, f.raw)
			}
		}
	}
}

func reservedOf(t *testing.T, dr *dsRig, booking uuid.UUID) *uuid.UUID {
	t.Helper()
	var p *uuid.UUID
	if err := dr.p.QueryRow(context.Background(), `SELECT reserved_pro_id FROM doorstep.bookings WHERE id = $1`, booking).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func pair(t *testing.T, dr *dsRig, booking uuid.UUID, a, au, b, bu uuid.UUID) (first, firstUser, second, secondUser uuid.UUID) {
	t.Helper()
	r := reservedOf(t, dr, booking)
	if r == nil {
		t.Fatal("no reserved professional")
	}
	if *r == a {
		return a, au, b, bu
	}
	return b, bu, a, au
}

// ---------------------------------------------------------------- tests

func TestOfferAcceptOnTheDatabase(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "female")
	b, bu := dr.addPro(t, "female")
	start := dr.tomorrowAt(10, 0)
	booking, customer := dr.confirmed(t, start)
	first, firstUser, _, secondUser := pair(t, dr, booking, a, au, b, bu)

	// The offer went to the professional holding the slot, with the window
	// the city config gives (2 h when the slot is > 12 h away, else 10 min).
	offer, pro := dr.liveOffer(t, booking)
	if pro != first {
		t.Fatal("offer not made to the reserved professional")
	}
	wantExp := dispatch.OfferExpiry(dr.now, start, dispatch.DefaultWindows, nil)
	if got := dr.one(t, `SELECT offer_expires_at FROM doorstep.booking_assignments WHERE id = $1`, offer).(time.Time); !got.Equal(wantExp) {
		t.Fatalf("offer expires %v, want %v", got, wantExp)
	}
	if n := len(dr.events(t, booking, "doorstep.pro.offer_created")); n != 1 {
		t.Fatalf("%d offer_created events", n)
	}
	ev := dr.events(t, booking, "doorstep.pro.offer_created")[0]
	if !strings.Contains(ev, `"expires_at"`) || !strings.Contains(ev, `"locality":"HITEC City"`) || strings.Contains(ev, "Cyber") {
		t.Fatalf("offer event: %s", ev)
	}
	// Dispatch again is a no-op (idempotent per booking).
	if err := dr.svc.Dispatch(context.Background(), booking); err != nil {
		t.Fatal(err)
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.booking_assignments WHERE booking_id = $1`, booking); v.(int64) != 1 {
		t.Fatalf("%v assignments after a second dispatch", v)
	}

	// The offer list: locality only.
	status, body := dr.pro(firstUser, "GET", "/offers", "")
	want(t, "offers", status, body, 200)
	if !strings.Contains(string(body), offer.String()) || !strings.Contains(string(body), `"locality":"HITEC City"`) ||
		strings.Contains(string(body), "Cyber") || !strings.Contains(string(body), `"status":"open"`) {
		t.Fatalf("offers: %s", body)
	}
	// Before accepting, the job shows no address.
	status, body = dr.pro(firstUser, "GET", "/jobs/"+booking.String(), "")
	want(t, "job before accept", status, body, 200)
	if !strings.Contains(string(body), `"address":null`) || strings.Contains(string(body), "Cyber") || !strings.Contains(string(body), `"chat_open":false`) {
		t.Fatalf("job before accept: %s", body)
	}
	// Someone else's offer cannot be accepted (or even seen).
	status, body = dr.pro(secondUser, "POST", "/offers/"+offer.String()+"/accept", "")
	if status != 404 || errCode(body) != "DOORSTEP_OFFER_NOT_FOUND" {
		t.Fatalf("other professional accepted: %d %s", status, body)
	}
	status, body = dr.pro(secondUser, "GET", "/jobs/"+booking.String(), "")
	if status != 404 {
		t.Fatalf("other professional read the job: %d %s", status, body)
	}

	// Accept: assigned, address now.
	status, body = dr.pro(firstUser, "POST", "/offers/"+offer.String()+"/accept", "")
	want(t, "accept", status, body, 200)
	if !strings.Contains(string(body), "Flat 4B, Cyber Residency") || !strings.Contains(string(body), `"chat_open":true`) ||
		!strings.Contains(string(body), `"status":"assigned"`) {
		t.Fatalf("accepted job: %s", body)
	}
	if s := dr.status(t, booking); s != "assigned" {
		t.Fatalf("booking %s after accept", s)
	}
	if v := dr.one(t, `SELECT offers_accepted FROM doorstep.professionals WHERE id = $1`, first); v.(int32) != 1 {
		t.Fatalf("offers_accepted %v", v)
	}
	if len(dr.events(t, booking, "doorstep.booking.assigned")) != 1 {
		t.Fatal("no doorstep.booking.assigned")
	}
	// Replay of the accept answers the same job.
	status, _ = dr.pro(firstUser, "POST", "/offers/"+offer.String()+"/accept", "")
	want(t, "accept replay", status, nil, 200)
	// Jobs list, by status and IST date.
	date := start.In(time.FixedZone("IST", 5*3600+30*60)).Format("2006-01-02")
	status, body = dr.pro(firstUser, "GET", "/jobs?status=upcoming&date="+date, "")
	want(t, "jobs", status, body, 200)
	if !strings.Contains(string(body), booking.String()) {
		t.Fatalf("job not listed: %s", body)
	}
	status, body = dr.pro(firstUser, "GET", "/jobs?status=upcoming&date=2020-01-01", "")
	if status != 200 || strings.Contains(string(body), booking.String()) {
		t.Fatalf("date filter: %s", body)
	}
	// Realtime: the offer and the status went out; nothing carries PII.
	if dr.frames.count(service.ProTopic(firstUser), service.FrameOfferNew) != 1 ||
		dr.frames.count(service.BookingTopic(booking), service.FrameBookingStatus) < 2 {
		t.Fatalf("frames: %+v", dr.frames.all())
	}
	dr.noPII(t)
	_ = customer
}

// changePro is the customer's pick for a pro_unavailable booking.
func (dr *dsRig) changePro(customer, booking, pro uuid.UUID, start *time.Time, key string) (int, []byte) {
	body := fmt.Sprintf(`{"pro_id":"%s","asap":true}`, pro)
	if start != nil {
		body = fmt.Sprintf(`{"pro_id":"%s","slot_start":"%s"}`, pro, start.Format(time.RFC3339))
	}
	return dr.user(customer, "POST", "/bookings/"+booking.String()+"/change-professional", body, "Idempotency-Key", key)
}

// assertProUnavailable: the booking waits for the customer — nobody holds
// it, no live offer, the professional excluded, the event and the frame out.
func (dr *dsRig) assertProUnavailable(t *testing.T, booking, customer, gone uuid.UUID, cause string) {
	t.Helper()
	if s := dr.status(t, booking); s != "pro_unavailable" {
		t.Fatalf("booking %s, want pro_unavailable", s)
	}
	if blocks := dr.activeBlocks(t, booking); len(blocks) != 0 {
		t.Fatalf("blocks left: %v", blocks)
	}
	if r := reservedOf(t, dr, booking); r != nil {
		t.Fatal("a professional is still reserved")
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.booking_assignments WHERE booking_id = $1 AND status IN ('offered', 'accepted')`, booking); v.(int64) != 0 {
		t.Fatalf("%v live assignments: nobody may be reassigned silently", v)
	}
	if v := dr.one(t, `SELECT $2 = ANY(excluded_pro_ids) FROM doorstep.bookings WHERE id = $1`, booking, gone); v.(bool) != true {
		t.Fatal("the professional who let it go is not excluded")
	}
	ev := dr.events(t, booking, "doorstep.booking.pro_unavailable")
	if len(ev) == 0 || !strings.Contains(ev[len(ev)-1], `"cause":"`+cause+`"`) || !strings.Contains(ev[len(ev)-1], `"choice_deadline"`) ||
		!strings.Contains(ev[len(ev)-1], customer.String()) {
		t.Fatalf("pro_unavailable events: %v", ev)
	}
	if dr.frames.count(service.BookingTopic(booking), service.FrameProUnavailable) < 1 {
		t.Fatal("the customer was not told live")
	}
	if v := dr.one(t, `SELECT choice_deadline FROM doorstep.bookings WHERE id = $1`, booking).(time.Time); !v.Equal(dr.now.Add(dispatch.ChoiceWindow)) {
		t.Fatalf("choice deadline %v (now %v)", v, dr.now)
	}
}

// B1: a decline never moves the job to someone else silently: the booking
// goes to pro_unavailable and the customer picks (here the second
// professional, cheaper: the difference is refunded and only they are
// offered the job).
func TestDeclineGoesProUnavailableThenCustomerPicks(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "male")
	b, bu := dr.addPro(t, "male")
	dr.exec(t, `UPDATE doorstep.pro_service_prices SET price_paise = price_paise - 10000 WHERE pro_id = $1`, b)
	start := dr.tomorrowAt(11, 0)
	booking, customer := dr.confirmed(t, start)
	if r := reservedOf(t, dr, booking); r == nil || *r != a {
		t.Fatal("the hold is not on the professional the customer picked")
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.pro_calendar_blocks WHERE pro_id = $1 AND booking_id = $2 AND active`, b, booking); v.(int64) != 0 {
		t.Fatal("a professional the customer did not pick holds the slot")
	}
	offer, _ := dr.liveOffer(t, booking)

	status, body := dr.pro(au, "POST", "/offers/"+offer.String()+"/decline", `{"reason":"too_far"}`)
	want(t, "decline", status, body, 204)
	dr.assertProUnavailable(t, booking, customer, a, "declined")
	if v := dr.one(t, `SELECT decline_reason FROM doorstep.booking_assignments WHERE id = $1`, offer); v.(string) != "too_far" {
		t.Fatalf("decline reason %v", v)
	}
	closed := dr.events(t, booking, "doorstep.pro.offer_closed")
	if len(closed) != 1 || !strings.Contains(closed[0], `"outcome":"declined"`) {
		t.Fatalf("offer_closed: %v", closed)
	}
	// The workers never reassign it either.
	dr.now = dr.now.Add(dispatch.RetryEvery + time.Minute)
	dr.tick(t)
	if v := dr.one(t, `SELECT count(*) FROM doorstep.booking_assignments WHERE booking_id = $1 AND pro_id = $2`, booking, b); v.(int64) != 0 {
		t.Fatal("the second professional was offered the job without the customer choosing")
	}
	dr.now = dr.now.Add(-(dispatch.RetryEvery + time.Minute))
	// Declining twice is a no-op; the declined professional cannot accept.
	status, _ = dr.pro(au, "POST", "/offers/"+offer.String()+"/decline", "")
	want(t, "decline replay", status, nil, 204)
	status, body = dr.pro(au, "POST", "/offers/"+offer.String()+"/accept", "")
	if status != 409 || errCode(body) != "DOORSTEP_OFFER_TAKEN" {
		t.Fatalf("accept after decline: %d %s", status, body)
	}

	// The alternatives: the second professional, not the first.
	status, body = dr.user(customer, "GET", "/bookings/"+booking.String()+"/professionals?date="+start.In(slots.IST).Format("2006-01-02"), "")
	want(t, "alternatives", status, body, 200)
	if !strings.Contains(string(body), b.String()) || strings.Contains(string(body), a.String()) ||
		!strings.Contains(string(body), `"difference_paise":-10000`) {
		t.Fatalf("alternatives: %s", body)
	}
	status, body = dr.changePro(customer, booking, a, &start, "pick-a")
	if status != 422 || errCode(body) != "DOORSTEP_SLOT_UNAVAILABLE" {
		t.Fatalf("picked the professional who declined: %d %s", status, body)
	}
	status, body = dr.changePro(customer, booking, b, &start, "pick-b")
	want(t, "pick b", status, body, 200)
	if s := dr.status(t, booking); s != "confirmed" {
		t.Fatalf("after the pick: %s", s)
	}
	if !strings.Contains(string(body), `"status":"applied"`) || !strings.Contains(string(body), `"refund_paise":10000`) {
		t.Fatalf("change: %s", body)
	}
	if _, p := dr.liveOffer(t, booking); p != b {
		t.Fatal("the picked professional was not offered the job")
	}
	if blocks := dr.activeBlocks(t, booking); len(blocks) != 1 || blocks[0] != b {
		t.Fatalf("blocks %v", blocks)
	}
	if v := dr.one(t, `SELECT total_paise FROM doorstep.bookings WHERE id = $1`, booking); v.(int64) != dr.world.pricePaise-10000 {
		t.Fatalf("total %v", v)
	}
	var key string
	if err := dr.p.QueryRow(context.Background(), `SELECT idempotency_key FROM doorstep.refunds WHERE booking_id = $1 AND cause LIKE 'pro_change_%'`,
		booking).Scan(&key); err != nil || dr.pay.calls(key) != 1 {
		t.Fatalf("difference refund %q %v", key, err)
	}
	if v := dr.one(t, `SELECT amount_paise FROM doorstep.refunds WHERE idempotency_key = $1`, key); v.(int64) != 10000 {
		t.Fatalf("difference refund %v", v)
	}
	if ev := dr.events(t, booking, "doorstep.booking.pro_changed"); len(ev) != 1 || !strings.Contains(ev[0], `"new_pro_user_id":"`+bu.String()) ||
		!strings.Contains(ev[0], `"previous_pro_user_id":"`+au.String()) || !strings.Contains(ev[0], `"difference_paise":-10000`) {
		t.Fatalf("pro_changed: %v", ev)
	}
	// The same key replays the change; nothing else moves.
	status, again := dr.changePro(customer, booking, b, &start, "pick-b")
	want(t, "replay", status, again, 200)
	if v := dr.one(t, `SELECT count(*) FROM doorstep.booking_pro_changes WHERE booking_id = $1`, booking); v.(int64) != 1 {
		t.Fatalf("%v changes after a replay", v)
	}
	dr.noPII(t)
}

// An offer that lapses: pro_unavailable, never the next professional.
func TestOfferExpiryWorker(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "female")
	b, _ := dr.addPro(t, "female")
	booking, customer := dr.confirmed(t, dr.tomorrowAt(12, 0))
	offer, _ := dr.liveOffer(t, booking)
	exp := dr.one(t, `SELECT offer_expires_at FROM doorstep.booking_assignments WHERE id = $1`, offer).(time.Time)

	// Not yet due: nothing happens.
	dr.now = exp.Add(-time.Second)
	dr.tick(t)
	if v := dr.one(t, `SELECT status FROM doorstep.booking_assignments WHERE id = $1`, offer); v.(string) != "offered" {
		t.Fatalf("offer %v before expiry", v)
	}
	dr.now = exp.Add(time.Second)
	status, body := dr.pro(au, "POST", "/offers/"+offer.String()+"/accept", "")
	if status != 410 || errCode(body) != "DOORSTEP_OFFER_EXPIRED" {
		t.Fatalf("accept of a lapsed offer: %d %s", status, body)
	}
	dr.tick(t)
	if v := dr.one(t, `SELECT status FROM doorstep.booking_assignments WHERE id = $1`, offer); v.(string) != "expired" {
		t.Fatalf("offer %v after expiry", v)
	}
	dr.assertProUnavailable(t, booking, customer, a, "offer_expired")
	if v := dr.one(t, `SELECT count(*) FROM doorstep.booking_assignments WHERE booking_id = $1 AND pro_id = $2`, booking, b); v.(int64) != 0 {
		t.Fatal("the next professional was offered the job silently")
	}
	if dr.frames.count(service.ProTopic(au), service.FrameOfferClosed) != 1 {
		t.Fatal("no offer.closed frame to the first professional")
	}
}

func TestConcurrentAcceptsOneWins(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "male")
	b, bu := dr.addPro(t, "male")
	booking, _ := dr.confirmed(t, dr.tomorrowAt(13, 0))
	_, firstUser, _, _ := pair(t, dr, booking, a, au, b, bu)
	offer, _ := dr.liveOffer(t, booking)

	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = dr.pro(firstUser, "POST", "/offers/"+offer.String()+"/accept", "")
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != 200 {
			t.Fatalf("accept %d answered %d", i, c)
		}
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.booking_status_history WHERE booking_id = $1 AND to_status = 'assigned'`, booking); v.(int64) != 1 {
		t.Fatalf("%v assigned transitions", v)
	}
	if n := len(dr.events(t, booking, "doorstep.booking.assigned")); n != 1 {
		t.Fatalf("%d assigned events", n)
	}

	// Accept racing an ops redispatch of a fresh booking: whichever wins,
	// exactly one live assignment remains and the states agree.
	booking2, _ := dr.confirmed(t, dr.tomorrowAt(16, 0))
	_, u1, _, _ := pair(t, dr, booking2, a, au, b, bu)
	offer2, _ := dr.liveOffer(t, booking2)
	var acceptCode, adminCode int
	wg.Add(2)
	go func() {
		defer wg.Done()
		acceptCode, _ = dr.pro(u1, "POST", "/offers/"+offer2.String()+"/accept", "")
	}()
	go func() {
		defer wg.Done()
		adminCode, _ = dr.adminCall("POST", "/bookings/"+booking2.String()+"/redispatch", doorstephttp.PermBookingsRedispatch,
			`{"reason":"ops test"}`)
	}()
	wg.Wait()
	if adminCode != 200 && adminCode != 409 {
		t.Fatalf("redispatch answered %d", adminCode)
	}
	if acceptCode != 200 && acceptCode != 409 {
		t.Fatalf("accept answered %d", acceptCode)
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.booking_assignments WHERE booking_id = $1 AND status IN ('offered', 'accepted')`,
		booking2); v.(int64) > 1 {
		t.Fatalf("%v live assignments", v)
	}
	if blocks := dr.activeBlocks(t, booking2); len(blocks) > 1 {
		t.Fatalf("%d active blocks", len(blocks))
	}
}

func TestUnassignedAlertAndTMinus45CancelRefund(t *testing.T) {
	dr := newDispatchRig(t)
	dr.addPro(t, "female")
	start := dr.tomorrowAt(10, 30)
	booking, _ := dr.confirmed(t, start)
	// The professional keeps the offer open (unanswered) up to the slot.
	dr.exec(t, `UPDATE doorstep.booking_assignments SET offer_expires_at = $2 WHERE booking_id = $1 AND status = 'offered'`, booking, start)

	// T-2 h: one alert, never twice.
	dr.now = start.Add(-dispatch.AlertBefore + time.Minute)
	dr.tick(t)
	dr.tick(t)
	tMinus2 := 0
	for _, a := range dr.events(t, booking, "doorstep.booking.unassigned_alert") {
		if strings.Contains(a, `"reason":"t_minus_2h"`) {
			tMinus2++
		}
	}
	if tMinus2 != 1 {
		t.Fatalf("%d T-2h alerts", tMinus2)
	}
	if dr.frames.count(service.AdminLiveTopic, service.FrameAdminUnassigned) < 1 {
		t.Fatal("no ops live frame")
	}
	// T-46: still confirmed.
	dr.now = start.Add(-dispatch.CancelBefore - time.Minute)
	dr.tick(t)
	if s := dr.status(t, booking); s != "confirmed" {
		t.Fatalf("booking %s at T-46", s)
	}
	// T-44: cancelled by the system with a full refund under its own key.
	dr.now = start.Add(-dispatch.CancelBefore + time.Minute)
	dr.tick(t)
	if s := dr.status(t, booking); s != "cancelled" {
		t.Fatalf("booking %s at T-44", s)
	}
	if v := dr.one(t, `SELECT cancelled_by_kind FROM doorstep.bookings WHERE id = $1`, booking); v.(string) != "system" {
		t.Fatalf("cancelled by %v", v)
	}
	key := "doorstep:refund:" + booking.String() + ":unassigned"
	if v := dr.one(t, `SELECT amount_paise FROM doorstep.refunds WHERE booking_id = $1 AND idempotency_key = $2`, booking, key); v.(int64) != dr.world.pricePaise {
		t.Fatalf("refund %v", v)
	}
	if dr.pay.calls(key) != 1 {
		t.Fatal("refund not submitted to payments")
	}
	if blocks := dr.activeBlocks(t, booking); len(blocks) != 0 {
		t.Fatalf("blocks left: %v", blocks)
	}
}

func TestLateNoShowThenCustomerPicksAgain(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "male")
	b, bu := dr.addPro(t, "male")
	start := dr.tomorrowAt(10, 0)
	booking, customer := dr.confirmed(t, start)
	offer, _ := dr.liveOffer(t, booking)
	status, body := dr.pro(au, "POST", "/offers/"+offer.String()+"/accept", "")
	want(t, "accept", status, body, 200)

	// Slot + 16: late; the customer may cancel free.
	dr.now = start.Add(dispatch.LateAfter + time.Minute)
	dr.tick(t)
	if len(dr.events(t, booking, "doorstep.booking.pro_late")) != 1 {
		t.Fatal("no pro_late event")
	}
	status, body = dr.user(customer, "GET", "/bookings/"+booking.String()+"/cancel-preview", "")
	want(t, "preview", status, body, 200)
	if !strings.Contains(string(body), `"fee_paise":0`) || !strings.Contains(string(body), service.RuleProLate) {
		t.Fatalf("late cancel preview: %s", body)
	}
	if dr.frames.count(service.BookingTopic(booking), service.FrameBookingLate) != 1 {
		t.Fatal("no late frame")
	}

	// Slot + 31: no-show (penalty, counter); the customer chooses.
	dr.now = start.Add(dispatch.NoShowAfter + time.Minute)
	dr.tick(t)
	if v := dr.one(t, `SELECT status FROM doorstep.booking_assignments WHERE booking_id = $1 AND pro_id = $2`, booking, a); v.(string) != "no_show" {
		t.Fatalf("first assignment %v", v)
	}
	if v := dr.one(t, `SELECT no_show_count FROM doorstep.professionals WHERE id = $1`, a); v.(int32) != 1 {
		t.Fatalf("no_show_count %v", v)
	}
	if v := dr.one(t, `SELECT amount_paise FROM doorstep.earning_lines WHERE booking_id = $1 AND pro_id = $2 AND kind = 'penalty'`, booking, a); v.(int64) != -dispatch.PenaltyNoShowPaise {
		t.Fatalf("penalty %v", v)
	}
	dr.assertProUnavailable(t, booking, customer, a, "pro_no_show")
	if dr.frames.count(service.ProTopic(au), service.FrameJobRemoved) != 1 {
		t.Fatal("the professional who did not turn up was not told")
	}
	// The customer picks the second professional later today.
	later := start.Add(3 * time.Hour)
	status, body = dr.changePro(customer, booking, b, &later, "after-no-show")
	want(t, "pick", status, body, 200)
	offer2, p2 := dr.liveOffer(t, booking)
	if p2 != b || dr.status(t, booking) != "confirmed" {
		t.Fatal("the picked professional was not offered the job")
	}
	status, body = dr.pro(bu, "POST", "/offers/"+offer2.String()+"/accept", "")
	want(t, "replacement accept", status, body, 200)
}

// No choice within 30 minutes: cancelled with a full refund (not a minute
// before).
func TestProUnavailableTimeoutRefundsInFull(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "female")
	start := dr.tomorrowAt(9, 0)
	booking, customer := dr.confirmed(t, start)
	offer, _ := dr.liveOffer(t, booking)
	status, body := dr.pro(au, "POST", "/offers/"+offer.String()+"/decline", "")
	want(t, "decline", status, body, 204)
	dr.assertProUnavailable(t, booking, customer, a, "declined")
	deadline := dr.now.Add(dispatch.ChoiceWindow)
	dr.now = deadline.Add(-time.Minute)
	dr.tick(t)
	if s := dr.status(t, booking); s != "pro_unavailable" {
		t.Fatalf("booking %s before the deadline", s)
	}
	dr.now = deadline.Add(time.Second)
	if r := dr.tick(t); r.ChoiceTimeouts != 1 {
		t.Fatalf("choice timeouts %d", r.ChoiceTimeouts)
	}
	if s := dr.status(t, booking); s != "cancelled" {
		t.Fatalf("booking %s after the deadline", s)
	}
	key := "doorstep:refund:" + booking.String() + ":pro_unavailable"
	if v := dr.one(t, `SELECT amount_paise FROM doorstep.refunds WHERE idempotency_key = $1`, key); v.(int64) != dr.world.pricePaise {
		t.Fatalf("refund %v", v)
	}
	if dr.pay.calls(key) != 1 {
		t.Fatal("refund not submitted")
	}
	if v := dr.one(t, `SELECT cancelled_by_kind FROM doorstep.bookings WHERE id = $1`, booking); v.(string) != "system" {
		t.Fatalf("cancelled by %v", v)
	}
	// Too late to pick now.
	status, body = dr.changePro(customer, booking, a, &start, "too-late")
	if status != 409 {
		t.Fatalf("pick after the timeout: %d %s", status, body)
	}
}

// The customer may cancel a pro_unavailable booking free, in full.
func TestProUnavailableCustomerCancelIsFree(t *testing.T) {
	dr := newDispatchRig(t)
	_, au := dr.addPro(t, "female")
	booking, customer := dr.confirmed(t, dr.tomorrowAt(16, 0))
	offer, _ := dr.liveOffer(t, booking)
	want(t, "decline", func() int { s, _ := dr.pro(au, "POST", "/offers/"+offer.String()+"/decline", ""); return s }(), nil, 204)
	status, body := dr.user(customer, "GET", "/bookings/"+booking.String()+"/cancel-preview", "")
	if status != 200 || !strings.Contains(string(body), `"rule":"pro_unavailable_free"`) ||
		!strings.Contains(string(body), fmt.Sprintf(`"refund_paise":%d`, dr.world.pricePaise)) {
		t.Fatalf("preview: %d %s", status, body)
	}
	status, body = dr.user(customer, "POST", "/bookings/"+booking.String()+"/cancel", `{"reason":"no thanks"}`)
	want(t, "cancel", status, body, 200)
	if v := dr.one(t, `SELECT sum(amount_paise)::bigint FROM doorstep.refunds WHERE booking_id = $1`, booking); v.(int64) != dr.world.pricePaise {
		t.Fatalf("refund %v", v)
	}
}

func TestNotOnDutyAndProCancelGoProUnavailable(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "male")
	b, bu := dr.addPro(t, "male")
	start := dr.tomorrowAt(14, 0)
	booking, customer := dr.confirmed(t, start)
	offer, _ := dr.liveOffer(t, booking)
	status, body := dr.pro(au, "POST", "/offers/"+offer.String()+"/accept", "")
	want(t, "accept", status, body, 200)

	// On duty at T-89: kept.
	dr.exec(t, `UPDATE doorstep.professionals SET on_duty = TRUE, on_duty_since = $2, last_fix_at = $3 WHERE id = $1`,
		a, start.Add(-3*time.Hour), start.Add(-dispatch.DutyBefore+time.Minute))
	dr.now = start.Add(-dispatch.DutyBefore + time.Minute)
	dr.tick(t)
	if s := dr.status(t, booking); s != "assigned" {
		t.Fatalf("on-duty professional lost the job: %s", s)
	}
	// Off duty: pro_unavailable (no penalty).
	dr.exec(t, `UPDATE doorstep.professionals SET on_duty = FALSE, on_duty_since = NULL WHERE id = $1`, a)
	dr.tick(t)
	dr.assertProUnavailable(t, booking, customer, a, "not_on_duty")
	if dr.frames.count(service.ProTopic(au), service.FrameJobRemoved) != 1 {
		t.Fatal("first professional not told")
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.earning_lines WHERE booking_id = $1 AND pro_id = $2`, booking, a); v.(int64) != 0 {
		t.Fatal("a penalty for not being on duty")
	}
	// The customer picks the second at 17:00; they accept, then give it back
	// (4.5 h out: the 3-24 h tier), counted, and the customer chooses again.
	later := start.Add(3 * time.Hour)
	status, body = dr.changePro(customer, booking, b, &later, "pick-second")
	want(t, "pick", status, body, 200)
	offer2, _ := dr.liveOffer(t, booking)
	status, body = dr.pro(bu, "POST", "/offers/"+offer2.String()+"/accept", "")
	want(t, "accept 2", status, body, 200)
	status, body = dr.pro(bu, "POST", "/jobs/"+booking.String()+"/cancel", `{"reason":"family emergency"}`)
	want(t, "give back", status, body, 204)
	if v := dr.one(t, `SELECT amount_paise FROM doorstep.earning_lines WHERE booking_id = $1 AND pro_id = $2 AND kind = 'penalty'`, booking, b); v.(int64) != -dispatch.PenaltyLowPaise {
		t.Fatalf("penalty %v", v)
	}
	if v := dr.one(t, `SELECT cancellations_count FROM doorstep.professionals WHERE id = $1`, b); v.(int32) != 1 {
		t.Fatalf("cancellations %v", v)
	}
	dr.assertProUnavailable(t, booking, customer, b, "pro_cancel")
	// Someone else's job cannot be given back.
	status, _ = dr.pro(au, "POST", "/jobs/"+booking.String()+"/cancel", `{"reason":"x"}`)
	if status != 404 {
		t.Fatalf("give back of a job not yours: %d", status)
	}
}

// Reschedule keeps the professional the customer picked: never moved to
// someone else silently (B1).
func TestRescheduleKeepsThePickedProfessional(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "female")
	b, _ := dr.addPro(t, "female")
	start := dr.tomorrowAt(10, 0)
	booking, customer := dr.confirmed(t, start)
	offer, _ := dr.liveOffer(t, booking)
	status, body := dr.pro(au, "POST", "/offers/"+offer.String()+"/accept", "")
	want(t, "accept", status, body, 200)

	// The picked professional is busy at 15:00: refused, nobody else held.
	busy := dr.tomorrowAt(15, 0)
	dr.exec(t, `INSERT INTO doorstep.pro_calendar_blocks (pro_id, kind, during) VALUES ($1, 'break', tstzrange($2, $3, '[)'))`,
		a, busy.Add(-30*time.Minute), busy.Add(3*time.Hour))
	status, body = dr.user(customer, "POST", "/bookings/"+booking.String()+"/reschedule",
		fmt.Sprintf(`{"slot_start":"%s"}`, busy.Format(time.RFC3339)))
	if status != 422 || errCode(body) != "DOORSTEP_SLOT_UNAVAILABLE" {
		t.Fatalf("reschedule to a slot only another professional can take: %d %s", status, body)
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.pro_calendar_blocks WHERE pro_id = $1 AND booking_id = $2 AND active`, b, booking); v.(int64) != 0 {
		t.Fatal("another professional was held")
	}
	// A slot they can take: moved, still theirs, still assigned.
	free := dr.tomorrowAt(12, 0)
	status, body = dr.user(customer, "POST", "/bookings/"+booking.String()+"/reschedule",
		fmt.Sprintf(`{"slot_start":"%s"}`, free.Format(time.RFC3339)))
	want(t, "reschedule", status, body, 200)
	if s := dr.status(t, booking); s != "assigned" {
		t.Fatalf("booking %s after a reschedule with the same professional", s)
	}
	if blocks := dr.activeBlocks(t, booking); len(blocks) != 1 || blocks[0] != a {
		t.Fatalf("blocks %v", blocks)
	}
	if dr.frames.count(service.ProTopic(au), service.FrameJobUpdated) != 1 {
		t.Fatal("the professional was not told the slot moved")
	}
}

func TestRealtimeTokenScope(t *testing.T) {
	dr := newDispatchRig(t)
	_, u := dr.addPro(t, "male")
	booking, customer := dr.confirmed(t, dr.tomorrowAt(17, 0))

	status, body := dr.user(customer, "POST", "/realtime/token", fmt.Sprintf(`{"booking_id":"%s"}`, booking))
	want(t, "customer token", status, body, 200)
	var tok struct {
		Token  string   `json:"token"`
		Topics []string `json:"topics"`
	}
	data(t, body, &tok)
	if len(tok.Topics) != 1 || tok.Topics[0] != service.BookingTopic(booking) || !strings.HasSuffix(tok.Token, service.BookingTopic(booking)) {
		t.Fatalf("customer token: %s", body)
	}
	// Someone else's booking: refused as not found.
	status, body = dr.user(uuid.New(), "POST", "/realtime/token", fmt.Sprintf(`{"booking_id":"%s"}`, booking))
	if status != 404 || errCode(body) != "DOORSTEP_BOOKING_NOT_FOUND" {
		t.Fatalf("stranger's token: %d %s", status, body)
	}
	status, _ = dr.user(customer, "POST", "/realtime/token", `{}`)
	want(t, "no booking id", status, nil, 400)

	// The professional: their own topic; the booking only once accepted.
	status, body = dr.pro(u, "POST", "/realtime/token", "")
	want(t, "pro token", status, body, 200)
	data(t, body, &tok)
	if len(tok.Topics) != 1 || tok.Topics[0] != service.ProTopic(u) {
		t.Fatalf("pro token before accept: %s", body)
	}
	offer, _ := dr.liveOffer(t, booking)
	status, body = dr.pro(u, "POST", "/offers/"+offer.String()+"/accept", "")
	want(t, "accept", status, body, 200)
	status, body = dr.pro(u, "POST", "/realtime/token", "")
	want(t, "pro token", status, body, 200)
	data(t, body, &tok)
	if len(tok.Topics) != 2 || tok.Topics[1] != service.BookingTopic(booking) {
		t.Fatalf("pro token after accept: %s", body)
	}
	// Not a professional: refused.
	status, body = dr.pro(customer, "POST", "/realtime/token", "")
	if status != 404 || errCode(body) != "DOORSTEP_PRO_NOT_FOUND" {
		t.Fatalf("non-professional token: %d %s", status, body)
	}
}

// completePro finishes every onboarding step for a professional addPro made.
func (dr *dsRig) completePro(t *testing.T, pro uuid.UUID) {
	t.Helper()
	dr.exec(t, `UPDATE doorstep.professionals SET photo_media_id = $2, agreement_version = '2026-10-04' WHERE id = $1`, pro, uuid.NewString())
	// A passed selfie is an admin's decision (audited in its transaction).
	adminTx(t, dr.p, func(exec func(string, ...any)) {
		exec(`INSERT INTO doorstep.pro_kyc_checks (pro_id, kind, status, verified_at) VALUES ($1, 'digilocker_aadhaar', 'passed', NOW()),
			($1, 'selfie_face_match', 'passed', NOW())`, pro)
	})
	dr.exec(t, `INSERT INTO doorstep.pro_payout_accounts (pro_id, account_holder, account_number_sealed, account_last4, ifsc, key_version)
		VALUES ($1, 'Asha Rao', '\x00', '1234', 'HDFC0001234', '1')`, pro)
}

func TestDutyLocationAndStaleFix(t *testing.T) {
	dr := newDispatchRig(t)
	pro, u := dr.addPro(t, "female")

	// Location before duty: refused.
	status, body := dr.pro(u, "POST", "/location", `{"lat":17.45,"lng":78.38}`)
	if status != 409 || errCode(body) != "DOORSTEP_NOT_ON_DUTY" {
		t.Fatalf("location off duty: %d %s", status, body)
	}
	// Onboarding incomplete: refused.
	status, body = dr.pro(u, "POST", "/duty/on", "")
	if status != 422 || errCode(body) != "DOORSTEP_ONBOARDING_INCOMPLETE" {
		t.Fatalf("duty on incomplete: %d %s", status, body)
	}
	dr.completePro(t, pro)
	// Suspended: refused.
	dr.exec(t, `UPDATE doorstep.professionals SET incident_suspended = TRUE WHERE id = $1`, pro)
	status, body = dr.pro(u, "POST", "/duty/on", "")
	if status != 403 || errCode(body) != "DOORSTEP_PRO_SUSPENDED" {
		t.Fatalf("duty on suspended: %d %s", status, body)
	}
	dr.exec(t, `UPDATE doorstep.professionals SET incident_suspended = FALSE WHERE id = $1`, pro)

	status, body = dr.pro(u, "POST", "/duty/on", `{"lat":17.45,"lng":78.38,"accuracy_m":12}`)
	want(t, "duty on", status, body, 200)
	if !strings.Contains(string(body), `"on_duty":true`) {
		t.Fatalf("duty: %s", body)
	}
	status, body = dr.pro(u, "GET", "/me/duty", "")
	if status != 200 || !strings.Contains(string(body), `"on_duty":true`) {
		t.Fatalf("duty read: %s", body)
	}
	status, _ = dr.pro(u, "POST", "/location", `{"lat":17.451,"lng":78.381}`)
	want(t, "location", status, nil, 204)
	if v := dr.one(t, `SELECT round(ST_Y(last_point::geometry)::numeric, 3)::float8 FROM doorstep.professionals WHERE id = $1`, pro); v.(float64) != 17.451 {
		t.Fatalf("last point %v", v)
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.pro_duty_sessions WHERE pro_id = $1 AND ended_at IS NULL`, pro); v.(int64) != 1 {
		t.Fatalf("%v open duty sessions", v)
	}
	// Silent for more than five minutes: off duty.
	dr.now = dr.now.Add(dispatch.StaleFix + time.Minute)
	dr.tick(t)
	if v := dr.one(t, `SELECT on_duty FROM doorstep.professionals WHERE id = $1`, pro); v.(bool) {
		t.Fatal("stale professional still on duty")
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.pro_duty_sessions WHERE pro_id = $1 AND ended_at IS NULL`, pro); v.(int64) != 0 {
		t.Fatal("duty session left open")
	}
	if dr.frames.count(service.ProTopic(u), service.FrameDuty) != 2 {
		t.Fatalf("duty frames: %+v", dr.frames.all())
	}
	// Profile reads.
	for _, p := range []string{"/zones", "/me/skills", "/me/area", "/me/bank", "/me/documents"} {
		status, body := dr.pro(u, "GET", p, "")
		want(t, p, status, body, 200)
	}
	status, body = dr.pro(u, "GET", "/me/bank", "")
	if !strings.Contains(string(body), `"account_last4":"1234"`) || strings.Contains(string(body), "sealed") {
		t.Fatalf("bank: %s", body)
	}
}

func TestAdminRedispatchGivesTheCustomerTheChoice(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "male")
	b, _ := dr.addPro(t, "male")
	booking, customer := dr.confirmed(t, dr.tomorrowAt(18, 0))
	offer, _ := dr.liveOffer(t, booking)
	status, body := dr.pro(au, "POST", "/offers/"+offer.String()+"/accept", "")
	want(t, "accept", status, body, 200)

	// Ops cannot name who gets the job.
	status, body = dr.adminCall("POST", "/bookings/"+booking.String()+"/redispatch", doorstephttp.PermBookingsRedispatch,
		fmt.Sprintf(`{"reason":"customer complaint","pro_id":"%s"}`, b))
	if status != 400 {
		t.Fatalf("hand-pick accepted: %d %s", status, body)
	}
	// Ops take it off the first and exclude the second: the customer picks
	// (nobody is offered it by ops).
	status, body = dr.adminCall("POST", "/bookings/"+booking.String()+"/redispatch", doorstephttp.PermBookingsRedispatch,
		fmt.Sprintf(`{"reason":"customer complaint","exclude_pro_ids":["%s"]}`, b))
	want(t, "redispatch", status, body, 200)
	if !strings.Contains(string(body), `"status":"pro_unavailable"`) || strings.Contains(string(body), `"start_otp":"`) {
		t.Fatalf("redispatch answer: %s", body)
	}
	dr.assertProUnavailable(t, booking, customer, a, "ops_redispatch")
	if v := dr.one(t, `SELECT $2 = ANY(excluded_pro_ids) FROM doorstep.bookings WHERE id = $1`, booking, b); v.(bool) != true {
		t.Fatal("ops' exclusion not kept")
	}
	// Audited in the same transaction as the change.
	if v := dr.one(t, `SELECT count(*) FROM doorstep.admin_audit_log a JOIN doorstep.bookings b ON b.id::text = a.entity_id
		WHERE a.action = 'booking.redispatch' AND a.entity_id = $1 AND a.actor_user_id = $2`, booking.String(), dr.actor); v.(int64) != 1 {
		t.Fatalf("%v audit rows", v)
	}
	// Excluded professionals are not listed for the customer.
	status, body = dr.user(customer, "GET", "/bookings/"+booking.String()+"/professionals", "")
	want(t, "alternatives", status, body, 200)
	if strings.Contains(string(body), a.String()) || strings.Contains(string(body), b.String()) {
		t.Fatalf("excluded professionals listed: %s", body)
	}
	// Wrong permission: refused.
	status, _ = dr.adminCall("POST", "/bookings/"+booking.String()+"/redispatch", doorstephttp.PermBookingsCancel, `{"reason":"x"}`)
	if status != 403 {
		t.Fatalf("redispatch with bookings.cancel: %d", status)
	}
}

// A dearer professional: held while the difference is paid through
// payments-service (doorstep_extras, key doorstep:extras:{bill}); the signed
// capture applies the change once; money for a lapsed change is refunded.
func TestDearerChangeChargesTheDifference(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "female")
	b, bu := dr.addPro(t, "female")
	dr.exec(t, `UPDATE doorstep.pro_service_prices SET price_paise = price_paise + 20000 WHERE pro_id = $1`, b)
	start := dr.tomorrowAt(13, 0)
	booking, customer := dr.confirmed(t, start)
	offer, _ := dr.liveOffer(t, booking)
	want(t, "decline", func() int { s, _ := dr.pro(au, "POST", "/offers/"+offer.String()+"/decline", ""); return s }(), nil, 204)

	status, body := dr.changePro(customer, booking, b, &start, "dearer")
	want(t, "dearer change", status, body, 200)
	if !strings.Contains(string(body), `"status":"pending_payment"`) || !strings.Contains(string(body), `"difference_paise":20000`) ||
		!strings.Contains(string(body), `"reference_type":"doorstep_extras"`) {
		t.Fatalf("dearer change: %s", body)
	}
	if s := dr.status(t, booking); s != "pro_unavailable" {
		t.Fatalf("booking %s before the difference is paid", s)
	}
	var bill, change uuid.UUID
	if err := dr.p.QueryRow(context.Background(), `SELECT extras_bill_id, id FROM doorstep.booking_pro_changes WHERE booking_id = $1
		AND status = 'pending_payment'`, booking).Scan(&bill, &change); err != nil {
		t.Fatal(err)
	}
	if v := dr.one(t, `SELECT kind FROM doorstep.pro_calendar_blocks WHERE booking_id = $1 AND pro_id = $2 AND active`, booking, b); v.(string) != "hold" {
		t.Fatalf("block %v", v)
	}
	intent, ok := dr.pay.intents[payments.ExtrasIntentKey(bill)]
	if !ok {
		t.Fatal("no intent on doorstep:extras:{bill}")
	}
	// The customer's view carries the pending change and its checkout.
	status, body = dr.user(customer, "GET", "/bookings/"+booking.String(), "")
	if status != 200 || !strings.Contains(string(body), `"pending_change":{"id":"`+change.String()) {
		t.Fatalf("booking view: %s", body)
	}
	// The window does not close on a change being paid for.
	dr.now = dr.now.Add(dispatch.ChoiceWindow + time.Second)
	if v := dr.one(t, `SELECT hold_expires_at > $2 FROM doorstep.booking_pro_changes WHERE id = $1`, change, dr.now); v.(bool) {
		dr.tick(t)
		if s := dr.status(t, booking); s != "pro_unavailable" {
			t.Fatalf("timed out while the change was being paid: %s", s)
		}
	}
	dr.now = dr.now.Add(-(dispatch.ChoiceWindow + time.Second))

	// A capture for another amount: flagged, never applied.
	extras := func(eventID string, amount int64) {
		dr.event(t, eventID, events.EventPaymentSucceeded, map[string]any{"id": intent.ID.String(), "payer_id": customer.String(),
			"payee_id": intent.PayeeID.String(), "reference_type": payments.RefExtras, "reference_id": bill.String(),
			"amount_minor": amount, "currency": "INR", "method": "upi", "status": "succeeded", "provider_ref": "pay_it_change",
			"application_id": payments.ApplicationID})
	}
	extras("evt-change-bad", 19999)
	if s := dr.status(t, booking); s != "pro_unavailable" {
		t.Fatalf("a mismatched capture applied the change: %s", s)
	}
	extras("evt-change-ok", 20000)
	extras("evt-change-ok", 20000) // a duplicate is a no-op
	if s := dr.status(t, booking); s != "confirmed" {
		t.Fatalf("booking %s after the difference was paid", s)
	}
	if v := dr.one(t, `SELECT paid_paise FROM doorstep.bookings WHERE id = $1`, booking); v.(int64) != dr.world.pricePaise+20000 {
		t.Fatalf("paid %v", v)
	}
	if v := dr.one(t, `SELECT kind FROM doorstep.pro_calendar_blocks WHERE booking_id = $1 AND pro_id = $2 AND active`, booking, b); v.(string) != "booking" {
		t.Fatalf("block %v after payment", v)
	}
	if _, p := dr.liveOffer(t, booking); p != b {
		t.Fatal("the picked professional was not offered the job after payment")
	}
	if ev := dr.events(t, booking, "doorstep.booking.pro_changed"); len(ev) != 1 || !strings.Contains(ev[0], bu.String()) ||
		!strings.Contains(ev[0], `"difference_paise":20000`) {
		t.Fatalf("pro_changed: %v", ev)
	}
	// Cancelling now refunds both payments in full.
	status, body = dr.user(customer, "POST", "/bookings/"+booking.String()+"/cancel", `{"reason":"changed my mind"}`)
	want(t, "cancel", status, body, 200)
	if v := dr.one(t, `SELECT count(DISTINCT payment_id) FROM doorstep.refunds WHERE booking_id = $1 AND cause = 'customer_cancel'`, booking); v.(int64) != 2 {
		t.Fatalf("refunds over %v payments", v)
	}
	if v := dr.one(t, `SELECT sum(amount_paise)::bigint FROM doorstep.refunds WHERE booking_id = $1 AND cause = 'customer_cancel'`, booking); v.(int64) != dr.world.pricePaise+20000 {
		t.Fatalf("refunded %v", v)
	}
	_ = a
}

// The move is one transaction: a failure after the old block is released
// (here the next "professional" does not exist) rolls everything back —
// the open offer and the first professional's block are untouched.
func TestRedispatchFailureRollsBackTheMove(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "female")
	b, bu := dr.addPro(t, "female")
	start := dr.tomorrowAt(15, 30)
	booking, _ := dr.confirmed(t, start)
	first, _, _, _ := pair(t, dr, booking, a, au, b, bu)
	offer, _ := dr.liveOffer(t, booking)
	_, err := dr.st.Redispatch(context.Background(), store.Redispatch{BookingID: booking, From: []string{"confirmed"},
		Close:      &store.CloseAssignment{ID: offer, ProID: first, From: []string{"offered"}, To: "declined"},
		Candidates: []uuid.UUID{uuid.New()}, Start: start, End: start.Add(time.Hour), BlockEnd: start.Add(90 * time.Minute),
		OfferExpiresAt: dr.now.Add(10 * time.Minute), ActorKind: "system", At: dr.now})
	if err == nil {
		t.Fatal("a hold for a professional who does not exist succeeded")
	}
	if blocks := dr.activeBlocks(t, booking); len(blocks) != 1 || blocks[0] != first {
		t.Fatalf("blocks after a failed move: %v", blocks)
	}
	if o, p := dr.liveOffer(t, booking); o != offer || p != first {
		t.Fatal("the open offer did not survive the failed move")
	}
}

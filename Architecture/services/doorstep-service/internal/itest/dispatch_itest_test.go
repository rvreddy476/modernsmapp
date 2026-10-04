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
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/store"
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

func TestDeclineMovesTheBlockAtomically(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "male")
	b, bu := dr.addPro(t, "male")
	booking, _ := dr.confirmed(t, dr.tomorrowAt(11, 0))
	first, firstUser, second, secondUser := pair(t, dr, booking, a, au, b, bu)
	offer, _ := dr.liveOffer(t, booking)

	status, body := dr.pro(firstUser, "POST", "/offers/"+offer.String()+"/decline", `{"reason":"too_far"}`)
	want(t, "decline", status, body, 204)
	// One transaction: the first professional's block released, the
	// second's inserted, the offer moved.
	if blocks := dr.activeBlocks(t, booking); len(blocks) != 1 || blocks[0] != second {
		t.Fatalf("active blocks after decline: %v", blocks)
	}
	if r := reservedOf(t, dr, booking); r == nil || *r != second {
		t.Fatal("reserved professional not moved")
	}
	offer2, pro2 := dr.liveOffer(t, booking)
	if pro2 != second {
		t.Fatal("next offer not to the second professional")
	}
	if v := dr.one(t, `SELECT decline_reason FROM doorstep.booking_assignments WHERE id = $1`, offer); v.(string) != "too_far" {
		t.Fatalf("decline reason %v", v)
	}
	// Declining twice is a no-op; the declined professional cannot accept.
	status, _ = dr.pro(firstUser, "POST", "/offers/"+offer.String()+"/decline", "")
	want(t, "decline replay", status, nil, 204)
	status, body = dr.pro(firstUser, "POST", "/offers/"+offer.String()+"/accept", "")
	if status != 409 || errCode(body) != "DOORSTEP_OFFER_TAKEN" {
		t.Fatalf("accept after decline: %d %s", status, body)
	}
	status, body = dr.pro(firstUser, "GET", "/offers/"+offer.String(), "")
	if status != 200 || !strings.Contains(string(body), `"status":"declined"`) {
		t.Fatalf("declined offer read: %d %s", status, body)
	}

	// The second declines: nobody left. Confirmed, no professional, no
	// block, ops alerted once.
	status, body = dr.pro(secondUser, "POST", "/offers/"+offer2.String()+"/decline", "")
	want(t, "second decline", status, body, 204)
	if s := dr.status(t, booking); s != "confirmed" {
		t.Fatalf("booking %s", s)
	}
	if blocks := dr.activeBlocks(t, booking); len(blocks) != 0 {
		t.Fatalf("blocks left: %v", blocks)
	}
	if r := reservedOf(t, dr, booking); r != nil {
		t.Fatal("a professional is still reserved")
	}
	alerts := dr.events(t, booking, "doorstep.booking.unassigned_alert")
	if len(alerts) != 1 || !strings.Contains(alerts[0], `"reason":"no_professional_left"`) {
		t.Fatalf("alerts: %v", alerts)
	}
	closed := dr.events(t, booking, "doorstep.pro.offer_closed")
	if len(closed) != 2 || !strings.Contains(closed[0], `"outcome":"declined"`) {
		t.Fatalf("offer_closed: %v", closed)
	}
	// The retry worker finds nobody either and does not alert again.
	dr.now = dr.now.Add(dispatch.RetryEvery + time.Minute)
	dr.tick(t)
	if n := len(dr.events(t, booking, "doorstep.booking.unassigned_alert")); n != 1 {
		t.Fatalf("%d alerts after retry", n)
	}
	_ = first
}

func TestOfferExpiryWorker(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "female")
	b, bu := dr.addPro(t, "female")
	booking, _ := dr.confirmed(t, dr.tomorrowAt(12, 0))
	_, firstUser, second, _ := pair(t, dr, booking, a, au, b, bu)
	offer, _ := dr.liveOffer(t, booking)
	exp := dr.one(t, `SELECT offer_expires_at FROM doorstep.booking_assignments WHERE id = $1`, offer).(time.Time)

	// Not yet due: nothing happens.
	dr.now = exp.Add(-time.Second)
	dr.tick(t)
	if v := dr.one(t, `SELECT status FROM doorstep.booking_assignments WHERE id = $1`, offer); v.(string) != "offered" {
		t.Fatalf("offer %v before expiry", v)
	}
	// Due: expired, next professional offered, block moved.
	dr.now = exp.Add(time.Second)
	status, body := dr.pro(firstUser, "POST", "/offers/"+offer.String()+"/accept", "")
	if status != 410 || errCode(body) != "DOORSTEP_OFFER_EXPIRED" {
		t.Fatalf("accept of a lapsed offer: %d %s", status, body)
	}
	dr.tick(t)
	if v := dr.one(t, `SELECT status FROM doorstep.booking_assignments WHERE id = $1`, offer); v.(string) != "expired" {
		t.Fatalf("offer %v after expiry", v)
	}
	if _, p := dr.liveOffer(t, booking); p != second {
		t.Fatal("expired offer not passed on")
	}
	if blocks := dr.activeBlocks(t, booking); len(blocks) != 1 || blocks[0] != second {
		t.Fatalf("blocks %v", blocks)
	}
	if dr.frames.count(service.ProTopic(firstUser), service.FrameOfferClosed) != 1 {
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

	// T-2 h: one alert, never twice.
	dr.now = start.Add(-dispatch.AlertBefore + time.Minute)
	dr.tick(t)
	dr.tick(t)
	// (The lone professional's offer lapsed on the way, so dispatch also
	// raised its own "nobody left" alert.)
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

func TestLateNoShowAndReassign(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "male")
	b, bu := dr.addPro(t, "male")
	start := dr.tomorrowAt(10, 0)
	booking, customer := dr.confirmed(t, start)
	first, firstUser, second, secondUser := pair(t, dr, booking, a, au, b, bu)
	offer, _ := dr.liveOffer(t, booking)
	status, body := dr.pro(firstUser, "POST", "/offers/"+offer.String()+"/accept", "")
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

	// Slot + 31: no-show. Penalty and counter; the second professional can
	// start within the hour: offered, slot moved, rescue deadline set.
	dr.now = start.Add(dispatch.NoShowAfter + time.Minute)
	dr.tick(t)
	if v := dr.one(t, `SELECT status FROM doorstep.booking_assignments WHERE booking_id = $1 AND pro_id = $2`, booking, first); v.(string) != "no_show" {
		t.Fatalf("first assignment %v", v)
	}
	if v := dr.one(t, `SELECT no_show_count FROM doorstep.professionals WHERE id = $1`, first); v.(int32) != 1 {
		t.Fatalf("no_show_count %v", v)
	}
	if v := dr.one(t, `SELECT amount_paise FROM doorstep.earning_lines WHERE booking_id = $1 AND pro_id = $2 AND kind = 'penalty'`, booking, first); v.(int64) != -dispatch.PenaltyNoShowPaise {
		t.Fatalf("penalty %v", v)
	}
	if s := dr.status(t, booking); s != "confirmed" {
		t.Fatalf("booking %s after no-show", s)
	}
	offer2, p2 := dr.liveOffer(t, booking)
	if p2 != second {
		t.Fatal("replacement not offered")
	}
	newStart := dr.one(t, `SELECT slot_start FROM doorstep.bookings WHERE id = $1`, booking).(time.Time)
	if !newStart.After(dr.now) || newStart.After(dr.now.Add(dispatch.RescueWithin)) {
		t.Fatalf("rescue slot %v (now %v)", newStart, dr.now)
	}
	re := dr.events(t, booking, "doorstep.booking.reassigned")
	if len(re) != 1 || !strings.Contains(re[0], `"cause":"pro_no_show"`) || !strings.Contains(re[0], firstUser.String()) {
		t.Fatalf("reassigned: %v", re)
	}
	status, body = dr.pro(secondUser, "POST", "/offers/"+offer2.String()+"/accept", "")
	want(t, "replacement accept", status, body, 200)
	if v := dr.one(t, `SELECT rescue_until IS NULL FROM doorstep.bookings WHERE id = $1`, booking); v.(bool) != true {
		t.Fatal("rescue not cleared on accept")
	}
}

func TestNoShowWithoutReplacementRefunds(t *testing.T) {
	dr := newDispatchRig(t)
	_, u := dr.addPro(t, "female")
	start := dr.tomorrowAt(9, 0)
	booking, _ := dr.confirmed(t, start)
	offer, _ := dr.liveOffer(t, booking)
	status, body := dr.pro(u, "POST", "/offers/"+offer.String()+"/accept", "")
	want(t, "accept", status, body, 200)
	dr.now = start.Add(dispatch.NoShowAfter + time.Minute)
	dr.tick(t)
	if s := dr.status(t, booking); s != "pro_no_show" {
		t.Fatalf("booking %s", s)
	}
	key := "doorstep:refund:" + booking.String() + ":pro_no_show"
	if v := dr.one(t, `SELECT amount_paise FROM doorstep.refunds WHERE idempotency_key = $1`, key); v.(int64) != dr.world.pricePaise {
		t.Fatalf("refund %v", v)
	}
	ns := dr.events(t, booking, "doorstep.booking.no_show")
	if len(ns) != 1 || !strings.Contains(ns[0], `"party":"pro"`) {
		t.Fatalf("no_show events: %v", ns)
	}
	if len(dr.events(t, booking, "doorstep.booking.reassigned")) != 0 {
		t.Fatal("reassigned with nobody to take it")
	}
}

func TestNotOnDutyReassignsAndProCancelPenalty(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "male")
	b, bu := dr.addPro(t, "male")
	start := dr.tomorrowAt(14, 0)
	booking, _ := dr.confirmed(t, start)
	first, firstUser, second, secondUser := pair(t, dr, booking, a, au, b, bu)
	offer, _ := dr.liveOffer(t, booking)
	status, body := dr.pro(firstUser, "POST", "/offers/"+offer.String()+"/accept", "")
	want(t, "accept", status, body, 200)

	// On duty at T-89: kept.
	dr.exec(t, `UPDATE doorstep.professionals SET on_duty = TRUE, on_duty_since = $2, last_fix_at = $3 WHERE id = $1`,
		first, start.Add(-3*time.Hour), start.Add(-dispatch.DutyBefore+time.Minute))
	dr.now = start.Add(-dispatch.DutyBefore + time.Minute)
	dr.tick(t)
	if s := dr.status(t, booking); s != "assigned" {
		t.Fatalf("on-duty professional lost the job: %s", s)
	}
	// Off duty: reassigned (no penalty), the second professional offered.
	dr.exec(t, `UPDATE doorstep.professionals SET on_duty = FALSE, on_duty_since = NULL WHERE id = $1`, first)
	dr.tick(t)
	if s := dr.status(t, booking); s != "confirmed" {
		t.Fatalf("booking %s", s)
	}
	re := dr.events(t, booking, "doorstep.booking.reassigned")
	if len(re) != 1 || !strings.Contains(re[0], `"cause":"not_on_duty"`) || !strings.Contains(re[0], firstUser.String()) {
		t.Fatalf("reassigned: %v", re)
	}
	if dr.frames.count(service.ProTopic(firstUser), service.FrameJobRemoved) != 1 {
		t.Fatal("first professional not told")
	}
	offer2, p2 := dr.liveOffer(t, booking)
	if p2 != second {
		t.Fatal("second not offered")
	}
	status, body = dr.pro(secondUser, "POST", "/offers/"+offer2.String()+"/accept", "")
	want(t, "accept 2", status, body, 200)

	// The second gives it back 89 min before: the under-3 h tier, counted,
	// and nobody left (the first was excluded).
	status, body = dr.pro(secondUser, "POST", "/jobs/"+booking.String()+"/cancel", `{"reason":"family emergency"}`)
	want(t, "give back", status, body, 204)
	if v := dr.one(t, `SELECT amount_paise FROM doorstep.earning_lines WHERE booking_id = $1 AND pro_id = $2 AND kind = 'penalty'`, booking, second); v.(int64) != -dispatch.PenaltyHighPaise {
		t.Fatalf("penalty %v", v)
	}
	if v := dr.one(t, `SELECT cancellations_count FROM doorstep.professionals WHERE id = $1`, second); v.(int32) != 1 {
		t.Fatalf("cancellations %v", v)
	}
	if s := dr.status(t, booking); s != "confirmed" {
		t.Fatalf("booking %s", s)
	}
	// Someone else's job cannot be given back.
	status, _ = dr.pro(firstUser, "POST", "/jobs/"+booking.String()+"/cancel", `{"reason":"x"}`)
	if status != 404 {
		t.Fatalf("give back of a job not yours: %d", status)
	}
}

func TestRescheduleToAnotherProReoffers(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "female")
	b, bu := dr.addPro(t, "female")
	start := dr.tomorrowAt(10, 0)
	booking, customer := dr.confirmed(t, start)
	first, firstUser, second, _ := pair(t, dr, booking, a, au, b, bu)
	offer, _ := dr.liveOffer(t, booking)
	status, body := dr.pro(firstUser, "POST", "/offers/"+offer.String()+"/accept", "")
	want(t, "accept", status, body, 200)

	// The first professional is busy at the new slot.
	newStart := dr.tomorrowAt(15, 0)
	dr.exec(t, `INSERT INTO doorstep.pro_calendar_blocks (pro_id, kind, during) VALUES ($1, 'break', tstzrange($2, $3, '[)'))`,
		first, newStart.Add(-30*time.Minute), newStart.Add(3*time.Hour))
	status, body = dr.user(customer, "POST", "/bookings/"+booking.String()+"/reschedule",
		fmt.Sprintf(`{"slot_start":"%s"}`, newStart.Format(time.RFC3339)))
	want(t, "reschedule", status, body, 200)
	if s := dr.status(t, booking); s != "confirmed" {
		t.Fatalf("booking %s after moving to another professional", s)
	}
	if _, p := dr.liveOffer(t, booking); p != second {
		t.Fatal("not re-offered to the professional holding the new slot")
	}
	re := dr.events(t, booking, "doorstep.booking.reassigned")
	if len(re) != 1 || !strings.Contains(re[0], `"cause":"rescheduled"`) || !strings.Contains(re[0], `"previous_pro_user_id":"`+firstUser.String()) {
		t.Fatalf("reassigned: %v", re)
	}
	if dr.frames.count(service.ProTopic(firstUser), service.FrameJobRemoved) != 1 {
		t.Fatal("previous professional not told live")
	}
	if blocks := dr.activeBlocks(t, booking); len(blocks) != 1 || blocks[0] != second {
		t.Fatalf("blocks %v", blocks)
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
	dr.exec(t, `INSERT INTO doorstep.pro_kyc_checks (pro_id, kind, status, verified_at) VALUES ($1, 'digilocker_aadhaar', 'passed', NOW()),
		($1, 'selfie_face_match', 'passed', NOW())`, pro)
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

func TestAdminRedispatchExcludesAndAudits(t *testing.T) {
	dr := newDispatchRig(t)
	a, au := dr.addPro(t, "male")
	b, bu := dr.addPro(t, "male")
	booking, _ := dr.confirmed(t, dr.tomorrowAt(18, 0))
	_, firstUser, second, _ := pair(t, dr, booking, a, au, b, bu)
	offer, _ := dr.liveOffer(t, booking)
	status, body := dr.pro(firstUser, "POST", "/offers/"+offer.String()+"/accept", "")
	want(t, "accept", status, body, 200)

	// Ops cannot name who gets the job.
	status, body = dr.adminCall("POST", "/bookings/"+booking.String()+"/redispatch", doorstephttp.PermBookingsRedispatch,
		fmt.Sprintf(`{"reason":"customer complaint","pro_id":"%s"}`, second))
	if status != 400 {
		t.Fatalf("hand-pick accepted: %d %s", status, body)
	}
	// Excluding the only other professional: nobody left.
	status, body = dr.adminCall("POST", "/bookings/"+booking.String()+"/redispatch", doorstephttp.PermBookingsRedispatch,
		fmt.Sprintf(`{"reason":"customer complaint","exclude_pro_ids":["%s"]}`, second))
	want(t, "redispatch", status, body, 200)
	if !strings.Contains(string(body), `"status":"confirmed"`) || strings.Contains(string(body), `"start_otp":"`) {
		t.Fatalf("redispatch answer: %s", body)
	}
	if blocks := dr.activeBlocks(t, booking); len(blocks) != 0 {
		t.Fatalf("blocks %v", blocks)
	}
	if v := dr.one(t, `SELECT count(*) FROM doorstep.admin_audit_log WHERE action = 'booking.redispatch' AND entity_id = $1`, booking.String()); v.(int64) != 1 {
		t.Fatalf("%v audit rows", v)
	}
	re := dr.events(t, booking, "doorstep.booking.reassigned")
	if len(re) != 1 || !strings.Contains(re[0], `"cause":"ops_redispatch"`) {
		t.Fatalf("reassigned: %v", re)
	}
	// Wrong permission: refused.
	status, _ = dr.adminCall("POST", "/bookings/"+booking.String()+"/redispatch", doorstephttp.PermBookingsCancel, `{"reason":"x"}`)
	if status != 403 {
		t.Fatalf("redispatch with bookings.cancel: %d", status)
	}
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

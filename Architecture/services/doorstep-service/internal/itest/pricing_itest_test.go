package itest

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	doorstephttp "github.com/atpost/doorstep-service/internal/http"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// B1 on the real store (4 Oct 2026): professionals' own prices reviewed by
// an admin, the customer's professionals list (every hard filter, ASAP),
// and the database guards — only approved prices are quoted or booked, and
// nothing becomes approved or verified without an audited admin review.

// seededPro inserts an approved professional for a SEEDED service (both
// seeded zones, every day 08:00-20:00, clear background check), with an
// admin-approved price for each item ("option:<key>" or "addon:<key>").
func seededPro(t *testing.T, p *pgxpool.Pool, gender, skill string, lat, lng float64, service string, prices map[string]int64) uuid.UUID {
	t.Helper()
	pro := uuid.New()
	adminTx(t, p, func(exec func(string, ...any)) {
		exec(`INSERT INTO doorstep.professionals (id, user_id, status, display_name, city_code, gender, gender_source, home_point,
			service_radius_m, approved_at, approved_by) VALUES ($1, $2, 'approved', 'Seeded Pro', 'HYD', $3, 'digilocker',
			ST_SetSRID(ST_MakePoint($5, $4), 4326)::geography, 15000, NOW(), $6)`, pro, uuid.New(), gender, lat, lng, itAdmin)
		exec(`INSERT INTO doorstep.pro_skills (pro_id, skill_code, status, verified_at, verified_by) VALUES ($1, $2, 'verified', NOW(), $3)
			ON CONFLICT DO NOTHING`, pro, skill, itAdmin)
		for _, z := range []string{"west-hitec-gachibowli", "central-banjara-jubilee"} {
			exec(`INSERT INTO doorstep.pro_zones (pro_id, zone_id) VALUES ($1, $2)`, pro, id("zone", z))
		}
		for d := 0; d < 7; d++ {
			exec(`INSERT INTO doorstep.pro_weekly_hours (pro_id, weekday, start_time, end_time) VALUES ($1, $2, '08:00', '20:00')`, pro, d)
		}
		doc := uuid.New()
		exec(`INSERT INTO doorstep.pro_documents (id, pro_id, kind, media_id, status, issued_on, reviewed_by, reviewed_at)
			VALUES ($1, $2, 'police_certificate', $3, 'approved', CURRENT_DATE - 10, $4, NOW())`, doc, pro, uuid.NewString(), itAdmin)
		exec(`INSERT INTO doorstep.background_checks (pro_id, source, document_id, status, valid_from, valid_until, reviewed_by)
			VALUES ($1, 'uploaded_document', $2, 'clear', CURRENT_DATE - 10, CURRENT_DATE + 355, $3)`, pro, doc, itAdmin)
		for key, paise := range prices {
			kind, item, _ := strings.Cut(key, ":")
			col := "option_id"
			if kind == "addon" {
				col = "addon_id"
			}
			exec(`INSERT INTO doorstep.pro_service_prices (pro_id, service_id, item_kind, `+col+`, unit, price_paise, status, effective_from,
				submitted_at, reviewed_by, reviewed_at) VALUES ($1, $2, $3, $4, 'per_job', $5, 'approved', NOW() - INTERVAL '30 days',
				NOW() - INTERVAL '30 days', $6, NOW())`, pro, service, kind, id(kind, item), paise, itAdmin)
		}
	})
	return pro
}

// listPath is the professionals list for the suite's any-gender (or women's)
// service at the customer's address.
func (dr *dsRig) listPath(women bool, address uuid.UUID, extra string) string {
	svc, opt := dr.world.anyService, dr.world.anyOption
	if women {
		svc, opt = dr.world.womenService, dr.world.womenOption
	}
	q := url.Values{}
	q.Set("option_id", opt.String())
	q.Set("address_id", address.String())
	s := "/services/" + svc.String() + "/professionals?" + q.Encode()
	if extra != "" {
		s += "&" + extra
	}
	return s
}

func (dr *dsRig) address(t *testing.T, customer uuid.UUID) uuid.UUID {
	t.Helper()
	status, body := dr.user(customer, "POST", "/addresses", fmt.Sprintf(
		`{"label":"Home","line1":"Flat 4B, Cyber Residency","locality":"HITEC City","pincode":"500081","lat":%f,"lng":%f}`,
		dr.world.lat, dr.world.lng))
	want(t, "address", status, body, 201)
	var a struct {
		ID uuid.UUID `json:"id"`
	}
	data(t, body, &a)
	return a.ID
}

// Price submitted by the professional -> pending (never listed, quoted or
// booked) -> an admin approves (audited in the same transaction) -> listed,
// quoted and bookable. A changed price waits while the old one stays live.
func TestPriceSubmitReviewBookable(t *testing.T) {
	dr := newDispatchRig(t)
	pro, pu := dr.addPro(t, "female")
	dr.exec(t, `DELETE FROM doorstep.pro_service_prices WHERE pro_id = $1`, pro)
	customer := uuid.New()
	addr := dr.address(t, customer)
	date := dr.tomorrowAt(10, 0).In(slots.IST).Format("2006-01-02")
	listed := func() string {
		status, body := dr.user(customer, "GET", dr.listPath(false, addr, "date="+date), "")
		want(t, "list", status, body, 200)
		return string(body)
	}
	quote := func() (int, []byte) {
		return dr.user(customer, "POST", "/quotes", fmt.Sprintf(`{"service_id":"%s","pro_id":"%s","option_id":"%s","lat":%f,"lng":%f}`,
			dr.world.anyService, pro, dr.world.anyOption, dr.world.lat, dr.world.lng))
	}
	submit := func(paise int) uuid.UUID {
		status, body := dr.pro(pu, "POST", "/me/prices", fmt.Sprintf(`{"service_id":"%s","item_kind":"option","item_id":"%s","price_paise":%d}`,
			dr.world.anyService, dr.world.anyOption, paise))
		want(t, "submit", status, body, 201)
		var p struct {
			ID     uuid.UUID `json:"id"`
			Status string    `json:"status"`
		}
		data(t, body, &p)
		if p.Status != "pending" {
			t.Fatalf("submitted price %s", p.Status)
		}
		return p.ID
	}
	first := submit(49900)
	if strings.Contains(listed(), pro.String()) {
		t.Fatal("a pending price is listed")
	}
	if status, body := quote(); status != 422 || errCode(body) != "DOORSTEP_PRICE_UNAVAILABLE" {
		t.Fatalf("quote on a pending price: %d %s", status, body)
	}
	// The database refuses an approval nobody audited.
	if _, err := dr.p.Exec(context.Background(), `UPDATE doorstep.pro_service_prices SET status = 'approved', effective_from = NOW(),
		reviewed_by = $2, reviewed_at = NOW() WHERE id = $1`, first, uuid.New()); err == nil || !strings.Contains(err.Error(), "audited admin review") {
		t.Fatalf("an unaudited approval went through: %v", err)
	}
	// Reject needs a reason; approve with the price permission.
	status, body := dr.adminCall("POST", "/pro-prices/"+first.String()+"/reject", doorstephttp.PermPricesReview, `{}`)
	if status != 400 {
		t.Fatalf("reject without a reason: %d %s", status, body)
	}
	status, body = dr.adminCall("POST", "/pro-prices/"+first.String()+"/approve", doorstephttp.PermProsApprove, `{}`)
	if status != 403 {
		t.Fatalf("approve with pros.approve: %d %s", status, body)
	}
	status, body = dr.adminCall("GET", "/pro-prices?status=pending&pro_id="+pro.String(), doorstephttp.PermPricesReview, "")
	if status != 200 || !strings.Contains(string(body), first.String()) || !strings.Contains(string(body), `"suggested_price_paise":59900`) {
		t.Fatalf("review queue: %d %s", status, body)
	}
	status, body = dr.adminCall("POST", "/pro-prices/"+first.String()+"/approve", doorstephttp.PermPricesReview, `{"reason":"fair"}`)
	want(t, "approve", status, body, 200)
	if v := dr.one(t, `SELECT count(*) FROM doorstep.admin_audit_log a WHERE a.entity_id = $1 AND a.action = 'price.approve'
		AND a.actor_user_id = $2 AND a.permission = 'doorstep:prices.review'`, first.String(), dr.actor); v.(int64) != 1 {
		t.Fatalf("approval audit rows %v", v)
	}
	if s := listed(); !strings.Contains(s, pro.String()) || !strings.Contains(s, `"total_paise":49900`) {
		t.Fatalf("approved price not listed: %s", s)
	}
	status, body = quote()
	want(t, "quote", status, body, 201)
	if !strings.Contains(string(body), `"total_paise":49900`) {
		t.Fatalf("quote: %s", body)
	}
	// A changed price waits; the approved one stays live meanwhile.
	second := submit(54900)
	if s := listed(); !strings.Contains(s, `"total_paise":49900`) {
		t.Fatalf("the live price stopped while the new one waits: %s", s)
	}
	status, body = dr.adminCall("POST", "/pro-prices/"+second.String()+"/approve", doorstephttp.PermPricesReview, `{}`)
	want(t, "approve 2", status, body, 200)
	if s := listed(); !strings.Contains(s, `"total_paise":54900`) {
		t.Fatalf("the new price is not live: %s", s)
	}
	if v := dr.one(t, `SELECT effective_to IS NOT NULL FROM doorstep.pro_service_prices WHERE id = $1`, first); v.(bool) != true {
		t.Fatal("the old price was not closed")
	}
	status, body = dr.adminCall("POST", "/pro-prices/"+second.String()+"/approve", doorstephttp.PermPricesReview, `{}`)
	if status != 409 {
		t.Fatalf("approve twice: %d %s", status, body)
	}
	// Events for the professional's app and the ops queue.
	var submitted, reviewed int64
	if err := dr.p.QueryRow(context.Background(), `SELECT
		count(*) FILTER (WHERE event_type = 'doorstep.pro.price_submitted'), count(*) FILTER (WHERE event_type = 'doorstep.pro.price_reviewed')
		FROM doorstep.outbox_events WHERE partition_key = $1`, pu.String()).Scan(&submitted, &reviewed); err != nil || submitted != 2 || reviewed != 2 {
		t.Fatalf("price events %d %d %v", submitted, reviewed, err)
	}
	// The professional's view: live, nothing pending.
	status, body = dr.pro(pu, "GET", "/me/prices", "")
	if status != 200 || !strings.Contains(string(body), `"price_paise":54900,"status":"approved"`) || !strings.Contains(string(body), `"pending":null`) {
		t.Fatalf("pro prices: %d %s", status, body)
	}
	// Withdrawing the live price: no longer listed or quotable.
	status, body = dr.pro(pu, "POST", "/me/prices/"+second.String()+"/withdraw", "")
	want(t, "withdraw", status, body, 200)
	if strings.Contains(listed(), pro.String()) {
		t.Fatal("a withdrawn price is still listed")
	}
	if status, _ := quote(); status != 422 {
		t.Fatalf("quote on a withdrawn price: %d", status)
	}
	// Same-day opt-in for a service of a declared skill.
	status, body = dr.pro(pu, "PUT", "/me/services/"+dr.world.anyService.String()+"/same-day", `{"enabled":true}`)
	if status != 200 || !strings.Contains(string(body), `"same_day":true`) {
		t.Fatalf("same day: %d %s", status, body)
	}
}

// A quote line can only carry an approved price of the quote's own
// professional (database trigger), and a booking re-checks every price is
// still live.
func TestOnlyApprovedPricesAreBookable(t *testing.T) {
	dr := newDispatchRig(t)
	a, _ := dr.addPro(t, "female")
	b, _ := dr.addPro(t, "female")
	customer := uuid.New()
	q, addr := dr.quoteAndAddress(t, customer, false) // a's prices
	// Another professional's price on a's quote: refused by the database.
	var bPrice, aLine uuid.UUID
	ctx := context.Background()
	if err := dr.p.QueryRow(ctx, `SELECT id FROM doorstep.pro_service_prices WHERE pro_id = $1 AND option_id = $2`, b, dr.world.anyOption).
		Scan(&bPrice); err != nil {
		t.Fatal(err)
	}
	if _, err := dr.p.Exec(ctx, `UPDATE doorstep.quote_items SET pro_price_id = $2, price_id = $2 WHERE quote_id = $1`, q, bPrice); err == nil ||
		!strings.Contains(err.Error(), "approved price of its own professional") {
		t.Fatalf("another professional's price on the quote: %v", err)
	}
	// A pending price is refused too.
	pending := uuid.New()
	dr.exec(t, `INSERT INTO doorstep.pro_service_prices (id, pro_id, service_id, item_kind, option_id, unit, price_paise, status)
		VALUES ($1, $2, $3, 'option', $4, 'per_job', 100, 'pending')`, pending, a, dr.world.womenService, dr.world.womenOption)
	if _, err := dr.p.Exec(ctx, `UPDATE doorstep.quote_items SET pro_price_id = $2 WHERE quote_id = $1`, q, pending); err == nil {
		t.Fatal("a pending price was put on a quote")
	}
	// The price is withdrawn after the quote: the booking refuses.
	if err := dr.p.QueryRow(ctx, `SELECT pro_price_id FROM doorstep.quote_items WHERE quote_id = $1`, q).Scan(&aLine); err != nil {
		t.Fatal(err)
	}
	dr.exec(t, `UPDATE doorstep.pro_service_prices SET status = 'withdrawn', effective_to = NOW() WHERE id = $1`, aLine)
	status, body := dr.book(customer, q, addr, dr.tomorrowAt(10, 0), "withdrawn-1")
	if status != 422 || errCode(body) != "DOORSTEP_PRICE_UNAVAILABLE" {
		t.Fatalf("booked on a withdrawn price: %d %s", status, body)
	}
}

// The list applies every hard filter: zone, radius, calendar, gender
// (category rule and the customer's preference); distances are bands only;
// a booking holds the picked professional and nobody else.
func TestProfessionalsListFilters(t *testing.T) {
	dr := newDispatchRig(t)
	ok, _ := dr.addPro(t, "female")
	man, _ := dr.addPro(t, "male")
	otherZone, _ := dr.addPro(t, "female")
	dayOff, _ := dr.addPro(t, "female")
	far, _ := dr.addPro(t, "female")
	dr.exec(t, `UPDATE doorstep.pro_zones SET zone_id = $2 WHERE pro_id = $1`, otherZone, id("zone", "central-banjara-jubilee"))
	start := dr.tomorrowAt(10, 0)
	day := start.In(slots.IST)
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, slots.IST)
	dr.exec(t, `INSERT INTO doorstep.pro_calendar_blocks (pro_id, kind, during) VALUES ($1, 'day_off', tstzrange($2, $3, '[)'))`,
		dayOff, dayStart, dayStart.AddDate(0, 0, 1))
	dr.exec(t, `UPDATE doorstep.professionals SET service_radius_m = 1000, home_point = ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography
		WHERE id = $1`, far, dr.world.lng+0.05, dr.world.lat)
	customer := uuid.New()
	addr := dr.address(t, customer)
	date := "date=" + day.Format("2006-01-02")
	status, body := dr.user(customer, "GET", dr.listPath(false, addr, date), "")
	want(t, "list", status, body, 200)
	s := string(body)
	for name, p := range map[string]uuid.UUID{"in zone": ok, "man": man} {
		if !strings.Contains(s, p.String()) {
			t.Errorf("%s not listed", name)
		}
	}
	for name, p := range map[string]uuid.UUID{"other zone": otherZone, "day off": dayOff, "outside radius": far} {
		if strings.Contains(s, p.String()) {
			t.Errorf("%s listed", name)
		}
	}
	for _, leak := range []string{`"lat"`, `"lng"`, `"distance_m"`, `"home`, `"live_distance`} {
		if strings.Contains(s, leak) {
			t.Fatalf("the list leaks %s: %s", leak, s)
		}
	}
	if !strings.Contains(s, `"distance_band":"under_2_km"`) {
		t.Fatalf("no distance band: %s", s)
	}
	_, body = dr.user(customer, "GET", dr.listPath(false, addr, date+"&require_female_pro=true"), "")
	if strings.Contains(string(body), man.String()) || !strings.Contains(string(body), ok.String()) {
		t.Fatalf("woman preference: %s", body)
	}
	_, body = dr.user(customer, "GET", dr.listPath(true, addr, date), "")
	if strings.Contains(string(body), man.String()) || !strings.Contains(string(body), ok.String()) {
		t.Fatalf("women's service: %s", body)
	}
	// Book the man (the customer's pick): only he is held.
	dr.pick = &man
	q, a := dr.quoteAndAddress(t, customer, false)
	status, body = dr.book(customer, q, a, start, "pick-man")
	want(t, "book", status, body, 201)
	var out bookingOut
	data(t, body, &out)
	if blocks := dr.activeBlocks(t, out.Booking.ID); len(blocks) != 1 || blocks[0] != man {
		t.Fatalf("held %v, want only the picked professional", blocks)
	}
	dr.pick = nil
}

// ASAP: only on-duty professionals within range, with a fresh fix, who
// opted in to same-day jobs for the service; the block runs from now and
// the offer window is 3 minutes. Nobody: said so, with the scheduled times.
func TestASAPListAndBooking(t *testing.T) {
	dr := newDispatchRig(t)
	dr.now = dr.tomorrowAt(11, 0)
	ready, readyUser := dr.addPro(t, "female")
	noSameDay, _ := dr.addPro(t, "female")
	offDuty, _ := dr.addPro(t, "female")
	tooFar, _ := dr.addPro(t, "female")
	stale, _ := dr.addPro(t, "female")
	fix := dr.now.Add(-time.Minute)
	near := func(pro uuid.UUID, dLat float64, onDuty bool, at time.Time) {
		dr.exec(t, `UPDATE doorstep.professionals SET on_duty = $2, on_duty_since = $3, last_fix_at = $3, service_radius_m = 5000,
			last_point = ST_SetSRID(ST_MakePoint($4, $5), 4326)::geography WHERE id = $1`, pro, onDuty, at, dr.world.lng, dr.world.lat+dLat)
	}
	near(ready, 0.02, true, fix)
	near(noSameDay, 0.02, true, fix)
	near(offDuty, 0.02, false, fix)
	near(tooFar, 0.08, true, fix) // ~8.9 km, radius 5 km
	near(stale, 0.02, true, dr.now.Add(-10*time.Minute))
	for _, p := range []uuid.UUID{ready, offDuty, tooFar, stale} {
		dr.exec(t, `INSERT INTO doorstep.pro_service_settings (pro_id, service_id, same_day) VALUES ($1, $2, TRUE)`, p, dr.world.anyService)
	}
	customer := uuid.New()
	addr := dr.address(t, customer)
	status, body := dr.user(customer, "GET", dr.listPath(false, addr, "asap=true"), "")
	want(t, "asap list", status, body, 200)
	s := string(body)
	if !strings.Contains(s, ready.String()) || !strings.Contains(s, `"mode":"asap"`) || !strings.Contains(s, `"eta_minutes":`) {
		t.Fatalf("asap list: %s", s)
	}
	for name, p := range map[string]uuid.UUID{"no same-day": noSameDay, "off duty": offDuty, "out of range": tooFar, "stale fix": stale} {
		if strings.Contains(s, p.String()) {
			t.Errorf("%s listed for ASAP", name)
		}
	}
	// Book ASAP with the ready professional: the block starts now.
	dr.pick = &ready
	q, a := dr.quoteAndAddress(t, customer, false)
	dr.pick = nil
	status, body = dr.user(customer, "POST", "/bookings", fmt.Sprintf(`{"quote_id":"%s","address_id":"%s","asap":true}`, q, a),
		"Idempotency-Key", "asap-it")
	want(t, "asap book", status, body, 201)
	var out bookingOut
	data(t, body, &out)
	b := out.Booking.ID
	lower := dr.one(t, `SELECT lower(during) FROM doorstep.pro_calendar_blocks WHERE booking_id = $1 AND active`, b).(time.Time)
	slot := dr.one(t, `SELECT slot_start FROM doorstep.bookings WHERE id = $1 AND asap`, b).(time.Time)
	if !lower.Equal(dr.now.Truncate(time.Minute)) || !slot.After(dr.now) || slot.After(dr.now.Add(time.Hour)) {
		t.Fatalf("asap block from %v, slot %v (now %v)", lower, slot, dr.now)
	}
	dr.captured(t, b, "evt-asap-"+b.String(), nil)
	offer, p := dr.liveOffer(t, b)
	if p != ready {
		t.Fatal("asap offer not to the picked professional")
	}
	if exp := dr.one(t, `SELECT offer_expires_at FROM doorstep.booking_assignments WHERE id = $1`, offer).(time.Time); !exp.Equal(dr.now.Add(3 * time.Minute)) {
		t.Fatalf("asap offer expires %v (now %v)", exp, dr.now)
	}
	// The T-45 and T-2 h rules leave an ASAP job alone.
	dr.tick(t)
	if s := dr.status(t, b); s != "confirmed" {
		t.Fatalf("asap booking %s after a tick", s)
	}
	// The offer lapses: pro_unavailable; asking again for ASAP finds nobody
	// (the ready professional let it go) and lists the scheduled times.
	dr.now = dr.now.Add(3*time.Minute + time.Second)
	dr.exec(t, `UPDATE doorstep.professionals SET last_fix_at = $2 WHERE id = $1`, ready, dr.now)
	dr.tick(t)
	dr.assertProUnavailable(t, b, customer, ready, "offer_expired")
	status, body = dr.user(customer, "GET", "/bookings/"+b.String()+"/professionals?asap=true", "")
	want(t, "asap alternatives", status, body, 200)
	if s := string(body); !strings.Contains(s, `"no_professional":true`) || !strings.Contains(s, `"no_professional_reason":"none_available_now"`) ||
		!strings.Contains(s, `"scheduled_alternatives":[{`) || strings.Contains(s, ready.String()) {
		t.Fatalf("asap none: %s", s)
	}
	_ = readyUser
}

// Founder rule (4 Oct 2026): nothing a professional submits is approved by
// itself. Every approval or verification needs an audited admin review in
// its own transaction — the database refuses anything else, whatever code
// path tries.
func TestNothingApprovedWithoutAnAuditedAdmin(t *testing.T) {
	dr := newDispatchRig(t)
	pro, _ := dr.addPro(t, "female")
	ctx := context.Background()
	reviewer := uuid.New()
	doc := uuid.New()
	dr.exec(t, `INSERT INTO doorstep.pro_documents (id, pro_id, kind, media_id, status) VALUES ($1, $2, 'other', $3, 'pending')`, doc, pro, uuid.NewString())
	dr.exec(t, `UPDATE doorstep.pro_skills SET status = 'pending', verified_by = NULL WHERE pro_id = $1`, pro)
	dr.exec(t, `UPDATE doorstep.professionals SET status = 'suspended' WHERE id = $1`, pro)
	dr.exec(t, `UPDATE doorstep.pro_service_prices SET effective_to = NOW() - INTERVAL '1 day' WHERE pro_id = $1 AND option_id = $2`,
		pro, dr.world.womenOption)
	price := uuid.New()
	dr.exec(t, `INSERT INTO doorstep.pro_service_prices (id, pro_id, service_id, item_kind, option_id, unit, price_paise, status)
		VALUES ($1, $2, $3, 'option', $4, 'per_job', 777, 'pending')`, price, pro, dr.world.womenService, dr.world.womenOption)
	grants := map[string]string{
		"skill verified":     `UPDATE doorstep.pro_skills SET status = 'verified', verified_by = '` + reviewer.String() + `' WHERE pro_id = '` + pro.String() + `'`,
		"document approved":  `UPDATE doorstep.pro_documents SET status = 'approved', reviewed_by = '` + reviewer.String() + `' WHERE id = '` + doc.String() + `'`,
		"selfie passed":      `INSERT INTO doorstep.pro_kyc_checks (pro_id, kind, status) VALUES ('` + pro.String() + `', 'selfie_face_match', 'passed')`,
		"professional":       `UPDATE doorstep.professionals SET status = 'approved' WHERE id = '` + pro.String() + `'`,
		"price approved":     `UPDATE doorstep.pro_service_prices SET status = 'approved', effective_from = NOW(), reviewed_at = NOW(), reviewed_by = '` + reviewer.String() + `' WHERE id = '` + price.String() + `'`,
		"skill without name": `UPDATE doorstep.pro_skills SET status = 'verified' WHERE pro_id = '` + pro.String() + `'`,
	}
	for name, sql := range grants {
		if _, err := dr.p.Exec(ctx, sql); err == nil || !strings.Contains(err.Error(), "ck_doorstep_admin_review") &&
			!strings.Contains(err.Error(), "admin") {
			t.Errorf("%s without an audited admin: %v", name, err)
		}
	}
	// The same changes inside an audited admin transaction go through (the
	// reviewer named on the row must be the auditing admin).
	adminTx(t, dr.p, func(exec func(string, ...any)) {
		exec(strings.ReplaceAll(grants["skill verified"], reviewer.String(), itAdmin.String()))
		exec(strings.ReplaceAll(grants["document approved"], reviewer.String(), itAdmin.String()))
		exec(grants["selfie passed"])
		exec(grants["professional"])
		exec(strings.ReplaceAll(grants["price approved"], reviewer.String(), itAdmin.String()))
	})
	// An admin who audited is not enough when the row names another one.
	tx, err := dr.p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO doorstep.admin_audit_log (actor_user_id, permission, action, entity, entity_id)
		VALUES ($1, 'doorstep:pros.approve', 'itest', 'itest', 'x')`, itAdmin); err != nil {
		t.Fatal(err)
	}
	doc2 := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_documents (id, pro_id, kind, media_id, status, reviewed_by) VALUES ($1, $2, 'other', $3, 'approved', $4)`,
		doc2, pro, uuid.NewString(), reviewer); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("an approval named a reviewer who did not audit it")
	}
}

// DB-backed admin-service token tests (admin console Wave 2 — Feast): every
// newly audited admin action writes exactly one food.admin_audit_logs row
// whose actor is the token's signed act claim, and the stats route counts
// what is seeded. Requires TEST_PG_DSN on a "_test" database (food_it_test,
// -p 1); skipped when unset.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/database"
	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/kyc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupAdminTokenIT(t *testing.T) (*adminTokenRig, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping food-service admin token integration tests")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	pricingCfg := pricing.DefaultConfig()
	d, err := kyc.GSTINCheckDigit("29ZZZCZ9999Z1Z")
	if err != nil {
		t.Fatal(err)
	}
	pricingCfg.PlatformGSTIN = "29ZZZCZ9999Z1Z" + string(d)
	rg := newAdminTokenRig(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	New(service.New(postgres.New(pool).WithPricingConfig(pricingCfg))).WithServiceAuth(rg.v).RegisterRoutes(r)
	rg.r = r
	return rg, pool
}

// itExec runs one seed statement.
func itExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed %q: %v", sql, err)
	}
}

// seedITOrder inserts an order for restaurantID with the given status, money
// (paise) and placement time, and returns its id.
func seedITOrder(t *testing.T, pool *pgxpool.Pool, restaurantID uuid.UUID, status string, paise int64, placedAt time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO food.orders (order_number, user_id, restaurant_id, restaurant_name_snapshot,
			restaurant_address_snapshot, delivery_address_snapshot, status, final_amount, final_amount_paise, placed_at)
		VALUES ($1, $2, $3, 'IT Kitchen', '{}'::jsonb, '{}'::jsonb, $4::food.order_status, ($5::bigint)::numeric / 100, $5, $6)
		RETURNING id`, "IT-ADM-"+uuid.NewString()[:18], uuid.New(), restaurantID, status, paise, placedAt).Scan(&id); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	return id
}

type auditRow struct {
	Actor  uuid.UUID
	Action string
}

func auditRowsFor(t *testing.T, pool *pgxpool.Pool, entityID uuid.UUID) []auditRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT actor_user_id, action FROM food.admin_audit_logs WHERE entity_id = $1 ORDER BY created_at`, entityID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var a auditRow
		if err := rows.Scan(&a.Actor, &a.Action); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func TestAdminTokenIT_NewlyAuditedActionsRecordAct(t *testing.T) {
	rg, pool := setupAdminTokenIT(t)
	ctx := context.Background()
	owner := uuid.New()
	restaurantID, _ := createRestaurantViaAPI(t, rg.r, owner)
	seedAvailableMenuItem(t, pool, restaurantID)
	var itemID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM food.menu_items WHERE restaurant_id = $1`, restaurantID).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	orderID := seedITOrder(t, pool, restaurantID, "PLACED", 18172, time.Now())
	reviewOrder := seedITOrder(t, pool, restaurantID, "DELIVERED", 12000, time.Now())
	var ticketID, refundID, reviewID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO food.support_tickets (customer_id, category, subject) VALUES ($1, 'order', 'IT admin audit') RETURNING id`, uuid.New()).Scan(&ticketID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO food.refund_requests (order_id, customer_id, amount, reason) VALUES ($1, $2, 10, 'IT') RETURNING id`, orderID, uuid.New()).Scan(&refundID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO food.item_reviews (order_id, menu_item_id, customer_id, rating, review) VALUES ($1, $2, $3, 1, 'IT') RETURNING id`, reviewOrder, itemID, uuid.New()).Scan(&reviewID); err != nil {
		t.Fatal(err)
	}

	// call sends one admin-service token request with a forged gateway
	// identity riding along, and returns the response.
	call := func(perm, method, path, body string) *json.RawMessage {
		t.Helper()
		hdr := legacyAdmin(uuid.New(), "superadmin")
		delete(hdr, "X-Internal-Service-Key")
		hdr["X-Admin-Id"] = uuid.NewString()
		hdr[ServiceAuthHeader] = "Bearer " + rg.mint(t, rg.admin, AudienceFood, []string{perm}, rg.actor.String())
		w := adminServe(rg.r, method, InternalAdminPrefix+path, body, hdr)
		if w.Code < 200 || w.Code > 299 {
			t.Fatalf("%s %s: status=%d body=%s", method, path, w.Code, w.Body.String())
		}
		raw := decodeEnvelope(t, w).Data
		return &raw
	}
	idOf := func(raw *json.RawMessage) uuid.UUID {
		t.Helper()
		var v struct {
			ID uuid.UUID `json:"id"`
		}
		if err := json.Unmarshal(*raw, &v); err != nil || v.ID == uuid.Nil {
			t.Fatalf("no id in %s: %v", string(*raw), err)
		}
		return v.ID
	}
	expectOne := func(entity uuid.UUID, action string) {
		t.Helper()
		var n int
		for _, row := range auditRowsFor(t, pool, entity) {
			if row.Action != action {
				continue
			}
			if row.Actor != rg.actor {
				t.Fatalf("%s audit actor = %s, want act %s", action, row.Actor, rg.actor)
			}
			n++
		}
		if n != 1 {
			t.Fatalf("%s: %d audit rows for %s, want exactly 1", action, n, entity)
		}
	}

	// A refused token writes nothing.
	wrong := bearer(rg.mint(t, rg.admin, AudienceFood, []string{PermOrdersRead}, rg.actor.String()))
	if w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/orders/"+orderID.String()+"/cancel", `{"reason":"IT"}`, wrong); w.Code != http.StatusForbidden {
		t.Fatalf("cancel with orders.read: status=%d, want 403", w.Code)
	}
	if rows := auditRowsFor(t, pool, orderID); len(rows) != 0 {
		t.Fatalf("a refused cancel wrote %d audit rows", len(rows))
	}

	call(PermOrdersCancel, http.MethodPost, "/orders/"+orderID.String()+"/cancel", `{"reason":"IT admin cancel"}`)
	expectOne(orderID, "order.cancel")

	code := "ITADM" + strings.ToUpper(uuid.NewString()[:8])
	couponID := idOf(call(PermCouponsManage, http.MethodPost, "/coupons", `{"code":"`+code+`","title":"IT coupon","discount_value":40}`))
	expectOne(couponID, "coupon.create")
	call(PermCouponsManage, http.MethodPatch, "/coupons/"+couponID.String(), `{"title":"IT coupon 2","discount_value":45,"is_active":false}`)
	expectOne(couponID, "coupon.update")

	areaID := idOf(call(PermServiceAreasManage, http.MethodPost, "/service-areas", `{"name":"IT area `+uuid.NewString()[:8]+`","city":"Bengaluru","radius_km":6}`))
	expectOne(areaID, "service_area.create")
	call(PermServiceAreasManage, http.MethodPatch, "/service-areas/"+areaID.String(), `{"name":"IT area moved `+uuid.NewString()[:8]+`","city":"Bengaluru","radius_km":7}`)
	expectOne(areaID, "service_area.update")

	call(PermMenuModerate, http.MethodPost, "/moderation/menu-items/"+itemID.String(), `{"status":"flagged","reason":"IT"}`)
	expectOne(itemID, "menu_item.moderate")

	call(PermTicketsAct, http.MethodPost, "/support/tickets/"+ticketID.String()+"/status", `{"status":"in_progress"}`)
	expectOne(ticketID, "ticket.status")

	call(PermReviewsModerate, http.MethodDelete, "/item-reviews/"+reviewID.String(), ``)
	expectOne(reviewID, "item_review.hide")

	call(PermRefundIssue, http.MethodPost, "/refunds/"+refundID.String()+"/decide", `{"status":"rejected","reason":"IT"}`)
	expectOne(refundID, "refund_request.decide")

	// Moderating an item that does not exist is refused and writes nothing.
	ghost := uuid.New()
	hdr := bearer(rg.mint(t, rg.admin, AudienceFood, []string{PermMenuModerate}, rg.actor.String()))
	if w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/moderation/menu-items/"+ghost.String(), `{"status":"flagged"}`, hdr); w.Code == http.StatusOK {
		t.Fatalf("moderating a missing item: status=%d, want refused", w.Code)
	}
	if rows := auditRowsFor(t, pool, ghost); len(rows) != 0 {
		t.Fatalf("a missing item wrote %d audit rows", len(rows))
	}
}

// The LEGACY path still works and now audits too, with the gateway's user.
func TestAdminTokenIT_LegacyTicketStatusAudited(t *testing.T) {
	rg, pool := setupAdminTokenIT(t)
	var ticketID uuid.UUID
	if err := pool.QueryRow(context.Background(), `INSERT INTO food.support_tickets (customer_id, category, subject) VALUES ($1, 'order', 'IT legacy') RETURNING id`, uuid.New()).Scan(&ticketID); err != nil {
		t.Fatal(err)
	}
	admin := uuid.New()
	hdr := legacyAdmin(admin, "admin")
	delete(hdr, "X-Internal-Service-Key")
	w := adminServe(rg.r, http.MethodPost, "/v1/food/admin/support/tickets/"+ticketID.String()+"/status", `{"status":"resolved"}`, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy ticket status: status=%d body=%s", w.Code, w.Body.String())
	}
	rows := auditRowsFor(t, pool, ticketID)
	if len(rows) != 1 || rows[0].Actor != admin || rows[0].Action != "ticket.status" {
		t.Fatalf("legacy audit rows = %+v, want one ticket.status by %s", rows, admin)
	}
}

func adminStatsVia(t *testing.T, rg *adminTokenRig) postgres.AdminStats {
	t.Helper()
	w := adminServe(rg.r, http.MethodGet, InternalAdminPrefix+"/stats", "",
		bearer(rg.mint(t, rg.admin, AudienceFood, []string{PermStatsRead}, rg.actor.String())))
	if w.Code != http.StatusOK {
		t.Fatalf("stats: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	var out postgres.AdminStats
	if err := json.Unmarshal(decodeEnvelope(t, w).Data, &out); err != nil {
		t.Fatalf("decode stats: %v body=%s", err, w.Body.String())
	}
	return out
}

func TestAdminTokenIT_Stats(t *testing.T) {
	rg, pool := setupAdminTokenIT(t)
	ctx := context.Background()

	// Token only: a gateway superadmin gets nothing; another permission neither.
	if w := adminServe(rg.r, http.MethodGet, InternalAdminPrefix+"/stats", "", legacyAdmin(uuid.New(), "superadmin")); w.Code != http.StatusUnauthorized {
		t.Fatalf("stats without a token: status=%d, want 401", w.Code)
	}
	if w := adminServe(rg.r, http.MethodGet, InternalAdminPrefix+"/stats", "",
		bearer(rg.mint(t, rg.admin, AudienceFood, []string{PermOrdersRead}, rg.actor.String()))); w.Code != http.StatusForbidden {
		t.Fatalf("stats with orders.read: status=%d, want 403", w.Code)
	}

	before := adminStatsVia(t, rg)

	// Restaurants: one ACTIVE, one PENDING_REVIEW.
	active, _ := createRestaurantViaAPI(t, rg.r, uuid.New())
	pending, _ := createRestaurantViaAPI(t, rg.r, uuid.New())
	itExec(t, pool, `UPDATE food.restaurants SET status = 'ACTIVE' WHERE id = $1`, active)
	itExec(t, pool, `UPDATE food.restaurants SET status = 'PENDING_REVIEW' WHERE id = $1`, pending)

	// Orders: three today (one cancelled) = 14550 paise; one two days ago.
	seedITOrder(t, pool, active, "PLACED", 12050, time.Now())
	seedITOrder(t, pool, active, "DELIVERED", 2000, time.Now())
	cancelled := seedITOrder(t, pool, active, "CANCELLED_BY_ADMIN", 500, time.Now())
	seedITOrder(t, pool, active, "DELIVERED", 99900, time.Now().Add(-48*time.Hour))

	// Riders: one pending review, one online.
	var riderPending, riderOnline uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO food.delivery_partners (user_id, full_name, phone, status) VALUES ($1, 'IT Rider', $2, 'PENDING_REVIEW') RETURNING id`, uuid.New(), "9"+uuid.NewString()[:9]).Scan(&riderPending); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO food.delivery_partners (user_id, full_name, phone, status, is_online) VALUES ($1, 'IT Rider 2', $2, 'ACTIVE', TRUE) RETURNING id`, uuid.New(), "8"+uuid.NewString()[:9]).Scan(&riderOnline); err != nil {
		t.Fatal(err)
	}

	// Documents: two pending restaurant documents + one approved; one pending rider document.
	itExec(t, pool, `INSERT INTO food.restaurant_documents (restaurant_id, document_type) VALUES ($1, 'IT_DOC_A'), ($1, 'IT_DOC_B')`, pending)
	itExec(t, pool, `INSERT INTO food.restaurant_documents (restaurant_id, document_type, status) VALUES ($1, 'IT_DOC_C', 'APPROVED')`, pending)
	itExec(t, pool, `INSERT INTO food.delivery_partner_documents (delivery_partner_id, document_type) VALUES ($1, 'IT_DOC')`, riderPending)

	// Refund requests: one requested, one rejected. Payment refunds: one pending, one processed.
	itExec(t, pool, `INSERT INTO food.refund_requests (order_id, customer_id, amount) VALUES ($1, $2, 5)`, cancelled, uuid.New())
	itExec(t, pool, `INSERT INTO food.refund_requests (order_id, customer_id, amount, status) VALUES ($1, $2, 5, 'rejected')`, cancelled, uuid.New())
	itExec(t, pool, `INSERT INTO food.refunds (order_id, amount, reason) VALUES ($1, 5, 'IT')`, cancelled)
	itExec(t, pool, `INSERT INTO food.refunds (order_id, amount, reason, status) VALUES ($1, 5, 'IT', 'PROCESSED')`, cancelled)

	// Tickets: open + in_progress count; closed does not.
	itExec(t, pool, `INSERT INTO food.support_tickets (customer_id, category, subject, status) VALUES ($1, 'o', 'IT', 'open'), ($1, 'o', 'IT', 'in_progress'), ($1, 'o', 'IT', 'closed')`, uuid.New())

	// Settlements: restaurant PENDING 250.25 counts, PAID does not; rider PROCESSING 40.00 counts, FAILED does not.
	itExec(t, pool, `INSERT INTO food.restaurant_settlements (restaurant_id, period_start, period_end, payout_amount) VALUES ($1, '2026-01-01', '2026-01-07', 250.25)`, active)
	itExec(t, pool, `INSERT INTO food.restaurant_settlements (restaurant_id, period_start, period_end, payout_amount, status) VALUES ($1, '2026-01-08', '2026-01-14', 99, 'PAID')`, active)
	itExec(t, pool, `INSERT INTO food.delivery_partner_settlements (delivery_partner_id, period_start, period_end, payout_amount, status) VALUES ($1, '2026-01-01', '2026-01-07', 40, 'PROCESSING')`, riderOnline)
	itExec(t, pool, `INSERT INTO food.delivery_partner_settlements (delivery_partner_id, period_start, period_end, payout_amount, status) VALUES ($1, '2026-01-08', '2026-01-14', 40, 'FAILED')`, riderOnline)

	after := adminStatsVia(t, rg)

	deltas := map[string][2]int64{
		"orders_today":                        {int64(after.OrdersToday - before.OrdersToday), 3},
		"gmv_today_paise":                     {after.GMVTodayPaise - before.GMVTodayPaise, 14550},
		"cancelled_orders_today":              {int64(after.CancelledOrdersToday - before.CancelledOrdersToday), 1},
		"restaurants_active":                  {int64(after.RestaurantsActive - before.RestaurantsActive), 1},
		"restaurants_pending_review":          {int64(after.RestaurantsPendingReview - before.RestaurantsPendingReview), 1},
		"delivery_partners_pending_review":    {int64(after.DeliveryPartnersPendingReview - before.DeliveryPartnersPendingReview), 1},
		"delivery_partners_online":            {int64(after.DeliveryPartnersOnline - before.DeliveryPartnersOnline), 1},
		"restaurant_documents_pending":        {int64(after.RestaurantDocumentsPending - before.RestaurantDocumentsPending), 2},
		"delivery_documents_pending":          {int64(after.DeliveryDocumentsPending - before.DeliveryDocumentsPending), 1},
		"refund_requests_pending":             {int64(after.RefundRequestsPending - before.RefundRequestsPending), 1},
		"refunds_in_flight":                   {int64(after.RefundsInFlight - before.RefundsInFlight), 1},
		"tickets_open":                        {int64(after.TicketsOpen - before.TicketsOpen), 2},
		"restaurant_settlements_unpaid":       {int64(after.RestaurantSettlementsUnpaid - before.RestaurantSettlementsUnpaid), 1},
		"restaurant_settlements_unpaid_paise": {after.RestaurantSettlementsUnpaidPaise - before.RestaurantSettlementsUnpaidPaise, 25025},
		"delivery_settlements_unpaid":         {int64(after.DeliverySettlementsUnpaid - before.DeliverySettlementsUnpaid), 1},
		"delivery_settlements_unpaid_paise":   {after.DeliverySettlementsUnpaidPaise - before.DeliverySettlementsUnpaidPaise, 4000},
	}
	for name, v := range deltas {
		if v[0] != v[1] {
			t.Errorf("%s delta = %d, want %d", name, v[0], v[1])
		}
	}
	if after.GeneratedAt.Before(after.DayStartsAt) || after.GeneratedAt.Sub(after.DayStartsAt) > 24*time.Hour {
		t.Errorf("day_starts_at %s is not within the day before generated_at %s", after.DayStartsAt, after.GeneratedAt)
	}
}

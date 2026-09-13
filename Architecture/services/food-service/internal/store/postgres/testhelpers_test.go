package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/atpost/food-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// requireTestDatabase refuses any DSN whose database name does not end in
// `_test`. The integration suite bootstraps the schema and writes fixture
// rows, so pointing TEST_PG_DSN at `app` (or any live database) would seed
// fake restaurants, partners and orders into real data. The check parses the
// DSN offline; it never connects, and it never echoes the DSN back.
func requireTestDatabase(dsn string) error {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("TEST_PG_DSN does not parse")
	}
	if !strings.HasSuffix(cfg.Database, "_test") {
		return fmt.Errorf("TEST_PG_DSN database %q does not end in _test; refusing to run integration tests against it", cfg.Database)
	}
	return nil
}

// foodTestStore returns a *Store backed by TEST_PG_DSN, applying the
// food schema first so a fresh test container is fully ready. Skips
// the test if TEST_PG_DSN is unset (CI runs unit-only).
//
// RUN INTEGRATION PACKAGES ONE AT A TIME:
//
//	go test -p 1 -count=1 ./internal/store/postgres/ ./internal/service/ ./internal/http/
//
// Every integration package seeds and truncates the same tables in the one
// scratch database. With Go's default package parallelism, tests in
// different packages collide mid-fixture and fail with `deadlock detected`
// (40P01) or `current transaction is aborted` (25P02) — observed 13 Sep 2026.
// Each package passes on its own and all pass under -p 1. This is test
// isolation, not a product bug. The schema bootstrap itself IS safe to run
// concurrently: BootstrapSchema holds food-service's advisory lock, pinned by
// TestBootstrapSchemaSurvivesConcurrentBooters.
//
// Mirrors rider-service/internal/store/testhelpers_test.go.
func foodTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping food-service store integration tests")
	}
	if err := requireTestDatabase(dsn); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := BootstrapSchema(context.Background(), pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return New(pool).WithPricingConfig(testPricingConfig(t)), func() { pool.Close() }
}

// seedOrderWithItem inserts a minimal restaurant + menu_item + order +
// order_items row tuple so tests targeting per-order behavior have
// stable foreign keys. Returns the new IDs.
//
// The order is created in DELIVERED state so item-review tests can run
// without needing to drive the full status machine — callers needing a
// different status can override via the orderStatus arg.
func seedOrderWithItem(t *testing.T, s *Store, orderStatus string) (orderID, menuItemID, customerID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	ownerID := uuid.New()
	customerID = uuid.New()

	// Partner row first — restaurants references it via FK.
	var partnerID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.restaurant_partners (owner_user_id, legal_name, status)
		VALUES ($1, 'Test Partner', 'APPROVED')
		RETURNING id
	`, ownerID).Scan(&partnerID); err != nil {
		t.Fatalf("seed restaurant partner: %v", err)
	}

	var restaurantID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.restaurants
			(partner_id, name, slug, owner_user_id, status, is_open, is_accepting_orders,
			 address_line1, city, state, tax_category, min_order_amount, packaging_fee,
			 avg_preparation_minutes, commission_percentage)
		VALUES ($1, $2, $3, $4, 'ACTIVE', TRUE, TRUE,
			'1 Test Lane', 'Bengaluru', 'Karnataka', 'RESTAURANT_STANDALONE', 0, 0, 20, 10)
		RETURNING id
	`, partnerID, "Test "+uuid.NewString()[:8], "test-"+uuid.NewString()[:8], ownerID).Scan(&restaurantID); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}

	var menuCategoryID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.menu_categories (restaurant_id, name, sort_order)
		VALUES ($1, 'Mains', 1)
		RETURNING id
	`, restaurantID).Scan(&menuCategoryID); err != nil {
		t.Fatalf("seed category: %v", err)
	}

	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.menu_items
			(restaurant_id, category_id, name, base_price, food_type, preparation_minutes, is_available, is_active)
		VALUES ($1, $2, 'Paneer Tikka', 250, 'VEG', 15, TRUE, TRUE)
		RETURNING id
	`, restaurantID, menuCategoryID).Scan(&menuItemID); err != nil {
		t.Fatalf("seed menu item: %v", err)
	}

	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.orders
			(order_number, user_id, restaurant_id, status, payment_status, payment_method,
			 restaurant_name_snapshot, restaurant_address_snapshot, delivery_address_snapshot,
			 item_subtotal, final_amount, commission_percentage_snapshot, commission_amount)
		VALUES ($1, $2, $3, $4::food.order_status, 'CAPTURED', 'ONLINE',
			'Test', '{}'::jsonb, '{}'::jsonb,
			250, 250, 10, 25)
		RETURNING id
	`, "TEST-"+uuid.NewString()[:8], customerID, restaurantID, orderStatus).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	if _, err := s.db.Exec(ctx, `
		INSERT INTO food.order_items
			(order_id, menu_item_id, item_name_snapshot, food_type_snapshot,
			 unit_price_snapshot, quantity, line_total)
		VALUES ($1, $2, 'Paneer Tikka', 'VEG', 250, 1, 250)
	`, orderID, menuItemID); err != nil {
		t.Fatalf("seed order item: %v", err)
	}

	return orderID, menuItemID, customerID
}

// seedDeliveryPartner inserts an ACTIVE + is_online=TRUE delivery
// partner so the offer + OTP tests can target an eligible candidate.
// Returns the partner row's user_id (X-User-Id source) and the
// food.delivery_partners.id.
func seedDeliveryPartner(t *testing.T, s *Store) (userID, partnerID uuid.UUID) {
	t.Helper()
	return seedDeliveryPartnerWithStatus(t, s, "ACTIVE", true)
}

// seedDeliveryPartnerWithStatus inserts a delivery partner in an arbitrary
// lifecycle status (PENDING_REVIEW, SUSPENDED, ...) so the claim tests can
// prove that only an ACTIVE partner may act on an assignment.
func seedDeliveryPartnerWithStatus(t *testing.T, s *Store, status string, online bool) (userID, partnerID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	userID = uuid.New()
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.delivery_partners
			(user_id, full_name, phone, status, vehicle_type, vehicle_number,
			 city, is_online)
		VALUES ($1, $2, $3, $4::food.delivery_partner_status, 'bike', 'KA-01-0000', 'Bengaluru', $5)
		RETURNING id
	`, userID, "Test Rider", "+91900000"+uuid.NewString()[:4], status, online).Scan(&partnerID); err != nil {
		t.Fatalf("seed delivery partner: %v", err)
	}
	return userID, partnerID
}

// seedDeliveryAssignment binds an order to a delivery partner so the
// OTP-verify tests have something to operate on.
func seedDeliveryAssignment(t *testing.T, s *Store, orderID, partnerID uuid.UUID) {
	t.Helper()
	seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ASSIGNED")
}

// seedDeliveryAssignmentWithStatus inserts an assignment in any status. A nil
// partnerID leaves the assignment unclaimed, which is what PlaceOrder and
// ConfirmPayment create. Returns the assignment id.
func seedDeliveryAssignmentWithStatus(t *testing.T, s *Store, orderID uuid.UUID, partnerID *uuid.UUID, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := s.db.QueryRow(context.Background(), `
		INSERT INTO food.delivery_assignments
			(order_id, delivery_partner_id, status, assigned_at, accepted_at)
		VALUES ($1, $2, $3::food.assignment_status,
			CASE WHEN $2::uuid IS NULL THEN NULL ELSE NOW() END,
			CASE WHEN $2::uuid IS NULL THEN NULL ELSE NOW() END)
		RETURNING id
	`, orderID, partnerID, status).Scan(&id); err != nil {
		t.Fatalf("seed delivery assignment: %v", err)
	}
	return id
}

// seedPendingOffer inserts a pending, unexpired offer with raw SQL so the
// lifecycle tests do not depend on the CreateDeliveryOffer signature.
func seedPendingOffer(t *testing.T, s *Store, orderID, partnerID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := s.db.QueryRow(context.Background(), `
		INSERT INTO food.delivery_offers (order_id, delivery_partner_id, expires_at)
		VALUES ($1, $2, NOW() + INTERVAL '5 minutes')
		RETURNING id
	`, orderID, partnerID).Scan(&id); err != nil {
		t.Fatalf("seed offer: %v", err)
	}
	return id
}

type historyRow struct {
	From string
	To   string
}

func readHistory(t *testing.T, s *Store, orderID uuid.UUID) []historyRow {
	t.Helper()
	rows, err := s.db.Query(context.Background(), `
		SELECT COALESCE(from_status::text, ''), to_status::text
		FROM food.order_status_history
		WHERE order_id = $1
		ORDER BY created_at, id
	`, orderID)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	defer rows.Close()
	var out []historyRow
	for rows.Next() {
		var r historyRow
		if err := rows.Scan(&r.From, &r.To); err != nil {
			t.Fatalf("scan history: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// assertHistoryChain walks the history rows from `start` and requires each
// step in `want` to be recorded with the REAL from_status. Rows written in one
// transaction share NOW(), so the walk links rows by from_status instead of
// trusting created_at order.
func assertHistoryChain(t *testing.T, s *Store, orderID uuid.UUID, start string, want []string) {
	t.Helper()
	rows := readHistory(t, s, orderID)
	used := make([]bool, len(rows))
	cur := start
	for _, next := range want {
		found := false
		for i, r := range rows {
			if !used[i] && r.From == cur && r.To == next {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("history has no %s -> %s row; rows=%v", cur, next, rows)
		}
		cur = next
	}
	for _, r := range rows {
		if r.From == "" {
			t.Fatalf("history row with NULL from_status: %v (all rows=%v)", r, rows)
		}
	}
}

func readAssignment(t *testing.T, s *Store, orderID uuid.UUID) (id uuid.UUID, partnerID *uuid.UUID, status string) {
	t.Helper()
	if err := s.db.QueryRow(context.Background(), `
		SELECT id, delivery_partner_id, status::text
		FROM food.delivery_assignments WHERE order_id = $1
	`, orderID).Scan(&id, &partnerID, &status); err != nil {
		t.Fatalf("read assignment: %v", err)
	}
	return id, partnerID, status
}

// seedPlaceableCart creates an ACTIVE, open restaurant (optionally without
// coordinates), one menu item (250, 5% tax) and a customer whose cart holds
// one of it. Returns the customer, restaurant, owner and menu item ids.
func seedPlaceableCart(t *testing.T, s *Store, lat, lng *float64) (customerID, restaurantID, ownerID, menuItemID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	ownerID = uuid.New()
	customerID = uuid.New()
	var partnerID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.restaurant_partners (owner_user_id, legal_name, status)
		VALUES ($1, 'Test Partner', 'APPROVED') RETURNING id
	`, ownerID).Scan(&partnerID); err != nil {
		t.Fatalf("seed partner: %v", err)
	}
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.restaurants
			(partner_id, name, slug, owner_user_id, status, is_open, is_accepting_orders,
			 address_line1, city, state, tax_category, latitude, longitude, min_order_amount, packaging_fee,
			 avg_preparation_minutes, commission_percentage)
		VALUES ($1, $2, $3, $4, 'ACTIVE', TRUE, TRUE, '1 Test Lane', 'Bengaluru', 'Karnataka', 'RESTAURANT_STANDALONE', $5, $6, 0, 0, 20, 10)
		RETURNING id
	`, partnerID, "Test "+uuid.NewString()[:8], "test-"+uuid.NewString()[:8], ownerID, lat, lng).Scan(&restaurantID); err != nil {
		t.Fatalf("seed restaurant: %v", err)
	}
	var categoryID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.menu_categories (restaurant_id, name, sort_order) VALUES ($1, 'Mains', 1) RETURNING id
	`, restaurantID).Scan(&categoryID); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.menu_items
			(restaurant_id, category_id, name, base_price, food_type, preparation_minutes,
			 is_available, is_active, tax_percentage)
		VALUES ($1, $2, 'Paneer Tikka', 250, 'VEG', 15, TRUE, TRUE, 5)
		RETURNING id
	`, restaurantID, categoryID).Scan(&menuItemID); err != nil {
		t.Fatalf("seed menu item: %v", err)
	}
	if _, err := s.AddCartItem(ctx, customerID, AddCartItemInput{MenuItemID: menuItemID, Quantity: 1}); err != nil {
		t.Fatalf("add cart item: %v", err)
	}
	return customerID, restaurantID, ownerID, menuItemID
}

func seedAddress(t *testing.T, s *Store, userID uuid.UUID, lat, lng *float64) uuid.UUID {
	t.Helper()
	a, err := s.CreateAddress(context.Background(), userID, AddressInput{
		Label: "Home", AddressLine1: "2 Test Road", City: "Bengaluru", Latitude: lat, Longitude: lng,
	})
	if err != nil {
		t.Fatalf("seed address: %v", err)
	}
	return a.ID
}

func f64(v float64) *float64 { return &v }

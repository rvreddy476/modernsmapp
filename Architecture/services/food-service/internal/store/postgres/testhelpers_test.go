package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/atpost/food-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// foodTestStore returns a *Store backed by TEST_PG_DSN, applying the
// food schema first so a fresh test container is fully ready. Skips
// the test if TEST_PG_DSN is unset (CI runs unit-only).
//
// Mirrors rider-service/internal/store/testhelpers_test.go.
func foodTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping food-service store integration tests")
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
	return New(pool), func() { pool.Close() }
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
			 address_line1, city, min_order_amount, packaging_fee,
			 avg_preparation_minutes, commission_percentage)
		VALUES ($1, $2, $3, $4, 'ACTIVE', TRUE, TRUE,
			'1 Test Lane', 'Bengaluru', 0, 0, 20, 10)
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

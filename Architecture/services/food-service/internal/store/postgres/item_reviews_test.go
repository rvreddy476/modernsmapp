package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestCreateItemReview_UpdatesAggregateAtomically locks in the
// cf57a26 fix: creating a review must update menu_items.avg_rating +
// rating_count in the same tx, and hiding must recompute it.
func TestCreateItemReview_UpdatesAggregateAtomically(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, menuItemID, customerID := seedOrderWithItem(t, s, "DELIVERED")

	r1, err := s.CreateItemReview(ctx, CreateItemReviewInput{
		OrderID: orderID, MenuItemID: menuItemID, CustomerID: customerID,
		Rating: 4, Review: "tasty",
	})
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	if r1.Rating != 4 {
		t.Fatalf("rating: want 4, got %d", r1.Rating)
	}

	// Aggregate after 1 review.
	avg, count := readAggregate(t, s, menuItemID)
	if count != 1 || avg != 4.00 {
		t.Fatalf("after 1 review: want (1, 4.00), got (%d, %.2f)", count, avg)
	}

	// Second review by a different customer on a different order — needs
	// its own seeded order because of the UNIQUE (order, item, customer)
	// constraint.
	order2, _, customer2 := seedOrderWithItem(t, s, "DELIVERED")
	// Use the SAME menu_item_id, so we need to also insert an order_items
	// row that references it for the new order.
	if _, err := s.db.Exec(ctx, `
		INSERT INTO food.order_items
			(order_id, menu_item_id, item_name_snapshot, food_type_snapshot,
			 unit_price_snapshot, quantity, line_total)
		VALUES ($1, $2, 'Paneer Tikka', 'VEG', 250, 1, 250)
	`, order2, menuItemID); err != nil {
		t.Fatalf("seed second order item: %v", err)
	}
	if _, err := s.CreateItemReview(ctx, CreateItemReviewInput{
		OrderID: order2, MenuItemID: menuItemID, CustomerID: customer2,
		Rating: 2, Review: "meh",
	}); err != nil {
		t.Fatalf("create second review: %v", err)
	}
	avg, count = readAggregate(t, s, menuItemID)
	if count != 2 || avg != 3.00 {
		t.Fatalf("after 2 reviews: want (2, 3.00), got (%d, %.2f)", count, avg)
	}

	// Hide the first review — aggregate must drop back to (1, 2.00).
	if err := s.HideItemReview(ctx, r1.ID); err != nil {
		t.Fatalf("hide review: %v", err)
	}
	avg, count = readAggregate(t, s, menuItemID)
	if count != 1 || avg != 2.00 {
		t.Fatalf("after hide: want (1, 2.00), got (%d, %.2f)", count, avg)
	}
}

func TestCreateItemReview_RejectsNonDeliveredOrder(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, menuItemID, customerID := seedOrderWithItem(t, s, "CONFIRMED")

	if _, err := s.CreateItemReview(ctx, CreateItemReviewInput{
		OrderID: orderID, MenuItemID: menuItemID, CustomerID: customerID,
		Rating: 5,
	}); err == nil {
		t.Fatal("expected rejection — order not DELIVERED")
	}
}

func TestCreateItemReview_RejectsDuplicate(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, menuItemID, customerID := seedOrderWithItem(t, s, "DELIVERED")
	if _, err := s.CreateItemReview(ctx, CreateItemReviewInput{
		OrderID: orderID, MenuItemID: menuItemID, CustomerID: customerID,
		Rating: 4,
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := s.CreateItemReview(ctx, CreateItemReviewInput{
		OrderID: orderID, MenuItemID: menuItemID, CustomerID: customerID,
		Rating: 5,
	}); err == nil {
		t.Fatal("expected UNIQUE constraint violation on duplicate review")
	}
}

func readAggregate(t *testing.T, s *Store, menuItemID uuid.UUID) (avg float64, count int) {
	t.Helper()
	var avgNullable *float64
	if err := s.db.QueryRow(context.Background(), `
		SELECT COALESCE(avg_rating, 0)::float8, rating_count
		FROM food.menu_items WHERE id = $1
	`, menuItemID).Scan(&avgNullable, &count); err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	if avgNullable != nil {
		avg = *avgNullable
	}
	return
}

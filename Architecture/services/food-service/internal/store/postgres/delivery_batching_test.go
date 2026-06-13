package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestDeliveryBatching_HappyPath covers:
//
//  1. ListUnbatchedReadyOrders surfaces DELIVERY_ASSIGNING orders that
//     have neither an active assignment nor a pending offer.
//  2. CreateBatch creates the batch + links member assignments with
//     ascending batch_sequence.
//  3. CreateDeliveryOfferForBatch + AcceptBatchOfferTx flips every
//     member order to DELIVERY_ASSIGNED in one tx and marks the batch
//     `assigned`. Sibling offers are superseded.
//
// Gated on TEST_PG_DSN (mirrors the rest of food-service store tests).
func TestDeliveryBatching_HappyPath(t *testing.T) {
	store, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Seed 2 orders at the same restaurant + a partner.
	restaurantID, o1 := seedOrderInState(t, store, "DELIVERY_ASSIGNING")
	o2 := seedOrderInRestaurant(t, store, restaurantID, "DELIVERY_ASSIGNING")
	userID, _ := seedDeliveryPartner(t, store)

	// 1. List should return both orders, grouped by restaurant.
	ready, err := store.ListUnbatchedReadyOrders(ctx, 25)
	if err != nil {
		t.Fatalf("list ready: %v", err)
	}
	if len(ready) < 2 {
		t.Fatalf("want >=2 ready orders, got %d", len(ready))
	}

	// 2. Create the batch with both orders.
	batch, err := store.CreateBatch(ctx, restaurantID, []uuid.UUID{o1, o2})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if batch.Status != "pending" {
		t.Errorf("want batch status pending, got %s", batch.Status)
	}
	if len(batch.Members) != 2 {
		t.Fatalf("want 2 members, got %d", len(batch.Members))
	}
	if batch.Members[0].Sequence != 1 || batch.Members[1].Sequence != 2 {
		t.Errorf("want sequences [1,2], got [%d,%d]", batch.Members[0].Sequence, batch.Members[1].Sequence)
	}

	// 3. Create a batch offer for the partner.
	expiresAt := time.Now().Add(30 * time.Second)
	offer, err := store.CreateDeliveryOfferForBatch(ctx, batch.ID, o1, userID, expiresAt)
	if err != nil {
		// Partner id ≠ userID — CreateDeliveryOfferForBatch wants the
		// partner.id, not user_id. Resolve and retry.
		var partnerID uuid.UUID
		if err := store.db.QueryRow(ctx, `SELECT id FROM food.delivery_partners WHERE user_id = $1`, userID).Scan(&partnerID); err != nil {
			t.Fatalf("partner lookup: %v", err)
		}
		offer, err = store.CreateDeliveryOfferForBatch(ctx, batch.ID, o1, partnerID, expiresAt)
		if err != nil {
			t.Fatalf("create batch offer: %v", err)
		}
	}
	if offer.Status != "pending" {
		t.Errorf("want offer status pending, got %s", offer.Status)
	}

	// 4. Accept the batch offer.
	acceptedBatch, partnerID, err := store.AcceptBatchOfferTx(ctx, userID, offer.ID)
	if err != nil {
		t.Fatalf("accept batch: %v", err)
	}
	if acceptedBatch.Status != "assigned" {
		t.Errorf("want batch assigned, got %s", acceptedBatch.Status)
	}
	if partnerID == uuid.Nil {
		t.Errorf("want non-nil partner id")
	}
	if len(acceptedBatch.Members) != 2 {
		t.Fatalf("want 2 accepted members, got %d", len(acceptedBatch.Members))
	}

	// 5. Both orders should now be DELIVERY_ASSIGNED.
	var o1Status, o2Status string
	if err := store.db.QueryRow(ctx, `SELECT status::text FROM food.orders WHERE id = $1`, o1).Scan(&o1Status); err != nil {
		t.Fatalf("o1 status: %v", err)
	}
	if err := store.db.QueryRow(ctx, `SELECT status::text FROM food.orders WHERE id = $1`, o2).Scan(&o2Status); err != nil {
		t.Fatalf("o2 status: %v", err)
	}
	if o1Status != "DELIVERY_ASSIGNED" || o2Status != "DELIVERY_ASSIGNED" {
		t.Errorf("want both DELIVERY_ASSIGNED, got %s + %s", o1Status, o2Status)
	}

	// 6. GetBatchForOrder works for either member.
	for _, oid := range []uuid.UUID{o1, o2} {
		got, err := store.GetBatchForOrder(ctx, oid)
		if err != nil {
			t.Fatalf("get batch for %s: %v", oid, err)
		}
		if got.ID != batch.ID {
			t.Errorf("want batch %s, got %s", batch.ID, got.ID)
		}
	}
}

// TestDeliveryBatching_RejectsAlreadyAssignedOrder confirms CreateBatch
// can't steal an order that already has an ASSIGNED assignment (i.e.
// the WHERE clause on the ON CONFLICT DO UPDATE works as expected).
func TestDeliveryBatching_RejectsAlreadyAssignedOrder(t *testing.T) {
	store, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	restaurantID, o1 := seedOrderInState(t, store, "DELIVERY_ASSIGNING")
	o2 := seedOrderInRestaurant(t, store, restaurantID, "DELIVERY_ASSIGNING")
	userID, partnerID := seedDeliveryPartner(t, store)
	_ = userID
	// Manually assign o1 to the partner first.
	seedDeliveryAssignment(t, store, o1, partnerID)

	// Now attempt to batch both. CreateBatch will insert the batch row
	// and try to link both orders. o1's link should be skipped (WHERE
	// status='CREATED' false), o2's link should succeed.
	batch, err := store.CreateBatch(ctx, restaurantID, []uuid.UUID{o1, o2})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	// Verify o1's assignment was not stomped.
	var o1BatchID *uuid.UUID
	var o1Status string
	if err := store.db.QueryRow(ctx, `
		SELECT batch_id, status::text FROM food.delivery_assignments WHERE order_id = $1
	`, o1).Scan(&o1BatchID, &o1Status); err != nil {
		t.Fatalf("o1 assignment: %v", err)
	}
	if o1Status != "ASSIGNED" {
		t.Errorf("want o1 still ASSIGNED, got %s", o1Status)
	}
	if o1BatchID != nil && *o1BatchID == batch.ID {
		t.Errorf("o1 should not be linked to new batch")
	}
	// And o2 should be in the batch.
	var o2BatchID uuid.UUID
	if err := store.db.QueryRow(ctx, `
		SELECT batch_id FROM food.delivery_assignments WHERE order_id = $1
	`, o2).Scan(&o2BatchID); err != nil {
		t.Fatalf("o2 assignment: %v", err)
	}
	if o2BatchID != batch.ID {
		t.Errorf("want o2 linked to %s, got %s", batch.ID, o2BatchID)
	}
}

// seedOrderInState is a thin wrapper around seedOrderWithItem that also
// returns the restaurant_id so batching tests can seed sibling orders
// at the same restaurant.
func seedOrderInState(t *testing.T, s *Store, status string) (restaurantID, orderID uuid.UUID) {
	t.Helper()
	orderID, _, _ = seedOrderWithItem(t, s, status)
	if err := s.db.QueryRow(context.Background(), `
		SELECT restaurant_id FROM food.orders WHERE id = $1
	`, orderID).Scan(&restaurantID); err != nil {
		t.Fatalf("lookup restaurant: %v", err)
	}
	return
}

// seedOrderInRestaurant inserts a second order at an existing
// restaurant. Reuses an existing menu_item under that restaurant.
func seedOrderInRestaurant(t *testing.T, s *Store, restaurantID uuid.UUID, status string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	customerID := uuid.New()
	var menuItemID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		SELECT id FROM food.menu_items WHERE restaurant_id = $1 LIMIT 1
	`, restaurantID).Scan(&menuItemID); err != nil {
		t.Fatalf("lookup menu item: %v", err)
	}
	var orderID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.orders
			(order_number, user_id, restaurant_id, status, payment_status, payment_method,
			 restaurant_name_snapshot, restaurant_address_snapshot, delivery_address_snapshot,
			 item_subtotal, final_amount, commission_percentage_snapshot, commission_amount,
			 placed_at)
		VALUES ($1, $2, $3, $4::food.order_status, 'CAPTURED', 'ONLINE',
			'Test', '{}'::jsonb, '{}'::jsonb,
			250, 250, 10, 25, NOW())
		RETURNING id
	`, "TEST-"+uuid.NewString()[:8], customerID, restaurantID, status).Scan(&orderID); err != nil {
		t.Fatalf("seed sibling order: %v", err)
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO food.order_items
			(order_id, menu_item_id, item_name_snapshot, food_type_snapshot,
			 unit_price_snapshot, quantity, line_total)
		VALUES ($1, $2, 'Paneer Tikka', 'VEG', 250, 1, 250)
	`, orderID, menuItemID); err != nil {
		t.Fatalf("seed sibling order item: %v", err)
	}
	return orderID
}

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Food server lane (9 Nov 2026): the rules only the database proves. These
// run in CI on a _test database (TEST_PG_DSN); locally they skip.

// A placement whose expected total differs is refused before anything is
// written; the server's total is reported and is the one an order then gets.
func TestPlaceOrder_ExpectedTotalMismatchWritesNothing(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	customer, _, _, _ := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	addr := seedAddress(t, s, customer, f64(12.901), f64(77.6))

	wrong := int64(1)
	key := uuid.NewString()
	_, err := s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD", ExpectedTotalPaise: &wrong}, key)
	var changed *OrderTotalChangedError
	if !errors.As(err, &changed) || changed.ServerTotalPaise <= 0 || changed.ExpectedTotalPaise != 1 {
		t.Fatalf("want OrderTotalChangedError with the server total, got %v", err)
	}
	var orders, keys, cartItems int
	if err := s.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM food.orders WHERE user_id=$1),
		(SELECT count(*) FROM food.idempotency_keys WHERE user_id=$1 AND key=$2),
		(SELECT count(*) FROM food.cart_items ci JOIN food.carts c ON c.id=ci.cart_id WHERE c.user_id=$1)`,
		customer, key).Scan(&orders, &keys, &cartItems); err != nil {
		t.Fatal(err)
	}
	if orders != 0 || keys != 0 || cartItems == 0 {
		t.Fatalf("a refused placement wrote: orders=%d keys=%d cart_items=%d", orders, keys, cartItems)
	}

	// The same key, now with the server's total, places the order at it.
	shown := changed.ServerTotalPaise
	order, err := s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD", ExpectedTotalPaise: &shown}, key)
	if err != nil {
		t.Fatalf("placement at the server total: %v", err)
	}
	var charged int64
	if err := s.db.QueryRow(ctx, `SELECT final_amount_paise FROM food.orders WHERE id=$1`, order.ID).Scan(&charged); err != nil {
		t.Fatal(err)
	}
	if charged != shown {
		t.Fatalf("order total %d, want the server's %d", charged, shown)
	}
}

// The status filter and cursor walk only the owner's orders, newest first,
// with no order twice and none left out.
func TestListPartnerOrdersPage_FilterAndCursor(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	restaurant, first := seedOrderInState(t, s, "CONFIRMED")
	want := map[uuid.UUID]bool{first: true}
	for i := 0; i < 2; i++ {
		want[seedOrderInRestaurant(t, s, restaurant, "CONFIRMED")] = true
	}
	delivered := seedOrderInRestaurant(t, s, restaurant, "DELIVERED")
	var owner uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT owner_user_id FROM food.restaurants WHERE id=$1`, restaurant).Scan(&owner); err != nil {
		t.Fatal(err)
	}

	seen := map[uuid.UUID]bool{}
	var after *OrderCursor
	for page := 0; ; page++ {
		orders, next, err := s.ListPartnerOrdersPage(ctx, owner, restaurant, PartnerOrderFilter{Statuses: []string{"CONFIRMED"}, Limit: 2, After: after})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range orders {
			if o.Status != "CONFIRMED" || seen[o.ID] || o.RestaurantID != restaurant {
				t.Fatalf("page %d: order %s status %s repeated=%v", page, o.ID, o.Status, seen[o.ID])
			}
			seen[o.ID] = true
		}
		if next == nil {
			break
		}
		if page > 3 {
			t.Fatal("cursor never ended")
		}
		after = next
	}
	if len(seen) != len(want) {
		t.Fatalf("walked %d confirmed orders, want %d", len(seen), len(want))
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("confirmed order %s left out", id)
		}
	}
	all, next, err := s.ListPartnerOrdersPage(ctx, owner, restaurant, PartnerOrderFilter{})
	if err != nil || next != nil || len(all) != 4 {
		t.Fatalf("unfiltered: %d orders, next=%v, err=%v", len(all), next, err)
	}
	if all[0].ID != delivered {
		t.Fatalf("newest first: got %s, want the last seeded order %s", all[0].ID, delivered)
	}
	if _, _, err := s.ListPartnerOrdersPage(ctx, uuid.New(), restaurant, PartnerOrderFilter{}); err == nil {
		t.Fatal("a stranger read the restaurant's orders")
	}
}

// A resend under the same Idempotency-Key gets the first answer, not the
// balance refusal; the same key for another order is refused.
func TestRequestRefund_IdempotencyKeyReplaysTheFirstAnswer(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	order, owner, _ := seedPaymentOrder(t, s, "DELIVERED", "CAPTURED")
	key := uuid.NewString()
	in := RefundRequestInput{CustomerID: owner, OrderID: order, Amount: 250, Reason: "late", IdempotencyKey: key}
	first, err := s.RequestRefund(ctx, in)
	if err != nil || first.Replayed {
		t.Fatalf("first request: %+v %v", first, err)
	}
	again, err := s.RequestRefund(ctx, in)
	if err != nil || !again.Replayed || again.ID != first.ID {
		t.Fatalf("resend: %+v %v, want the first request %s", again, err, first.ID)
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM food.refund_requests WHERE order_id=$1`, order).Scan(&n); err != nil || n != 1 {
		t.Fatalf("refund requests = %d %v, want 1", n, err)
	}
	// Without a key the same resend is refused by the balance, as before.
	in.IdempotencyKey = ""
	if _, err := s.RequestRefund(ctx, in); !errors.Is(err, ErrInvalidRefundAmount) {
		t.Fatalf("keyless resend: %v", err)
	}
	// The same key for another order is a reuse, never that order's answer.
	if _, err := s.RequestRefund(ctx, RefundRequestInput{CustomerID: owner, OrderID: uuid.New(), Amount: 10, IdempotencyKey: key}); !errors.Is(err, ErrIdempotencyPayloadMismatch) {
		t.Fatalf("key reused for another order: %v", err)
	}
}

func TestCreateTicket_IdempotencyKeyReplaysTheFirstTicket(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	order, owner, _ := seedPaymentOrder(t, s, "DELIVERED", "CAPTURED")
	in := CreateTicketInput{CustomerID: owner, OrderID: &order, Category: "refund", Subject: "Refund request", IdempotencyKey: uuid.NewString()}
	first, err := s.CreateTicket(ctx, in)
	if err != nil || first.Replayed {
		t.Fatalf("first ticket: %+v %v", first, err)
	}
	again, err := s.CreateTicket(ctx, in)
	if err != nil || !again.Replayed || again.ID != first.ID {
		t.Fatalf("resend: %+v %v, want ticket %s", again, err, first.ID)
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM food.support_tickets WHERE customer_id=$1`, owner).Scan(&n); err != nil || n != 1 {
		t.Fatalf("tickets = %d %v, want 1", n, err)
	}
	// Keyless calls open a ticket each, as before.
	in.IdempotencyKey = ""
	if _, err := s.CreateTicket(ctx, in); err != nil {
		t.Fatal(err)
	}
}

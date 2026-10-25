package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCheckoutRequestHash(t *testing.T) {
	in := PlaceOrderInput{AddressID: uuid.New(), PaymentMethod: "ONLINE", PaymentInstrument: "upi"}
	if first, again := checkoutRequestHash(in), checkoutRequestHash(in); first != again {
		t.Fatal("unstable hash")
	}
	changed := in
	changed.CustomerInstruction = "another request"
	if checkoutRequestHash(in) == checkoutRequestHash(changed) {
		t.Fatal("payload not bound")
	}
}

func TestCheckoutFinalValidation(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	for _, name := range []string{"sold out", "archived", "variant unavailable", "variant removed", "foreign variant", "addon unavailable", "required addon added", "restaurant minimum", "valid"} {
		t.Run(name, func(t *testing.T) {
			customer, restaurant, _, item := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
			addr := seedAddress(t, s, customer, f64(12.901), f64(77.6))
			variant := uuid.New()
			_, err := s.db.Exec(ctx, `INSERT INTO food.menu_item_variants(id,menu_item_id,name,price) VALUES($1,$2,'Large',300)`, variant, item)
			if err != nil {
				t.Fatal(err)
			}
			_, addon := seedAddon(t, s, item, 0, 2, false, "Side", 30)
			if err := s.ClearCart(ctx, customer); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: item, VariantID: &variant, Quantity: 1, Addons: []CartAddonInput{{AddonID: addon, Quantity: 1}}}); err != nil {
				t.Fatal(err)
			}
			want := ErrCartItemUnavailable
			switch name {
			case "sold out":
				_, err = s.db.Exec(ctx, `UPDATE food.menu_items SET is_available=false WHERE id=$1`, item)
			case "archived":
				_, err = s.db.Exec(ctx, `UPDATE food.menu_items SET is_active=false WHERE id=$1`, item)
			case "variant unavailable":
				want = ErrCartVariantInvalid
				_, err = s.db.Exec(ctx, `UPDATE food.menu_item_variants SET is_available=false WHERE id=$1`, variant)
			case "variant removed":
				want = ErrCartVariantInvalid
				_, err = s.db.Exec(ctx, `DELETE FROM food.menu_item_variants WHERE id=$1`, variant)
			case "foreign variant":
				want = ErrCartVariantInvalid
				_, _, _, foreign := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
				_, err = s.db.Exec(ctx, `UPDATE food.menu_item_variants SET menu_item_id=$2 WHERE id=$1`, variant, foreign)
			case "addon unavailable":
				want = ErrAddonInvalid
				_, err = s.db.Exec(ctx, `UPDATE food.menu_item_addons SET is_available=false WHERE id=$1`, addon)
			case "required addon added":
				want = ErrAddonInvalid
				seedAddon(t, s, item, 1, 1, true, "Required choice", 10)
			case "restaurant minimum":
				want = ErrRestaurantMinimum
				_, err = s.db.Exec(ctx, `UPDATE food.restaurants SET min_order_amount=500 WHERE id=$1`, restaurant)
			case "valid":
				want = nil
			}
			if err != nil {
				t.Fatal(err)
			}
			order, err := s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString())
			if !errors.Is(err, want) {
				t.Fatalf("want %v got %v", want, err)
			}
			if want == nil {
				if order == nil {
					t.Fatal("missing order")
				}
				return
			}
			var count int
			if err := s.db.QueryRow(ctx, `SELECT count(*) FROM food.orders WHERE user_id=$1`, customer).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("invalid checkout wrote %d orders", count)
			}
		})
	}
}

func TestCheckoutVariantOwnershipAtAdd(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	customer, _, _, item := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	_, _, _, foreign := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	variant := uuid.New()
	if _, err := s.db.Exec(ctx, `INSERT INTO food.menu_item_variants(id,menu_item_id,name,price) VALUES($1,$2,'Foreign',1)`, variant, foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: item, VariantID: &variant, Quantity: 1}); !errors.Is(err, ErrCartVariantInvalid) {
		t.Fatalf("foreign variant: %v", err)
	}
}

func TestCheckoutConcurrentIdempotency(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	customer, _, _, _ := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	addr := seedAddress(t, s, customer, f64(12.901), f64(77.6))
	in := PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}
	key := uuid.NewString()
	const n = 8
	orders := make([]*Order, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; orders[i], errs[i] = s.PlaceOrder(ctx, customer, in, key) }(i)
	}
	close(start)
	wg.Wait()
	for i := range orders {
		if errs[i] != nil {
			t.Fatalf("request %d: %v", i, errs[i])
		}
		if orders[i].ID != orders[0].ID {
			t.Fatal("duplicate order")
		}
	}
	changed := in
	changed.CustomerInstruction = "changed"
	if _, err := s.PlaceOrder(ctx, customer, changed, key); !errors.Is(err, ErrIdempotencyPayloadMismatch) {
		t.Fatalf("changed payload: %v", err)
	}
	var count int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM food.orders WHERE user_id=$1`, customer).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("got %d orders", count)
	}
}

func TestCheckoutWaitsForMenuEditAndRejectsNewStockout(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	customer, _, _, item := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	addr := seedAddress(t, s, customer, f64(12.901), f64(77.6))
	tx, err := s.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	if _, err := tx.Exec(ctx, `UPDATE food.menu_items SET is_available=false WHERE id=$1`, item); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString())
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("checkout did not wait for concurrent edit: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrCartItemUnavailable) {
		t.Fatalf("expected stockout refusal: %v", err)
	}
}

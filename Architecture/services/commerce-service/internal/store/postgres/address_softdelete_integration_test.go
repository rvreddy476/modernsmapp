//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Audit 2026-10-30 L5: an address an order references is hidden, not a
// foreign-key error; the order keeps it; an unreferenced one is removed.
func TestDeleteAddress_UsedByAnOrderIsHiddenNotRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newFixture(t, 10, 100000, "18")
	f.addToCart(1, 100000)
	res, err := store.Checkout(ctx, f.params(f.quote(0), "idem-"+uuid.NewString()))
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}

	// A stranger cannot delete it.
	if err := store.DeleteAddress(ctx, f.addressID, uuid.New()); !errors.Is(err, ErrAddressNotFound) {
		t.Fatalf("stranger delete: err = %v, want ErrAddressNotFound", err)
	}

	if err := store.DeleteAddress(ctx, f.addressID, f.userID); err != nil {
		t.Fatalf("delete an address an order uses: %v", err)
	}
	book, err := store.GetAddressRowsByUser(ctx, f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(book) != 0 {
		t.Fatalf("address book still lists %d address(es) after delete", len(book))
	}
	if _, ok, err := store.DefaultAddressPincode(ctx, f.userID); err != nil || ok {
		t.Fatalf("default pincode still served from a deleted address: ok=%v err=%v", ok, err)
	}
	// The row the order points at is still there, marked.
	row, err := store.GetAddressRow(ctx, f.addressID)
	if err != nil || !row.Deleted {
		t.Fatalf("referenced row: row=%+v err=%v, want present and Deleted", row, err)
	}
	var ref uuid.UUID
	if err := testPool.QueryRow(ctx, `SELECT delivery_address_id FROM orders WHERE id=$1`, res.OrderID).Scan(&ref); err != nil || ref != f.addressID {
		t.Fatalf("order lost its address: %v %v", ref, err)
	}
	// Deleted twice, set default, and checkout all refuse it.
	if err := store.DeleteAddress(ctx, f.addressID, f.userID); !errors.Is(err, ErrAddressNotFound) {
		t.Fatalf("second delete: err = %v", err)
	}
	if err := store.SetDefaultAddress(ctx, f.addressID, f.userID); err == nil {
		t.Fatal("a deleted address became the default")
	}
	f.addToCart(1, 100000)
	if _, err := store.Checkout(ctx, f.params(f.quote(0), "idem-"+uuid.NewString())); !errors.Is(err, ErrAddressNotOwned) {
		t.Fatalf("checkout to a deleted address: err = %v, want ErrAddressNotOwned", err)
	}
}

func TestDeleteAddress_UnreferencedIsRemoved(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newFixture(t, 10, 100000, "18")
	if err := store.DeleteAddress(ctx, f.addressID, f.userID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM customer_addresses WHERE id=$1`, f.addressID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("unreferenced address rows = %d (%v), want removed", n, err)
	}
}

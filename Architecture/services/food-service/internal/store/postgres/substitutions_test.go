package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestProposeSubstitution_RejectsWrongStatus locks the
// "proposed/preparing only" gate in the store.
func TestProposeSubstitution_RejectsWrongStatus(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERED")
	ownerID := readRestaurantOwner(t, s, orderID)
	originalItemID := readFirstOrderItemID(t, s, orderID)
	suggestedName := "Soya chaap"

	if _, err := s.ProposeSubstitution(ctx, ownerID, ProposeSubstitutionInput{
		OrderID:           orderID,
		OriginalItemID:    originalItemID,
		SuggestedItemName: &suggestedName,
		PriceDiff:         0,
		ProposedBy:        ownerID,
	}); err == nil {
		t.Fatal("expected refusal — DELIVERED order can't substitute")
	}
}

// TestProposeAndAcceptSubstitution_HappyPath drives the proposed →
// approved transition end-to-end.
func TestProposeAndAcceptSubstitution_HappyPath(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, customerID := seedOrderWithItem(t, s, "CONFIRMED")
	ownerID := readRestaurantOwner(t, s, orderID)
	originalItemID := readFirstOrderItemID(t, s, orderID)
	suggestedName := "Soya chaap"

	sub, err := s.ProposeSubstitution(ctx, ownerID, ProposeSubstitutionInput{
		OrderID:           orderID,
		OriginalItemID:    originalItemID,
		SuggestedItemName: &suggestedName,
		PriceDiff:         20,
		ProposedBy:        ownerID,
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if sub.Status != "proposed" {
		t.Fatalf("status after propose: %s", sub.Status)
	}

	got, err := s.RespondToSubstitution(ctx, customerID, sub.ID, "approved")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got.Status != "approved" {
		t.Fatalf("status after approve: %s", got.Status)
	}

	// Replay must be rejected — already responded.
	if _, err := s.RespondToSubstitution(ctx, customerID, sub.ID, "declined"); err == nil {
		t.Fatal("expected refusal on replay")
	}
}

// TestRespondToSubstitution_RejectsForeignCustomer ensures only the
// order owner can respond.
func TestRespondToSubstitution_RejectsForeignCustomer(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "CONFIRMED")
	ownerID := readRestaurantOwner(t, s, orderID)
	originalItemID := readFirstOrderItemID(t, s, orderID)
	suggestedName := "Soya chaap"

	sub, err := s.ProposeSubstitution(ctx, ownerID, ProposeSubstitutionInput{
		OrderID:           orderID,
		OriginalItemID:    originalItemID,
		SuggestedItemName: &suggestedName,
		ProposedBy:        ownerID,
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	stranger := uuid.New()
	if _, err := s.RespondToSubstitution(ctx, stranger, sub.ID, "approved"); err == nil {
		t.Fatal("expected refusal — stranger is not the order customer")
	}
}

func readRestaurantOwner(t *testing.T, s *Store, orderID uuid.UUID) uuid.UUID {
	t.Helper()
	var owner uuid.UUID
	if err := s.db.QueryRow(context.Background(), `
		SELECT r.owner_user_id
		FROM food.orders o
		JOIN food.restaurants r ON r.id = o.restaurant_id
		WHERE o.id = $1
	`, orderID).Scan(&owner); err != nil {
		t.Fatalf("read owner: %v", err)
	}
	return owner
}

func readFirstOrderItemID(t *testing.T, s *Store, orderID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := s.db.QueryRow(context.Background(), `
		SELECT id FROM food.order_items WHERE order_id = $1 ORDER BY created_at LIMIT 1
	`, orderID).Scan(&id); err != nil {
		t.Fatalf("read order item: %v", err)
	}
	return id
}

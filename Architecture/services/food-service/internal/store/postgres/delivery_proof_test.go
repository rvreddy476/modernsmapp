package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestVerifyPickupCode_HappyPath walks the restaurant-side OTP verify
// from issuance through the right code → status flip to PICKED_UP.
func TestVerifyPickupCode_HappyPath(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	_, partnerID := seedDeliveryPartner(t, s)
	seedDeliveryAssignment(t, s, orderID, partnerID)

	pickup, _, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatalf("ensure codes: %v", err)
	}
	if pickup == "" {
		t.Fatal("pickup code missing after EnsureDeliveryCodes")
	}

	ownerID := readRestaurantOwner(t, s, orderID)
	if err := s.VerifyPickupCode(ctx, ownerID, orderID, pickup); err != nil {
		t.Fatalf("verify pickup: %v", err)
	}
	assertOrderStatus(t, s, orderID, "PICKED_UP")
}

// TestVerifyPickupCode_RejectsWrongCode confirms a wrong OTP does not
// flip status (the row stays where it was).
func TestVerifyPickupCode_RejectsWrongCode(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	_, partnerID := seedDeliveryPartner(t, s)
	seedDeliveryAssignment(t, s, orderID, partnerID)
	if _, _, err := s.EnsureDeliveryCodes(ctx, orderID); err != nil {
		t.Fatalf("ensure codes: %v", err)
	}

	ownerID := readRestaurantOwner(t, s, orderID)
	if err := s.VerifyPickupCode(ctx, ownerID, orderID, "0000"); err == nil {
		t.Fatal("expected refusal on wrong pickup code")
	}
	assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNED")
}

// TestVerifyPickupCode_RejectsForeignOwner ensures only the restaurant
// owner can verify the pickup, even with the right code.
func TestVerifyPickupCode_RejectsForeignOwner(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	_, partnerID := seedDeliveryPartner(t, s)
	seedDeliveryAssignment(t, s, orderID, partnerID)
	pickup, _, _ := s.EnsureDeliveryCodes(ctx, orderID)

	stranger := uuid.New()
	if err := s.VerifyPickupCode(ctx, stranger, orderID, pickup); err == nil {
		t.Fatal("expected refusal — stranger is not the restaurant owner")
	}
	assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNED")
}

// TestVerifyDeliveryCode_HappyPath walks the customer-side OTP at drop.
func TestVerifyDeliveryCode_HappyPath(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, customerID := seedOrderWithItem(t, s, "OUT_FOR_DELIVERY")
	_, partnerID := seedDeliveryPartner(t, s)
	seedDeliveryAssignment(t, s, orderID, partnerID)

	_, delivery, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatalf("ensure codes: %v", err)
	}
	if err := s.VerifyDeliveryCode(ctx, customerID, orderID, delivery); err != nil {
		t.Fatalf("verify delivery: %v", err)
	}
	assertOrderStatus(t, s, orderID, "DELIVERED")
}

// TestVerifyDeliveryCode_RejectsForeignCustomer ensures only the order
// owner can verify the drop.
func TestVerifyDeliveryCode_RejectsForeignCustomer(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "OUT_FOR_DELIVERY")
	_, partnerID := seedDeliveryPartner(t, s)
	seedDeliveryAssignment(t, s, orderID, partnerID)
	_, delivery, _ := s.EnsureDeliveryCodes(ctx, orderID)

	stranger := uuid.New()
	if err := s.VerifyDeliveryCode(ctx, stranger, orderID, delivery); err == nil {
		t.Fatal("expected refusal — stranger is not the order customer")
	}
	assertOrderStatus(t, s, orderID, "OUT_FOR_DELIVERY")
}

// TestEnsureDeliveryCodes_Idempotent re-runs the mint and asserts the
// codes don't change — important because AcceptDeliveryOffer + any
// retry of the same flow call EnsureDeliveryCodes.
func TestEnsureDeliveryCodes_Idempotent(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	_, partnerID := seedDeliveryPartner(t, s)
	seedDeliveryAssignment(t, s, orderID, partnerID)

	pickup1, delivery1, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatalf("first mint: %v", err)
	}
	pickup2, delivery2, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatalf("second mint: %v", err)
	}
	if pickup1 != pickup2 || delivery1 != delivery2 {
		t.Fatalf("codes changed on re-mint: %s/%s -> %s/%s",
			pickup1, delivery1, pickup2, delivery2)
	}
}

func assertOrderStatus(t *testing.T, s *Store, orderID uuid.UUID, want string) {
	t.Helper()
	var got string
	if err := s.db.QueryRow(context.Background(), `
		SELECT status::text FROM food.orders WHERE id = $1
	`, orderID).Scan(&got); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if got != want {
		t.Fatalf("status mismatch: want %s, got %s", want, got)
	}
}

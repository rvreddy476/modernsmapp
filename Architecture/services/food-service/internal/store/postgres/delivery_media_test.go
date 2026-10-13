package postgres

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestProofMediaAssignmentOwnershipAndImmutability(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	order, _, _ := seedOrderWithItem(t, s, "OUT_FOR_DELIVERY")
	rider, partner := seedDeliveryPartner(t, s)
	assignment := seedDeliveryAssignmentWithStatus(t, s, order, &partner, "ARRIVED_AT_CUSTOMER")
	media := uuid.New()
	if err := s.AttachProofURL(ctx, uuid.New(), order, "delivery", media.String()); err == nil {
		t.Fatal("foreign rider accepted")
	}
	if err := s.AttachProofURL(ctx, rider, order, "delivery", "https://example.com"); err == nil {
		t.Fatal("external URL accepted")
	}
	for i := 0; i < 2; i++ {
		if err := s.AttachProofURL(ctx, rider, order, "delivery", media.String()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AttachProofURL(ctx, rider, order, "delivery", uuid.New().String()); err == nil {
		t.Fatal("existing evidence overwritten")
	}
	if _, err := s.db.Exec(ctx, `UPDATE food.delivery_assignments SET status='CANCELLED' WHERE id=$1`, assignment); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachProofURL(ctx, rider, order, "pickup", media.String()); err == nil {
		t.Fatal("terminal assignment accepted proof")
	}
	var saved uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT proof_of_delivery_media_id FROM food.delivery_assignments WHERE id=$1`, assignment).Scan(&saved); err != nil || saved != media {
		t.Fatal("media binding lost", err)
	}
}

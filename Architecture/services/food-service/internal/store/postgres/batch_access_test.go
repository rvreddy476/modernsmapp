package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The batch (sibling order ids + sequence) is visible to the partner who holds
// the order's assignment and nobody else.
func TestGetBatchForOrderForPartner_AssignedPartnerOnly(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	restaurantID, o1 := seedOrderInState(t, s, "DELIVERY_ASSIGNING")
	o2 := seedOrderInRestaurant(t, s, restaurantID, "DELIVERY_ASSIGNING")
	batch, err := s.CreateBatch(ctx, restaurantID, []uuid.UUID{o1, o2})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	holder, holderPartner := seedDeliveryPartner(t, s)
	other, _ := seedDeliveryPartner(t, s)
	offer, err := s.CreateDeliveryOfferForBatch(ctx, batch.ID, o1, holderPartner, time.Now().Add(time.Minute), nil)
	if err != nil {
		t.Fatalf("offer: %v", err)
	}
	if _, _, err := s.AcceptBatchOfferTx(ctx, holder, offer.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}

	got, err := s.GetBatchForOrderForPartner(ctx, holder, o2)
	if err != nil {
		t.Fatalf("holder lookup: %v", err)
	}
	if got.ID != batch.ID || len(got.Members) != 2 {
		t.Fatalf("holder got %+v", got)
	}
	if _, err := s.GetBatchForOrderForPartner(ctx, other, o1); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("other partner: want ErrNoRows, got %v", err)
	}
	if _, err := s.GetBatchForOrderForPartner(ctx, uuid.New(), o1); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stranger: want ErrNoRows, got %v", err)
	}
	assertHistoryChain(t, s, o1, "DELIVERY_ASSIGNING", []string{"DELIVERY_ASSIGNED"})
	assertHistoryChain(t, s, o2, "DELIVERY_ASSIGNING", []string{"DELIVERY_ASSIGNED"})
}

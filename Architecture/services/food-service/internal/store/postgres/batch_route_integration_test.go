package postgres

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"testing"
	"time"
)

func TestAcceptedBatchRouteInputIntegration(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	restaurant, first := seedOrderInState(t, s, "DELIVERY_ASSIGNING")
	second := seedOrderInRestaurant(t, s, restaurant, "DELIVERY_ASSIGNING")
	batch, err := s.CreateBatch(ctx, restaurant, []uuid.UUID{first, second})
	if err != nil {
		t.Fatal(err)
	}
	rider, partner := seedDeliveryPartner(t, s)
	offer, err := s.CreateDeliveryOfferForBatch(ctx, batch.ID, first, partner, time.Now().Add(time.Minute), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AcceptBatchOfferTx(ctx, rider, offer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(ctx, `UPDATE food.restaurants SET latitude=17.37,longitude=78.48 WHERE id=$1`, restaurant); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(ctx, `UPDATE food.orders SET delivery_address_snapshot='{"latitude":17.36,"longitude":78.47}'::jsonb WHERE id=ANY($1::uuid[])`, []uuid.UUID{first, second}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetAcceptedBatchRouteInput(ctx, rider, first); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unaccepted batch leaked: %v", err)
	}
	if _, err = s.db.Exec(ctx, `UPDATE food.delivery_assignments SET status='ACCEPTED' WHERE batch_id=$1`, batch.ID); err != nil {
		t.Fatal(err)
	}
	input, err := s.GetAcceptedBatchRouteInput(ctx, rider, second)
	if err != nil || len(input.Stops) != 2 || input.Stops[0].OrderID != first {
		t.Fatalf("accepted input=%+v err=%v", input, err)
	}
	if _, err = s.GetAcceptedBatchRouteInput(ctx, uuid.New(), first); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stranger leaked: %v", err)
	}
	if _, err = s.db.Exec(ctx, `UPDATE food.orders SET status='CANCELLED_BY_ADMIN' WHERE id=$1`, second); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetAcceptedBatchRouteInput(ctx, rider, first); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cancelled sibling leaked: %v", err)
	}
}

package postgres

import (
	"context"
	"github.com/google/uuid"
	"sync"
	"testing"
)

func TestSequentialDeliveryConcurrentWorkers(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	order, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	_, p1 := seedDeliveryPartner(t, s)
	_, p2 := seedDeliveryPartner(t, s)
	var wg sync.WaitGroup
	offers := make([]*DeliveryOffer, 10)
	errs := make([]error, 10)
	for i := range offers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := p1
			if i%2 == 0 {
				p = p2
			}
			offers[i], errs[i] = s.CreateSequentialDeliveryOffer(ctx, order, p, nil, 1)
		}(i)
	}
	wg.Wait()
	n := 0
	for i, o := range offers {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if o != nil {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("offered to %d partners", n)
	}
	var events int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM food.outbox_events WHERE event_type='food.delivery.offered' AND payload->'offer'->>'order_id'=$1`, order.String()).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("outbox events=%d", events)
	}
}

func TestSequentialDeliveryRetriesOriginalBatchWithoutReplay(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	restaurant, o1 := seedOrderInState(t, s, "DELIVERY_ASSIGNING")
	o2 := seedOrderInRestaurant(t, s, restaurant, "DELIVERY_ASSIGNING")
	_, p1 := seedDeliveryPartner(t, s)
	u2, p2 := seedDeliveryPartner(t, s)
	batch, err := s.CreateBatch(ctx, restaurant, []uuid.UUID{o1, o2})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateSequentialDeliveryOffer(ctx, o1, p1, &batch.ID, 1)
	if err != nil || first == nil {
		t.Fatalf("first: %v", err)
	}
	if _, err = s.db.Exec(ctx, `UPDATE food.delivery_offers SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExpireDeliveryOffers(ctx); err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateSequentialDeliveryOffer(ctx, o1, p1, &batch.ID, 1)
	if err != nil || replay != nil {
		t.Fatalf("replay: %v %v", replay, err)
	}
	pending, err := s.ListPendingDeliveryBatches(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range pending {
		found = found || b.BatchID == batch.ID
	}
	if !found {
		t.Fatal("original batch lost")
	}
	ready, err := s.ListUnbatchedReadyOrders(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ready {
		if o.OrderID == o1 || o.OrderID == o2 {
			t.Fatal("batch orders regrouped")
		}
	}
	next, err := s.CreateSequentialDeliveryOffer(ctx, o1, p2, &batch.ID, 1)
	if err != nil || next == nil {
		t.Fatalf("next: %v", err)
	}
	accepted, _, err := s.AcceptBatchOfferTx(ctx, u2, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.ID != batch.ID || len(accepted.Members) != 2 {
		t.Fatal("batch changed")
	}
}

func TestSequentialDeliveryPartnerCanCompareTwoOffers(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	b, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	_, p := seedDeliveryPartner(t, s)
	var wg sync.WaitGroup
	offers := make([]*DeliveryOffer, 2)
	errs := make([]error, 2)
	for i, id := range []uuid.UUID{a, b} {
		wg.Add(1)
		go func(i int, id uuid.UUID) {
			defer wg.Done()
			offers[i], errs[i] = s.CreateSequentialDeliveryOffer(ctx, id, p, nil, 1)
		}(i, id)
	}
	wg.Wait()
	n := 0
	for i, o := range offers {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if o != nil {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("offers=%d", n)
	}
}

func TestSequentialDeliveryOutboxFailureDoesNotCommitOffer(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	o, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	_, p := seedDeliveryPartner(t, s)
	_, err := s.db.Exec(ctx, `CREATE OR REPLACE FUNCTION food.reject_test_offer() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test outbox unavailable'; END $$; CREATE TRIGGER reject_test_offer BEFORE INSERT ON food.outbox_events FOR EACH ROW EXECUTE FUNCTION food.reject_test_offer()`)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Exec(ctx, `DROP TRIGGER reject_test_offer ON food.outbox_events; DROP FUNCTION food.reject_test_offer()`)
	if _, err = s.CreateSequentialDeliveryOffer(ctx, o, p, nil, 1); err == nil {
		t.Fatal("outbox failure ignored")
	}
	var n int
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM food.delivery_offers WHERE order_id=$1`, o).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("orphan offer")
	}
}

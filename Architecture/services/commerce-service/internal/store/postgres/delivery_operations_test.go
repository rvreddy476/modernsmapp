package postgres

import (
	"context"
	"errors"
	"github.com/atpost/commerce-service/database"
	"github.com/google/uuid"
	"strings"
	"sync"
	"testing"
	"time"
)

func deliveryOperationsStore(t *testing.T) *Store {
	t.Helper()
	s := deliveryCoverageTestStore(t)
	ctx := context.Background()
	_, err := s.db.Exec(ctx, `CREATE TABLE IF NOT EXISTS orders(id uuid PRIMARY KEY,customer_user_id uuid,payment_status text,payment_method text,status text,updated_at timestamptz DEFAULT now()); CREATE TABLE IF NOT EXISTS order_items(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),order_id uuid,seller_id uuid,status text,delivered_at timestamptz);CREATE TABLE IF NOT EXISTS order_status_transitions(from_status text,to_status text,actor_type text,PRIMARY KEY(from_status,to_status,actor_type));CREATE TABLE IF NOT EXISTS order_status_history(id uuid PRIMARY KEY,order_id uuid,from_status text,to_status text,changed_by uuid,actor_type text,notes text,created_at timestamptz DEFAULT now());INSERT INTO order_status_transitions VALUES('confirmed','packed','system'),('packed','shipped','system'),('shipped','delivered','system') ON CONFLICT DO NOTHING;`)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"002_invoices_shipments.sql", "042_delivery_operations.sql", "043_delivery_offer_choices.sql", "044_customer_delivery_tracking.sql"} {
		raw, e := database.Migrations.ReadFile("migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.db.Exec(ctx, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	return s
}

type deliveryFixture struct {
	s                                                *Store
	ctx                                              context.Context
	order, seller, customer, sellerUser, user, actor uuid.UUID
	area                                             *DeliveryArea
	job                                              *DeliveryJob
}

func deliveryFixtureNew(t *testing.T) *deliveryFixture {
	t.Helper()
	f := &deliveryFixture{s: deliveryOperationsStore(t), ctx: context.Background(), order: uuid.New(), seller: uuid.New(), customer: uuid.New(), sellerUser: uuid.New(), user: uuid.New(), actor: uuid.New()}
	payout := int64(6500)
	var err error
	f.area, err = f.s.SaveDeliveryArea(f.ctx, f.actor, DeliveryArea{Name: "QA isolated area", Latitude: 17.4, Longitude: 78.6, RadiusMeters: 5000, PayoutPaise: &payout, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.db.Exec(f.ctx, `INSERT INTO sellers(id,user_id,store_name) VALUES($1,$2,'QA seller');`, f.seller, f.sellerUser)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.SaveDeliveryAreaSeller(f.ctx, f.actor, DeliveryAreaSeller{AreaID: f.area.ID, SellerID: f.seller, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.db.Exec(f.ctx, `INSERT INTO orders(id,customer_user_id,payment_status,payment_method,status) VALUES($1,$2,'pending','razorpay','confirmed');`, f.order, f.customer)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.db.Exec(f.ctx, `INSERT INTO order_items(order_id,seller_id,status) VALUES($1,$2,'confirmed')`, f.order, f.seller)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []struct {
		user, id uuid.UUID
		kind     string
	}{{f.customer, f.order, "order"}, {f.sellerUser, f.seller, "seller"}} {
		if err = f.s.SaveDeliveryLocation(f.ctx, l.user, l.id, l.kind, DeliveryLocation{Latitude: 17.4, Longitude: 78.6, Label: "QA supplied coordinates"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.s.SaveDeliveryProfile(f.ctx, f.user, "QA courier", "bicycle"); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.db.Exec(f.ctx, `UPDATE commerce_delivery_profiles SET status='approved' WHERE user_id=$1`, f.user)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.SaveDeliveryAvailability(f.ctx, f.user, DeliveryAvailability{AreaID: f.area.ID, Latitude: 17.4, Longitude: 78.6, Online: true}); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *deliveryFixture) prepare(t *testing.T) {
	t.Helper()
	_, err := f.s.db.Exec(f.ctx, `UPDATE orders SET payment_status='paid' WHERE id=$1`, f.order)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := f.s.PrepareLocalDelivery(f.ctx, f.order, f.seller)
	if err != nil || sh == nil {
		t.Fatalf("prepare: %v %v", sh, err)
	}
	var id uuid.UUID
	if err = f.s.db.QueryRow(f.ctx, `SELECT id FROM commerce_delivery_jobs WHERE shipment_id=$1`, sh.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	f.job, err = f.s.DeliveryJob(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
}
func TestDeliveryOperationsDBPrepaidReadyToVerifiedCompletion(t *testing.T) {
	f := deliveryFixtureNew(t)
	if sh, e := f.s.PrepareLocalDelivery(f.ctx, f.order, f.seller); e != nil || sh != nil {
		t.Fatalf("unpaid routed: %v %v", sh, e)
	}
	f.prepare(t)
	if f.job.Status != "awaiting_ready" {
		t.Fatal(f.job.Status)
	}
	if jobs, e := f.s.UnassignedDeliveryJobs(f.ctx); e != nil {
		t.Fatal(e)
	} else {
		for _, j := range jobs {
			if j.ID == f.job.ID {
				t.Fatal("offered before seller ready")
			}
		}
	}
	if _, e := f.s.SellerDeliveryReady(f.ctx, f.customer, f.job.ID); e == nil {
		t.Fatal("customer acted as seller")
	}
	pickup, e := f.s.SellerDeliveryReady(f.ctx, f.sellerUser, f.job.ID)
	if e != nil {
		t.Fatal(e)
	}
	offer, e := f.s.CreateDeliveryOffer(f.ctx, f.job.ID, f.user, uuid.New(), time.Now().UTC().Add(19*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	j, e := f.s.DecideDeliveryOffer(f.ctx, f.user, offer.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	if j.PayoutPaise != 6500 {
		t.Fatal("payout not snapshotted")
	}
	var shipped *time.Time
	if e = f.s.db.QueryRow(f.ctx, `SELECT shipped_at FROM shipments WHERE id=$1`, j.ShipmentID).Scan(&shipped); e != nil || shipped != nil {
		t.Fatal("assignment marked shipment shipped", e)
	}
	// Changing configured prices after the offer cannot change its payout.
	updated := *f.area
	v := int64(9000)
	updated.PayoutPaise = &v
	if _, e = f.s.SaveDeliveryArea(f.ctx, f.actor, updated); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.TransitionDeliveryJob(f.ctx, f.user, j.ID, "pickup", "000000", "", j.Revision); !errors.Is(e, ErrDeliveryCode) {
		t.Fatal(e)
	}
	j, e = f.s.TransitionDeliveryJob(f.ctx, f.user, j.ID, "pickup", pickup, "", j.Revision)
	if e != nil {
		t.Fatal(e)
	}
	if e := f.s.UpdateDeliveryLocation(f.ctx, f.user, DeliveryLocation{Latitude: 17.4, Longitude: 78.6}); e != nil {
		t.Fatal(e)
	}
	jobs, e := f.s.OrderDeliveryJobs(f.ctx, f.customer, f.order)
	if e != nil || len(jobs) != 1 || jobs[0].CustomerLocation == nil {
		t.Fatal("customer cannot track picked-up shipment", e)
	}
	sellerJobs, e := f.s.OrderDeliveryJobs(f.ctx, f.sellerUser, f.order)
	if e != nil || len(sellerJobs) != 1 || sellerJobs[0].CustomerLocation != nil {
		t.Fatal("seller received private courier location", e)
	}
	otherJobs, e := f.s.OrderDeliveryJobs(f.ctx, uuid.New(), f.order)
	if e != nil || len(otherJobs) != 0 {
		t.Fatal("another customer read shipment", e)
	}
	if _, e := f.s.db.Exec(f.ctx, `UPDATE commerce_delivery_availability SET location_recorded_at=now()-INTERVAL '3 minutes' WHERE user_id=$1`, f.user); e != nil {
		t.Fatal(e)
	}
	jobs, e = f.s.OrderDeliveryJobs(f.ctx, f.customer, f.order)
	if e != nil || jobs[0].CustomerLocation != nil {
		t.Fatal("stale courier location visible", e)
	}
	code, e := f.s.DeliveryCustomerCode(f.ctx, f.customer, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.DeliveryCustomerCode(f.ctx, f.user, j.ID); e == nil {
		t.Fatal("partner read customer code")
	}
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.s.TransitionDeliveryJob(f.ctx, f.user, j.ID, "deliver", code, "", j.Revision)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	won := 0
	for e := range results {
		if e == nil {
			won++
		} else if !errors.Is(e, ErrDeliveryWorkConflict) {
			t.Fatal(e)
		}
	}
	if won != 1 {
		t.Fatalf("completion winners %d", won)
	}
	if _, e := f.s.db.Exec(f.ctx, `UPDATE commerce_delivery_availability SET location_recorded_at=now() WHERE user_id=$1`, f.user); e != nil {
		t.Fatal(e)
	}
	jobs, e = f.s.OrderDeliveryJobs(f.ctx, f.customer, f.order)
	if e != nil || jobs[0].CustomerLocation != nil {
		t.Fatal("completed shipment exposed location", e)
	}
	earnings, e := f.s.DeliveryEarnings(f.ctx, f.user)
	if e != nil || len(earnings) != 1 || earnings[0].AmountPaise != 6500 {
		t.Fatalf("earnings: %v %v", earnings, e)
	}
	var status string
	if e = f.s.db.QueryRow(f.ctx, `SELECT status FROM orders WHERE id=$1`, f.order).Scan(&status); e != nil || status != "delivered" {
		t.Fatalf("tracking %s %v", status, e)
	}
	var payload string
	if e = f.s.db.QueryRow(f.ctx, `SELECT string_agg(payload::text,'') FROM outbox_events WHERE partition_key=$1`, j.ID.String()).Scan(&payload); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(payload, pickup) || strings.Contains(payload, code) {
		t.Fatal("verification secrets in outbox")
	}
}
func TestDeliveryOperationsDBExpiryNoReplayAndSellerIsolation(t *testing.T) {
	f := deliveryFixtureNew(t)
	f.prepare(t)
	if _, e := f.s.SellerDeliveryReady(f.ctx, f.sellerUser, f.job.ID); e != nil {
		t.Fatal(e)
	}
	o, e := f.s.CreateDeliveryOffer(f.ctx, f.job.ID, f.user, uuid.New(), time.Now().Add(19*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.db.Exec(f.ctx, `UPDATE commerce_delivery_offers SET expires_at=now()-interval '1 second' WHERE id=$1`, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.DecideDeliveryOffer(f.ctx, f.user, o.ID, true); !errors.Is(e, ErrDeliveryWorkConflict) {
		t.Fatal("expired accepted", e)
	}
	if e = f.s.ExpireDeliveryOffers(f.ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.CreateDeliveryOffer(f.ctx, f.job.ID, f.user, uuid.New(), time.Now().Add(19*time.Second)); !errors.Is(e, ErrDeliveryWorkConflict) {
		t.Fatal("old offer replay", e)
	}
	if _, e = f.s.PrepareLocalDelivery(f.ctx, f.order, f.seller); !errors.Is(e, ErrDeliveryWorkConflict) {
		t.Fatal("duplicate shipment", e)
	}
}

func TestDeliveryOperationsDBThreeConcurrentChoicesOneAcceptance(t *testing.T) {
	f := deliveryFixtureNew(t)
	f.prepare(t)
	jobs := []uuid.UUID{f.job.ID}
	for i := 0; i < 3; i++ {
		order := uuid.New()
		if _, err := f.s.db.Exec(f.ctx, `INSERT INTO orders(id,customer_user_id,payment_status,payment_method,status) VALUES($1,$2,'pending','razorpay','confirmed')`, order, f.customer); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.db.Exec(f.ctx, `INSERT INTO order_items(order_id,seller_id,status) VALUES($1,$2,'confirmed')`, order, f.seller); err != nil {
			t.Fatal(err)
		}
		if err := f.s.SaveDeliveryLocation(f.ctx, f.customer, order, "order", DeliveryLocation{Latitude: 17.4, Longitude: 78.6, Label: "QA choice"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.db.Exec(f.ctx, `UPDATE orders SET payment_status='paid' WHERE id=$1`, order); err != nil {
			t.Fatal(err)
		}
		shipment, err := f.s.PrepareLocalDelivery(f.ctx, order, f.seller)
		if err != nil || shipment == nil {
			t.Fatalf("prepare: %v", err)
		}
		var job uuid.UUID
		if err = f.s.db.QueryRow(f.ctx, `SELECT id FROM commerce_delivery_jobs WHERE shipment_id=$1`, shipment.ID).Scan(&job); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, job)
	}
	for _, job := range jobs {
		if _, err := f.s.SellerDeliveryReady(f.ctx, f.sellerUser, job); err != nil {
			t.Fatal(err)
		}
	}
	// Leave no multi-offer rows for the fixture's idempotent historical migrations.
	t.Cleanup(func() {
		_, _ = f.s.db.Exec(f.ctx, `UPDATE commerce_delivery_offers SET status='expired' WHERE partner_user_id=$1 AND status='offered'`, f.user)
	})
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		go func(job uuid.UUID) {
			defer wg.Done()
			_, _ = f.s.CreateDeliveryOffer(f.ctx, job, f.user, uuid.New(), time.Now().Add(19*time.Second))
		}(job)
	}
	wg.Wait()
	offers, err := f.s.DeliveryOffers(f.ctx, f.user)
	if err != nil || len(offers) != 3 {
		t.Fatalf("choices=%d err=%v", len(offers), err)
	}
	for _, offer := range offers {
		if offer.PayoutPaise != 6500 || offer.PickupLabel == "" || offer.DropLabel == "" {
			t.Fatalf("missing comparison: %+v", offer)
		}
	}
	results := make(chan error, 2)
	for _, offer := range offers[:2] {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			_, err := f.s.DecideDeliveryOffer(f.ctx, f.user, id, true)
			results <- err
		}(offer.ID)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("concurrent accept successes=%d", accepted)
	}
}
func TestDeliveryOperationsDBCoverageMissingAndCODStayCourier(t *testing.T) {
	f := deliveryFixtureNew(t)
	_, e := f.s.db.Exec(f.ctx, `UPDATE orders SET payment_status='paid',payment_method='cod' WHERE id=$1`, f.order)
	if e != nil {
		t.Fatal(e)
	}
	if sh, e := f.s.PrepareLocalDelivery(f.ctx, f.order, f.seller); e != nil || sh != nil {
		t.Fatal("COD local", sh, e)
	}
	_, e = f.s.db.Exec(f.ctx, `UPDATE orders SET payment_method='razorpay' WHERE id=$1`, f.order)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.db.Exec(f.ctx, `UPDATE commerce_delivery_locations SET latitude=18 WHERE owner_kind='order' AND owner_id=$1`, f.order)
	if e != nil {
		t.Fatal(e)
	}
	if sh, e := f.s.PrepareLocalDelivery(f.ctx, f.order, f.seller); e != nil || sh != nil {
		t.Fatal("out of area local", sh, e)
	}
	// A courier decision is immutable even if coverage/location is changed while
	// the external booking request is still in flight.
	if _, e = f.s.db.Exec(f.ctx, `UPDATE commerce_delivery_locations SET latitude=17.4 WHERE owner_kind='order' AND owner_id=$1`, f.order); e != nil {
		t.Fatal(e)
	}
	if sh, e := f.s.PrepareLocalDelivery(f.ctx, f.order, f.seller); e != nil || sh != nil {
		t.Fatal("courier route changed to local", sh, e)
	}
	loc, e := f.s.SellerDeliveryLocation(f.ctx, f.sellerUser, f.seller)
	if e != nil || loc == nil || loc.Latitude != 17.4 {
		t.Fatal("saved pickup readback", loc, e)
	}
	if _, e = f.s.SellerDeliveryLocation(f.ctx, f.customer, f.seller); e == nil {
		t.Fatal("pickup location leaked to nonowner")
	}
}

func TestDeliveryOperationsDBSequentialRaceAndOutboxRollback(t *testing.T) {
	f := deliveryFixtureNew(t)
	f.prepare(t)
	if _, e := f.s.SellerDeliveryReady(f.ctx, f.sellerUser, f.job.ID); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	offers := make(chan *DeliveryOffer, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, e := f.s.CreateDeliveryOffer(f.ctx, f.job.ID, f.user, uuid.New(), time.Now().Add(19*time.Second))
			if e != nil {
				errs <- e
			} else {
				offers <- o
			}
		}()
	}
	wg.Wait()
	close(offers)
	close(errs)
	for e := range errs {
		if !errors.Is(e, ErrDeliveryWorkConflict) {
			t.Fatal(e)
		}
	}
	if len(offers) != 1 {
		t.Fatalf("concurrent offer winners %d", len(offers))
	}
	o := <-offers
	// Reject only this test job's assignment event, proving state/outbox atomicity.
	constraint := "qa_delivery_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, e := f.s.db.Exec(f.ctx, `ALTER TABLE outbox_events ADD CONSTRAINT `+constraint+` CHECK (NOT(event_type='commerce.delivery.assigned' AND payload->>'job_id'='`+f.job.ID.String()+`'))`)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = f.s.db.Exec(f.ctx, `ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS `+constraint) })
	if _, e = f.s.DecideDeliveryOffer(f.ctx, f.user, o.ID, true); e == nil {
		t.Fatal("assignment survived outbox rejection")
	}
	j, e := f.s.DeliveryJob(f.ctx, f.job.ID)
	if e != nil || j.Status != "unassigned" || j.PartnerUserID != nil {
		t.Fatal("rollback failed", j, e)
	}
	if _, e = f.s.db.Exec(f.ctx, `ALTER TABLE outbox_events DROP CONSTRAINT `+constraint); e != nil {
		t.Fatal(e)
	}
	accepted := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := f.s.DecideDeliveryOffer(f.ctx, f.user, o.ID, true); accepted <- e }()
	}
	wg.Wait()
	close(accepted)
	for e := range accepted {
		if e != nil && !errors.Is(e, ErrDeliveryWorkConflict) {
			t.Fatal(e)
		}
	}
	var count int
	if e = f.s.db.QueryRow(f.ctx, `SELECT count(*) FROM outbox_events WHERE event_type='commerce.delivery.assigned' AND payload->>'job_id'=$1`, f.job.ID.String()).Scan(&count); e != nil || count != 1 {
		t.Fatalf("assignment events %d %v", count, e)
	}
}
func TestDeliveryOperationsDBFailuresSupportAndCoverageHeartbeat(t *testing.T) {
	f := deliveryFixtureNew(t)
	f.prepare(t)
	pickup, e := f.s.SellerDeliveryReady(f.ctx, f.sellerUser, f.job.ID)
	if e != nil {
		t.Fatal(e)
	}
	o, e := f.s.CreateDeliveryOffer(f.ctx, f.job.ID, f.user, uuid.New(), time.Now().Add(19*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	j, e := f.s.DecideDeliveryOffer(f.ctx, f.user, o.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.UpdateDeliveryLocation(f.ctx, f.user, DeliveryLocation{Latitude: 19, Longitude: 78.6}); !errors.Is(e, ErrDeliveryConfigInvalid) {
		t.Fatal("outofzone heartbeat", e)
	}
	j, e = f.s.TransitionDeliveryJob(f.ctx, f.user, j.ID, "pickup", pickup, "", j.Revision)
	if e != nil {
		t.Fatal(e)
	}
	j, e = f.s.TransitionDeliveryJob(f.ctx, f.user, j.ID, "failed", "", "Customer unavailable after arrival", j.Revision)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := f.s.DeliveryEarnings(f.ctx, f.user)
	if e != nil || len(rows) != 0 {
		t.Fatal("failed credited", rows, e)
	}
	if e = f.s.AdminDeliveryAction(f.ctx, f.actor, j.ID, "retry", "Try another partner", j.Revision); !errors.Is(e, ErrDeliveryWorkConflict) {
		t.Fatal("pickedup parcel reassigned", e)
	}
	tickets, e := f.s.DeliverySupportTickets(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, ticket := range tickets {
		if ticket.JobID == j.ID {
			found = true
			if e = f.s.ResolveDeliverySupport(f.ctx, f.actor, ticket.ID, "Arranged return to seller"); e != nil {
				t.Fatal(e)
			}
			var count int
			if e = f.s.db.QueryRow(f.ctx, `SELECT count(*) FROM outbox_events WHERE event_type='commerce.delivery.support' AND payload->>'ticket_id'=$1 AND payload->>'partner_user_id'=$2 AND payload->>'status'='resolved'`, ticket.ID.String(), f.user.String()).Scan(&count); e != nil || count != 1 {
				t.Fatal("support resolution missing addressed event", count, e)
			}
			if e = f.s.ResolveDeliverySupport(f.ctx, f.actor, ticket.ID, "Repeated resolve"); e == nil {
				t.Fatal("resolved ticket repeated")
			}
		}
	}
	if !found {
		t.Fatal("failed delivery has no support ticket")
	}
	pending, e := f.s.DeliveryPendingReleases(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	found = false
	for _, p := range pending {
		if p.ID == j.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("terminal release not durable")
	}
}
func TestDeliveryOperationsDBCodeLockAndOtherPartnerCannotReadOrAct(t *testing.T) {
	f := deliveryFixtureNew(t)
	f.prepare(t)
	pickup, e := f.s.SellerDeliveryReady(f.ctx, f.sellerUser, f.job.ID)
	if e != nil {
		t.Fatal(e)
	}
	o, e := f.s.CreateDeliveryOffer(f.ctx, f.job.ID, f.user, uuid.New(), time.Now().Add(19*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	j, e := f.s.DecideDeliveryOffer(f.ctx, f.user, o.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.TransitionDeliveryJob(f.ctx, uuid.New(), j.ID, "pickup", pickup, "", j.Revision); e == nil {
		t.Fatal("other account pickedup")
	}
	if _, e = f.s.DeliveryOffer(f.ctx, uuid.New(), o.ID); e == nil {
		t.Fatal("otheraccount read offer")
	}
	for i := 0; i < 5; i++ {
		if _, e = f.s.TransitionDeliveryJob(f.ctx, f.user, j.ID, "pickup", "000000", "", j.Revision); !errors.Is(e, ErrDeliveryCode) {
			t.Fatal(e)
		}
	}
	if _, e = f.s.TransitionDeliveryJob(f.ctx, f.user, j.ID, "pickup", pickup, "", j.Revision); !errors.Is(e, ErrDeliveryCode) {
		t.Fatal("locked code accepted", e)
	}
}
func TestDeliveryOperationsDBMixedSellerTrackingAndCancellation(t *testing.T) {
	f := deliveryFixtureNew(t)
	other := uuid.New()
	_, e := f.s.db.Exec(f.ctx, `INSERT INTO sellers(id,user_id,store_name) VALUES($1,$2,'QA courier seller')`, other, uuid.New())
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.db.Exec(f.ctx, `INSERT INTO order_items(order_id,seller_id,status) VALUES($1,$2,'confirmed')`, f.order, other)
	if e != nil {
		t.Fatal(e)
	}
	// Existing non-local courier shipment is retained, including its identity.
	var courierShipment uuid.UUID
	e = f.s.db.QueryRow(f.ctx, `INSERT INTO shipments(order_id,seller_id,courier,status,shipped_at) VALUES($1,$2,'qa_external','delivered',now()) RETURNING id`, f.order, other).Scan(&courierShipment)
	if e != nil {
		t.Fatal(e)
	}
	f.prepare(t)
	pickup, e := f.s.SellerDeliveryReady(f.ctx, f.sellerUser, f.job.ID)
	if e != nil {
		t.Fatal(e)
	}
	o, e := f.s.CreateDeliveryOffer(f.ctx, f.job.ID, f.user, uuid.New(), time.Now().Add(19*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	j, e := f.s.DecideDeliveryOffer(f.ctx, f.user, o.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	j, e = f.s.TransitionDeliveryJob(f.ctx, f.user, j.ID, "pickup", pickup, "", j.Revision)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := f.s.db.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.cancelDeliveryOrderTx(f.ctx, tx, f.order); !errors.Is(e, ErrCancelNotPermitted) {
		t.Fatal("pickeduporder cancel admitted", e)
	}
	_ = tx.Rollback(f.ctx)
	code, e := f.s.DeliveryCustomerCode(f.ctx, f.customer, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.TransitionDeliveryJob(f.ctx, f.user, j.ID, "deliver", code, "", j.Revision)
	if e != nil {
		t.Fatal(e)
	}
	var preserved bool
	e = f.s.db.QueryRow(f.ctx, `SELECT courier='qa_external' AND status='delivered' FROM shipments WHERE id=$1`, courierShipment).Scan(&preserved)
	if e != nil || !preserved {
		t.Fatal("existing courier changed", e)
	}
	var status string
	e = f.s.db.QueryRow(f.ctx, `SELECT status FROM orders WHERE id=$1`, f.order).Scan(&status)
	if e != nil || status != "delivered" {
		t.Fatal("mixed courier tracking", status, e)
	}
	for _, typ := range []string{"commerce.order.shipped", "commerce.order.delivered"} {
		var count int
		if e = f.s.db.QueryRow(f.ctx, `SELECT count(*) FROM outbox_events WHERE event_type=$1 AND payload->>'order_id'=$2 AND payload->>'user_id'=$3`, typ, f.order.String(), f.customer.String()).Scan(&count); e != nil || count != 1 {
			t.Fatal("customer event", typ, count, e)
		}
	}
	// Cancellation before pickup atomically revokes the job and pending offer.
	g := deliveryFixtureNew(t)
	g.prepare(t)
	if _, e = g.s.SellerDeliveryReady(g.ctx, g.sellerUser, g.job.ID); e != nil {
		t.Fatal(e)
	}
	offer, e := g.s.CreateDeliveryOffer(g.ctx, g.job.ID, g.user, uuid.New(), time.Now().Add(19*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	tx, e = g.s.db.Begin(g.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = g.s.cancelDeliveryOrderTx(g.ctx, tx, g.order); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(g.ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = g.s.DecideDeliveryOffer(g.ctx, g.user, offer.ID, true); !errors.Is(e, ErrDeliveryWorkConflict) {
		t.Fatal("cancelledoffer accepted", e)
	}
	releases, e := g.s.DeliveryOfferReleases(g.ctx)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range releases {
		if r.ID == offer.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("cancelledoffer release lost")
	}
}

func TestDeliveryOperationsDBPartialShipmentDoesNotNotifyWholeOrderDelivered(t *testing.T) {
	f := deliveryFixtureNew(t)
	other := uuid.New()
	if _, e := f.s.db.Exec(f.ctx, `INSERT INTO sellers(id,user_id,store_name) VALUES($1,$2,'QA pending seller')`, other, uuid.New()); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.db.Exec(f.ctx, `INSERT INTO order_items(order_id,seller_id,status) VALUES($1,$2,'confirmed')`, f.order, other); e != nil {
		t.Fatal(e)
	}
	f.prepare(t)
	var count int
	if e := f.s.db.QueryRow(f.ctx, `SELECT count(*) FROM outbox_events WHERE event_type='commerce.order.shipped' AND payload->>'order_id'=$1`, f.order.String()).Scan(&count); e != nil || count != 0 {
		t.Fatal("shipment creation notified pickup", count, e)
	}
	pc, e := f.s.SellerDeliveryReady(f.ctx, f.sellerUser, f.job.ID)
	if e != nil {
		t.Fatal(e)
	}
	o, e := f.s.CreateDeliveryOffer(f.ctx, f.job.ID, f.user, uuid.New(), time.Now().Add(19*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	j, e := f.s.DecideDeliveryOffer(f.ctx, f.user, o.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	j, e = f.s.TransitionDeliveryJob(f.ctx, f.user, j.ID, "pickup", pc, "", j.Revision)
	if e != nil {
		t.Fatal(e)
	}
	dc, e := f.s.DeliveryCustomerCode(f.ctx, f.customer, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.TransitionDeliveryJob(f.ctx, f.user, j.ID, "deliver", dc, "", j.Revision); e != nil {
		t.Fatal(e)
	}
	if e = f.s.db.QueryRow(f.ctx, `SELECT count(*) FROM outbox_events WHERE event_type='commerce.order.delivered' AND payload->>'order_id'=$1`, f.order.String()).Scan(&count); e != nil || count != 0 {
		t.Fatal("partial delivery notified whole order", count, e)
	}
	if e = f.s.db.QueryRow(f.ctx, `SELECT count(*) FROM outbox_events WHERE event_type='commerce.order.shipped' AND payload->>'order_id'=$1`, f.order.String()).Scan(&count); e != nil || count != 1 {
		t.Fatal("actual pickup missing shipment notice", count, e)
	}
}

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/pricing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func bengaluru(t *testing.T, s *Store) City {
	t.Helper()
	cs, _ := s.ListActiveCities(context.Background())
	for _, c := range cs {
		if c.Name == "Bengaluru" {
			return c
		}
	}
	t.Skip("Bengaluru seed missing")
	return City{}
}

func TestFareRule_PaiseColumnsAndChargeDefaults(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	blr := bengaluru(t, s)
	if blr.Timezone != "Asia/Kolkata" {
		t.Fatalf("timezone %q", blr.Timezone)
	}
	auto, err := s.GetFareRule(context.Background(), blr.ID, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if auto.BasePaise != 2500 || auto.PerKMPaise != 1200 || auto.MinimumPaise != 4000 || auto.CancellationFeePaise != 1500 {
		t.Fatalf("auto paise %+v", auto)
	}
	if auto.WaitingFreeMinutes != 3 || auto.WaitingPerMinutePaise != 150 || auto.CancelFreeSeconds != 120 {
		t.Fatalf("auto charges %+v", auto)
	}
	bike, _ := s.GetFareRule(context.Background(), blr.ID, "bike")
	if bike.WaitingPerMinutePaise != 100 {
		t.Fatalf("bike waiting %d", bike.WaitingPerMinutePaise)
	}
	// Legacy floats are derived from the paise.
	if auto.BaseFare != 25 || auto.PerKMFare != 12 {
		t.Fatalf("derived floats %+v", auto)
	}
	pr := auto.PricingRule()
	if pr.BasePaise != 2500 || pr.WaitingPerMinutePaise != 150 {
		t.Fatalf("pricing rule %+v", pr)
	}
}

func TestFareRule_FloatFallbackWhenPaiseIsZero(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	city, err := s.CreateCity(ctx, "Legacy "+uuid.NewString()[:6], "TestState", "India", "INR")
	if err != nil {
		t.Fatal(err)
	}
	// A row written by the legacy float admin API before 002/003 backfilled it.
	if _, err := s.db.Exec(ctx, `INSERT INTO rider_fare_rules (city_id, vehicle_type, base_fare, per_km_fare, minimum_fare, cancellation_fee)
		VALUES ($1, 'auto', 30.5, 11, 45, 20)`, city.ID); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetFareRule(ctx, city.ID, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if r.BasePaise != 3050 || r.PerKMPaise != 1100 || r.MinimumPaise != 4500 || r.CancellationFeePaise != 2000 {
		t.Fatalf("fallback %+v", r)
	}
}

func TestFareWindows_SeededPerCity(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	blr := bengaluru(t, s)
	ws, err := s.ListFareWindows(context.Background(), blr.ID, "auto")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]FareWindow{}
	for _, w := range ws {
		names[w.Name] = w
	}
	if len(ws) != 3 || names["Morning peak"].MultiplierBPS != 12500 || names["Night"].MultiplierBPS != 12000 || names["Evening peak"].DaysOfWeek != 31 {
		t.Fatalf("auto windows %+v", ws)
	}
	if names["Night"].StartMinute != 1380 || names["Night"].EndMinute != 300 || names["Night"].VehicleType == nil {
		t.Fatalf("night window %+v", names["Night"])
	}
	sedan, _ := s.ListFareWindows(context.Background(), blr.ID, "sedan")
	if len(sedan) != 2 {
		t.Fatalf("sedan gets the all-vehicle windows only: %d", len(sedan))
	}
	pw := PricingWindows(ws)
	if w := pricing.SelectWindow(pw, "auto", time.Date(2026, 9, 18, 9, 0, 0, 0, time.FixedZone("IST", 19800))); w == nil || w.Name != "Morning peak" {
		t.Fatalf("select %+v", w)
	}
}

func TestDemandCounts(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	blr := bengaluru(t, s)
	req, online, err := s.DemandCounts(ctx, blr.ID, "auto")
	if err != nil || req != 0 || online != 0 {
		t.Fatalf("empty: %d %d %v", req, online, err)
	}
	pid, _ := makePartnerWithRide(t, s) // one requested auto ride in blr (cs[0]) — may not be blr
	v, err := s.CreateVehicle(ctx, CreateVehicleInput{PartnerID: pid, VehicleType: "auto", RegistrationNumber: "KA" + uuid.NewString()[:6]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE rider_vehicles SET status = 'approved' WHERE id = $1`, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE rider_partners SET city_id = $2 WHERE id = $1`, pid, blr.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPartnerOnlineFlag(ctx, pid, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.CreateRide(ctx, CreateRideInput{CustomerUserID: uuid.New(), CityID: &blr.ID, VehicleType: "auto",
			PickupAddress: "P", PickupLat: 12.97, PickupLng: 77.59, DropAddress: "D", DropLat: 12.93, DropLng: 77.62}); err != nil {
			t.Fatal(err)
		}
	}
	req, online, err = s.DemandCounts(ctx, blr.ID, "auto")
	if err != nil || req < 3 || online != 1 {
		t.Fatalf("seeded: requested %d online %d %v", req, online, err)
	}
	if _, o, _ := s.DemandCounts(ctx, blr.ID, "bike"); o != 0 {
		t.Fatalf("an auto partner is not bike supply: %d", o)
	}
}

func TestCoupon_ReserveEnforcesLimitsUnderLock(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	blr := bengaluru(t, s)
	c, err := s.CreateCoupon(ctx, CreateCouponInput{Code: "lock" + uuid.NewString()[:6], DiscountType: "flat", DiscountValuePaise: 500, PerUserLimit: 1, TotalLimit: 2, CreatedBy: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCouponByCode(ctx, "  "+c.Code+" "); err != nil {
		t.Fatalf("case/space-insensitive lookup: %v", err)
	}
	cust := uuid.New()
	newRide := func(u uuid.UUID) uuid.UUID {
		r, err := s.CreateRide(ctx, CreateRideInput{CustomerUserID: u, CityID: &blr.ID, VehicleType: "auto",
			PickupAddress: "P", PickupLat: 12.97, PickupLng: 77.59, DropAddress: "D", DropLat: 12.93, DropLng: 77.62})
		if err != nil {
			t.Fatal(err)
		}
		// Free the customer for the next ride (one active ride per customer).
		_, _ = s.db.Exec(ctx, `UPDATE rider_rides SET status = 'expired' WHERE id = $1`, r.ID)
		return r.ID
	}
	reserve := func(u, ride uuid.UUID) error {
		tx, err := s.db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err := ReserveCouponTx(ctx, tx, CouponReservation{CouponID: c.ID, DiscountPaise: 500}, u, ride, time.Now()); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err := reserve(cust, newRide(cust)); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := reserve(cust, newRide(cust)); !errors.Is(err, ErrCouponUserExhausted) {
		t.Fatalf("second use by the same customer: %v", err)
	}
	other := uuid.New()
	if err := reserve(other, newRide(other)); err != nil {
		t.Fatalf("other customer: %v", err)
	}
	third := uuid.New()
	if err := reserve(third, newRide(third)); !errors.Is(err, ErrCouponTotalExhausted) {
		t.Fatalf("past the total limit: %v", err)
	}
	if n, _ := s.CountCouponUses(ctx, c.ID, cust); n != 1 {
		t.Fatalf("uses %d", n)
	}
	cc, _ := s.GetCoupon(ctx, c.ID)
	if cc.UsedCount != 2 {
		t.Fatalf("used_count %d", cc.UsedCount)
	}
	if _, err := s.CreateCoupon(ctx, CreateCouponInput{Code: c.Code, DiscountType: "flat", DiscountValuePaise: 1, CreatedBy: uuid.New()}); !errors.Is(err, ErrCouponCodeTaken) {
		t.Fatalf("duplicate code: %v", err)
	}
}

func TestOutstanding_ReserveSettleReleaseWaive(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	blr := bengaluru(t, s)
	cust := uuid.New()
	mk := func() uuid.UUID {
		r, err := s.CreateRide(ctx, CreateRideInput{CustomerUserID: cust, CityID: &blr.ID, VehicleType: "auto",
			PickupAddress: "P", PickupLat: 12.97, PickupLng: 77.59, DropAddress: "D", DropLat: 12.93, DropLng: 77.62})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = s.db.Exec(ctx, `UPDATE rider_rides SET status = 'expired' WHERE id = $1`, r.ID)
		return r.ID
	}
	inTx := func(fn func(tx pgx.Tx) error) error {
		tx, err := s.db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	cancelled := mk()
	if err := inTx(func(tx pgx.Tx) error { return CreateOutstandingTx(ctx, tx, cust, cancelled, 1500, "cancellation_fee") }); err != nil {
		t.Fatal(err)
	}
	// Zero fee never creates a row.
	if err := inTx(func(tx pgx.Tx) error { return CreateOutstandingTx(ctx, tx, cust, mk(), 0, "x") }); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.ListPendingOutstanding(ctx, cust)
	if len(pending) != 1 {
		t.Fatalf("pending %+v", pending)
	}
	next := mk()
	if err := inTx(func(tx pgx.Tx) error { return ReserveOutstandingTx(ctx, tx, []uuid.UUID{pending[0].ID}, cust, next) }); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.ListPendingOutstanding(ctx, cust); len(p) != 0 {
		t.Fatalf("reserved row still listed as chargeable: %+v", p)
	}
	// Reserving again (another ride) fails: it is no longer free.
	if err := inTx(func(tx pgx.Tx) error { return ReserveOutstandingTx(ctx, tx, []uuid.UUID{pending[0].ID}, cust, mk()) }); !errors.Is(err, ErrOutstandingChanged) {
		t.Fatalf("double reserve: %v", err)
	}
	if err := inTx(func(tx pgx.Tx) error { return ReleaseOutstandingByRideTx(ctx, tx, next) }); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.ListPendingOutstanding(ctx, cust); len(p) != 1 {
		t.Fatalf("released row must be chargeable again: %+v", p)
	}
	if err := inTx(func(tx pgx.Tx) error { return ReserveOutstandingTx(ctx, tx, []uuid.UUID{pending[0].ID}, cust, next) }); err != nil {
		t.Fatal(err)
	}
	if err := inTx(func(tx pgx.Tx) error { return SettleOutstandingByRideTx(ctx, tx, next) }); err != nil {
		t.Fatal(err)
	}
	o, _ := s.GetOutstanding(ctx, pending[0].ID)
	if o.Status != OutstandingSettled || o.SettledAt == nil {
		t.Fatalf("settled %+v", o)
	}
	if _, err := s.WaiveOutstanding(ctx, o.ID, uuid.New(), "late"); !errors.Is(err, ErrOutstandingNotFound) {
		t.Fatalf("waiving a settled row: %v", err)
	}
	all, _ := s.ListOutstanding(ctx, &cust, "", 10, 0)
	if len(all) != 1 {
		t.Fatalf("admin list %+v", all)
	}
}

func TestTrackPoints_AppendOnlyForActiveRideAndThrottled(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	pid, rid := makePartnerWithRide(t, s)
	if ok, err := s.AppendTrackPointForActiveRide(ctx, pid, 12.97, 77.59, nil); err != nil || ok {
		t.Fatalf("no active ride: %v %v", ok, err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE rider_rides SET partner_id = $2, status = 'in_progress' WHERE id = $1`, rid, pid); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.AppendTrackPointForActiveRide(ctx, pid, 12.97, 77.59, nil); err != nil || !ok {
		t.Fatalf("first fix: %v %v", ok, err)
	}
	if ok, _ := s.AppendTrackPointForActiveRide(ctx, pid, 12.98, 77.60, nil); ok {
		t.Fatal("a fix inside 5 s must be throttled")
	}
	// Age the first fix by two minutes: past the 5 s throttle, and slow enough
	// (~1.5 km in 120 s) that the hop is not discarded as a teleport.
	if _, err := s.db.Exec(ctx, `UPDATE rider_ride_track_points SET recorded_at = recorded_at - INTERVAL '120 seconds' WHERE ride_id = $1`, rid); err != nil {
		t.Fatal(err)
	}
	speed := 8.5
	if ok, _ := s.AppendTrackPointForActiveRide(ctx, pid, 12.98, 77.60, &speed); !ok {
		t.Fatal("a fix after 5 s must be recorded")
	}
	pts, err := s.ListTrackPoints(ctx, rid)
	if err != nil || len(pts) != 2 || pts[1].Lat != 12.98 {
		t.Fatalf("points %+v %v", pts, err)
	}
	if d := pricing.TrackedDistanceMeters(pts); d < 1000 || d > 2000 {
		t.Fatalf("tracked %d m", d)
	}
}

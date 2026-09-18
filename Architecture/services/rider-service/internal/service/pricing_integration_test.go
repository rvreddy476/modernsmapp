package service

// Pricing engine integration tests (TEST_PG_DSN on rider_it_test): fare
// windows, demand surge, coupons, waiting and cancellation charges, the
// upfront fare with server-tracked distance, and the outstanding-fee loop.
// Every test pins the service clock so the seeded windows are deterministic.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/pricing"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

var (
	ist = time.FixedZone("IST", 19800)
	// Friday 18 Sep 2026.
	fridayNoonIST  = time.Date(2026, 9, 18, 12, 0, 0, 0, ist)
	fridayPeakIST  = time.Date(2026, 9, 18, 9, 0, 0, 0, ist)
	fridayNightIST = time.Date(2026, 9, 18, 23, 30, 0, 0, ist)
)

var quoteRoute = FareEstimateRequest{
	PickupLabel: "P", PickupLat: 12.9716, PickupLng: 77.5946,
	DropLabel: "D", DropLat: 12.9352, DropLng: 77.6245,
}

func pin(svc *Service, at time.Time) { svc.SetClock(func() time.Time { return at }) }

func estimateAt(t *testing.T, svc *Service, at time.Time, cust *uuid.UUID, vt, coupon string) (*FareEstimateResult, error) {
	t.Helper()
	pin(svc, at)
	blr := pickBangaloreCity(t, svc)
	req := quoteRoute
	req.CityID, req.CustomerUserID, req.VehicleType, req.CouponCode = blr.ID, cust, vt, coupon
	return svc.EstimateFare(context.Background(), req)
}

func mustEstimate(t *testing.T, svc *Service, at time.Time, cust *uuid.UUID, vt, coupon string) *FareEstimateResult {
	t.Helper()
	out, err := estimateAt(t, svc, at, cust, vt, coupon)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	return out
}

func bookQuote(t *testing.T, svc *Service, cust uuid.UUID, quoteID string, vt, method string) (*store.Ride, error) {
	t.Helper()
	qid := uuid.MustParse(quoteID)
	blr := pickBangaloreCity(t, svc)
	return svc.CreateRide(context.Background(), cust, CreateRideRequest{
		QuoteID: &qid, VehicleType: vt, CityID: &blr.ID, PaymentMethod: method,
		PickupAddress: "P", PickupLat: quoteRoute.PickupLat, PickupLng: quoteRoute.PickupLng,
		DropAddress: "D", DropLat: quoteRoute.DropLat, DropLng: quoteRoute.DropLng,
		IdempotencyKey: "pricing-" + uuid.NewString(),
	})
}

func couponErr(t *testing.T, err error, want string) {
	t.Helper()
	ce, ok := AsCouponError(err)
	if !ok {
		t.Fatalf("want CouponError %s, got %v", want, err)
	}
	if ce.Code != want {
		t.Fatalf("coupon code = %s, want %s (%s)", ce.Code, want, ce.Message)
	}
}

func decodeBreakdown(t *testing.T, raw []byte) pricing.Breakdown {
	t.Helper()
	var b pricing.Breakdown
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("decode breakdown: %v", err)
	}
	return b
}

// --- windows and surge -------------------------------------------------------

func TestEstimateFare_PeakWindowOnlyInsideItsHours(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	peak := mustEstimate(t, svc, fridayPeakIST, nil, "auto", "")
	if peak.SurgeBPS != 2500 || peak.SurgeReason != pricing.SurgePeakHours || peak.WindowName != "Morning peak" {
		t.Fatalf("09:00 Friday: surge %d %s %q", peak.SurgeBPS, peak.SurgeReason, peak.WindowName)
	}
	noon := mustEstimate(t, svc, fridayNoonIST, nil, "auto", "")
	if noon.SurgeBPS != 0 || noon.SurgeReason != pricing.SurgeNone || noon.WindowName != "" {
		t.Fatalf("noon Friday: surge %d %s %q", noon.SurgeBPS, noon.SurgeReason, noon.WindowName)
	}
	night := mustEstimate(t, svc, fridayNightIST, nil, "auto", "")
	if night.SurgeBPS != 2000 || night.WindowName != "Night" {
		t.Fatalf("23:30: surge %d %q", night.SurgeBPS, night.WindowName)
	}
	// The peak fare is the noon fare with the surge on the ride fare only.
	pb, nb := peak.Options[0].Breakdown, noon.Options[0].Breakdown
	if pb.SurgePaise != (nb.BasePaise+nb.DistancePaise+nb.TimePaise)*2500/10000 || pb.PlatformFeePaise != nb.PlatformFeePaise {
		t.Fatalf("peak breakdown %+v vs noon %+v", pb, nb)
	}
	if pb.TaxNote != pricing.TaxNote || len(pb.TaxLines) == 0 || pb.FarePolicyVersion != pricing.FarePolicyVersion {
		t.Fatalf("breakdown labels %+v", pb)
	}
	if pb.TotalPaise != pb.TaxableRideFarePaise+pb.PlatformFeePaise+pb.TaxPaise {
		t.Fatalf("total does not add up: %+v", pb)
	}
}

func TestEstimateFare_DemandSurgeCappedAndNeverStacked(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	blr := pickBangaloreCity(t, svc)
	// Ten open requests, zero online partners: ratio 10 -> the cap.
	for i := 0; i < 10; i++ {
		if _, err := svc.Store().CreateRide(ctx, store.CreateRideInput{
			CustomerUserID: uuid.New(), CityID: &blr.ID, VehicleType: "auto",
			PickupAddress: "P", PickupLat: 12.97, PickupLng: 77.59, DropAddress: "D", DropLat: 12.93, DropLng: 77.62,
		}); err != nil {
			t.Fatalf("seed ride: %v", err)
		}
	}
	svc.cfg.SurgeCapBPS = 3000
	noon := mustEstimate(t, svc, fridayNoonIST, nil, "auto", "")
	if noon.SurgeBPS != 3000 || noon.SurgeReason != pricing.SurgeHighDemand {
		t.Fatalf("demand at noon: %d %s, want the 3000 cap / high_demand", noon.SurgeBPS, noon.SurgeReason)
	}
	// In the peak window the larger of window (2500) and demand (3000) wins.
	peak := mustEstimate(t, svc, fridayPeakIST, nil, "auto", "")
	if peak.SurgeBPS != 3000 || peak.SurgeReason != pricing.SurgeHighDemand {
		t.Fatalf("peak + demand: %d %s, want max 3000 not the sum", peak.SurgeBPS, peak.SurgeReason)
	}
	svc.cfg.SurgeCapBPS = 2000
	peak = mustEstimate(t, svc, fridayPeakIST, nil, "auto", "")
	if peak.SurgeBPS != 2500 || peak.SurgeReason != pricing.SurgePeakHours {
		t.Fatalf("window above capped demand: %d %s", peak.SurgeBPS, peak.SurgeReason)
	}
}

// --- coupons -----------------------------------------------------------------

func makeCoupon(t *testing.T, svc *Service, req CreateCouponRequest) *store.Coupon {
	t.Helper()
	if req.Code == "" {
		req.Code = "T" + uuid.NewString()[:8]
	}
	c, err := svc.CreateCoupon(context.Background(), uuid.New(), req)
	if err != nil {
		t.Fatalf("create coupon: %v", err)
	}
	return c
}

func TestCoupon_ValidationCodes(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	cust := uuid.New()
	blr := pickBangaloreCity(t, svc)
	pin(svc, fridayNoonIST)

	_, err := estimateAt(t, svc, fridayNoonIST, &cust, "auto", "NOPE")
	couponErr(t, err, CouponCodeInvalid)

	ended := fridayNoonIST.Add(-time.Hour)
	expired := makeCoupon(t, svc, CreateCouponRequest{DiscountType: "flat", DiscountValuePaise: 1000, EndsAt: &ended})
	_, err = estimateAt(t, svc, fridayNoonIST, &cust, "auto", expired.Code)
	couponErr(t, err, CouponCodeExpired)

	big := makeCoupon(t, svc, CreateCouponRequest{DiscountType: "flat", DiscountValuePaise: 1000, MinFarePaise: 100000})
	_, err = estimateAt(t, svc, fridayNoonIST, &cust, "auto", big.Code)
	couponErr(t, err, CouponCodeMinFare)

	other, err := svc.Store().CreateCity(ctx, "Elsewhere "+uuid.NewString()[:6], "Goa", "India", "INR")
	if err != nil {
		t.Fatal(err)
	}
	wrongCity := makeCoupon(t, svc, CreateCouponRequest{DiscountType: "flat", DiscountValuePaise: 1000, CityID: &other.ID})
	_, err = estimateAt(t, svc, fridayNoonIST, &cust, "auto", wrongCity.Code)
	couponErr(t, err, CouponCodeWrongCity)

	bikeOnly := makeCoupon(t, svc, CreateCouponRequest{DiscountType: "flat", DiscountValuePaise: 1000, VehicleTypes: []string{"bike"}})
	_, err = estimateAt(t, svc, fridayNoonIST, &cust, "auto", bikeOnly.Code)
	couponErr(t, err, CouponCodeWrongVehicle)
	// Quoting every type: the bike option carries the discount, the auto one does not.
	both := mustEstimate(t, svc, fridayNoonIST, &cust, "", bikeOnly.Code)
	for _, o := range both.Options {
		if (o.VehicleType == "bike") != (o.DiscountPaise > 0) {
			t.Fatalf("option %s discount %d", o.VehicleType, o.DiscountPaise)
		}
	}

	exhausted := makeCoupon(t, svc, CreateCouponRequest{DiscountType: "flat", DiscountValuePaise: 1000, TotalLimit: 1})
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_coupons SET used_count = 1 WHERE id = $1`, exhausted.ID); err != nil {
		t.Fatal(err)
	}
	_, err = estimateAt(t, svc, fridayNoonIST, &cust, "auto", exhausted.Code)
	couponErr(t, err, CouponCodeExhausted)

	first := makeCoupon(t, svc, CreateCouponRequest{DiscountType: "flat", DiscountValuePaise: 1000, FirstRideOnly: true})
	if _, err := svc.Store().DB().Exec(ctx, `INSERT INTO rider_rides (customer_user_id, city_id, vehicle_type, status, pickup_address, pickup_location, drop_address, drop_location)
		VALUES ($1, $2, 'auto', 'completed', 'P', ST_SetSRID(ST_MakePoint(77.59, 12.97), 4326)::geography, 'D', ST_SetSRID(ST_MakePoint(77.62, 12.93), 4326)::geography)`, cust, blr.ID); err != nil {
		t.Fatal(err)
	}
	_, err = estimateAt(t, svc, fridayNoonIST, &cust, "auto", first.Code)
	couponErr(t, err, CouponCodeFirstRideOnly)

	svc.cfg.CouponsEnabled, svc.cfg.CouponsFlagSet = false, true
	_, err = estimateAt(t, svc, fridayNoonIST, &cust, "auto", first.Code)
	couponErr(t, err, CouponCodeDisabled)
	if _, err := svc.CheckCoupon(ctx, first.Code, nil, nil); err == nil {
		t.Fatal("validate endpoint must refuse when coupons are off")
	}
}

func TestCoupon_ReservedOnBookingAppliedOnCompletionReleasedOnCancel(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	cust := uuid.New()
	c := makeCoupon(t, svc, CreateCouponRequest{DiscountType: "percent", PercentBPS: 2000, MaxDiscountPaise: 5000, PerUserLimit: 1})

	q := mustEstimate(t, svc, fridayNoonIST, &cust, "auto", c.Code)
	opt := q.Options[0]
	if opt.DiscountPaise != opt.Breakdown.RideFarePaise*2000/10000 || opt.CouponCode != c.Code || opt.Breakdown.CouponID != c.ID.String() {
		t.Fatalf("coupon not priced into the option: %+v", opt)
	}
	plain := mustEstimate(t, svc, fridayNoonIST, nil, "auto", "").Options[0].Breakdown
	if opt.Breakdown.TaxableRideFarePaise != opt.Breakdown.RideFarePaise-opt.DiscountPaise || opt.Breakdown.PlatformFeePaise != plain.PlatformFeePaise ||
		opt.Breakdown.RideFarePaise != plain.RideFarePaise {
		t.Fatalf("discount must reduce the taxable ride fare only: %+v vs plain %+v", opt.Breakdown, plain)
	}

	ride, err := bookQuote(t, svc, cust, q.QuoteID, "auto", "cash")
	if err != nil {
		t.Fatalf("book: %v", err)
	}
	red, err := svc.Store().GetRedemptionByRide(ctx, ride.ID)
	if err != nil || red.Status != store.RedemptionReserved || red.DiscountPaise != opt.DiscountPaise {
		t.Fatalf("redemption %+v %v", red, err)
	}
	if cc, _ := svc.Store().GetCoupon(ctx, c.ID); cc.UsedCount != 1 {
		t.Fatalf("used_count %d, want 1", cc.UsedCount)
	}
	// Per-user limit: the same customer cannot quote it again while reserved.
	_, err = estimateAt(t, svc, fridayNoonIST, &cust, "auto", c.Code)
	couponErr(t, err, CouponCodeUserLimit)
	// Another customer's quote passes validation but the total limit races at booking.
	other := uuid.New()
	q2 := mustEstimate(t, svc, fridayNoonIST, &other, "auto", c.Code)
	one := 1
	if _, err := svc.UpdateCoupon(ctx, uuid.New(), c.ID, UpdateCouponRequest{TotalLimit: &one}); err != nil {
		t.Fatal(err)
	}
	_, err = bookQuote(t, svc, other, q2.QuoteID, "auto", "cash")
	couponErr(t, err, CouponCodeExhausted)

	// Customer cancels before assignment: no fee, coupon released.
	if _, err := svc.CancelRide(ctx, cust, ride.ID, "customer", CancelRideRequest{Reason: "changed my mind", ExpectedRevision: ride.Revision}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	red, _ = svc.Store().GetRedemptionByRide(ctx, ride.ID)
	if red.Status != store.RedemptionReleased {
		t.Fatalf("redemption after cancel: %s", red.Status)
	}
	if cc, _ := svc.Store().GetCoupon(ctx, c.ID); cc.UsedCount != 0 {
		t.Fatalf("used_count after release %d, want 0", cc.UsedCount)
	}
	cancelled, _ := svc.Store().GetRide(ctx, ride.ID)
	if cancelled.Status != "cancelled_by_customer" {
		t.Fatalf("status %s", cancelled.Status)
	}
	if out, _ := svc.Store().ListPendingOutstanding(ctx, cust); len(out) != 0 {
		t.Fatalf("no partner yet: no fee, got %+v", out)
	}
}

func TestCreateRide_RejectsWalletAndUnknownPaymentMethods(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	cust := uuid.New()
	q := mustEstimate(t, svc, fridayNoonIST, &cust, "auto", "")
	for _, m := range []string{"wallet", "cheque"} {
		if _, err := bookQuote(t, svc, cust, q.QuoteID, "auto", m); err == nil || !contains(err.Error(), "payment_method must be one of cash, upi, card") {
			t.Fatalf("%s accepted: %v", m, err)
		}
	}
	for _, m := range []string{"upi", "card", "cash"} {
		ride, err := bookQuote(t, svc, cust, q.QuoteID, "auto", m)
		if err != nil {
			t.Fatalf("%s refused: %v", m, err)
		}
		if _, err := svc.CancelRide(context.Background(), cust, ride.ID, "customer", CancelRideRequest{Reason: "x", ExpectedRevision: ride.Revision}); err != nil {
			t.Fatal(err)
		}
	}
}

// --- completion: upfront fare, waiting, tracked distance ---------------------

// assignedRide books a quote for cust and drives the row into status with
// the partner attached and the timestamps set relative to the pinned clock.
func assignedRide(t *testing.T, svc *Service, cust uuid.UUID, quoteID, status string, assignedAgo, arrivedAgo, startedAgo time.Duration) (*store.Ride, *store.Partner) {
	t.Helper()
	ctx := context.Background()
	partner, seedRide := makeApprovedPartnerWithVehicle(t, svc)
	// The helper's own ride is not needed: expire it so the partner is free.
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_rides SET status = 'expired' WHERE id = $1`, seedRide); err != nil {
		t.Fatal(err)
	}
	ride, err := bookQuote(t, svc, cust, quoteID, "auto", "cash")
	if err != nil {
		t.Fatalf("book: %v", err)
	}
	now := svc.now()
	var arrived, started *time.Time
	if arrivedAgo > 0 {
		a := now.Add(-arrivedAgo)
		arrived = &a
	}
	if startedAgo > 0 {
		s := now.Add(-startedAgo)
		started = &s
	}
	if _, err := svc.Store().DB().Exec(ctx, `
		UPDATE rider_rides SET status = $2::rider_ride_status, partner_id = $3, assigned_at = $4, arrived_at = $5, started_at = $6
		WHERE id = $1`, ride.ID, status, partner.ID, now.Add(-assignedAgo), arrived, started); err != nil {
		t.Fatalf("drive ride: %v", err)
	}
	r, err := svc.Store().GetRide(ctx, ride.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r, partner
}

func TestCompleteRide_UpfrontFareWaitingChargeAndReportedDistanceIgnored(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	cust := uuid.New()
	q := mustEstimate(t, svc, fridayPeakIST, &cust, "auto", "")
	quoted := q.Options[0].Breakdown
	// Arrived 10 min ago, started 4 min ago: waited 6 min, 3 free -> 3 x Rs 1.50.
	ride, partner := assignedRide(t, svc, cust, q.QuoteID, "in_progress", 30*time.Minute, 10*time.Minute, 4*time.Minute)

	// A wildly inflated captain report must not move the fare.
	pay, err := svc.CompleteRide(ctx, partner.UserID, ride.ID, CompleteRideRequest{
		FinalDistanceKM: 250, FinalDurationMin: 600, IdempotencyKey: "complete-" + ride.ID.String(), ExpectedRevision: ride.Revision,
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	done, _ := svc.Store().GetRide(ctx, ride.ID)
	final := decodeBreakdown(t, done.FareBreakdown)
	if final.WaitingMinutes != 3 || final.WaitingChargePaise != 450 {
		t.Fatalf("waiting %+v", final)
	}
	if final.DistanceMeters != quoted.DistanceMeters || final.RecomputedFromTrack || final.RideFarePaise != quoted.RideFarePaise {
		t.Fatalf("captain report priced the ride: final %+v quoted %+v", final, quoted)
	}
	if final.SurgeBasisPoints != 2500 || final.WindowName != "Morning peak" {
		t.Fatalf("locked window lost: %+v", final)
	}
	waitTax := (450*500 + 5000) / 10000
	want := quoted.TotalPaise + 450 + int64(waitTax)
	if final.TotalPaise != want || pay.AmountPaise != want || *done.FinalFarePaise != want {
		t.Fatalf("final %d pay %d ride %d, want %d", final.TotalPaise, pay.AmountPaise, *done.FinalFarePaise, want)
	}
	if pay.Status != "pending_cash_confirmation" || pay.PaymentMethod != "cash" {
		t.Fatalf("payment %+v", pay)
	}
	receipt, err := svc.GetRideReceipt(ctx, cust, ride.ID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TotalPaise != want || receipt.WaitingChargePaise != 450 || receipt.DistanceMeters != quoted.DistanceMeters ||
		receipt.ReportedDistanceKM == nil || *receipt.ReportedDistanceKM != 250 || receipt.TaxNote != pricing.TaxNote {
		t.Fatalf("receipt %+v", receipt)
	}
	if len(receipt.FareBreakdown) == 0 {
		t.Fatal("receipt must carry the breakdown")
	}
}

func trackPoints(t *testing.T, svc *Service, ride *store.Ride, partnerID uuid.UUID, pts [][2]float64) {
	t.Helper()
	// Five minutes between fixes: a multi-km hop stays under the 50 m/s
	// teleport filter.
	base := svc.now().Add(-40 * time.Minute)
	for i, p := range pts {
		if _, err := svc.Store().DB().Exec(context.Background(),
			`INSERT INTO rider_ride_track_points (ride_id, partner_id, lat, lng, recorded_at) VALUES ($1, $2, $3, $4, $5)`,
			ride.ID, partnerID, p[0], p[1], base.Add(time.Duration(i)*5*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompleteRide_RecomputesDistanceOnlyOnTrackedDeviation(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()

	// Case 1: a small detour keeps the quote. East then south is ~7.3 km
	// against a ~6.5 km quote: over it, but by under 20% and under 1 km.
	cust := uuid.New()
	q := mustEstimate(t, svc, fridayNoonIST, &cust, "auto", "")
	ride, partner := assignedRide(t, svc, cust, q.QuoteID, "in_progress", 30*time.Minute, 8*time.Minute, 5*time.Minute)
	trackPoints(t, svc, ride, partner.ID, [][2]float64{{12.9716, 77.5946}, {12.9716, 77.6100}, {12.9716, 77.6245}, {12.9530, 77.6245}, {12.9352, 77.6245}})
	if _, err := svc.CompleteRide(ctx, partner.UserID, ride.ID, CompleteRideRequest{FinalDistanceKM: 5, FinalDurationMin: 15, IdempotencyKey: "c1-" + ride.ID.String(), ExpectedRevision: ride.Revision}); err != nil {
		t.Fatal(err)
	}
	done, _ := svc.Store().GetRide(ctx, ride.ID)
	final := decodeBreakdown(t, done.FareBreakdown)
	rc1, _ := svc.GetRideReceipt(ctx, cust, ride.ID)
	if final.RecomputedFromTrack || final.DistanceMeters != q.Options[0].DistanceMeters {
		t.Fatalf("small deviation recomputed: %+v (quote %d m, tracked %v)", final, q.Options[0].DistanceMeters, rc1.TrackedDistanceM)
	}
	if rc1.TrackedDistanceM == nil || *rc1.TrackedDistanceM <= 0 {
		t.Fatalf("tracked distance not recorded as telemetry: %+v", rc1)
	}

	// Case 2: the tracked route is ~3x the quote -> distance re-priced from
	// the track at the locked surge, ride fare capped at 2x the quoted one.
	cust2 := uuid.New()
	q2 := mustEstimate(t, svc, fridayPeakIST, &cust2, "auto", "")
	ride2, partner2 := assignedRide(t, svc, cust2, q2.QuoteID, "in_progress", 40*time.Minute, 20*time.Minute, 15*time.Minute)
	trackPoints(t, svc, ride2, partner2.ID, [][2]float64{{12.9716, 77.5946}, {13.02, 77.5946}, {13.02, 77.68}, {12.9352, 77.6245}})
	if _, err := svc.CompleteRide(ctx, partner2.UserID, ride2.ID, CompleteRideRequest{FinalDistanceKM: 1, FinalDurationMin: 1, IdempotencyKey: "c2-" + ride2.ID.String(), ExpectedRevision: ride2.Revision}); err != nil {
		t.Fatal(err)
	}
	done2, _ := svc.Store().GetRide(ctx, ride2.ID)
	final2 := decodeBreakdown(t, done2.FareBreakdown)
	rc2, _ := svc.GetRideReceipt(ctx, cust2, ride2.ID)
	quoted2 := q2.Options[0].Breakdown
	if !final2.RecomputedFromTrack || rc2.TrackedDistanceM == nil || final2.DistanceMeters != *rc2.TrackedDistanceM {
		t.Fatalf("large deviation not re-priced from the track: %+v tracked %v", final2, rc2.TrackedDistanceM)
	}
	if !pricing.Deviates(quoted2.DistanceMeters, final2.DistanceMeters) {
		t.Fatalf("test route does not deviate enough: quote %d tracked %d", quoted2.DistanceMeters, final2.DistanceMeters)
	}
	if final2.SurgeBasisPoints != 2500 || final2.RideFarePaise <= quoted2.RideFarePaise || final2.RideFarePaise > 2*quoted2.RideFarePaise {
		t.Fatalf("re-priced ride fare %d (quoted %d, surge %d)", final2.RideFarePaise, quoted2.RideFarePaise, final2.SurgeBasisPoints)
	}
	if !final2.CapApplied || final2.RideFarePaise != 2*quoted2.RideFarePaise {
		t.Fatalf("a 3x route must hit the 2x cap: %+v", final2)
	}
}

// --- cancellation and outstanding --------------------------------------------

func TestCancelRide_FeeOnlyAfterFreeWindowAndChargedOnNextRide(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	cust := uuid.New()

	// Inside the free window (assigned 60 s ago): no fee.
	q := mustEstimate(t, svc, fridayNoonIST, &cust, "auto", "")
	early, _ := assignedRide(t, svc, cust, q.QuoteID, "partner_assigned", 60*time.Second, 0, 0)
	got, err := svc.CancelRide(ctx, cust, early.ID, "customer", CancelRideRequest{Reason: "quick", ExpectedRevision: early.Revision})
	if err != nil {
		t.Fatalf("cancel early: %v", err)
	}
	if got.Status != "cancelled_by_customer" {
		t.Fatalf("status %s", got.Status)
	}
	if o, _ := svc.Store().ListPendingOutstanding(ctx, cust); len(o) != 0 {
		t.Fatalf("fee inside the free window: %+v", o)
	}

	// Past the free window (assigned 3 min ago): the rule's fee (auto Rs 15).
	q = mustEstimate(t, svc, fridayNoonIST, &cust, "auto", "")
	late, _ := assignedRide(t, svc, cust, q.QuoteID, "partner_arriving", 3*time.Minute, 0, 0)
	if _, err := svc.CancelRide(ctx, cust, late.ID, "customer", CancelRideRequest{Reason: "late", ExpectedRevision: late.Revision}); err != nil {
		t.Fatalf("cancel late: %v", err)
	}
	cancelled, _ := svc.Store().GetRide(ctx, late.ID)
	receipt, _ := svc.GetRideReceipt(ctx, cust, late.ID)
	if receipt.CancellationFeePaise != 1500 || receipt.TaxPaise != 75 {
		t.Fatalf("cancelled receipt %+v", receipt)
	}
	_ = cancelled
	pending, _ := svc.Store().ListPendingOutstanding(ctx, cust)
	if len(pending) != 1 || pending[0].AmountPaise != 1500 || pending[0].Status != store.OutstandingPending {
		t.Fatalf("outstanding %+v", pending)
	}

	// The next quote carries it as a taxed line; booking reserves it;
	// completion settles it.
	q = mustEstimate(t, svc, fridayNoonIST, &cust, "auto", "")
	if q.OutstandingPaise != 1500 || q.Options[0].Breakdown.OutstandingPaise != 1500 || len(q.Options[0].Breakdown.OutstandingIDs) != 1 {
		t.Fatalf("outstanding not on the quote: %+v", q)
	}
	found := false
	for _, l := range q.Options[0].Breakdown.TaxLines {
		if l.Ref == pricing.RefOutstanding && l.TaxablePaise == 1500 && l.TaxPaise == 75 {
			found = true
		}
	}
	if !found {
		t.Fatalf("outstanding tax line missing: %+v", q.Options[0].Breakdown.TaxLines)
	}
	next, partner := assignedRide(t, svc, cust, q.QuoteID, "in_progress", 20*time.Minute, 5*time.Minute, 4*time.Minute)
	reserved, _ := svc.Store().GetOutstanding(ctx, pending[0].ID)
	if reserved.SettledByRideID == nil || *reserved.SettledByRideID != next.ID {
		t.Fatalf("booking did not reserve the outstanding row: %+v", reserved)
	}
	if q2 := mustEstimate(t, svc, fridayNoonIST, &cust, "auto", ""); q2.OutstandingPaise != 0 {
		t.Fatalf("a reserved fee must not be quoted twice: %+v", q2)
	}
	if _, err := svc.CompleteRide(ctx, partner.UserID, next.ID, CompleteRideRequest{FinalDistanceKM: 5, FinalDurationMin: 15, IdempotencyKey: "c-" + next.ID.String(), ExpectedRevision: next.Revision}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	settled, _ := svc.Store().GetOutstanding(ctx, pending[0].ID)
	if settled.Status != store.OutstandingSettled || settled.SettledAt == nil {
		t.Fatalf("outstanding after completion %+v", settled)
	}
	done, _ := svc.Store().GetRide(ctx, next.ID)
	if b := decodeBreakdown(t, done.FareBreakdown); b.OutstandingPaise != 1500 {
		t.Fatalf("final breakdown lost the outstanding line: %+v", b)
	}
}

func TestCancelRide_PartnerAndNoShowActors(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()

	// Partner cancels late: no customer fee.
	cust := uuid.New()
	q := mustEstimate(t, svc, fridayNoonIST, &cust, "auto", "")
	ride, partner := assignedRide(t, svc, cust, q.QuoteID, "partner_arriving", 10*time.Minute, 0, 0)
	if _, err := svc.CancelRide(ctx, partner.UserID, ride.ID, "partner", CancelRideRequest{Reason: "breakdown", ExpectedRevision: ride.Revision}); err != nil {
		t.Fatalf("partner cancel: %v", err)
	}
	if o, _ := svc.Store().ListPendingOutstanding(ctx, cust); len(o) != 0 {
		t.Fatalf("partner cancel charged the customer: %+v", o)
	}

	// No-show reported by the partner past the free window: same fee as a
	// late customer cancellation.
	cust2 := uuid.New()
	q2 := mustEstimate(t, svc, fridayNoonIST, &cust2, "auto", "")
	ride2, partner2 := assignedRide(t, svc, cust2, q2.QuoteID, "arrived", 10*time.Minute, 5*time.Minute, 0)
	if err := svc.MarkRideNoShow(ctx, partner2.UserID, ride2.ID, ""); err != nil {
		t.Fatalf("no-show: %v", err)
	}
	r2, _ := svc.Store().GetRide(ctx, ride2.ID)
	if r2.Status != "cancelled_by_partner" || r2.CancellationReason == nil || *r2.CancellationReason != "customer_no_show" {
		t.Fatalf("no-show ride %+v", r2)
	}
	o, _ := svc.Store().ListPendingOutstanding(ctx, cust2)
	if len(o) != 1 || o[0].AmountPaise != 1500 {
		t.Fatalf("no-show outstanding %+v", o)
	}
	// Waived by an admin: gone from the next quote.
	admin := uuid.New()
	w, err := svc.WaiveOutstanding(ctx, admin, o[0].ID, "goodwill")
	if err != nil || w.Status != store.OutstandingWaived || w.WaivedBy == nil || *w.WaivedBy != admin {
		t.Fatalf("waive %+v %v", w, err)
	}
	if q3 := mustEstimate(t, svc, fridayNoonIST, &cust2, "auto", ""); q3.OutstandingPaise != 0 {
		t.Fatalf("waived fee still quoted: %+v", q3)
	}
	if _, err := svc.WaiveOutstanding(ctx, admin, o[0].ID, "again"); err == nil || !errors.Is(err, err) || !contains(err.Error(), "not_found") {
		t.Fatalf("second waive: %v", err)
	}
}

func TestUpdateLocation_TracksOnlyDuringTheRideAndThrottles(t *testing.T) {
	svc, _, cleanup := newIntegrationService(t)
	defer cleanup()
	ctx := context.Background()
	cust := uuid.New()
	q := mustEstimate(t, svc, fridayNoonIST, &cust, "auto", "")
	ride, partner := assignedRide(t, svc, cust, q.QuoteID, "partner_arriving", 5*time.Minute, 0, 0)
	ping := func(lat float64) {
		if err := svc.UpdateLocation(ctx, partner.UserID, UpdateLocationRequest{Lat: lat, Lng: 77.60}); err != nil {
			t.Fatalf("ping: %v", err)
		}
	}
	ping(12.9700)
	if pts, _ := svc.Store().ListTrackPoints(ctx, ride.ID); len(pts) != 0 {
		t.Fatalf("tracked before arrival: %d", len(pts))
	}
	if _, err := svc.Store().DB().Exec(ctx, `UPDATE rider_rides SET status = 'in_progress', arrived_at = NOW(), started_at = NOW() WHERE id = $1`, ride.ID); err != nil {
		t.Fatal(err)
	}
	ping(12.9710)
	ping(12.9720) // within 5 s of the last point: throttled
	pts, _ := svc.Store().ListTrackPoints(ctx, ride.ID)
	if len(pts) != 1 || pts[0].Lat != 12.9710 {
		t.Fatalf("track points %+v, want exactly the first in-ride fix", pts)
	}
}

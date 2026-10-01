package service

// The delivery-date arithmetic, the pincode shape, the carrier cache and the
// review sort — everything in engagement.go that needs no database.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/courier"
)

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

func TestComputeDeliveryWindow(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		now                      time.Time
		sla, courierDays         int
		wantBy                   string
		wantMin, wantMax, wantDp int
	}{
		// Wed 30 Sep 2026 15:30 IST; 2 dispatch days Thu, Fri; +3 → Mon 5.
		{"default SLA, weekday", utc(2026, 9, 30, 10, 0), 0, 3, "2026-10-05", 4, 5, 2},
		// The IST date boundary. 18:29Z is 23:59 Wed in India...
		{"IST: last minute of Wed", utc(2026, 9, 30, 18, 29), 48, 3, "2026-10-05", 4, 5, 2},
		// ...and 18:30Z is 00:00 Thu: dispatch Fri, Sat; +3 → Tue 6.
		{"IST: first minute of Thu", utc(2026, 9, 30, 18, 30), 48, 3, "2026-10-06", 4, 5, 2},
		// Still Thu 1 Oct in UTC terms at 20:00Z on the 30th: UTC would say Wed.
		{"IST, not UTC", utc(2026, 9, 30, 20, 0), 24, 3, "2026-10-05", 3, 4, 1},
		// Fri 2 Oct: dispatch Sat, (Sun skipped) Mon 5; +3 → Thu 8.
		{"Sunday skipped in dispatch", utc(2026, 10, 2, 6, 0), 48, 3, "2026-10-08", 4, 6, 2},
		// Sat 3 Oct, 1 day: (Sun skipped) Mon 5; +2 → Wed 7. Earliest: today
		// Sat + 2 → Mon 5.
		{"Saturday, one day", utc(2026, 10, 3, 6, 0), 24, 2, "2026-10-07", 2, 4, 1},
		// Sun 4 Oct, 1 day: Mon 5 + 3 → Thu 8; earliest is also Mon (no
		// Sunday dispatch) → Thu 8.
		{"ordered on a Sunday", utc(2026, 10, 4, 6, 0), 24, 3, "2026-10-08", 4, 4, 1},
		// Transit days are calendar days: Sundays are NOT skipped in transit.
		{"transit crosses a Sunday", utc(2026, 9, 30, 10, 0), 24, 4, "2026-10-05", 4, 5, 1},
		// SLA rounding: 25 h is two days, not one.
		{"SLA 25h rounds up", utc(2026, 9, 30, 10, 0), 25, 3, "2026-10-05", 4, 5, 2},
		{"SLA 49h rounds up", utc(2026, 9, 30, 10, 0), 49, 3, "2026-10-06", 5, 6, 3},
		{"SLA 72h", utc(2026, 9, 30, 10, 0), 72, 3, "2026-10-06", 5, 6, 3},
		{"SLA negative → default", utc(2026, 9, 30, 10, 0), -5, 3, "2026-10-05", 4, 5, 2},
	} {
		w, ok := ComputeDeliveryWindow(tc.now, tc.sla, tc.courierDays)
		if !ok {
			t.Fatalf("%s: no window", tc.name)
		}
		if got := w.DeliverBy.Format("2006-01-02"); got != tc.wantBy || w.MinDays != tc.wantMin || w.MaxDays != tc.wantMax || w.DispatchDays != tc.wantDp {
			t.Errorf("%s: deliver_by=%s min=%d max=%d dispatch=%d; want %s %d %d %d",
				tc.name, got, w.MinDays, w.MaxDays, w.DispatchDays, tc.wantBy, tc.wantMin, tc.wantMax, tc.wantDp)
		}
	}
	if _, ok := ComputeDeliveryWindow(utc(2026, 9, 30, 10, 0), 48, 0); ok {
		t.Error("a carrier with no transit time produced a date")
	}
}

func TestDispatchDaysFromSLA(t *testing.T) {
	for hours, want := range map[int]int{0: 2, -1: 2, 1: 1, 23: 1, 24: 1, 25: 2, 47: 2, 48: 2, 49: 3, 72: 3, 73: 4} {
		if got := dispatchDaysFromSLA(hours); got != want {
			t.Errorf("dispatchDaysFromSLA(%d) = %d, want %d", hours, got, want)
		}
	}
}

func TestValidPincode(t *testing.T) {
	for s, want := range map[string]bool{
		"500081": true, "110001": true, "999999": true,
		"050081": false, "50008": false, "5000811": false, "50008a": false, "": false, " 50008": false, "５00081": false,
	} {
		if got := ValidPincode(s); got != want {
			t.Errorf("ValidPincode(%q) = %v, want %v", s, got, want)
		}
	}
}

// The shape check runs before any lookup: a Service with no store and no
// courier refuses a bad pincode rather than reaching either.
func TestEstimateDeliveryRefusesABadPincodeFirst(t *testing.T) {
	for _, bad := range []string{"050081", "5000", "50008a", ""} {
		_, err := (&Service{}).EstimateDelivery(context.Background(), [16]byte{1}, bad, PincodeFromQuery)
		if !errors.Is(err, ErrInvalidPincode) {
			t.Errorf("EstimateDelivery(%q): %v, want ErrInvalidPincode", bad, err)
		}
	}
}

func TestWeightBand(t *testing.T) {
	for g, want := range map[int]int{0: 500, -3: 500, 1: 500, 350: 500, 500: 500, 501: 1000, 1000: 1000, 1499: 1500} {
		if got := weightBandGrams(g); got != want {
			t.Errorf("weightBandGrams(%d) = %d, want %d", g, got, want)
		}
	}
}

// countingCourier answers a fixed result and counts carrier calls.
type countingCourier struct {
	courier.StubCourier
	name  string
	calls int
	last  courier.ServiceabilityRequest
}

func (c *countingCourier) Name() string { return c.name }
func (c *countingCourier) CheckServiceability(_ context.Context, req courier.ServiceabilityRequest) (*courier.ServiceabilityResult, error) {
	c.calls++
	c.last = req
	return &courier.ServiceabilityResult{Serviceable: true, Courier: c.name, EstimatedDays: 4, ShippingChargeMinor: 7000}, nil
}

func TestEstimateCacheShiprocketOnly(t *testing.T) {
	now := utc(2026, 9, 30, 10, 0)
	sr := &countingCourier{name: "shiprocket"}
	s := (&Service{}).WithCourier(sr).WithClock(func() time.Time { return now })
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		res, err := s.serviceabilityForEstimate(ctx, "560001", "500081", 350)
		if err != nil || res.EstimatedDays != 4 {
			t.Fatalf("call %d: %v %+v", i, err, res)
		}
	}
	if sr.calls != 1 {
		t.Fatalf("three reads inside the TTL made %d carrier calls, want 1", sr.calls)
	}
	if sr.last.WeightKg != 0.5 {
		t.Fatalf("the carrier was asked for %.3f kg, want the 0.5 kg band", sr.last.WeightKg)
	}
	// Same band (400 g → 500 g): still cached. Next band: a new call.
	_, _ = s.serviceabilityForEstimate(ctx, "560001", "500081", 400)
	if sr.calls != 1 {
		t.Fatalf("same weight band missed the cache (%d calls)", sr.calls)
	}
	_, _ = s.serviceabilityForEstimate(ctx, "560001", "500081", 900)
	if sr.calls != 2 {
		t.Fatalf("a new weight band hit the cache (%d calls)", sr.calls)
	}
	// Another drop pin: a new call.
	_, _ = s.serviceabilityForEstimate(ctx, "560001", "400001", 350)
	if sr.calls != 3 {
		t.Fatalf("a new drop pin hit the cache (%d calls)", sr.calls)
	}
	// Just inside six hours: cached. At six hours: asked again.
	now = now.Add(etaTTL - time.Second)
	_, _ = s.serviceabilityForEstimate(ctx, "560001", "500081", 350)
	if sr.calls != 3 {
		t.Fatalf("an answer inside the TTL was not reused (%d calls)", sr.calls)
	}
	now = now.Add(time.Second)
	_, _ = s.serviceabilityForEstimate(ctx, "560001", "500081", 350)
	if sr.calls != 4 {
		t.Fatalf("an answer six hours old was reused (%d calls)", sr.calls)
	}

	// The stub is never cached.
	stub := &countingCourier{name: "stub"}
	s2 := (&Service{}).WithCourier(stub).WithClock(func() time.Time { return now })
	for i := 0; i < 3; i++ {
		_, _ = s2.serviceabilityForEstimate(ctx, "560001", "500081", 350)
	}
	if stub.calls != 3 {
		t.Fatalf("the stub was cached: %d calls for 3 reads", stub.calls)
	}
}

func TestNormaliseReviewSort(t *testing.T) {
	for in, want := range map[string]string{"": "helpful", "helpful": "helpful", "recent": "recent", "RECENT": "recent", " recent ": "recent", "top": "helpful"} {
		if got := NormaliseReviewSort(in); got != want {
			t.Errorf("NormaliseReviewSort(%q) = %q, want %q", in, got, want)
		}
	}
}

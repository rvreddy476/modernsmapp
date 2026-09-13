package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func hm(h, m int) int { return h*3600 + m*60 }

func TestOpenAt(t *testing.T) {
	loc := time.UTC
	// 2026-09-14 is a Monday.
	at := func(day, h, m int) time.Time { return time.Date(2026, 9, 13+day, h, m, 0, 0, loc) } // day 0 = Sunday
	const sun, mon, tue, wed, thu, fri, sat = 0, 1, 2, 3, 4, 5, 6

	cases := []struct {
		name    string
		windows []hoursWindow
		now     time.Time
		open    bool
	}{
		{"no rows is unrestricted", nil, at(mon, 3, 0), true},
		{"before opening", []hoursWindow{{Day: mon, Opens: hm(10, 0), Closes: hm(22, 0)}}, at(mon, 9, 59), false},
		{"at opening", []hoursWindow{{Day: mon, Opens: hm(10, 0), Closes: hm(22, 0)}}, at(mon, 10, 0), true},
		{"at closing is closed", []hoursWindow{{Day: mon, Opens: hm(10, 0), Closes: hm(22, 0)}}, at(mon, 22, 0), false},
		{"overnight same evening", []hoursWindow{{Day: fri, Opens: hm(18, 0), Closes: hm(2, 0)}}, at(fri, 23, 0), true},
		{"overnight after midnight counts yesterday", []hoursWindow{{Day: fri, Opens: hm(18, 0), Closes: hm(2, 0)}}, at(sat, 1, 30), true},
		{"overnight ends at close", []hoursWindow{{Day: fri, Opens: hm(18, 0), Closes: hm(2, 0)}}, at(sat, 2, 0), false},
		{"overnight not yet open", []hoursWindow{{Day: fri, Opens: hm(18, 0), Closes: hm(2, 0)}}, at(fri, 17, 0), false},
		{"closed day wins", []hoursWindow{{Day: sun, Closed: true}, {Day: sun, Opens: hm(10, 0), Closes: hm(22, 0)}}, at(sun, 12, 0), false},
		{"closed yesterday voids overnight", []hoursWindow{{Day: sat, Closed: true}, {Day: sat, Opens: hm(18, 0), Closes: hm(2, 0)}}, at(sun, 1, 0), false},
		{"split shift gap", []hoursWindow{{Day: tue, Opens: hm(11, 0), Closes: hm(15, 0)}, {Day: tue, Opens: hm(18, 0), Closes: hm(23, 0)}}, at(tue, 16, 0), false},
		{"split shift evening", []hoursWindow{{Day: tue, Opens: hm(11, 0), Closes: hm(15, 0)}, {Day: tue, Opens: hm(18, 0), Closes: hm(23, 0)}}, at(tue, 19, 0), true},
		{"rows exist but none today", []hoursWindow{{Day: wed, Opens: hm(10, 0), Closes: hm(22, 0)}}, at(thu, 12, 0), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := openAt(tc.now, tc.windows); got != tc.open {
				t.Fatalf("openAt(%s) = %v, want %v", tc.now.Format("Mon 15:04"), got, tc.open)
			}
		})
	}
}

func TestRiderPayoutForFee(t *testing.T) {
	if got := riderPayoutForFee(29); got != 23.20 {
		t.Fatalf("payout for 29 = %v, want 23.20", got)
	}
	if got := riderPayoutForFee(0); got != 0 {
		t.Fatalf("payout for 0 = %v", got)
	}
}

func TestAddressServiceable(t *testing.T) {
	lat, lng := 12.9, 77.6
	if !addressServiceable(lat, lng, kmNorth(lat, 6), lng, nil, 7) {
		t.Error("6 km within default 7 km refused")
	}
	if addressServiceable(lat, lng, kmNorth(lat, 8), lng, nil, 7) {
		t.Error("8 km beyond default 7 km accepted")
	}
	farCentreLat := kmNorth(lat, 20)
	areas := []serviceArea{{CenterLat: &farCentreLat, CenterLng: &lng, RadiusKM: 3}}
	if !addressServiceable(lat, lng, kmNorth(lat, 21), lng, areas, 7) {
		t.Error("address inside an area with its own centre refused")
	}
	if addressServiceable(lat, lng, kmNorth(lat, 1), lng, areas, 7) {
		t.Error("areas exist: the default radius must not apply")
	}
	if !addressServiceable(lat, lng, kmNorth(lat, 9), lng, []serviceArea{{RadiusKM: 10}}, 7) {
		t.Error("area without a centre is centred on the restaurant")
	}
}

// Hours are evaluated in the configured zone with the injected clock.
func TestPlaceOrder_OutsideHoursByClock(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	ist := DefaultOrderingConfig().Location
	lat, lng := 12.9, 77.6

	c, rid, _, _ := seedPlaceableCart(t, s, f64(lat), f64(lng))
	// Monday 10:00-22:00 only.
	if _, err := s.db.Exec(ctx, `
		INSERT INTO food.restaurant_operating_hours (restaurant_id, day_of_week, opens_at, closes_at)
		VALUES ($1, 1, '10:00', '22:00')`, rid); err != nil {
		t.Fatal(err)
	}
	addr := seedAddress(t, s, c, f64(kmNorth(lat, 1)), f64(lng))

	// 03:00 IST Monday is 21:30 UTC Sunday: a UTC evaluation would also be
	// closed, so check 10:30 IST (05:00 UTC, before a UTC opening) as open.
	s.WithOrderingConfig(OrderingConfig{Now: func() time.Time { return time.Date(2026, 9, 14, 3, 0, 0, 0, ist) }})
	_, err := s.PlaceOrder(ctx, c, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString())
	assertOutsideHours(t, err)

	s.WithOrderingConfig(OrderingConfig{Now: func() time.Time { return time.Date(2026, 9, 14, 10, 30, 0, 0, ist) }})
	if _, err := s.PlaceOrder(ctx, c, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString()); err != nil {
		t.Fatalf("10:30 IST Monday should be open: %v", err)
	}
}

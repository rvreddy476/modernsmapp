package postgres

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestNextOpenAt(t *testing.T) {
	loc := DefaultOrderingConfig().Location
	// 2026-09-14 is a Monday; day 0 = Sunday 13 Sep.
	at := func(day, h, m int) time.Time { return time.Date(2026, 9, 13+day, h, m, 0, 0, loc) }
	const sun, mon, tue, wed, fri, sat = 0, 1, 2, 3, 5, 6
	allClosed := []hoursWindow{}
	for d := 0; d < 7; d++ {
		allClosed = append(allClosed, hoursWindow{Day: d, Closed: true})
	}

	cases := []struct {
		name    string
		windows []hoursWindow
		now     time.Time
		want    time.Time
		ok      bool
	}{
		{"unrestricted", nil, at(mon, 3, 0), time.Time{}, false},
		{"open now", []hoursWindow{{Day: mon, Opens: hm(10, 0), Closes: hm(22, 0)}}, at(mon, 12, 0), time.Time{}, false},
		{"later today", []hoursWindow{{Day: mon, Opens: hm(10, 0), Closes: hm(22, 0)}}, at(mon, 9, 0), at(mon, 10, 0), true},
		{"after closing, tomorrow", []hoursWindow{{Day: mon, Opens: hm(10, 0), Closes: hm(22, 0)}, {Day: tue, Opens: hm(11, 0), Closes: hm(15, 0)}},
			at(mon, 22, 30), at(tue, 11, 0), true},
		{"split shift gap", []hoursWindow{{Day: tue, Opens: hm(11, 0), Closes: hm(15, 0)}, {Day: tue, Opens: hm(18, 0), Closes: hm(23, 0)}},
			at(tue, 16, 0), at(tue, 18, 0), true},
		{"closed day skipped", []hoursWindow{{Day: tue, Closed: true}, {Day: wed, Closed: true}, {Day: fri, Opens: hm(18, 0), Closes: hm(2, 0)}},
			at(mon, 23, 0), at(fri, 18, 0), true},
		{"same weekday next week", []hoursWindow{{Day: mon, Opens: hm(10, 0), Closes: hm(22, 0)}}, at(mon, 22, 0), at(mon+7, 10, 0), true},
		{"overnight not yet open", []hoursWindow{{Day: fri, Opens: hm(18, 0), Closes: hm(2, 0)}}, at(fri, 17, 0), at(fri, 18, 0), true},
		{"after an overnight close", []hoursWindow{{Day: fri, Opens: hm(18, 0), Closes: hm(2, 0)}}, at(sat, 2, 0), at(fri+7, 18, 0), true},
		{"sunday only", []hoursWindow{{Day: sun, Opens: hm(9, 0), Closes: hm(12, 0)}}, at(sat, 23, 0), at(sun+7, 9, 0), true},
		{"never opens", allClosed, at(mon, 12, 0), time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := nextOpenAt(tc.now, tc.windows)
			if ok != tc.ok || !got.Equal(tc.want) {
				t.Fatalf("nextOpenAt(%s) = %s, %v; want %s, %v", tc.now.Format("Mon 02 15:04"), got.Format("Mon 02 15:04"), ok, tc.want.Format("Mon 02 15:04"), tc.ok)
			}
			if !ok {
				return
			}
			// It agrees with openAt: open at that instant, closed every minute before it.
			if !openAt(got, tc.windows) {
				t.Fatalf("openAt(%s) is false", got)
			}
			for m := tc.now; m.Before(got); m = m.Add(time.Minute) {
				if openAt(m, tc.windows) {
					t.Fatalf("open at %s, before the reported next opening %s", m.Format("Mon 02 15:04"), got.Format("Mon 02 15:04"))
				}
			}
		})
	}
}

func TestEvaluateServiceabilityOrder(t *testing.T) {
	cfg := DefaultOrderingConfig()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, cfg.Location) // Monday
	lat, lng := 12.9, 77.6
	pin := func(km float64) *deliveryPoint { return &deliveryPoint{Lat: f64(kmNorth(lat, km)), Lng: f64(lng)} }
	closed := []hoursWindow{{Day: 1, Closed: true}}
	base := serviceabilityFacts{Active: true, Lat: f64(lat), Lng: f64(lng)}
	with := func(edit func(*serviceabilityFacts)) serviceabilityFacts { f := base; edit(&f); return f }

	cases := []struct {
		name string
		f    serviceabilityFacts
		to   *deliveryPoint
		want error
	}{
		{"serviceable", base, pin(1), nil},
		{"not accepting wins over everything", with(func(f *serviceabilityFacts) { f.Active, f.Lat, f.Windows = false, nil, closed }), pin(50), ErrRestaurantNotAccepting},
		{"restaurant pin before address pin", with(func(f *serviceabilityFacts) { f.Lat = nil }), &deliveryPoint{}, ErrRestaurantLocationMissing},
		{"address pin before hours", with(func(f *serviceabilityFacts) { f.Windows = closed }), &deliveryPoint{}, ErrAddressLocationRequired},
		{"hours before range", with(func(f *serviceabilityFacts) { f.Windows = closed }), pin(50), ErrRestaurantOutsideHours},
		{"out of range", base, pin(50), ErrAddressOutOfRange},
		{"service area decides range", with(func(f *serviceabilityFacts) { f.Areas = []serviceArea{{RadiusKM: 3}} }), pin(5), ErrAddressOutOfRange},
		{"no point: pins and range not checked", with(func(f *serviceabilityFacts) { f.Lat = nil }), nil, nil},
		{"no point: hours still checked", with(func(f *serviceabilityFacts) { f.Windows = closed }), nil, ErrRestaurantOutsideHours},
		{"no point: accepting still checked", with(func(f *serviceabilityFacts) { f.Active = false }), nil, ErrRestaurantNotAccepting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cfg.evaluateServiceability(now, tc.f, tc.to)
			if !errors.Is(err, tc.want) || (err == nil) != (tc.want == nil) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	d, err := cfg.evaluateServiceability(now, base, pin(50))
	if !errors.Is(err, ErrAddressOutOfRange) || d == nil || math.Abs(*d-50) > 0.01 {
		t.Fatalf("a refusal still reports the distance: %v %v", d, err)
	}
	if d, _ := cfg.evaluateServiceability(now, base, nil); d != nil {
		t.Fatalf("no point, but distance %v", *d)
	}
}

func TestRankByServiceability(t *testing.T) {
	yes, no := true, false
	m := func(v int64) *int64 { return &v }
	in := []RestaurantSummary{
		{Name: "A", Serviceable: &no, DistanceMeters: m(100)},
		{Name: "B", Serviceable: &yes, DistanceMeters: m(3000)},
		{Name: "C", Serviceable: &yes, DistanceMeters: m(1000)},
		{Name: "D", Serviceable: &yes},
		{Name: "E", Serviceable: &no},
		{Name: "F", Serviceable: &yes, DistanceMeters: m(1000)},
		{Name: "G", Serviceable: &no, DistanceMeters: m(50)},
	}
	rankByServiceability(in)
	got := ""
	for _, r := range in {
		got += r.Name
	}
	if got != "CFBDGAE" {
		t.Fatalf("order = %s, want CFBDGAE (serviceable first, then nearest, no distance last, ties stable)", got)
	}
}

func TestDescribeServiceability(t *testing.T) {
	cfg := DefaultOrderingConfig()
	now := time.Date(2026, 9, 14, 3, 30, 0, 0, time.UTC) // 09:00 IST Monday
	lat, lng := 12.9, 77.6
	f := serviceabilityFacts{Active: true, Lat: f64(lat), Lng: f64(lng),
		Windows: []hoursWindow{{Day: 1, Opens: hm(10, 0), Closes: hm(22, 0)}}}

	var plain RestaurantSummary
	cfg.describeServiceability(&plain, now, f, nil)
	if plain.IsOpenNow || plain.NextOpensAt == nil || *plain.NextOpensAt != "2026-09-14T10:00:00+05:30" {
		t.Fatalf("schedule fields = %v %v", plain.IsOpenNow, plain.NextOpensAt)
	}
	if plain.Serviceable != nil || plain.DistanceMeters != nil || plain.Unserviceable != nil {
		t.Fatalf("no point was given but serviceability was set: %+v", plain)
	}

	var near RestaurantSummary
	cfg.describeServiceability(&near, now, f, &GeoPoint{Lat: kmNorth(lat, 2.5), Lng: lng})
	if near.Serviceable == nil || *near.Serviceable || !errors.Is(near.Unserviceable, ErrRestaurantOutsideHours) ||
		near.DistanceMeters == nil || *near.DistanceMeters != 2500 {
		t.Fatalf("near = serviceable %v err %v distance %v", near.Serviceable, near.Unserviceable, near.DistanceMeters)
	}

	var open RestaurantSummary
	cfg.describeServiceability(&open, now.Add(2*time.Hour), f, &GeoPoint{Lat: kmNorth(lat, 1), Lng: lng})
	if !open.IsOpenNow || open.NextOpensAt != nil || open.Serviceable == nil || !*open.Serviceable || open.Unserviceable != nil {
		t.Fatalf("open = %+v", open)
	}
}

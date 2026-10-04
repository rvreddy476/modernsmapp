package dispatch

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 4, 6, 30, 0, 0, time.UTC)

func TestOfferExpiry(t *testing.T) {
	for _, c := range []struct {
		name   string
		slotIn time.Duration
		rescue *time.Time
		want   time.Duration
	}{
		{"far slot: 2 h", 13 * time.Hour, nil, 2 * time.Hour},
		{"exactly 12 h: near", 12 * time.Hour, nil, 10 * time.Minute},
		{"near slot: 10 min", 3 * time.Hour, nil, 10 * time.Minute},
		{"capped at the rescue deadline", time.Hour, ptr(t0.Add(4 * time.Minute)), 4 * time.Minute},
		{"a past rescue deadline does not cap", time.Hour, ptr(t0.Add(-time.Minute)), 10 * time.Minute},
	} {
		if got := OfferExpiry(t0, t0.Add(c.slotIn), DefaultWindows, c.rescue); !got.Equal(t0.Add(c.want)) {
			t.Errorf("%s: %v, want +%v", c.name, got.Sub(t0), c.want)
		}
	}
	// Zero windows fall back to the defaults.
	if got := OfferExpiry(t0, t0.Add(time.Hour), Windows{}, nil); !got.Equal(t0.Add(10 * time.Minute)) {
		t.Errorf("zero windows: %v", got.Sub(t0))
	}
}

func ptr(t time.Time) *time.Time { return &t }

func TestRescueStart(t *testing.T) {
	now := t0.Add(31 * time.Minute) // 06:61 -> 07:01
	start, ok := RescueStart(now, 30)
	if !ok || !start.Equal(time.Date(2026, 10, 4, 7, 35, 0, 0, time.UTC)) {
		t.Fatalf("start %v ok %v", start, ok)
	}
	if _, ok := RescueStart(now, 61); ok {
		t.Fatal("a buffer over an hour is not a rescue")
	}
	if s, _ := RescueStart(t0, 30); !s.Equal(t0.Add(30 * time.Minute)) {
		t.Fatalf("already on the 5-minute grid: %v", s)
	}
}

func TestCancelPenalty(t *testing.T) {
	for _, c := range []struct {
		left   time.Duration
		status string
		paise  int64
		tier   string
	}{
		{25 * time.Hour, "assigned", 0, TierFree},
		{24 * time.Hour, "assigned", 0, TierFree},
		{23 * time.Hour, "assigned", PenaltyLowPaise, TierLow},
		{3 * time.Hour, "assigned", PenaltyLowPaise, TierLow},
		{2 * time.Hour, "assigned", PenaltyHighPaise, TierHigh},
		{30 * time.Hour, "en_route", PenaltyEnRoutePaise, TierEnRoute},
	} {
		p, tier := CancelPenalty(t0, t0.Add(c.left), c.status)
		if p != c.paise || tier != c.tier {
			t.Errorf("%v %s: %d %s, want %d %s", c.left, c.status, p, tier, c.paise, c.tier)
		}
	}
}

func TestEarningEstimate(t *testing.T) {
	if got := EarningEstimate(100000, 2000); got != 80000 {
		t.Fatalf("20%%: %d", got)
	}
	if got := EarningEstimate(99999, 2000); got != 80000 { // commission rounds down
		t.Fatalf("rounding: %d", got)
	}
	if EarningEstimate(1000, -5) != 1000 || EarningEstimate(1000, 20000) != 0 {
		t.Fatal("bps not clamped")
	}
}

func TestOfferStatus(t *testing.T) {
	exp := t0.Add(time.Minute)
	for as, want := range map[string]string{"accepted": OfferAccepted, "completed": OfferAccepted, "declined": OfferDeclined,
		"expired": OfferExpired, "released": OfferWithdrawn, "cancelled": OfferWithdrawn, "no_show": OfferWithdrawn} {
		if got := OfferStatus(as, exp, t0); got != want {
			t.Errorf("%s: %s want %s", as, got, want)
		}
	}
	if OfferStatus("offered", exp, t0) != OfferOpen || OfferStatus("offered", exp, exp) != OfferExpired {
		t.Fatal("offered by expiry")
	}
}

// The address is visible from acceptance to completion + 2 h only.
func TestAddressVisible(t *testing.T) {
	done := t0
	for _, c := range []struct {
		as, bs    string
		completed *time.Time
		now       time.Time
		want      bool
	}{
		{"offered", "confirmed", nil, t0, false},
		{"accepted", "assigned", nil, t0, true},
		{"accepted", "in_progress", nil, t0, true},
		{"completed", "completed", &done, t0.Add(119 * time.Minute), true},
		{"completed", "completed", &done, t0.Add(2 * time.Hour), false},
		{"accepted", "cancelled", nil, t0, false},
		{"declined", "assigned", nil, t0, false},
		{"cancelled", "assigned", nil, t0, false},
	} {
		if got := AddressVisible(c.as, c.bs, c.completed, c.now); got != c.want {
			t.Errorf("%s/%s at %v: %v", c.as, c.bs, c.now.Sub(t0), got)
		}
	}
}

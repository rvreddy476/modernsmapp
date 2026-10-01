package courier

// The delivery estimate's transit time from Shiprocket: `estimated_delivery_days`
// when it is there, and the `etd` date when only that is.

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestDaysUntilETD(t *testing.T) {
	// Wed 30 Sep 2026 15:30 IST.
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	for etd, want := range map[string]int{
		"Oct 04, 2026":        4,
		"Oct 4, 2026":         4,
		"2026-10-04 18:00:00": 4,
		"2026-10-04":          4,
		"Oct 01, 2026":        1,
		"Sep 30, 2026":        0, // today is not a transit time
		"Sep 20, 2026":        0, // the past is unknown, not negative
		"":                    0,
		"soon":                0,
	} {
		if got := daysUntilETD(etd, now); got != want {
			t.Errorf("daysUntilETD(%q) = %d, want %d", etd, got, want)
		}
	}
	// The IST boundary: 18:30Z on the 30th is already 1 Oct in India, so
	// 4 Oct is three days away, not four.
	if got := daysUntilETD("Oct 04, 2026", time.Date(2026, 9, 30, 18, 30, 0, 0, time.UTC)); got != 3 {
		t.Errorf("etd across the IST midnight: %d, want 3", got)
	}
}

// A fake Shiprocket that sends only `etd`: the adapter still reports the
// transit days, so the product page still gets a date.
func TestServiceabilityTransitFromEtdOnly(t *testing.T) {
	etd := time.Now().In(shiprocketIST).AddDate(0, 0, 4).Format("Jan 02, 2006")
	c := fakeShiprocket(t, rateCard(map[string]any{
		"courier_name": "Delhivery", "rate": 61.0, "estimated_delivery_days": "", "etd": etd, "cod": 0,
	}), http.StatusOK)
	res, err := c.CheckServiceability(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Serviceable || res.EstimatedDays != 4 {
		t.Fatalf("etd-only answer: %+v, want serviceable with 4 days", res)
	}
	// And estimated_delivery_days wins when both are present.
	c = fakeShiprocket(t, rateCard(map[string]any{
		"courier_name": "Delhivery", "rate": 61.0, "estimated_delivery_days": "2", "etd": etd, "cod": 0,
	}), http.StatusOK)
	res, err = c.CheckServiceability(context.Background(), req)
	if err != nil || res.EstimatedDays != 2 {
		t.Fatalf("both present: %+v %v, want 2 days", res, err)
	}
}

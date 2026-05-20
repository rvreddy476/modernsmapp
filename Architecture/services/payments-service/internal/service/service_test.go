package service

import "testing"

// TestRupeesToPaise pins the API-boundary unit conversion so a future
// refactor that "simplifies" the cast back to int64 trips immediately.
// The reconciliation bug previously here passed ₹X to a gateway that
// reads paise, so the provider order opened at ₹X/100.
func TestRupeesToPaise(t *testing.T) {
	cases := []struct {
		name        string
		rupees      float64
		wantPaise   int64
	}{
		{"one rupee", 1.0, 100},
		{"one hundred", 100.0, 10000},
		{"one hundred and fifty paise", 100.50, 10050},
		// Banker's-rounded floats: 0.295 lands at 0.2949999... in
		// IEEE-754, so math.Round(29.499...) is 29. Verify we round
		// the math.Round result, not truncate.
		{"₹0.01", 0.01, 1},
		{"₹0.05", 0.05, 5},
		{"₹0.99", 0.99, 99},
		{"₹99.99", 99.99, 9999},
		// A common Razorpay test amount.
		{"₹500", 500.0, 50000},
		// Zero passes through.
		{"zero", 0.0, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rupeesToPaise(c.rupees)
			if got != c.wantPaise {
				t.Errorf("rupeesToPaise(%v) = %d, want %d", c.rupees, got, c.wantPaise)
			}
		})
	}
}

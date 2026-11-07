package service

import "testing"

// Affiliate commission is integer paise (review 2026-11-07): the stored
// percentage and flat amount are converted once, and the money is integer
// arithmetic, truncated.
func TestAffiliateCommissionPaise(t *testing.T) {
	flat := func(rupees float64) *float64 { return &rupees }
	cases := []struct {
		name   string
		total  int64
		pct    float32
		flat   *float64
		expect int64
	}{
		{"5% of Rs 999.99", 99_999, 5.0, nil, 4_999},           // 4999.95 truncated
		{"3.3% stored as a REAL", 1_000_000, 3.3, nil, 33_000}, // float32 3.2999999 -> 330 bps
		{"2.5% of one paisa", 1, 2.5, nil, 0},
		{"percentage wins over flat", 10_000, 10, flat(50), 1_000},
		{"flat when the percentage is zero", 10_000, 0, flat(12.34), 1_234},
		{"flat 0.29 rupees is 29 paise, not 28", 10_000, 0, flat(0.29), 29},
		{"nothing configured", 10_000, 0, nil, 0},
		{"no order total", 0, 5, nil, 0},
		{"a percentage over 100 is capped at the order", 10_000, 150, nil, 10_000},
		{"large order does not overflow", 20_000_000_000_000_000, 7.5, nil, 1_500_000_000_000_000},
	}
	for _, c := range cases {
		if got := AffiliateCommissionPaise(c.total, c.pct, c.flat); got != c.expect {
			t.Errorf("%s: AffiliateCommissionPaise(%d, %v) = %d, want %d", c.name, c.total, c.pct, got, c.expect)
		}
	}
}

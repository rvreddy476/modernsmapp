package service

import "testing"

// TestClampListPagination covers the HP3 default + cap + non-negative
// offset rules. Lives at the service layer because every list endpoint
// funnels through clampListPagination — if any of these defaults drift,
// pages can quietly grow unbounded.
func TestClampListPagination(t *testing.T) {
	cases := []struct {
		name              string
		inLimit, inOffset int
		wantLimit         int
		wantOffset        int
	}{
		{"zero defaults to 20", 0, 0, 20, 0},
		{"negative defaults to 20", -5, 0, 20, 0},
		{"sane value passes through", 50, 100, 50, 100},
		{"caps at 200", 1000, 0, 200, 0},
		{"exactly 200 passes through", 200, 0, 200, 0},
		{"201 clamps to 200", 201, 0, 200, 0},
		{"negative offset becomes 0", 20, -10, 20, 0},
		{"both clamped together", 10000, -1, 200, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotLimit, gotOffset := clampListPagination(tc.inLimit, tc.inOffset)
			if gotLimit != tc.wantLimit {
				t.Errorf("limit: got %d want %d", gotLimit, tc.wantLimit)
			}
			if gotOffset != tc.wantOffset {
				t.Errorf("offset: got %d want %d", gotOffset, tc.wantOffset)
			}
		})
	}
}

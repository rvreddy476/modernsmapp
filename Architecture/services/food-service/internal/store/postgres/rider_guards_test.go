package postgres

import "testing"

// Audit 2026-10-30 L3 / L4: the pure rider rules.

func TestPlausibleRiderMove(t *testing.T) {
	cases := []struct {
		name    string
		km, sec float64
		want    bool
	}{
		{"standing still", 0, 0, true},
		{"jitter in the same instant", 0.25, 0, true},
		{"just past jitter in the same instant", 0.26, 0, false},
		{"60 km/h", 1, 60, true},
		{"exactly the cap", 2.5, 60, true},
		{"just over the cap", 2.51, 60, false},
		{"teleport", 22, 30, false},
		{"long gap makes a real journey believable", 100, 3600, true},
		{"negative clock skew is floored, not divided by", 1, -5, false},
	}
	for _, tc := range cases {
		if got := PlausibleRiderMove(tc.km, tc.sec); got != tc.want {
			t.Errorf("%s: PlausibleRiderMove(%v km, %v s) = %v, want %v", tc.name, tc.km, tc.sec, got, tc.want)
		}
	}
}

func TestRiderPingAllowed(t *testing.T) {
	for _, tc := range []struct {
		status string
		onJob  bool
		want   bool
	}{
		{"ACTIVE", false, true},
		{"ACTIVE", true, true},
		{"OFFLINE", true, true},
		{"OFFLINE", false, false},
		{"PENDING_REVIEW", true, false},
		{"SUSPENDED", true, false},
		{"APPROVED", false, false},
		{"CLOSED", true, false},
	} {
		if got := riderPingAllowed(tc.status, tc.onJob); got != tc.want {
			t.Errorf("riderPingAllowed(%s, onJob=%v) = %v, want %v", tc.status, tc.onJob, got, tc.want)
		}
	}
}

func TestRiderMayWorkHeldJob(t *testing.T) {
	for status, want := range map[string]bool{
		"ACTIVE": true, "OFFLINE": true,
		"SUSPENDED": false, "PENDING_REVIEW": false, "APPROVED": false, "REJECTED": false, "CLOSED": false, "DRAFT": false,
	} {
		if got := riderMayWorkHeldJob(status); got != want {
			t.Errorf("riderMayWorkHeldJob(%s) = %v, want %v", status, got, want)
		}
	}
}

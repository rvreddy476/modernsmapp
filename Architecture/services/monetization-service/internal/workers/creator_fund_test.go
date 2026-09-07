package workers

import (
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/service"
)

// The settlement worker wakes hourly and asks whether the closed period is
// due. Getting this wrong in one direction pays a period against analytics
// that has not been rolled up yet; in the other it never pays at all.
func TestSettlementReadyAt(t *testing.T) {
	cases := []struct {
		period  service.SettlementPeriod
		lagDays int
		hour    int
		want    string
	}{
		{service.MonthPeriod(2026, time.September), 1, 4, "2026-10-02T04:00:00Z"},
		{service.MonthPeriod(2026, time.December), 1, 4, "2027-01-02T04:00:00Z"},
		{service.SemiMonthPeriod(2026, time.September, 1), 1, 4, "2026-09-17T04:00:00Z"},
		{service.SemiMonthPeriod(2026, time.September, 2), 1, 4, "2026-10-02T04:00:00Z"},
		{service.MonthPeriod(2026, time.September), 0, 0, "2026-10-01T00:00:00Z"},
		{service.MonthPeriod(2026, time.February), 3, 6, "2026-03-04T06:00:00Z"},
	}
	for _, tc := range cases {
		got := settlementReadyAt(tc.period, tc.lagDays, tc.hour).UTC().Format(time.RFC3339)
		if got != tc.want {
			t.Errorf("%s lag=%d hour=%d: ready at %s, want %s",
				tc.period.Key, tc.lagDays, tc.hour, got, tc.want)
		}
	}
}

// The advisory lock is a de-duplication convenience, not the safety
// property — but it still has to be stable across processes (or two pods
// take different locks and both run) and non-negative (pg_try_advisory_lock
// takes a signed bigint, and a wrapped negative is still a valid key but
// makes the number useless to a human reading pg_locks).
func TestSettlementLockKeyIsStableAndNonNegative(t *testing.T) {
	seen := map[int64]string{}
	for _, key := range []string{"2026-09", "2026-10", "2026-09-H1", "2026-09-H2", "2025-12"} {
		got := settlementLockKey(key)
		if got < 0 {
			t.Errorf("%s: lock key %d is negative", key, got)
		}
		if got != settlementLockKey(key) {
			t.Errorf("%s: lock key is not deterministic", key)
		}
		if prev, ok := seen[got]; ok {
			t.Errorf("%s and %s hash to the same lock key %d", key, prev, got)
		}
		seen[got] = key
	}
}

package postgres

import (
	"strings"
	"testing"
)

// The reschedule and publish poll shift must type both times; otherwise
// Postgres infers $2 as an interval (42804, ends_at is timestamptz).
func TestShiftPollEndsSQLTypesBothTimes(t *testing.T) {
	for _, want := range []string{"$2::timestamptz", "$3::timestamptz"} {
		if !strings.Contains(shiftPollEndsSQL, want) {
			t.Fatalf("shiftPollEndsSQL lacks %s: %s", want, shiftPollEndsSQL)
		}
	}
}

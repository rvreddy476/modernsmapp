package postgres

import (
	"strings"
	"testing"

	"github.com/atpost/trust-safety-service/database"
)

// reels-29: only an upheld (resolved) post report owes a takedown, once; a
// dismissal sends nothing and cancels one not yet sent.
func TestTakedownOnTransition(t *testing.T) {
	cases := []struct {
		entity, status, current, want string
	}{
		{"post", "resolved", "", TakedownPending},
		// Not upheld: nothing is sent.
		{"post", "dismissed", "", ""},
		{"post", "reviewing", "", ""},
		// Other subjects are not taken down here.
		{"comment", "resolved", "", ""},
		{"user", "resolved", "", ""},
		{"message", "resolved", "", ""},
		{"conversation", "resolved", "", ""},
		{"hashtag", "resolved", "", ""},
		{"sound", "resolved", "", ""},
		// A takedown already owed or done is never queued twice.
		{"post", "resolved", TakedownSent, ""},
		{"post", "resolved", TakedownPending, ""},
		// Dismissing an upheld report cancels only a takedown not yet sent.
		{"post", "dismissed", TakedownPending, TakedownCancelled},
		{"post", "dismissed", TakedownSent, ""},
		{"post", "dismissed", TakedownFailed, ""},
	}
	for _, c := range cases {
		if got := TakedownOnTransition(c.entity, c.status, c.current); got != c.want {
			t.Errorf("TakedownOnTransition(%q, %q, %q) = %q, want %q", c.entity, c.status, c.current, got, c.want)
		}
	}
}

func TestReportTakedownReason(t *testing.T) {
	if got := ReportTakedownReason("  spam reel removed  ", "spam"); got != "spam reel removed" {
		t.Fatalf("notes reason = %q", got)
	}
	if got := ReportTakedownReason("   ", "hate_abuse"); got != "Removed after a report: hate abuse" {
		t.Fatalf("fallback reason = %q", got)
	}
	long := strings.Repeat("é", maxTakedownReason+10)
	if got := ReportTakedownReason(long, "spam"); len([]rune(got)) != maxTakedownReason {
		t.Fatalf("reason runes = %d, want %d", len([]rune(got)), maxTakedownReason)
	}
}

// The migration keeps a pending takedown signable (actor and reason) and
// the status vocabulary closed.
func TestReportTakedownMigration(t *testing.T) {
	raw, err := database.Migrations.ReadFile("migrations/021_report_takedown.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"takedown_status IN ('pending', 'sent', 'superseded', 'failed', 'cancelled')",
		"takedown_actor_id IS NOT NULL",
		"WHERE takedown_status = 'pending'",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("021 lacks %q", want)
		}
	}
}

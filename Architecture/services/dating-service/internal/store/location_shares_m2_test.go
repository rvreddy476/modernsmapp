package store

import (
	"strings"
	"testing"
)

// Audit 2026-10-30 M2: a share to a match is readable only while the pair
// has an open match; a trusted-contact share does not depend on one.
func TestLiveShareRecipientPredicateRequiresOpenMatchForMatchShares(t *testing.T) {
	p := liveShareRecipientPredicate("ls")
	for _, want := range []string{
		"ls.recipient_kind <> 'match' OR EXISTS",
		"lsm.user_a = LEAST(ls.user_id, ls.recipient_id)",
		"lsm.user_b = GREATEST(ls.user_id, ls.recipient_id)",
		"lsm.status IN " + openMatchStatuses,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("predicate %q lacks %q", p, want)
		}
	}
}

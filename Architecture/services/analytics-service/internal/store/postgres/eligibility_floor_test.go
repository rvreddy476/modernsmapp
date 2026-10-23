package postgres

import (
	"strings"
	"testing"
)

// Copyright quick wins (migration 014): the eligibility write sets the
// floor only on the transition INTO eligible, so a held period never earns
// once the content is cleared, and an eligible re-affirmation keeps the
// floor where it was. Run on Postgres in the aggregation integration test.
func TestApplyContentEligibilitySetsTheFloorOnTheTransition(t *testing.T) {
	want := "WHEN $3 = 'eligible' AND eligibility_state <> 'eligible' THEN $4\n\t\t        ELSE eligibility_eligible_from END"
	if !strings.Contains(applyContentEligibilitySQL, want) {
		t.Fatalf("the floor clause changed:\n%s", applyContentEligibilitySQL)
	}
}

package scylla

import (
	"strings"
	"testing"
)

// feed-14 / feed-49: the writes to reactions_by_post are conditional. Their
// "applied" answer is what the like count, the Redis counter, the engagement
// event and the outbox row all hang on, so a statement that lost its IF
// clause would count every repeat tap. The behaviour itself needs Scylla
// (interactions_integration_test.go, CI); this pins what is sent.

func TestReactionWrites_AreConditional(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }

	insert := norm(reactInsertCQL)
	if !strings.HasPrefix(insert, "INSERT INTO reactions_by_post") || !strings.HasSuffix(insert, "IF NOT EXISTS") {
		t.Fatalf("the like insert must end in IF NOT EXISTS: %q", insert)
	}
	del := norm(unreactDeleteCQL)
	if !strings.HasPrefix(del, "DELETE FROM reactions_by_post") || !strings.HasSuffix(del, "IF EXISTS") {
		t.Fatalf("the unlike delete must end in IF EXISTS: %q", del)
	}
	// Scylla refuses to mix conditional and plain writes on one row, and a
	// plain UPDATE would bring back a row the viewer just removed.
	retype := norm(reactRetypeCQL)
	if !strings.HasPrefix(retype, "UPDATE reactions_by_post") || !strings.HasSuffix(retype, "IF EXISTS") {
		t.Fatalf("the reaction-type rewrite must end in IF EXISTS: %q", retype)
	}
}

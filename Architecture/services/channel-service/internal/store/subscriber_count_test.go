package store

import (
	"strings"
	"testing"
)

// subscriber_count is the roster (2026-11-07). Pinned-query test: the rule
// is enforced in SQL, so the SQL is what a unit test can hold still; the
// statements run in subscriber_count_integration_test.go (CI).
func TestRecountSubscribersSQLCountsTheRoster(t *testing.T) {
	q := recountSubscribersSQL
	if !strings.Contains(q, "FROM channel_members m") || !strings.Contains(q, "m.channel_id = ch.id") {
		t.Fatalf("the count must come from channel_members: %s", q)
	}
	if !strings.Contains(q, "m.role <> 'banned'") {
		t.Fatalf("a banned row is not a subscriber; every read excludes it: %s", q)
	}
	if !strings.Contains(q, "ch.id = ANY($1)") {
		t.Fatalf("the recount must be scoped to the channels named: %s", q)
	}
	if strings.Contains(q, "updated_at") {
		t.Fatalf("a recount must not bump updated_at (clients sort and cache on it): %s", q)
	}
	if !strings.Contains(q, "IS DISTINCT FROM c.n") {
		t.Fatalf("only wrong rows are written: %s", q)
	}
}

// It must agree with the batched read (CountSubscribersBatch).
func TestRecountAndBatchedCountAgree(t *testing.T) {
	if !strings.Contains(countSubscribersBatchSQL, "role <> 'banned'") {
		t.Fatal("the batched count and the recount must exclude the same rows")
	}
}

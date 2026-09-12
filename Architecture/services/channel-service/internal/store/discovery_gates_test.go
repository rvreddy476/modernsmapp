package store

import (
	"strings"
	"testing"
)

// Communities invite-only pilot (2026-09-12). These are pinned-query tests:
// the gates below are enforced in SQL, so the only thing a unit test can
// hold still is the SQL. They exist so that deleting a gate is a test
// failure rather than a silent leak.

// The founder's decision: "no public discovery directory". A private or
// paid community must never be listed, and a suspended one must drop out.
func TestDiscoverQueryGates(t *testing.T) {
	if !strings.Contains(discoverChannelsBaseQuery, "status = 'active'") {
		t.Fatal("/discover must list only active channels — a suspended community would otherwise stay in the directory")
	}
	if !strings.Contains(discoverChannelsBaseQuery, "channel_type IN ('public','creator','brand','education','official','topic')") {
		t.Fatal("/discover must filter channel_type to the public set — private/paid communities would otherwise be listed")
	}
	for _, private := range []string{"'private'", "'paid'"} {
		if strings.Contains(discoverChannelsBaseQuery, private) {
			t.Fatalf("/discover query mentions %s — a private community must never be discoverable", private)
		}
	}
}

// DiscoverPublicChannelTypes is the contract search-service mirrors. If this
// list and the SQL diverge, one surface leaks what the other hides.
func TestDiscoverPublicChannelTypesMatchesTheQuery(t *testing.T) {
	for _, ct := range DiscoverPublicChannelTypes {
		if !strings.Contains(discoverChannelsBaseQuery, "'"+ct+"'") {
			t.Fatalf("channel_type %q is in DiscoverPublicChannelTypes but not in the /discover query", ct)
		}
	}
	if len(DiscoverPublicChannelTypes) != 6 {
		t.Fatalf("the public set changed (%v) — update search-service's channel filter to match", DiscoverPublicChannelTypes)
	}
}

// Task 5: the moderation queue read is newest-first and keyset paginated,
// and the two must walk the same tuple or paging skips or repeats rows.
func TestReportQueueCursorOrdering(t *testing.T) {
	if !strings.Contains(reportQueueOrder, "r.created_at DESC") || !strings.Contains(reportQueueOrder, "r.id DESC") {
		t.Fatalf("the report queue must be ordered newest-first on (created_at, id): %q", reportQueueOrder)
	}
	if !strings.Contains(reportQueueKeyset, "(r.created_at, r.id) <") {
		t.Fatalf("the keyset must compare the same tuple the ordering sorts by: %q", reportQueueKeyset)
	}
}

func TestValidReportStatuses(t *testing.T) {
	// Must match the channel_reports CHECK in database/setup.sql.
	for _, ok := range []string{"open", "reviewed", "dismissed"} {
		if !ValidReportStatuses[ok] {
			t.Fatalf("status %q must be valid", ok)
		}
	}
	for _, bad := range []string{"", "all", "closed", "OPEN"} {
		if ValidReportStatuses[bad] {
			t.Fatalf("status %q must not be valid", bad)
		}
	}
}

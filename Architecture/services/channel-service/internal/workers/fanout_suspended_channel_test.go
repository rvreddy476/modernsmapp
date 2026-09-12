package workers

import (
	"strings"
	"testing"
)

// Communities invite-only pilot (2026-09-12), task 6: the per-channel
// emergency disable writes broadcast_channels.status = 'suspended'. The
// fan-out roster read is SQL, so the gate is pinned here: without the
// status join a suspended community would keep pushing notifications and
// injecting home-timeline rows after being switched off.
func TestSubscriberBatchQueryGatesSuspendedChannels(t *testing.T) {
	q := subscriberBatchQuery

	if !strings.Contains(q, "JOIN broadcast_channels") {
		t.Fatal("the fan-out roster must join broadcast_channels to see the channel's status")
	}
	if !strings.Contains(q, "bc.status = 'active'") {
		t.Fatal("the fan-out roster must fan out only for an ACTIVE channel — a suspended community would otherwise keep notifying subscribers")
	}
	// The pre-existing gate must survive the change.
	if !strings.Contains(q, "cm.role != 'banned'") {
		t.Fatal("the fan-out roster must still exclude banned members")
	}
	// The channel id has to stay qualified now that two tables are in play,
	// or the query is ambiguous and every fan-out errors.
	if !strings.Contains(q, "cm.channel_id = $1") {
		t.Fatalf("channel_id must be qualified to channel_members: %q", q)
	}
	if !strings.Contains(q, "cm.user_id") || !strings.Contains(q, "cm.notify_on") {
		t.Fatalf("the selected columns must be qualified: %q", q)
	}
}

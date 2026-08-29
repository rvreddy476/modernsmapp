package service

import "testing"

// CALL-LB-4: ringing/missed pushes for one call share a stable call-ID
// collapse key so at-least-once redelivery replaces rather than stacks.
func TestCallTypesCollapsePerCall(t *testing.T) {
	for _, typ := range []string{"incoming_call", "incoming_video_call", "missed_call"} {
		if got := GetCollapseKey(typ, "abc-call-id", "user-1"); got != "call:abc-call-id" {
			t.Fatalf("%s: collapse key = %q, want call:abc-call-id", typ, got)
		}
	}
}

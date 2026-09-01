package service

import "testing"

// Device-pass finding (2026-08-29): RINGING pushes must be NON-collapsible.
// FCM holds collapsible messages (max four distinct pending collapse keys
// per device, undefined beyond), and with a unique key per call, real rings
// stalled undelivered on healthy connected devices while identical
// non-collapsible sends arrived in seconds. The app's wake-up is idempotent
// and server-verified, so at-least-once duplicates are harmless — immediacy
// wins. missed_call keeps a per-call collapse key: it is an ordinary tray
// notification where replacement is correct.
func TestRingingPushesAreNonCollapsible(t *testing.T) {
	for _, typ := range []string{"incoming_call", "incoming_video_call"} {
		if got := GetCollapseKey(typ, "abc-call-id", "user-1"); got != "" {
			t.Fatalf("%s: collapse key %q — FCM would hold the ring instead of delivering it", typ, got)
		}
	}
	if got := GetCollapseKey("missed_call", "abc-call-id", "user-1"); got != "call:abc-call-id" {
		t.Fatalf("missed_call collapse key = %q, want call:abc-call-id", got)
	}
}

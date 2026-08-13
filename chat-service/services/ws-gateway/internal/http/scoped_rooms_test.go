package http

import "testing"

func TestBetaRejectsEveryClientSelectedRoomFrame(t *testing.T) {
	for _, messageType := range []string{
		"conversation.enter", "conversation.heartbeat", "conversation.leave",
		"typing.start", "subscribe_post", "unsubscribe_post", "subscribe_call",
		"unsubscribe_call", "subscribe_live_stream", "unsubscribe_live_stream",
		"subscribe_update", "unsubscribe_update", "subscribe_group_post",
		"unsubscribe_group_post", "group_post_typing", "call_offer",
		"ice_candidate", "call_join", "call_quality_report",
	} {
		if !isScopedRoomFrame(messageType) {
			t.Errorf("%q would bypass the beta room entitlement gate", messageType)
		}
	}
	if isScopedRoomFrame("unknown_non_room_frame") {
		t.Fatal("unrelated frame classified as a scoped room frame")
	}
}

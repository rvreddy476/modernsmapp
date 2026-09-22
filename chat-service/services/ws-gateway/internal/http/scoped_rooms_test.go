package http

import "testing"

// Every frame a client uses to select its own channel is classified as a
// scoped-room frame, so the beta gate can see it. Direct call signalling is
// classified too (it is in isScopedRoomFrame's union) — what differs is the
// gate's DECISION for it, tested below.
func TestEveryClientSelectedRoomFrameIsClassified(t *testing.T) {
	for _, messageType := range []string{
		"conversation.enter", "conversation.heartbeat", "conversation.leave",
		"typing.start", "subscribe_post", "unsubscribe_post", "subscribe_call",
		"unsubscribe_call", "subscribe_live_stream", "unsubscribe_live_stream",
		"subscribe_update", "unsubscribe_update", "subscribe_group_post",
		"unsubscribe_group_post", "group_post_typing", "call_offer",
		"ice_candidate", "call_join", "call_quality_report",
	} {
		if !isScopedRoomFrame(messageType) {
			t.Errorf("%q is not classified as a scoped room frame", messageType)
		}
	}
	if isScopedRoomFrame("unknown_non_room_frame") {
		t.Fatal("unrelated frame classified as a scoped room frame")
	}
}

// With scoped rooms disabled (public beta), client-selected room frames are
// refused — but direct call signalling passes the beta gate, because it is
// not room selection: it is relayed on chat:<target> and then checked
// against call-service's pair state, which is the stricter, owner-issued
// check. Until this was pinned, the beta gate swallowed every call_ring /
// call_offer / call_answer from every client before that check ever ran,
// and no call could reach its callee.
func TestBetaGateExemptsDirectSignallingOnly(t *testing.T) {
	s := &Server{opts: ServerOptions{EnableScopedRooms: false}}

	for _, direct := range []string{
		"call_ring", "call_offer", "call_answer", "ice_candidate",
		"call_accept", "call_decline", "call_busy", "call_end", "call_reject",
	} {
		if s.betaRoomGateRejects(direct) {
			t.Errorf("beta gate must not swallow direct signalling %q: callauth checks it", direct)
		}
	}

	for _, room := range []string{
		"subscribe_post", "subscribe_call", "subscribe_update", "subscribe_live_stream",
		"subscribe_group_post", "call_join", "call_leave", "call_quality_report",
		"conversation.enter", "conversation.heartbeat", "typing.start",
	} {
		if !s.betaRoomGateRejects(room) {
			t.Errorf("beta gate must still refuse client-selected room frame %q", room)
		}
	}

	if s.betaRoomGateRejects("unknown_non_room_frame") {
		t.Error("a frame that is not room selection must not be gated")
	}
}

func TestScopedRoomsEnabledGatesNothing(t *testing.T) {
	s := &Server{opts: ServerOptions{EnableScopedRooms: true}}
	for _, frame := range []string{"subscribe_call", "call_join", "call_ring", "conversation.heartbeat"} {
		if s.betaRoomGateRejects(frame) {
			t.Errorf("with scoped rooms enabled %q must pass the beta gate", frame)
		}
	}
}

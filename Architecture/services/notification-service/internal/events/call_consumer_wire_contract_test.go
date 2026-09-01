package events

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// The producer wire contract, as LITERALS — deliberately NOT built from any
// shared constant on either side.
//
// Device-pass finding (2026-08-29): call-service publishes envelopes through
// chat-service/shared/events with event_type "CallInvited"/"CallEnded";
// this module's events.EventCall* constants are the unrelated dotted strings
// ("call.invited"). The consumer matched its OWN constants, so every real
// call event silently hit the default branch and committed — no production
// ring push was ever sent — while every test stayed green because test
// envelopes were built from the same wrong-side constants. This test replays
// byte-literal producer envelopes so any future drift in either module
// breaks the gate instead of the product.

func literalProducerInvite(invitee uuid.UUID) kafka.Message {
	return kafka.Message{Offset: 42, Value: []byte(`{
		"event_id": "wire-evt-1",
		"event_type": "CallInvited",
		"occurred_at": "2026-08-29T10:00:00Z",
		"trace_id": "",
		"payload": {
			"call_id": "` + uuid.New().String() + `",
			"invite_id": "` + uuid.New().String() + `",
			"inviter_user_id": "` + uuid.New().String() + `",
			"invitee_user_id": "` + invitee.String() + `",
			"call_type": "direct_video",
			"created_at": "2026-08-29T10:00:00Z"
		}
	}`)}
}

func TestConsumerMatchesTheProducersLiteralWireEvents(t *testing.T) {
	invitee := uuid.New()
	source := &scriptedSource{messages: []kafka.Message{literalProducerInvite(invitee)}}
	notifier := &flakyNotifier{}
	cancel, done := startConsumer(t, source, &recordingQuarantine{}, notifier)
	defer cancel()

	waitFor(t, "the literal producer envelope to create a notification", func() bool {
		_, ids, _ := notifier.state()
		return len(ids) == 1
	})
	cancel()
	<-done

	_, ids, _ := notifier.state()
	if want := "call:wire-evt-1:" + invitee.String(); ids[0] != want {
		t.Fatalf("identity %q, want %q", ids[0], want)
	}
}

func TestConsumerMatchesTheLiteralCallEndedWireEvent(t *testing.T) {
	initiator := uuid.New()
	m := kafka.Message{Offset: 43, Value: []byte(`{
		"event_id": "wire-evt-2",
		"event_type": "CallEnded",
		"occurred_at": "2026-08-29T10:01:00Z",
		"payload": {
			"call_id": "` + uuid.New().String() + `",
			"initiator_user_id": "` + initiator.String() + `",
			"ended_by": "` + uuid.New().String() + `",
			"ended_reason": "missed",
			"duration_seconds": 0,
			"source_type": "profile",
			"ended_at": "2026-08-29T10:01:00Z"
		}
	}`)}
	source := &scriptedSource{messages: []kafka.Message{m}}
	notifier := &flakyNotifier{}
	cancel, done := startConsumer(t, source, &recordingQuarantine{}, notifier)
	defer cancel()

	waitFor(t, "the literal CallEnded envelope to create a missed-call row", func() bool {
		_, ids, _ := notifier.state()
		return len(ids) == 1
	})
	cancel()
	<-done

	_, ids, _ := notifier.state()
	if !strings.Contains(ids[0], "wire-evt-2") {
		t.Fatalf("missed-call identity %q not bound to the wire event", ids[0])
	}}

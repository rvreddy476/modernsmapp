package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

var (
	frqRequester = uuid.MustParse("00000000-0000-4000-8000-0000000fb001")
	frqPrivate   = uuid.MustParse("00000000-0000-4000-8000-0000000fb002")
	frqAt        = time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
)

type fakeFollowRequestNotifier struct {
	got []service.FollowRequestNotification
}

func (f *fakeFollowRequestNotifier) CreateFollowRequestNotification(_ context.Context, n service.FollowRequestNotification) error {
	f.got = append(f.got, n)
	return nil
}

// graphEnvelope builds the message exactly as graph-service's outbox relay
// writes it: the canonical envelope, event_id = outbox row id, plus pair_seq,
// keyed by the canonical pair.
func graphEnvelope(t *testing.T, eventType, eventID string, payload any) kafka.Message {
	t.Helper()
	p, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"event_id": eventID, "event_type": eventType, "version": 1,
		"actor_id": frqRequester.String(), "payload": json.RawMessage(p), "pair_seq": 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Key: []byte(frqRequester.String() + ":" + frqPrivate.String()), Value: body}
}

func TestFollowRequestedReachesThePrivateAccount(t *testing.T) {
	f := &fakeFollowRequestNotifier{}
	c := &Consumer{followRequestNotify: f}
	m := graphEnvelope(t, events.GraphFollowRequested, "row-1", events.FollowRequestedPayload{
		RequesterID: frqRequester.String(), TargetID: frqPrivate.String(), CreatedAt: frqAt,
	})
	if err := c.processMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 1 {
		t.Fatalf("got %d notices, want 1", len(f.got))
	}
	n := f.got[0]
	if n.Type != service.FollowRequestType || n.RecipientID != frqPrivate || n.ActorID != frqRequester ||
		n.DeepLink != "/notifications?tab=requests" || n.EventID != "row-1" || !n.CreatedAt.Equal(frqAt) {
		t.Fatalf("notice %+v", n)
	}
}

func TestFollowRequestAcceptedReachesTheRequester(t *testing.T) {
	f := &fakeFollowRequestNotifier{}
	c := &Consumer{followRequestNotify: f}
	m := graphEnvelope(t, events.GraphFollowRequestAccepted, "row-2", events.FollowRequestAcceptedPayload{
		RequesterID: frqRequester.String(), TargetID: frqPrivate.String(), AcceptedAt: frqAt,
	})
	if err := c.processMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if len(f.got) != 1 {
		t.Fatalf("got %d notices, want 1", len(f.got))
	}
	n := f.got[0]
	if n.Type != service.FollowRequestAcceptedType || n.RecipientID != frqRequester || n.ActorID != frqPrivate ||
		n.DeepLink != "/u/"+frqPrivate.String() || n.EventID != "row-2" {
		t.Fatalf("notice %+v", n)
	}
}

func TestFollowRequestIdentityIsPerEventNotPerPair(t *testing.T) {
	// Same pair (same Kafka key), two outbox rows: two identities. A
	// redelivery of one row keeps its identity.
	a := graphEnvelope(t, events.GraphFollowRequested, "row-a", events.FollowRequestedPayload{RequesterID: frqRequester.String(), TargetID: frqPrivate.String()})
	b := graphEnvelope(t, events.GraphFollowRequested, "row-b", events.FollowRequestedPayload{RequesterID: frqRequester.String(), TargetID: frqPrivate.String()})
	f := &fakeFollowRequestNotifier{}
	c := &Consumer{followRequestNotify: f}
	for _, m := range []kafka.Message{a, a, b} {
		if err := c.processMessage(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	if f.got[0].Identity() != f.got[1].Identity() {
		t.Fatal("a redelivery changed the identity")
	}
	if f.got[0].Identity() == f.got[2].Identity() {
		t.Fatal("two requests on one pair share an identity")
	}

	// No event id: the body hash, never the pair key.
	var env events.EventEnvelope
	_ = json.Unmarshal(a.Value, &env)
	env.EventID = ""
	if id := followRequestDedupID(env, a.Value); id == "" || id == string(a.Key) {
		t.Fatalf("fallback dedup id = %q", id)
	}
	if followRequestDedupID(env, a.Value) != followRequestDedupID(env, a.Value) {
		t.Fatal("fallback dedup id is not stable across a redelivery")
	}
	if followRequestDedupID(env, a.Value) == followRequestDedupID(env, b.Value) {
		t.Fatal("two different events share a fallback dedup id")
	}
}

func TestPlanFollowRequestNoticeRejectsUndeliverable(t *testing.T) {
	now := time.Now()
	for name, p := range map[string]events.FollowRequestedPayload{
		"self":          {RequesterID: frqPrivate.String(), TargetID: frqPrivate.String()},
		"bad requester": {RequesterID: "nope", TargetID: frqPrivate.String()},
		"no target":     {RequesterID: frqRequester.String()},
	} {
		_, ok, err := planFollowRequestNotice(events.GraphFollowRequested, "row", rawJSON(t, p), now)
		if err != nil || ok {
			t.Errorf("%s: ok=%v err=%v", name, ok, err)
		}
	}
	n, ok, err := planFollowRequestNotice(events.GraphFollowRequested, "row",
		rawJSON(t, events.FollowRequestedPayload{RequesterID: frqRequester.String(), TargetID: frqPrivate.String()}), now)
	if err != nil || !ok || !n.CreatedAt.Equal(now) {
		t.Fatalf("missing created_at should default to now: %+v ok=%v err=%v", n, ok, err)
	}
	if _, ok, _ := planFollowRequestNotice(events.UserFollowed, "row", rawJSON(t, map[string]string{}), now); ok {
		t.Fatal("an unrelated event planned a follow-request notice")
	}
}

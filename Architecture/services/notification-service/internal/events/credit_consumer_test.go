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
	crCredit   = uuid.MustParse("00000000-0000-4000-8000-0000000cd001")
	crPost     = uuid.MustParse("00000000-0000-4000-8000-0000000cd002")
	crUploader = uuid.MustParse("00000000-0000-4000-8000-0000000cd003")
	crTagged   = uuid.MustParse("00000000-0000-4000-8000-0000000cd004")
	crAt       = time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
)

type fakeCreditNotifier struct{ got []service.CreditNotification }

func (f *fakeCreditNotifier) CreateCreditNotification(_ context.Context, n service.CreditNotification) error {
	f.got = append(f.got, n)
	return nil
}

func creditRequested() events.PostCreditRequestedPayload {
	return events.PostCreditRequestedPayload{
		CreditID: crCredit.String(), PostID: crPost.String(), UploaderID: crUploader.String(),
		TaggedUserID: crTagged.String(), Name: "Asha Rao", Role: "Director", PostTitle: "Monsoon Letters",
		UploaderName: "Blue Hour Films", CreatedAt: crAt,
	}
}

func creditAccepted() events.PostCreditAcceptedPayload {
	return events.PostCreditAcceptedPayload{
		CreditID: crCredit.String(), PostID: crPost.String(), UploaderID: crUploader.String(),
		TaggedUserID: crTagged.String(), Name: "Asha Rao", Role: "Director", PostTitle: "Monsoon Letters",
		AcceptedAt: crAt,
	}
}

func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPlanCreditNotice(t *testing.T) {
	now := time.Now()
	n, ok, err := planCreditNotice(events.EventPostCreditRequested, rawJSON(t, creditRequested()), now)
	if err != nil || !ok {
		t.Fatalf("request: ok=%v err=%v", ok, err)
	}
	if n.Type != service.CreditTypeRequest || n.RecipientID != crTagged || n.ActorID != crUploader ||
		n.CreditID != crCredit || n.PostID != crPost || n.DeepLink != "/credits/requests" ||
		n.UploaderName != "Blue Hour Films" || n.Role != "Director" || !n.CreatedAt.Equal(crAt) {
		t.Fatalf("request notice = %+v", n)
	}
	if n.Identity() != crCredit.String()+":"+crTagged.String()+":credit_request" {
		t.Fatalf("request identity %q", n.Identity())
	}

	a, ok, err := planCreditNotice(events.EventPostCreditAccepted, rawJSON(t, creditAccepted()), now)
	if err != nil || !ok {
		t.Fatalf("accepted: ok=%v err=%v", ok, err)
	}
	if a.Type != service.CreditTypeAccepted || a.RecipientID != crUploader || a.ActorID != crTagged ||
		a.DeepLink != "/tube/watch/"+crPost.String() || a.Name != "Asha Rao" || !a.CreatedAt.Equal(crAt) {
		t.Fatalf("accepted notice = %+v", a)
	}
	if a.Identity() != crCredit.String()+":"+crUploader.String()+":credit_accepted" {
		t.Fatalf("accepted identity %q", a.Identity())
	}

	// A zero timestamp falls back to now.
	p := creditRequested()
	p.CreatedAt = time.Time{}
	if n, _, _ := planCreditNotice(events.EventPostCreditRequested, rawJSON(t, p), now); !n.CreatedAt.Equal(now) {
		t.Fatalf("created_at fallback = %v", n.CreatedAt)
	}
}

func TestPlanCreditNoticeSkipsSelfAndBadIDs(t *testing.T) {
	self := creditRequested()
	self.TaggedUserID = self.UploaderID
	selfAccept := creditAccepted()
	selfAccept.TaggedUserID = selfAccept.UploaderID
	cases := map[string]struct {
		event string
		body  any
	}{
		"self request":         {events.EventPostCreditRequested, self},
		"self accept":          {events.EventPostCreditAccepted, selfAccept},
		"bad credit id":        {events.EventPostCreditRequested, func() any { p := creditRequested(); p.CreditID = "x"; return p }()},
		"bad post id":          {events.EventPostCreditAccepted, func() any { p := creditAccepted(); p.PostID = ""; return p }()},
		"bad recipient":        {events.EventPostCreditRequested, func() any { p := creditRequested(); p.TaggedUserID = "nope"; return p }()},
		"nil actor":            {events.EventPostCreditAccepted, func() any { p := creditAccepted(); p.TaggedUserID = uuid.Nil.String(); return p }()},
		"unrelated event type": {events.PostCreated, creditRequested()},
	}
	for name, c := range cases {
		if _, ok, err := planCreditNotice(c.event, rawJSON(t, c.body), time.Now()); ok || err != nil {
			t.Errorf("%s: ok=%v err=%v, want skipped", name, ok, err)
		}
	}
}

// The envelope switch routes both events to the notifier, and a self-credit
// reaches nobody.
func TestConsumerRoutesCreditEvents(t *testing.T) {
	f := &fakeCreditNotifier{}
	c := &Consumer{creditNotify: f}
	send := func(eventType string, payload any) {
		t.Helper()
		env := events.EventEnvelope{EventID: uuid.NewString(), EventType: eventType, OccurredAt: crAt, Payload: rawJSON(t, payload)}
		if err := c.processMessage(context.Background(), kafka.Message{Value: rawJSON(t, env)}); err != nil {
			t.Fatalf("%s: %v", eventType, err)
		}
	}
	send(events.EventPostCreditRequested, creditRequested())
	send(events.EventPostCreditAccepted, creditAccepted())
	self := creditRequested()
	self.TaggedUserID = self.UploaderID
	send(events.EventPostCreditRequested, self)
	if len(f.got) != 2 {
		t.Fatalf("notices = %d, want 2: %+v", len(f.got), f.got)
	}
	if f.got[0].Type != service.CreditTypeRequest || f.got[0].RecipientID != crTagged {
		t.Errorf("first = %+v", f.got[0])
	}
	if f.got[1].Type != service.CreditTypeAccepted || f.got[1].RecipientID != crUploader {
		t.Errorf("second = %+v", f.got[1])
	}
}

package events

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type fakeCopyrightNotifier struct {
	got []service.CopyrightReviewNotification
}

func (f *fakeCopyrightNotifier) CreateCopyrightReviewNotification(_ context.Context, n service.CopyrightReviewNotification) error {
	f.got = append(f.got, n)
	return nil
}

func copyrightReview(state string) events.PostCopyrightReviewPayload {
	return events.PostCopyrightReviewPayload{
		ReviewID: "00000000-0000-4000-8000-00000000c001", PostID: crPost.String(), AuthorID: crUploader.String(),
		State: state, MatchedTitle: "Jailer 2", PostTitle: "Jailer 2 full movie", ContentType: "long_video",
		CreatedAt: crAt, OccurredAt: crAt, DecidedBy: uuid.NewString(),
	}
}

func TestPlanCopyrightNotice(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		state, typ, link string
	}{
		{events.CopyrightReviewPending, service.CopyrightTypeHold, "/posttube/hub"},
		{events.CopyrightReviewCleared, service.CopyrightTypeCleared, "/tube/watch/" + crPost.String()},
		{events.CopyrightReviewBlocked, service.CopyrightTypeBlocked, "/posttube/hub"},
	} {
		n, ok, err := planCopyrightNotice(rawJSON(t, copyrightReview(c.state)), now)
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", c.state, ok, err)
		}
		if n.Type != c.typ || n.DeepLink != c.link || n.RecipientID != crUploader || n.PostID != crPost ||
			n.MatchedTitle != "Jailer 2" || !n.CreatedAt.Equal(crAt) {
			t.Fatalf("%s notice = %+v", c.state, n)
		}
		if n.Identity() != "00000000-0000-4000-8000-00000000c001:"+c.typ {
			t.Fatalf("%s identity %q", c.state, n.Identity())
		}
	}
	for name, body := range map[string]events.PostCopyrightReviewPayload{
		"unknown state": copyrightReview("escalated"),
		"bad author":    func() events.PostCopyrightReviewPayload { p := copyrightReview("blocked"); p.AuthorID = "x"; return p }(),
		"nil review": func() events.PostCopyrightReviewPayload {
			p := copyrightReview("blocked")
			p.ReviewID = uuid.Nil.String()
			return p
		}(),
		"no post": func() events.PostCopyrightReviewPayload { p := copyrightReview("pending"); p.PostID = ""; return p }(),
	} {
		if _, ok, err := planCopyrightNotice(rawJSON(t, body), now); ok || err != nil {
			t.Errorf("%s: ok=%v err=%v, want skipped", name, ok, err)
		}
	}
}

// The envelope switch routes the review event to the author's notifier.
func TestConsumerRoutesCopyrightReviewEvents(t *testing.T) {
	f := &fakeCopyrightNotifier{}
	c := &Consumer{copyrightNotify: f}
	for _, state := range []string{"pending", "blocked"} {
		env := events.EventEnvelope{EventID: uuid.NewString(), EventType: events.EventPostCopyrightReview, OccurredAt: crAt,
			Payload: rawJSON(t, copyrightReview(state))}
		if err := c.processMessage(context.Background(), kafka.Message{Value: rawJSON(t, env)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.got) != 2 || f.got[0].Type != service.CopyrightTypeHold || f.got[1].Type != service.CopyrightTypeBlocked {
		t.Fatalf("notices = %+v", f.got)
	}
}

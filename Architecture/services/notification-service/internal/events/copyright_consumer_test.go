package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type fakeCopyrightNotifier struct {
	got  []service.CopyrightReviewNotification
	fail int // the first fail deliveries return an error
}

func (f *fakeCopyrightNotifier) CreateCopyrightReviewNotification(_ context.Context, n service.CopyrightReviewNotification) error {
	f.got = append(f.got, n)
	if f.fail > 0 {
		f.fail--
		return errors.New("postgres down")
	}
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

// The owner and the system's reason ride through to the notice (the email
// names the owner and explains an owner-registered block).
func TestPlanCopyrightNotice_OwnerAndReason(t *testing.T) {
	p := copyrightReview(events.CopyrightReviewBlocked)
	p.RightsOwner, p.Reason, p.DecidedBy = "Sun Pictures", events.CopyrightReasonOwnerRegistered, ""
	n, ok, err := planCopyrightNotice(rawJSON(t, p), time.Now())
	if err != nil || !ok || n.RightsOwner != "Sun Pictures" || n.Reason != events.CopyrightReasonOwnerRegistered || n.Type != service.CopyrightTypeBlocked {
		t.Fatalf("notice = %+v ok=%v err=%v", n, ok, err)
	}
}

// An appeal (post-service 076): the author's own request back to a reviewer
// is no notice; the reviewer's answer is one, keyed apart from the block it
// reopened so the inbox write and the email are not deduped away.
func TestPlanCopyrightNotice_Appeal(t *testing.T) {
	now := time.Now()
	p := copyrightReview(events.CopyrightReviewPending)
	p.Appeal = true
	if _, ok, err := planCopyrightNotice(rawJSON(t, p), now); ok || err != nil {
		t.Fatalf("appeal sent back to review: ok=%v err=%v, want no notice", ok, err)
	}
	for _, c := range []struct{ state, typ string }{
		{events.CopyrightReviewBlocked, service.CopyrightTypeBlocked},
		{events.CopyrightReviewCleared, service.CopyrightTypeCleared},
	} {
		p := copyrightReview(c.state)
		p.Appeal = true
		n, ok, err := planCopyrightNotice(rawJSON(t, p), now)
		if err != nil || !ok || !n.Appeal || n.Type != c.typ {
			t.Fatalf("%s answer: %+v ok=%v err=%v", c.state, n, ok, err)
		}
		if n.Identity() != "00000000-0000-4000-8000-00000000c001:"+c.typ+":appeal" {
			t.Fatalf("%s answer identity %q", c.state, n.Identity())
		}
	}
}

// The review event is committed only after its notice and email were
// written: a failed write is retried on the read loop, not lost (it used to
// go to the worker pool, committed when queued). An undecodable payload is
// skipped so it cannot hold the topic.
func TestCopyrightReviewEvent_CommittedOnlyAfterDelivery(t *testing.T) {
	if !commitsAfterProcessing(events.EventPostCopyrightReview) {
		t.Fatal("post.copyright_review.changed is committed before it is handled")
	}
	if commitsAfterProcessing(events.PostCreated) {
		t.Fatal("PostCreated moved onto the read loop")
	}
	f := &fakeCopyrightNotifier{fail: 1}
	c := &Consumer{copyrightNotify: f}
	env := events.EventEnvelope{EventID: uuid.NewString(), EventType: events.EventPostCopyrightReview, OccurredAt: crAt,
		Payload: rawJSON(t, copyrightReview(events.CopyrightReviewBlocked))}
	r := &copyrightCommitRecorder{}
	if !processAndCommit(context.Background(), r, kafka.Message{Value: rawJSON(t, env)}, c.processMessage) {
		t.Fatal("not committed")
	}
	if len(f.got) != 2 || r.commits != 1 {
		t.Fatalf("deliveries=%d commits=%d, want the failure retried then one commit", len(f.got), r.commits)
	}
	bad := events.EventEnvelope{EventID: uuid.NewString(), EventType: events.EventPostCopyrightReview, OccurredAt: crAt,
		Payload: []byte(`{"state": 7}`)}
	if err := c.processMessage(context.Background(), kafka.Message{Value: rawJSON(t, bad)}); err != nil {
		t.Fatalf("undecodable payload: %v, want skipped", err)
	}
}

type copyrightCommitRecorder struct{ commits int }

func (r *copyrightCommitRecorder) CommitMessages(context.Context, ...kafka.Message) error {
	r.commits++
	return nil
}

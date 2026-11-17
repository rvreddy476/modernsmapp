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

// reels-29 / REG-6: post.author_notice becomes the author's notice, keyed on
// notice_id, committed only once delivered.

type fakeAuthorNotifier struct {
	got  []service.AuthorNotice
	fail int
}

func (f *fakeAuthorNotifier) CreateAuthorNotice(_ context.Context, n service.AuthorNotice) error {
	f.got = append(f.got, n)
	if f.fail > 0 {
		f.fail--
		return errors.New("scylla down")
	}
	return nil
}

var (
	anPost   = uuid.MustParse("00000000-0000-4000-8000-0000000a0001")
	anAuthor = uuid.MustParse("00000000-0000-4000-8000-0000000a0002")
	anNotice = uuid.MustParse("00000000-0000-4000-8000-0000000a0003")
	anAt     = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
)

// authorNotice is the payload exactly as post-service writes it.
func authorNotice(kind string) postAuthorNoticePayload {
	return postAuthorNoticePayload{
		NoticeID: anNotice.String(), Kind: kind, PostID: anPost.String(), AuthorID: anAuthor.String(),
		ContentType: "flick", Reason: "spam reel removed", DecisionID: anNotice.String(), Source: "report",
		SourceRefID: anNotice.String(), Appealable: kind == "moderation_takedown", OccurredAt: anAt,
	}
}

func TestPlanAuthorNotice(t *testing.T) {
	now := time.Now()
	for kind, typ := range map[string]string{
		"moderation_takedown":    service.AuthorNoticeTypeTakedown,
		"moderation_restored":    service.AuthorNoticeTypeRestored,
		"scheduled_video_failed": service.AuthorNoticeTypeScheduledFailed,
	} {
		n, ok, err := planAuthorNotice(rawJSON(t, authorNotice(kind)), now)
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", kind, ok, err)
		}
		if n.Type != typ || n.RecipientID != anAuthor || n.PostID != anPost || n.NoticeID != anNotice ||
			n.Reason != "spam reel removed" || n.ContentType != "flick" || !n.CreatedAt.Equal(anAt) ||
			n.DeepLink != "/post/"+anPost.String() || n.Appealable != (kind == "moderation_takedown") {
			t.Fatalf("%s notice = %+v", kind, n)
		}
		if n.Identity() != "author_notice:"+anNotice.String()+":"+typ {
			t.Fatalf("%s identity %q", kind, n.Identity())
		}
	}
	for name, p := range map[string]postAuthorNoticePayload{
		"unknown kind": authorNotice("moderation_escalated"),
		"bad author":   func() postAuthorNoticePayload { p := authorNotice("moderation_takedown"); p.AuthorID = "x"; return p }(),
		"nil notice": func() postAuthorNoticePayload {
			p := authorNotice("moderation_takedown")
			p.NoticeID = uuid.Nil.String()
			return p
		}(),
		"no post": func() postAuthorNoticePayload { p := authorNotice("moderation_takedown"); p.PostID = ""; return p }(),
	} {
		if _, ok, err := planAuthorNotice(rawJSON(t, p), now); ok || err != nil {
			t.Errorf("%s: ok=%v err=%v, want skipped", name, ok, err)
		}
	}
}

// The envelope switch routes the event to the author's notifier, and the
// event is committed only after delivery: a failure is retried, not lost.
func TestAuthorNoticeEvent_RoutedAndCommittedOnlyAfterDelivery(t *testing.T) {
	if !commitsAfterProcessing(EventPostAuthorNotice) {
		t.Fatal("post.author_notice is committed before it is handled")
	}
	f := &fakeAuthorNotifier{fail: 1}
	c := &Consumer{authorNotify: f}
	env := events.EventEnvelope{EventID: uuid.NewString(), EventType: EventPostAuthorNotice, OccurredAt: anAt,
		Payload: rawJSON(t, authorNotice("moderation_takedown"))}
	r := &copyrightCommitRecorder{}
	if !processAndCommit(context.Background(), r, kafka.Message{Value: rawJSON(t, env)}, c.processMessage) {
		t.Fatal("not committed")
	}
	if len(f.got) != 2 || r.commits != 1 || f.got[1].Type != service.AuthorNoticeTypeTakedown {
		t.Fatalf("deliveries=%+v commits=%d, want the failure retried then one commit", f.got, r.commits)
	}
	bad := events.EventEnvelope{EventID: uuid.NewString(), EventType: EventPostAuthorNotice, OccurredAt: anAt,
		Payload: []byte(`{"kind": 7}`)}
	if err := c.processMessage(context.Background(), kafka.Message{Value: rawJSON(t, bad)}); err != nil {
		t.Fatalf("undecodable payload: %v, want skipped", err)
	}
}

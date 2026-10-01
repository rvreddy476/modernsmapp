package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/postgres"
	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// fakeLiveFanout is the upload recorder plus the two audience probes.
type fakeLiveFanout struct {
	fakeFanout
	reminders, subscribers bool
	remErr, subErr         error
	probedStream           uuid.UUID
	probedChannel          uuid.UUID
	resolvedFor            uuid.UUID
}

func (f *fakeLiveFanout) ResolveChannel(_ context.Context, owner uuid.UUID) uuid.UUID {
	f.resolvedFor = owner
	return f.channel
}

func (f *fakeLiveFanout) HasReminders(_ context.Context, stream uuid.UUID) (bool, error) {
	f.probedStream = stream
	return f.reminders, f.remErr
}

func (f *fakeLiveFanout) HasSubscribers(_ context.Context, channel uuid.UUID) (bool, error) {
	f.probedChannel = channel
	// As the real probe: a creator without a channel has no subscribers.
	if channel == uuid.Nil {
		return false, f.subErr
	}
	return f.subscribers, f.subErr
}

// Which streams become a job, and what the job says.
func TestEnqueueLiveFanout_Gating(t *testing.T) {
	streamID, creatorID, channelID := uuid.New(), uuid.New(), uuid.New()
	started := time.Now().UTC().Truncate(time.Second)
	base := func() sharedevents.LiveStreamStartedPayload {
		return sharedevents.LiveStreamStartedPayload{
			StreamID: streamID.String(), CreatorID: creatorID.String(), CreatorUserID: creatorID.String(),
			Title: "Friday Q&A", Visibility: "public", StartedAt: started, Orientation: "landscape",
		}
	}
	wide := "/posttube/live/" + streamID.String()
	tall := "/reels/live/" + streamID.String()

	cases := []struct {
		name        string
		mutate      func(*sharedevents.LiveStreamStartedPayload)
		channel     uuid.UUID
		reminders   bool
		subscribers bool
		remErr      error
		subErr      error
		wantJob     bool
		wantLink    string
		wantChannel uuid.UUID
	}{
		{name: "public, reminders and subscribers", channel: channelID, reminders: true, subscribers: true,
			wantJob: true, wantLink: wide, wantChannel: channelID},
		{name: "followers-visible", mutate: func(e *sharedevents.LiveStreamStartedPayload) { e.Visibility = "followers" },
			channel: channelID, subscribers: true, wantJob: true, wantLink: wide, wantChannel: channelID},
		{name: "private", mutate: func(e *sharedevents.LiveStreamStartedPayload) { e.Visibility = "private" },
			channel: channelID, reminders: true, subscribers: true},
		{name: "paid", mutate: func(e *sharedevents.LiveStreamStartedPayload) { e.Visibility = "paid" },
			channel: channelID, reminders: true, subscribers: true},
		{name: "unlisted", mutate: func(e *sharedevents.LiveStreamStartedPayload) { e.Visibility = "unlisted" },
			channel: channelID, reminders: true, subscribers: true},
		{name: "visibility missing", mutate: func(e *sharedevents.LiveStreamStartedPayload) { e.Visibility = "" },
			channel: channelID, reminders: true, subscribers: true},
		{name: "no reminders and no subscribers", channel: channelID},
		{name: "no reminders, no channel at all"},
		{name: "reminders only, creator has no channel", reminders: true,
			wantJob: true, wantLink: wide, wantChannel: uuid.Nil},
		{name: "subscribers only", channel: channelID, subscribers: true,
			wantJob: true, wantLink: wide, wantChannel: channelID},
		{name: "reminder probe failed: enqueue so the worker retries", channel: channelID,
			remErr: errors.New("live-service-v2 down"), wantJob: true, wantLink: wide, wantChannel: channelID},
		{name: "subscriber probe failed: enqueue so the worker retries", channel: channelID,
			subErr: errors.New("post-service down"), wantJob: true, wantLink: wide, wantChannel: channelID},
		{name: "portrait opens the reels room", mutate: func(e *sharedevents.LiveStreamStartedPayload) { e.Orientation = "portrait" },
			channel: channelID, reminders: true, wantJob: true, wantLink: tall, wantChannel: channelID},
		{name: "orientation missing is landscape", mutate: func(e *sharedevents.LiveStreamStartedPayload) { e.Orientation = "" },
			channel: channelID, reminders: true, wantJob: true, wantLink: wide, wantChannel: channelID},
		{name: "older event without creator_user_id", mutate: func(e *sharedevents.LiveStreamStartedPayload) { e.CreatorUserID = "" },
			channel: channelID, reminders: true, wantJob: true, wantLink: wide, wantChannel: channelID},
		{name: "malformed stream id", mutate: func(e *sharedevents.LiveStreamStartedPayload) { e.StreamID = "nope" },
			channel: channelID, reminders: true, subscribers: true},
		{name: "malformed creator id", mutate: func(e *sharedevents.LiveStreamStartedPayload) {
			e.CreatorID, e.CreatorUserID = "nope", ""
		}, channel: channelID, reminders: true, subscribers: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base()
			if c.mutate != nil {
				c.mutate(&e)
			}
			f := &fakeLiveFanout{
				fakeFanout: fakeFanout{channel: c.channel},
				reminders:  c.reminders, subscribers: c.subscribers, remErr: c.remErr, subErr: c.subErr,
			}
			consumer := &Consumer{fanout: f}
			if err := consumer.enqueueLiveFanout(context.Background(), e); err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			if !c.wantJob {
				if len(f.enqueued) != 0 {
					t.Fatalf("expected no job, got %+v", f.enqueued)
				}
				return
			}
			if len(f.enqueued) != 1 {
				t.Fatalf("expected one job, got %d", len(f.enqueued))
			}
			p := f.enqueued[0]
			// The job is keyed on the stream; the creator is the author
			// (which is what excludes them at delivery).
			if p.PostID != streamID || p.AuthorID != creatorID || p.ChannelID != c.wantChannel {
				t.Fatalf("ids: %+v", p)
			}
			if f.resolvedFor != creatorID || f.probedStream != streamID || f.probedChannel != c.wantChannel {
				t.Fatalf("lookups: channel for %s, reminders of %s, subscribers of %s", f.resolvedFor, f.probedStream, f.probedChannel)
			}
			if p.NotifType != "creator_went_live" || p.ContentType != service.LiveContentType {
				t.Fatalf("type = %q/%q", p.NotifType, p.ContentType)
			}
			if p.DeepLink != c.wantLink {
				t.Fatalf("deep link = %q, want %q", p.DeepLink, c.wantLink)
			}
			if p.Phase != postgres.FanoutPhaseReminders {
				t.Fatalf("phase = %q, want reminders first", p.Phase)
			}
			if p.Title != "Friday Q&A" || p.Visibility != e.Visibility || !p.CreatedAt.Equal(started) {
				t.Fatalf("render inputs: %+v", p)
			}
		})
	}
}

// A started_at the producer left zero would make the job look hours old
// and be dropped as stale; it is stamped with now instead.
func TestEnqueueLiveFanout_ZeroStartedAtIsNow(t *testing.T) {
	f := &fakeLiveFanout{fakeFanout: fakeFanout{channel: uuid.New()}, subscribers: true}
	c := &Consumer{fanout: f}
	err := c.enqueueLiveFanout(context.Background(), sharedevents.LiveStreamStartedPayload{
		StreamID: uuid.NewString(), CreatorID: uuid.NewString(), Visibility: "public",
	})
	if err != nil || len(f.enqueued) != 1 {
		t.Fatalf("enqueue: %v, jobs %d", err, len(f.enqueued))
	}
	if age := time.Since(f.enqueued[0].CreatedAt); age < 0 || age > time.Minute {
		t.Fatalf("created_at = %v", f.enqueued[0].CreatedAt)
	}
}

// A consumer with no fan-out attached must be a silent no-op (and must not
// fall back to followers: there is no follower path left to fall back to).
func TestEnqueueLiveFanout_NoPipelineIsNoop(t *testing.T) {
	c := &Consumer{}
	err := c.enqueueLiveFanout(context.Background(), sharedevents.LiveStreamStartedPayload{
		StreamID: uuid.NewString(), CreatorID: uuid.NewString(), Visibility: "public",
	})
	if err != nil {
		t.Fatalf("expected no-op, got %v", err)
	}
}

// The bytes live-service-v2's outbox writes (shared envelope around the
// shared payload) reach the durable job through processMessage, and an
// enqueue failure is returned rather than swallowed.
func TestProcessMessage_LiveStreamStartedEnqueuesJob(t *testing.T) {
	streamID, creatorID, channelID := uuid.New(), uuid.New(), uuid.New()
	payload, _ := json.Marshal(sharedevents.LiveStreamStartedPayload{
		StreamID: streamID.String(), CreatorID: creatorID.String(), CreatorUserID: creatorID.String(),
		Title: "Friday Q&A", Visibility: "public", StartedAt: time.Now().UTC(),
		Orientation: "portrait", Category: "gaming",
	})
	actor := creatorID.String()
	env := sharedevents.NewEnvelope(context.Background(), sharedevents.LiveStreamStarted, &actor, payload)
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}

	f := &fakeLiveFanout{fakeFanout: fakeFanout{channel: channelID}, reminders: true}
	c := &Consumer{fanout: f}
	if err := c.processMessage(context.Background(), kafka.Message{Value: raw}); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	if len(f.enqueued) != 1 {
		t.Fatalf("jobs = %d, want 1", len(f.enqueued))
	}
	p := f.enqueued[0]
	if p.PostID != streamID || p.AuthorID != creatorID || p.DeepLink != "/reels/live/"+streamID.String() {
		t.Fatalf("job: %+v", p)
	}
}

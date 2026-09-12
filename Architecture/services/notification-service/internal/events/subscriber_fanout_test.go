package events

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
)

type fakeFanout struct {
	channel  uuid.UUID
	enqueued []service.EnqueueParams
}

func (f *fakeFanout) Enqueue(_ context.Context, p service.EnqueueParams) error {
	f.enqueued = append(f.enqueued, p)
	return nil
}

func (f *fakeFanout) ResolveChannel(context.Context, uuid.UUID) uuid.UUID { return f.channel }

func boolp(b bool) *bool { return &b }

// Enqueue gating: only public video/flick uploads on a channel, with the
// creator's notify_subscribers switch left on, become a job. Everything
// else is silently skipped, never retried, never fanned out to followers.
func TestEnqueueSubscriberFanout_Gating(t *testing.T) {
	postID, authorID, channelID := uuid.New(), uuid.New(), uuid.New()
	base := func() sharedevents.PostCreatedPayload {
		return sharedevents.PostCreatedPayload{
			PostID: postID.String(), AuthorID: authorID.String(),
			ContentType: "long_video", Visibility: "public",
			ChannelID: channelID.String(), ChannelName: "Cal B", Title: "My video",
			CreatedAt: time.Now(),
		}
	}
	cases := []struct {
		name     string
		mutate   func(*sharedevents.PostCreatedPayload)
		resolved uuid.UUID
		wantJob  bool
		wantLink string
		wantType string
	}{
		{name: "public long_video", mutate: func(*sharedevents.PostCreatedPayload) {},
			wantJob: true, wantLink: "/tube/watch/" + postID.String(), wantType: "creator_uploaded_video"},
		{name: "public video alias", mutate: func(e *sharedevents.PostCreatedPayload) { e.ContentType = "video" },
			wantJob: true, wantLink: "/tube/watch/" + postID.String(), wantType: "creator_uploaded_video"},
		{name: "flick", mutate: func(e *sharedevents.PostCreatedPayload) { e.ContentType = "flick" },
			wantJob: true, wantLink: "/reels/" + postID.String(), wantType: "creator_uploaded_flick"},
		{name: "reel alias", mutate: func(e *sharedevents.PostCreatedPayload) { e.ContentType = "reel" },
			wantJob: true, wantLink: "/reels/" + postID.String(), wantType: "creator_uploaded_flick"},
		{name: "text post", mutate: func(e *sharedevents.PostCreatedPayload) { e.ContentType = "post" }},
		{name: "photo", mutate: func(e *sharedevents.PostCreatedPayload) { e.ContentType = "photo" }},
		{name: "poll", mutate: func(e *sharedevents.PostCreatedPayload) { e.ContentType = "poll" }},
		{name: "unlisted", mutate: func(e *sharedevents.PostCreatedPayload) { e.Visibility = "unlisted" }},
		{name: "private", mutate: func(e *sharedevents.PostCreatedPayload) { e.Visibility = "private" }},
		{name: "creator opted out", mutate: func(e *sharedevents.PostCreatedPayload) { e.NotifySubscribers = boolp(false) }},
		{name: "creator opted in explicitly", mutate: func(e *sharedevents.PostCreatedPayload) { e.NotifySubscribers = boolp(true) },
			wantJob: true, wantLink: "/tube/watch/" + postID.String(), wantType: "creator_uploaded_video"},
		{name: "no channel on event, none resolved", mutate: func(e *sharedevents.PostCreatedPayload) { e.ChannelID = "" }},
		{name: "no channel on event, resolved by lookup", mutate: func(e *sharedevents.PostCreatedPayload) { e.ChannelID = "" },
			resolved: channelID, wantJob: true, wantLink: "/tube/watch/" + postID.String(), wantType: "creator_uploaded_video"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base()
			c.mutate(&e)
			f := &fakeFanout{channel: c.resolved}
			consumer := &Consumer{fanout: f}
			if err := consumer.enqueueSubscriberFanout(context.Background(), e); err != nil {
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
			if p.PostID != postID || p.AuthorID != authorID || p.ChannelID != channelID {
				t.Fatalf("ids: %+v", p)
			}
			if p.DeepLink != c.wantLink || p.NotifType != c.wantType {
				t.Fatalf("link/type = %q/%q, want %q/%q", p.DeepLink, p.NotifType, c.wantLink, c.wantType)
			}
			if p.Title != "My video" || p.ChannelName != "Cal B" {
				t.Fatalf("title/channel not carried: %+v", p)
			}
		})
	}
}

// A consumer with no fan-out attached must be a silent no-op.
func TestEnqueueSubscriberFanout_NoPipelineIsNoop(t *testing.T) {
	c := &Consumer{}
	err := c.enqueueSubscriberFanout(context.Background(), sharedevents.PostCreatedPayload{
		PostID: uuid.NewString(), AuthorID: uuid.NewString(), ContentType: "long_video", Visibility: "public",
	})
	if err != nil {
		t.Fatalf("expected no-op, got %v", err)
	}
}

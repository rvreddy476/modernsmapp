package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// feed-07 leftover: the direct (non-outbox) publishes about one post go out
// keyed by the post, through the key-hashing writer the outbox relay uses,
// so they share the post's partition with its outbox events. Publishes about
// a user keep the writer and key they had.

type sentDirect struct {
	writer *kafka.Writer
	msg    kafka.Message
}

func capturingProducer(t *testing.T) (*Producer, *[]sentDirect) {
	t.Helper()
	p := NewProducer([]string{"127.0.0.1:1"}, "post.events.test")
	t.Cleanup(func() { _ = p.Close() })
	var sent []sentDirect
	p.send = func(_ context.Context, w *kafka.Writer, msg kafka.Message) error {
		sent = append(sent, sentDirect{writer: w, msg: msg})
		return nil
	}
	return p, &sent
}

func TestDirectPublish_PostScopedEventsAreKeyedByThePostOnTheHashWriter(t *testing.T) {
	ctx := context.Background()
	post, actor, other := uuid.New(), uuid.New(), uuid.New()

	cases := []struct {
		name      string
		eventType string
		wantActor bool
		publish   func(p *Producer) error
	}{
		{"PostCreated (crosspost embed)", events.PostCreated, true, func(p *Producer) error {
			return p.PublishPostCreated(ctx, post, actor, "t", "public", "post", "approved", 0, time.Now())
		}},
		{"PostContentTypeChanged", events.PostContentTypeChanged, true, func(p *Producer) error {
			return p.PublishPostContentTypeChanged(ctx, post, actor, "long_video", "flick")
		}},
		{"PostReacted", events.PostReacted, true, func(p *Producer) error {
			return p.PublishPostReacted(ctx, post, other, actor, "like")
		}},
		{"CommentCreated (reply)", events.CommentCreated, true, func(p *Producer) error {
			return p.PublishCommentCreated(ctx, uuid.New(), post, other, actor, "hi")
		}},
		{"UploadDeleted", events.UploadDeleted, true, func(p *Producer) error {
			return p.PublishUploadDeleted(ctx, post, actor, "long_video")
		}},
		{"CrosspostRemoved (keyed by the embed post)", events.CrosspostRemoved, false, func(p *Producer) error {
			return p.PublishCrosspostRemoved(ctx, uuid.New(), other, "reels", post)
		}},
		{"PostReposted (keyed by the original post)", events.EventPostReposted, true, func(p *Producer) error {
			return p.PublishPostReposted(ctx, uuid.New(), actor, post, other, "repost", "", "public", "", "")
		}},
		{"PostRepostUndone (keyed by the original post)", events.EventPostRepostUndone, true, func(p *Producer) error {
			return p.PublishPostRepostUndone(ctx, uuid.New(), actor, post, other, "repost")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, sent := capturingProducer(t)
			if err := c.publish(p); err != nil {
				t.Fatal(err)
			}
			if len(*sent) != 1 {
				t.Fatalf("%d messages written, want 1", len(*sent))
			}
			got := (*sent)[0]
			if got.writer != p.relay {
				t.Fatal("written through the key-ignoring writer; a post's events must go through the hash writer")
			}
			if string(got.msg.Key) != post.String() {
				t.Fatalf("key = %q, want the post id %s", got.msg.Key, post)
			}
			var env events.EventEnvelope
			if err := json.Unmarshal(got.msg.Value, &env); err != nil {
				t.Fatal(err)
			}
			if env.EventType != c.eventType {
				t.Fatalf("event type = %q, want %q", env.EventType, c.eventType)
			}
			// The envelope is what consumers read; only the key moved.
			if c.wantActor && (env.ActorUserID == nil || *env.ActorUserID != actor.String()) {
				t.Fatalf("envelope actor = %v, want %s", env.ActorUserID, actor)
			}
			if !c.wantActor && env.ActorUserID != nil {
				t.Fatalf("envelope actor = %v, want none", *env.ActorUserID)
			}
		})
	}
}

// A repost and its undo, and a post's outbox events, all carry one key.
func TestDirectPublish_RepostAndUndoShareTheOriginalPostsOutboxKey(t *testing.T) {
	ctx := context.Background()
	post, reposter, repost := uuid.New(), uuid.New(), uuid.New()
	p, sent := capturingProducer(t)
	if err := p.PublishPostReposted(ctx, repost, reposter, post, uuid.New(), "repost", "", "public", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := p.PublishPostRepostUndone(ctx, repost, reposter, post, uuid.New(), "repost"); err != nil {
		t.Fatal(err)
	}
	outbox, err := OutboxMessage(ctx, uuid.New(), events.PostDeleted, post, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range *sent {
		if string(s.msg.Key) != string(outbox.Key) {
			t.Fatalf("direct publish %d key = %q, the post's outbox key = %q", i, s.msg.Key, outbox.Key)
		}
	}
}

// Events about a user, not a post, keep their writer and their actor key.
func TestDirectPublish_UserScopedEventsKeepTheirWriterAndKey(t *testing.T) {
	ctx := context.Background()
	actor := uuid.New()
	cases := map[string]func(p *Producer) error{
		"SpamDetected":  func(p *Producer) error { return p.PublishSpamDetected(ctx, actor, "r", 0.9) },
		"StoryCreated":  func(p *Producer) error { return p.PublishStoryCreated(ctx, uuid.New(), actor, "image") },
		"UserMentioned": func(p *Producer) error { return p.PublishUserMentioned(ctx, uuid.New(), actor, uuid.NewString()) },
		"CommentReacted": func(p *Producer) error {
			return p.PublishCommentReacted(ctx, uuid.New(), uuid.New(), uuid.New(), actor, "like")
		},
	}
	for name, publish := range cases {
		t.Run(name, func(t *testing.T) {
			p, sent := capturingProducer(t)
			if err := publish(p); err != nil {
				t.Fatal(err)
			}
			got := (*sent)[0]
			if got.writer != p.writer {
				t.Fatal("a user-scoped event moved to the relay writer")
			}
			if string(got.msg.Key) != actor.String() {
				t.Fatalf("key = %q, want the actor %s", got.msg.Key, actor)
			}
		})
	}
}

// An unknown post id keeps the actor key rather than one shared zero key.
func TestEncodePostMessage_NilPostKeepsTheActorKey(t *testing.T) {
	actor := uuid.New()
	msg, err := encodePostMessage(context.Background(), events.PostCreated, &actor, uuid.Nil, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if string(msg.Key) != actor.String() {
		t.Fatalf("key = %q, want the actor %s", msg.Key, actor)
	}
}

// The relay still writes through its own writer.
func TestPublishOutbox_UsesTheRelayWriter(t *testing.T) {
	p, sent := capturingProducer(t)
	if err := p.PublishOutbox(context.Background(), kafka.Message{Key: []byte("k")}); err != nil {
		t.Fatal(err)
	}
	if (*sent)[0].writer != p.relay {
		t.Fatal("PublishOutbox did not use the relay writer")
	}
}

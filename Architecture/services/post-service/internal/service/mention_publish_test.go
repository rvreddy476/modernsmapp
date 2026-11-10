package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Bug 10 Nov 2026 (dev): a post with "@call_b" in the caption, or with
// call_b in the explicit `mentions` field, was created but call_b was never
// notified. These pin the immediate-publish path end to end inside
// post-service: CreatePost (not scheduled, not held) resolves the handle,
// asks the mention gate, and writes one user.mentioned message whose payload
// is what notification-service decodes into a "mention" notification.
func TestImmediatePublishMentionReachesTheMentionedPerson(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		mentions []string
	}{
		{"caption", "hello @call_b, look at this", nil},
		{"explicit field", "hello there", []string{"call_b"}},
		{"both, once", "hello @call_b", []string{"@call_b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newCreateRig(t)
			w, usersURL, graphURL := newPrivacyWorld(t)
			wireWorld(r.svc, usersURL, graphURL)
			callB := w.person("call_b", "everyone", "everyone") // the default for a fresh adult account

			type sent struct {
				mentioned, author uuid.UUID
				postID            string
			}
			var mu sync.Mutex
			var got []sent
			r.svc.postMentionPublisher = func(_ context.Context, mentioned, author uuid.UUID, postID string) error {
				mu.Lock()
				defer mu.Unlock()
				got = append(got, sent{mentioned, author, postID})
				return nil
			}

			in := r.input("post", nil)
			in.Text = tc.text
			in.Mentions = tc.mentions
			p, err := r.svc.CreatePost(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if p.IsScheduled {
				t.Fatal("an immediate post must not be scheduled")
			}
			if len(p.MentionUsernames) != 1 || p.MentionUsernames[0] != "call_b" {
				t.Fatalf("stored mentions = %v, want [call_b]", p.MentionUsernames)
			}

			// The fan-out is asynchronous (announcePublishedPost).
			deadline := time.Now().Add(3 * time.Second)
			for {
				mu.Lock()
				n := len(got)
				mu.Unlock()
				if n > 0 || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond) // and nothing more arrives
			mu.Lock()
			defer mu.Unlock()
			if len(got) != 1 {
				t.Fatalf("user.mentioned events = %d, want exactly 1 (%v)", len(got), got)
			}
			ev := got[0]
			if ev.mentioned != callB || ev.author != r.author || ev.postID != p.ID.String() {
				t.Fatalf("event = %+v, want mentioned=%s author=%s post=%s", ev, callB, r.author, p.ID)
			}

			// What the producer writes for those arguments, decoded the way
			// notification-service's social consumer decodes it.
			msg, err := postEvents.UserMentionedMessage(context.Background(), ev.mentioned, ev.author, ev.postID, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			var env events.EventEnvelope
			if err := json.Unmarshal(msg.Value, &env); err != nil {
				t.Fatal(err)
			}
			var payload events.UserMentionedPayload
			if err := json.Unmarshal(env.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if env.EventType != events.EventUserMentioned || payload.MentionedUserID != callB.String() ||
				payload.AuthorID != r.author.String() || payload.PostID != p.ID.String() || payload.CommentID != "" {
				t.Fatalf("wire event = %s %+v", env.EventType, payload)
			}
		})
	}
}

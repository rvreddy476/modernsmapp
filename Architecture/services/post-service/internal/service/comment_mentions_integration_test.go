//go:build integration

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Comment @mentions (T-12) end to end against a real Postgres: the real
// CreateCommentPG / CreateReply write paths, with fake user-service and
// graph-service, and the publisher seam in place of Kafka.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run CommentMentionsAgainstPostgres -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).
func TestCommentMentionsAgainstPostgres(t *testing.T) {
	pool := openHubMediaDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `ALTER TABLE comments ADD COLUMN IF NOT EXISTS dislike_count INTEGER NOT NULL DEFAULT 0`); err != nil {
		t.Fatal(err)
	}

	rig := newMentionRig(t)
	sam := rig.user("sam")
	blocker := rig.user("blocker")
	rig.user("stranger")
	rig.users["me"] = rig.commenter
	rig.graph.rels[blocker.String()] = ViewerRelationship{BlockedBy: true}

	public, followersOnly := uuid.New(), uuid.New()
	for id, vis := range map[uuid.UUID]string{public: "public", followersOnly: "followers"} {
		if _, err := pool.Exec(ctx, `INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, created_at, updated_at)
			VALUES ($1, $2, 'mention me', $3, 'post', 'approved', NOW(), NOW())`, id, rig.author, vis); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		for _, id := range []uuid.UUID{public, followersOnly} {
			_, _ = pool.Exec(bg, `DELETE FROM comment_idempotency WHERE post_id = $1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM comments WHERE post_id = $1 AND parent_id IS NOT NULL`, id)
			_, _ = pool.Exec(bg, `DELETE FROM comments WHERE post_id = $1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id = $1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, id)
		}
	})

	svc := New(postgres.New(pool), nil, nil)
	svc.userServiceURL = rig.svc.userServiceURL
	svc.graphServiceURL = rig.svc.graphServiceURL
	svc.internalServiceKey = rig.svc.internalServiceKey
	svc.httpClient = rig.svc.httpClient
	svc.storyAudience = rig.svc.storyAudience
	// No Redis here: a limiter on an unreachable address fails open, which
	// is its documented behaviour, instead of dereferencing a nil client.
	unreachable := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = unreachable.Close() })
	svc.rateLimiter = engagement.NewRateLimiter(unreachable)

	var mu sync.Mutex
	var sent []sentMention
	svc.commentMentionPublisher = func(_ context.Context, mentioned, commenter, postID, commentID uuid.UUID) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, sentMention{mentioned, commenter, postID, commentID})
		return nil
	}
	expect := func(step string, want ...sentMention) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			mu.Lock()
			n := len(sent)
			mu.Unlock()
			if n >= len(want) || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond) // nothing extra arrives late
		mu.Lock()
		got := append([]sentMention(nil), sent...)
		sent = nil
		mu.Unlock()
		if len(got) != len(want) {
			t.Fatalf("%s: sent %+v, want %+v", step, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: sent %+v, want %+v", step, got, want)
			}
		}
	}

	// A comment: Sam is told; the commenter and a user who blocked them are not.
	c, err := svc.CreateCommentPG(ctx, public, rig.commenter, "nice one @sam @Sam @blocker @me", "mention-key", "fp-1")
	if err != nil {
		t.Fatalf("CreateCommentPG: %v", err)
	}
	expect("comment", sentMention{sam, rig.commenter, public, c.ID})

	// The same intent replayed is the same comment and no second notification.
	again, err := svc.CreateCommentPG(ctx, public, rig.commenter, "nice one @sam @Sam @blocker @me", "mention-key", "fp-1")
	if err != nil || again.ID != c.ID {
		t.Fatalf("replay: %v %v", again, err)
	}
	expect("replay")

	// A reply: Sam mentions the commenter back; the event names the reply.
	reply, err := svc.CreateReply(ctx, c.ID, sam, "thanks @me")
	if err != nil {
		t.Fatalf("CreateReply: %v", err)
	}
	expect("reply", sentMention{rig.commenter, sam, public, reply.ID})

	// A followers-only post: the follower hears, the stranger (who could not
	// open the post) does not.
	rig.followers[rig.commenter] = true
	rig.followers[sam] = true
	fc, err := svc.CreateCommentPG(ctx, followersOnly, rig.commenter, "@sam @stranger look", "", "")
	if err != nil {
		t.Fatalf("CreateCommentPG followers-only: %v", err)
	}
	expect("followers-only", sentMention{sam, rig.commenter, followersOnly, fc.ID})
}

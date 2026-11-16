package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

/*
	POST /v1/posts/:postId/like (feed-49, with feed-14's tests).

	The route is a toggle: the client sends no intent, and the answer is
	{liked, count}. What decides which way a tap goes used to be a Redis key,
	liked:<viewer>:<post>, with a 24 h TTL, flipped by a Lua script. After 24 h
	the key was gone, so a tap meant to UNLIKE ran the "like" branch: the
	viewer was counted a second time in the Redis counter, in
	engagement_counters and in PostgreSQL like_count, a second PostReacted went
	out, and the Scylla row (which the feed card reads) said the opposite of
	Redis from then on. The same happened to a like made through
	POST /reactions, which never wrote the key.

	Now the stored reaction decides. The viewer's row in Scylla
	reactions_by_post is read, and the tap asks for the other state through
	the same conditional writes POST / DELETE /reactions use:

	  like    INSERT … IF NOT EXISTS   (scylla.InteractionStore.React)
	  unlike  DELETE … IF EXISTS       (scylla.InteractionStore.Unreact)

	Each reports whether IT changed the row, and everything that counts or
	announces a like hangs on that answer: the Redis counter, liker set and
	membership key (engagement.SetLike), the engagement event that moves
	engagement_counters and PostgreSQL like_count, and the PostReacted /
	PostUnreacted outbox row. So however many taps race, and however old the
	like is, one like is counted once and one unlike takes it away once. Fifty
	concurrent taps on an unliked post all read "not liked", all ask for the
	like, one insert wins, and every answer is liked = true with the count up
	by one.

	The response keeps its shape. count is the like count the post's card
	shows (Scylla post_counters), which is the number that just moved; it used
	to be the Redis counter, which only knew the likes made through this route.

	Still best-effort after the row changed, logged on failure, as before: the
	Redis copies, the engagement event (published from a goroutine) and the
	outbox row. Two opposite taps in flight at once apply their Redis copies in
	no fixed order, so the liker set can briefly keep a viewer who has
	unliked; the counters add up either way, and nothing decides from Redis.
*/

// heartStore is the stored reaction on a post: the viewer's row, and the
// two conditional writes that report whether they changed it.
type heartStore interface {
	GetReaction(ctx context.Context, postID, userID uuid.UUID) (string, error)
	React(ctx context.Context, postID, userID uuid.UUID, reaction string) (bool, error)
	Unreact(ctx context.Context, postID, userID uuid.UUID) (bool, error)
}

// reactionOutbox is where a like's PostReacted / PostUnreacted row goes.
type reactionOutbox interface {
	InsertOutboxEvent(ctx context.Context, eventType, aggregateType string, aggregateID uuid.UUID, payload interface{}) error
}

// likeSeams are the like route's test seams; each nil means the real thing.
type likeSeams struct {
	// heartSeam stands in for Scylla reactions_by_post + post_counters.
	heartSeam heartStore
	// reactionOutboxSeam stands in for the Postgres outbox insert.
	reactionOutboxSeam reactionOutbox
	// engagementPublishSeam stands in for the engagement topic's producer
	// and is called in line, not from a goroutine.
	engagementPublishSeam func(ctx context.Context, event *engagement.EngagementEvent) error
	// tuneClearSeam stands in for the Postgres tune delete.
	tuneClearSeam func(ctx context.Context, userID, postID uuid.UUID) error
}

// errNoReactionStore is a Service built without the Scylla store.
var errNoReactionStore = errors.New("reaction store is not configured")

func (s *Service) hearts() heartStore {
	if s.heartSeam != nil {
		return s.heartSeam
	}
	if s.scyllaStore == nil {
		return nil
	}
	return s.scyllaStore
}

func (s *Service) reactionOutboxWriter() reactionOutbox {
	if s.reactionOutboxSeam != nil {
		return s.reactionOutboxSeam
	}
	if s.producer == nil || s.pgStore == nil {
		return nil
	}
	return s.pgStore
}

func (s *Service) clearTune(ctx context.Context, userID, postID uuid.UUID) error {
	if s.tuneClearSeam != nil {
		return s.tuneClearSeam(ctx, userID, postID)
	}
	if s.pgStore == nil {
		return nil
	}
	return s.pgStore.DeleteTune(ctx, userID, postID)
}

// LikeToggleResult is the response shape for the like toggle API.
type LikeToggleResult struct {
	Liked bool  `json:"liked"`
	Count int64 `json:"count"`
}

// ToggleLike flips the viewer's like on a post: the stored reaction says
// which way, and the idempotent write does it (see the note above).
func (s *Service) ToggleLike(ctx context.Context, postID, userID uuid.UUID) (*LikeToggleResult, error) {
	// Audit C5 + H2: one fetch covers visibility, NoLikes flag, and
	// the author-id used for the PostReacted event below.
	post, err := s.loadPostForEngagement(ctx, postID, userID)
	if err != nil {
		return nil, err
	}
	if post.NoLikes {
		return nil, ErrLikesDisabled
	}

	// Rate limit
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:like:%s", userID), engagement.LikeLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	hearts := s.hearts()
	if hearts == nil {
		return nil, errNoReactionStore
	}
	// feed-49: the stored reaction, not a Redis key that expires, says
	// whether this tap likes or unlikes. Any stored reaction is a heart
	// (heart_only.go), so any of them is taken off.
	stored, err := hearts.GetReaction(ctx, postID, userID)
	if err != nil {
		return nil, fmt.Errorf("read stored reaction: %w", err)
	}
	return s.setLike(ctx, hearts, post, userID, stored == "")
}

// setLike puts the viewer's like on a post into the wanted state and answers
// with that state and the like count. It is idempotent: asking for the state
// the viewer is already in changes nothing and counts nothing.
func (s *Service) setLike(ctx context.Context, hearts heartStore, post *postgres.Post, userID uuid.UUID, want bool) (*LikeToggleResult, error) {
	postID := post.ID

	// The liked list's record first, as on POST / DELETE /reactions
	// (liked_reels.go): it is idempotent, so a failure here or at the
	// stored-reaction write is an error the client can retry. The retry
	// reads the same stored state and asks for the same thing.
	reaction := ""
	if want {
		reaction = "like"
	}
	if err := s.indexReaction(ctx, postID, userID, reaction, want); err != nil {
		return nil, err
	}

	var changed bool
	var err error
	if want {
		changed, err = hearts.React(ctx, postID, userID, "like")
	} else {
		changed, err = hearts.Unreact(ctx, postID, userID)
	}
	if err != nil {
		if !changed {
			return nil, err
		}
		// The row changed and its counter update failed. The viewer's state
		// is what they asked for; failing the tap would make the retry flip
		// it back.
		slog.WarnContext(ctx, "like: reaction stored but its counter update failed",
			"post_id", postID, "liked", want, "err", err)
	}

	if want {
		// Like and tune are mutually exclusive (tune.go): a like being SET
		// clears the viewer's private dislike.
		if err := s.clearTune(ctx, userID, postID); err != nil {
			log.Printf("Warning: failed to clear tune on like: %v", err)
		}
	}
	// A liked long video joins the viewer's "Loved" collection; an unliked
	// one leaves it (system_playlists.go; reels are never written there).
	s.syncLikedCollection(ctx, userID, post, want)
	s.invalidateFeedHydration(ctx, userID, postID)

	// feed-49: count and announce only the tap whose write changed the
	// stored reaction. A repeat (a second tap that raced the first, a retry,
	// a like already made through /reactions) moves nothing.
	if changed {
		s.recordLikeChange(ctx, post, userID, want)
	}

	return &LikeToggleResult{Liked: want, Count: s.likeCountAfterToggle(ctx, postID)}, nil
}

// recordLikeChange counts and announces one like that was set or removed:
// the Redis copies, the engagement event and the outbox row. Each failure
// is logged; the stored reaction has already changed.
func (s *Service) recordLikeChange(ctx context.Context, post *postgres.Post, userID uuid.UUID, liked bool) {
	postID := post.ID

	var seq, actionTS int64 = 0, time.Now().UnixMicro()
	if s.rdb != nil {
		if res, err := engagement.SetLike(ctx, s.rdb, userID, postID, liked); err != nil {
			slog.WarnContext(ctx, "like: redis copies not updated", "post_id", postID, "err", err)
		} else {
			seq, actionTS = res.Seq, res.ActionTS
		}
	}

	eventType := engagement.EventPostLiked
	if !liked {
		eventType = engagement.EventPostUnliked
	}
	event := engagement.BuildEvent(eventType, postID, userID, post.AuthorID, postID, "post", "like", liked, seq, actionTS)
	switch {
	case s.engagementPublishSeam != nil:
		if err := s.engagementPublishSeam(ctx, event); err != nil {
			log.Printf("Warning: failed to publish engagement event: %v", err)
		}
	case s.engProducer != nil:
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish engagement event: %v", err)
			}
		}()
	}

	// Audit H4: the notification and ranking trigger goes through the
	// outbox. PostUnreacted on the way down too, as DELETE /reactions sends
	// it, so feed-service's velocity counter comes back down.
	if out := s.reactionOutboxWriter(); out != nil {
		payload := events.PostReactedPayload{
			PostID:       postID.String(),
			PostAuthorID: post.AuthorID.String(),
			ReactorID:    userID.String(),
			ReactType:    "like",
			CreatedAt:    time.Now(),
		}
		outboxType := events.PostReacted
		if !liked {
			outboxType = events.PostUnreacted
		}
		if err := out.InsertOutboxEvent(ctx, outboxType, "post", postID, payload); err != nil {
			log.Printf("Warning: failed to enqueue %s (ToggleLike) to outbox: %v", outboxType, err)
		}
	}
}

// likeCountAfterToggle is the like count the post's card shows.
func (s *Service) likeCountAfterToggle(ctx context.Context, postID uuid.UUID) int64 {
	counts, err := s.likeCountsForPost(ctx, postID)
	if err != nil || counts == nil {
		return 0
	}
	if counts.Likes < 0 {
		return 0
	}
	return counts.Likes
}

// removeLike takes the viewer's like off a post if there is one, for a
// caller that wants it gone whatever it is (tune.go). Idempotent.
func (s *Service) removeLike(ctx context.Context, postID, userID uuid.UUID) error {
	hearts := s.hearts()
	if hearts == nil {
		return nil
	}
	stored, err := hearts.GetReaction(ctx, postID, userID)
	if err != nil {
		return err
	}
	if stored == "" {
		return nil
	}
	post, err := s.loadPostForEngagement(ctx, postID, userID)
	if err != nil {
		return err
	}
	_, err = s.setLike(ctx, hearts, post, userID, false)
	return err
}

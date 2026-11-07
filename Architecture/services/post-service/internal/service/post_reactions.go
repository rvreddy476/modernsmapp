// post_reactions.go holds post reactions: the legacy React / Unreact /
// GetMyReaction calls and the multi-reaction toggle with its counts and
// event. Moved out of post.go unchanged.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

func (s *Service) React(ctx context.Context, postID, userID uuid.UUID, reaction string) error {
	// Heart only (heart_only.go): before anything is read or written.
	if err := checkPostReaction(reaction); err != nil {
		return err
	}
	// Audit C5 + H2: one fetch covers visibility + author-id for
	// the PostReacted event. Was: visibility check did a GetPost,
	// the goroutine below did *another* GetPost just for AuthorID.
	post, err := s.loadPostForEngagement(ctx, postID, userID)
	if err != nil {
		return err
	}
	// The liked list's record first (liked_reels.go): idempotent, so a
	// failure here or below is an error the client retries.
	if err := s.indexReaction(ctx, postID, userID, reaction, true); err != nil {
		return err
	}
	added, err := s.scyllaStore.React(ctx, postID, userID, reaction)
	if err != nil {
		return err
	}
	s.invalidateFeedHydration(ctx, userID, postID)
	// The "Liked videos" collection follows a like made through the
	// reactions route too (Android likes this way; the web uses /like), and a
	// like changed to another reaction leaves it.
	s.syncLikedCollection(ctx, userID, post, reaction == "like")

	// Audit H4: PostReacted via outbox. Synchronous insert so a
	// process crash in the React goroutine window doesn't drop the
	// notification trigger. Only for a NEW reaction: a repeat tap or a
	// change of type adds no reaction to announce (see reactionEvent).
	if s.producer != nil && added {
		payload := events.PostReactedPayload{
			PostID:       postID.String(),
			PostAuthorID: post.AuthorID.String(),
			ReactorID:    userID.String(),
			ReactType:    reaction,
			CreatedAt:    time.Now(),
		}
		if err := s.pgStore.InsertOutboxEvent(ctx, events.PostReacted, "post", postID, payload); err != nil {
			log.Printf("Warning: failed to enqueue PostReacted to outbox: %v", err)
		}
	}

	// Ephemeral Redis pub/sub for live feed viewers — best-effort.
	go func() {
		bgCtx := context.Background()
		counts, _ := s.countsForPost(bgCtx, postID)
		if counts != nil {
			signal, _ := json.Marshal(map[string]any{
				"type": "post_update",
				"payload": map[string]any{
					"post_id":     postID.String(),
					"update_type": "reaction",
					"actor_id":    userID.String(),
					"likes":       counts.Likes,
					"comments":    counts.Comments,
				},
			})
			s.rdb.Publish(bgCtx, "feed:post_update", signal)
		}
	}()

	return nil
}

func (s *Service) Unreact(ctx context.Context, postID, userID uuid.UUID) error {
	if err := s.checkEngagementVisibility(ctx, postID, userID); err != nil {
		return err
	}
	if err := s.indexReaction(ctx, postID, userID, "", false); err != nil {
		return err
	}
	removed, err := s.scyllaStore.Unreact(ctx, postID, userID)
	if err != nil {
		return err
	}
	s.invalidateFeedHydration(ctx, userID, postID)
	// An unlike through the reactions route leaves "Liked videos" too.
	if removed {
		if post, getErr := s.pgStore.GetPost(ctx, postID); getErr == nil {
			s.syncLikedCollection(ctx, userID, post, false)
		}
	}

	// PostUnreacted, so feed-service's velocity counter comes back down
	// (it used to only ever go up). Only when a reaction was actually
	// removed. Author id is not loaded on this path; the consumers key on
	// post and reactor.
	if s.producer != nil && removed {
		payload := events.PostReactedPayload{
			PostID:    postID.String(),
			ReactorID: userID.String(),
			CreatedAt: time.Now(),
		}
		if err := s.pgStore.InsertOutboxEvent(ctx, events.PostUnreacted, "post", postID, payload); err != nil {
			log.Printf("Warning: failed to enqueue PostUnreacted to outbox: %v", err)
		}
	}

	// Fire-and-forget: Redis publish in background
	go func() {
		bgCtx := context.Background()
		counts, _ := s.countsForPost(bgCtx, postID)
		if counts != nil {
			signal, _ := json.Marshal(map[string]any{
				"type": "post_update",
				"payload": map[string]any{
					"post_id":     postID.String(),
					"update_type": "reaction",
					"actor_id":    userID.String(),
					"likes":       counts.Likes,
					"comments":    counts.Comments,
				},
			})
			s.rdb.Publish(bgCtx, "feed:post_update", signal)
		}
	}()

	return nil
}

func (s *Service) GetMyReaction(ctx context.Context, postID, userID uuid.UUID) (string, error) {
	stored, err := s.scyllaStore.GetReaction(ctx, postID, userID)
	if err != nil {
		return "", err
	}
	// An old emoji reads as the heart (heart_only.go).
	return heartViewerReaction(stored), nil
}

// ============================================================
// Multi-Reactions
// ============================================================

// ReactionToggleResult is the response for the multi-reaction toggle API.
type ReactionToggleResult struct {
	ReactionType string                   `json:"reaction_type"`
	IsSet        bool                     `json:"is_set"`
	Counts       *postgres.ReactionCounts `json:"counts"`
}

// ToggleReaction sets, changes, or removes a reaction on a post.
func (s *Service) ToggleReaction(ctx context.Context, postID, userID uuid.UUID, reactionType string) (*ReactionToggleResult, error) {
	// Heart only (heart_only.go). The store toggles any existing reaction
	// off for a post, so a viewer whose old emoji reads as the heart
	// un-hearts on the next tap rather than being switched to "like".
	if err := checkPostReaction(reactionType); err != nil {
		return nil, err
	}

	// Audit C5 + H2 combined: one fetch checks visibility *and*
	// reads the per-post NoLikes flag — handler used to do its own
	// GetPost just for that, then the service called GetPost again
	// inside the visibility gate.
	post, err := s.loadPostForEngagement(ctx, postID, userID)
	if err != nil {
		return nil, err
	}
	if post.NoLikes {
		return nil, ErrLikesDisabled
	}

	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:react:%s", userID), engagement.LikeLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	// The reactions row this writes is also the liked list's record
	// (liked_reels.go); no separate index write.
	// Audit H4: the reaction event rides the reaction's own transaction
	// (store/postgres/reactions.go). Self-reactions announce nothing, as
	// before. `post` is already in scope from the visibility check above.
	toggle, err := s.pgStore.ToggleReaction(ctx, "post", postID, userID, reactionType, func(t postgres.ReactionToggle) (string, interface{}, bool) {
		if s.producer == nil || post.AuthorID == userID {
			return "", nil, false
		}
		return reactionEvent(t, postID, post.AuthorID, userID)
	})
	if err != nil {
		return nil, err
	}
	newType, isSet := toggle.NewType, toggle.IsSet

	// Also sync to ScyllaDB for feed hydration compatibility
	if isSet {
		if _, err := s.scyllaStore.React(ctx, postID, userID, newType); err != nil {
			log.Printf("Warning: failed to sync reaction to ScyllaDB: %v", err)
		}
	} else {
		if _, err := s.scyllaStore.Unreact(ctx, postID, userID); err != nil {
			log.Printf("Warning: failed to remove reaction from ScyllaDB: %v", err)
		}
	}

	// Get updated counts
	counts, err := s.pgStore.GetReactionCounts(ctx, "post", postID)
	if err != nil {
		log.Printf("Warning: failed to get reaction counts: %v", err)
	}

	return &ReactionToggleResult{
		ReactionType: newType,
		IsSet:        isSet,
		Counts:       foldPostReactionCounts(counts),
	}, nil
}

// reactionEvent is the event a reaction toggle announces: PostReacted for a
// NEW reaction, PostUnreacted for a removed one, and nothing for a change of
// type — the post gained no reaction, so neither the author's notification
// nor feed-service's velocity counter should move. A type switch used to be
// announced as a fresh PostReacted, so flipping like → love → like inflated
// a post's velocity and re-notified its author.
func reactionEvent(t postgres.ReactionToggle, postID, authorID, reactorID uuid.UUID) (string, interface{}, bool) {
	payload := events.PostReactedPayload{
		PostID:       postID.String(),
		PostAuthorID: authorID.String(),
		ReactorID:    reactorID.String(),
		CreatedAt:    time.Now(),
	}
	switch {
	case t.IsSet && t.PrevType == "":
		payload.ReactType = t.NewType
		return events.PostReacted, payload, true
	case !t.IsSet && t.PrevType != "":
		payload.ReactType = t.PrevType
		return events.PostUnreacted, payload, true
	}
	return "", nil, false
}

// GetReactionCounts returns the breakdown of reaction counts for a post.
// Every emoji folds into "like" (heart_only.go).
func (s *Service) GetReactionCounts(ctx context.Context, postID uuid.UUID) (*postgres.ReactionCounts, error) {
	counts, err := s.pgStore.GetReactionCounts(ctx, "post", postID)
	if err != nil {
		return nil, err
	}
	return foldPostReactionCounts(counts), nil
}

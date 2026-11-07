// post_comments.go holds comments on a post: create, reply, edit,
// soft-delete, list (and around one comment), and the comment like /
// dislike toggles. Each mutation moves comment_count once and announces a
// comment_change (see comment_counts.go). Moved out of post.go unchanged.

package service

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// CommentLikeToggleResult is the response shape for the comment like toggle API.
type CommentLikeToggleResult struct {
	Liked        bool  `json:"liked"`
	Count        int64 `json:"count"`
	DislikeCount int64 `json:"dislike_count"`
}

// ToggleCommentLike is the legacy like route, now a toggle of the viewer's
// reaction between postgres.LikeEmoji and none on comment_reactions (2026-09-27),
// so old clients and the emoji UI count the same rows. Count is
// reaction_count. The Redis Lua toggle and the like_count column are no
// longer written for likes; dislikes keep their own path.
func (s *Service) ToggleCommentLike(ctx context.Context, commentID, userID uuid.UUID) (*CommentLikeToggleResult, error) {
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:comment_like:%s", userID), engagement.CommentLikeLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}
	comment, err := s.visibleCommentForViewer(ctx, commentID, userID)
	if err != nil {
		return nil, err
	}

	liked, err := s.pgStore.ToggleCommentHeart(ctx, commentID, userID)
	if err != nil {
		return nil, err
	}
	summary, err := s.pgStore.GetCommentReactionSummary(ctx, commentID, &userID)
	if err != nil {
		return nil, err
	}
	dislikes, err := s.pgStore.GetCommentDislikeCount(ctx, commentID)
	if err != nil {
		slog.Warn("comment dislike count read failed", "comment_id", commentID, "err", err)
	}

	s.publishCommentReactionEvents(ctx, comment, userID, postgres.LikeEmoji, liked)
	s.publishCommentChange(comment.PostID, commentID, comment.ParentID, CommentChangeReaction, userID)
	return &CommentLikeToggleResult{Liked: liked, Count: int64(summary.ReactionCount), DislikeCount: dislikes}, nil
}

// CommentDislikeToggleResult is the response shape for the comment dislike toggle API.
type CommentDislikeToggleResult struct {
	Disliked     bool  `json:"disliked"`
	DislikeCount int64 `json:"dislike_count"`
	LikeCount    int64 `json:"like_count"`
}

// ToggleCommentDislike executes the atomic Lua toggle for comment dislikes with mutual exclusion.
func (s *Service) ToggleCommentDislike(ctx context.Context, commentID, userID uuid.UUID) (*CommentDislikeToggleResult, error) {
	// The post's read gate first (2026-09-29): the like toggle already
	// refused comments under a post the viewer may not open; the dislike
	// did not, and its counts answered for private and 18+ posts.
	if err := s.gateCommentPost(ctx, commentID, &userID); err != nil {
		return nil, err
	}
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:comment_like:%s", userID), engagement.CommentLikeLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	result, err := engagement.ToggleCommentDislike(ctx, s.rdb, userID, commentID)
	if err != nil {
		return nil, err
	}

	// Update PostgreSQL dislike_count
	dislikeDelta := 1
	if !result.IsSet {
		dislikeDelta = -1
	}
	if err := s.pgStore.IncrementCommentDislikeCount(ctx, commentID, dislikeDelta); err != nil {
		log.Printf("Warning: failed to update comment dislike_count: %v", err)
	}

	// If a like was removed by mutual exclusion, decrement like_count in PG
	if result.OppositeRemoved {
		if err := s.pgStore.IncrementCommentLikeCount(ctx, commentID, -1); err != nil {
			log.Printf("Warning: failed to update comment like_count: %v", err)
		}
	}

	eventType := engagement.EventCommentDisliked
	if !result.IsSet {
		eventType = engagement.EventCommentUndisliked
	}

	if s.engProducer != nil {
		event := engagement.BuildEvent(eventType, uuid.Nil, userID, uuid.Nil, commentID, "comment", "dislike", result.IsSet, result.Seq, result.ActionTS)
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish comment dislike event: %v", err)
			}
		}()
		// If like was removed, also publish that event
		if result.OppositeRemoved {
			likeEvent := engagement.BuildEvent(engagement.EventCommentUnliked, uuid.Nil, userID, uuid.Nil, commentID, "comment", "like", false, result.Seq, result.ActionTS)
			go func() {
				if err := s.engProducer.Publish(context.Background(), likeEvent); err != nil {
					log.Printf("Warning: failed to publish comment unlike event: %v", err)
				}
			}()
		}
	}

	// No notifications for dislikes

	if c, err := s.pgStore.GetCommentByID(ctx, commentID); err == nil && c != nil {
		s.publishCommentChange(c.PostID, commentID, c.ParentID, CommentChangeReaction, userID)
	}
	return &CommentDislikeToggleResult{Disliked: result.IsSet, DislikeCount: result.DislikeCount, LikeCount: result.LikeCount}, nil
}

// CreateCommentPG creates a comment in PostgreSQL with counter update.
// CreateCommentPG creates a comment.
//
// clientKey/fingerprint make the insert durably idempotent (see
// postgres.CreateCommentIdempotent). Both may be empty, in which case the
// caller made no idempotency promise and none is enforced.
func (s *Service) CreateCommentPG(ctx context.Context, postID, authorID uuid.UUID, body, clientKey, fingerprint string) (*postgres.Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("INVALID_REQUEST: comment body cannot be blank")
	}
	// Audit C5 + H2: one fetch covers visibility, NoComments flag,
	// and the post-author-id used for the CommentCreated event below.
	// Previously the handler did a GetPost just to check NoComments,
	// then this service did another GetPost just for AuthorID — two
	// full Postgres+Scylla fetches per comment.
	post, err := s.loadPostForEngagement(ctx, postID, authorID)
	if err != nil {
		return nil, err
	}
	// Members-only post: members and the author only (members_read.go).
	if err := s.requireMemberOf(ctx, post, &authorID); err != nil {
		return nil, err
	}
	if post.NoComments {
		return nil, ErrCommentsDisabled
	}
	if err := s.commentAccessAllows(ctx, post, authorID); err != nil {
		return nil, err
	}
	// Comments audience (allow_comments_from: everyone | friends). Resolved
	// by graph-service against the post author's CURRENT setting and the
	// live connection/mutual-follow graph; fail-closed (privacy_gate.go).
	if !s.canComment(ctx, authorID, post.AuthorID) {
		return nil, ErrCommentsRestricted
	}

	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:comment:%s", authorID), engagement.CommentLimitPerMin, time.Minute) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	comment, replayed, err := s.pgStore.CreateCommentIdempotent(ctx, postID, authorID, body, clientKey, fingerprint)
	if err != nil {
		return nil, err
	}
	if replayed {
		// The intent was already recorded. Return the original comment and do
		// NOT re-run any of the side effects below — bumping trending scores,
		// counters or notifications a second time is exactly the duplication
		// this path exists to prevent.
		return comment, nil
	}

	// Bump trending scores for hashtags in the comment body (max 5 per design spec).
	// Fire-and-forget: comment hashtags influence trending but are not stored per-comment.
	if commentTags := extractHashtags(body); len(commentTags) > 0 {
		if len(commentTags) > 5 {
			commentTags = commentTags[:5]
		}
		go func() {
			bgCtx := context.Background()
			today := time.Now().UTC().Format("2006-01-02")
			key := "trending:hashtags:" + today
			for _, tag := range commentTags {
				if err := s.rdb.ZIncrBy(bgCtx, key, 0.5, tag).Err(); err != nil {
					log.Printf("Warning: failed to bump comment hashtag trending for %s: %v", tag, err)
				}
			}
			s.rdb.Expire(bgCtx, key, 48*time.Hour)
		}()
	}

	// The one writer of comment_count: a visible comment came into being.
	if err := s.pgStore.AdjustCommentCount(ctx, postID, 1); err != nil {
		slog.Warn("failed to increment comment_count", "post_id", postID, "error", err)
	}
	s.publishCommentChange(postID, comment.ID, nil, CommentChangeCreated, authorID)

	// Author already loaded above.
	postAuthorID := post.AuthorID

	// Publish engagement event
	if s.engProducer != nil {
		seqKey := fmt.Sprintf("eng:seq:%s", authorID)
		seq, _ := s.rdb.Incr(ctx, seqKey).Result()
		s.rdb.Expire(ctx, seqKey, 24*time.Hour)

		event := engagement.BuildEvent(engagement.EventCommentCreated, postID, authorID, postAuthorID, comment.ID, "post", "comment", true, seq, time.Now().UnixMicro())
		event.CommentBody = body
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish comment event: %v", err)
			}
		}()
	}

	// Audit H4: route CommentCreated through the outbox so a
	// crash in the previous goroutine window can't silently drop
	// the notification trigger. The synchronous INSERT runs after
	// the comment row is committed; the outbox worker publishes
	// to Kafka with at-least-once delivery.
	if s.producer != nil {
		payload := events.CommentCreatedPayload{
			CommentID:    comment.ID.String(),
			PostID:       postID.String(),
			PostAuthorID: postAuthorID.String(),
			AuthorID:     authorID.String(),
			Text:         body,
			CreatedAt:    time.Now(),
		}
		if err := s.pgStore.InsertOutboxEvent(ctx, events.CommentCreated, "post", postID, payload); err != nil {
			log.Printf("Warning: failed to enqueue CommentCreated to outbox: %v", err)
		}
	}

	// @mentions in the comment (T-12): blocks, self and the post's audience
	// are applied per mentioned user (comment_mentions.go).
	s.announceCommentMentions(post, authorID, comment.ID, body)

	return comment, nil
}

// CreateReply creates a reply under a comment (2026-09-27: for everyone).
//
// The gates are EXACTLY AddComment's (CreateCommentPG): the post must be
// visible to the viewer (private accounts, blocks both ways, followers-only
// — loadPostForEngagement, fail-closed), comments not disabled, the comments
// audience (canComment), and the rate limit. The former post-owner rule and
// one-reply cap are gone. Replying to a reply attaches to the reply's parent
// (store.CreateReply), so the frame's parent_id is the top-level comment.
func (s *Service) CreateReply(ctx context.Context, commentID, userID uuid.UUID, body string) (*postgres.Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("INVALID_REQUEST: reply body cannot be blank")
	}
	target, err := s.visibleComment(ctx, commentID)
	if err != nil {
		return nil, err
	}
	post, err := s.loadPostForEngagement(ctx, target.PostID, userID)
	if err != nil {
		return nil, err
	}
	// Members-only post: members and the author only (members_read.go).
	if err := s.requireMemberOf(ctx, post, &userID); err != nil {
		return nil, err
	}
	if post.NoComments {
		return nil, ErrCommentsDisabled
	}
	if err := s.commentAccessAllows(ctx, post, userID); err != nil {
		return nil, err
	}
	if !s.canComment(ctx, userID, post.AuthorID) {
		return nil, ErrCommentsRestricted
	}
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:reply:%s", userID), engagement.ReplyLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	reply, parentAuthorID, err := s.pgStore.CreateReply(ctx, commentID, userID, body)
	if err != nil {
		return nil, err
	}
	// A reply is a comment: it counts, and the thread's viewers hear about it.
	if err := s.pgStore.AdjustCommentCount(ctx, reply.PostID, 1); err != nil {
		slog.Warn("failed to increment comment_count for reply", "post_id", reply.PostID, "error", err)
	}
	s.publishCommentChange(reply.PostID, reply.ID, reply.ParentID, CommentChangeReplied, userID)

	// Publish engagement event
	if s.engProducer != nil {
		seqKey := fmt.Sprintf("eng:seq:%s", userID)
		seq, _ := s.rdb.Incr(ctx, seqKey).Result()
		s.rdb.Expire(ctx, seqKey, 24*time.Hour)

		event := engagement.BuildEvent(engagement.EventReplyCreated, reply.PostID, userID, uuid.Nil, reply.ID, "comment", "reply", true, seq, time.Now().UnixMicro())
		event.CommentBody = body
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish reply event: %v", err)
			}
		}()
	}

	// Publish legacy event so notification-service sends a notification to
	// the author of the comment replied to (not to yourself).
	if s.producer != nil && parentAuthorID != userID {
		go func() {
			if err := s.producer.PublishCommentCreated(context.Background(), reply.ID, reply.PostID, parentAuthorID, userID, body); err != nil {
				log.Printf("Warning: failed to publish legacy reply notification event: %v", err)
			}
		}()
	}

	// @mentions in the reply (T-12), same rules as a top-level comment.
	s.announceCommentMentions(post, userID, reply.ID, body)

	return reply, nil
}

// SoftDeleteComment marks a comment as deleted and decrements counter.
func (s *Service) SoftDeleteComment(ctx context.Context, commentID, userID uuid.UUID) error {
	postID, counted, err := s.pgStore.SoftDeleteComment(ctx, commentID, userID)
	if err != nil {
		return err
	}
	// Only a comment that was in the visible set leaves the count; a hidden
	// or held one was never in it. A retried delete is COMMENT_NOT_FOUND
	// above and never reaches here.
	if counted {
		if err := s.pgStore.AdjustCommentCount(ctx, postID, -1); err != nil {
			slog.Warn("failed to decrement comment_count", "post_id", postID, "error", err)
		}
	}
	s.publishCommentChange(postID, commentID, nil, CommentChangeDeleted, userID)

	if s.engProducer != nil {
		seqKey := fmt.Sprintf("eng:seq:%s", userID)
		seq, _ := s.rdb.Incr(ctx, seqKey).Result()
		s.rdb.Expire(ctx, seqKey, 24*time.Hour)

		event := engagement.BuildEvent(engagement.EventCommentDeleted, postID, userID, uuid.Nil, commentID, "post", "comment", false, seq, time.Now().UnixMicro())
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish comment delete event: %v", err)
			}
		}()
	}

	return nil
}

// EditComment edits a comment within 15 minutes of creation.
func (s *Service) EditComment(ctx context.Context, commentID, userID uuid.UUID, body string) error {
	postID, err := s.pgStore.EditComment(ctx, commentID, userID, body)
	if err != nil {
		return err
	}
	s.publishCommentChange(postID, commentID, nil, CommentChangeEdited, userID)
	return nil
}

// ListCommentsPG returns paginated threaded comments from PostgreSQL.
// viewerID drives moderation visibility: held-for-review comments are
// only shown to their own author. Pass nil for anonymous viewers.
func (s *Service) ListCommentsPG(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID, cursor string, limit int) ([]postgres.Comment, string, error) {
	return s.pgStore.ListComments(ctx, postID, viewerID, cursor, limit)
}

// GetCommentsAroundPG returns comments surrounding a target comment
// for deep-link navigation. viewerID drives moderation visibility:
// held-for-review comments are only shown to their own author.
// The post's read decision comes first, as in ListCommentsSortedPG.
func (s *Service) GetCommentsAroundPG(ctx context.Context, postID, commentID uuid.UUID, viewerID *uuid.UUID, limit int) ([]postgres.Comment, error) {
	if err := s.singlePostRead(ctx, postID, viewerID); err != nil {
		return nil, err
	}
	if err := s.postMembersGate(ctx, postID, viewerID); err != nil {
		return nil, err
	}
	return s.pgStore.GetCommentsAround(ctx, postID, commentID, viewerID, limit)
}

package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/store/postgres"
)

/*
	Replies for everyone and emoji reactions on comments (2026-09-27).

	  GET    /v1/comments/:id/replies   → ListReplies
	  PUT    /v1/comments/:id/reaction  → SetCommentReaction
	  DELETE /v1/comments/:id/reaction  → RemoveCommentReaction
	  POST   /v1/comments/:id/like      → ToggleCommentLike (post.go), same table

	Every one of these resolves the comment through visibleCommentForViewer:
	the comment must be in the public thread AND its post visible to the
	viewer (the same decision the ws-gateway room and GET /posts/:id make).
	Anything else is COMMENT_NOT_FOUND — a 404, never a 403 that would
	confirm a hidden post exists.

	Both reaction writes publish the comment_change frame with
	change: "reaction", exactly as the like toggle always has.
*/

// ErrInvalidEmoji is a PUT reaction whose emoji is empty or longer than
// CommentEmojiMaxChars characters.
var ErrInvalidEmoji = errors.New("emoji must be 1 to 16 characters")

// CommentEmojiMaxChars mirrors the table CHECK (char_length BETWEEN 1 AND 16).
const CommentEmojiMaxChars = 16

// NormalizeCommentEmoji trims the emoji and enforces the length rule.
func NormalizeCommentEmoji(raw string) (string, error) {
	e := strings.TrimSpace(raw)
	if e == "" || utf8.RuneCountInString(e) > CommentEmojiMaxChars {
		return "", ErrInvalidEmoji
	}
	return e, nil
}

// visibleCommentForViewer loads a comment for a reaction or reply-list
// request: it must be visible (not deleted, moderation_status 'visible')
// and its post must be visible to the viewer. Returns COMMENT_NOT_FOUND
// otherwise so the handler answers 404.
func (s *Service) visibleCommentForViewer(ctx context.Context, commentID uuid.UUID, viewerID uuid.UUID) (*postgres.Comment, error) {
	return s.visibleCommentForOptionalViewer(ctx, commentID, &viewerID)
}

func (s *Service) visibleCommentForOptionalViewer(ctx context.Context, commentID uuid.UUID, viewerID *uuid.UUID) (*postgres.Comment, error) {
	c, err := s.pgStore.GetVisibleCommentByID(ctx, commentID)
	if err != nil {
		return nil, err
	}
	ok, err := s.PostVisibleTo(ctx, c.PostID, viewerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("COMMENT_NOT_FOUND")
	}
	return c, nil
}

// ListReplies pages a comment's replies oldest-first. viewerID nil is an
// anonymous viewer (public posts only, visible replies only).
func (s *Service) ListReplies(ctx context.Context, commentID uuid.UUID, viewerID *uuid.UUID, cursor string, limit int) ([]postgres.Comment, string, error) {
	if _, err := s.visibleCommentForOptionalViewer(ctx, commentID, viewerID); err != nil {
		return nil, "", err
	}
	return s.pgStore.GetReplies(ctx, commentID, viewerID, cursor, limit)
}

// SetCommentReaction upserts the viewer's single reaction (a new emoji
// replaces the old one) and returns the comment's reaction summary.
func (s *Service) SetCommentReaction(ctx context.Context, commentID, userID uuid.UUID, emoji string) (*postgres.CommentReactionSummary, error) {
	emoji, err := NormalizeCommentEmoji(emoji)
	if err != nil {
		return nil, err
	}
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:comment_like:%s", userID), engagement.CommentLikeLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}
	comment, err := s.visibleCommentForViewer(ctx, commentID, userID)
	if err != nil {
		return nil, err
	}
	if err := s.pgStore.SetCommentReaction(ctx, commentID, userID, emoji); err != nil {
		return nil, err
	}
	summary, err := s.pgStore.GetCommentReactionSummary(ctx, commentID, &userID)
	if err != nil {
		return nil, err
	}
	summary.Emoji = &emoji

	s.publishCommentReactionEvents(ctx, comment, userID, emoji, true)
	s.publishCommentChange(comment.PostID, commentID, comment.ParentID, CommentChangeReaction, userID)
	return summary, nil
}

// RemoveCommentReaction deletes the viewer's reaction and returns the
// summary with emoji and viewer_reaction null. Removing a reaction that
// was never there is a no-op that still answers the summary.
func (s *Service) RemoveCommentReaction(ctx context.Context, commentID, userID uuid.UUID) (*postgres.CommentReactionSummary, error) {
	comment, err := s.visibleCommentForViewer(ctx, commentID, userID)
	if err != nil {
		return nil, err
	}
	removed, err := s.pgStore.RemoveCommentReaction(ctx, commentID, userID)
	if err != nil {
		return nil, err
	}
	summary, err := s.pgStore.GetCommentReactionSummary(ctx, commentID, &userID)
	if err != nil {
		return nil, err
	}
	summary.Emoji = nil
	summary.ViewerReaction = nil

	if removed {
		s.publishCommentReactionEvents(ctx, comment, userID, "", false)
	}
	s.publishCommentChange(comment.PostID, commentID, comment.ParentID, CommentChangeReaction, userID)
	return summary, nil
}

// publishCommentReactionEvents keeps the engagement stream and the
// notification event the like toggle always emitted: EventCommentLiked /
// EventCommentUnliked on the engagement topic, and CommentReacted (the
// social event notification-service turns into "X reacted to your comment")
// only when a reaction is set and it is not the author's own. reactType on
// the notification is "like" for the heart and the emoji otherwise.
func (s *Service) publishCommentReactionEvents(ctx context.Context, comment *postgres.Comment, userID uuid.UUID, emoji string, set bool) {
	if s.engProducer != nil {
		seq, actionTS := s.nextEngagementSeq(ctx, userID)
		eventType := engagement.EventCommentLiked
		if !set {
			eventType = engagement.EventCommentUnliked
		}
		event := engagement.BuildEvent(eventType, comment.PostID, userID, comment.AuthorID, comment.ID, "comment", "like", set, seq, actionTS)
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish comment reaction event: %v", err)
			}
		}()
	}
	if s.producer != nil && set && comment.AuthorID != userID {
		reactType := "like"
		if emoji != postgres.LikeEmoji {
			reactType = emoji
		}
		commentID, postID, authorID := comment.ID, comment.PostID, comment.AuthorID
		go func() {
			if err := s.producer.PublishCommentReacted(context.Background(), commentID, postID, authorID, userID, reactType); err != nil {
				log.Printf("Warning: failed to publish CommentReacted event: %v", err)
			}
		}()
	}
}

// nextEngagementSeq is the per-actor sequence the engagement events carry
// (eng:seq:<user>, 24h), the same counter CreateCommentPG advances.
func (s *Service) nextEngagementSeq(ctx context.Context, userID uuid.UUID) (int64, int64) {
	if s.rdb == nil {
		return 0, time.Now().UnixMicro()
	}
	seqKey := fmt.Sprintf("eng:seq:%s", userID)
	seq, _ := s.rdb.Incr(ctx, seqKey).Result()
	s.rdb.Expire(ctx, seqKey, 24*time.Hour)
	return seq, time.Now().UnixMicro()
}

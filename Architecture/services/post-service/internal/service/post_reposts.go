// post_reposts.go holds reposts (Echo): create, undo, the viewer's repost
// state (single and batch), and who reposted a post. Moved out of post.go
// unchanged.

package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Repost (Echo) Service Methods
// ---------------------------------------------------------------------------

// RepostResult is the response shape for repost create APIs.
type RepostResult struct {
	ID             uuid.UUID `json:"id"`
	OriginalPostID uuid.UUID `json:"original_post_id"`
	Type           string    `json:"type"`
	QuoteText      string    `json:"quote_text,omitempty"`
	Status         string    `json:"status"`
	CreatedAt      string    `json:"created_at"`
}

// CreateRepostInput holds all parameters for creating a repost.
type CreateRepostInput struct {
	UserID            uuid.UUID
	PostID            uuid.UUID
	Type              string // "plain" or "quote"
	QuoteText         string
	SourceContextType string
	SourceContextID   *uuid.UUID
}

// CreateRepost creates a plain or quote repost per the spec.
func (s *Service) CreateRepost(ctx context.Context, input CreateRepostInput) (*RepostResult, error) {
	// A repost publishes on the reposter's behalf: author standing first
	// (publish_standing.go). Cached per user, so it is cheap before the
	// rate limit.
	if err := s.requirePublishStanding(ctx, input.UserID); err != nil {
		return nil, err
	}
	// Rate limit
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:repost:%s", input.UserID), 30, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	// 1. Verify original post exists and is not deleted (GetPost filters deleted_at IS NULL)
	post, err := s.loadRepostTarget(ctx, input.PostID)
	if err != nil || post == nil {
		return nil, fmt.Errorf("POST_NOT_FOUND")
	}

	// 1b. A reel is never reposted, plain or quote (heart_only.go). An
	// existing reel repost stays; switching its type is a new repost and is
	// refused here too, while undo (UndoRepost) still works.
	if err := checkRepostContentType(post.ContentType); err != nil {
		return nil, err
	}

	// 2. Visibility check — private or followers-only posts cannot be reposted
	if !repostableVisibility(post.Visibility) {
		return nil, fmt.Errorf("NOT_ELIGIBLE")
	}

	// 3. Quote repost validation
	if input.Type == "quote" {
		text := strings.TrimSpace(input.QuoteText)
		if text == "" {
			return nil, fmt.Errorf("QUOTE_TEXT_REQUIRED")
		}
		if len([]rune(text)) > 500 {
			return nil, fmt.Errorf("QUOTE_TEXT_TOO_LONG")
		}
		input.QuoteText = text
	}

	// 4. Check if user already has an active repost
	existing, err := s.pgStore.GetActiveRepost(ctx, input.UserID, input.PostID)
	if err != nil {
		return nil, err
	}

	var repost *postgres.Repost

	if existing != nil {
		// Same type → 409 conflict
		if existing.RepostType == input.Type {
			return nil, fmt.Errorf("ALREADY_REPOSTED")
		}
		// Different type → switch (soft-delete old, create new, net-zero counter)
		repost, err = s.pgStore.SwitchRepostType(
			ctx, input.UserID, input.PostID,
			input.Type, input.QuoteText, post.Visibility,
			input.SourceContextType, input.SourceContextID,
		)
		if err != nil {
			return nil, err
		}
		// Net-zero counter change (decrement old + increment new), but we still
		// publish the event for feed fanout with the new repost.
	} else {
		// Fresh repost
		repost = &postgres.Repost{
			UserID:            input.UserID,
			OriginalPostID:    input.PostID,
			RepostType:        input.Type,
			QuoteText:         input.QuoteText,
			Visibility:        post.Visibility,
			SourceContextType: input.SourceContextType,
			SourceContextID:   input.SourceContextID,
		}
		if err := s.pgStore.CreateRepost(ctx, repost); err != nil {
			if err.Error() == "ALREADY_REPOSTED" {
				return nil, fmt.Errorf("ALREADY_REPOSTED")
			}
			return nil, err
		}
		// Increment the sharded post_engagement_counts.repost_count
		// (replaces the legacy per-event PG UPDATE that was the hot
		// row on viral reposts).
		if err := s.adjustEngagementCount(ctx, s.repostCounter, input.PostID, "repost_count", 1); err != nil {
			slog.Warn("failed to increment repost count", "error", err, "post_id", input.PostID)
		}
		repostCountKey := fmt.Sprintf("post:%s:repost_count", input.PostID)
		s.rdb.Incr(ctx, repostCountKey)
		s.rdb.Expire(ctx, repostCountKey, 7*24*time.Hour)
	}

	// Publish event
	if s.producer != nil {
		sourceCtxID := ""
		if repost.SourceContextID != nil {
			sourceCtxID = repost.SourceContextID.String()
		}
		go func() {
			if err := s.producer.PublishPostReposted(
				context.Background(),
				repost.ID, repost.UserID, repost.OriginalPostID, post.AuthorID,
				repost.RepostType, repost.QuoteText, repost.Visibility,
				repost.SourceContextType, sourceCtxID,
			); err != nil {
				slog.Warn("failed to publish post.reposted event", "error", err)
			}
		}()
	}
	s.invalidateFeedHydration(ctx, input.UserID, input.PostID)

	return &RepostResult{
		ID:             repost.ID,
		OriginalPostID: repost.OriginalPostID,
		Type:           repost.RepostType,
		QuoteText:      repost.QuoteText,
		Status:         repost.Status,
		CreatedAt:      repost.CreatedAt.Format(time.RFC3339),
	}, nil
}

// UndoRepost soft-deletes the active repost for (user, post) and decrements counters.
func (s *Service) UndoRepost(ctx context.Context, userID, postID uuid.UUID) error {
	// Look up the active repost so we can get its ID/type for the event
	existing, err := s.pgStore.GetActiveRepost(ctx, userID, postID)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("REPOST_NOT_FOUND")
	}

	// Fetch original post author for event
	post, _ := s.pgStore.GetPost(ctx, postID)
	var originalAuthorID uuid.UUID
	if post != nil {
		originalAuthorID = post.AuthorID
	}

	// Soft-delete
	if err := s.pgStore.SoftDeleteRepost(ctx, userID, postID); err != nil {
		return err
	}

	// Decrement the sharded post_engagement_counts.repost_count.
	if err := s.adjustEngagementCount(ctx, s.repostCounter, postID, "repost_count", -1); err != nil {
		slog.Warn("failed to decrement repost count", "error", err, "post_id", postID)
	}
	repostCountKey := fmt.Sprintf("post:%s:repost_count", postID)
	s.rdb.Decr(ctx, repostCountKey)

	// Publish undo event
	if s.producer != nil {
		go func() {
			if err := s.producer.PublishPostRepostUndone(
				context.Background(),
				existing.ID, userID, postID, originalAuthorID, existing.RepostType,
			); err != nil {
				slog.Warn("failed to publish post.repost_undone event", "error", err)
			}
		}()
	}
	s.invalidateFeedHydration(ctx, userID, postID)

	return nil
}

// RepostStateResult is the response shape for GET /posts/{postId}/repost/me.
type RepostStateResult struct {
	HasReposted bool       `json:"has_reposted"`
	RepostID    *uuid.UUID `json:"repost_id,omitempty"`
	Type        string     `json:"type,omitempty"`
	QuoteText   string     `json:"quote_text,omitempty"`
	CreatedAt   string     `json:"created_at,omitempty"`
}

// GetRepostState returns the current user's repost state for a given post.
func (s *Service) GetRepostState(ctx context.Context, userID, postID uuid.UUID) (*RepostStateResult, error) {
	repost, err := s.pgStore.GetActiveRepost(ctx, userID, postID)
	if err != nil {
		return nil, err
	}
	if repost == nil {
		return &RepostStateResult{HasReposted: false}, nil
	}
	return &RepostStateResult{
		HasReposted: true,
		RepostID:    &repost.ID,
		Type:        repost.RepostType,
		QuoteText:   repost.QuoteText,
		CreatedAt:   repost.CreatedAt.Format(time.RFC3339),
	}, nil
}

// ReposterItem is a single entry in the "who reposted this" list.
type ReposterItem struct {
	UserID     uuid.UUID `json:"user_id"`
	RepostedAt string    `json:"reposted_at"`
}

// ListRepostersResult is the response shape for GET /posts/{postId}/reposters.
type ListRepostersResult struct {
	Reposters  []ReposterItem `json:"reposters"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// ListReposters returns a paginated list of users who reposted a post.
func (s *Service) ListReposters(ctx context.Context, postID uuid.UUID, limit int, cursor string) (*ListRepostersResult, error) {
	reposts, nextCursor, err := s.pgStore.ListReposters(ctx, postID, limit, cursor)
	if err != nil {
		return nil, err
	}
	items := make([]ReposterItem, 0, len(reposts))
	for _, r := range reposts {
		items = append(items, ReposterItem{
			UserID:     r.UserID,
			RepostedAt: r.CreatedAt.Format(time.RFC3339),
		})
	}
	return &ListRepostersResult{Reposters: items, NextCursor: nextCursor}, nil
}

// UserRepostItem is a single repost in the user's profile reposts feed.
type UserRepostItem struct {
	RepostID       uuid.UUID `json:"repost_id"`
	Type           string    `json:"type"`
	QuoteText      string    `json:"quote_text,omitempty"`
	OriginalPostID uuid.UUID `json:"original_post_id"`
	CreatedAt      string    `json:"created_at"`
	// Post is the original post as THIS viewer may read it (the batch read:
	// the original author's privacy, blocks, review state, age gate, and the
	// members-only redaction). A repost whose original the viewer may not
	// read is left off the page, so this is always set on a listed item.
	Post *PostDetail `json:"post,omitempty"`
}

// ListUserRepostsResult is the response shape for GET
// /v1/creators/:creatorId/reposts (and the older /v1/users/:userId/reposts).
type ListUserRepostsResult struct {
	Items      []UserRepostItem `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// BatchGetRepostStates returns repost states for multiple posts for a single user.
// Used for hydrating viewer_context in feed responses.
func (s *Service) BatchGetRepostStates(ctx context.Context, userID uuid.UUID, postIDs []uuid.UUID) (map[uuid.UUID]*RepostStateResult, error) {
	reposts, err := s.pgStore.BatchGetActiveReposts(ctx, userID, postIDs)
	if err != nil {
		return nil, err
	}
	result := make(map[uuid.UUID]*RepostStateResult, len(postIDs))
	for _, pid := range postIDs {
		r, ok := reposts[pid]
		if !ok {
			result[pid] = &RepostStateResult{HasReposted: false}
			continue
		}
		result[pid] = &RepostStateResult{
			HasReposted: true,
			RepostID:    &r.ID,
			Type:        r.RepostType,
			QuoteText:   r.QuoteText,
			CreatedAt:   r.CreatedAt.Format(time.RFC3339),
		}
	}
	return result, nil
}

// post_engagement.go holds the viewer actions on a post other than
// comments and reactions: bookmarks, the like / bookmark toggles, shares,
// the Redis-backed "did I?" checks, unsaving an item, and reading a poll.
// Moved out of post.go unchanged.

package service

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// --- Bookmark methods ---

func (s *Service) AddBookmark(ctx context.Context, userID, postID uuid.UUID) error {
	if err := s.checkEngagementVisibility(ctx, postID, userID); err != nil {
		return err
	}
	if err := s.pgStore.AddBookmark(ctx, userID, postID); err != nil {
		return err
	}
	s.invalidateFeedHydration(ctx, userID, postID)
	return nil
}

func (s *Service) RemoveBookmark(ctx context.Context, userID, postID uuid.UUID) error {
	// No visibility gate on remove — a user who already bookmarked
	// must always be able to clean up their own row even if the
	// post's visibility tightened (author switched to followers-only).
	if err := s.pgStore.RemoveBookmark(ctx, userID, postID); err != nil {
		return err
	}
	s.invalidateFeedHydration(ctx, userID, postID)
	return nil
}

// GetBookmarks is the viewer's saved list. contentTypes is the canonical
// filter from the handler (nil means every type); it is applied in SQL so
// a page of long videos is a full page, not a page of everything with the
// flicks removed afterwards. scope narrows it to one saved collection or the
// default one (saved_collections.go; the zero scope is every saved post).
//
// A save outlives every later change to the post and its author, so the
// list applies the viewer gate other lists do (2026-10-30 audit B3): the
// store keeps live, effectively approved rows (the saver's own excepted),
// and postsVisibleToViewer drops what the saver may no longer see — an
// author who blocked them or went private, an audience they left, a
// deactivated author. The row stays in saved_items: it comes back if the
// post becomes visible to them again.
func (s *Service) GetBookmarks(ctx context.Context, userID uuid.UUID, contentTypes []string, limit int, cursor string, scope postgres.SavedScope) ([]PostDetail, string, error) {
	store := s.listReads()
	if store == nil {
		return nil, "", ErrAuthoringStoreUnavailable
	}
	// A page the gates empty reads on (list_pages.go).
	details, nextCursor, err := visiblePage(cursor,
		func(cur string) ([]postgres.Post, string, error) {
			return store.GetBookmarks(ctx, userID, contentTypes, limit, cur, scope)
		},
		func(posts []postgres.Post) ([]PostDetail, error) {
			posts = s.postsVisibleToViewer(ctx, &userID, posts)
			// Counts and polls for the page in batched reads (list_reads.go).
			details := s.pageDetails(ctx, &userID, posts)
			for i := range details {
				details[i].IsBookmarked = true
			}
			// Instant publish (processing.go): the bookmarker is the viewer.
			return s.attachMediaStateToDetails(ctx, details, &userID)
		})
	if err != nil {
		return nil, "", err
	}
	// Members-only rows: a bookmark outlives a membership (members_read.go).
	s.redactDetailsForViewer(ctx, &userID, details)
	ptrs := make([]*PostDetail, len(details))
	for i := range details {
		ptrs[i] = &details[i]
	}
	s.attachChannelRefs(ctx, userID, ptrs)
	return details, nextCursor, nil
}

// --- Poll methods ---

// GetPoll returns poll data with vote counts and optionally the viewer's votes.
func (s *Service) GetPoll(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) (*postgres.PollData, error) {
	poll, err := s.pgStore.GetPoll(ctx, postID)
	if err != nil {
		return nil, err
	}
	if poll == nil {
		return nil, nil
	}

	if viewerID != nil {
		votes, _ := s.pgStore.GetUserPollVotes(ctx, postID, *viewerID)
		poll.ViewerVotes = votes
	}

	return poll, nil
}

// CastVote and CastPollVote both live in polls.go now — one implementation
// serving both vote routes, returning the typed errors the handlers map to
// stable codes.

// ============================================================
// New Engagement System (dual-write: Redis hot path + async consumers)
// ============================================================

// LikeToggleResult is the response shape for the like toggle API.
type LikeToggleResult struct {
	Liked bool  `json:"liked"`
	Count int64 `json:"count"`
}

// ToggleLike executes the atomic Lua toggle and publishes an engagement event.
func (s *Service) ToggleLike(ctx context.Context, postID, userID uuid.UUID) (*LikeToggleResult, error) {
	// Audit C5 + H2: one fetch covers visibility, NoLikes flag, and
	// the author-id used for the PostReacted event below.
	// Previously the handler did a GetPost just to check NoLikes,
	// then the service did another GetPost just to read AuthorID —
	// two full Postgres+Scylla fetches per like toggle.
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

	// Lua atomic toggle
	result, err := engagement.ToggleLike(ctx, s.rdb, userID, postID)
	if err != nil {
		return nil, err
	}

	// Sync ScyllaDB reactions_by_post so feed hydration sees the change immediately
	if result.IsSet {
		if _, err := s.scyllaStore.React(ctx, postID, userID, "like"); err != nil {
			log.Printf("Warning: failed to write reaction to ScyllaDB: %v", err)
		}
		// Like and tune are mutually exclusive (tune.go): a like being
		// SET clears the viewer's private dislike.
		if err := s.pgStore.DeleteTune(ctx, userID, postID); err != nil {
			log.Printf("Warning: failed to clear tune on like: %v", err)
		}
	} else {
		if _, err := s.scyllaStore.Unreact(ctx, postID, userID); err != nil {
			log.Printf("Warning: failed to remove reaction from ScyllaDB: %v", err)
		}
	}
	// The liked list's record (liked_reels.go). Best-effort: the toggle has
	// already flipped, and a retried toggle would flip it back.
	s.indexReactionBestEffort(ctx, postID, userID, "like", result.IsSet)
	// A liked long video joins the viewer's "Loved" collection; an unliked
	// one leaves it (system_playlists.go; reels are never written there).
	s.syncLikedCollection(ctx, userID, post, result.IsSet)
	s.invalidateFeedHydration(ctx, userID, postID)

	// Author already loaded above — no second GetPost needed.
	authorID := post.AuthorID

	// Self-engagement check (return early but don't error, Lua already toggled)
	// We do the check here for the event publishing. The handler should block self-likes before calling this.

	// Build and publish engagement event async
	eventType := engagement.EventPostLiked
	if !result.IsSet {
		eventType = engagement.EventPostUnliked
	}

	if s.engProducer != nil {
		event := engagement.BuildEvent(eventType, postID, userID, authorID, postID, "post", "like", result.IsSet, result.Seq, result.ActionTS)
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish engagement event: %v", err)
			}
		}()
	}

	// Audit H4: route the legacy PostReacted notification trigger
	// through the outbox. Was fire-and-forget Kafka in a goroutine;
	// a crash window dropped the notification.
	if s.producer != nil && result.IsSet {
		payload := events.PostReactedPayload{
			PostID:       postID.String(),
			PostAuthorID: authorID.String(),
			ReactorID:    userID.String(),
			ReactType:    "like",
			CreatedAt:    time.Now(),
		}
		if err := s.pgStore.InsertOutboxEvent(ctx, events.PostReacted, "post", postID, payload); err != nil {
			log.Printf("Warning: failed to enqueue PostReacted (ToggleLike) to outbox: %v", err)
		}
	}

	return &LikeToggleResult{Liked: result.IsSet, Count: result.Count}, nil
}

// BookmarkToggleResult is the response shape for the bookmark toggle API.
type BookmarkToggleResult struct {
	Bookmarked bool `json:"bookmarked"`
}

// ToggleBookmarkNew executes the atomic Lua toggle for bookmarks.
// NO notification, NO WebSocket — bookmarks are completely private.
func (s *Service) ToggleBookmarkNew(ctx context.Context, postID, userID uuid.UUID) (*BookmarkToggleResult, error) {
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:bookmark:%s", userID), engagement.BookmarkLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	result, err := engagement.ToggleBookmark(ctx, s.rdb, userID, postID)
	if err != nil {
		return nil, err
	}

	// Keep the Postgres saved_items row in sync so the /v1/saved page mirrors
	// the post bookmark icon. Best-effort: failures here don't unwind the
	// Redis/Scylla bookmark — the icon's source of truth is still the
	// engagement layer.
	if result.IsSet {
		if _, err := s.pgStore.SaveItem(ctx, userID, "post", postID, nil); err != nil {
			log.Printf("Warning: failed to mirror bookmark into saved_items: %v", err)
		}
	} else {
		if err := s.pgStore.UnsaveItemByTarget(ctx, userID, "post", postID); err != nil {
			log.Printf("Warning: failed to remove bookmark from saved_items: %v", err)
		}
	}

	// Publish engagement event for durable write (ScyllaDB consumer only — no notification, no WS)
	eventType := engagement.EventPostBookmarked
	if !result.IsSet {
		eventType = engagement.EventPostUnbookmarked
	}

	if s.engProducer != nil {
		event := engagement.BuildEvent(eventType, postID, userID, uuid.Nil, postID, "post", "bookmark", result.IsSet, result.Seq, result.ActionTS)
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				log.Printf("Warning: failed to publish bookmark event: %v", err)
			}
		}()
	}

	return &BookmarkToggleResult{Bookmarked: result.IsSet}, nil
}

// ShareResult is the response shape for the share API.
type ShareResult struct {
	Shared bool  `json:"shared"`
	Count  int64 `json:"count"`
}

// SharePost creates a share record. Reposts are idempotent (409 on duplicate).
func (s *Service) SharePost(ctx context.Context, postID, userID uuid.UUID, shareType, quoteText string) (*ShareResult, error) {
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:share:%s", userID), engagement.ShareLimitPerHour, time.Hour) {
		return nil, fmt.Errorf("RATE_LIMITED")
	}

	// Check circle restriction
	post, err := s.loadRepostTarget(ctx, postID)
	if err != nil || post == nil {
		return nil, fmt.Errorf("POST_NOT_FOUND")
	}
	// A reel is never reposted (heart_only.go); an external share is not a
	// repost and is unaffected.
	if isRepostShareType(shareType) {
		if err := checkRepostContentType(post.ContentType); err != nil {
			return nil, err
		}
	}
	if shareRestricted(post.Visibility, shareType) {
		return nil, fmt.Errorf("CIRCLE_SHARE_RESTRICTED")
	}

	// Repost idempotency check
	if shareType == "repost" {
		shareKey := fmt.Sprintf("shared:%s:%s", userID, postID)
		exists, _ := s.rdb.Exists(ctx, shareKey).Result()
		if exists > 0 {
			return nil, fmt.Errorf("ALREADY_SHARED")
		}
	}

	// Update Redis counter + membership
	shareKey := fmt.Sprintf("shared:%s:%s", userID, postID)
	s.rdb.Set(ctx, shareKey, "1", 7*24*time.Hour)
	engKey := fmt.Sprintf("post:eng:%s", postID)
	newCount, _ := s.rdb.HIncrBy(ctx, engKey, "shares", 1).Result()

	// Get sequence for event
	seqKey := fmt.Sprintf("eng:seq:%s", userID)
	seq, _ := s.rdb.Incr(ctx, seqKey).Result()
	s.rdb.Expire(ctx, seqKey, 24*time.Hour)

	if s.engProducer != nil {
		event := engagement.BuildEvent(engagement.EventPostShared, postID, userID, post.AuthorID, postID, "post", "share", true, seq, time.Now().UnixMicro())
		event.ShareType = shareType
		event.QuoteText = quoteText
		go func() {
			if err := s.engProducer.Publish(context.Background(), event); err != nil {
				slog.Warn("failed to publish share event", "error", err)
			}
		}()
	}

	return &ShareResult{Shared: true, Count: newCount}, nil
}

// IsBookmarkedWithFallback checks Redis first, falls back to ScyllaDB.
func (s *Service) IsBookmarkedWithFallback(ctx context.Context, userID, postID uuid.UUID) bool {
	bmKey := fmt.Sprintf("bookmarked:%s:%s", userID, postID)
	val, err := s.rdb.Get(ctx, bmKey).Result()
	if err == nil {
		return val == "1"
	}

	// Cache miss → ScyllaDB fallback
	if s.scyllaSession != nil {
		var collection string
		if err := s.scyllaSession.Query(`
			SELECT collection FROM bookmark_check WHERE user_id = ? AND post_id = ?`,
			userID, postID,
		).WithContext(ctx).Scan(&collection); err == nil {
			s.rdb.Set(ctx, bmKey, "1", 24*time.Hour)
			return true
		}
	}

	// Negative cache (shorter TTL)
	s.rdb.Set(ctx, bmKey, "0", time.Hour)
	return false
}

// IsLikedFromRedis checks if the viewer liked a post via Redis.
func (s *Service) IsLikedFromRedis(ctx context.Context, userID, postID uuid.UUID) bool {
	key := fmt.Sprintf("liked:%s:%s", userID, postID)
	exists, _ := s.rdb.Exists(ctx, key).Result()
	return exists > 0
}

// IsSharedFromRedis checks if the viewer shared a post via Redis.
func (s *Service) IsSharedFromRedis(ctx context.Context, userID, postID uuid.UUID) bool {
	key := fmt.Sprintf("shared:%s:%s", userID, postID)
	exists, _ := s.rdb.Exists(ctx, key).Result()
	return exists > 0
}

// ============================================================
// Saved Items / Collections
// ============================================================

// SaveItem, ListSavedItems and the collection routes are in
// saved_collections.go (migration 074).

// UnsaveItem removes a saved item.
func (s *Service) UnsaveItem(ctx context.Context, savedID, userID uuid.UUID) error {
	return s.pgStore.UnsaveItem(ctx, savedID, userID)
}

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

const watchProgressTTL = 90 * 24 * time.Hour

// ErrWatchProgressNotFound: the viewer has never reported progress on the post.
var ErrWatchProgressNotFound = errors.New("watch progress not found")

// SaveWatchProgress upserts watch progress in both the DB and Redis cache.
func (s *Service) SaveWatchProgress(ctx context.Context, wp *postgres.WatchProgress) error {
	if err := s.pgStore.UpsertWatchProgress(ctx, wp); err != nil {
		return err
	}

	// Mirror to Redis hash for fast reads.
	key := fmt.Sprintf("watch_progress:%s:%s", wp.UserID, wp.PostID)
	fields := map[string]interface{}{
		"position_ms":     wp.PositionMs,
		"last_watched_at": time.Now().UTC().Format(time.RFC3339),
		"completed":       wp.Completed,
	}
	pipe := s.rdb.Pipeline()
	pipe.HSet(ctx, key, fields)
	pipe.Expire(ctx, key, watchProgressTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// GetWatchProgress tries Redis first, falls back to DB.
func (s *Service) GetWatchProgress(ctx context.Context, userID, postID uuid.UUID) (*postgres.WatchProgress, error) {
	key := fmt.Sprintf("watch_progress:%s:%s", userID, postID)
	vals, err := s.rdb.HGetAll(ctx, key).Result()
	if err == nil && len(vals) > 0 {
		// Partial hit — return a lightweight response from cache.
		wp := &postgres.WatchProgress{
			UserID: userID,
			PostID: postID,
		}
		if v, ok := vals["position_ms"]; ok {
			fmt.Sscanf(v, "%d", &wp.PositionMs) //nolint:errcheck
		}
		if v, ok := vals["completed"]; ok {
			wp.Completed = v == "1" || v == "true"
		}
		if v, ok := vals["last_watched_at"]; ok {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				wp.LastWatchedAt = t
			}
		}
		// Fill remaining fields from DB to avoid partial data exposure.
		dbWP, dbErr := s.pgStore.GetWatchProgress(ctx, userID, postID)
		if dbErr == nil && dbWP != nil {
			return dbWP, nil
		}
		return wp, nil
	}

	// Cache miss — go to DB.
	dbWP, err := s.pgStore.GetWatchProgress(ctx, userID, postID)
	if err != nil {
		return nil, err
	}
	if dbWP == nil {
		return nil, ErrWatchProgressNotFound
	}
	return dbWP, nil
}

// DeleteWatchProgress removes watch progress from DB and Redis.
func (s *Service) DeleteWatchProgress(ctx context.Context, userID, postID uuid.UUID) error {
	if err := s.pgStore.DeleteWatchProgress(ctx, userID, postID); err != nil {
		return err
	}
	key := fmt.Sprintf("watch_progress:%s:%s", userID, postID)
	return s.rdb.Del(ctx, key).Err()
}

// ContinueWatchingItem is one row of the Tube "Continue watching" shelf:
// the progress, plus the post it belongs to in the ordinary post shape
// (title, media with duration_ms / hls_url, counts) so the shelf renders
// without a second round trip.
type ContinueWatchingItem struct {
	postgres.WatchProgress
	Post *PostDetail `json:"post"`
}

// GetContinueWatching returns the viewer's incomplete watch progress,
// most recent first, each with its post hydrated through the batch read
// (GetPostsByIDs) as the viewer. A row whose post that read no longer
// returns — deleted, moderated out, or otherwise not the viewer's to see —
// is dropped: there is nothing to resume.
func (s *Service) GetContinueWatching(ctx context.Context, userID uuid.UUID, limit int) ([]ContinueWatchingItem, error) {
	rows, err := s.pgStore.GetContinueWatching(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	return s.hydrateWatchProgress(ctx, userID, rows)
}

// ErrInvalidWatchHistoryCursor: the cursor is not one this service issued.
var ErrInvalidWatchHistoryCursor = postgres.ErrInvalidWatchHistoryCursor

// GetWatchHistory is the viewer's full watch record (Tube "You" page,
// 2026-09-12): every progress row, finished or not, most recent first,
// keyset-paged. Items are the continue-watching shape, and a row whose
// post is gone is dropped the same way. The cursor is taken from the last
// DB row, not the last surviving item, so a run of deleted posts moves the
// reader forward instead of pinning them to one page.
func (s *Service) GetWatchHistory(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]ContinueWatchingItem, string, error) {
	rows, nextCursor, err := s.pgStore.GetWatchHistory(ctx, userID, limit, cursor)
	if err != nil {
		return nil, "", err
	}
	items, err := s.hydrateWatchProgress(ctx, userID, rows)
	if err != nil {
		return nil, "", err
	}
	return items, nextCursor, nil
}

// ClearWatchHistory removes every progress row of the viewer, then the
// Redis mirror of each. The DB is the record; the mirror is a read cache
// with a 90-day TTL, so a failed DEL is logged and not surfaced: the worst
// case is GET /progress answering from a stale hash until it expires, and
// the next SaveWatchProgress overwrites it anyway.
func (s *Service) ClearWatchHistory(ctx context.Context, userID uuid.UUID) error {
	postIDs, err := s.pgStore.DeleteAllWatchProgress(ctx, userID)
	if err != nil {
		return err
	}
	if s.rdb == nil || len(postIDs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(postIDs))
	for _, id := range postIDs {
		keys = append(keys, fmt.Sprintf("watch_progress:%s:%s", userID, id))
	}
	// Chunked: a viewer with years of history should not become one giant
	// DEL that blocks the Redis event loop.
	const chunk = 500
	for start := 0; start < len(keys); start += chunk {
		end := start + chunk
		if end > len(keys) {
			end = len(keys)
		}
		if err := s.rdb.Del(ctx, keys[start:end]...).Err(); err != nil {
			slog.WarnContext(ctx, "clear watch history: redis mirror not cleared", "user", userID, "keys", end-start, "err", err)
		}
	}
	return nil
}

// hydrateWatchProgress pairs progress rows with their posts, read through
// the batch read (GetPostsByIDs) as the viewer, and attaches the channel
// card each long video carries. Shared by the shelf and the history.
func (s *Service) hydrateWatchProgress(ctx context.Context, userID uuid.UUID, rows []postgres.WatchProgress) ([]ContinueWatchingItem, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.PostID)
	}
	posts, err := s.GetPostsByIDs(ctx, ids, &userID)
	if err != nil {
		return nil, fmt.Errorf("hydrate watch progress: %w", err)
	}
	// Tube: the channel card on each resumable long video.
	details := make([]*PostDetail, 0, len(posts))
	for _, p := range posts {
		details = append(details, p)
	}
	s.attachChannelRefs(ctx, userID, details)
	return attachContinueWatchingPosts(rows, posts), nil
}

// attachContinueWatchingPosts pairs progress rows with their posts, keeping
// the rows' order and dropping any without a post. Pure, for the test.
func attachContinueWatchingPosts(rows []postgres.WatchProgress, posts map[uuid.UUID]*PostDetail) []ContinueWatchingItem {
	out := make([]ContinueWatchingItem, 0, len(rows))
	for _, r := range rows {
		p, ok := posts[r.PostID]
		if !ok || p == nil {
			continue
		}
		out = append(out, ContinueWatchingItem{WatchProgress: r, Post: p})
	}
	return out
}

// SaveChapters replaces a post's chapters after authorising the caller as the
// post's author. The store DELETEs every existing chapter first, so an
// unauthorised call here erases a creator's set — the check has to come
// before the write, and has to fail closed. See video_authoring_authz.go.
func (s *Service) SaveChapters(ctx context.Context, callerID, postID uuid.UUID, chapters []postgres.MediaChapter) error {
	if err := s.requirePostAuthor(ctx, callerID, postID); err != nil {
		return err
	}
	return s.pgStore.SaveChapters(ctx, postID, chapters)
}

// GetChapters delegates to the store.
func (s *Service) GetChapters(ctx context.Context, postID uuid.UUID) ([]postgres.MediaChapter, error) {
	return s.pgStore.GetChapters(ctx, postID)
}

// SaveEndScreens replaces a post's end screens after authorising the caller
// as the post's author. Full replace: see SaveChapters.
func (s *Service) SaveEndScreens(ctx context.Context, callerID, postID uuid.UUID, screens []postgres.EndScreen) error {
	if err := s.requirePostAuthor(ctx, callerID, postID); err != nil {
		return err
	}
	return s.pgStore.SaveEndScreens(ctx, postID, screens)
}

// GetEndScreens delegates to the store.
func (s *Service) GetEndScreens(ctx context.Context, postID uuid.UUID) ([]postgres.EndScreen, error) {
	return s.pgStore.GetEndScreens(ctx, postID)
}

// SaveVideoCards replaces a post's cards after authorising the caller as the
// post's author. Full replace: see SaveChapters.
func (s *Service) SaveVideoCards(ctx context.Context, callerID, postID uuid.UUID, cards []postgres.VideoCard) error {
	if err := s.requirePostAuthor(ctx, callerID, postID); err != nil {
		return err
	}
	return s.pgStore.SaveVideoCards(ctx, postID, cards)
}

// GetVideoCards delegates to the store.
func (s *Service) GetVideoCards(ctx context.Context, postID uuid.UUID) ([]postgres.VideoCard, error) {
	return s.pgStore.GetVideoCards(ctx, postID)
}

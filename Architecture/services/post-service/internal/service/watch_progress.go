package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

const watchProgressTTL = 90 * 24 * time.Hour

// SaveWatchProgress upserts watch progress in both the DB and Redis cache.
func (s *Service) SaveWatchProgress(ctx context.Context, wp *postgres.WatchProgress) error {
	if err := s.pgStore.UpsertWatchProgress(ctx, wp); err != nil {
		return err
	}

	// Mirror to Redis hash for fast reads.
	key := fmt.Sprintf("watch_progress:%s:%s", wp.UserID, wp.PostID)
	fields := map[string]interface{}{
		"position_ms":    wp.PositionMs,
		"last_watched_at": time.Now().UTC().Format(time.RFC3339),
		"completed":      wp.Completed,
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
		return nil, fmt.Errorf("watch progress not found")
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

// GetContinueWatching returns incomplete watch progress items from the DB.
func (s *Service) GetContinueWatching(ctx context.Context, userID uuid.UUID, limit int) ([]postgres.WatchProgress, error) {
	return s.pgStore.GetContinueWatching(ctx, userID, limit)
}

// SaveChapters delegates to the store.
func (s *Service) SaveChapters(ctx context.Context, postID uuid.UUID, chapters []postgres.MediaChapter) error {
	return s.pgStore.SaveChapters(ctx, postID, chapters)
}

// GetChapters delegates to the store.
func (s *Service) GetChapters(ctx context.Context, postID uuid.UUID) ([]postgres.MediaChapter, error) {
	return s.pgStore.GetChapters(ctx, postID)
}

// SaveEndScreens delegates to the store.
func (s *Service) SaveEndScreens(ctx context.Context, postID uuid.UUID, screens []postgres.EndScreen) error {
	return s.pgStore.SaveEndScreens(ctx, postID, screens)
}

// GetEndScreens delegates to the store.
func (s *Service) GetEndScreens(ctx context.Context, postID uuid.UUID) ([]postgres.EndScreen, error) {
	return s.pgStore.GetEndScreens(ctx, postID)
}

// SaveVideoCards delegates to the store.
func (s *Service) SaveVideoCards(ctx context.Context, postID uuid.UUID, cards []postgres.VideoCard) error {
	return s.pgStore.SaveVideoCards(ctx, postID, cards)
}

// GetVideoCards delegates to the store.
func (s *Service) GetVideoCards(ctx context.Context, postID uuid.UUID) ([]postgres.VideoCard, error) {
	return s.pgStore.GetVideoCards(ctx, postID)
}

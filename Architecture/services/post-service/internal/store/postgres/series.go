package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type FlickSeries struct {
	ID           uuid.UUID `json:"id"`
	CreatorID    uuid.UUID `json:"creator_id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	CoverURL     *string   `json:"cover_url,omitempty"`
	EpisodeCount int       `json:"episode_count"`
	IsComplete   bool      `json:"is_complete"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type FlickSeriesItem struct {
	SeriesID   uuid.UUID `json:"series_id"`
	PostID     uuid.UUID `json:"post_id"`
	EpisodeNum int       `json:"episode_num"`
	AddedAt    time.Time `json:"added_at"`
}

func (s *Store) CreateFlickSeries(ctx context.Context, creatorID uuid.UUID, title, description string) (*FlickSeries, error) {
	fs := &FlickSeries{}
	err := s.db.QueryRow(ctx, `
		INSERT INTO flick_series (creator_id, title, description)
		VALUES ($1, $2, $3)
		RETURNING id, creator_id, title, description, cover_url, episode_count, is_complete, created_at, updated_at`,
		creatorID, title, description,
	).Scan(&fs.ID, &fs.CreatorID, &fs.Title, &fs.Description, &fs.CoverURL, &fs.EpisodeCount, &fs.IsComplete, &fs.CreatedAt, &fs.UpdatedAt)
	return fs, err
}

func (s *Store) GetFlickSeries(ctx context.Context, seriesID uuid.UUID) (*FlickSeries, error) {
	fs := &FlickSeries{}
	err := s.db.QueryRow(ctx, `
		SELECT id, creator_id, title, description, cover_url, episode_count, is_complete, created_at, updated_at
		FROM flick_series WHERE id = $1`, seriesID,
	).Scan(&fs.ID, &fs.CreatorID, &fs.Title, &fs.Description, &fs.CoverURL, &fs.EpisodeCount, &fs.IsComplete, &fs.CreatedAt, &fs.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return fs, err
}

func (s *Store) ListFlickSeriesByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]FlickSeries, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, creator_id, title, description, cover_url, episode_count, is_complete, created_at, updated_at
		FROM flick_series WHERE creator_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		creatorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var series []FlickSeries
	for rows.Next() {
		var fs FlickSeries
		if err := rows.Scan(&fs.ID, &fs.CreatorID, &fs.Title, &fs.Description, &fs.CoverURL, &fs.EpisodeCount, &fs.IsComplete, &fs.CreatedAt, &fs.UpdatedAt); err != nil {
			return nil, err
		}
		series = append(series, fs)
	}
	return series, rows.Err()
}

func (s *Store) AddEpisodeToSeries(ctx context.Context, seriesID, postID uuid.UUID, episodeNum int) (*FlickSeriesItem, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	item := &FlickSeriesItem{}
	err = tx.QueryRow(ctx, `
		INSERT INTO flick_series_items (series_id, post_id, episode_num)
		VALUES ($1, $2, $3)
		ON CONFLICT (series_id, episode_num) DO UPDATE SET post_id = EXCLUDED.post_id
		RETURNING series_id, post_id, episode_num, added_at`,
		seriesID, postID, episodeNum,
	).Scan(&item.SeriesID, &item.PostID, &item.EpisodeNum, &item.AddedAt)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx,
		`UPDATE flick_series SET episode_count = (SELECT COUNT(*) FROM flick_series_items WHERE series_id = $1), updated_at = NOW() WHERE id = $1`,
		seriesID)
	if err != nil {
		return nil, err
	}
	return item, tx.Commit(ctx)
}

func (s *Store) GetSeriesEpisodes(ctx context.Context, seriesID uuid.UUID) ([]FlickSeriesItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT series_id, post_id, episode_num, added_at
		FROM flick_series_items WHERE series_id = $1
		ORDER BY episode_num ASC`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []FlickSeriesItem
	for rows.Next() {
		var i FlickSeriesItem
		if err := rows.Scan(&i.SeriesID, &i.PostID, &i.EpisodeNum, &i.AddedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

func (s *Store) FollowSeries(ctx context.Context, seriesID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO flick_series_followers (series_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		seriesID, userID)
	return err
}

func (s *Store) UnfollowSeries(ctx context.Context, seriesID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM flick_series_followers WHERE series_id = $1 AND user_id = $2`,
		seriesID, userID)
	return err
}

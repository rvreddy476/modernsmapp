package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// VideoSeries represents a named series of video episodes created by a creator.
type VideoSeries struct {
	ID             uuid.UUID  `json:"id"`
	CreatorID      uuid.UUID  `json:"creator_id"`
	ChannelID      *uuid.UUID `json:"channel_id,omitempty"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	CoverMediaID   *uuid.UUID `json:"cover_media_id,omitempty"`
	TrailerPostID  *uuid.UUID `json:"trailer_post_id,omitempty"`
	EpisodeCount   int        `json:"episode_count"`
	IsComplete     bool       `json:"is_complete"`
	IsPublic       bool       `json:"is_public"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// VideoSeriesEpisode links a post to a video series at a specific episode number.
type VideoSeriesEpisode struct {
	SeriesID   uuid.UUID `json:"series_id"`
	PostID     uuid.UUID `json:"post_id"`
	EpisodeNum int       `json:"episode_num"`
	Title      *string   `json:"title,omitempty"`
	AddedAt    time.Time `json:"added_at"`
}

// CreateVideoSeries inserts a new video series and populates id, created_at, updated_at.
func (s *Store) CreateVideoSeries(ctx context.Context, vs *VideoSeries) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO video_series (creator_id, channel_id, title, description, cover_media_id, trailer_post_id, is_complete, is_public)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at`,
		vs.CreatorID, vs.ChannelID, vs.Title, vs.Description, vs.CoverMediaID, vs.TrailerPostID, vs.IsComplete, vs.IsPublic,
	).Scan(&vs.ID, &vs.CreatedAt, &vs.UpdatedAt)
}

// GetVideoSeries retrieves a video series by ID. Returns nil, nil if not found.
func (s *Store) GetVideoSeries(ctx context.Context, id uuid.UUID) (*VideoSeries, error) {
	vs := &VideoSeries{}
	err := s.db.QueryRow(ctx, `
		SELECT id, creator_id, channel_id, title, description, cover_media_id, trailer_post_id,
		       episode_count, is_complete, is_public, created_at, updated_at
		FROM video_series WHERE id = $1`, id,
	).Scan(
		&vs.ID, &vs.CreatorID, &vs.ChannelID, &vs.Title, &vs.Description,
		&vs.CoverMediaID, &vs.TrailerPostID, &vs.EpisodeCount, &vs.IsComplete,
		&vs.IsPublic, &vs.CreatedAt, &vs.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return vs, err
}

// ListVideoSeriesByCreator returns paginated video series for a creator.
func (s *Store) ListVideoSeriesByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]VideoSeries, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, creator_id, channel_id, title, description, cover_media_id, trailer_post_id,
		       episode_count, is_complete, is_public, created_at, updated_at
		FROM video_series WHERE creator_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		creatorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []VideoSeries
	for rows.Next() {
		var vs VideoSeries
		if err := rows.Scan(
			&vs.ID, &vs.CreatorID, &vs.ChannelID, &vs.Title, &vs.Description,
			&vs.CoverMediaID, &vs.TrailerPostID, &vs.EpisodeCount, &vs.IsComplete,
			&vs.IsPublic, &vs.CreatedAt, &vs.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, vs)
	}
	return result, rows.Err()
}

// AddEpisodeToVideoSeries inserts a new episode and increments the series episode_count in a single tx.
func (s *Store) AddEpisodeToVideoSeries(ctx context.Context, seriesID, postID uuid.UUID, episodeNum int, title *string) (*VideoSeriesEpisode, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	ep := &VideoSeriesEpisode{}
	err = tx.QueryRow(ctx, `
		INSERT INTO video_series_episodes (series_id, post_id, episode_num, title)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (series_id, episode_num) DO UPDATE SET post_id = EXCLUDED.post_id, title = EXCLUDED.title
		RETURNING series_id, post_id, episode_num, title, added_at`,
		seriesID, postID, episodeNum, title,
	).Scan(&ep.SeriesID, &ep.PostID, &ep.EpisodeNum, &ep.Title, &ep.AddedAt)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx,
		`UPDATE video_series SET episode_count = (SELECT COUNT(*) FROM video_series_episodes WHERE series_id = $1), updated_at = NOW() WHERE id = $1`,
		seriesID)
	if err != nil {
		return nil, err
	}
	return ep, tx.Commit(ctx)
}

// GetVideoSeriesEpisodes returns all episodes for a series ordered by episode_num.
func (s *Store) GetVideoSeriesEpisodes(ctx context.Context, seriesID uuid.UUID) ([]VideoSeriesEpisode, error) {
	rows, err := s.db.Query(ctx, `
		SELECT series_id, post_id, episode_num, title, added_at
		FROM video_series_episodes WHERE series_id = $1
		ORDER BY episode_num ASC`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var eps []VideoSeriesEpisode
	for rows.Next() {
		var ep VideoSeriesEpisode
		if err := rows.Scan(&ep.SeriesID, &ep.PostID, &ep.EpisodeNum, &ep.Title, &ep.AddedAt); err != nil {
			return nil, err
		}
		eps = append(eps, ep)
	}
	return eps, rows.Err()
}

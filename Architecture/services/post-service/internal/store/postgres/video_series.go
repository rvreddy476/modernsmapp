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
	ID            uuid.UUID  `json:"id"`
	CreatorID     uuid.UUID  `json:"creator_id"`
	ChannelID     *uuid.UUID `json:"channel_id,omitempty"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	CoverMediaID  *uuid.UUID `json:"cover_media_id,omitempty"`
	TrailerPostID *uuid.UUID `json:"trailer_post_id,omitempty"`
	EpisodeCount  int        `json:"episode_count"`
	IsComplete    bool       `json:"is_complete"`
	IsPublic      bool       `json:"is_public"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
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

// ErrEpisodePostAlreadyInSeries is returned when a post is already an episode
// of the series at a DIFFERENT episode number. The table's primary key is
// (series_id, episode_num), so nothing stopped one post occupying episode 1
// and episode 3 of the same series — the watch page's next-episode control
// then loops back to the video already playing. Re-adding a post at the
// number it already holds is still an update, not a conflict.
var ErrEpisodePostAlreadyInSeries = errors.New("post is already an episode of this series")

// AddEpisodeToVideoSeries inserts a new episode and recomputes the series
// episode_count in a single tx.
//
// The guard against one post occupying two episode numbers lives INSIDE the
// transaction, as a WHERE NOT EXISTS on the insert itself rather than a
// read-then-write: a service-level pre-check alone would leave a window where
// two concurrent adds both pass. Returns ErrEpisodePostAlreadyInSeries when
// the guard fires.
func (s *Store) AddEpisodeToVideoSeries(ctx context.Context, seriesID, postID uuid.UUID, episodeNum int, title *string) (*VideoSeriesEpisode, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	ep := &VideoSeriesEpisode{}
	err = tx.QueryRow(ctx, `
		INSERT INTO video_series_episodes (series_id, post_id, episode_num, title)
		SELECT $1, $2, $3, $4
		WHERE NOT EXISTS (
			SELECT 1 FROM video_series_episodes
			WHERE series_id = $1 AND post_id = $2 AND episode_num <> $3
		)
		ON CONFLICT (series_id, episode_num) DO UPDATE SET post_id = EXCLUDED.post_id, title = EXCLUDED.title
		RETURNING series_id, post_id, episode_num, title, added_at`,
		seriesID, postID, episodeNum, title,
	).Scan(&ep.SeriesID, &ep.PostID, &ep.EpisodeNum, &ep.Title, &ep.AddedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The SELECT produced no row, so the WHERE NOT EXISTS matched: this
		// post is already an episode elsewhere in the series.
		return nil, ErrEpisodePostAlreadyInSeries
	}
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

// FindVideoSeriesEpisodeByPost returns the episode a post already occupies in
// a series, or nil, nil when it occupies none. Used to tell a creator WHICH
// episode number a duplicate add collides with.
func (s *Store) FindVideoSeriesEpisodeByPost(ctx context.Context, seriesID, postID uuid.UUID) (*VideoSeriesEpisode, error) {
	ep := &VideoSeriesEpisode{}
	err := s.db.QueryRow(ctx, `
		SELECT series_id, post_id, episode_num, title, added_at
		FROM video_series_episodes WHERE series_id = $1 AND post_id = $2
		ORDER BY episode_num ASC LIMIT 1`, seriesID, postID,
	).Scan(&ep.SeriesID, &ep.PostID, &ep.EpisodeNum, &ep.Title, &ep.AddedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return ep, err
}

// DeleteVideoSeries removes a series. Its episodes go with it: the
// video_series_episodes FK is ON DELETE CASCADE (012_posttube_features.sql).
// The posts themselves are untouched — an episode row is a link, not the
// video.
func (s *Store) DeleteVideoSeries(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM video_series WHERE id = $1`, id)
	return err
}

// DeleteVideoSeriesEpisodeByNum removes the episode at episodeNum and
// recomputes episode_count in the same tx. Reports whether a row was removed
// so the handler can answer 404 for an episode that was never there.
func (s *Store) DeleteVideoSeriesEpisodeByNum(ctx context.Context, seriesID uuid.UUID, episodeNum int) (bool, error) {
	return s.deleteVideoSeriesEpisode(ctx, seriesID,
		`DELETE FROM video_series_episodes WHERE series_id = $1 AND episode_num = $2`, episodeNum)
}

// DeleteVideoSeriesEpisodeByPost removes whichever episode a post occupies.
// The creator tools hold post ids, not episode numbers, so both spellings of
// the delete resolve here.
func (s *Store) DeleteVideoSeriesEpisodeByPost(ctx context.Context, seriesID, postID uuid.UUID) (bool, error) {
	return s.deleteVideoSeriesEpisode(ctx, seriesID,
		`DELETE FROM video_series_episodes WHERE series_id = $1 AND post_id = $2`, postID)
}

// deleteVideoSeriesEpisode is the shared body: delete, then recompute
// episode_count from the rows that remain — the same COUNT(*) recomputation
// the add path uses, so the two can never disagree about the total.
//
// Episode NUMBERS are deliberately left alone; see the renumbering note in
// internal/service/video_series.go.
func (s *Store) deleteVideoSeriesEpisode(ctx context.Context, seriesID uuid.UUID, deleteSQL string, arg any) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, deleteSQL, seriesID, arg)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE video_series SET episode_count = (SELECT COUNT(*) FROM video_series_episodes WHERE series_id = $1), updated_at = NOW() WHERE id = $1`,
		seriesID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
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

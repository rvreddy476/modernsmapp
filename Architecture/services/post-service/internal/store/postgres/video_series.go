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

// MaxSeriesEpisodes is the server's bound on how many episodes one series
// holds. It is a safety limit, not the product's: the founder's editor lets a
// creator add 3 episodes, and that number lives on the client where it can
// change without a deploy. 50 exists because the episode list is unpaginated
// (GetVideoSeriesEpisodes returns every row and the watch page renders all
// of them), so a series that grew without bound would eventually be a
// response nobody can load. The number is well above anything the editor
// produces and well below anything that hurts.
const MaxSeriesEpisodes = 50

// ErrVideoSeriesFull is returned when an add would grow the series past
// MaxSeriesEpisodes. Overwriting an occupied episode number is not growth
// and is never refused on this ground.
var ErrVideoSeriesFull = errors.New("video series is full")

// AddEpisodeToVideoSeries inserts a new episode and recomputes the series
// episode_count in a single tx.
//
// The guard against one post occupying two episode numbers lives INSIDE the
// transaction, as a WHERE NOT EXISTS on the insert itself rather than a
// read-then-write: a service-level pre-check alone would leave a window where
// two concurrent adds both pass. Returns ErrEpisodePostAlreadyInSeries when
// the guard fires.
//
// The MaxSeriesEpisodes cap is a count, and a count cannot be folded into
// the insert's WHERE the way the duplicate guard is without racing: two
// concurrent adds to a series of 49 would each count 49 and both insert. So
// the series row is locked first (SELECT ... FOR UPDATE), which serialises
// every add to one series behind that lock; the count taken afterwards is
// then exact. The lock is on video_series, which the episode_count
// recompute at the end updates anyway, so no extra row is contended.
func (s *Store) AddEpisodeToVideoSeries(ctx context.Context, seriesID, postID uuid.UUID, episodeNum int, title *string) (*VideoSeriesEpisode, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var locked uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM video_series WHERE id = $1 FOR UPDATE`, seriesID).Scan(&locked); err != nil {
		// The service has already answered "no such series" by the time we
		// are here; ErrNoRows would mean it vanished between the two reads.
		return nil, err
	}
	var count int
	var occupied bool
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(bool_or(episode_num = $2), FALSE)
		FROM video_series_episodes WHERE series_id = $1`, seriesID, episodeNum,
	).Scan(&count, &occupied); err != nil {
		return nil, err
	}
	if count >= MaxSeriesEpisodes && !occupied {
		return nil, ErrVideoSeriesFull
	}

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

// GetVideoSeriesEpisodes returns the episodes of a series ordered by
// episode_num.
//
// An episode row is a link to a post, and the post can be in a state the
// audience must not see: soft-deleted (posts.deleted_at, migration 007) or
// scheduled and not yet live (posts.publish_at IS NOT NULL, the contract
// migration 042 fixed: the schedule worker clears it at publish time, so a
// non-null value means "not live" whether or not the moment has passed;
// every other live-post read in this store uses the same two predicates).
// Without the filter the watch page's next-episode control would step onto a
// video the viewer then cannot load.
//
// includeUnpublished=true is the creator's view: they need to see the
// scheduled episode they just placed and the deleted one they can restore.
// The join is a LEFT JOIN rather than an INNER one so an episode whose post
// row is missing still lists for the owner (the FK cascades on hard delete,
// so in practice there is none, but the read must not silently shrink if
// that ever changes).
func (s *Store) GetVideoSeriesEpisodes(ctx context.Context, seriesID uuid.UUID, includeUnpublished bool) ([]VideoSeriesEpisode, error) {
	q := `
		SELECT e.series_id, e.post_id, e.episode_num, e.title, e.added_at
		FROM video_series_episodes e
		WHERE e.series_id = $1
		ORDER BY e.episode_num ASC`
	if !includeUnpublished {
		q = `
		SELECT e.series_id, e.post_id, e.episode_num, e.title, e.added_at
		FROM video_series_episodes e
		LEFT JOIN posts p ON p.id = e.post_id
		WHERE e.series_id = $1
		  AND p.deleted_at IS NULL
		  AND p.publish_at IS NULL
		ORDER BY e.episode_num ASC`
	}
	rows, err := s.db.Query(ctx, q, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVideoSeriesEpisodes(rows)
}

// FindSeriesMembershipsByPost returns every episode row that points at
// postID, newest membership first. A post can be an episode of more than one
// series (nothing in the schema forbids it, and a creator may legitimately
// file one video under two collections); the watch page shows one, and the
// one the creator added most recently is the best guess at the one they
// mean. Uses idx_video_series_episodes_post.
func (s *Store) FindSeriesMembershipsByPost(ctx context.Context, postID uuid.UUID) ([]VideoSeriesEpisode, error) {
	rows, err := s.db.Query(ctx, `
		SELECT series_id, post_id, episode_num, title, added_at
		FROM video_series_episodes WHERE post_id = $1
		ORDER BY added_at DESC, series_id ASC`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVideoSeriesEpisodes(rows)
}

func scanVideoSeriesEpisodes(rows pgx.Rows) ([]VideoSeriesEpisode, error) {
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

// VideoSeriesPatch is a partial update: a nil field is "leave it alone".
// The nullable references (channel, cover, trailer) can therefore be set
// but not cleared through this shape; clearing would need an explicit
// "set to null" signal the wire contract does not carry yet.
type VideoSeriesPatch struct {
	Title         *string
	Description   *string
	IsComplete    *bool
	IsPublic      *bool
	CoverMediaID  *uuid.UUID
	TrailerPostID *uuid.UUID
	ChannelID     *uuid.UUID
}

// UpdateVideoSeries applies the non-nil fields of patch and returns the row
// as it now stands. Returns nil, nil when there is no such series. COALESCE
// per column keeps this one statement for any subset of fields; the
// alternative, building SQL from whichever fields are set, is where
// injection bugs and off-by-one placeholders come from.
func (s *Store) UpdateVideoSeries(ctx context.Context, id uuid.UUID, patch VideoSeriesPatch) (*VideoSeries, error) {
	vs := &VideoSeries{}
	err := s.db.QueryRow(ctx, `
		UPDATE video_series SET
			title           = COALESCE($2, title),
			description     = COALESCE($3, description),
			is_complete     = COALESCE($4, is_complete),
			is_public       = COALESCE($5, is_public),
			cover_media_id  = COALESCE($6, cover_media_id),
			trailer_post_id = COALESCE($7, trailer_post_id),
			channel_id      = COALESCE($8, channel_id),
			updated_at      = NOW()
		WHERE id = $1
		RETURNING id, creator_id, channel_id, title, description, cover_media_id, trailer_post_id,
		          episode_count, is_complete, is_public, created_at, updated_at`,
		id, patch.Title, patch.Description, patch.IsComplete, patch.IsPublic,
		patch.CoverMediaID, patch.TrailerPostID, patch.ChannelID,
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

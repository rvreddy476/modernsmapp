package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Trending sounds (2026-10-24, migration 030). The rules live in
// service/trending_sounds.go; this file stores and reads the ranking.

// LibraryFacts is what the curated library row of a platform sound says
// about whether it may still be offered: active, not withdrawn, licence not
// lapsed. Present is false for every extracted sound (no library row).
type LibraryFacts struct {
	Present          bool
	IsActive         bool
	WithdrawnAt      *time.Time
	LicenseExpiresAt *time.Time
}

// TrendingCandidate is a sound the ranking run considers, with its library
// row's facts.
type TrendingCandidate struct {
	Track   AudioTrack
	Library LibraryFacts
}

// TrendingSound is one stored row of the ranking.
type TrendingSound struct {
	AudioTrackID uuid.UUID
	Rank         int
	Score        int
	Uses7d       int
	UsesPrev7d   int
	Rising       bool
}

// TrendingEntry is a stored row read back with its sound.
type TrendingEntry struct {
	TrendingSound
	Track   AudioTrack
	Library LibraryFacts
}

// TrendingRun is the last run's record.
type TrendingRun struct {
	ComputedAt time.Time
	SoundCount int
}

// trendingTrackFrom is audio_tracks with its library row's facts, as one
// relation x, so audioTrackCols resolve against it unqualified.
const trendingTrackFrom = `(
	SELECT t.*,
	       (l.id IS NOT NULL)          AS lib_present,
	       COALESCE(l.is_active, FALSE) AS lib_active,
	       l.withdrawn_at               AS lib_withdrawn_at,
	       l.license_expires_at         AS lib_expires_at
	  FROM audio_tracks t
	  LEFT JOIN audio_library l ON l.id = t.library_id
) x`

const trendingLibraryCols = `x.lib_present, x.lib_active, x.lib_withdrawn_at, x.lib_expires_at`

func scanTrendingTrack(dest []any, a *AudioTrack, l *LibraryFacts) []any {
	return append(dest,
		&a.ID, &a.SourceMediaID, &a.SourceReelID, &a.Title, &a.Artist, &a.Genre,
		&a.AudioKey, &a.WaveformKey, &a.DurationMs, &a.SampleRate, &a.Status, &a.IsOriginal,
		&a.LicenseType, &a.UsageCount, &a.UseCount, &a.CreatorUserID,
		&a.LibraryID, &a.CreatedAt, &a.UpdatedAt, &a.LibraryCoverURL, &a.ReuseBlock,
		&l.Present, &l.IsActive, &l.WithdrawnAt, &l.LicenseExpiresAt,
	)
}

// TrendingCandidates reads the sounds named by ids with their library
// facts. An id with no row is absent from the map.
func (s *MediaAssetStore) TrendingCandidates(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]TrendingCandidate, error) {
	out := make(map[uuid.UUID]TrendingCandidate, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+audioTrackCols+`, `+trendingLibraryCols+`
		  FROM `+trendingTrackFrom+`
		 WHERE x.id = ANY($1)
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c TrendingCandidate
		if err := rows.Scan(scanTrendingTrack(nil, &c.Track, &c.Library)...); err != nil {
			return nil, err
		}
		out[c.Track.ID] = c
	}
	return out, rows.Err()
}

// ReplaceTrendingSounds stores a run: every row of the previous one goes,
// these take their place, and the run is recorded, in one transaction.
func (s *MediaAssetStore) ReplaceTrendingSounds(ctx context.Context, computedAt time.Time, ranked []TrendingSound) error {
	ids := make([]uuid.UUID, len(ranked))
	ranks := make([]int32, len(ranked))
	scores := make([]int32, len(ranked))
	uses := make([]int32, len(ranked))
	prev := make([]int32, len(ranked))
	rising := make([]bool, len(ranked))
	for i, r := range ranked {
		ids[i], ranks[i], scores[i], uses[i], prev[i], rising[i] =
			r.AudioTrackID, int32(r.Rank), int32(r.Score), int32(r.Uses7d), int32(r.UsesPrev7d), r.Rising
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM audio_trending_sounds`); err != nil {
		return fmt.Errorf("clear trending sounds: %w", err)
	}
	if len(ranked) > 0 {
		// Only sounds that still exist: one deleted since the run read it
		// would refuse the whole insert on its foreign key.
		if _, err := tx.Exec(ctx, `
			INSERT INTO audio_trending_sounds (audio_track_id, rank, score, uses_7d, uses_prev_7d, rising, computed_at)
			SELECT r.id, r.rank, r.score, r.uses, r.prev, r.rising, $7
			  FROM unnest($1::uuid[], $2::int[], $3::int[], $4::int[], $5::int[], $6::bool[])
			       AS r(id, rank, score, uses, prev, rising)
			  JOIN audio_tracks t ON t.id = r.id
		`, ids, ranks, scores, uses, prev, rising, computedAt); err != nil {
			return fmt.Errorf("insert trending sounds: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audio_trending_runs (singleton, computed_at, sound_count)
		VALUES (TRUE, $1, $2)
		ON CONFLICT (singleton) DO UPDATE SET computed_at = EXCLUDED.computed_at, sound_count = EXCLUDED.sound_count
	`, computedAt, len(ranked)); err != nil {
		return fmt.Errorf("record trending run: %w", err)
	}
	return tx.Commit(ctx)
}

// ListTrendingSounds reads the stored ranking after afterRank, in rank
// order, at most limit rows; risingOnly keeps the rising ones.
func (s *MediaAssetStore) ListTrendingSounds(ctx context.Context, risingOnly bool, afterRank, limit int) ([]TrendingEntry, error) {
	if limit < 1 {
		return nil, errors.New("trending sounds limit must be at least 1")
	}
	rows, err := s.db.Query(ctx, `
		SELECT ts.audio_track_id, ts.rank, ts.score, ts.uses_7d, ts.uses_prev_7d, ts.rising,
		       `+audioTrackCols+`, `+trendingLibraryCols+`
		  FROM audio_trending_sounds ts
		  JOIN `+trendingTrackFrom+` ON x.id = ts.audio_track_id
		 WHERE ts.rank > $1
		   AND ($2::bool = FALSE OR ts.rising)
		 ORDER BY ts.rank ASC
		 LIMIT $3
	`, afterRank, risingOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrendingEntry
	for rows.Next() {
		var e TrendingEntry
		dest := []any{&e.AudioTrackID, &e.Rank, &e.Score, &e.Uses7d, &e.UsesPrev7d, &e.Rising}
		if err := rows.Scan(scanTrendingTrack(dest, &e.Track, &e.Library)...); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// TrendingSoundByID is the stored row of one sound with its library facts;
// (nil, nil) when the sound is not in the ranking.
func (s *MediaAssetStore) TrendingSoundByID(ctx context.Context, audioID uuid.UUID) (*TrendingEntry, error) {
	var e TrendingEntry
	dest := []any{&e.AudioTrackID, &e.Rank, &e.Score, &e.Uses7d, &e.UsesPrev7d, &e.Rising}
	err := s.db.QueryRow(ctx, `
		SELECT ts.audio_track_id, ts.rank, ts.score, ts.uses_7d, ts.uses_prev_7d, ts.rising,
		       `+audioTrackCols+`, `+trendingLibraryCols+`
		  FROM audio_trending_sounds ts
		  JOIN `+trendingTrackFrom+` ON x.id = ts.audio_track_id
		 WHERE ts.audio_track_id = $1
	`, audioID).Scan(scanTrendingTrack(dest, &e.Track, &e.Library)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// LatestTrendingRun is the last run; (nil, nil) before the first.
func (s *MediaAssetStore) LatestTrendingRun(ctx context.Context) (*TrendingRun, error) {
	var r TrendingRun
	err := s.db.QueryRow(ctx, `SELECT computed_at, sound_count FROM audio_trending_runs WHERE singleton`).
		Scan(&r.ComputedAt, &r.SoundCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// WithTrendingLease runs fn while this process holds the trending run's
// session advisory lock, and reports whether it did. A replica that does
// not get the lock skips the run: another one is computing it. PostgreSQL
// releases the lock with the connection if the process dies.
func (s *MediaAssetStore) WithTrendingLease(ctx context.Context, fn func(ctx context.Context) error) (bool, error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("acquire trending lease connection: %w", err)
	}
	defer conn.Release()
	key := advisoryLockKey("media-service-trending-sounds")
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		return false, fmt.Errorf("try trending lease: %w", err)
	}
	if !got {
		return false, nil
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key) }()
	return true, fn(ctx)
}

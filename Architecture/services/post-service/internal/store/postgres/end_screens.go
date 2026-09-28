package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// End screens and cards that viewers see (MTube, 2026-09-29; migration 054).
// The reads the save rules and the viewer resolution need, and the per-day
// impression / click counters. Rules live in service/end_screens.go.

// EndScreenSubject is what the save rules need to know about the video the
// elements sit on. DurationMs is 0 while unknown (media not probed yet).
type EndScreenSubject struct {
	PostID      uuid.UUID
	AuthorID    uuid.UUID
	ContentType string
	MadeForKids bool
	DurationMs  int
}

// GetEndScreenSubject reads the subject post; nil when it does not exist or
// is soft-deleted. The duration is the longest attached video asset's
// (media_assets, what the player plays), else the transcode measurement on
// video_metadata.
func (s *Store) GetEndScreenSubject(ctx context.Context, postID uuid.UUID) (*EndScreenSubject, error) {
	var sub EndScreenSubject
	err := s.db.QueryRow(ctx, `
		SELECT p.id, p.author_id, p.content_type, COALESCE(p.is_made_for_kids, false),
		       GREATEST(
		           COALESCE((SELECT MAX(COALESCE(ma.duration_ms, ma.duration_seconds * 1000))
		                     FROM post_media pm JOIN media_assets ma ON ma.id = pm.media_id
		                     WHERE pm.post_id = p.id AND ma.file_type = 'video'), 0),
		           COALESCE((SELECT (vm.duration_seconds * 1000)::bigint
		                     FROM video_metadata vm WHERE vm.post_id = p.id), 0))::int
		FROM posts p
		WHERE p.id = $1 AND p.deleted_at IS NULL`, postID,
	).Scan(&sub.PostID, &sub.AuthorID, &sub.ContentType, &sub.MadeForKids, &sub.DurationMs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

// PostTargetFacts is what the save rules check about a post an element or a
// card points at.
type PostTargetFacts struct {
	ID         uuid.UUID
	AuthorID   uuid.UUID
	Visibility string
	HasPoll    bool
}

// GetPostTargetFacts reads a target post; nil when missing or soft-deleted.
func (s *Store) GetPostTargetFacts(ctx context.Context, postID uuid.UUID) (*PostTargetFacts, error) {
	var f PostTargetFacts
	err := s.db.QueryRow(ctx, `
		SELECT p.id, p.author_id, p.visibility,
		       EXISTS (SELECT 1 FROM polls pl WHERE pl.post_id = p.id)
		FROM posts p
		WHERE p.id = $1 AND p.deleted_at IS NULL`, postID,
	).Scan(&f.ID, &f.AuthorID, &f.Visibility, &f.HasPoll)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ChannelPublicVideoIDs lists an owner's public long videos, newest first,
// leaving out excludeID: the candidates behind an end screen's "latest" and
// "popular" video. Same predicate as the channel page's public video count
// (channelVideoCountWhere).
func (s *Store) ChannelPublicVideoIDs(ctx context.Context, ownerID, excludeID uuid.UUID, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM posts
		WHERE author_id = $1
		  AND id <> $2
		  AND content_type IN ('long_video', 'video')
		  AND deleted_at IS NULL
		  AND visibility = 'public'
		  AND review_status = 'approved'
		  AND publish_at IS NULL
		ORDER BY COALESCE(published_at, created_at) DESC, id DESC
		LIMIT $3`, ownerID, excludeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ElementStats is one element's (or card's) summed counters.
type ElementStats struct {
	Impressions int64
	Clicks      int64
}

// EndScreenStatsSince sums each of a post's elements' counters from day
// since (inclusive, UTC date) on.
func (s *Store) EndScreenStatsSince(ctx context.Context, postID uuid.UUID, since time.Time) (map[uuid.UUID]ElementStats, error) {
	return s.statsSince(ctx, `
		SELECT element_id, COALESCE(SUM(impressions), 0), COALESCE(SUM(clicks), 0)
		FROM end_screen_stats WHERE post_id = $1 AND day >= $2::date
		GROUP BY element_id`, postID, since)
}

// CardStatsSince is EndScreenStatsSince for cards.
func (s *Store) CardStatsSince(ctx context.Context, postID uuid.UUID, since time.Time) (map[uuid.UUID]ElementStats, error) {
	return s.statsSince(ctx, `
		SELECT card_id, COALESCE(SUM(impressions), 0), COALESCE(SUM(clicks), 0)
		FROM card_stats WHERE post_id = $1 AND day >= $2::date
		GROUP BY card_id`, postID, since)
}

func (s *Store) statsSince(ctx context.Context, q string, postID uuid.UUID, since time.Time) (map[uuid.UUID]ElementStats, error) {
	rows, err := s.db.Query(ctx, q, postID, since.UTC().Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]ElementStats{}
	for rows.Next() {
		var (
			id uuid.UUID
			st ElementStats
		)
		if err := rows.Scan(&id, &st.Impressions, &st.Clicks); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// EndScreenOnPost reports whether elementID is one of postID's elements.
func (s *Store) EndScreenOnPost(ctx context.Context, postID, elementID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM video_end_screens WHERE id = $1 AND post_id = $2)`,
		elementID, postID).Scan(&ok)
	return ok, err
}

// CardOnPost reports whether cardID is one of postID's cards.
func (s *Store) CardOnPost(ctx context.Context, postID, cardID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM video_cards WHERE id = $1 AND post_id = $2)`,
		cardID, postID).Scan(&ok)
	return ok, err
}

// BumpEndScreenStat adds one impression (or one click) to the element's row
// for day. The element must be postID's: the INSERT selects it by both ids,
// so a mismatched pair writes nothing.
func (s *Store) BumpEndScreenStat(ctx context.Context, postID, elementID uuid.UUID, day time.Time, click bool) error {
	imp, clk := statDeltas(click)
	_, err := s.db.Exec(ctx, `
		INSERT INTO end_screen_stats (post_id, element_id, day, impressions, clicks)
		SELECT post_id, id, $3::date, $4, $5 FROM video_end_screens WHERE id = $2 AND post_id = $1
		ON CONFLICT (element_id, day) DO UPDATE
		SET impressions = end_screen_stats.impressions + EXCLUDED.impressions,
		    clicks      = end_screen_stats.clicks + EXCLUDED.clicks`,
		postID, elementID, day.UTC().Format("2006-01-02"), imp, clk)
	return err
}

// BumpCardStat is BumpEndScreenStat for cards.
func (s *Store) BumpCardStat(ctx context.Context, postID, cardID uuid.UUID, day time.Time, click bool) error {
	imp, clk := statDeltas(click)
	_, err := s.db.Exec(ctx, `
		INSERT INTO card_stats (post_id, card_id, day, impressions, clicks)
		SELECT post_id, id, $3::date, $4, $5 FROM video_cards WHERE id = $2 AND post_id = $1
		ON CONFLICT (card_id, day) DO UPDATE
		SET impressions = card_stats.impressions + EXCLUDED.impressions,
		    clicks      = card_stats.clicks + EXCLUDED.clicks`,
		postID, cardID, day.UTC().Format("2006-01-02"), imp, clk)
	return err
}

func statDeltas(click bool) (int, int) {
	if click {
		return 0, 1
	}
	return 1, 0
}

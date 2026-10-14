package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RecordSearchHistory remembers a signed-in user's search (T-15).
//
// Rules, in order:
//   - uuid.Nil (an anonymous caller) is never recorded.
//   - The query is normalised (NormaliseHistoryQuery); one shorter than
//     MinHistoryQueryRunes is ignored.
//   - One row per (user, normalised query): searching the same thing again
//     moves the existing row to the top (same id, newer searched_at) rather
//     than adding a duplicate. The display text is the latest spelling.
//   - An entry written in the last RefineWindowSeconds that is a strict
//     prefix of this query is the same search being refined ("ca" → "cat"),
//     and is removed.
//   - Only the newest MaxSearchHistory rows are kept.
//
// The three statements go in one batch (one round trip, one implicit
// transaction). Callers run this best-effort, off the request path.
func (s *SearchExtrasStore) RecordSearchHistory(ctx context.Context, userID uuid.UUID, query string) error {
	if userID == uuid.Nil {
		return nil
	}
	display, key, ok := NormaliseHistoryQuery(query)
	if !ok {
		return nil
	}
	b := &pgx.Batch{}
	b.Queue(`
		INSERT INTO search_history (id, user_id, query, query_key, searched_at)
		VALUES ($1, $2, $3, $4, clock_timestamp())
		ON CONFLICT (user_id, query_key)
		DO UPDATE SET query = EXCLUDED.query, searched_at = EXCLUDED.searched_at
	`, uuid.New(), userID, display, key)
	b.Queue(`
		DELETE FROM search_history
		WHERE user_id = $1
		  AND length(query_key) < length($2::text)
		  AND left($2::text, length(query_key)) = query_key
		  AND searched_at > clock_timestamp() - make_interval(secs => $3)
	`, userID, key, float64(RefineWindowSeconds))
	b.Queue(`
		DELETE FROM search_history
		WHERE user_id = $1
		  AND id NOT IN (
		      SELECT id FROM search_history
		      WHERE user_id = $1
		      ORDER BY searched_at DESC, id DESC
		      LIMIT $2
		  )
	`, userID, MaxSearchHistory)
	br := s.db.SendBatch(ctx, b)
	for i := 0; i < b.Len(); i++ {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return err
		}
	}
	return br.Close()
}

// GetSearchHistory returns recent search history for a user, newest first.
// limit is capped at MaxSearchHistory.
func (s *SearchExtrasStore) GetSearchHistory(ctx context.Context, userID uuid.UUID, limit int) ([]SearchHistoryItem, error) {
	if limit <= 0 || limit > MaxSearchHistory {
		limit = MaxSearchHistory
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, query, searched_at
		FROM search_history
		WHERE user_id = $1
		ORDER BY searched_at DESC, id DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchHistoryItem
	for rows.Next() {
		var h SearchHistoryItem
		if err := rows.Scan(&h.ID, &h.UserID, &h.Query, &h.SearchedAt); err != nil {
			return nil, err
		}
		results = append(results, h)
	}
	return results, rows.Err()
}

// DeleteSearchHistoryItem removes one history entry owned by userID.
// ErrNotFound when no such entry exists OR it belongs to someone else —
// the two are deliberately indistinguishable to the caller.
func (s *SearchExtrasStore) DeleteSearchHistoryItem(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM search_history WHERE id = $1 AND user_id = $2
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PopularQuery is one row of the popular-searches read. Users (how many
// distinct people searched it) is kept server-side, never serialised.
type PopularQuery struct {
	Query string `json:"query"`
	Users int    `json:"-"`
}

// PopularMinUsersFloor: the threshold can be configured up, never below
// this. One or two people's searches are never shown to anyone else.
const PopularMinUsersFloor = 3

// PopularQueries aggregates search_history over the last `window` and
// returns normalised queries searched by at least minUsers distinct people,
// most people first. Queries that look like contact or identity data are
// dropped (IsSafePopularQuery). The normalised key, not anyone's own
// spelling, is what is returned.
func (s *SearchExtrasStore) PopularQueries(ctx context.Context, window time.Duration, minUsers, limit int) ([]PopularQuery, error) {
	if minUsers < PopularMinUsersFloor {
		minUsers = PopularMinUsersFloor
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(ctx, `
		SELECT query_key, count(DISTINCT user_id)::int AS users
		FROM search_history
		WHERE searched_at > now() - make_interval(secs => $1)
		GROUP BY query_key
		HAVING count(DISTINCT user_id) >= $2
		ORDER BY users DESC, max(searched_at) DESC, query_key
		LIMIT $3
	`, window.Seconds(), minUsers, limit*3)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]PopularQuery, 0, limit)
	for rows.Next() {
		var p PopularQuery
		if err := rows.Scan(&p.Query, &p.Users); err != nil {
			return nil, err
		}
		if len(out) < limit && IsSafePopularQuery(p.Query) {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// Daily picks store — mechanic M7.
//
// dating_daily_picks is the day's selection for a user, keyed by the user's
// local date: chosen once, then served all day in the same order. Rows are
// a snapshot only; every read re-applies visibility through FetchCandidates
// (CandidateQuery.OnlyIDs), so a block, a pause or an action since the pick
// was made takes the person out.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DailyPicks returns the candidate ids picked for userID on day, in order.
func (s *Store) DailyPicks(ctx context.Context, userID uuid.UUID, day time.Time) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
        SELECT candidate_id FROM dating_daily_picks
        WHERE user_id = $1 AND pick_date = $2::date
        ORDER BY position`, userID, day.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("daily picks: %w", err)
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

// HasDailyPicks reports whether the day's selection was already made (an
// empty selection is recorded too, with a sentinel row, so a day with nobody
// to pick is not recomputed on every request).
func (s *Store) HasDailyPicks(ctx context.Context, userID uuid.UUID, day time.Time) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `
        SELECT EXISTS (SELECT 1 FROM dating_daily_pick_days WHERE user_id = $1 AND pick_date = $2::date)`,
		userID, day.Format("2006-01-02")).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("daily pick day: %w", err)
	}
	return ok, nil
}

// SaveDailyPicks records the day's selection, once: a concurrent second
// selection for the same day is dropped and the first one stands.
func (s *Store) SaveDailyPicks(ctx context.Context, userID uuid.UUID, day time.Time, picks []uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin picks: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
        INSERT INTO dating_daily_pick_days (user_id, pick_date) VALUES ($1, $2::date)
        ON CONFLICT DO NOTHING`, userID, day.Format("2006-01-02"))
	if err != nil {
		return fmt.Errorf("record pick day: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	for i, id := range picks {
		if _, err := tx.Exec(ctx, `
            INSERT INTO dating_daily_picks (user_id, pick_date, candidate_id, position)
            VALUES ($1, $2::date, $3, $4)`, userID, day.Format("2006-01-02"), id, i+1); err != nil {
			return fmt.Errorf("record pick: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// PickReciprocity is what a candidate is looking for, read for mutual picks.
// Zero bounds mean "not set".
type PickReciprocity struct {
	MinAge       int
	MaxAge       int
	DistanceKm   int
	IntentFilter []string
	VerifiedOnly bool
}

// PickReciprocities returns each listed user's own preferences (age range,
// distance, intents) and verified-only toggle. A user with no preferences
// row gets zero bounds.
func (s *Store) PickReciprocities(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]PickReciprocity, error) {
	out := make(map[uuid.UUID]PickReciprocity, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
        SELECT p.user_id, COALESCE(pr.min_age, 0), COALESCE(pr.max_age, 0), COALESCE(pr.distance_km, 0),
               COALESCE(pr.intent_filter, '{}'), p.verified_only_filter
        FROM dating_profiles p
        LEFT JOIN dating_preferences pr ON pr.user_id = p.user_id
        WHERE p.user_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("pick reciprocity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var r PickReciprocity
		if err := rows.Scan(&id, &r.MinAge, &r.MaxAge, &r.DistanceKm, &r.IntentFilter, &r.VerifiedOnly); err != nil {
			return nil, err
		}
		out[id] = r
	}
	return out, rows.Err()
}

// PickExposure counts, for each listed user, how many people's picks they
// are in for the local date day.
func (s *Store) PickExposure(ctx context.Context, ids []uuid.UUID, day time.Time) (map[uuid.UUID]int, error) {
	out := make(map[uuid.UUID]int, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
        SELECT candidate_id, count(*) FROM dating_daily_picks
        WHERE candidate_id = ANY($1::uuid[]) AND pick_date = $2::date
        GROUP BY candidate_id`, ids, day.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("pick exposure: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// InRecentPicks reports whether candidateID was one of userID's daily picks
// for a local date no older than yesterday (UTC), which covers every zone's
// "today". Visibility is the caller's to apply.
func (s *Store) InRecentPicks(ctx context.Context, userID, candidateID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `
        SELECT EXISTS (SELECT 1 FROM dating_daily_picks
            WHERE user_id = $1 AND candidate_id = $2
              AND pick_date >= (now() AT TIME ZONE 'UTC')::date - 1)`, userID, candidateID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("recent picks: %w", err)
	}
	return ok, nil
}

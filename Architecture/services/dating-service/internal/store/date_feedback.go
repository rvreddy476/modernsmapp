// After-date check-ins — mechanic M14 (DATING_DATE_CHECKIN_ENABLED).
//
// dating_meets.date_checkin_asked_at marks a planned meet whose check-in has
// been sent (claimed once across replicas). dating_date_feedback is what
// people answer: did you meet, would you again, did you feel safe. A "did
// not feel safe" answer is safety evidence: rows are never deleted, and a
// purge swaps the purged id for its subject token.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DateCheckinMeet is a planned meet whose check-in is now due.
type DateCheckinMeet struct {
	MeetID      uuid.UUID
	UserID      uuid.UUID
	WithUserID  uuid.UUID
	ScheduledAt time.Time
}

// ClaimMeetsForDateCheckin claims meets scheduled between within and after
// ago that have not been asked about, skipping blocked pairs, and stamps
// them asked. Each meet is claimed once across replicas.
func (s *Store) ClaimMeetsForDateCheckin(ctx context.Context, after, within time.Duration, limit int) ([]DateCheckinMeet, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
        UPDATE dating_meets
        SET date_checkin_asked_at = now()
        WHERE id IN (
            SELECT id FROM dating_meets dm
            WHERE dm.date_checkin_asked_at IS NULL
              AND dm.scheduled_at < now() - make_interval(secs => $1)
              AND dm.scheduled_at > now() - make_interval(secs => $2)
              AND NOT `+blockedPairPredicate("dm.user_id", "dm.with_user_id")+`
            ORDER BY dm.scheduled_at
            LIMIT $3
            FOR UPDATE SKIP LOCKED
        )
        RETURNING id, user_id, with_user_id, scheduled_at`, after.Seconds(), within.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("claim date check-ins: %w", err)
	}
	defer rows.Close()
	var out []DateCheckinMeet
	for rows.Next() {
		var m DateCheckinMeet
		if err := rows.Scan(&m.MeetID, &m.UserID, &m.WithUserID, &m.ScheduledAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DateFeedback is one answer.
type DateFeedback struct {
	ID        uuid.UUID
	MatchID   uuid.UUID
	UserID    uuid.UUID
	OtherID   uuid.UUID
	Met       string
	Again     *string
	FeltSafe  *bool
	CreatedAt time.Time
}

// RecordDateFeedback appends an answer.
func (s *Store) RecordDateFeedback(ctx context.Context, f DateFeedback) (*DateFeedback, error) {
	out := f
	err := s.db.QueryRow(ctx, `
        INSERT INTO dating_date_feedback (match_id, user_id, other_id, met, again, felt_safe)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id, created_at`, f.MatchID, f.UserID, f.OtherID, f.Met, f.Again, f.FeltSafe).Scan(&out.ID, &out.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("record date feedback: %w", err)
	}
	return &out, nil
}

// DateFeedbackCount is how many answers userID gave about matchID.
func (s *Store) DateFeedbackCount(ctx context.Context, matchID, userID uuid.UUID) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM dating_date_feedback WHERE match_id = $1 AND user_id = $2`, matchID, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("date feedback count: %w", err)
	}
	return n, nil
}

// PendingDateCheckin is a check-in userID was asked and has not answered.
type PendingDateCheckin struct {
	MeetID    uuid.UUID
	MatchID   uuid.UUID
	OtherID   uuid.UUID
	FirstName *string
	AskedAt   time.Time
}

// PendingDateCheckins lists the check-ins userID was asked within the last
// window and has not answered since, newest first. The match is the pair's
// most recent one; a blocked pair is left out.
func (s *Store) PendingDateCheckins(ctx context.Context, userID uuid.UUID, window time.Duration) ([]PendingDateCheckin, error) {
	rows, err := s.db.Query(ctx, `
        SELECT q.meet_id, q.match_id, q.other_id, p.first_name, q.asked_at
        FROM (
            SELECT dm.id AS meet_id, o.other_id, dm.date_checkin_asked_at AS asked_at,
                   (SELECT m.id FROM dating_matches m
                     WHERE m.user_a = LEAST($1::uuid, o.other_id) AND m.user_b = GREATEST($1::uuid, o.other_id)
                     ORDER BY m.matched_at DESC LIMIT 1) AS match_id
            FROM dating_meets dm
            CROSS JOIN LATERAL (SELECT CASE WHEN dm.user_id = $1 THEN dm.with_user_id ELSE dm.user_id END AS other_id) o
            WHERE (dm.user_id = $1 OR dm.with_user_id = $1)
              AND dm.date_checkin_asked_at > now() - make_interval(secs => $2)
              AND NOT `+blockedPairPredicate("dm.user_id", "dm.with_user_id")+`
        ) q
        JOIN dating_profiles p ON p.user_id = q.other_id
        WHERE q.match_id IS NOT NULL
          AND NOT EXISTS (SELECT 1 FROM dating_date_feedback f
              WHERE f.match_id = q.match_id AND f.user_id = $1 AND f.created_at >= q.asked_at)
        ORDER BY q.asked_at DESC
        LIMIT 20`, userID, window.Seconds())
	if err != nil {
		return nil, fmt.Errorf("pending date check-ins: %w", err)
	}
	defer rows.Close()
	var out []PendingDateCheckin
	for rows.Next() {
		var c PendingDateCheckin
		if err := rows.Scan(&c.MeetID, &c.MatchID, &c.OtherID, &c.FirstName, &c.AskedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

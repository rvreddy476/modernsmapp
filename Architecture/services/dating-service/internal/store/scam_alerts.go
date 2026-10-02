// Scam alerts — mechanic M17 (DATING_SCAM_ALERT_ENABLED).
//
// When an admin suspends someone on a scam report, everyone who matched with
// them in the last ScamAlertLookback (except the reporter, who hears through
// their report) is warned once. dating_scam_alerts is the outbox: one row per
// (subject, recipient), queued at the suspension and marked sent once the
// event reached Kafka, so the sweeper retries what did not. It is a safety
// record and is never deleted; a purge replaces the purged person's id with
// their subject token (PurgeProfile).
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ScamAlertLookback is how far back a match counts.
const ScamAlertLookback = 90 * 24 * time.Hour

// scamAlertMaxAttempts stops retrying an alert that keeps failing.
const scamAlertMaxAttempts = 10

// ScamAlert is one queued warning.
type ScamAlert struct {
	SubjectID   uuid.UUID
	RecipientID uuid.UUID
	MatchID     uuid.UUID
	FirstName   *string
}

// QueueScamAlerts queues a warning for everyone who matched with subjectID
// since the lookback (by the later of matched_at and last_message_at),
// except exceptID. One row per recipient, their latest match; a pair already
// warned is not warned again. Returns how many were queued.
func (s *Store) QueueScamAlerts(ctx context.Context, subjectID, exceptID uuid.UUID) (int, error) {
	tag, err := s.db.Exec(ctx, `
        INSERT INTO dating_scam_alerts (subject_id, recipient_id, match_id, first_name)
        SELECT DISTINCT ON (o.other_id) $1, o.other_id, m.id, sp.first_name
        FROM dating_matches m
        CROSS JOIN LATERAL (SELECT CASE WHEN m.user_a = $1 THEN m.user_b ELSE m.user_a END AS other_id) o
        JOIN dating_profiles rp ON rp.user_id = o.other_id AND rp.deleted_at IS NULL
        LEFT JOIN dating_profiles sp ON sp.user_id = $1
        WHERE (m.user_a = $1 OR m.user_b = $1)
          AND m.anonymised_at IS NULL
          AND o.other_id <> $2
          AND GREATEST(m.matched_at, COALESCE(m.last_message_at, m.matched_at)) > now() - make_interval(secs => $3)
        ORDER BY o.other_id, m.matched_at DESC
        ON CONFLICT (subject_id, recipient_id) DO NOTHING`, subjectID, exceptID, ScamAlertLookback.Seconds())
	if err != nil {
		return 0, fmt.Errorf("queue scam alerts: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// SendPendingScamAlerts claims up to limit unsent alerts and hands each to
// send; a nil error marks it sent, an error counts an attempt. Claimed rows
// are locked for the call, so two sweepers never send the same alert.
func (s *Store) SendPendingScamAlerts(ctx context.Context, limit int, send func(ScamAlert) error) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin scam alerts: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
        SELECT subject_id, recipient_id, match_id, first_name FROM dating_scam_alerts
        WHERE sent_at IS NULL AND cancelled_at IS NULL AND attempts < $2
        ORDER BY created_at
        LIMIT $1
        FOR UPDATE SKIP LOCKED`, limit, scamAlertMaxAttempts)
	if err != nil {
		return 0, fmt.Errorf("claim scam alerts: %w", err)
	}
	var claimed []ScamAlert
	for rows.Next() {
		var a ScamAlert
		if err := rows.Scan(&a.SubjectID, &a.RecipientID, &a.MatchID, &a.FirstName); err != nil {
			rows.Close()
			return 0, err
		}
		claimed = append(claimed, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	sent := 0
	for _, a := range claimed {
		if serr := send(a); serr != nil {
			if _, err := tx.Exec(ctx, `
                UPDATE dating_scam_alerts SET attempts = attempts + 1, last_error = $3
                WHERE subject_id = $1 AND recipient_id = $2`, a.SubjectID, a.RecipientID, serr.Error()); err != nil {
				return sent, err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `
            UPDATE dating_scam_alerts SET sent_at = now(), attempts = attempts + 1, last_error = NULL
            WHERE subject_id = $1 AND recipient_id = $2`, a.SubjectID, a.RecipientID); err != nil {
			return sent, err
		}
		sent++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit scam alerts: %w", err)
	}
	return sent, nil
}

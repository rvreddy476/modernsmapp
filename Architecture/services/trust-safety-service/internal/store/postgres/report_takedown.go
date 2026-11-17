package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Report takedowns (reels-29, migration 021) ──────────────────────────────
//
// An upheld report on a post owes post-service one signed reject decision.
// UpdateReport writes that obligation on the report row in the transaction
// that resolves it; the service's sender claims due rows, sends and records
// the outcome here. The report id is the decision id, so every replay is the
// same decision.

// Takedown states (trust.reports.takedown_status).
const (
	TakedownPending    = "pending"
	TakedownSent       = "sent"
	TakedownSuperseded = "superseded"
	TakedownFailed     = "failed"
	TakedownCancelled  = "cancelled"
)

// takedownEntityType is the only report subject a takedown is sent for: a
// post (the reel and video aliases normalise to it at filing).
const takedownEntityType = "post"

// maxTakedownReason bounds the reason the author is shown.
const maxTakedownReason = 2000

// TakedownOnTransition is the takedown change a report status change makes,
// given the report's subject and its current takedown state: "pending" when
// a post report is upheld ('resolved') for the first time, "cancelled" when
// a report whose takedown has not gone yet is dismissed, "" otherwise.
// Dismissal never sends anything, and a takedown already sent stays sent
// (lifting it is an appeal or an admin decision on post-service).
func TakedownOnTransition(entityType, newStatus, currentTakedown string) string {
	switch {
	case newStatus == "resolved" && entityType == takedownEntityType && currentTakedown == "":
		return TakedownPending
	case newStatus == "dismissed" && currentTakedown == TakedownPending:
		return TakedownCancelled
	}
	return ""
}

// ReportTakedownReason is the reason the decision carries and the author is
// shown: the moderator's resolution notes, or, when they wrote none, the
// report's category.
func ReportTakedownReason(notes, category string) string {
	reason := strings.TrimSpace(notes)
	if reason == "" {
		reason = "Removed after a report: " + strings.ReplaceAll(strings.TrimSpace(category), "_", " ")
	}
	if utf8.RuneCountInString(reason) > maxTakedownReason {
		reason = string([]rune(reason)[:maxTakedownReason])
	}
	return reason
}

// ReportTakedown is one claimed, due takedown.
type ReportTakedown struct {
	ReportID uuid.UUID
	PostID   uuid.UUID
	ActorID  uuid.UUID
	Reason   string
	Attempts int
}

// applyTakedownTransitionTx writes the takedown change of a status change
// inside the report's transaction. actor is the human who resolved it: a
// takedown with no human actor cannot be signed, so it is recorded as
// failed at once instead of waiting forever.
func applyTakedownTransitionTx(ctx context.Context, tx pgx.Tx, reportID uuid.UUID, change string, actor uuid.UUID, reason string) error {
	var err error
	switch change {
	case "":
		return nil
	case TakedownPending:
		if actor == uuid.Nil {
			_, err = tx.Exec(ctx, `
				UPDATE trust.reports
				   SET takedown_status = 'failed',
				       takedown_reason = $2,
				       takedown_last_error = 'no human moderator resolved the report; a takedown needs one',
				       takedown_done_at = clock_timestamp()
				 WHERE id = $1
			`, reportID, reason)
			break
		}
		_, err = tx.Exec(ctx, `
			UPDATE trust.reports
			   SET takedown_status = 'pending',
			       takedown_reason = $2,
			       takedown_actor_id = $3,
			       takedown_attempts = 0,
			       takedown_last_error = NULL,
			       takedown_next_attempt_at = clock_timestamp(),
			       takedown_done_at = NULL
			 WHERE id = $1
		`, reportID, reason, actor)
	case TakedownCancelled:
		_, err = tx.Exec(ctx, `
			UPDATE trust.reports
			   SET takedown_status = 'cancelled',
			       takedown_next_attempt_at = NULL,
			       takedown_done_at = clock_timestamp()
			 WHERE id = $1 AND takedown_status = 'pending'
		`, reportID)
	default:
		return fmt.Errorf("report takedown: unknown change %q", change)
	}
	if err != nil {
		return fmt.Errorf("report takedown: record %s: %w", change, err)
	}
	return nil
}

// ClaimReportTakedowns claims up to limit due pending takedowns (only one
// report when only is set) by moving their next attempt lease into the
// future, so another sender skips them while this one works. A sender that
// dies leaves the row to be claimed again when the lease runs out.
func (s *ReportStore) ClaimReportTakedowns(ctx context.Context, only *uuid.UUID, lease time.Duration, limit int) ([]ReportTakedown, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		UPDATE trust.reports r
		   SET takedown_next_attempt_at = clock_timestamp() + ($3::bigint * interval '1 millisecond')
		  FROM (SELECT id FROM trust.reports
		         WHERE takedown_status = 'pending'
		           AND takedown_next_attempt_at <= clock_timestamp()
		           AND ($1::uuid IS NULL OR id = $1::uuid)
		         ORDER BY takedown_next_attempt_at, id
		         LIMIT $2
		         FOR UPDATE SKIP LOCKED) due
		 WHERE r.id = due.id
		RETURNING r.id, r.entity_id, r.takedown_actor_id, COALESCE(r.takedown_reason, ''), r.takedown_attempts
	`, only, limit, lease.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("report takedown: claim: %w", err)
	}
	defer rows.Close()
	var out []ReportTakedown
	for rows.Next() {
		var t ReportTakedown
		if err := rows.Scan(&t.ReportID, &t.PostID, &t.ActorID, &t.Reason, &t.Attempts); err != nil {
			return nil, fmt.Errorf("report takedown: scan claim: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RecordReportTakedown records one attempt's outcome on a pending takedown:
// still pending (retry after retryAfter, with the error kept visible) or
// terminal. A row no longer pending (dismissed meanwhile, or recorded by a
// racing sender) is left as it is.
func (s *ReportStore) RecordReportTakedown(ctx context.Context, reportID uuid.UUID, status, lastError string, retryAfter time.Duration) error {
	switch status {
	case TakedownPending, TakedownSent, TakedownSuperseded, TakedownFailed:
	default:
		return fmt.Errorf("report takedown: cannot record status %q", status)
	}
	if len(lastError) > 2000 {
		lastError = lastError[:2000]
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE trust.reports
		   SET takedown_status = $2::text,
		       takedown_attempts = takedown_attempts + 1,
		       takedown_last_error = NULLIF($3, ''),
		       takedown_next_attempt_at = CASE WHEN $2::text = 'pending'
		            THEN clock_timestamp() + ($4::bigint * interval '1 millisecond') END,
		       takedown_done_at = CASE WHEN $2::text = 'pending' THEN NULL ELSE clock_timestamp() END
		 WHERE id = $1 AND takedown_status = 'pending'
	`, reportID, status, lastError, retryAfter.Milliseconds()); err != nil {
		return fmt.Errorf("report takedown: record: %w", err)
	}
	return nil
}

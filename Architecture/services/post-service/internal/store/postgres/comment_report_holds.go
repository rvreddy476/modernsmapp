package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CommentReportOutcome is what one trust-safety comment report did.
type CommentReportOutcome struct {
	// PostID is the comment's post (zero when Found is false).
	PostID uuid.UUID
	// Found: the comment exists in post-service. A report about a comment
	// this service does not hold changes nothing.
	Found bool
	// Counted: this report moved flagged_count. False for a redelivery and
	// for a reporter whose earlier report on the comment already counted.
	Counted bool
	// Held: this report took the comment from 'visible' to 'review' (the
	// auto-review threshold). At most one report per comment ever does.
	Held bool
	// LeftCount: the hold also took the comment out of comment_count
	// (it was not already soft-deleted, so it was being counted).
	LeftCount bool
}

// ApplyCommentReportHold counts one report filed in trust-safety-service
// against a comment (TikTok parity T-13; migration 062).
//
// One transaction, serialised per comment by the comment row lock:
//
//  1. the (comment, reporter) row in comment_report_reporters is the
//     idempotency key: a Kafka redelivery of the same report, or a second
//     report by the same reporter, finds it and changes nothing;
//  2. otherwise flagged_count moves by one and, at
//     commentAutoReviewThreshold, a visible comment goes to 'review'
//     (the rule IncrementCommentFlaggedCount has always applied);
//  3. that hold leaves comment_count in the same transaction, so a crash
//     between the hold and the count cannot leave them disagreeing (a
//     redelivery would find step 1's row and never retry the count).
func (s *Store) ApplyCommentReportHold(ctx context.Context, reportID, commentID, reporterID uuid.UUID) (CommentReportOutcome, error) {
	var out CommentReportOutcome
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var before string
	var deleted bool
	err = tx.QueryRow(ctx,
		`SELECT post_id, moderation_status, is_deleted FROM comments WHERE id = $1 FOR UPDATE`,
		commentID).Scan(&out.PostID, &before, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommentReportOutcome{}, nil
	}
	if err != nil {
		return out, fmt.Errorf("lock comment: %w", err)
	}
	out.Found = true

	tag, err := tx.Exec(ctx, `
		INSERT INTO comment_report_reporters (comment_id, reporter_id, report_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (comment_id, reporter_id) DO NOTHING`,
		commentID, reporterID, reportID)
	if err != nil {
		return out, fmt.Errorf("record comment reporter: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return out, tx.Commit(ctx)
	}

	var after string
	if err := tx.QueryRow(ctx, `
		UPDATE comments
		SET flagged_count = flagged_count + 1,
		    moderation_status = CASE
		        WHEN moderation_status = 'visible' AND flagged_count + 1 >= $2
		        THEN 'review'
		        ELSE moderation_status
		    END
		WHERE id = $1
		RETURNING moderation_status`,
		commentID, commentAutoReviewThreshold).Scan(&after); err != nil {
		return out, fmt.Errorf("count comment report: %w", err)
	}
	out.Counted = true
	out.Held = before == "visible" && after == "review"
	if out.Held && !deleted {
		// The comment was in comment_count's definition
		// (visibleCommentPredicate) and has just left it.
		if _, err := tx.Exec(ctx, `
			INSERT INTO post_engagement_counts (post_id, comment_count)
			VALUES ($1, 0)
			ON CONFLICT (post_id) DO UPDATE SET
				comment_count = GREATEST(post_engagement_counts.comment_count - 1, 0),
				updated_at = now()`, out.PostID); err != nil {
			return out, fmt.Errorf("decrement comment_count on hold: %w", err)
		}
		out.LeftCount = true
	}
	return out, tx.Commit(ctx)
}

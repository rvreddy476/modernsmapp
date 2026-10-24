package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Upheld copyright blocks (migration 019; founder quick wins 2026-10-24).
//
// A copyright title review that a reviewer blocked (post-service,
// post.copyright_review.changed state "blocked") is recorded here once per
// review. The standing answer counts these with trust-safety's own upheld
// copyright cases and copyright strikes to decide the long-video pause.
//
// A block that is overturned — the author appealed (post-service migration
// 076) and a reviewer cleared the review — is VOIDED (migration 020), so it
// stops counting and the pause lifts on the next standing answer if fewer
// than three blocks remain. The voided row stays; a later block of the same
// review is a new row and counts once again.

// CopyrightBlockSourceTitleReview is post-service's title review.
const CopyrightBlockSourceTitleReview = "title_review"

// Why a title-review block was voided (copyright_blocks.void_reason).
const (
	// CopyrightBlockVoidAppealCleared: the author appealed and a reviewer
	// cleared the review.
	CopyrightBlockVoidAppealCleared = "appeal_cleared"
	// CopyrightBlockVoidReviewCleared: the review was cleared without the
	// event saying it was appealed (post-service has no such path today;
	// kept so a clear never leaves a block standing).
	CopyrightBlockVoidReviewCleared = "review_cleared"
)

// ErrCopyrightBlockInvalid: a block is missing its user, post or review.
var ErrCopyrightBlockInvalid = errors.New("invalid copyright block")

// CopyrightBlockInput is one upheld block.
type CopyrightBlockInput struct {
	UserID       uuid.UUID
	PostID       uuid.UUID
	ReviewID     uuid.UUID
	MatchedTitle string
	DecidedBy    *uuid.UUID
	BlockedAt    time.Time
}

// recordCopyrightBlockSQL inserts a block only when it is NEWER than every
// row its review already has, and only while the review has no active
// block (the partial unique index of 020 is the backstop):
//
//   - a redelivered block (same blocked_at) is nothing, active or voided:
//     an old block replayed after its void never comes back;
//   - a block after a clear (a later blocked_at than the voided row) is a
//     new row and counts once again.
const recordCopyrightBlockSQL = `
	INSERT INTO trust.copyright_blocks (id, user_id, post_id, source, source_ref, matched_title, decided_by, blocked_at)
	SELECT $1::uuid, $2::uuid, $3::uuid, $4::text, $5::uuid, $6::text, $7::uuid, $8::timestamptz
	 WHERE NOT EXISTS (
		SELECT 1 FROM trust.copyright_blocks b
		 WHERE b.source = $4::text AND b.source_ref = $5::uuid
		   AND (b.voided_at IS NULL OR b.blocked_at >= $8::timestamptz))
	ON CONFLICT (source, source_ref) WHERE voided_at IS NULL DO NOTHING`

// RecordCopyrightBlock writes the block once: a replay of the same review
// is created=false and changes nothing, even after the block was voided.
func (s *TrustExtrasStore) RecordCopyrightBlock(ctx context.Context, in CopyrightBlockInput) (bool, error) {
	if in.UserID == uuid.Nil || in.PostID == uuid.Nil || in.ReviewID == uuid.Nil || in.BlockedAt.IsZero() {
		return false, ErrCopyrightBlockInvalid
	}
	var title *string
	if t := strings.TrimSpace(in.MatchedTitle); t != "" {
		if len(t) > 200 {
			t = t[:200]
		}
		title = &t
	}
	tag, err := s.db.Exec(ctx, recordCopyrightBlockSQL,
		uuid.New(), in.UserID, in.PostID, CopyrightBlockSourceTitleReview, in.ReviewID, title, in.DecidedBy, in.BlockedAt.UTC())
	if err != nil {
		return false, fmt.Errorf("record copyright block: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CopyrightBlockVoidInput is one clear of a title review.
type CopyrightBlockVoidInput struct {
	UserID   uuid.UUID
	ReviewID uuid.UUID
	// ClearedAt is when the review was cleared: only a block recorded
	// BEFORE it is voided, so a clear redelivered after a later re-block
	// never voids that re-block.
	ClearedAt time.Time
	// VoidedBy is the reviewer who cleared it; nil on a system clear.
	VoidedBy *uuid.UUID
	Reason   string
}

// voidCopyrightBlockSQL voids the review's active block when it is older
// than the clear. A clear without a prior block, or a replay after the
// void, matches no row.
const voidCopyrightBlockSQL = `
	UPDATE trust.copyright_blocks
	   SET voided_at = clock_timestamp(), void_reason = $4, voided_by = $5
	 WHERE source = $1 AND source_ref = $2 AND user_id = $3
	   AND voided_at IS NULL AND blocked_at < $6`

// VoidCopyrightBlock voids the block recorded for a review that was then
// cleared: voided=false, and nothing changes, when the review has no
// active block older than the clear.
func (s *TrustExtrasStore) VoidCopyrightBlock(ctx context.Context, in CopyrightBlockVoidInput) (bool, error) {
	reason := strings.TrimSpace(in.Reason)
	if in.UserID == uuid.Nil || in.ReviewID == uuid.Nil || in.ClearedAt.IsZero() || reason == "" {
		return false, ErrCopyrightBlockInvalid
	}
	tag, err := s.db.Exec(ctx, voidCopyrightBlockSQL,
		CopyrightBlockSourceTitleReview, in.ReviewID, in.UserID, reason, in.VoidedBy, in.ClearedAt.UTC())
	if err != nil {
		return false, fmt.Errorf("void copyright block: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// copyrightBlocksSQL lists one user's upheld copyright blocks since $2,
// one time per distinct block, oldest first:
//
//   - post-service title-review blocks (trust.copyright_blocks, not voided);
//   - trust-safety copyright cases whose hold is in place (hold_active: a
//     removal a reviewer upheld and nobody has released);
//   - strikes issued in strike_group 'copyright' and not voided — a strike
//     naming one of those cases is the same block, not a second one.
const copyrightBlocksSQL = `
	SELECT MIN(at) FROM (
		SELECT 'title_review:' || source_ref::text AS k, blocked_at AS at
		  FROM trust.copyright_blocks
		 WHERE user_id = $1 AND voided_at IS NULL AND blocked_at > $2
		UNION ALL
		SELECT 'copyright_case:' || id::text, created_at
		  FROM trust.copyright_cases
		 WHERE subject_author_id = $1 AND state = 'hold_active' AND created_at > $2
		UNION ALL
		SELECT COALESCE('copyright_case:' || case_id::text, 'strike:' || id::text), created_at
		  FROM trust.user_strikes
		 WHERE user_id = $1 AND strike_group = 'copyright' AND voided_at IS NULL AND created_at > $2
	) blocks
	GROUP BY k
	ORDER BY 1`

// CopyrightBlockTimes returns when each of the user's upheld copyright
// blocks since `since` happened, oldest first.
func (s *TrustExtrasStore) CopyrightBlockTimes(ctx context.Context, userID uuid.UUID, since time.Time) ([]time.Time, error) {
	rows, err := s.db.Query(ctx, copyrightBlocksSQL, userID, since)
	if err != nil {
		return nil, fmt.Errorf("copyright blocks: %w", err)
	}
	defer rows.Close()
	out := []time.Time{}
	for rows.Next() {
		var at time.Time
		if err := rows.Scan(&at); err != nil {
			return nil, fmt.Errorf("copyright blocks: scan: %w", err)
		}
		out = append(out, at.UTC())
	}
	return out, rows.Err()
}

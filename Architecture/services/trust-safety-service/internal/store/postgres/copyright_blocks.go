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

// CopyrightBlockSourceTitleReview is post-service's title review.
const CopyrightBlockSourceTitleReview = "title_review"

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

// RecordCopyrightBlock writes the block once: a replay of the same review
// is created=false and changes nothing.
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
	tag, err := s.db.Exec(ctx, `
		INSERT INTO trust.copyright_blocks (id, user_id, post_id, source, source_ref, matched_title, decided_by, blocked_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (source, source_ref) DO NOTHING`,
		uuid.New(), in.UserID, in.PostID, CopyrightBlockSourceTitleReview, in.ReviewID, title, in.DecidedBy, in.BlockedAt.UTC())
	if err != nil {
		return false, fmt.Errorf("record copyright block: %w", err)
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

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Follower deltas (TikTok parity, 2026-10-15): the daily follow/unfollow
// counts behind followers_delta on GET /v1/analytics/creator/me. The
// table, its receipts and the tracking marker are migration 013; the
// writer is consumers.FollowerDeltaConsumer.

// FollowerDelta is one change to one creator's follower count, applied
// to the UTC day of Day.
type FollowerDelta struct {
	CreatorID uuid.UUID
	Day       time.Time
	Follows   int64
	Unfollows int64
}

// ApplyFollowerEvent records the event's receipt and its deltas in one
// transaction. A receipt that already exists means the event was applied
// before (a Kafka redelivery or an outbox re-publish) and nothing is
// written; applied reports which happened. An event with no deltas still
// gets its receipt, so a no-op is recognised as one on redelivery too.
func (s *Store) ApplyFollowerEvent(ctx context.Context, eventID, eventType string, deltas []FollowerDelta) (applied bool, err error) {
	if eventID == "" {
		return false, errors.New("follower event has no event id")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	command, err := tx.Exec(ctx, `
		INSERT INTO analytics.follower_event_receipts (event_id, event_type)
		VALUES ($1, $2)
		ON CONFLICT (event_id) DO NOTHING`, eventID, eventType)
	if err != nil {
		return false, fmt.Errorf("follower receipt: %w", err)
	}
	if command.RowsAffected() == 0 {
		return false, nil
	}
	for _, d := range deltas {
		if d.CreatorID == uuid.Nil || (d.Follows == 0 && d.Unfollows == 0) {
			continue
		}
		if d.Follows < 0 || d.Unfollows < 0 {
			return false, fmt.Errorf("follower delta for %s is negative", d.CreatorID)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO analytics.creator_follower_daily (creator_id, day_bucket, follows, unfollows, updated_at)
			VALUES ($1, $2::date, $3, $4, NOW())
			ON CONFLICT (creator_id, day_bucket) DO UPDATE SET
				follows    = analytics.creator_follower_daily.follows + EXCLUDED.follows,
				unfollows  = analytics.creator_follower_daily.unfollows + EXCLUDED.unfollows,
				updated_at = NOW()`,
			d.CreatorID, d.Day.UTC().Format("2006-01-02"), d.Follows, d.Unfollows); err != nil {
			return false, fmt.Errorf("follower daily upsert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// PruneFollowerReceipts deletes receipts accepted before `before`. Only
// ever called with a cut-off well past the topic's retention.
func (s *Store) PruneFollowerReceipts(ctx context.Context, before time.Time) (int64, error) {
	command, err := s.db.Exec(ctx,
		`DELETE FROM analytics.follower_event_receipts WHERE accepted_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

// CreatorFollowersDelta is follows minus unfollows for the creator over
// the UTC days from since's day through now's day — the same day
// boundaries GetCreatorPeriodTotals uses for views.
//
// tracked is false when follower tracking has not started on this
// database (no follower_tracking row: the migration has not run, or ran
// in baseline mode). Then delta is meaningless and the caller reports
// null. Once tracking has started, a creator with no rows is a real 0.
func (s *AggregateStore) CreatorFollowersDelta(ctx context.Context, creatorID uuid.UUID, since, now time.Time) (delta int64, tracked bool, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM analytics.follower_tracking),
		       COALESCE((
		           SELECT SUM(follows - unfollows)
		             FROM analytics.creator_follower_daily
		            WHERE creator_id = $1
		              AND day_bucket >= $2::date
		              AND day_bucket <= $3::date
		       ), 0)::bigint`,
		creatorID, since.UTC().Format("2006-01-02"), now.UTC().Format("2006-01-02"),
	).Scan(&tracked, &delta)
	if err != nil {
		return 0, false, fmt.Errorf("creator followers delta: %w", err)
	}
	if !tracked {
		return 0, false, nil
	}
	return delta, true, nil
}

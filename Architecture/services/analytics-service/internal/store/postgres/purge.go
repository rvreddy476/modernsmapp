package postgres

import (
	"context"
	"fmt"

	"github.com/atpost/analytics-service/internal/model"
	"github.com/google/uuid"
)

// ── Account erasure (reels-8) ───────────────────────────────────────────────
//
// auth-service emits user.purge_requested 30 days after an account deletion
// and completes the purge only when every required service has acked
// (identity-platform/services/auth-service/internal/purge). analytics acks
// as "analytics" through internal/purge once PurgeUser and the Scylla and
// Redis erasures have all returned nil.
//
// PurgePlan below is the decision for every column in the analytics schema
// that holds a person's id. purge_plan_test.go reads setup.sql and every
// migration and fails when a user-id column appears that the plan does not
// name, so a new table cannot quietly escape erasure.
//
// Bounded batches, not one transaction: a heavy viewer has hundreds of
// thousands of heartbeat rows, and one DELETE of all of them would hold
// locks on the ingest tables for as long as it takes. Every statement is a
// delete by the user's own id, so a crash between batches loses nothing: the
// retry (auth re-emits every 24h, and the consumer holds its offset until
// the ack) deletes what is left. A second run finds nothing and succeeds.

// PurgeAction is what account erasure does to one user-id column.
type PurgeAction string

const (
	// PurgeDelete removes every row whose column holds the user.
	PurgeDelete PurgeAction = "delete"
	// PurgeRetain keeps the rows. Reason says why; each one is an open
	// decision recorded in the reels-8 report, not an oversight.
	PurgeRetain PurgeAction = "retain"
)

// PurgeTarget is one (table, column) decision.
type PurgeTarget struct {
	Table  string
	Column string
	Action PurgeAction
	Reason string
}

// creatorSideRetained is the reason shared by the creator-keyed aggregates.
const creatorSideRetained = "creator-side aggregate: monetization-service reads it for Creator Fund settlement " +
	"(monetization-service/internal/store/postgres/analytics_contract.go); erasing or re-keying it must be " +
	"agreed with monetization's own purge, which auth-service does not require yet"

// PurgePlan names every user-id column in the analytics schema. Order is
// the order PurgeUser runs the deletes: ingest_receipts before events_raw,
// because the receipts are found through the content ids of the user's own
// events and sessions.
var PurgePlan = []PurgeTarget{
	// The user as a viewer: raw events, dedupe receipts, playback sessions.
	{Table: "analytics.ingest_receipts", Column: "actor_id", Action: PurgeDelete,
		Reason: "the viewer's per-event receipts (session, content, event type)"},
	{Table: "analytics.playback_sessions", Column: "actor_id", Action: PurgeDelete,
		Reason: "the viewer's watch sessions; settled aggregates hold no viewer id and stay"},
	{Table: "analytics.events_raw", Column: "user_id", Action: PurgeDelete,
		Reason: "the viewer's raw events"},
	// The user as a creator, not money (migration 013 says so).
	{Table: "analytics.creator_follower_daily", Column: "creator_id", Action: PurgeDelete,
		Reason: "the creator's own daily follow/unfollow counts; not read by monetization"},
	// Gamification (migration 003): unused today, deleted all the same.
	{Table: "user_streaks", Column: "user_id", Action: PurgeDelete, Reason: "per-user gamification"},
	{Table: "user_badges", Column: "user_id", Action: PurgeDelete, Reason: "per-user gamification"},
	{Table: "mission_progress", Column: "user_id", Action: PurgeDelete, Reason: "per-user gamification"},
	{Table: "loyalty_points", Column: "user_id", Action: PurgeDelete, Reason: "per-user gamification"},
	{Table: "point_transactions", Column: "user_id", Action: PurgeDelete, Reason: "per-user gamification"},

	// The user as a creator, money-bearing: kept (see creatorSideRetained).
	{Table: "analytics.content_ownership", Column: "creator_id", Action: PurgeRetain, Reason: creatorSideRetained},
	{Table: "analytics.content_hourly_agg", Column: "creator_id", Action: PurgeRetain, Reason: creatorSideRetained},
	{Table: "analytics.content_daily_summary", Column: "creator_id", Action: PurgeRetain, Reason: creatorSideRetained},
	{Table: "analytics.content_type_exclusions", Column: "creator_id", Action: PurgeRetain, Reason: creatorSideRetained},
	{Table: "analytics.playback_sessions", Column: "creator_id", Action: PurgeRetain,
		Reason: "other viewers' sessions on the creator's content, snapshotting the creator id; the hourly rebuild reads it. " + creatorSideRetained},
}

// purgeBatchSize bounds every DELETE.
const purgeBatchSize = 5000

// purgeContentPage bounds the content ids one receipts pass scopes to.
const purgeContentPage = 500

// uuidPattern guards a cast of a JSON payload field; CASE fixes the order
// so a malformed legacy value is skipped, never cast.
const uuidPattern = `'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'`

// purgeDeleteSQL is the bounded delete for a plain PurgeDelete target. The
// ctid array form lets PostgreSQL use a TID scan for the outer delete.
func purgeDeleteSQL(t PurgeTarget) string {
	if t.Table == "analytics.events_raw" {
		// Partitioned: ctid is per partition, so batch by event id under
		// the user filter (idx_events_user_ts serves both halves). id has
		// no NOT NULL; a legacy row without one goes in the first batch.
		return `DELETE FROM analytics.events_raw WHERE user_id = $1 AND (id IS NULL OR id = ANY(ARRAY(
			SELECT id FROM analytics.events_raw WHERE user_id = $1 AND id IS NOT NULL LIMIT $2)))`
	}
	return fmt.Sprintf(`DELETE FROM %[1]s WHERE ctid = ANY(ARRAY(
		SELECT ctid FROM %[1]s WHERE %[2]s = $1 LIMIT $2))`, t.Table, t.Column)
}

// userContentIDsSQL pages the content ids of the user's own sessions and
// events, the scope ingest_receipts is deleted through (it has no index
// leading with actor_id; idx_ingest_receipts_content leads with content_id).
const userContentIDsSQL = `
	SELECT content_id FROM (
		SELECT content_id FROM analytics.playback_sessions WHERE actor_id = $1
		UNION
		SELECT CASE WHEN payload->>'content_id' ~ ` + uuidPattern + `
		            THEN (payload->>'content_id')::uuid END
		FROM analytics.events_raw WHERE user_id = $1
	) c
	WHERE content_id IS NOT NULL AND content_id > $2
	ORDER BY content_id
	LIMIT $3`

const receiptsDeleteSQL = `DELETE FROM analytics.ingest_receipts WHERE ctid = ANY(ARRAY(
	SELECT ctid FROM analytics.ingest_receipts
	WHERE content_id = ANY($2::uuid[]) AND actor_id = $1 LIMIT $3))`

// watchSessionKeysSQL pages the (content, session) keys of the user's play
// sessions, for the Scylla watch_sessions erase. Read before PurgeUser.
const watchSessionKeysSQL = `
	SELECT content_id, session_id FROM (
		SELECT content_id, session_id::text AS session_id
		FROM analytics.playback_sessions WHERE actor_id = $1
		UNION
		SELECT CASE WHEN payload->>'content_id' ~ ` + uuidPattern + `
		            THEN (payload->>'content_id')::uuid END,
		       session_id::text
		FROM analytics.events_raw
		WHERE user_id = $1 AND type = 'play_end' AND session_id IS NOT NULL
	) k
	WHERE content_id IS NOT NULL AND (content_id, session_id) > ($2::uuid, $3::text)
	ORDER BY content_id, session_id
	LIMIT $4`

// WatchSessionKeys returns up to limit of the user's watch-session keys
// after the given key, in key order. Implements purge.SessionKeySource.
func (s *Store) WatchSessionKeys(ctx context.Context, userID uuid.UUID, after model.WatchSessionKey, limit int) ([]model.WatchSessionKey, error) {
	rows, err := s.db.Query(ctx, watchSessionKeysSQL, userID, after.ContentID, after.SessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("purge: watch session keys: %w", err)
	}
	defer rows.Close()
	var out []model.WatchSessionKey
	for rows.Next() {
		var k model.WatchSessionKey
		if err := rows.Scan(&k.ContentID, &k.SessionID); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// PurgeUser deletes every PostgreSQL row PurgePlan marks PurgeDelete for
// the user. Idempotent and safe to retry. Implements purge.PGStore.
func (s *Store) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return fmt.Errorf("purge: nil user id")
	}
	if err := s.purgeReceipts(ctx, userID); err != nil {
		return err
	}
	for _, t := range PurgePlan {
		if t.Action != PurgeDelete || t.Table == "analytics.ingest_receipts" {
			continue
		}
		if err := s.deleteInBatches(ctx, purgeDeleteSQL(t), t.Table, userID); err != nil {
			return err
		}
	}
	return nil
}

// deleteInBatches repeats a bounded delete until one deletes nothing. The
// stop is "zero rows", not "a short batch": a short batch can still leave
// rows behind (an events_raw id shared by two rows deletes both), and the
// extra empty pass is one indexed lookup.
func (s *Store) deleteInBatches(ctx context.Context, sql, table string, args ...any) error {
	for {
		tag, err := s.db.Exec(ctx, sql, append(args, purgeBatchSize)...)
		if err != nil {
			return fmt.Errorf("purge: delete from %s: %w", table, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// purgeReceipts walks the user's content ids a page at a time and deletes
// their receipts on each. Runs first: the ids come from the sessions and
// events the later deletes remove.
func (s *Store) purgeReceipts(ctx context.Context, userID uuid.UUID) error {
	after := uuid.Nil
	for {
		rows, err := s.db.Query(ctx, userContentIDsSQL, userID, after, purgeContentPage)
		if err != nil {
			return fmt.Errorf("purge: user content ids: %w", err)
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("purge: user content ids: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		for {
			tag, err := s.db.Exec(ctx, receiptsDeleteSQL, userID, ids, purgeBatchSize)
			if err != nil {
				return fmt.Errorf("purge: delete from analytics.ingest_receipts: %w", err)
			}
			if tag.RowsAffected() == 0 {
				break
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if len(ids) < purgeContentPage {
			return nil
		}
		after = ids[len(ids)-1]
	}
}

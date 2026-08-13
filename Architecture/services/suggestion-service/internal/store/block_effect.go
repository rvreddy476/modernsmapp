package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Module 3 CLB-1 — the UserBlocked safety effect is ONE durable transaction.
//
// WHAT WAS WRONG
//
// The consumer applied a block by making seven independent calls — two cooldown
// upserts and four candidate deletes through helpers that discarded their
// errors, plus cache deletes — and then returned nil regardless. With
// PostgreSQL unavailable every one of them failed silently and the handler
// still reported success, so the broker offset advanced past an event whose
// safety effect had never been written. The blocked account stayed in the
// viewer's suggestions and nothing anywhere recorded that it should not.
//
// WHY A TRANSACTION AND AN INBOX ROW
//
// Kafka delivery is at-least-once, so the same UserBlocked event WILL be
// redelivered — after a rebalance, after a relay retry, after a consumer crash
// between applying the effect and committing the offset. Correctness therefore
// needs two properties that only a single transaction can give together:
//
//   - all-or-nothing: a partial block (one direction cooled down, the other
//     not) must never be observable, and must never be mistaken for a
//     completed one;
//   - idempotence: the inbox row is inserted in the SAME transaction as the
//     effect, so "this event has been applied" and "the effect exists" commit
//     or roll back as one fact. A replay sees the conflict and does nothing.
//
// Putting the applied-marker in Redis instead cannot do this: Redis would
// record the event as applied and PostgreSQL would then fail, and the replay
// that could have repaired it would be suppressed. That is exactly the loss
// path this replaces.
//
// The failure is RETURNED so the consumer can decline to commit the offset and
// let the broker redeliver. Cache invalidation is deliberately NOT in here —
// it is rebuildable state that expires on its own, and letting a Redis blip
// roll back a durable safety write would be the wrong trade.

// ConsumerInboxDDL creates the durable applied-event ledger.
//
// event_id is the graph outbox ROW id (the publisher sets EventID to it rather
// than minting a fresh uuid per publish), which is what makes a redelivery
// recognisable at all.
const ConsumerInboxDDL = `
CREATE TABLE IF NOT EXISTS suggestion_consumer_inbox (
	event_id   TEXT PRIMARY KEY,
	event_type VARCHAR(48) NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_consumer_inbox_applied ON suggestion_consumer_inbox (applied_at);
`

// ApplyUserBlockedEffects writes the complete UserBlocked safety effect in one
// transaction and records the event as applied in the same transaction.
//
// It reports whether THIS call applied the effect. false with a nil error means
// the event was already applied by an earlier delivery — a replay, which is a
// success for the caller: the state is present and the offset may be committed.
// A non-nil error means nothing was written and the caller must NOT commit.
func (s *Store) ApplyUserBlockedEffects(ctx context.Context, eventID string, blockerID, blockedID uuid.UUID) (bool, error) {
	tx, err := s.appDB.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin block effect: %w", err)
	}
	// Rollback after a successful Commit is a no-op; this is the guard for
	// every early return below.
	defer func() { _ = tx.Rollback(ctx) }()

	if eventID != "" {
		ct, err := tx.Exec(ctx, `
			INSERT INTO suggestion_consumer_inbox (event_id, event_type)
			VALUES ($1, 'UserBlocked')
			ON CONFLICT (event_id) DO NOTHING
		`, eventID)
		if err != nil {
			return false, fmt.Errorf("claim inbox %s: %w", eventID, err)
		}
		if ct.RowsAffected() == 0 {
			// Already applied. The effect committed with the row that is
			// already there, so there is nothing to repeat.
			return false, nil
		}
	}

	// Permanent cooldown in BOTH directions. A block is symmetric: neither
	// party may be suggested to the other.
	for _, pair := range [2][2]uuid.UUID{{blockerID, blockedID}, {blockedID, blockerID}} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO suggestion_cooldowns (viewer_id, candidate_id, cooldown_type, cooldown_until)
			VALUES ($1, $2, 'block', NULL)
			ON CONFLICT (viewer_id, candidate_id) DO UPDATE SET
				cooldown_type  = 'block',
				cooldown_until = NULL,
				created_at     = now()
		`, pair[0], pair[1]); err != nil {
			return false, fmt.Errorf("cooldown %s->%s: %w", pair[0], pair[1], err)
		}
	}

	// Remove any already-generated candidate rows in both directions and for
	// every suggestion type. Cooldowns keep the pair out of FUTURE batches;
	// this removes what is already sitting in the pool.
	if _, err := tx.Exec(ctx, `
		DELETE FROM suggestion_candidates
		WHERE (viewer_id = $1 AND candidate_id = $2)
		   OR (viewer_id = $2 AND candidate_id = $1)
	`, blockerID, blockedID); err != nil {
		return false, fmt.Errorf("remove candidates %s<->%s: %w", blockerID, blockedID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit block effect: %w", err)
	}
	return true, nil
}

// UserBlockedEffectApplied reports whether the durable inbox has recorded this
// event. Used by tests and by operational checks; the consumer relies on the
// transactional conflict above rather than on a separate read.
func (s *Store) UserBlockedEffectApplied(ctx context.Context, eventID string) (bool, error) {
	var n int
	err := s.appDB.QueryRow(ctx,
		`SELECT count(*) FROM suggestion_consumer_inbox WHERE event_id = $1`, eventID).Scan(&n)
	return n > 0, err
}

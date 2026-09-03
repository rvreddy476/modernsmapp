package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ── Account control: hide / unhide / purge ──────────────────────────────────
//
// See Architecture/shared/events/events.go ("Account control") and
// internal/purge. Hide is a row in hidden_users; purge is one transaction that
// removes every row keyed by the user and the hidden_users row itself.

// SetUserHidden hides (true) or unhides (false) a user. Idempotent.
func (s *Store) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, reason string) error {
	if hidden {
		_, err := s.db.Exec(ctx, `
			INSERT INTO hidden_users (user_id, reason) VALUES ($1, $2)
			ON CONFLICT (user_id) DO UPDATE SET reason = EXCLUDED.reason, hidden_at = NOW()`,
			userID, reason)
		return err
	}
	_, err := s.db.Exec(ctx, `DELETE FROM hidden_users WHERE user_id = $1`, userID)
	return err
}

// AnyHidden reports whether either user is currently hidden. A hidden user is
// treated by the permission matrix exactly like a blocked pair.
func (s *Store) AnyHidden(ctx context.Context, a, b uuid.UUID) (bool, error) {
	var hidden bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM hidden_users WHERE user_id = $1 OR user_id = $2)`, a, b,
	).Scan(&hidden)
	return hidden, err
}

// PurgeUser erases every graph row keyed by the user in ONE transaction:
// follows (both directions, with the counterparties' counts corrected),
// blocks, mutes, connections, connection_requests, follow_requests,
// close_friends, favorites, relationship_labels, circles, counts and the
// hidden_users marker. Idempotent — a second call deletes nothing.
//
// graph_outbox_events rows about this user are NOT touched: they are the
// durable delivery ledger for events already committed, and the relay marks
// them published on its own.
func (s *Store) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	exec := func(stmt string) error {
		if _, err := tx.Exec(ctx, stmt, userID); err != nil {
			return fmt.Errorf("purge: %s: %w", firstWords(stmt), err)
		}
		return nil
	}

	// Counters on the OTHER side of each edge shrink before the edges go.
	stmts := []string{
		`UPDATE counts c SET follower_count = GREATEST(c.follower_count - 1, 0), updated_at = NOW()
		   FROM follows f WHERE f.follower_id = $1 AND c.user_id = f.followee_id`,
		`UPDATE counts c SET following_count = GREATEST(c.following_count - 1, 0), updated_at = NOW()
		   FROM follows f WHERE f.followee_id = $1 AND c.user_id = f.follower_id`,
		`UPDATE counts c SET friend_count = GREATEST(c.friend_count - 1, 0), updated_at = NOW()
		   FROM connections x WHERE (x.user_a = $1 AND c.user_id = x.user_b) OR (x.user_b = $1 AND c.user_id = x.user_a)`,
		`DELETE FROM follows WHERE follower_id = $1 OR followee_id = $1`,
		`DELETE FROM blocks WHERE blocker_id = $1 OR blocked_id = $1`,
		`DELETE FROM graph.mutes WHERE muter_id = $1 OR muted_id = $1`,
		`DELETE FROM connections WHERE user_a = $1 OR user_b = $1`,
		`DELETE FROM connection_requests WHERE sender_id = $1 OR receiver_id = $1`,
		`DELETE FROM follow_requests WHERE requester_id = $1 OR target_id = $1`,
		`DELETE FROM close_friends WHERE user_id = $1 OR friend_id = $1`,
		`DELETE FROM favorites WHERE user_id = $1 OR target_id = $1`,
		`DELETE FROM relationship_labels WHERE user_id = $1 OR target_id = $1`,
		`DELETE FROM circle_members WHERE user_id = $1 OR circle_id IN (SELECT id FROM circles WHERE owner_id = $1)`,
		`DELETE FROM circles WHERE owner_id = $1`,
		`DELETE FROM counts WHERE user_id = $1`,
		`DELETE FROM hidden_users WHERE user_id = $1`,
	}
	for _, st := range stmts {
		if err := exec(st); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("purge: commit: %w", err)
	}
	return nil
}

// InsertPurgeAckOutbox writes the purge ack into graph_outbox_events so the
// existing relay (with its lease/retry/backoff) delivers it. The relay routes
// event_type user.purge_acked to the purge-acks topic. actor = target = the
// purged user so the pair sequence stays well-formed.
func (s *Store) InsertPurgeAckOutbox(ctx context.Context, userID uuid.UUID, payload any) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge ack outbox: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := appendGraphOutboxTx(ctx, tx, "user.purge_acked", userID, userID, payload); err != nil {
		return fmt.Errorf("purge ack outbox: %w", err)
	}
	return tx.Commit(ctx)
}

func firstWords(stmt string) string {
	var b []byte
	n := 0
	for i := 0; i < len(stmt); i++ {
		c := stmt[i]
		if c == ' ' || c == '\n' || c == '\t' {
			if len(b) > 0 && b[len(b)-1] != ' ' {
				b = append(b, ' ')
				n++
			}
			if n == 3 {
				break
			}
			continue
		}
		b = append(b, c)
	}
	return string(b)
}

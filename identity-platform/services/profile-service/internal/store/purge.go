package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ── Account control: hide / purge (acks as "profile") ───────────────────────
//
// auth-service owns auth.*; this service owns profile.*. The purge erases the
// profile.* slice only — auth anonymises its own credential row once every
// required service (this one acks as "profile") has acked. See
// internal/purge, and Architecture/shared/events/events.go ("Account
// control — deactivate / delete / purge") for the wire contract.

// SetUserHidden marks/unmarks a profile hidden because auth-service reported
// user.deactivated or user.deletion_scheduled (hidden=true) / user.reactivated
// or user.deletion_cancelled (hidden=false). Reversible: never erases.
//
// hidden=true is a conditional INSERT — it only ever writes a row when
// profile.profiles already has one for this user. Without that guard, hiding
// a user profile-service has never heard of (no UserRegistered processed yet)
// would violate the FK to profile.profiles and turn a routine lifecycle event
// into a hard error that HandleUntilDurable would retry forever. Skipping it
// is correct: there is nothing to hide.
//
// hidden=false deletes the marker row. Idempotent both ways: a repeat call
// with nothing to do returns nil.
func (s *Store) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, reason string) error {
	if !hidden {
		_, err := s.db.Exec(ctx, `DELETE FROM profile.hidden_profiles WHERE user_id = $1`, userID)
		return err
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO profile.hidden_profiles (user_id, reason)
		SELECT $1, $2 WHERE EXISTS (SELECT 1 FROM profile.profiles WHERE user_id = $1)
		ON CONFLICT (user_id) DO UPDATE SET reason = EXCLUDED.reason, hidden_at = NOW()`,
		userID, reason)
	return err
}

// IsHidden reports whether userID currently has a hidden_profiles marker.
// Backs the GetProfile / GetProfileByUsername / GetProfilesBatch read gate —
// see internal/http/hidden_denial_gate.go.
func (s *Store) IsHidden(ctx context.Context, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM profile.hidden_profiles WHERE user_id = $1)`, userID,
	).Scan(&exists)
	return exists, err
}

// PurgeUser erases every profile.* row keyed by userID in ONE transaction.
// Never touches auth.* or usr.*. Idempotent: a second call finds nothing left
// to delete and still commits cleanly, because auth-service re-emits
// user.purge_requested every 24h until it sees this service's ack.
//
// Deletes children before the profile.profiles parent row, even though every
// child already carries ON DELETE CASCADE back to it — explicit statements
// keep each step visible in the error wrap and match the sibling
// identity-platform/services/user-service/internal/store/purge.go pattern.
//
// profile.inbox_events is deliberately NOT touched here. It dedups this
// consumer's Kafka deliveries by (consumer_name, event_id) — event_id is a
// random envelope id assigned by the producer, not derived from user_id, and
// the table carries no user_id column at all (see database/setup.sql). There
// is no "this user's row" to find there; it is consumer-wide processing
// state, not user data.
func (s *Store) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stmt := range []string{
		`DELETE FROM profile.profile_links WHERE profile_id = $1`,
		`DELETE FROM profile.user_links WHERE user_id = $1`,
		`DELETE FROM profile.user_about WHERE user_id = $1`,
		`DELETE FROM profile.handle_history WHERE user_id = $1`,
		`DELETE FROM profile.module_profiles WHERE user_id = $1`,
		`DELETE FROM profile.profile_stats WHERE user_id = $1`,
		`DELETE FROM profile.hidden_profiles WHERE user_id = $1`,
		`DELETE FROM profile.profiles WHERE user_id = $1`,
	} {
		if _, err := tx.Exec(ctx, stmt, userID); err != nil {
			return fmt.Errorf("purge: %s: %w", stmt[:30], err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("purge: commit: %w", err)
	}
	return nil
}

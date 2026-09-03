package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ── Account control: hide / purge (acks as "user") ──────────────────────────
//
// auth-service owns auth.*; this service owns usr.*. The purge erases the
// usr.* slice only — auth anonymises its own credential row once every
// required service (this one acks as "user") has acked.

// SetUserHidden flips usr.users.status between 'active' and 'hidden'.
// Only a 'hidden' row is ever flipped back, so a suspended account cannot be
// laundered into 'active' by a reactivation event. Idempotent.
func (s *Store) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, _ string) error {
	if hidden {
		_, err := s.db.Exec(ctx,
			`UPDATE usr.users SET status = 'hidden', updated_at = NOW() WHERE id = $1 AND status = 'active'`, userID)
		return err
	}
	_, err := s.db.Exec(ctx,
		`UPDATE usr.users SET status = 'active', updated_at = NOW() WHERE id = $1 AND status = 'hidden'`, userID)
	return err
}

// PurgeUser erases usr.module_preferences, usr.user_settings and the
// usr.users row in ONE transaction. Never touches auth.*. Idempotent.
func (s *Store) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stmt := range []string{
		`DELETE FROM usr.module_preferences WHERE user_id = $1`,
		`DELETE FROM usr.user_settings WHERE user_id = $1`,
		`DELETE FROM usr.users WHERE id = $1`,
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

package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Account control: hide / purge (acks as "notification") ──────────────────

// SetUserHidden inserts/removes the delivery-suppression row. While the row
// exists, createNotification drops every notification addressed to the
// user. Idempotent.
func (s *Store) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, reason string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if hidden {
		_, err := s.db.Exec(ctx, `
			INSERT INTO notification_suppressed_users (user_id, reason) VALUES ($1, $2)
			ON CONFLICT (user_id) DO UPDATE SET reason = EXCLUDED.reason, suppressed_at = NOW()`, userID, reason)
		return err
	}
	_, err := s.db.Exec(ctx, `DELETE FROM notification_suppressed_users WHERE user_id = $1`, userID)
	return err
}

// IsSuppressed reports whether deliveries to the user are suppressed.
func (s *Store) IsSuppressed(ctx context.Context, userID uuid.UUID) (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}
	var yes bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM notification_suppressed_users WHERE user_id = $1)`, userID).Scan(&yes)
	return yes, err
}

// PurgeUser erases every Postgres row keyed by the user in ONE transaction.
// notification_preferences.user_id is UUID or TEXT depending on which
// migration created it, so the comparison is done as text. Optional tables
// are probed with to_regclass. Idempotent.
func (s *Store) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	steps := []struct{ table, sql string }{
		{"notification_preferences", `DELETE FROM notification_preferences WHERE user_id::text = $1::text`},
		{"user_devices", `DELETE FROM user_devices WHERE user_id = $1`},
		{"notification_bundles", `DELETE FROM notification_bundles WHERE user_id = $1`},
		{"notification_digests", `DELETE FROM notification_digests WHERE user_id = $1`},
		{"subscriber_fanout_delivered", `DELETE FROM subscriber_fanout_delivered WHERE user_id = $1`},
		{"subscriber_fanout_jobs", `DELETE FROM subscriber_fanout_jobs WHERE author_id = $1`},
		{"notification_suppressed_users", `DELETE FROM notification_suppressed_users WHERE user_id = $1`},
	}
	for _, st := range steps {
		ok, err := tableExists(ctx, tx, st.table)
		if err != nil {
			return fmt.Errorf("purge: probe %s: %w", st.table, err)
		}
		if !ok {
			continue
		}
		if _, err := tx.Exec(ctx, st.sql, userID); err != nil {
			return fmt.Errorf("purge: %s: %w", st.table, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("purge: commit: %w", err)
	}
	return nil
}

func tableExists(ctx context.Context, tx pgx.Tx, table string) (bool, error) {
	var oid *uint32
	if err := tx.QueryRow(ctx, `SELECT to_regclass($1)::oid`, "public."+table).Scan(&oid); err != nil {
		return false, err
	}
	return oid != nil, nil
}

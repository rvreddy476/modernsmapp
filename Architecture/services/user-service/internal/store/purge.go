package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Account control: hide / purge (acks as "user-extras") ───────────────────
//
// See Architecture/shared/events/events.go ("Account control") and
// internal/purge. The identity user-service is the "user" ack; this service
// holds the app-side profile extras and acks as "user-extras".

// SetUserHidden flags the app.users projection row. Hidden rows keep every
// column (the account may come back); public surfaces that want to honour it
// read users.hidden. Idempotent; a missing row is not an error.
func (s *Store) SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, _ string) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET hidden = $2, updated_at = NOW() WHERE id = $1`, userID, hidden)
	return err
}

// purgeStatement is one erase step; table is checked with to_regclass so a
// database that never ran the optional migrations still purges cleanly.
type purgeStatement struct {
	table string
	sql   string
}

// PurgeUser erases every app-side profile row keyed by the user in ONE
// transaction: links, about, settings, portfolio, QR codes, wellbeing, screen
// time, pins, page/channel memberships, endorsements, reputation, referrals,
// reviews, the pages and channels the user OWNS (with their dependents), and
// finally the users row. Idempotent.
//
// Other services keep FKs to users(id) in the same database; if one of them
// has not purged yet the final DELETE fails with a foreign-key violation and
// the consumer retries with backoff until that service catches up.
func (s *Store) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	steps := []purgeStatement{
		{"user_links", `DELETE FROM user_links WHERE user_id = $1`},
		{"user_about", `DELETE FROM user_about WHERE user_id = $1`},
		{"user_settings", `DELETE FROM user_settings WHERE user_id = $1`},
		{"portfolio_items", `DELETE FROM portfolio_items WHERE user_id = $1`},
		{"profile_qr_codes", `DELETE FROM profile_qr_codes WHERE user_id = $1`},
		{"digital_wellbeing", `DELETE FROM digital_wellbeing WHERE user_id = $1`},
		{"screen_time_log", `DELETE FROM screen_time_log WHERE user_id = $1`},
		{"profile_pins", `DELETE FROM profile_pins WHERE user_id = $1`},
		{"page_followers", `DELETE FROM page_followers WHERE user_id = $1`},
		{"page_roles", `DELETE FROM page_roles WHERE user_id = $1`},
		{"channel_members", `DELETE FROM channel_members WHERE user_id = $1`},
		{"channel_subscriptions", `DELETE FROM channel_subscriptions WHERE user_id = $1`},
		{"endorsements", `DELETE FROM endorsements WHERE from_user_id = $1 OR to_user_id = $1`},
		{"user_reputation", `DELETE FROM user_reputation WHERE user_id = $1`},
		{"referrals", `DELETE FROM referrals WHERE referrer_id = $1 OR referee_id = $1`},
		{"business_reviews", `DELETE FROM business_reviews WHERE reviewer_id = $1`},
		// Pages the user owns, with dependents (most have ON DELETE CASCADE;
		// explicit for the ones that do not).
		{"page_followers", `DELETE FROM page_followers WHERE page_id IN (SELECT id FROM business_pages WHERE user_id = $1)`},
		{"page_roles", `DELETE FROM page_roles WHERE page_id IN (SELECT id FROM business_pages WHERE user_id = $1)`},
		{"page_verification_documents", `DELETE FROM page_verification_documents WHERE page_id IN (SELECT id FROM business_pages WHERE user_id = $1)`},
		{"business_reviews", `DELETE FROM business_reviews WHERE page_id IN (SELECT id FROM business_pages WHERE user_id = $1)`},
		{"business_pages", `DELETE FROM business_pages WHERE user_id = $1`},
		// Channels the user owns.
		{"channel_links", `DELETE FROM channel_links WHERE channel_id IN (SELECT id FROM channels WHERE user_id = $1)`},
		{"channel_milestones", `DELETE FROM channel_milestones WHERE channel_id IN (SELECT id FROM channels WHERE user_id = $1)`},
		{"channel_members", `DELETE FROM channel_members WHERE channel_id IN (SELECT id FROM channels WHERE user_id = $1)`},
		{"channel_subscriptions", `DELETE FROM channel_subscriptions WHERE channel_id IN (SELECT id FROM channels WHERE user_id = $1)`},
		{"channels", `DELETE FROM channels WHERE user_id = $1`},
		{"users", `DELETE FROM users WHERE id = $1`},
	}
	present := map[string]bool{}
	for _, st := range steps {
		ok, known := present[st.table]
		if !known {
			ok, err = tableExists(ctx, tx, st.table)
			if err != nil {
				return fmt.Errorf("purge: probe %s: %w", st.table, err)
			}
			present[st.table] = ok
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

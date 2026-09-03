package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Account control: purge (acks as "trust-safety") ─────────────────────────

// nilUserID replaces a purged user's id on rows that are moderation
// evidence and must survive (reports about OTHER users).
const nilUserID = "00000000-0000-0000-0000-000000000000"

// PurgeUser erases the user's trust slice in ONE transaction: user-scoped
// keyword filters, appeals, teen-account row, trust state, strikes,
// grievances and verification requests. Reports the user FILED are kept as
// evidence with the reporter anonymised (open ones are dismissed first so
// the one-active-report-per-reporter index cannot collide). Optional tables
// are probed with to_regclass. Idempotent.
func (s *ReportStore) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	steps := []struct{ table, sql string }{
		{"trust.keyword_filters", `DELETE FROM trust.keyword_filters WHERE scope = 'user' AND scope_id = $1`},
		{"trust.keyword_filters", `UPDATE trust.keyword_filters SET added_by = '` + nilUserID + `'::uuid WHERE added_by = $1`},
		{"trust.content_appeals", `DELETE FROM trust.content_appeals WHERE user_id = $1`},
		{"trust.content_appeals", `UPDATE trust.content_appeals SET reviewed_by = NULL WHERE reviewed_by = $1`},
		{"trust.teen_accounts", `DELETE FROM trust.teen_accounts WHERE user_id = $1`},
		{"trust.teen_accounts", `UPDATE trust.teen_accounts SET guardian_id = NULL WHERE guardian_id = $1`},
		{"trust.user_trust_state", `DELETE FROM trust.user_trust_state WHERE user_id = $1`},
		{"trust.user_strikes", `DELETE FROM trust.user_strikes WHERE user_id = $1`},
		{"trust.user_strikes", `UPDATE trust.user_strikes SET created_by = NULL WHERE created_by = $1`},
		{"trust.grievances", `DELETE FROM trust.grievances WHERE complainant_id = $1`},
		{"trust.grievances", `UPDATE trust.grievances SET assigned_to = NULL WHERE assigned_to = $1`},
		{"trust.verification_requests", `DELETE FROM trust.verification_requests WHERE user_id = $1`},
		{"trust.verification_requests", `UPDATE trust.verification_requests SET reviewed_by = NULL WHERE reviewed_by = $1`},
		{"trust.reports", `UPDATE trust.reports SET status = 'dismissed', updated_at = NOW()
			WHERE reporter_id = $1 AND status IN ('open', 'reviewing')`},
		{"trust.reports", `UPDATE trust.reports SET reporter_id = '` + nilUserID + `'::uuid, updated_at = NOW() WHERE reporter_id = $1`},
		{"trust.reports", `UPDATE trust.reports SET assigned_to = NULL WHERE assigned_to = $1`},
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
	if err := tx.QueryRow(ctx, `SELECT to_regclass($1)::oid`, table).Scan(&oid); err != nil {
		return false, err
	}
	return oid != nil, nil
}

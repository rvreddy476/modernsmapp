package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ── Account control: hide / purge (acks as "dating") ────────────────────────
//
// PurgeUserData (profiles.go) is the DPDP erase reused by both the 30-day
// cron worker and the platform purge consumer. These two methods cover what
// it deliberately leaves alone.

// PurgeUserAuxiliary removes account-risk and device-fingerprint rows in one
// transaction. dating_consent_log and dating_admin_audit are regulatory
// audit and are retained. Idempotent; missing tables are skipped.
func (s *Store) PurgeUserAuxiliary(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge aux: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, t := range []string{"dating_account_risk", "dating_device_fingerprints"} {
		var oid *uint32
		if err := tx.QueryRow(ctx, `SELECT to_regclass($1)::oid`, "public."+t).Scan(&oid); err != nil {
			return fmt.Errorf("purge aux: probe %s: %w", t, err)
		}
		if oid == nil {
			continue
		}
		if _, err := tx.Exec(ctx, `DELETE FROM `+t+` WHERE user_id = $1`, userID); err != nil {
			return fmt.Errorf("purge aux: %s: %w", t, err)
		}
	}
	return tx.Commit(ctx)
}

// Account-lifecycle hide/unhide is service.SetProfileHidden: it pauses
// through TransitionProfileStatus (profile_status.go) and invalidates the
// deck caches, so there is no raw pause writer here.

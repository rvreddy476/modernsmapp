package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ── Account control: purge (acks as "call") ─────────────────────────────────

// nilUserID is the sentinel written over a purged user's identity on
// call_sessions rows that other participants still own.
const nilUserID = "00000000-0000-0000-0000-000000000000"

// PurgeUser erases every calls.* row keyed by the user in ONE transaction.
// Sessions the user initiated are deleted when nobody else took part and
// anonymised (initiator = zero uuid) otherwise. Idempotent.
func (s *CallStore) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	stmts := []string{
		`DELETE FROM calls.call_device_sessions WHERE user_id = $1`,
		`DELETE FROM calls.call_reminders WHERE user_id = $1`,
		`DELETE FROM calls.call_invites WHERE inviter_user_id = $1 OR invitee_user_id = $1`,
		`DELETE FROM calls.call_event_summaries WHERE participant_user_id = $1`,
		`DELETE FROM calls.call_participants WHERE user_id = $1`,
		`UPDATE calls.call_summaries SET participants = array_remove(participants, $1) WHERE $1 = ANY(participants)`,
		// Sessions the user initiated with nobody else left: remove entirely.
		`DELETE FROM calls.call_event_summaries WHERE call_session_id IN (
			SELECT id FROM calls.call_sessions cs WHERE cs.initiator_user_id = $1
			  AND NOT EXISTS (SELECT 1 FROM calls.call_participants p WHERE p.call_session_id = cs.id)
			  AND NOT EXISTS (SELECT 1 FROM calls.call_invites i WHERE i.call_session_id = cs.id))`,
		`DELETE FROM calls.call_reminders WHERE call_session_id IN (
			SELECT id FROM calls.call_sessions cs WHERE cs.initiator_user_id = $1
			  AND NOT EXISTS (SELECT 1 FROM calls.call_participants p WHERE p.call_session_id = cs.id)
			  AND NOT EXISTS (SELECT 1 FROM calls.call_invites i WHERE i.call_session_id = cs.id))`,
		`DELETE FROM calls.call_summaries WHERE call_session_id IN (
			SELECT id FROM calls.call_sessions cs WHERE cs.initiator_user_id = $1
			  AND NOT EXISTS (SELECT 1 FROM calls.call_participants p WHERE p.call_session_id = cs.id)
			  AND NOT EXISTS (SELECT 1 FROM calls.call_invites i WHERE i.call_session_id = cs.id))`,
		`DELETE FROM calls.call_sessions cs WHERE cs.initiator_user_id = $1
			  AND NOT EXISTS (SELECT 1 FROM calls.call_participants p WHERE p.call_session_id = cs.id)
			  AND NOT EXISTS (SELECT 1 FROM calls.call_invites i WHERE i.call_session_id = cs.id)`,
		// Sessions with other participants keep the row; the identity goes.
		`UPDATE calls.call_sessions SET initiator_user_id = '` + nilUserID + `'::uuid WHERE initiator_user_id = $1`,
	}
	for _, st := range stmts {
		if _, err := tx.Exec(ctx, st, userID); err != nil {
			return fmt.Errorf("purge: %.48s: %w", st, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("purge: commit: %w", err)
	}
	return nil
}

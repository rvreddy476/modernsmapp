package store

import (
	"context"

	"github.com/atpost/shared/identityroles"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Identity role plumbing for the rider partner journey.
//
// # WHEN `rider_partner` IS GRANTED — AND WHY IT IS NOT AT APPROVAL
//
// The role is granted when the rider_partners row is CREATED, at
// status='draft', not when an admin approves. rider-service makes the argument
// unusually clearly: a fresh partner is 'draft' and the ONLY way to reach
// 'pending_verification' is to upload a KYC document at
// POST /v1/rider/partners/me/documents (see service.SubmitKYCDocument), which
// is a partner-area route. Gate the role on approval and the partner can never
// reach the endpoint that makes approval possible. The journey deadlocks.
//
// rider_partners.status keeps gating what they may actually do. identity says
// who you are; that column says what state you are in.
//
// # THE STATUS TABLE
//
// Kept in ONE function so the admin routes, the onboarding transition and the
// nightly fraud job cannot disagree about what 'suspended' means. It matches
// commerce-service's and food-service's tables; see shared/identityroles'
// package doc.
//
//	draft, pending_verification, approved -> grant
//	rejected, blocked                     -> revoke (terminal: off the journey)
//	suspended, inactive                   -> no change (a pause)
//
// 'suspended' does not revoke because a suspended partner still needs the
// partner area to see why and to appeal — and because rider-service has no
// unblock/reactivate route at all today (see the report), so a revoke here
// would be unrecoverable without a database edit.

// riderRoleForStatus maps a partner status to a role action. The bool reports
// whether any action is needed.
func riderRoleForStatus(status string) (identityroles.Op, bool) {
	switch status {
	case "draft", "pending_verification", "approved":
		// Re-granting is idempotent, and it is the safety net for a partner
		// whose create-time intent was dead-lettered during an outage.
		return identityroles.OpGrant, true
	case "rejected", "blocked":
		return identityroles.OpRevoke, true
	default:
		// suspended, inactive — a pause, not a departure.
		return "", false
	}
}

// WithRoleIntents attaches the identity role queue, matching the Set…/With…
// convention the Service uses for its optional dependencies. nil is supported:
// rider-service keeps working when identity is not configured.
func (s *Store) WithRoleIntents(ob *identityroles.Outbox) *Store {
	s.roles = ob
	return s
}

// enqueueRoleIntentTx records a grant/revoke in the SAME transaction as the
// partner row it describes.
//
// Calling identity inline would put another service's availability on the
// critical path of an approval, and a failed call would leave an approved
// partner who can never see the partner area, with nothing recording why. The
// intent commits with the row instead, and shared/identityroles' Worker
// delivers it, retrying through an outage.
func (s *Store) enqueueRoleIntentTx(ctx context.Context, tx pgx.Tx, op identityroles.Op, userID uuid.UUID, reason string) error {
	if s.roles == nil {
		return nil
	}
	return s.roles.Enqueue(ctx, tx, identityroles.Intent{
		Op:     op,
		UserID: userID.String(),
		Role:   identityroles.RoleRiderPartner,
		Reason: reason,
	})
}

// updatePartnerStatusTx wraps a partner status update so the role intent
// commits with it.
//
// It takes the whole UPDATE as `sql` and expects it to RETURNING user_id.
// Doing it this way keeps every rider partner status writer on one code path,
// which matters because the three callers (admin status changes, admin
// approval, the fraud job) were previously three unrelated bare Execs with no
// transaction between them.
func (s *Store) updatePartnerStatusTx(ctx context.Context, sql, status, reason string, args ...any) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID uuid.UUID
	if err := tx.QueryRow(ctx, sql, args...).Scan(&userID); err != nil {
		return err
	}
	if op, ok := riderRoleForStatus(status); ok {
		if err := s.enqueueRoleIntentTx(ctx, tx, op, userID, reason); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

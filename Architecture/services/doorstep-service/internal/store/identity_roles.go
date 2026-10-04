package store

import (
	"context"

	"github.com/atpost/shared/identityroles"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Identity role plumbing for the Doorstep professional journey
// (tools/identityrolebackfill names this file as the table its "doorstep"
// query mirrors).
//
// # WHEN `service_professional` IS GRANTED — AND WHY NOT AT APPROVAL
//
// The role is granted when the doorstep.professionals row is CREATED
// (POST /v1/doorstep/pro/apply, status 'draft'), not when an admin approves.
// Every onboarding step — DigiLocker, selfie, skills, service area, hours,
// bank, police certificate, agreement — is a /pro route the professional
// must reach BEFORE anyone can approve them. Gate the role on approval and
// the journey deadlocks.
//
// doorstep.professionals.status keeps gating what they may DO (only
// 'approved' goes on duty or receives offers). identity says who you are;
// that column says what state you are in.
//
// # THE STATUS TABLE (shared with commerce, food and rider)
//
//	draft, pending_verification, approved -> grant (idempotent; the
//	                                         safety net for a lost intent)
//	rejected, blocked                     -> revoke (terminal: off the journey)
//	suspended                             -> no change (a pause: they need
//	                                         the app to see why and appeal)
//
// doorstep.professionals.user_id is UNIQUE, so a revoke needs no
// "is there another record" check (food's restaurant-owner trap).

// proRoleForStatus maps a professional status to a role action. The bool
// reports whether any action is needed. One function, so apply, approve,
// reject, suspend, reinstate and block cannot disagree.
func proRoleForStatus(status string) (identityroles.Op, bool) {
	switch status {
	case "draft", "pending_verification", "approved":
		return identityroles.OpGrant, true
	case "rejected", "blocked":
		return identityroles.OpRevoke, true
	default:
		// suspended — a pause, not a departure.
		return "", false
	}
}

// WithRoleIntents attaches the identity role queue. nil is supported:
// doorstep keeps working when identity is not configured.
func (s *Store) WithRoleIntents(ob *identityroles.Outbox) *Store {
	s.roles = ob
	return s
}

// enqueueRoleForStatusTx records the grant/revoke a status implies in the
// SAME transaction as the professional row it describes. The intent commits
// with the row and shared/identityroles' Worker delivers it, retrying
// through an identity outage; the approval never waits on identity.
func (s *Store) enqueueRoleForStatusTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, status, reason string) error {
	if s.roles == nil {
		return nil
	}
	op, ok := proRoleForStatus(status)
	if !ok {
		return nil
	}
	return s.roles.Enqueue(ctx, tx, identityroles.Intent{
		Op:     op,
		UserID: userID.String(),
		Role:   identityroles.RoleServiceProfessional,
		Reason: reason,
	})
}

package postgres

import (
	"context"

	"github.com/atpost/shared/identityroles"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Identity role plumbing for the two food partner journeys.
//
// # WHEN A ROLE IS GRANTED — AND WHY IT IS NOT AT APPROVAL
//
// `restaurant_owner` and `delivery_partner` are granted when the partner ROW
// IS CREATED, at status PENDING_REVIEW, not when an admin approves. The reason
// is concrete: a delivery partner must upload documents
// (POST /v1/food/delivery/documents) and a restaurant owner must fill in a
// menu and a profile, and all of that lives behind the partner area, BEFORE
// anybody reviews them. Gate the role on approval and you lock people out of
// the flow that gets them approved.
//
// food.delivery_partners.status keeps gating what they may actually do — only
// an ACTIVE partner receives dispatch offers (see delivery_offers.go). identity
// says who you are; that column says what state you are in.
//
// REVOKE fires on REJECTED and CLOSED. It does NOT fire on SUSPENDED, OFFLINE
// or TEMP_CLOSED: those are pauses, and the partner still needs the partner
// area to see why and to come back. This table is shared with
// commerce-service and rider-service; see shared/identityroles' package doc
// for the whole argument, and keep the three in step.
//
// # THE ONE-OWNER-MANY-RESTAURANTS TRAP
//
// food.restaurant_partners.owner_user_id is NOT unique — one person can own
// several partner rows. So rejecting ONE restaurant must not strip
// `restaurant_owner` from someone who still runs another. revokeRestaurantOwnerIfLast
// checks for a surviving non-terminal row first. delivery_partners.user_id IS
// unique, so its revoke needs no such check.

// enqueueRoleIntent records a grant/revoke in the SAME transaction as the
// partner row it describes, so the two commit or roll back together. A nil
// queue is a no-op: food-service must keep working when identity is not
// configured.
func (s *Store) enqueueRoleIntent(ctx context.Context, tx pgx.Tx, op identityroles.Op, userID uuid.UUID, role, reason string) error {
	if s.roles == nil {
		return nil
	}
	return s.roles.Enqueue(ctx, tx, identityroles.Intent{
		Op:     op,
		UserID: userID.String(),
		Role:   role,
		Reason: reason,
	})
}

// revokeRestaurantOwnerIfLast revokes `restaurant_owner` only when ownerID has
// no remaining restaurant_partners row in a live status.
//
// Terminal statuses are REJECTED and CLOSED. SUSPENDED counts as SURVIVING on
// purpose — a suspended partner is still on the journey, so an owner whose
// only other restaurant is suspended keeps the role.
func (s *Store) revokeRestaurantOwnerIfLast(ctx context.Context, tx pgx.Tx, ownerID uuid.UUID, excludePartnerID uuid.UUID, reason string) error {
	if s.roles == nil {
		return nil
	}
	var survives bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM food.restaurant_partners
			WHERE owner_user_id = $1
			  AND id <> $2
			  AND status NOT IN ('REJECTED','CLOSED')
		)`, ownerID, excludePartnerID).Scan(&survives); err != nil {
		return err
	}
	if survives {
		return nil
	}
	return s.enqueueRoleIntent(ctx, tx, identityroles.OpRevoke, ownerID,
		identityroles.RoleRestaurantOwner, reason)
}

// deliveryPartnerRoleForStatus maps a delivery-partner status transition to a
// role action. The bool reports whether any action is needed at all.
//
// Kept as one function so the admin review path and the free-form status path
// cannot disagree about what SUSPENDED means.
func deliveryPartnerRoleForStatus(status string) (identityroles.Op, bool) {
	switch status {
	case "REJECTED", "CLOSED":
		return identityroles.OpRevoke, true
	case "DRAFT", "PENDING_REVIEW", "APPROVED", "ACTIVE":
		// Re-granting is idempotent, and it is the safety net for a partner
		// whose create-time intent was dead-lettered during an identity
		// outage.
		return identityroles.OpGrant, true
	default:
		// OFFLINE, SUSPENDED — a pause, not a departure. No change.
		return "", false
	}
}

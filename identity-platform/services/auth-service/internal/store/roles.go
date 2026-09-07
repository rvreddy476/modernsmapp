package store

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/google/uuid"
)

// UserRole is a single role grant for a user.
type UserRole struct {
	UserID    uuid.UUID  `json:"user_id"`
	Role      string     `json:"role"`
	GrantedBy *uuid.UUID `json:"granted_by,omitempty"`
	GrantedAt time.Time  `json:"granted_at"`
}

// AdminAuditEntry is one row of the privileged-action audit trail.
//
// ActorID is a POINTER and ActorService exists because an actor is no longer
// always a person. When commerce-service approves a seller it calls the
// internal grant API, and there is no user acting — the service is. actor_id
// was `UUID NOT NULL`, so representing that honestly needed a schema change;
// the alternative was to invent a uuid for each service and write it into an
// audit trail as though a human had done the thing, which is exactly the sort
// of quiet fiction an audit trail exists to prevent.
//
// The change is the smallest one that stays truthful: actor_id drops NOT NULL
// and a nullable actor_service TEXT column is added. Exactly one of the two is
// set on every row. See the ALTERs in database/setup.sql.
type AdminAuditEntry struct {
	ID uuid.UUID `json:"id"`
	// ActorID is the acting user, or nil when a service acted.
	ActorID *uuid.UUID `json:"actor_id,omitempty"`
	// ActorService is the calling service's name (e.g. "commerce-service"),
	// or "" when a user acted.
	ActorService string     `json:"actor_service,omitempty"`
	Action       string     `json:"action"`
	TargetID     *uuid.UUID `json:"target_id,omitempty"`
	Detail       string     `json:"detail"`
	Allowed      bool       `json:"allowed"`
	CreatedAt    time.Time  `json:"created_at"`
}

// ValidRole reports whether r is an assignable role.
//
// The vocabulary itself lives in internal/roles, which is the single list the
// CHECK constraint, this function and config.ExpandRoles all derive from. This
// wrapper stays because it is the name the service layer already calls.
func ValidRole(r string) bool { return roles.Valid(r) }

// ValidEcosystemRole reports whether r is one of the four roles a SERVICE may
// grant over the internal API (seller, restaurant_owner, delivery_partner,
// rider_partner). A service must never be able to mint admin or superadmin.
func ValidEcosystemRole(r string) bool { return roles.IsEcosystem(r) }

// GrantRole grants role to a user. Idempotent: re-granting is a no-op.
// grantedBy may be uuid.Nil for env/system grants.
func (s *Store) GrantRole(ctx context.Context, userID, grantedBy uuid.UUID, role string) error {
	if !ValidRole(role) {
		return fmt.Errorf("invalid role: %s", role)
	}
	var gb *uuid.UUID
	if grantedBy != uuid.Nil {
		gb = &grantedBy
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.user_roles (user_id, role, granted_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, role) DO NOTHING
	`, userID, role, gb)
	if err != nil {
		return fmt.Errorf("grant role: %w", err)
	}
	return nil
}

// RevokeRole removes a role from a user. Removing an absent role is a no-op.
func (s *Store) RevokeRole(ctx context.Context, userID uuid.UUID, role string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM auth.user_roles WHERE user_id = $1 AND role = $2`, userID, role)
	if err != nil {
		return fmt.Errorf("revoke role: %w", err)
	}
	return nil
}

// RolesForUser returns the raw role strings granted to a user (DB only).
func (s *Store) RolesForUser(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT role FROM auth.user_roles WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("roles for user: %w", err)
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

// InsertAdminAudit appends an immutable privileged-action record. actor is the
// caller; target may be uuid.Nil. allowed=false records a denied attempt.
func (s *Store) InsertAdminAudit(ctx context.Context, actorID, targetID uuid.UUID, action, detail string, allowed bool) error {
	var target *uuid.UUID
	if targetID != uuid.Nil {
		target = &targetID
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.admin_audit (actor_id, action, target_id, detail, allowed)
		VALUES ($1, $2, $3, $4, $5)
	`, actorID, action, target, detail, allowed)
	if err != nil {
		return fmt.Errorf("insert admin audit: %w", err)
	}
	return nil
}

// InsertServiceAudit is InsertAdminAudit for an actor that is not a person.
//
// actor_id is left NULL and the calling service is named in actor_service, so
// a reader of the trail can tell "commerce-service granted this seller role"
// from "a superadmin granted it" without guessing from the detail string.
func (s *Store) InsertServiceAudit(ctx context.Context, targetID uuid.UUID, service, action, detail string, allowed bool) error {
	var target *uuid.UUID
	if targetID != uuid.Nil {
		target = &targetID
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.admin_audit (actor_id, actor_service, action, target_id, detail, allowed)
		VALUES (NULL, $1, $2, $3, $4, $5)
	`, service, action, target, detail, allowed)
	if err != nil {
		return fmt.Errorf("insert service audit: %w", err)
	}
	return nil
}

// ListAdminAudit returns the most recent privileged-action audit rows, newest
// first. limit is clamped by the caller.
func (s *Store) ListAdminAudit(ctx context.Context, limit int) ([]AdminAuditEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, actor_id, COALESCE(actor_service, ''), action, target_id,
		       COALESCE(detail, ''), allowed, created_at
		FROM auth.admin_audit ORDER BY created_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list admin audit: %w", err)
	}
	defer rows.Close()
	var out []AdminAuditEntry
	for rows.Next() {
		var e AdminAuditEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorService, &e.Action, &e.TargetID, &e.Detail, &e.Allowed, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan admin audit: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListUserRoles returns all role grants for a user with metadata (admin view).
func (s *Store) ListUserRoles(ctx context.Context, userID uuid.UUID) ([]UserRole, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, role, granted_by, granted_at
		FROM auth.user_roles WHERE user_id = $1 ORDER BY role
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user roles: %w", err)
	}
	defer rows.Close()
	var out []UserRole
	for rows.Next() {
		var ur UserRole
		if err := rows.Scan(&ur.UserID, &ur.Role, &ur.GrantedBy, &ur.GrantedAt); err != nil {
			return nil, fmt.Errorf("scan user role: %w", err)
		}
		out = append(out, ur)
	}
	return out, rows.Err()
}

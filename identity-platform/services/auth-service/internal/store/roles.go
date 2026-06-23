package store

import (
	"context"
	"fmt"
	"time"

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
type AdminAuditEntry struct {
	ID        uuid.UUID  `json:"id"`
	ActorID   uuid.UUID  `json:"actor_id"`
	Action    string     `json:"action"`
	TargetID  *uuid.UUID `json:"target_id,omitempty"`
	Detail    string     `json:"detail"`
	Allowed   bool       `json:"allowed"`
	CreatedAt time.Time  `json:"created_at"`
}

// ValidRole reports whether r is an assignable role.
func ValidRole(r string) bool {
	switch r {
	case "superadmin", "admin", "moderator":
		return true
	}
	return false
}

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

// ListAdminAudit returns the most recent privileged-action audit rows, newest
// first. limit is clamped by the caller.
func (s *Store) ListAdminAudit(ctx context.Context, limit int) ([]AdminAuditEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, actor_id, action, target_id, COALESCE(detail, ''), allowed, created_at
		FROM auth.admin_audit ORDER BY created_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list admin audit: %w", err)
	}
	defer rows.Close()
	var out []AdminAuditEntry
	for rows.Next() {
		var e AdminAuditEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.Action, &e.TargetID, &e.Detail, &e.Allowed, &e.CreatedAt); err != nil {
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

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

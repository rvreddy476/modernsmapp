package service

import (
	"context"
	"errors"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// ErrNotSuperadmin is returned when a non-superadmin attempts role management.
var ErrNotSuperadmin = errors.New("superadmin role required")

// resolveScopes computes the access-token `scopes` claim for a user by UNIONing
// the env allowlist roles (bootstrap) with the DB roles table, then expanding
// implications (superadmin⊇admin⊇moderator). On a DB error it falls back to the
// env-derived scopes rather than failing the login — the env allowlist is the
// safe minimum and an outage shouldn't lock admins out or block ordinary logins.
func (s *Service) resolveScopes(ctx context.Context, userID uuid.UUID) string {
	roles := s.cfg.EnvRolesForUser(userID.String())
	dbRoles, err := s.store.RolesForUser(ctx, userID)
	if err != nil {
		s.log.Warn("roles lookup failed; falling back to env scopes", "user_id", userID, "err", err)
		return config.ExpandRoles(roles)
	}
	roles = append(roles, dbRoles...)
	return config.ExpandRoles(roles)
}

// IsSuperadmin reports whether a user is a superadmin via env allowlist OR the
// DB roles table. Used to authorize role-management operations. Fail-closed: a
// DB error yields the env-only answer (never silently grants).
func (s *Service) IsSuperadmin(ctx context.Context, userID uuid.UUID) bool {
	for _, r := range s.cfg.EnvRolesForUser(userID.String()) {
		if r == "superadmin" {
			return true
		}
	}
	dbRoles, err := s.store.RolesForUser(ctx, userID)
	if err != nil {
		s.log.Warn("superadmin check: roles lookup failed", "user_id", userID, "err", err)
		return false
	}
	for _, r := range dbRoles {
		if r == "superadmin" {
			return true
		}
	}
	return false
}

// GrantRole grants a role to a target user. Only a superadmin (actor) may do so.
// The new scope takes effect on the target's next token mint (login/refresh) —
// existing tokens are not retroactively upgraded, which is standard for JWTs and
// honors the no-forced-logout rule.
func (s *Service) GrantRole(ctx context.Context, actorID, targetID uuid.UUID, role string) error {
	if !store.ValidRole(role) {
		return errors.New("invalid role")
	}
	if !s.IsSuperadmin(ctx, actorID) {
		return ErrNotSuperadmin
	}
	return s.store.GrantRole(ctx, targetID, actorID, role)
}

// RevokeRole removes a role from a target user. Superadmin (actor) only.
func (s *Service) RevokeRole(ctx context.Context, actorID, targetID uuid.UUID, role string) error {
	if !s.IsSuperadmin(ctx, actorID) {
		return ErrNotSuperadmin
	}
	return s.store.RevokeRole(ctx, targetID, role)
}

// ListUserRoles lists a target user's role grants. Superadmin (actor) only.
func (s *Service) ListUserRoles(ctx context.Context, actorID, targetID uuid.UUID) ([]store.UserRole, error) {
	if !s.IsSuperadmin(ctx, actorID) {
		return nil, ErrNotSuperadmin
	}
	return s.store.ListUserRoles(ctx, targetID)
}

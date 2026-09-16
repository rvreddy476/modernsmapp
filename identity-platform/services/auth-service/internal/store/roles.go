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
	// App is the application the role is scoped to; nil is platform-wide.
	App       *string    `json:"app"`
	ExpiresAt *time.Time `json:"expires_at"`
	Reason    *string    `json:"reason"`
	// Active is false once ExpiresAt has passed. Expired rows are listed so
	// an admin can see and renew them, but grant nothing.
	Active bool `json:"active"`
}

// RoleGrant is an active role row as the permission resolver needs it. App is
// "" for a platform-wide grant.
type RoleGrant struct {
	Role      string
	App       string
	ExpiresAt *time.Time
}

// SuperadminHolder is one active platform-wide superadmin row, read under a
// row lock inside ChangeRole.
type SuperadminHolder struct {
	UserID    uuid.UUID
	ExpiresAt *time.Time
}

// RoleChange is one admin grant or revoke. App "" is platform-wide.
type RoleChange struct {
	Revoke    bool
	UserID    uuid.UUID
	GrantedBy uuid.UUID
	Role      string
	App       string
	ExpiresAt *time.Time
	Reason    string
}

// RoleAudit is the audit row written in the same transaction as a RoleChange.
type RoleAudit struct {
	ActorID  uuid.UUID
	TargetID uuid.UUID
	Action   string
	Detail   string
}

// activeRole is the SQL predicate for "this row grants something now".
const activeRole = `(expires_at IS NULL OR expires_at > NOW())`

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
	// Platform-wide row (app NULL). The conflict target is the unique
	// expression index uq_user_roles_user_role_app.
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.user_roles (user_id, role, granted_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, role, (COALESCE(app, ''))) DO NOTHING
	`, userID, role, gb)
	if err != nil {
		return fmt.Errorf("grant role: %w", err)
	}
	return nil
}

// RevokeRole removes a platform-wide role from a user. Removing an absent role
// is a no-op. App-scoped rows are untouched (see ChangeRole).
func (s *Store) RevokeRole(ctx context.Context, userID uuid.UUID, role string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM auth.user_roles WHERE user_id = $1 AND role = $2 AND app IS NULL`, userID, role)
	if err != nil {
		return fmt.Errorf("revoke role: %w", err)
	}
	return nil
}

// RoleGrantsForUser returns every ACTIVE role row of a user, platform-wide and
// app-scoped. Expired rows are filtered here and again by permissions.Resolve.
func (s *Store) RoleGrantsForUser(ctx context.Context, userID uuid.UUID) ([]RoleGrant, error) {
	rows, err := s.db.Query(ctx, `
		SELECT role, COALESCE(app, ''), expires_at
		FROM auth.user_roles WHERE user_id = $1 AND `+activeRole, userID)
	if err != nil {
		return nil, fmt.Errorf("role grants for user: %w", err)
	}
	defer rows.Close()
	var out []RoleGrant
	for rows.Next() {
		var g RoleGrant
		if err := rows.Scan(&g.Role, &g.App, &g.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan role grant: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ChangeRole applies one admin grant or revoke and writes its audit row in the
// SAME transaction: if the audit insert fails, the change is rolled back, so
// no role change can exist without its record.
//
// When the change touches the superadmin role, every active platform-wide
// superadmin row is read FOR UPDATE and handed to guard before anything is
// written. guard is where "never remove the last superadmin" lives; the lock
// makes two concurrent revokes see each other's result instead of both
// passing. guard may be nil for other roles.
//
// changed reports whether a row was inserted, updated or deleted.
func (s *Store) ChangeRole(ctx context.Context, ch RoleChange, audit RoleAudit,
	guard func(holders []SuperadminHolder) error) (changed bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("change role: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if guard != nil {
		rows, err := tx.Query(ctx, `
			SELECT user_id, expires_at FROM auth.user_roles
			WHERE role = 'superadmin' AND app IS NULL AND `+activeRole+`
			FOR UPDATE`)
		if err != nil {
			return false, fmt.Errorf("change role: lock superadmins: %w", err)
		}
		var holders []SuperadminHolder
		for rows.Next() {
			var h SuperadminHolder
			if err := rows.Scan(&h.UserID, &h.ExpiresAt); err != nil {
				rows.Close()
				return false, fmt.Errorf("change role: scan superadmin: %w", err)
			}
			holders = append(holders, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, fmt.Errorf("change role: read superadmins: %w", err)
		}
		if err := guard(holders); err != nil {
			return false, err
		}
	}

	var app *string
	if ch.App != "" {
		app = &ch.App
	}
	if ch.Revoke {
		tag, err := tx.Exec(ctx, `
			DELETE FROM auth.user_roles
			WHERE user_id = $1 AND role = $2 AND COALESCE(app, '') = $3`,
			ch.UserID, ch.Role, ch.App)
		if err != nil {
			return false, fmt.Errorf("change role: revoke: %w", err)
		}
		changed = tag.RowsAffected() > 0
	} else {
		var gb *uuid.UUID
		if ch.GrantedBy != uuid.Nil {
			gb = &ch.GrantedBy
		}
		// Re-granting renews: expiry, reason and grantor are replaced.
		tag, err := tx.Exec(ctx, `
			INSERT INTO auth.user_roles (user_id, role, app, granted_by, expires_at, reason)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (user_id, role, (COALESCE(app, ''))) DO UPDATE
			SET granted_by = EXCLUDED.granted_by, granted_at = NOW(),
			    expires_at = EXCLUDED.expires_at, reason = EXCLUDED.reason`,
			ch.UserID, ch.Role, app, gb, ch.ExpiresAt, ch.Reason)
		if err != nil {
			return false, fmt.Errorf("change role: grant: %w", err)
		}
		changed = tag.RowsAffected() > 0
	}

	var actor, target *uuid.UUID
	if audit.ActorID != uuid.Nil {
		actor = &audit.ActorID
	}
	if audit.TargetID != uuid.Nil {
		target = &audit.TargetID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth.admin_audit (actor_id, action, target_id, detail, allowed)
		VALUES ($1, $2, $3, $4, TRUE)`, actor, audit.Action, target, audit.Detail); err != nil {
		return false, fmt.Errorf("change role: audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("change role: commit: %w", err)
	}
	return changed, nil
}

// RecordRoleBootstrap writes the one audit row that records an env allowlist
// holder. Idempotent: the partial unique index uq_admin_audit_role_bootstrap
// makes a second boot (or a concurrent replica) a no-op. inserted reports
// whether this call wrote the row.
func (s *Store) RecordRoleBootstrap(ctx context.Context, userID uuid.UUID, detail string) (inserted bool, err error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO auth.admin_audit (actor_service, action, target_id, detail, allowed)
		VALUES ('auth-service', 'role.bootstrap', $1, $2, TRUE)
		ON CONFLICT (target_id, detail) WHERE action = 'role.bootstrap' DO NOTHING`,
		userID, detail)
	if err != nil {
		return false, fmt.Errorf("record role bootstrap: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// RolesForUser returns the raw platform-wide role strings a user holds now
// (DB only). App-scoped and expired rows are excluded: this feeds the token's
// `scopes` claim and the superadmin check, where a "dating moderator" must
// never read as a platform moderator.
func (s *Store) RolesForUser(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT role FROM auth.user_roles WHERE user_id = $1 AND app IS NULL AND `+activeRole, userID)
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
		SELECT user_id, role, granted_by, granted_at, app, expires_at, reason,
		       `+activeRole+`
		FROM auth.user_roles WHERE user_id = $1 ORDER BY role, COALESCE(app, '')
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user roles: %w", err)
	}
	defer rows.Close()
	var out []UserRole
	for rows.Next() {
		var ur UserRole
		if err := rows.Scan(&ur.UserID, &ur.Role, &ur.GrantedBy, &ur.GrantedAt,
			&ur.App, &ur.ExpiresAt, &ur.Reason, &ur.Active); err != nil {
			return nil, fmt.Errorf("scan user role: %w", err)
		}
		out = append(out, ur)
	}
	return out, rows.Err()
}

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/google/uuid"
)

// Reads for the admin console's Access page (token-only
// /v1/auth/internal/admin/* family; see internal/http/admin_console.go).

// RoleHolder is one role row with what the Access page shows next to it.
type RoleHolder struct {
	UserID    uuid.UUID  `json:"user_id"`
	Role      string     `json:"role"`
	App       *string    `json:"app"`
	ExpiresAt *time.Time `json:"expires_at"`
	Reason    *string    `json:"reason"`
	GrantedBy *uuid.UUID `json:"granted_by,omitempty"`
	GrantedAt time.Time  `json:"granted_at"`
	// MFAEnrolled: the holder has TOTP enrolled (auth.users.two_factor_enabled).
	MFAEnrolled bool `json:"mfa_enrolled"`
	// Active is false once ExpiresAt has passed; expired rows are still listed
	// so they can be renewed or cleaned up.
	Active bool `json:"active"`
}

// AppFilter selects rows by application scope.
type AppFilter int

const (
	// AppAny lists platform-wide and app-scoped rows alike.
	AppAny AppFilter = iota
	// AppExact lists rows scoped to RoleHolderFilter.App.
	AppExact
	// AppPlatformWide lists rows with app NULL only.
	AppPlatformWide
)

// RoleHolderFilter narrows ListRoleHolders. Role "" lists the ADMIN roles
// (superadmin … auditor) and leaves the ecosystem roles out: the Access page
// manages admins, and sellers number in the thousands. Name an ecosystem role
// explicitly to page through it.
type RoleHolderFilter struct {
	Role   string
	Scope  AppFilter
	App    string
	Limit  int
	Offset int
}

// ListRoleHolders pages role rows newest grant first, joined with the
// holder's TOTP enrolment. Limit is the caller's clamp.
func (s *Store) ListRoleHolders(ctx context.Context, f RoleHolderFilter) ([]RoleHolder, error) {
	roleSet := roles.AdminRoles()
	if f.Role != "" {
		roleSet = []string{f.Role}
	}
	rows, err := s.db.Query(ctx, `
		SELECT r.user_id, r.role, r.app, r.expires_at, r.reason, r.granted_by, r.granted_at,
		       COALESCE(u.two_factor_enabled, FALSE), `+activeRoleAlias("r.")+`
		FROM auth.user_roles r
		LEFT JOIN auth.users u ON u.user_id = r.user_id
		WHERE r.role = ANY($1)
		  AND ($2 = 0 OR ($2 = 1 AND r.app = $3) OR ($2 = 2 AND r.app IS NULL))
		ORDER BY r.granted_at DESC, r.user_id, r.role, COALESCE(r.app, '')
		LIMIT $4 OFFSET $5`,
		roleSet, int(f.Scope), f.App, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list role holders: %w", err)
	}
	defer rows.Close()
	out := []RoleHolder{}
	for rows.Next() {
		var h RoleHolder
		if err := rows.Scan(&h.UserID, &h.Role, &h.App, &h.ExpiresAt, &h.Reason, &h.GrantedBy, &h.GrantedAt,
			&h.MFAEnrolled, &h.Active); err != nil {
			return nil, fmt.Errorf("scan role holder: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// activeRole with a table alias, for joined queries.
func activeRoleAlias(alias string) string {
	return `(` + alias + `expires_at IS NULL OR ` + alias + `expires_at > NOW())`
}

// AuditFilter narrows ListAdminAuditFiltered. Nil means "any". To is
// exclusive so a day range is [from, to).
type AuditFilter struct {
	Actor  *uuid.UUID
	Target *uuid.UUID
	Action string
	From   *time.Time
	To     *time.Time
	Limit  int
	Offset int
}

// ListAdminAuditFiltered pages the privileged-action trail newest first.
func (s *Store) ListAdminAuditFiltered(ctx context.Context, f AuditFilter) ([]AdminAuditEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, actor_id, COALESCE(actor_service, ''), action, target_id,
		       COALESCE(detail, ''), allowed, created_at
		FROM auth.admin_audit
		WHERE ($1::uuid IS NULL OR actor_id = $1)
		  AND ($2::uuid IS NULL OR target_id = $2)
		  AND ($3 = '' OR action = $3)
		  AND ($4::timestamptz IS NULL OR created_at >= $4)
		  AND ($5::timestamptz IS NULL OR created_at < $5)
		ORDER BY created_at DESC, id
		LIMIT $6 OFFSET $7`,
		f.Actor, f.Target, f.Action, f.From, f.To, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list admin audit: %w", err)
	}
	defer rows.Close()
	out := []AdminAuditEntry{}
	for rows.Next() {
		var e AdminAuditEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorService, &e.Action, &e.TargetID, &e.Detail, &e.Allowed, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan admin audit: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UserSearchHit is one account matched by SearchUsers: the id, the RAW email
// (the service masks it before it leaves) and the profile handle. Nothing
// else is read — no phone, name, date of birth or status.
type UserSearchHit struct {
	UserID uuid.UUID
	Email  string
	Handle string
}

// SearchUsers finds accounts by exact user id, or by an email or handle
// PREFIX (case-insensitive, a leading "@" on the query is ignored for the
// handle). The handle comes from profile.profiles, which auth-service's own
// setup.sql creates in this database (store/requirements.go checks it), so
// the join cannot hit a missing table. Purged accounts have no email and no
// profile row and do not match.
func (s *Store) SearchUsers(ctx context.Context, q string, limit int) ([]UserSearchHit, error) {
	q = strings.TrimSpace(q)
	var (
		rowsQ string
		args  []any
	)
	if id, err := uuid.Parse(q); err == nil {
		rowsQ = `
			SELECT u.user_id, COALESCE(u.email, ''), COALESCE(p.username, '')
			FROM auth.users u LEFT JOIN profile.profiles p ON p.user_id = u.user_id
			WHERE u.user_id = $1 LIMIT $2`
		args = []any{id, limit}
	} else {
		prefix := escapeLike(q) + "%"
		handle := escapeLike(strings.TrimPrefix(q, "@")) + "%"
		rowsQ = `
			SELECT u.user_id, COALESCE(u.email, ''), COALESCE(p.username, '')
			FROM auth.users u LEFT JOIN profile.profiles p ON p.user_id = u.user_id
			WHERE u.email ILIKE $1 ESCAPE '\' OR p.username ILIKE $2 ESCAPE '\'
			ORDER BY u.email, u.user_id LIMIT $3`
		args = []any{prefix, handle, limit}
	}
	rows, err := s.db.Query(ctx, rowsQ, args...)
	if err != nil {
		return nil, fmt.Errorf("search users: %w", err)
	}
	defer rows.Close()
	out := []UserSearchHit{}
	for rows.Next() {
		var h UserSearchHit
		if err := rows.Scan(&h.UserID, &h.Email, &h.Handle); err != nil {
			return nil, fmt.Errorf("scan user search: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// escapeLike neutralises LIKE metacharacters in user input.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

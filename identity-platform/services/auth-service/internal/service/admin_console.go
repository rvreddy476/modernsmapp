package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// Admin console Access page: role management on behalf of a human admin,
// arriving from admin-service with a signed token (admin console Wave 1, B4).
//
// HOW THIS DIFFERS FROM GrantRole / RevokeRole / ForceLogout
//
// Those take the ACTOR'S OWN SESSION as evidence: authorizePrivileged wants a
// superadmin whose session is an admin MFA session with a step-up younger
// than five minutes. The console never calls identity with the admin's
// session; admin-service does, after it has itself verified the admin's
// permission for the operation, the admin MFA session and the fresh
// step-up (admin-service internal/http, adminauth). It then signs a
// per-call token whose `act` claim names the admin. So this path:
//
//   - does NOT re-check the session — there is none on the request, and a
//     check here would only ever fail;
//   - takes the actor from the SIGNED act claim, never from a header;
//   - applies the same rules as the session path (catalogue validation,
//     self-grant, env bootstrap holders, last superadmin, audit in the
//     same transaction) through the shared applyRoleChange;
//   - additionally refuses to touch the superadmin role unless act is a
//     superadmin NOW (identity checks that live, not admin-service's word
//     for it) — see ErrSuperadminChangeRequiresSuperadmin;
//   - records in every audit row that the action came through
//     admin-service, with the token's jti, so a row from this path is
//     never mistaken for one written under the admin's own session.

// ConsoleActor is the human admin admin-service acts for: the verified
// `act` claim and the token's jti.
type ConsoleActor struct {
	UserID uuid.UUID
	JTI    string
}

// ConsoleVia is the marker every audit row from this path carries.
const ConsoleVia = "via=admin-service"

// detailPrefix is prepended to the audit detail of every action on this path.
func (a ConsoleActor) detailPrefix() string {
	return ConsoleVia + " jti=" + a.JTI + " "
}

// ErrSuperadminChangeRequiresSuperadmin: granting or revoking the superadmin
// role through admin-service needs an actor who is a superadmin now. This is
// the bootstrap safety of the token path: platform:roles.manage is held only
// by superadmins in the catalogue, but identity does not take that on trust —
// a misconfigured or compromised admin-service must not be able to mint the
// first superadmin. HTTP 403 SUPERADMIN_REQUIRED.
var ErrSuperadminChangeRequiresSuperadmin = errors.New("only a superadmin can grant or revoke the superadmin role")

// ErrSearchQueryTooShort: a user search needs at least two characters (or a
// user id). HTTP 400.
var ErrSearchQueryTooShort = errors.New("search query must be at least 2 characters or a user id")

// ConsoleGrantRole grants a role for actor through admin-service. See the
// package comment above for the rules.
func (s *Service) ConsoleGrantRole(ctx context.Context, actor ConsoleActor, req RoleChangeRequest) error {
	return s.consoleChangeRole(ctx, actor, req, false)
}

// ConsoleRevokeRole revokes one grant (role + app) for actor through
// admin-service. Env allowlist holders stay unrevocable here (409
// ENV_BOOTSTRAP_ROLE, as on the session path).
func (s *Service) ConsoleRevokeRole(ctx context.Context, actor ConsoleActor, req RoleChangeRequest) error {
	return s.consoleChangeRole(ctx, actor, req, true)
}

func (s *Service) consoleChangeRole(ctx context.Context, actor ConsoleActor, req RoleChangeRequest, revoke bool) error {
	action := "role.grant"
	if revoke {
		action = "role.revoke"
	}
	if err := validateRoleChange(&req, revoke, time.Now()); err != nil {
		return err
	}
	if actor.UserID == uuid.Nil {
		// The HTTP layer refuses a token without a valid act before it gets
		// here; this is the service-level backstop.
		return ErrNotSuperadmin
	}
	// MFA and step-up: enforced by admin-service before it signed the token.
	// Deliberately not re-checked — see the file comment.
	prefix := actor.detailPrefix()
	if !revoke && actor.UserID == req.TargetID {
		s.audit(ctx, actor.UserID, req.TargetID, action, prefix+"denied: self-grant, "+roleChangeDetail(req), false)
		return ErrSelfGrant
	}
	if req.Role == roles.Superadmin && !s.IsSuperadmin(ctx, actor.UserID) {
		s.audit(ctx, actor.UserID, req.TargetID, action, prefix+"denied: superadmin change by a non-superadmin, "+roleChangeDetail(req), false)
		return ErrSuperadminChangeRequiresSuperadmin
	}
	return s.applyRoleChange(ctx, actor.UserID, req, revoke, action, prefix)
}

// ConsoleForceLogout revokes every live session of target for actor through
// admin-service, audited in the same transaction (action
// session.force_logout) and marked sess_revoked:<sid> for the gateway.
func (s *Service) ConsoleForceLogout(ctx context.Context, actor ConsoleActor, targetID uuid.UUID, reason string) (int, error) {
	const action = "session.force_logout"
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return 0, ErrReasonRequired
	}
	if len(reason) > maxReasonLen {
		reason = reason[:maxReasonLen]
	}
	if actor.UserID == uuid.Nil {
		return 0, ErrNotSuperadmin
	}
	ids, err := s.store.RevokeAllSessionsAudited(ctx, targetID, store.RoleAudit{
		ActorID: actor.UserID, TargetID: targetID, Action: action,
		Detail: actor.detailPrefix() + "reason=" + reason,
	})
	if err != nil {
		return 0, err
	}
	s.revokeCached(ctx, ids)
	s.InvalidatePending2FASessions(ctx, targetID)
	return len(ids), nil
}

// EnvRoleHolder is a role held through an environment allowlist rather than
// a row: no grantor, no expiry, no reason, and unrevocable through the API.
type EnvRoleHolder struct {
	UserID      uuid.UUID `json:"user_id"`
	Role        string    `json:"role"`
	Source      string    `json:"source"`
	MFAEnrolled bool      `json:"mfa_enrolled"`
}

// ConsoleRoleHolders is one page of role holders plus the env allowlist
// holders that match the filter (shown on the first page only, since they
// are not rows and cannot be paged).
type ConsoleRoleHolders struct {
	Holders    []store.RoleHolder `json:"holders"`
	EnvHolders []EnvRoleHolder    `json:"env_holders"`
}

// maxConsolePage clamps every paged console read.
const maxConsolePage = 200

func clampPage(limit, offset int) (int, int) {
	if limit <= 0 || limit > maxConsolePage {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// ConsoleListRoleHolders lists who holds which role. Read-only; the
// permission was checked by admin-service and named in the token.
func (s *Service) ConsoleListRoleHolders(ctx context.Context, f store.RoleHolderFilter) (ConsoleRoleHolders, error) {
	f.Limit, f.Offset = clampPage(f.Limit, f.Offset)
	if f.Role != "" && !roles.Valid(f.Role) {
		return ConsoleRoleHolders{}, ErrInvalidRole
	}
	rows, err := s.store.ListRoleHolders(ctx, f)
	if err != nil {
		return ConsoleRoleHolders{}, err
	}
	out := ConsoleRoleHolders{Holders: rows, EnvHolders: []EnvRoleHolder{}}
	if f.Offset == 0 && f.Scope != store.AppExact {
		out.EnvHolders = s.envRoleHolders(ctx, f.Role)
	}
	return out, nil
}

// envRoleHolders lists the env allowlist holders whose RAW role matches
// role ("" = all three), sorted by env var then id.
func (s *Service) envRoleHolders(ctx context.Context, role string) []EnvRoleHolder {
	sources := []struct {
		set    map[string]struct{}
		role   string
		envVar string
	}{
		{s.cfg.ScopeSuperadminUserIDs, roles.Superadmin, "SUPERADMIN_USER_IDS"},
		{s.cfg.ScopeAdminUserIDs, roles.Admin, "ADMIN_USER_IDS"},
		{s.cfg.ScopeModeratorUserIDs, roles.Moderator, "MODERATOR_USER_IDS"},
	}
	out := []EnvRoleHolder{}
	for _, src := range sources {
		if role != "" && role != src.role {
			continue
		}
		for _, id := range sortedIDs(src.set) {
			h := EnvRoleHolder{UserID: id, Role: src.role, Source: "env:" + src.envVar}
			if u, err := s.store.GetUserByID(ctx, id); err == nil && u != nil {
				h.MFAEnrolled = u.TwoFactorEnabled
			}
			out = append(out, h)
		}
	}
	return out
}

func sortedIDs(set map[string]struct{}) []uuid.UUID {
	var out []uuid.UUID
	for id := range set {
		if u, err := uuid.Parse(id); err == nil {
			out = append(out, u)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].String() < out[j-1].String(); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ConsoleListUserRoles lists a target's role grants for the console.
func (s *Service) ConsoleListUserRoles(ctx context.Context, targetID uuid.UUID) ([]store.UserRole, error) {
	rows, err := s.store.ListUserRoles(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []store.UserRole{}
	}
	return rows, nil
}

// ConsoleListAudit pages the privileged-action audit trail for the console.
func (s *Service) ConsoleListAudit(ctx context.Context, f store.AuditFilter) ([]store.AdminAuditEntry, error) {
	f.Limit, f.Offset = clampPage(f.Limit, f.Offset)
	return s.store.ListAdminAuditFiltered(ctx, f)
}

// UserSearchResult is what the console may see of an account it is about to
// grant a role to: the id, the email masked to its first character and
// domain, and the profile handle. Nothing else.
type UserSearchResult struct {
	UserID      string `json:"user_id"`
	EmailMasked string `json:"email_masked"`
	Handle      string `json:"handle"`
}

// maxSearchResults bounds one search page.
const maxSearchResults = 20

// ConsoleSearchUsers finds accounts by id, email prefix or handle prefix.
func (s *Service) ConsoleSearchUsers(ctx context.Context, q string, limit int) ([]UserSearchResult, error) {
	q = strings.TrimSpace(q)
	if _, err := uuid.Parse(q); err != nil && len([]rune(strings.TrimPrefix(q, "@"))) < 2 {
		return nil, ErrSearchQueryTooShort
	}
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	hits, err := s.store.SearchUsers(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	out := make([]UserSearchResult, 0, len(hits))
	for _, h := range hits {
		out = append(out, UserSearchResult{UserID: h.UserID.String(), EmailMasked: MaskEmail(h.Email), Handle: h.Handle})
	}
	return out, nil
}

// MaskEmail keeps the first character of the local part and the domain:
// "raghu@example.com" → "r***@example.com". Anything without an "@" is
// masked entirely; "" stays "".
func MaskEmail(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return ""
	}
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	local := []rune(email[:at])
	return string(local[0]) + "***" + email[at:]
}

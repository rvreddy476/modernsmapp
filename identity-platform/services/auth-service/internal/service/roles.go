package service

import (
	"context"
	"errors"
	"strings"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// ErrNotSuperadmin is returned when a non-superadmin attempts role management.
var ErrNotSuperadmin = errors.New("superadmin role required")

// ErrMFARequired is returned when REQUIRE_MFA_FOR_PRIVILEGED is on and the
// acting user has not enrolled 2FA.
var ErrMFARequired = errors.New("MFA must be enabled for privileged actions")

// authorizePrivileged enforces the gate for role-management actions: the actor
// must be a superadmin and (when REQUIRE_MFA_FOR_PRIVILEGED is set) must have
// 2FA enabled. Denied attempts are audit-logged. action/target are recorded.
func (s *Service) authorizePrivileged(ctx context.Context, actorID, targetID uuid.UUID, action string) error {
	if !s.IsSuperadmin(ctx, actorID) {
		s.audit(ctx, actorID, targetID, action, "denied: not superadmin", false)
		return ErrNotSuperadmin
	}
	if s.cfg.RequireMFAForPrivileged {
		actor, err := s.store.GetUserByID(ctx, actorID)
		if err != nil || actor == nil || !actor.TwoFactorEnabled {
			s.audit(ctx, actorID, targetID, action, "denied: MFA not enabled", false)
			return ErrMFARequired
		}
	}
	return nil
}

// audit writes a best-effort privileged-action record. A logging failure must
// not fail the underlying action, but it is surfaced in the service log.
func (s *Service) audit(ctx context.Context, actorID, targetID uuid.UUID, action, detail string, allowed bool) {
	if err := s.store.InsertAdminAudit(ctx, actorID, targetID, action, detail, allowed); err != nil {
		s.log.Warn("admin audit write failed", "action", action, "actor", actorID, "err", err)
	}
}

// ResolveRoles returns a user's effective roles: the env allowlist (bootstrap)
// UNION the DB roles table, implication-expanded and in canonical order.
//
// This is the ONE read path. resolveScopes joins its output into the token's
// `scopes` claim; /v1/auth/me and /v1/auth/me/capabilities render it directly.
// A caller must never re-derive roles from another service's tables.
//
// It returns no error deliberately. On a DB failure it degrades to the
// env-derived roles and logs, matching the mint path: an outage must not lock
// admins out or block ordinary logins, and the env allowlist is the safe
// minimum. A caller that needs to distinguish "no roles" from "lookup broken"
// does not exist yet; if one appears, add a variant rather than changing this.
func (s *Service) ResolveRoles(ctx context.Context, userID uuid.UUID) []string {
	envRoles := s.cfg.EnvRolesForUser(userID.String())
	dbRoles, err := s.store.RolesForUser(ctx, userID)
	if err != nil {
		s.log.Warn("roles lookup failed; falling back to env roles", "user_id", userID, "err", err)
		return roles.Expand(envRoles)
	}
	return roles.Expand(append(envRoles, dbRoles...))
}

// resolveScopes computes the access-token `scopes` claim for a user.
//
// Space-separated, stable order. Since identity became the ecosystem's single
// role authority this carries the four ecosystem roles too, so a seller's token
// says `seller` and commerce no longer has to look the answer up itself.
//
// NOTE FOR CALLERS OF THE GRANT API: scopes are stamped at MINT time. A role
// granted now does not appear in a token already issued. It appears on the
// next mint — which includes POST /v1/auth/refresh, because RefreshSession
// calls generateAccessToken, which calls this. No re-login is required.
func (s *Service) resolveScopes(ctx context.Context, userID uuid.UUID) string {
	return config.ExpandRoles(s.ResolveRoles(ctx, userID))
}

// Capabilities is the shape a client renders a role switcher from.
//
// Deliberately answers "which hats does this person wear" in ONE request: the
// resolved role list, an exhaustive true/false map so a client never has to
// know which roles exist, and a ready-to-render switcher.
//
// Prior art worth naming: food-service's GET /v1/food/me/capabilities returns
// a similar shape but DERIVES it from food's own partner tables — the design
// this work replaces. This one reads identity's roles and nothing else.
type Capabilities struct {
	UserID string `json:"user_id"`
	// Roles is the effective, implication-expanded role list.
	Roles []string `json:"roles"`
	// IsCustomer is always true. Every account is a customer; there is no
	// customer role and there must never be one. It is returned as a constant
	// so a client can render the shopping tab without special-casing.
	IsCustomer bool `json:"is_customer"`
	// Capabilities names EVERY known role with an explicit boolean, so adding
	// a role to the vocabulary surfaces it to clients without a shape change
	// and a client can render "not a seller" rather than "unknown".
	Capabilities map[string]bool `json:"capabilities"`
	// Switcher is what the role switcher lists: the customer hat, then each
	// role actually held, in canonical order, with display wording.
	Switcher []CapabilitySwitch `json:"switcher"`
}

// CapabilitySwitch is one row of the role switcher.
type CapabilitySwitch struct {
	// Role is the vocabulary token, or "customer" for the always-present row.
	Role  string `json:"role"`
	Label string `json:"label"`
}

// CustomerSwitchRole is the pseudo-role the switcher uses for the default hat.
// It is NOT a grantable role and never appears in auth.user_roles — it exists
// only so the switcher's rows have a uniform shape.
const CustomerSwitchRole = "customer"

// CapabilitiesForUser builds the switcher payload for a user.
func (s *Service) CapabilitiesForUser(ctx context.Context, userID uuid.UUID) Capabilities {
	held := s.ResolveRoles(ctx, userID)
	heldSet := make(map[string]bool, len(held))
	for _, r := range held {
		heldSet[r] = true
	}

	caps := make(map[string]bool, len(roles.All()))
	for _, r := range roles.All() {
		caps[r] = heldSet[r]
	}

	switcher := []CapabilitySwitch{{Role: CustomerSwitchRole, Label: "Customer"}}
	for _, r := range held {
		switcher = append(switcher, CapabilitySwitch{Role: r, Label: roles.Label(r)})
	}

	return Capabilities{
		UserID:       userID.String(),
		Roles:        held,
		IsCustomer:   true,
		Capabilities: caps,
		Switcher:     switcher,
	}
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
	if err := s.authorizePrivileged(ctx, actorID, targetID, "role.grant"); err != nil {
		return err
	}
	if err := s.store.GrantRole(ctx, targetID, actorID, role); err != nil {
		return err
	}
	s.audit(ctx, actorID, targetID, "role.grant", "role="+role, true)
	return nil
}

// RevokeRole removes a role from a target user. Superadmin (actor) only.
func (s *Service) RevokeRole(ctx context.Context, actorID, targetID uuid.UUID, role string) error {
	if err := s.authorizePrivileged(ctx, actorID, targetID, "role.revoke"); err != nil {
		return err
	}
	if err := s.store.RevokeRole(ctx, targetID, role); err != nil {
		return err
	}
	s.audit(ctx, actorID, targetID, "role.revoke", "role="+role, true)
	return nil
}

// ErrRoleNotGrantableByService is returned when the internal (service-to-
// service) role API is asked for a role outside the ecosystem set.
//
// THIS IS THE ENTIRE SECURITY BOUNDARY OF THAT ENDPOINT. The internal API is
// authenticated by a single shared key that every service in the cluster
// holds; if it could grant `admin`, then any one of them — or anything that
// ever obtains that key — could mint platform authority for an arbitrary
// account. Ecosystem roles are the blast radius by design: the worst a
// compromised commerce-service can do is make someone a seller.
var ErrRoleNotGrantableByService = errors.New(
	"a service may grant only ecosystem roles (seller, restaurant_owner, " +
		"delivery_partner, rider_partner); admin and superadmin are granted by a " +
		"superadmin through POST /v1/auth/admin/roles")

// ErrCallingServiceRequired is returned when the internal role API is called
// without naming the calling service. The name is what the audit trail records
// as the actor, so a blank one would produce an unattributable row.
var ErrCallingServiceRequired = errors.New("calling service name is required")

// GrantEcosystemRole grants one of the four ecosystem roles on behalf of a
// SERVICE — commerce approving a seller, food onboarding a restaurant or
// delivery partner, rider approving a fleet partner.
//
// There is no acting user, so this does NOT go through authorizePrivileged:
// the caller is authenticated at the HTTP layer by the shared internal-service
// key, and authorised here by the role it is asking for. callingService names
// the actor in the audit trail.
//
// Idempotent. auth.user_roles is keyed on (user_id, role) and the INSERT is
// ON CONFLICT DO NOTHING, so re-granting is a success, not an error — an
// approval flow that retries, or a backfill run twice, must not fail.
func (s *Service) GrantEcosystemRole(ctx context.Context, callingService string, targetID uuid.UUID, role, reason string) error {
	callingService = strings.TrimSpace(callingService)
	if callingService == "" {
		return ErrCallingServiceRequired
	}
	if !store.ValidEcosystemRole(role) {
		// Audited as a DENIED attempt. A service asking for `superadmin` is
		// either a bug or an attack, and either way it must leave a record.
		s.serviceAudit(ctx, targetID, callingService, "role.grant", "denied: role not grantable by a service, role="+role, false)
		return ErrRoleNotGrantableByService
	}
	// grantedBy is uuid.Nil — no user granted this. The store writes NULL.
	if err := s.store.GrantRole(ctx, targetID, uuid.Nil, role); err != nil {
		return err
	}
	s.serviceAudit(ctx, targetID, callingService, "role.grant", roleDetail(role, reason), true)
	return nil
}

// RevokeEcosystemRole is the matching revoke — a seller suspended, a partner
// removed. Same constraints as the grant: ecosystem roles only, so a service
// can neither mint nor strip platform authority.
//
// Idempotent in the same way: removing a role the user does not hold is a
// success. A suspension flow that retries must not fail.
func (s *Service) RevokeEcosystemRole(ctx context.Context, callingService string, targetID uuid.UUID, role, reason string) error {
	callingService = strings.TrimSpace(callingService)
	if callingService == "" {
		return ErrCallingServiceRequired
	}
	if !store.ValidEcosystemRole(role) {
		s.serviceAudit(ctx, targetID, callingService, "role.revoke", "denied: role not revocable by a service, role="+role, false)
		return ErrRoleNotGrantableByService
	}
	if err := s.store.RevokeRole(ctx, targetID, role); err != nil {
		return err
	}
	s.serviceAudit(ctx, targetID, callingService, "role.revoke", roleDetail(role, reason), true)
	return nil
}

// roleDetail renders the audit detail for a service-driven role change. The
// caller's free-text reason ("seller application SA-1042 approved", "suspended
// for policy violation") is what makes the trail answer WHY months later, so
// it is recorded rather than only logged.
func roleDetail(role, reason string) string {
	detail := "role=" + role
	if r := strings.TrimSpace(reason); r != "" {
		detail += " reason=" + r
	}
	return detail
}

// serviceAudit is audit() for a non-human actor. Best-effort, same as audit():
// a logging failure must not fail the underlying action.
func (s *Service) serviceAudit(ctx context.Context, targetID uuid.UUID, callingService, action, detail string, allowed bool) {
	if err := s.store.InsertServiceAudit(ctx, targetID, callingService, action, detail, allowed); err != nil {
		s.log.Warn("service audit write failed", "action", action, "service", callingService, "err", err)
	}
}

// ListUserRoles lists a target user's role grants. Superadmin (actor) only.
// Read-only, so it is not gated on MFA (no state change to audit).
func (s *Service) ListUserRoles(ctx context.Context, actorID, targetID uuid.UUID) ([]store.UserRole, error) {
	if !s.IsSuperadmin(ctx, actorID) {
		return nil, ErrNotSuperadmin
	}
	return s.store.ListUserRoles(ctx, targetID)
}

// ListAdminAudit returns the recent privileged-action audit trail. Superadmin
// (actor) only. Read-only. limit is clamped to [1,500].
func (s *Service) ListAdminAudit(ctx context.Context, actorID uuid.UUID, limit int) ([]store.AdminAuditEntry, error) {
	if !s.IsSuperadmin(ctx, actorID) {
		return nil, ErrNotSuperadmin
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.store.ListAdminAudit(ctx, limit)
}

package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/permissions"
	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// ErrNotSuperadmin is returned when a non-superadmin attempts role management.
var ErrNotSuperadmin = errors.New("superadmin role required")

// ErrMFARequired is returned when the acting session is not an admin MFA
// session (admin_mfa=false: TOTP not enrolled, or not completed on this
// session). HTTP code MFA_REQUIRED.
var ErrMFARequired = errors.New("an admin session with two-factor authentication is required: enrol TOTP, then sign in with it or POST /v1/auth/step-up")

// authorizePrivileged enforces the gate for role management and force logout:
// the actor must be a superadmin, the session must be an admin MFA session
// (ErrMFARequired) and must carry a step_up_at younger than
// accesstoken.StepUpValidity (ErrStepUpRequired). The session claims come from
// the request context (WithSessionAuth); without them the gate fails closed.
// Denied attempts are audit-logged.
//
// REQUIRE_MFA_FOR_PRIVILEGED no longer affects this gate — MFA is always
// required here — and it guards nothing else; the setting is inert.
func (s *Service) authorizePrivileged(ctx context.Context, actorID, targetID uuid.UUID, action string) error {
	if !s.IsSuperadmin(ctx, actorID) {
		s.audit(ctx, actorID, targetID, action, "denied: not superadmin", false)
		return ErrNotSuperadmin
	}
	if err := s.requireFreshAdminSession(ctx, actorID); err != nil {
		detail := "denied: admin MFA session required"
		if errors.Is(err, ErrStepUpRequired) {
			detail = "denied: step-up required"
		}
		s.audit(ctx, actorID, targetID, action, detail, false)
		return err
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
	// Admin is the admin-console permission map, resolved live on every
	// request (a grant shows without re-login). Additive: every field above is
	// unchanged. {"apps":{},"platform":[],"mfa_required":false,...} for a user
	// with no admin role.
	Admin AdminCapabilities `json:"admin"`
}

// AdminCapabilities is the admin permission map as THIS session may use it.
//
// An admin whose session is not an admin MFA session (admin_mfa=false) gets
// empty apps/platform and mfa_required=true; mfa_enrolled then tells the
// console whether to send them to TOTP enrolment or to step-up.
type AdminCapabilities struct {
	permissions.Admin
	MFARequired bool `json:"mfa_required"`
	MFAEnrolled bool `json:"mfa_enrolled"`
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

	// TokenRoles, not All: staff roles are not part of this map's shape.
	caps := make(map[string]bool, len(roles.TokenRoles()))
	for _, r := range roles.TokenRoles() {
		caps[r] = heldSet[r]
	}

	switcher := []CapabilitySwitch{{Role: CustomerSwitchRole, Label: "Customer"}}
	for _, r := range held {
		switcher = append(switcher, CapabilitySwitch{Role: r, Label: roles.Label(r)})
	}

	admin, err := s.PermissionsForUser(ctx, userID)
	if err != nil {
		s.log.Warn("admin permissions lookup failed; env roles only", "user_id", userID, "err", err)
	}
	adminCaps := AdminCapabilities{Admin: admin}
	if !adminEmpty(admin) {
		if u, uerr := s.store.GetUserByID(ctx, userID); uerr == nil && u != nil {
			adminCaps.MFAEnrolled = u.TwoFactorEnabled
		}
		// The token's admin_mfa was resolved at mint; enrolment is re-read
		// live so disabling TOTP takes effect at once here too.
		if sa, ok := SessionAuthFrom(ctx); !ok || !sa.AdminMFA || !adminCaps.MFAEnrolled {
			adminCaps = AdminCapabilities{
				Admin:       permissions.Admin{Apps: map[string][]string{}, Platform: []string{}},
				MFARequired: true,
				MFAEnrolled: adminCaps.MFAEnrolled,
			}
		}
	}

	return Capabilities{
		UserID:       userID.String(),
		Roles:        held,
		IsCustomer:   true,
		Capabilities: caps,
		Switcher:     switcher,
		Admin:        adminCaps,
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

// Admin role-change errors. Each maps to a distinct HTTP code in
// internal/http/roles_handler.go.
var (
	// ErrInvalidRole: the role is not in the vocabulary. The text is matched by
	// older callers, so it stays "invalid role".
	ErrInvalidRole = errors.New("invalid role")
	// ErrInvalidApp: the app is not in the catalogue.
	ErrInvalidApp = errors.New("invalid app")
	// ErrRoleNotScopable: the role cannot be held in that app (superadmin is
	// platform-wide; an ecosystem role has no app; finance holds nothing in qa).
	ErrRoleNotScopable = errors.New("role cannot be granted for that app")
	// ErrReasonRequired: every admin grant and revoke records why.
	ErrReasonRequired = errors.New("reason is required")
	// ErrInvalidExpiry: expires_at is not in the future.
	ErrInvalidExpiry = errors.New("expires_at must be in the future")
	// ErrSelfGrant: nobody grants a role to themselves, superadmins included.
	ErrSelfGrant = errors.New("you cannot grant a role to yourself; another superadmin must do it")
	// ErrLastSuperadmin: the change would leave no durable superadmin.
	ErrLastSuperadmin = errors.New("refused: this would remove or expire the last active superadmin; grant another superadmin first")
	// ErrEnvBootstrapRole: the role comes from an env allowlist. Match with
	// errors.Is; the concrete *EnvBootstrapRoleError names the variable.
	ErrEnvBootstrapRole = errors.New("role is granted by an environment allowlist")
)

// maxReasonLen bounds the free-text reason stored on the row and in the audit.
const maxReasonLen = 500

// EnvBootstrapRoleError explains why an env allowlist role cannot be revoked
// through the API, and what to do instead.
type EnvBootstrapRoleError struct {
	Role   string
	EnvVar string
}

func (e *EnvBootstrapRoleError) Error() string {
	return fmt.Sprintf("role %q for this user comes from the %s environment allowlist (bootstrap) "+
		"and cannot be revoked through the API; remove the user id from %s and restart auth-service",
		e.Role, e.EnvVar, e.EnvVar)
}

func (e *EnvBootstrapRoleError) Is(target error) bool { return target == ErrEnvBootstrapRole }

// RoleChangeRequest is one admin grant or revoke. App "" is platform-wide.
// ExpiresAt is ignored on revoke.
type RoleChangeRequest struct {
	TargetID  uuid.UUID
	Role      string
	App       string
	ExpiresAt *time.Time
	Reason    string
}

// validateRoleChange checks the request against the vocabulary and catalogue.
func validateRoleChange(req *RoleChangeRequest, revoke bool, now time.Time) error {
	req.Role = strings.TrimSpace(req.Role)
	req.App = strings.TrimSpace(req.App)
	req.Reason = strings.TrimSpace(req.Reason)
	if !roles.Valid(req.Role) {
		return ErrInvalidRole
	}
	if req.App != "" && !permissions.ValidApp(req.App) {
		return ErrInvalidApp
	}
	if _, err := permissions.For(req.Role, req.App); err != nil {
		return fmt.Errorf("%w: %v", ErrRoleNotScopable, err)
	}
	if req.Reason == "" {
		return ErrReasonRequired
	}
	if len(req.Reason) > maxReasonLen {
		req.Reason = req.Reason[:maxReasonLen]
	}
	if revoke {
		req.ExpiresAt = nil
	} else if req.ExpiresAt != nil && !req.ExpiresAt.After(now) {
		return ErrInvalidExpiry
	}
	return nil
}

// roleChangeDetail renders the audit detail of an admin role change.
func roleChangeDetail(req RoleChangeRequest) string {
	d := "role=" + req.Role
	if req.App != "" {
		d += " app=" + req.App
	} else {
		d += " app=platform-wide"
	}
	if req.ExpiresAt != nil {
		d += " expires_at=" + req.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return d + " reason=" + req.Reason
}

// envBootstrapSource returns the env allowlist variable that grants role to
// userID platform-wide, or "" when none does (implication included: a
// SUPERADMIN_USER_IDS holder holds admin and moderator from that variable).
func (s *Service) envBootstrapSource(userID uuid.UUID, role string) string {
	id := userID.String()
	sources := []struct {
		set    map[string]struct{}
		role   string
		envVar string
	}{
		{s.cfg.ScopeSuperadminUserIDs, roles.Superadmin, "SUPERADMIN_USER_IDS"},
		{s.cfg.ScopeAdminUserIDs, roles.Admin, "ADMIN_USER_IDS"},
		{s.cfg.ScopeModeratorUserIDs, roles.Moderator, "MODERATOR_USER_IDS"},
	}
	for _, src := range sources {
		if _, ok := src.set[id]; !ok {
			continue
		}
		for _, r := range roles.Expand([]string{src.role}) {
			if r == role {
				return src.envVar
			}
		}
	}
	return ""
}

// lastSuperadminGuard refuses a change that takes the number of DURABLE
// superadmins (active, no expiry, or held through SUPERADMIN_USER_IDS) from at
// least one to zero. A superadmin with an expiry is not counted as durable:
// leaving only temporary holders would lock the platform out when they lapse.
func (s *Service) lastSuperadminGuard(target uuid.UUID) func([]store.SuperadminHolder) error {
	return func(holders []store.SuperadminHolder) error {
		durable := map[uuid.UUID]bool{}
		for id := range s.cfg.ScopeSuperadminUserIDs {
			if u, err := uuid.Parse(id); err == nil {
				durable[u] = true
			}
		}
		for _, h := range holders {
			if h.ExpiresAt == nil {
				durable[h.UserID] = true
			}
		}
		before := len(durable)
		if _, env := s.cfg.ScopeSuperadminUserIDs[target.String()]; !env {
			delete(durable, target)
		}
		if before > 0 && len(durable) == 0 {
			return ErrLastSuperadmin
		}
		return nil
	}
}

// GrantRole grants a role, platform-wide or for one app, to a target user.
//
// Rules, in order: the request must name a known role and app, the role must
// be holdable in that app, a reason is required and an expiry must be in the
// future (400s); the actor must be a superadmin (and 2FA-enrolled when
// REQUIRE_MFA_FOR_PRIVILEGED); nobody grants to themselves; putting an expiry
// on the last durable superadmin is refused. The row and its audit record are
// written in one transaction. Re-granting renews expiry and reason.
//
// The `scopes` claim changes on the next mint (refresh is enough); the admin
// permission map (capabilities, internal permissions route) changes at once.
func (s *Service) GrantRole(ctx context.Context, actorID uuid.UUID, req RoleChangeRequest) error {
	return s.changeRole(ctx, actorID, req, false)
}

// RevokeRole removes one grant (role + app). Same validation and
// authorisation as GrantRole; additionally a role held through an env
// allowlist cannot be revoked here, and removing the last durable superadmin
// is refused. Revoking a grant that does not exist succeeds and is audited.
func (s *Service) RevokeRole(ctx context.Context, actorID uuid.UUID, req RoleChangeRequest) error {
	return s.changeRole(ctx, actorID, req, true)
}

func (s *Service) changeRole(ctx context.Context, actorID uuid.UUID, req RoleChangeRequest, revoke bool) error {
	action := "role.grant"
	if revoke {
		action = "role.revoke"
	}
	if err := validateRoleChange(&req, revoke, time.Now()); err != nil {
		return err
	}
	if err := s.authorizePrivileged(ctx, actorID, req.TargetID, action); err != nil {
		return err
	}
	if !revoke && actorID == req.TargetID {
		s.audit(ctx, actorID, req.TargetID, action, "denied: self-grant, "+roleChangeDetail(req), false)
		return ErrSelfGrant
	}
	if revoke && req.App == "" {
		if envVar := s.envBootstrapSource(req.TargetID, req.Role); envVar != "" {
			s.audit(ctx, actorID, req.TargetID, action, "denied: env bootstrap role ("+envVar+"), "+roleChangeDetail(req), false)
			return &EnvBootstrapRoleError{Role: req.Role, EnvVar: envVar}
		}
	}

	var guard func([]store.SuperadminHolder) error
	if req.Role == roles.Superadmin && (revoke || req.ExpiresAt != nil) {
		guard = s.lastSuperadminGuard(req.TargetID)
	}
	res, err := s.store.ChangeRole(ctx, store.RoleChange{
		Revoke:    revoke,
		UserID:    req.TargetID,
		GrantedBy: actorID,
		Role:      req.Role,
		App:       req.App,
		ExpiresAt: req.ExpiresAt,
		Reason:    req.Reason,
	}, store.RoleAudit{
		ActorID:  actorID,
		TargetID: req.TargetID,
		Action:   action,
		Detail:   roleChangeDetail(req),
	}, guard)
	if errors.Is(err, ErrLastSuperadmin) {
		s.audit(ctx, actorID, req.TargetID, action, "denied: last superadmin, "+roleChangeDetail(req), false)
	}
	if err != nil {
		return err
	}
	// A revoke or an expiry-shortening renewal revoked the target's sessions
	// in the same transaction; mark each one so the gateway (which checks
	// sess_revoked:<sid>) stops their access tokens now, not at expiry.
	s.revokeCached(ctx, res.RevokedSessions)
	if len(res.RevokedSessions) > 0 {
		s.InvalidatePending2FASessions(ctx, req.TargetID)
	}
	return nil
}

// PermissionsForUser resolves a user's admin permission map live: active DB
// rows (platform-wide and app-scoped) plus the env allowlist roles as
// platform-wide grants. On a DB error it returns the env-only map AND the
// error, so a caller can choose to degrade (capabilities) or refuse (the
// internal route).
func (s *Service) PermissionsForUser(ctx context.Context, userID uuid.UUID) (permissions.Admin, error) {
	var grants []permissions.Grant
	for _, r := range s.cfg.EnvRolesForUser(userID.String()) {
		grants = append(grants, permissions.Grant{Role: r})
	}
	now := time.Now()
	dbGrants, err := s.store.RoleGrantsForUser(ctx, userID)
	if err != nil {
		return permissions.Resolve(grants, now), err
	}
	for _, g := range dbGrants {
		grants = append(grants, permissions.Grant{Role: g.Role, App: g.App, ExpiresAt: g.ExpiresAt})
	}
	return permissions.Resolve(grants, now), nil
}

// RecordEnvBootstrap writes one audit row per env allowlist holder per role,
// idempotently (a restart writes nothing new). Called once at boot. Invalid
// ids in the allowlists are logged and skipped. Errors are returned joined so
// boot can log them; they must not stop the service.
func (s *Service) RecordEnvBootstrap(ctx context.Context) error {
	sources := []struct {
		set    map[string]struct{}
		role   string
		envVar string
	}{
		{s.cfg.ScopeSuperadminUserIDs, roles.Superadmin, "SUPERADMIN_USER_IDS"},
		{s.cfg.ScopeAdminUserIDs, roles.Admin, "ADMIN_USER_IDS"},
		{s.cfg.ScopeModeratorUserIDs, roles.Moderator, "MODERATOR_USER_IDS"},
	}
	var errs []error
	for _, src := range sources {
		ids := make([]string, 0, len(src.set))
		for id := range src.set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			uid, err := uuid.Parse(id)
			if err != nil {
				s.log.Warn("env role allowlist holds an invalid user id; skipped", "env", src.envVar)
				continue
			}
			detail := "role=" + src.role + " app=platform-wide source=env:" + src.envVar
			inserted, err := s.store.RecordRoleBootstrap(ctx, uid, detail)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if inserted {
				s.log.Info("env bootstrap role recorded in audit", "user_id", uid, "role", src.role, "env", src.envVar)
			}
		}
	}
	return errors.Join(errs...)
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

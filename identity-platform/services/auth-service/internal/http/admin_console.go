package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/internal/servicetoken"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// The admin console's Access page, through admin-service (Wave 1, B4).
//
// admin-service is the console's one backend. For every call it has already
// resolved the admin's permissions (GET /v1/auth/internal/users/:id/permissions),
// required an admin MFA session, required a fresh step-up for role changes,
// and written its own audit row. It then calls THIS family with a token it
// signs for the call:
//
//	iss   admin-service
//	aud   identity
//	scope [the one permission it checked, e.g. platform:roles.manage]
//	act   the admin's user id
//	exp   ≤ 60 s, jti unique
//
// in X-Service-Authorization: Bearer <token>. Identity judges the request by
// the token alone: the internal key is not evidence (the family is outside
// the internal-key group), a gateway identity header is refused outright,
// and the actor is the SIGNED act claim. MFA and step-up are not re-checked
// here — there is no session on the request — so the trust boundary is:
// admin-service enforces them before it signs; identity enforces everything
// about the ROLE CHANGE itself (service/admin_console.go).
//
// Routes (all under /v1/auth/internal/admin, permission in the token's scope):
//
//	GET    /roles?role=&app=&limit=&offset=              platform:roles.read
//	GET    /users/search?q=&limit=                        platform:users.search
//	GET    /users/:userId/roles                           platform:roles.read
//	POST   /users/:userId/roles {role,app,expires_at,reason}   platform:roles.manage
//	DELETE /users/:userId/roles/:role?app=&reason=        platform:roles.manage
//	POST   /users/:userId/sessions/revoke {reason}        platform:sessions.revoke
//	GET    /audit?actor=&target=&action=&from=&to=&limit=&offset=
//	                                          platform:roles.read or platform:audit.read

// ServiceAuthHeader carries the admin-service token.
const ServiceAuthHeader = "X-Service-Authorization"

// IssuerAdminService is the only caller whose tokens reach this family.
const IssuerAdminService = "admin-service"

// AudienceIdentity is the audience admin-service must mint for.
const AudienceIdentity = "identity"

// Platform permissions this family checks, spelled as the catalogue spells them.
const (
	PermRolesRead      = "platform:roles.read"
	PermRolesManage    = "platform:roles.manage"
	PermUsersSearch    = "platform:users.search"
	PermSessionsRevoke = "platform:sessions.revoke"
	PermAuditRead      = "platform:audit.read"
)

// AdminServicePermissions is every operation admin-service may name in a
// token to identity. The verifier registers admin-service with exactly this
// list, so a token carrying anything else is refused for it.
var AdminServicePermissions = []string{PermRolesRead, PermRolesManage, PermUsersSearch, PermSessionsRevoke, PermAuditRead}

// Error codes for the token path. Shared vocabulary with the products
// (dating admin_token.go).
const (
	CodeAdminTokenRequired      = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeServiceTokenRejected    = "SERVICE_TOKEN_REJECTED"
	CodeServiceTokenReplayed    = "SERVICE_TOKEN_REPLAYED"
	CodeServiceTokenUnavailable = "SERVICE_TOKEN_CHECK_UNAVAILABLE"
	CodeAdminPermissionScope    = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired      = "ADMIN_ACTOR_REQUIRED"
	CodeSuperadminRequired      = "SUPERADMIN_REQUIRED"
)

const ctxConsoleActor = "auth_console_actor"

// AdminServiceVerifier builds the verifier for this family from
// ADMIN_SERVICE_TOKEN_PUBKEY and ADMIN_SERVICE_TOKEN_KID. No key → (nil, nil)
// and the family accepts nothing. A key without a kid, or an unreadable key,
// is a configuration error: the service must not boot with the Access page
// silently disabled.
func AdminServiceVerifier(cfg *config.Config) (*servicetoken.Verifier, error) {
	pub := strings.TrimSpace(cfg.AdminServiceTokenPubKey)
	kid := strings.TrimSpace(cfg.AdminServiceTokenKID)
	if pub == "" {
		return nil, nil
	}
	if kid == "" {
		return nil, errors.New("ADMIN_SERVICE_TOKEN_PUBKEY is set but ADMIN_SERVICE_TOKEN_KID is empty")
	}
	v := servicetoken.NewVerifier(AudienceIdentity)
	if err := v.RegisterBase64(IssuerAdminService, kid, pub, AdminServicePermissions); err != nil {
		return nil, err
	}
	return v, nil
}

// SetAdminTokenVerifier installs (or replaces) the verifier. Tests use it to
// pin the clock; New installs the configured one.
func (h *Handler) SetAdminTokenVerifier(v *servicetoken.Verifier) { h.adminTokens = v }

func rawServiceToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c.GetHeader(ServiceAuthHeader)), "Bearer "))
}

// requireAdminServiceToken admits ONLY an admin-service token whose scope
// holds one of perms. In order:
//
//  1. Any gateway identity header (X-User-Id, X-Verified-User-Id, X-Scopes,
//     X-Admin-Role) → 403 USER_CALLER_REFUSED. A user request, whatever
//     else it carries, never reaches these routes.
//  2. No token → 401 ADMIN_SERVICE_TOKEN_REQUIRED; no verifier configured
//     → 401 SERVICE_TOKEN_CHECK_UNAVAILABLE.
//  3. Bad signature, unknown caller or kid, wrong audience, expired,
//     over-long, no jti → 403 SERVICE_TOKEN_REJECTED; scope holds none of
//     perms → 403 ADMIN_PERMISSION_NOT_IN_TOKEN; issuer not admin-service →
//     403 SERVICE_TOKEN_REJECTED; act missing or not a uuid → 403
//     ADMIN_ACTOR_REQUIRED.
//  4. A jti seen before (Redis, keyed for the token's lifetime) → 403
//     SERVICE_TOKEN_REPLAYED; Redis unreachable → 503 (fail closed: a role
//     change must not be replayable just because the cache is down).
//
// On success the verified actor and jti are on the context for the handler.
func (h *Handler) requireAdminServiceToken(perms ...string) gin.HandlerFunc {
	if len(perms) == 0 {
		panic("auth: requireAdminServiceToken needs the route's permission")
	}
	return func(c *gin.Context) {
		for _, name := range gatewayIdentityHeaders {
			for _, v := range c.Request.Header.Values(name) {
				if strings.TrimSpace(v) != "" {
					api.Error(c.Writer, http.StatusForbidden, CodeUserCallerRefused,
						"service-only endpoint; user requests are not accepted", nil, nil)
					c.Abort()
					return
				}
			}
		}
		raw := rawServiceToken(c)
		if raw == "" {
			api.Error(c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired, "an admin-service token is required", nil, nil)
			c.Abort()
			return
		}
		actor, ok := h.verifyAdminServiceToken(c, raw, perms)
		if !ok {
			c.Abort()
			return
		}
		c.Set(ctxConsoleActor, actor)
		c.Next()
	}
}

func (h *Handler) verifyAdminServiceToken(c *gin.Context, raw string, perms []string) (service.ConsoleActor, bool) {
	log := h.log.With("path", c.Request.URL.Path, "request_id", RequestIDFromContext(c))
	if h.adminTokens == nil || h.adminTokens.Callers() == 0 {
		api.Error(c.Writer, http.StatusUnauthorized, CodeServiceTokenUnavailable,
			"admin-service tokens are not accepted by this deployment", nil, nil)
		return service.ConsoleActor{}, false
	}
	var verified *servicetoken.Verified
	for _, perm := range perms {
		v, err := h.adminTokens.Verify(raw, perm)
		if err == nil {
			verified = v
			break
		}
		if errors.Is(err, servicetoken.ErrScopeDenied) {
			continue
		}
		// Anything but a scope miss is about the token itself; no other
		// permission can rescue it.
		log.Warn("admin-service token refused", "reason", err.Error())
		api.Error(c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil, nil)
		return service.ConsoleActor{}, false
	}
	if verified == nil {
		log.Warn("admin-service token lacks the route permission", "required", perms[0])
		api.Error(c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
			"the token does not carry the permission this route requires", map[string]any{"required": perms[0]}, nil)
		return service.ConsoleActor{}, false
	}
	if verified.Issuer != IssuerAdminService {
		log.Warn("admin route called by a non-admin service", "issuer", verified.Issuer)
		api.Error(c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil, nil)
		return service.ConsoleActor{}, false
	}
	actorID, err := uuid.Parse(verified.Actor)
	if err != nil || actorID == uuid.Nil {
		api.Error(c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil, nil)
		return service.ConsoleActor{}, false
	}
	if ok, err := h.firstUseOfJTI(c, verified.JTI, verified.ExpiresAt); err != nil {
		log.Error("admin-service token replay check failed", "err", err)
		api.Error(c.Writer, http.StatusServiceUnavailable, CodeServiceTokenUnavailable,
			"service token could not be checked for replay", nil, nil)
		return service.ConsoleActor{}, false
	} else if !ok {
		log.Warn("admin-service token replayed", "jti", verified.JTI, "actor", actorID)
		api.Error(c.Writer, http.StatusForbidden, CodeServiceTokenReplayed, "service token already used", nil, nil)
		return service.ConsoleActor{}, false
	}
	return service.ConsoleActor{UserID: actorID, JTI: verified.JTI}, true
}

// firstUseOfJTI records the jti for the token's remaining lifetime and
// reports whether this is its first use. Without Redis (unit tests, a
// deployment that has none) every token is a first use.
func (h *Handler) firstUseOfJTI(c *gin.Context, jti string, expiresAt time.Time) (bool, error) {
	if h.rdb == nil {
		return true, nil
	}
	ttl := time.Until(expiresAt)
	if ttl < time.Second {
		ttl = time.Second
	}
	ok, err := h.rdb.SetNX(c.Request.Context(), "admin_svc_jti:"+jti, "1", ttl).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return false, err
	}
	return ok, nil
}

func consoleActor(c *gin.Context) service.ConsoleActor {
	v, _ := c.Get(ctxConsoleActor)
	a, _ := v.(service.ConsoleActor)
	return a
}

func pathUserID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("userId"))
	if err != nil || id == uuid.Nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid user id", nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

func queryInt(c *gin.Context, name string) int {
	n, _ := strconv.Atoi(c.Query(name))
	return n
}

// writeConsoleErr maps the console path's errors; everything else is the
// role vocabulary of writeRoleErr.
func (h *Handler) writeConsoleErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrSuperadminChangeRequiresSuperadmin):
		api.Error(c.Writer, http.StatusForbidden, CodeSuperadminRequired, err.Error(), nil, nil)
	case errors.Is(err, service.ErrSearchQueryTooShort):
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
	default:
		h.writeRoleErr(c, err)
	}
}

// ConsoleListRoleHolders — GET /v1/auth/internal/admin/roles
//
//	?role=moderator&app=dating|platform-wide&limit=50&offset=0
//	200 {"holders":[{user_id,role,app,expires_at,reason,granted_by,granted_at,mfa_enrolled,active}],
//	     "env_holders":[{user_id,role,source,mfa_enrolled}],"limit","offset"}
//
// role "" lists the admin roles (not sellers etc.); app "" lists every
// scope, "platform-wide" the app-NULL rows only.
func (h *Handler) ConsoleListRoleHolders(c *gin.Context) {
	f := store.RoleHolderFilter{
		Role:   strings.TrimSpace(c.Query("role")),
		Limit:  queryInt(c, "limit"),
		Offset: queryInt(c, "offset"),
	}
	switch app := strings.TrimSpace(c.Query("app")); app {
	case "":
		f.Scope = store.AppAny
	case "platform-wide":
		f.Scope = store.AppPlatformWide
	default:
		f.Scope, f.App = store.AppExact, app
	}
	out, err := h.svc.ConsoleListRoleHolders(c.Request.Context(), f)
	if err != nil {
		h.writeConsoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"holders": out.Holders, "env_holders": out.EnvHolders,
		"limit": f.Limit, "offset": f.Offset,
	}, nil)
}

// ConsoleListUserRoles — GET /v1/auth/internal/admin/users/:userId/roles
//
//	200 {"user_id","roles":[store.UserRole…]}
func (h *Handler) ConsoleListUserRoles(c *gin.Context) {
	target, ok := pathUserID(c)
	if !ok {
		return
	}
	rows, err := h.svc.ConsoleListUserRoles(c.Request.Context(), target)
	if err != nil {
		h.writeConsoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"user_id": target.String(), "roles": rows}, nil)
}

// consoleRoleBody is the body of POST …/roles and (optionally) DELETE
// …/roles/:role. The target is the path; the actor is the token.
type consoleRoleBody struct {
	Role      string     `json:"role"`
	App       string     `json:"app"`
	ExpiresAt *time.Time `json:"expires_at"`
	Reason    string     `json:"reason"`
}

// ConsoleGrantRole — POST /v1/auth/internal/admin/users/:userId/roles
//
//	{"role":"moderator","app":"dating","expires_at":"2026-12-31T00:00:00Z","reason":"…"}
//	200 {"status":"granted","user_id","role","app","expires_at"}
//
// Errors as POST /v1/auth/admin/roles (400 BAD_REQUEST / INVALID_APP /
// ROLE_NOT_SCOPABLE / REASON_REQUIRED / INVALID_EXPIRY, 403
// SELF_GRANT_REFUSED, 409 LAST_SUPERADMIN) plus 403 SUPERADMIN_REQUIRED
// when the role is superadmin and the actor is not one.
func (h *Handler) ConsoleGrantRole(c *gin.Context) {
	target, ok := pathUserID(c)
	if !ok {
		return
	}
	var body consoleRoleBody
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Role) == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			"role and reason are required; expires_at must be RFC 3339", nil, nil)
		return
	}
	req := service.RoleChangeRequest{TargetID: target, Role: body.Role, App: body.App, ExpiresAt: body.ExpiresAt, Reason: body.Reason}
	if err := h.svc.ConsoleGrantRole(c.Request.Context(), consoleActor(c), req); err != nil {
		h.writeConsoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, roleResponse("granted", req), nil)
}

// ConsoleRevokeRole — DELETE /v1/auth/internal/admin/users/:userId/roles/:role
//
//	?app=dating&reason=…   or a JSON body {"app","reason"} (body wins)
//	200 {"status":"revoked","user_id","role","app"}
//
// Errors as DELETE /v1/auth/admin/roles, including 409 ENV_BOOTSTRAP_ROLE
// for an env allowlist holder and 409 LAST_SUPERADMIN.
func (h *Handler) ConsoleRevokeRole(c *gin.Context) {
	target, ok := pathUserID(c)
	if !ok {
		return
	}
	req := service.RoleChangeRequest{
		TargetID: target, Role: c.Param("role"),
		App: strings.TrimSpace(c.Query("app")), Reason: strings.TrimSpace(c.Query("reason")),
	}
	if c.Request.ContentLength != 0 {
		var body consoleRoleBody
		if err := c.ShouldBindJSON(&body); err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "body must be JSON {app, reason}", nil, nil)
			return
		}
		if strings.TrimSpace(body.App) != "" {
			req.App = body.App
		}
		if strings.TrimSpace(body.Reason) != "" {
			req.Reason = body.Reason
		}
	}
	if err := h.svc.ConsoleRevokeRole(c.Request.Context(), consoleActor(c), req); err != nil {
		h.writeConsoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, roleResponse("revoked", req), nil)
}

// ConsoleForceLogout — POST /v1/auth/internal/admin/users/:userId/sessions/revoke
//
//	{"reason":"…"}
//	200 {"user_id","sessions_revoked":N}
func (h *Handler) ConsoleForceLogout(c *gin.Context) {
	target, ok := pathUserID(c)
	if !ok {
		return
	}
	var req forceLogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "reason is required", nil, nil)
		return
	}
	n, err := h.svc.ConsoleForceLogout(c.Request.Context(), consoleActor(c), target, req.Reason)
	if err != nil {
		h.writeConsoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"user_id": target.String(), "sessions_revoked": n}, nil)
}

// ConsoleListAudit — GET /v1/auth/internal/admin/audit
//
//	?actor=<uuid>&target=<uuid>&action=role.grant&from=RFC3339&to=RFC3339&limit=&offset=
//	200 {"entries":[store.AdminAuditEntry…],"limit","offset"}
func (h *Handler) ConsoleListAudit(c *gin.Context) {
	f := store.AuditFilter{Action: strings.TrimSpace(c.Query("action")), Limit: queryInt(c, "limit"), Offset: queryInt(c, "offset")}
	for name, dst := range map[string]**uuid.UUID{"actor": &f.Actor, "target": &f.Target} {
		if v := strings.TrimSpace(c.Query(name)); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", name+" must be a uuid", nil, nil)
				return
			}
			*dst = &id
		}
	}
	for name, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if v := strings.TrimSpace(c.Query(name)); v != "" {
			ts, err := time.Parse(time.RFC3339, v)
			if err != nil {
				api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", name+" must be RFC 3339", nil, nil)
				return
			}
			*dst = &ts
		}
	}
	entries, err := h.svc.ConsoleListAudit(c.Request.Context(), f)
	if err != nil {
		h.writeConsoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"entries": entries, "limit": f.Limit, "offset": f.Offset}, nil)
}

// ConsoleSearchUsers — GET /v1/auth/internal/admin/users/search?q=&limit=
//
//	200 {"results":[{"user_id","email_masked":"r***@example.com","handle":"raghu"}]}
//
// q is a user id, or an email or handle prefix of at least two characters.
// Nothing beyond the three fields is returned.
func (h *Handler) ConsoleSearchUsers(c *gin.Context) {
	results, err := h.svc.ConsoleSearchUsers(c.Request.Context(), c.Query("q"), queryInt(c, "limit"))
	if err != nil {
		h.writeConsoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"results": results}, nil)
}

// registerConsoleRoutes mounts the token-only family. Outside the
// internal-key group on purpose: the key is not evidence here.
func (h *Handler) registerConsoleRoutes(v1 *gin.RouterGroup) {
	g := v1.Group("/internal/admin")
	g.GET("/roles", h.requireAdminServiceToken(PermRolesRead), h.ConsoleListRoleHolders)
	g.GET("/users/search", h.requireAdminServiceToken(PermUsersSearch), h.ConsoleSearchUsers)
	g.GET("/users/:userId/roles", h.requireAdminServiceToken(PermRolesRead), h.ConsoleListUserRoles)
	g.POST("/users/:userId/roles", h.requireAdminServiceToken(PermRolesManage), h.ConsoleGrantRole)
	g.DELETE("/users/:userId/roles/:role", h.requireAdminServiceToken(PermRolesManage), h.ConsoleRevokeRole)
	g.POST("/users/:userId/sessions/revoke", h.requireAdminServiceToken(PermSessionsRevoke), h.ConsoleForceLogout)
	g.GET("/audit", h.requireAdminServiceToken(PermRolesRead, PermAuditRead), h.ConsoleListAudit)
}

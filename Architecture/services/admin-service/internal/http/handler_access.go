package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Platform permissions the Access page uses, spelled as identity's catalogue
// and its console family (auth-service/internal/http/admin_console.go) spell
// them. roles.manage is superadmin-only in the catalogue.
const (
	permPlatformRolesRead      = "platform:roles.read"
	permPlatformRolesManage    = "platform:roles.manage"
	permPlatformUsersSearch    = "platform:users.search"
	permPlatformSessionsRevoke = "platform:sessions.revoke"
	permPlatformAuditRead      = "platform:audit.read"
)

const (
	accessAuditApp = "platform"
	accessPrefix   = "/v1/admin/access"

	opAccessCatalogue      = "access.catalogue.read"
	opAccessRolesList      = "access.roles.list"
	opAccessUsersSearch    = "access.users.search"
	opAccessUserRolesList  = "access.user.roles.list"
	opAccessRoleGrant      = "access.role.grant"
	opAccessRoleRevoke     = "access.role.revoke"
	opAccessSessionsRevoke = "access.sessions.revoke"
	opAccessAuditList      = "access.audit.list"

	// accessTargetUser is the audit target type of every per-user route.
	accessTargetUser = "user"
)

// AccessRoutes is the Access page's route table under /v1/admin/access. Every
// route but the catalogue forwards to identity's console family at the same
// path, with a token for audience "identity" scoped to its permission.
//
//	step-up     every write: grant, revoke, force-logout
//	two-person  granting or revoking SUPERADMIN (decided from the request):
//	            permanent, highest-impact; a second TOTP-enrolled holder of
//	            platform:roles.manage approves, or the sole-holder path runs
//	            and is recorded, as elsewhere. Every other role change is
//	            step-up only.
//
// Identity's own rules (SUPERADMIN_REQUIRED, LAST_SUPERADMIN,
// ENV_BOOTSTRAP_ROLE, SELF_GRANT_REFUSED, REASON_REQUIRED, INVALID_APP,
// ROLE_NOT_SCOPABLE, INVALID_EXPIRY) are answered by identity and passed
// through unchanged, status and body.
var AccessRoutes = []productRoute{
	// The catalogue is served from admin-service's mirror (adminauth), not
	// forwarded: identity exposes no catalogue route.
	{method: http.MethodGet, path: "/catalogue", operation: opAccessCatalogue, permission: permPlatformRolesRead},

	{method: http.MethodGet, path: "/roles", operation: opAccessRolesList, permission: permPlatformRolesRead},
	{method: http.MethodGet, path: "/users/search", operation: opAccessUsersSearch, permission: permPlatformUsersSearch},
	{method: http.MethodGet, path: "/users/:userId/roles", operation: opAccessUserRolesList, permission: permPlatformRolesRead, targetType: accessTargetUser},
	{method: http.MethodPost, path: "/users/:userId/roles", operation: opAccessRoleGrant, permission: permPlatformRolesManage, stepUp: true, mayTwoPerson: true, targetType: accessTargetUser},
	{method: http.MethodDelete, path: "/users/:userId/roles/:role", operation: opAccessRoleRevoke, permission: permPlatformRolesManage, stepUp: true, mayTwoPerson: true, targetType: accessTargetUser},
	{method: http.MethodPost, path: "/users/:userId/sessions/revoke", operation: opAccessSessionsRevoke, permission: permPlatformSessionsRevoke, stepUp: true, targetType: accessTargetUser},
	// Identity admits the audit read by either permission; the token carries
	// the first one the admin holds.
	{method: http.MethodGet, path: "/audit", operation: opAccessAuditList, permission: permPlatformRolesRead, alternatives: []string{permPlatformRolesRead, permPlatformAuditRead}},
}

// accessRoleBody is the console body of a grant (all fields) and of a revoke
// (app and reason). It is also what identity reads.
type accessRoleBody struct {
	Role      string     `json:"role,omitempty"`
	App       string     `json:"app,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Reason    string     `json:"reason,omitempty"`
}

// accessRolePayload is the stored two-person request: exactly what the
// executor sends identity, with the target user, so the hash covers it all.
type accessRolePayload struct {
	UserID    string     `json:"user_id"`
	Role      string     `json:"role"`
	App       string     `json:"app,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Reason    string     `json:"reason"`
}

// RegisterAccessRoutes adds the Access page under /v1/admin/access.
func (h *Handler) RegisterAccessRoutes(r *gin.Engine) {
	p := product{app: accessAuditApp, label: "Access", prefix: accessPrefix, client: h.identity}
	routes := make([]productRoute, len(AccessRoutes))
	copy(routes, AccessRoutes)
	for i := range routes {
		switch routes[i].operation {
		case opAccessRoleGrant:
			routes[i].decide = accessGrantDecision
		case opAccessRoleRevoke:
			routes[i].decide = accessRevokeDecision
		}
	}

	// The two-person executors run the stored payload as the approver (or the
	// sole holder); identity judges the approver live as it would any actor.
	h.approvals.Register(accessAuditApp, opAccessRoleGrant, h.accessExecutor(func(pl accessRolePayload) service.ProductRequest {
		return service.ProductRequest{
			Method: http.MethodPost, Path: "/users/" + pl.UserID + "/roles",
			Body: accessRoleBody{Role: pl.Role, App: pl.App, ExpiresAt: pl.ExpiresAt, Reason: pl.Reason},
		}
	}))
	h.approvals.Register(accessAuditApp, opAccessRoleRevoke, h.accessExecutor(func(pl accessRolePayload) service.ProductRequest {
		return service.ProductRequest{
			Method: http.MethodDelete, Path: "/users/" + pl.UserID + "/roles/" + pl.Role,
			Body: accessRoleBody{App: pl.App, Reason: pl.Reason},
		}
	}))

	special := map[string]gin.HandlerFunc{
		opAccessCatalogue:  h.accessCatalogue,
		opAccessRoleGrant:  h.accessGrant(p),
		opAccessRoleRevoke: h.accessRevoke(p),
	}
	h.registerProduct(r, p, routes, special)
}

// accessExecutor builds an approvals executor from the payload → request
// mapping, signing for platform:roles.manage as the executing admin.
func (h *Handler) accessExecutor(build func(accessRolePayload) service.ProductRequest) approvals.Executor {
	return func(ctx context.Context, actor string, payload json.RawMessage) approvals.Result {
		var pl accessRolePayload
		if err := json.Unmarshal(payload, &pl); err != nil {
			return approvals.Result{Err: err}
		}
		id, err := uuid.Parse(pl.UserID)
		if err != nil || id == uuid.Nil || !validRoleName(pl.Role) {
			return approvals.Result{Err: errInvalidID}
		}
		pl.UserID = id.String()
		pr := build(pl)
		pr.Permission, pr.Actor = permPlatformRolesManage, actor
		resp, err := h.identity.Do(ctx, pr)
		return approvals.Result{Data: resp.Body, Status: resp.Status, Err: err}
	}
}

// validRoleName bounds a role before it becomes a path segment; identity
// still decides whether it is a role at all.
func validRoleName(role string) bool {
	if role == "" || len(role) > 64 {
		return false
	}
	for _, r := range role {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// accessGrantDecision reads the role being granted: superadmin is two-person.
// Anything else about the request is identity's to judge.
func accessGrantDecision(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
	raw, _, err := jsonBody(c)
	if err != nil {
		return Decision{}, badRequest(CodeInvalidBody, "The request body must be JSON")
	}
	var b accessRoleBody
	_ = json.Unmarshal(raw, &b)
	role := strings.TrimSpace(b.Role)
	audit := map[string]any{}
	if role != "" {
		audit["role"] = role
	}
	if app := strings.TrimSpace(b.App); app != "" {
		audit["app"] = app
	}
	return Decision{TwoPerson: role == adminauth.RoleSuperadmin, Audit: audit}, nil
}

// accessRevokeDecision reads the role in the path: superadmin is two-person.
func accessRevokeDecision(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
	if _, _, err := jsonBody(c); err != nil {
		return Decision{}, badRequest(CodeInvalidBody, "The request body must be JSON")
	}
	role := c.Param("role")
	audit := map[string]any{"role": role}
	if app := accessRevokeApp(c); app != "" {
		audit["app"] = app
	}
	return Decision{TwoPerson: role == adminauth.RoleSuperadmin, Audit: audit}, nil
}

// accessRevokeApp is the app of a revoke: the JSON body wins over ?app=.
func accessRevokeApp(c *gin.Context) string {
	_, fields, _ := jsonBody(c)
	if app := stringField(fields, "app"); app != "" {
		return app
	}
	return strings.TrimSpace(c.Query("app"))
}

// accessUserID reads and validates :userId; it answers 400 when invalid.
func accessUserID(c *gin.Context) (string, bool) {
	id, err := uuid.Parse(c.Param("userId"))
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid user id", nil)
		return "", false
	}
	return id.String(), true
}

// accessGrant forwards a grant as sent (identity validates the body), or
// submits it for a second superadmin when the decision made it two-person.
func (h *Handler) accessGrant(p product) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := accessUserID(c)
		if !ok {
			return
		}
		raw, _, _ := jsonBody(c)
		var b accessRoleBody
		_ = json.Unmarshal(raw, &b)
		info := auditFrom(c)
		info.targetType, info.targetID, info.reason = accessTargetUser, userID, b.Reason
		if req, ok := effectiveRequirement(c); ok && req.TwoPerson {
			h.submitTwoPerson(c, accessTargetUser, userID, b.Reason, accessRolePayload{
				UserID: userID, Role: strings.TrimSpace(b.Role), App: strings.TrimSpace(b.App), ExpiresAt: b.ExpiresAt, Reason: b.Reason,
			})
			return
		}
		h.productCall(c, p, service.ProductRequest{Method: http.MethodPost, Path: "/users/" + userID + "/roles", RawBody: raw}, false)
	}
}

// accessRevoke forwards a revoke with its JSON body {app, reason} (and any
// query) as sent, or submits it when the role is superadmin.
func (h *Handler) accessRevoke(p product) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := accessUserID(c)
		if !ok {
			return
		}
		role := c.Param("role")
		if !validRoleName(role) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid role", nil)
			return
		}
		raw, fields, _ := jsonBody(c)
		reason := stringField(fields, "reason")
		if reason == "" {
			reason = strings.TrimSpace(c.Query("reason"))
		}
		info := auditFrom(c)
		info.targetType, info.targetID, info.reason = accessTargetUser, userID, reason
		if req, ok := effectiveRequirement(c); ok && req.TwoPerson {
			h.submitTwoPerson(c, accessTargetUser, userID, reason, accessRolePayload{
				UserID: userID, Role: role, App: accessRevokeApp(c), Reason: reason,
			})
			return
		}
		h.productCall(c, p, service.ProductRequest{
			Method: http.MethodDelete, Path: "/users/" + userID + "/roles/" + role,
			RawQuery: c.Request.URL.RawQuery, RawBody: raw,
		}, false)
	}
}

// accessCatalogue answers the roles, apps and role → permissions map from
// admin-service's mirror of identity's catalogue.
func (h *Handler) accessCatalogue(c *gin.Context) {
	api.JSON(c.Writer, http.StatusOK, adminauth.Catalogue(), nil)
}

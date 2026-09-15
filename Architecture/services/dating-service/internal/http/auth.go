// Caller authorization for dating-service (Dating plan lane D1).
//
// Three kinds of caller reach this service, and each is told apart here
// rather than trusted because it "came through the gateway":
//
//   - End users, proxied by the api-gateway. The gateway strips any
//     client copy of X-User-Id / X-Verified-User-Id / X-Scopes /
//     X-Admin-Role and re-derives them from the verified JWT. It also
//     injects X-Internal-Service-Key on EVERY proxied request, so the key
//     alone proves nothing about who the caller is.
//   - Admins and moderators: end users whose gateway-set X-Scopes carries
//     admin, moderator or superadmin (the gateway's scopeAllows semantics).
//     requireAdmin gates every /admin route and photo moderation on that,
//     and the audit actor is their gateway-derived X-User-Id.
//   - Sibling services calling the /v1/dating/internal family. They carry
//     no end-user identity. requireServiceCaller admits a service token
//     (X-Service-Authorization, shared/servicetoken, audience "dating") or,
//     as the LEGACY fallback, the internal key — and refuses any request
//     that carries a gateway-set user identity, because that is a user
//     request the gateway proxied with the key attached.
package http

import (
	"crypto/hmac"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Header names. The identity headers are the ones the api-gateway strips
// from clients and sets only from a verified token (trustedIdentityHeaders
// in api-gateway/cmd/server/main.go).
const (
	headerUserID         = "X-User-Id"
	headerVerifiedUserID = "X-Verified-User-Id"
	headerScopes         = "X-Scopes"
	headerAdminRole      = "X-Admin-Role"
	headerInternalKey    = "X-Internal-Service-Key"

	// ServiceAuthHeader carries a caller's service token, same header
	// payments-service reads.
	ServiceAuthHeader = "X-Service-Authorization"
)

// AudienceDating is the service-token audience dating-service accepts.
const AudienceDating = "dating"

// Operations a service token may be scoped to. Constants so a typo is a
// compile error rather than a silently refused call.
const (
	OpProfilePreview    = "dating:profile.preview"
	OpMatchFirstMessage = "dating:match.first_message"
	OpRiskRead          = "dating:risk.read"
	OpModerationScan    = "dating:moderation.scan"
)

// The /v1/dating/internal family. Paths contain "/internal/" so the
// api-gateway's requireAdminForInternalPaths gate applies to anything that
// arrives through the gateway.
const (
	InternalProfilePreviewPath = "/v1/dating/internal/profile/:userId/preview"
	InternalFirstMessagePath   = "/v1/dating/internal/matches/:id/first-message"
	InternalRiskPath           = "/v1/dating/internal/risk/:userId"
)

// Stable error codes callers can branch on.
const (
	CodeAuthRequired              = "AUTH_REQUIRED"
	CodeAdminScopeRequired        = "ADMIN_SCOPE_REQUIRED"
	CodeUserCallerRefused         = "USER_CALLER_REFUSED"
	CodeServiceCredentialRequired = "SERVICE_CREDENTIAL_REQUIRED"
	CodeServiceTokenRejected      = "SERVICE_TOKEN_REJECTED"
	CodeEndpointMoved             = "ENDPOINT_MOVED"
)

// adminScopes are the platform roles that may use the dating admin console.
// Same set the gateway admits on /internal/ paths.
var adminScopes = []string{"superadmin", "admin", "moderator"}

const (
	ctxAdminActor    = "dating_admin_actor"
	ctxServiceCaller = "dating_service_caller"
)

// hasAdminScope reports whether a space-separated scopes list holds an
// admin role. Empty list → never.
func hasAdminScope(scopes string) bool {
	for _, s := range strings.Fields(scopes) {
		for _, want := range adminScopes {
			if s == want {
				return true
			}
		}
	}
	return false
}

// requireAdmin admits a caller whose gateway-set identity is a valid user
// id AND whose gateway-set scopes include an admin role. No identity → 401;
// identity without the scope → 403 ADMIN_SCOPE_REQUIRED. The admitted user
// id is stored as the audit actor; handlers read it with adminActor.
//
// X-Admin-Id is deliberately NOT read: the gateway never sets it, so any
// value there was chosen by whoever sent the request.
func (h *Handler) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader(headerUserID))
		if raw == "" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, CodeAuthRequired, "authentication required", nil)
			c.Abort()
			return
		}
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, CodeAuthRequired, "invalid user id", nil)
			c.Abort()
			return
		}
		if !hasAdminScope(c.GetHeader(headerScopes)) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminScopeRequired, "admin or moderator scope required", nil)
			c.Abort()
			return
		}
		c.Set(ctxAdminActor, id)
		c.Next()
	}
}

// adminActor returns the admin user id requireAdmin admitted. A handler
// reached without it refuses the action (401) — an admin action with no
// accountable actor never lands.
func adminActor(c *gin.Context) (uuid.UUID, bool) {
	if v, ok := c.Get(ctxAdminActor); ok {
		if id, ok := v.(uuid.UUID); ok && id != uuid.Nil {
			return id, true
		}
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, CodeAuthRequired, "admin actor required", nil)
	return uuid.Nil, false
}

// hasGatewayIdentity reports whether the request carries any end-user
// identity header. The gateway sets these only on a request with a verified
// user token, so their presence on a service route means a proxied user.
func hasGatewayIdentity(c *gin.Context) bool {
	for _, name := range []string{headerUserID, headerVerifiedUserID, headerScopes, headerAdminRole} {
		if strings.TrimSpace(c.GetHeader(name)) != "" {
			return true
		}
	}
	return false
}

// requireServiceCaller is the middleware form of authorizeServiceCaller.
func (h *Handler) requireServiceCaller(op string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !h.authorizeServiceCaller(c, op) {
			c.Abort()
			return
		}
		c.Next()
	}
}

// authorizeServiceCaller admits a sibling-service call for one operation.
//
//  1. A request carrying a gateway-set user identity is refused (403
//     USER_CALLER_REFUSED), whatever credential it also carries.
//  2. A service token, when present, must verify for this audience and
//     operation (403 SERVICE_TOKEN_REJECTED otherwise). A token is never
//     "downgraded" to the key check.
//  3. Otherwise the internal key is the LEGACY credential. It fails closed:
//     no configured key admits nobody.
//
// Residual (documented in the D1 report): the gateway injects the internal
// key on anonymous requests too, and those carry no identity. The gateway's
// /internal/ admin gate stops them on the new paths; on the legacy preview
// path only a gateway-side auth requirement or retiring the path closes it.
func (h *Handler) authorizeServiceCaller(c *gin.Context, op string) bool {
	if hasGatewayIdentity(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeUserCallerRefused,
			"service-only endpoint; user requests are not accepted", nil)
		return false
	}
	if raw := strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer ")); raw != "" {
		if h.verifier == nil || h.verifier.Callers() == 0 {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
				"service tokens are not accepted by this deployment", nil)
			return false
		}
		v, err := h.verifier.Verify(raw, op, "")
		if err != nil {
			slog.Warn("dating: service token refused", "op", op, "reason", err.Error(), "path", c.Request.URL.Path)
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			return false
		}
		c.Set(ctxServiceCaller, v.Issuer)
		return true
	}
	if h.internalKey != "" && hmac.Equal([]byte(c.GetHeader(headerInternalKey)), []byte(h.internalKey)) {
		c.Set(ctxServiceCaller, "legacy-internal-key")
		return true
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
		"service credential required", nil)
	return false
}

// movedTo answers a retired path with 410 and a pointer to its replacement.
// Kept for one release so a stale caller fails loudly instead of 404ing.
func movedTo(newPath string) gin.HandlerFunc {
	return func(c *gin.Context) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, CodeEndpointMoved,
			"this endpoint moved to "+newPath+" and requires a service credential",
			gin.H{"moved_to": newPath})
		c.Abort()
	}
}

// WithServiceAuth installs the service-token verifier for the internal
// family. Nil leaves the legacy internal key as the only service credential.
func (h *Handler) WithServiceAuth(v *servicetoken.Verifier) *Handler {
	h.verifier = v
	return h
}

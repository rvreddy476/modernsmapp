package http

import (
	"crypto/hmac"
	"net/http"
	"strings"

	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CodeUserCallerRefused is returned when a service-only route receives a
// request carrying an end-user identity. Same code as profile-service.
const CodeUserCallerRefused = "USER_CALLER_REFUSED"

// gatewayIdentityHeaders are the end-user identity headers the api-gateway
// strips from clients and sets only from a verified user token. Their presence
// means a proxied user request, not a sibling service.
var gatewayIdentityHeaders = []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"}

// RequireServiceCallerNoUser admits a sibling-service call only:
//
//  1. NO USER IDENTITY. Any non-empty gateway identity header is refused with
//     403 USER_CALLER_REFUSED, whatever else the request carries. This matters
//     today because the gateway admits moderators to /internal/ paths and
//     stamps the internal key on their requests (admin console plan, A3).
//  2. INTERNAL KEY. Compared in constant time; an unset key admits nobody.
//
// Why the internal key and not a signed service token: shared/servicetoken
// lives in the Architecture module, which identity's GOWORK=off build cannot
// import (same constraint and same pattern as profile-service's
// requireInternalServiceCaller). The upgrade path is a token binding the
// caller's name.
func RequireServiceCallerNoUser(secret string) gin.HandlerFunc {
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
		if secret == "" || !hmac.Equal([]byte(c.GetHeader("X-Internal-Service-Key")), []byte(secret)) {
			api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "internal service key required", nil, nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

// InternalUserPermissions — GET /v1/auth/internal/users/:userId/permissions
//
//	200 {"user_id":"<uuid>","admin":{"apps":{"dating":["dating:reports.act",…]},
//	                                 "platform":["platform:users.read",…]}}
//
// Resolved live from auth.user_roles (active rows, platform-wide and
// app-scoped) plus the env allowlists — the same map GET /v1/auth/me/capabilities
// returns under "admin". Unlike capabilities, a database failure is a 503, not
// a silently smaller map: the caller is enforcing, and must not cache a
// degraded answer as the truth.
func (h *Handler) InternalUserPermissions(c *gin.Context) {
	target, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid user id", nil, nil)
		return
	}
	admin, err := h.svc.PermissionsForUser(c.Request.Context(), target)
	if err != nil {
		h.log.Error("permissions lookup failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusServiceUnavailable, "PERMISSIONS_UNAVAILABLE",
			"permissions could not be resolved", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"user_id": target.String(), "admin": admin}, nil)
}

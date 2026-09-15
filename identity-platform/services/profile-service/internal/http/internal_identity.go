package http

import (
	"crypto/hmac"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Internal identity read (dating lane D2).
//
// dating-service takes a user's first name and date of birth from identity
// instead of from the dating client. A DOB is sensitive personal data, so this
// route is for sibling services only and is closed three ways:
//
//  1. PATH. The api-gateway proxies /v1/profiles/* to this service and injects
//     X-Internal-Service-Key on EVERY proxied request, anonymous ones
//     included. A route under /v1/profiles would therefore be one the edge can
//     reach with the key already attached. InternalIdentityPath sits outside
//     every prefix in the gateway's route table, so the edge answers 404 and
//     never forwards it. It still contains "/internal/", so if a future
//     catch-all ever did forward it, the gateway's requireAdminForInternalPaths
//     would demand an admin scope, and (2) refuses every admin, who necessarily
//     carries a gateway-set identity.
//  2. NO USER IDENTITY. A request carrying any gateway-set user identity header
//     is refused (403 USER_CALLER_REFUSED) whatever credential it also holds.
//     The gateway sets those only from a verified end-user token, so their
//     presence means a proxied user request, not a service. Same rule as
//     dating-service's authorizeServiceCaller (D1).
//  3. INTERNAL KEY. The request must carry the internal key, compared in
//     constant time. An unconfigured key admits nobody, and the route is not
//     registered at all without one; outside ENV local/dev the service refuses
//     to boot without it (ResolveInternalKey).
//
// Why the internal key and not a signed service token: no identity service
// verifies service tokens today (shared/servicetoken lives in the Architecture
// module, which this module's GOWORK=off Docker build cannot import). The key
// plus (1) and (2) is what the identity platform can enforce now. A token
// would bind the caller's name and operation; that is the upgrade path.

// InternalIdentityPath is the service-only identity read.
const InternalIdentityPath = "/internal/v1/profiles/users/:userId/identity"

// CodeUserCallerRefused is returned when a service-only route receives a
// request carrying an end-user identity.
const CodeUserCallerRefused = "USER_CALLER_REFUSED"

// callerServiceHeader is an optional, self-declared label for the audit log.
// It is NOT a credential and authorizes nothing.
const callerServiceHeader = "X-Caller-Service"

// gatewayIdentityHeaders are the end-user identity headers the api-gateway
// strips from clients and sets only from a verified token
// (trustedIdentityHeaders in api-gateway/cmd/server/main.go).
var gatewayIdentityHeaders = []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"}

// requireInternalServiceCaller admits a sibling-service call: no end-user
// identity, and the configured internal key.
func requireInternalServiceCaller(secret string) gin.HandlerFunc {
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

// internalIdentityResponse is the whole wire contract. Adding a field here is
// a disclosure decision: email, phone and last name are deliberately absent.
type internalIdentityResponse struct {
	UserID    string  `json:"user_id"`
	FirstName string  `json:"first_name"`
	DoB       *string `json:"dob"`
	DoBSource string  `json:"dob_source"`
}

func toInternalIdentityResponse(b *store.IdentityBasics) internalIdentityResponse {
	out := internalIdentityResponse{
		UserID:    b.UserID.String(),
		FirstName: b.FirstName,
		DoBSource: b.DoBSource,
	}
	if b.DoB != nil {
		d := b.DoB.Format("2006-01-02")
		out.DoB = &d
	}
	return out
}

// GetInternalIdentityBasics serves InternalIdentityPath.
//
// 404 covers an unknown user AND a deactivated, deletion-scheduled, purged or
// hidden one (see store.GetIdentityBasics): the same "reads as nonexistent"
// rule as every profile surface, and it never serves a DOB for an account its
// owner has shut. A status field was rejected because it would confirm to the
// caller that a shut account exists.
func (h *Handler) GetInternalIdentityBasics(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil || userID == uuid.Nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	// Audit: caller label and user id only. Never the DOB or the name.
	h.log.Debug("internal identity read",
		"caller", callerLabel(c.GetHeader(callerServiceHeader)),
		"user_id", userID)

	b, err := h.svc.GetIdentityBasics(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("internal identity read failed", "err", err, "user_id", userID)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if b == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, toInternalIdentityResponse(b), nil)
}

var callerLabelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// callerLabel keeps a self-declared caller name out of the log unless it looks
// like a service name, so a header cannot inject log fields.
func callerLabel(raw string) string {
	if callerLabelPattern.MatchString(raw) {
		return raw
	}
	return "unlabelled"
}

// ErrInternalKeyRequired is returned by ResolveInternalKey when the service
// would otherwise boot with no credential outside local/dev.
var ErrInternalKeyRequired = errors.New("INTERNAL_SERVICE_KEY is required unless the environment is local or dev: " +
	"without it every profile route trusts a spoofable X-User-Id and the internal identity read is unavailable")

// envModeVars is consulted in order; the first non-blank one decides. Same
// variables and order as auth-service's pkg/appenv, so an explicit APP_ENV
// wins over a stale ENV left in a manifest.
var envModeVars = []string{"APP_ENV", "ENVIRONMENT", "ENV"}

// IsLocalEnv is true only for local, dev and development. A blank environment
// is NOT local, so a deployment that forgets to set one fails closed.
func IsLocalEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "dev", "development":
		return true
	}
	return false
}

// ResolveInternalKey applies the fail-closed boot rule to INTERNAL_SERVICE_KEY.
// It returns the trimmed key; or a warning when the service may boot without
// one (local/dev only); or ErrInternalKeyRequired, on which main exits.
func ResolveInternalKey(getenv func(string) string) (key, warning string, err error) {
	key = strings.TrimSpace(getenv("INTERNAL_SERVICE_KEY"))
	if key != "" {
		return key, "", nil
	}
	name, value := "", ""
	for _, k := range envModeVars {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			name, value = k, v
			break
		}
	}
	if !IsLocalEnv(value) {
		if name == "" {
			return "", "", fmt.Errorf("%w (no APP_ENV, ENVIRONMENT or ENV set)", ErrInternalKeyRequired)
		}
		return "", "", fmt.Errorf("%w (%s=%q)", ErrInternalKeyRequired, name, value)
	}
	return "", "profile-service: INTERNAL_SERVICE_KEY not set (" + name + "=" + value + ") — " +
		"every endpoint is unauthenticated and the internal identity read is not registered. Local/dev only.", nil
}

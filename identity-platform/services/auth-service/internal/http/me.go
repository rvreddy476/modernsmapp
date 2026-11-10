// Account-owner and internal account routes: the internal contact lookup,
// /me and its capabilities, the mini-app JWKS and session minting, and the
// GDPR data export. Pure move out of handler.go; behaviour is unchanged.

package http

import (
	"net/http"

	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// InternalGetUserContact returns contact fields for service-to-service use.
// Guarded by RequireInternalServiceKey — never expose to end users.
func (h *Handler) InternalGetUserContact(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil, nil)
		return
	}
	u, err := h.svc.GetUserContact(c.Request.Context(), userID)
	if err != nil || u == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "user not found", nil, nil)
		return
	}
	email := ""
	if u.Email != nil {
		email = *u.Email
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"user_id":        u.ID,
		"email":          email,
		"phone":          u.Phone,
		"phone_verified": u.PhoneVerified,
		"email_verified": u.EmailVerified,
	}, nil)
}

// Me returns the authenticated user's ID. Used by server-side proxies (e.g. chat proxy)
// to validate a bearer token and retrieve the corresponding user identity.
func (h *Handler) Me(c *gin.Context) {
	// X-User-Id is set by AuthMiddleware after validating the JWT.
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user identity", nil, nil)
		return
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user identity", nil, nil)
		return
	}
	user, err := h.svc.GetUserContact(c.Request.Context(), uid)
	if err != nil || user == nil {
		h.log.Error("account summary lookup failed", "err", err, "user_id", uid, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load account details", nil, nil)
		return
	}
	email := ""
	if user.Email != nil {
		email = *user.Email
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"id":                 userID,
		"user_id":            userID,
		"email":              email,
		"phone":              user.Phone,
		"email_verified":     user.EmailVerified,
		"phone_verified":     user.PhoneVerified,
		"two_factor_enabled": user.TwoFactorEnabled,
		"two_factor_method":  user.SecondFactorMethod(), // "email" | "totp" | "" (off)
		"account_type":       user.AccountType,
		"account_status":     user.AccountStatus,
		"age_verification":   user.AgeVerification,
		// Identity is the ecosystem's single role authority, so /me carries
		// the answer. This used to return no roles at all, which meant a
		// client had to read them out of the token (and could not see a role
		// granted since that token was minted) or ask four other services.
		//
		// Resolved live from env allowlist ∪ auth.user_roles, NOT from the
		// presented token's `scopes` claim — so a role granted a moment ago
		// shows up here immediately, even though the token still predates it.
		// Always an array, never null.
		"roles": h.svc.ResolveRoles(c.Request.Context(), uid),
		// Account control: null unless the account is deactivated /
		// pending deletion respectively. A client renders "Your account is
		// scheduled for deletion on <date>; log in to cancel" from these.
		"deactivated_at":       user.DeactivatedAt,
		"scheduled_purge_date": user.ScheduledPurgeDate,
		"last_login_at":        user.LastLoginAt,
		"created_at":           user.CreatedAt,
	}, nil)
}

// MeCapabilities returns the role-switcher payload for the authenticated user.
//
//	GET /v1/auth/me/capabilities
//	{
//	  "user_id": "…",
//	  "roles": ["moderator","seller"],
//	  "is_customer": true,
//	  "capabilities": {
//	    "superadmin": false, "admin": false, "moderator": true,
//	    "seller": true, "restaurant_owner": false,
//	    "delivery_partner": false, "rider_partner": false,
//	    "service_professional": false
//	  },
//	  "switcher": [
//	    {"role":"customer","label":"Customer"},
//	    {"role":"moderator","label":"Moderator"},
//	    {"role":"seller","label":"Seller"}
//	  ]
//	}
//
// `capabilities` names every role in the vocabulary explicitly, so a client
// never has to know which roles exist to render "you are not a seller", and
// adding a role to the vocabulary reaches clients without a shape change.
//
// Read live from identity's own roles. Deliberately NOT derived from other
// services' partner tables, which is the design this replaces — see
// food-service's GET /v1/food/me/capabilities.
func (h *Handler) MeCapabilities(c *gin.Context) {
	uid, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user identity", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, h.svc.CapabilitiesForUser(c.Request.Context(), uid), nil)
}

func (h *Handler) MiniAppJWKS(c *gin.Context) {
	jwks, err := h.svc.MiniAppJWKS(c.Request.Context())
	if err != nil {
		api.Error(c.Writer, http.StatusServiceUnavailable, "JWKS_UNAVAILABLE", "Mini app JWKS is not configured", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, jwks, nil)
}

type CreateMiniAppSessionRequest struct {
	AppID              string   `json:"app_id" binding:"required"`
	UserID             string   `json:"user_id" binding:"required"`
	GrantedPermissions []string `json:"granted_permissions"`
}

func (h *Handler) CreateMiniAppSession(c *gin.Context) {
	var req CreateMiniAppSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	appID, err := uuid.Parse(req.AppID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid app_id", nil, nil)
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid user_id", nil, nil)
		return
	}

	resp, err := h.svc.IssueMiniAppSession(c.Request.Context(), appID, userID, req.GrantedPermissions)
	if err != nil {
		if err.Error() == "MINI_APP_SESSION_UNAVAILABLE" {
			api.Error(c.Writer, http.StatusServiceUnavailable, "SESSION_UNAVAILABLE", "Mini app session issuance is not configured", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to issue mini app session", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// DeleteAccount and DeactivateAccount live in account_control.go.

// ExportUserData returns all personal data held by the auth service for the
// requesting user as a downloadable JSON file (GDPR data portability).
func (h *Handler) ExportUserData(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "UNAUTHORIZED"}})
		return
	}
	export, err := h.svc.ExportUserData(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("export user data failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "INTERNAL_ERROR", "message": "Could not export your data. Try again later."}})
		return
	}
	c.Header("Content-Disposition", "attachment; filename=data-export.json")
	c.JSON(http.StatusOK, export)
}

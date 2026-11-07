// Session routes: refresh, logout and logout-all, listing and revoking
// sessions, the owner's login anomalies, and trusted devices. Pure move out
// of handler.go; behaviour is unchanged.

package http

import (
	"net/http"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) Refresh(c *gin.Context) {
	// Try cookie first, then fall back to JSON body (for mobile clients)
	refreshToken, _ := c.Cookie(refreshTokenCookieName)
	if refreshToken == "" {
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := c.ShouldBindJSON(&body); err == nil && body.RefreshToken != "" {
			refreshToken = body.RefreshToken
		}
	}
	if refreshToken == "" {
		h.log.Warn("missing refresh token", "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing refresh token", nil, nil)
		return
	}

	resp, err := h.svc.RefreshSession(c.Request.Context(), refreshToken, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		h.log.Warn("refresh failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "Authentication failed", nil, nil)
		return
	}

	h.setAuthCookies(c, resp.Tokens)
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// Logout revokes the session named by the refresh token. Web clients carry it
// in the refresh_token cookie; mobile clients send {"refresh_token": "..."} in
// the body, as /refresh accepts, because an app's cookie jar does not survive
// a process restart and logout would otherwise revoke nothing server-side.
// When both are present and differ, both are revoked: each is a credential the
// caller holds and wants dead. A missing or unknown token is still 200.
func (h *Handler) Logout(c *gin.Context) {
	cookieToken, _ := c.Cookie(refreshTokenCookieName)
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if c.Request.ContentLength != 0 {
		_ = c.ShouldBindJSON(&body)
	}
	for _, refreshToken := range uniqueNonEmpty(cookieToken, body.RefreshToken) {
		if err := h.svc.Logout(c.Request.Context(), refreshToken); err != nil {
			h.log.Error("logout failed", "err", err, "request_id", RequestIDFromContext(c))
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
			return
		}
	}
	h.clearAuthCookies(c)
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

// --- Protected endpoints ---

func (h *Handler) LogoutAll(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	count, err := h.svc.LogoutAll(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("logout-all failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok", "sessions_revoked": count}, nil)
}

func (h *Handler) ListSessions(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	sessions, err := h.svc.ListSessions(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("list sessions failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	// Scrub refresh token hashes from response
	type sessionResponse struct {
		ID        uuid.UUID `json:"id"`
		DeviceID  string    `json:"device_id"`
		Platform  string    `json:"platform"`
		IP        string    `json:"ip"`
		UserAgent string    `json:"user_agent"`
		CreatedAt time.Time `json:"created_at"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	result := make([]sessionResponse, 0, len(sessions))
	for _, s := range sessions {
		result = append(result, sessionResponse{
			ID:        s.ID,
			DeviceID:  s.DeviceID,
			Platform:  s.Platform,
			IP:        s.IP,
			UserAgent: s.UserAgent,
			CreatedAt: s.CreatedAt,
			ExpiresAt: s.ExpiresAt,
		})
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) RevokeSessionByID(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid session ID", nil, nil)
		return
	}

	if err := h.svc.RevokeSessionByID(c.Request.Context(), userID, sessionID); err != nil {
		h.log.Warn("revoke session failed", "err", err, "user_id", userID, "session_id", sessionID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Session not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

// ListMyAnomalies renders the user-facing security inbox — every
// detection (new IP, new device, refresh-fingerprint mismatch) that
// fired against this user's account. Read-only; the inbox shows the
// last 20 by default.
func (h *Handler) ListMyAnomalies(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}
	anomalies, err := h.svc.ListMyAnomalies(c.Request.Context(), userID, 20)
	if err != nil {
		h.log.Error("list anomalies failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if anomalies == nil {
		anomalies = []store.LoginAnomaly{}
	}
	api.JSON(c.Writer, http.StatusOK, anomalies, nil)
}

// AcknowledgeMyAnomaly clears an inbox entry. Used by the security UI
// after the user has reviewed an alert and confirmed it was them.
// Idempotent: already-acknowledged rows no-op.
func (h *Handler) AcknowledgeMyAnomaly(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}
	anomalyID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid anomaly ID", nil, nil)
		return
	}
	if err := h.svc.AcknowledgeMyAnomaly(c.Request.Context(), userID, anomalyID); err != nil {
		h.log.Warn("ack anomaly failed", "err", err, "user_id", userID, "anomaly_id", anomalyID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

// --- Trusted Devices ---

func (h *Handler) ListTrustedDevices(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	devices, err := h.svc.ListTrustedDevices(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("list trusted devices failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, devices, nil)
}

func (h *Handler) RemoveTrustedDevice(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid device ID", nil, nil)
		return
	}

	if err := h.svc.RemoveTrustedDevice(c.Request.Context(), userID, deviceID); err != nil {
		h.log.Warn("remove trusted device failed", "err", err, "user_id", userID, "device_id", deviceID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Device not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

type TrustDeviceRequest struct {
	Fingerprint string  `json:"fingerprint" binding:"required"`
	DeviceName  *string `json:"device_name"`
}

func (h *Handler) TrustDevice(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	var req TrustDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.TrustDevice(c.Request.Context(), userID, req.Fingerprint, req.DeviceName); err != nil {
		h.log.Error("trust device failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok", "message": "Device trusted"}, nil)
}

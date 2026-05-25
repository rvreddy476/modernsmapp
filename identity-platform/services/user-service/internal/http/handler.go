package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/atpost/identity-shared/api"
	"github.com/atpost/identity-user-service/internal/service"
	"github.com/atpost/identity-user-service/internal/store"
)

type Handler struct {
	svc         UserService
	log         *slog.Logger
	internalKey string
}

type UserService interface {
	GetUser(ctx context.Context, id uuid.UUID) (*store.User, error)
	ListUsers(ctx context.Context, limit, offset int) ([]store.User, int, error)
	GetSettings(ctx context.Context, id uuid.UUID) (*store.UserSettings, error)
	UpdateSettings(ctx context.Context, settings *store.UserSettings) (*store.UserSettings, error)
}

func New(svc UserService, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{svc: svc, log: logger}
}

// WithInternalKey enables the X-Internal-Service-Key gate on every
// gated /v1/users/* route. Audit UC1: without this, X-User-Id was a
// trust-the-caller header that any direct connection could spoof.
func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine, auth gin.HandlerFunc, csrf gin.HandlerFunc) {
	if h.internalKey != "" {
		r.Use(RequireInternalServiceKey(h.internalKey))
	}
	v1 := r.Group("/v1/users")
	{
		v1.GET("", h.ListUsers)
		v1.GET("/:userId", h.GetUser)
		// Internal-key gated (no user JWT): lets the permission resolver
		// in graph-service read any target user's privacy settings.
		v1.GET("/:userId/settings", h.GetUserSettings)
		v1.GET("/health", h.Health)
	}

	protected := v1.Group("")
	protected.Use(auth)
	{
		protected.GET("/me", h.GetMe)
		protected.GET("/me/settings", h.GetMySettings)
		protected.PUT("/me/settings", csrf, h.UpdateMySettings)
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) ListUsers(c *gin.Context) {
	limit := 50
	offset := 0

	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}
	if o := c.Query("offset"); o != "" {
		// UH1: cap offset to 10k. Without an upper bound a client can
		// drive a sequential scan over the entire users table by
		// requesting offset=50000000, costing PG seconds per request
		// even when limit is bounded. Real pagination should migrate
		// to keyset/cursor (tracked separately).
		if v, err := strconv.Atoi(o); err == nil && v >= 0 && v <= 10000 {
			offset = v
		}
	}

	users, total, err := h.svc.ListUsers(c.Request.Context(), limit, offset)
	if err != nil {
		h.log.Error("failed to list users", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"users":  users,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}, nil)
}

func (h *Handler) GetUser(c *gin.Context) {
	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.log.Warn("invalid user id", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	u, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch user", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if u == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetMe(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	if userIDStr == "" {
		h.log.Warn("missing user id header", "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil, nil)
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.log.Warn("invalid user id header", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	u, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch user", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetMySettings(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.log.Warn("invalid user id header", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	s, err := h.svc.GetSettings(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch settings", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, s, nil)
}

// GetUserSettings returns any user's privacy settings. Internal-key gated —
// it sits in the v1 group, not the JWT-protected group, so service callers
// (graph-service's permission resolver) can read a target's settings.
func (h *Handler) GetUserSettings(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	s, err := h.svc.GetSettings(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch settings", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if s == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Settings not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, s, nil)
}

// updateSettingsRequest is a partial-update (PATCH-style) payload: every field
// is a pointer, and only the fields present in the JSON body are applied.
// under_18_mode and privacy_version are deliberately absent — both are
// server-controlled (spec §5.4) and cannot be set by the user.
type updateSettingsRequest struct {
	AccountVisibility             *string `json:"account_visibility"`
	AllowMessagesFrom             *string `json:"allow_messages_from"`
	AllowCommentsFrom             *string `json:"allow_comments_from"`
	WhoCanMessage                 *string `json:"who_can_message"`
	WhoCanSendConnectionRequest   *string `json:"who_can_send_connection_request"`
	WhoCanCall                    *string `json:"who_can_call"`
	WhoCanAddToGroups             *string `json:"who_can_add_to_groups"`
	WhoCanSeeOnlineStatus         *string `json:"who_can_see_online_status"`
	WhoCanSeeReadReceipts         *string `json:"who_can_see_read_receipts"`
	WhoCanSeeLastSeen             *string `json:"who_can_see_last_seen"`
	WhoCanSeeProfilePhoto         *string `json:"who_can_see_profile_photo"`
	AllowPhoneDiscovery           *bool   `json:"allow_phone_discovery"`
	AllowContactSyncMatch         *bool   `json:"allow_contact_sync_match"`
	DiscoverableByPhoneToContacts *bool   `json:"discoverable_by_phone_to_contacts"`
	StrictPrivacyMode             *bool   `json:"strict_privacy_mode"`
	BlockUnknownCalls             *bool   `json:"block_unknown_calls"`
	AutoFilterAbusiveContent      *bool   `json:"auto_filter_abusive_content"`

	// Trusted Circle per-feature toggles (friends-sheets spec §3.3).
	TcCloseFriendsPosts *bool `json:"tc_close_friends_posts"`
	TcLocationPings     *bool `json:"tc_location_pings"`
	TcAfterHoursPosts   *bool `json:"tc_after_hours_posts"`
	TcAudioRoomInvite   *bool `json:"tc_audio_room_invite"`
}

// applyTo merges the present fields of the request onto cur.
func (r *updateSettingsRequest) applyTo(cur *store.UserSettings) {
	if r.AccountVisibility != nil {
		cur.AccountVisibility = *r.AccountVisibility
	}
	if r.AllowMessagesFrom != nil {
		cur.AllowMessagesFrom = *r.AllowMessagesFrom
	}
	if r.AllowCommentsFrom != nil {
		cur.AllowCommentsFrom = *r.AllowCommentsFrom
	}
	if r.WhoCanMessage != nil {
		cur.WhoCanMessage = *r.WhoCanMessage
	}
	if r.WhoCanSendConnectionRequest != nil {
		cur.WhoCanSendConnectionRequest = *r.WhoCanSendConnectionRequest
	}
	if r.WhoCanCall != nil {
		cur.WhoCanCall = *r.WhoCanCall
	}
	if r.WhoCanAddToGroups != nil {
		cur.WhoCanAddToGroups = *r.WhoCanAddToGroups
	}
	if r.WhoCanSeeOnlineStatus != nil {
		cur.WhoCanSeeOnlineStatus = *r.WhoCanSeeOnlineStatus
	}
	if r.WhoCanSeeReadReceipts != nil {
		cur.WhoCanSeeReadReceipts = *r.WhoCanSeeReadReceipts
	}
	if r.WhoCanSeeLastSeen != nil {
		cur.WhoCanSeeLastSeen = *r.WhoCanSeeLastSeen
	}
	if r.WhoCanSeeProfilePhoto != nil {
		cur.WhoCanSeeProfilePhoto = *r.WhoCanSeeProfilePhoto
	}
	if r.AllowPhoneDiscovery != nil {
		cur.AllowPhoneDiscovery = *r.AllowPhoneDiscovery
	}
	if r.AllowContactSyncMatch != nil {
		cur.AllowContactSyncMatch = *r.AllowContactSyncMatch
	}
	if r.DiscoverableByPhoneToContacts != nil {
		cur.DiscoverableByPhoneToContacts = *r.DiscoverableByPhoneToContacts
	}
	if r.StrictPrivacyMode != nil {
		cur.StrictPrivacyMode = *r.StrictPrivacyMode
	}
	if r.BlockUnknownCalls != nil {
		cur.BlockUnknownCalls = *r.BlockUnknownCalls
	}
	if r.AutoFilterAbusiveContent != nil {
		cur.AutoFilterAbusiveContent = *r.AutoFilterAbusiveContent
	}
	if r.TcCloseFriendsPosts != nil {
		cur.TcCloseFriendsPosts = *r.TcCloseFriendsPosts
	}
	if r.TcLocationPings != nil {
		cur.TcLocationPings = *r.TcLocationPings
	}
	if r.TcAfterHoursPosts != nil {
		cur.TcAfterHoursPosts = *r.TcAfterHoursPosts
	}
	if r.TcAudioRoomInvite != nil {
		cur.TcAudioRoomInvite = *r.TcAudioRoomInvite
	}
}

func (h *Handler) UpdateMySettings(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req updateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	// Read-modify-write: load current settings, merge the patch, persist.
	cur, err := h.svc.GetSettings(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch settings", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if cur == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Settings not found", nil, nil)
		return
	}
	req.applyTo(cur)
	cur.UserID = userID

	s, err := h.svc.UpdateSettings(c.Request.Context(), cur)
	if err != nil {
		if errors.Is(err, service.ErrInvalidPrivacySetting) {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
			return
		}
		h.log.Error("failed to update settings", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, s, nil)
}

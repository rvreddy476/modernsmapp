package http

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/chat-call-service/internal/service"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler handles HTTP requests for the call service.
type Handler struct {
	svc          *service.Service
	log          *slog.Logger
	callsEnabled bool
	// internalServiceKey gates /internal/v1/calls (service-to-service
	// writes such as the AI call summary). Empty refuses every internal
	// request: there is no unauthenticated fallback.
	internalServiceKey string
}

func New(svc *service.Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log, callsEnabled: false}
}

func (h *Handler) WithCallsEnabled(enabled bool) *Handler {
	h.callsEnabled = enabled
	return h
}

// WithInternalServiceKey wires the shared X-Internal-Service-Key that the
// /internal/v1/calls routes require.
func (h *Handler) WithInternalServiceKey(key string) *Handler {
	h.internalServiceKey = strings.TrimSpace(key)
	return h
}

// requireInternalKey refuses any request that does not carry the configured
// internal service key. The api-gateway never routes /internal/* from the
// edge (internalroutes.IsInternalPath), so this is reached only in-cluster;
// the key check makes a user JWT alone worthless here even so.
func (h *Handler) requireInternalKey(c *gin.Context) {
	if !internalKeyMatches(h.internalServiceKey, c.GetHeader(internalServiceKeyHeader)) {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "internal service key required", nil, nil)
		c.Abort()
		return
	}
	c.Next()
}

// internalKeyMatches is a constant-time comparison that never accepts an
// unconfigured (empty) key.
func internalKeyMatches(configured, presented string) bool {
	if configured == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(configured)) == 1
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	r.GET("/health", h.Health)

	v1 := r.Group("/v1/calls")
	v1.Use(func(c *gin.Context) {
		if !h.callsEnabled {
			api.Error(c.Writer, http.StatusServiceUnavailable, "CALLS_DISABLED", "Calls are not available in this release", nil, nil)
			c.Abort()
			return
		}
		c.Next()
	})
	{
		v1.POST("", h.CreateCall)
		v1.GET("/history", h.GetCallHistory)
		// The callee's discovery surface: pending invitations WITH invite
		// ids (accept/decline need them; nothing else client-reachable
		// carried them). Also how a push-tap cold start finds the ring.
		v1.GET("/invites/pending", h.ListPendingInvites)
		v1.GET("/:callId", h.GetCall)
		v1.POST("/:callId/join", h.JoinCall)
		v1.POST("/:callId/invites/:inviteId/accept", h.AcceptInvite)
		v1.POST("/:callId/invites/:inviteId/decline", h.DeclineInvite)
		v1.POST("/:callId/leave", h.LeaveCall)
		v1.POST("/:callId/end", h.EndCall)
		v1.POST("/:callId/participants/invite", h.InviteParticipants)
		v1.POST("/:callId/participants/:userId/mute", h.MuteParticipant)
		v1.POST("/:callId/participants/:userId/remove", h.RemoveParticipant)
		v1.PATCH("/:callId/upgrade", h.UpgradeCall)

		// Call links
		v1.POST("/link", h.GenerateCallLink)
		v1.POST("/join-by-link", h.JoinCallByLink)

		// Scheduled calls
		v1.POST("/schedule", h.ScheduleCall)
		v1.GET("/scheduled", h.ListScheduledCalls)

		// Reminders
		v1.POST("/:callId/reminders", h.SetCallReminder)
		v1.DELETE("/:callId/reminders", h.DeleteCallReminder)

		// AI summaries. Participants read; only the AI pipeline writes, and
		// it writes through the internal group below (audit H1: a user-facing
		// POST let any caller forge the "AI summary" another participant sees).
		v1.GET("/:callId/summary", h.GetCallSummary)
	}

	// Service-only routes. Not gated on callsEnabled: a summary for a call
	// that already happened stays writable while the kill switch is off.
	internal := r.Group("/internal/v1/calls")
	internal.Use(h.requireInternalKey)
	{
		internal.POST("/:callId/summary", h.CreateCallSummary)
	}
}

func (h *Handler) Health(c *gin.Context) {
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

// CreateCall handles POST /v1/calls
func (h *Handler) CreateCall(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	var req struct {
		CallType        string   `json:"call_type" binding:"required"`
		SourceType      string   `json:"source_type" binding:"required"`
		SourceID        *string  `json:"source_id"`
		TargetUserIDs   []string `json:"target_user_ids" binding:"required"`
		AudioOnly       bool     `json:"audio_only"`
		MaxParticipants int      `json:"max_participants"`
		IdempotencyKey  string   `json:"idempotency_key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	// Parse target user IDs
	targetIDs := make([]uuid.UUID, 0, len(req.TargetUserIDs))
	for _, idStr := range req.TargetUserIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid target_user_id: "+idStr, nil, nil)
			return
		}
		targetIDs = append(targetIDs, id)
	}

	var sourceID *uuid.UUID
	if req.SourceID != nil && *req.SourceID != "" {
		id, err := uuid.Parse(*req.SourceID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid source_id format", nil, nil)
			return
		}
		sourceID = &id
	}

	result, err := h.svc.CreateCall(c.Request.Context(), userID, service.CreateCallRequest{
		CallType:        req.CallType,
		SourceType:      req.SourceType,
		SourceID:        sourceID,
		TargetUserIDs:   targetIDs,
		AudioOnly:       req.AudioOnly,
		MaxParticipants: req.MaxParticipants,
		IdempotencyKey:  req.IdempotencyKey,
	})
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, result, nil)
}

// ListPendingInvites handles GET /v1/calls/invites/pending
func (h *Handler) ListPendingInvites(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	result, err := h.svc.ListPendingInvites(c.Request.Context(), userID)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// GetCall handles GET /v1/calls/:callId
func (h *Handler) GetCall(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}

	result, err := h.svc.GetCall(c.Request.Context(), userID, callID)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// JoinCall handles POST /v1/calls/:callId/join
func (h *Handler) JoinCall(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}

	result, err := h.svc.JoinCall(c.Request.Context(), userID, callID)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// AcceptInvite handles POST /v1/calls/:callId/invites/:inviteId/accept
func (h *Handler) AcceptInvite(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}
	inviteID, err := uuid.Parse(c.Param("inviteId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid invite ID", nil, nil)
		return
	}

	if err := h.svc.AcceptInvite(c.Request.Context(), userID, callID, inviteID); err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "accepted"}, nil)
}

// DeclineInvite handles POST /v1/calls/:callId/invites/:inviteId/decline
func (h *Handler) DeclineInvite(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}
	inviteID, err := uuid.Parse(c.Param("inviteId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid invite ID", nil, nil)
		return
	}

	if err := h.svc.DeclineInvite(c.Request.Context(), userID, callID, inviteID); err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "declined"}, nil)
}

// LeaveCall handles POST /v1/calls/:callId/leave
func (h *Handler) LeaveCall(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}

	if err := h.svc.LeaveCall(c.Request.Context(), userID, callID); err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "left"}, nil)
}

// EndCall handles POST /v1/calls/:callId/end
func (h *Handler) EndCall(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}

	if err := h.svc.EndCall(c.Request.Context(), userID, callID); err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ended"}, nil)
}

// InviteParticipants handles POST /v1/calls/:callId/participants/invite
func (h *Handler) InviteParticipants(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}

	var req struct {
		UserIDs []string `json:"user_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	targetIDs := make([]uuid.UUID, 0, len(req.UserIDs))
	for _, idStr := range req.UserIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid user_id: "+idStr, nil, nil)
			return
		}
		targetIDs = append(targetIDs, id)
	}

	result, err := h.svc.InviteParticipants(c.Request.Context(), userID, callID, service.InviteParticipantsRequest{
		UserIDs: targetIDs,
	})
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// MuteParticipant handles POST /v1/calls/:callId/participants/:userId/mute
func (h *Handler) MuteParticipant(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}
	targetUserID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid user ID", nil, nil)
		return
	}

	if err := h.svc.MuteParticipant(c.Request.Context(), userID, callID, targetUserID); err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "muted"}, nil)
}

// RemoveParticipant handles POST /v1/calls/:callId/participants/:userId/remove
func (h *Handler) RemoveParticipant(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}
	targetUserID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid user ID", nil, nil)
		return
	}

	if err := h.svc.RemoveParticipant(c.Request.Context(), userID, callID, targetUserID); err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

// GetCallHistory handles GET /v1/calls/history
func (h *Handler) GetCallHistory(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}
	cursor := c.Query("cursor")

	items, nextCursor, err := h.svc.GetCallHistory(c.Request.Context(), userID, limit, cursor)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}

	api.JSON(c.Writer, http.StatusOK, items, meta)
}

// UpgradeCall handles PATCH /v1/calls/:callId/upgrade
func (h *Handler) UpgradeCall(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}

	if err := h.svc.UpgradeCall(c.Request.Context(), userID, callID); err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "upgraded"}, nil)
}

// handleServiceError maps service errors to HTTP responses.
func (h *Handler) handleServiceError(c *gin.Context, err error) {
	switch err {
	case service.ErrCallNotFound, service.ErrInviteNotFound:
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
	case service.ErrNotParticipant, service.ErrNotHost, service.ErrGroupCallsDisabled:
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
	case service.ErrCallAlreadyEnded, service.ErrAlreadyInCall, service.ErrAlreadyJoined,
		service.ErrInviteNotPending, service.ErrCannotInviteSelf, service.ErrCallNotActive,
		service.ErrAlreadyAudioVideo, service.ErrMaxParticipants, service.ErrMaxInvitesPerCall,
		service.ErrTargetUnavailable:
		api.Error(c.Writer, http.StatusConflict, "CONFLICT", err.Error(), nil, nil)
	case service.ErrCallRateLimitExceeded, service.ErrInviteRateLimitExceeded,
		service.ErrJoinRateLimitExceeded, service.ErrRingAntiSpam, service.ErrLinkLookupRateExceeded:
		api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMIT", err.Error(), nil, nil)
	case service.ErrLobbyNotSupported:
		api.Error(c.Writer, http.StatusBadRequest, "LOBBY_NOT_SUPPORTED", err.Error(), nil, nil)
	case service.ErrCallNotAllowed:
		// ONE generic refusal for block/privacy/circle — distinguishing
		// them would leak the callee's block state. Previously these
		// policy errors fell through to the default 500.
		api.Error(c.Writer, http.StatusForbidden, "CALL_NOT_ALLOWED",
			"Calling this user is not permitted", nil, nil)
	case service.ErrGraphUnavailable:
		api.Error(c.Writer, http.StatusServiceUnavailable, "TEMPORARILY_UNAVAILABLE",
			"Please try again shortly", nil, nil)
	default:
		h.log.Error("unhandled service error", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
	}
}

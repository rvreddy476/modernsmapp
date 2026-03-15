package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/chat-call-service/internal/service"
	"github.com/atpost/chat-call-service/internal/store/postgres"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GenerateCallLink handles POST /v1/calls/link
func (h *Handler) GenerateCallLink(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	var req struct {
		CallSessionID string `json:"call_session_id" binding:"required"`
		ExpiresInHours int   `json:"expires_in_hours"`
		LobbyEnabled  bool   `json:"lobby_enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	callSessionID, err := uuid.Parse(req.CallSessionID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call_session_id", nil, nil)
		return
	}

	result, err := h.svc.GenerateCallLink(c.Request.Context(), userID, callSessionID, req.ExpiresInHours, req.LobbyEnabled)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, result, nil)
}

// JoinCallByLink handles POST /v1/calls/join-by-link
func (h *Handler) JoinCallByLink(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	result, err := h.svc.JoinCallByLink(c.Request.Context(), userID, req.Token)
	if err != nil {
		h.handleExtrasError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// ScheduleCall handles POST /v1/calls/schedule
func (h *Handler) ScheduleCall(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	var req struct {
		CallType      string   `json:"call_type" binding:"required"`
		SourceType    string   `json:"source_type" binding:"required"`
		SourceID      string   `json:"source_id"`
		ScheduledAt   string   `json:"scheduled_at" binding:"required"`
		InviteUserIDs []string `json:"invite_user_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	scheduledAt, err := time.Parse(time.RFC3339, req.ScheduledAt)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid scheduled_at format, use RFC3339", nil, nil)
		return
	}

	inviteIDs := make([]uuid.UUID, 0, len(req.InviteUserIDs))
	for _, idStr := range req.InviteUserIDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid invite_user_id: "+idStr, nil, nil)
			return
		}
		inviteIDs = append(inviteIDs, id)
	}

	result, err := h.svc.ScheduleCall(c.Request.Context(), userID, req.CallType, req.SourceType, req.SourceID, scheduledAt, inviteIDs)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, result, nil)
}

// ListScheduledCalls handles GET /v1/calls/scheduled
func (h *Handler) ListScheduledCalls(c *gin.Context) {
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

	results, err := h.svc.ListScheduledCalls(c.Request.Context(), userID, limit)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, results, nil)
}

// SetCallReminder handles POST /v1/calls/:callId/reminders
func (h *Handler) SetCallReminder(c *gin.Context) {
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
		RemindAt string `json:"remind_at" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	remindAt, err := time.Parse(time.RFC3339, req.RemindAt)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid remind_at format, use RFC3339", nil, nil)
		return
	}

	result, err := h.svc.SetCallReminder(c.Request.Context(), userID, callID, remindAt)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, result, nil)
}

// DeleteCallReminder handles DELETE /v1/calls/:callId/reminders
func (h *Handler) DeleteCallReminder(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}

	if err := h.svc.DeleteCallReminder(c.Request.Context(), userID, callID); err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

// GetCallSummary handles GET /v1/calls/:callId/summary
func (h *Handler) GetCallSummary(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}

	result, err := h.svc.GetCallSummary(c.Request.Context(), userID, callID)
	if err != nil {
		h.handleExtrasError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// CreateCallSummary handles POST /v1/calls/:callId/summary
func (h *Handler) CreateCallSummary(c *gin.Context) {
	_, ok := getUserID(c, h.log)
	if !ok {
		return
	}

	callID, err := uuid.Parse(c.Param("callId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid call ID", nil, nil)
		return
	}

	var req struct {
		TranscriptURL *string         `json:"transcript_url"`
		SummaryText   *string         `json:"summary_text"`
		KeyPoints     json.RawMessage `json:"key_points"`
		ActionItems   json.RawMessage `json:"action_items"`
		Participants  []string        `json:"participants"`
		DurationMs    *int            `json:"duration_ms"`
		Language      string          `json:"language"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}

	participants := make([]uuid.UUID, 0, len(req.Participants))
	for _, idStr := range req.Participants {
		id, err := uuid.Parse(idStr)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid participant UUID: "+idStr, nil, nil)
			return
		}
		participants = append(participants, id)
	}

	lang := req.Language
	if lang == "" {
		lang = "en"
	}

	sum := &postgres.CallSummary{
		CallSessionID: callID,
		TranscriptURL: req.TranscriptURL,
		SummaryText:   req.SummaryText,
		KeyPoints:     req.KeyPoints,
		ActionItems:   req.ActionItems,
		Participants:  participants,
		DurationMs:    req.DurationMs,
		Language:      lang,
	}

	result, err := h.svc.CreateCallSummary(c.Request.Context(), sum)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, result, nil)
}

// handleExtrasError handles errors specific to the extras handlers.
func (h *Handler) handleExtrasError(c *gin.Context, err error) {
	switch err {
	case service.ErrLinkNotFound, service.ErrSummaryNotFound:
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
	default:
		h.handleServiceError(c, err)
	}
}

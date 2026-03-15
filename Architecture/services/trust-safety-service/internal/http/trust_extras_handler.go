package http

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Appeals ──────────────────────────────────────────────────────────────────

type submitAppealRequest struct {
	ContentType string `json:"content_type" binding:"required"`
	ContentID   string `json:"content_id" binding:"required"`
	ActionTaken string `json:"action_taken" binding:"required"`
	AppealReason string `json:"appeal_reason" binding:"required"`
}

func (h *Handler) SubmitAppeal(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	var req submitAppealRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	appeal, err := h.svc.SubmitAppeal(c.Request.Context(), userID, req.ContentType, req.ContentID, req.ActionTaken, req.AppealReason)
	if err != nil {
		slog.Error("SubmitAppeal", "err", err)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, appeal, nil)
}

func (h *Handler) AdminListAppeals(c *gin.Context) {
	if !hasScope(c.GetHeader("X-Scopes"), "admin") {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil, nil)
		return
	}
	status := c.Query("status")
	limit, offset := paginate(c)
	appeals, err := h.svc.ListAppeals(c.Request.Context(), status, limit, offset)
	if err != nil {
		slog.Error("ListAppeals", "err", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list appeals", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": appeals}, nil)
}

type reviewAppealRequest struct {
	Status string `json:"status" binding:"required"`
	Note   string `json:"note"`
}

func (h *Handler) ReviewAppeal(c *gin.Context) {
	if !hasScope(c.GetHeader("X-Scopes"), "admin") {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil, nil)
		return
	}
	reviewerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid reviewer ID", nil, nil)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid appeal ID", nil, nil)
		return
	}
	var req reviewAppealRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	if err := h.svc.ReviewAppeal(c.Request.Context(), id, req.Status, req.Note, reviewerID); err != nil {
		slog.Error("ReviewAppeal", "err", err)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

// ─── Keyword filters ──────────────────────────────────────────────────────────

type addKeywordFilterRequest struct {
	Scope   string  `json:"scope" binding:"required"`
	ScopeID *string `json:"scope_id,omitempty"`
	Keyword string  `json:"keyword" binding:"required"`
	Action  string  `json:"action"`
}

func (h *Handler) AddKeywordFilter(c *gin.Context) {
	if !hasScope(c.GetHeader("X-Scopes"), "admin") {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil, nil)
		return
	}
	addedBy, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	var req addKeywordFilterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	if req.Action == "" {
		req.Action = "hide"
	}
	var scopeID *uuid.UUID
	if req.ScopeID != nil {
		id, err := uuid.Parse(*req.ScopeID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid scope_id", nil, nil)
			return
		}
		scopeID = &id
	}
	filter, err := h.svc.AddKeywordFilter(c.Request.Context(), req.Scope, scopeID, req.Keyword, req.Action, addedBy)
	if err != nil {
		slog.Error("AddKeywordFilter", "err", err)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, filter, nil)
}

func (h *Handler) GetKeywordFilters(c *gin.Context) {
	scope := c.Query("scope")
	if scope == "" {
		scope = "platform"
	}
	var scopeID *uuid.UUID
	if sid := c.Query("scope_id"); sid != "" {
		id, err := uuid.Parse(sid)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid scope_id", nil, nil)
			return
		}
		scopeID = &id
	}
	filters, err := h.svc.GetKeywordFilters(c.Request.Context(), scope, scopeID)
	if err != nil {
		slog.Error("GetKeywordFilters", "err", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get keyword filters", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": filters}, nil)
}

// ─── Teen accounts ────────────────────────────────────────────────────────────

type upsertTeenAccountRequest struct {
	GuardianID     *string `json:"guardian_id,omitempty"`
	DailyLimitMins int     `json:"daily_limit_mins"`
	ContentFilter  string  `json:"content_filter"`
	DMRestricted   bool    `json:"dm_restricted"`
	FollowerApproval bool  `json:"follower_approval"`
	LocationHidden bool    `json:"location_hidden"`
}

func (h *Handler) UpsertTeenAccount(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	var req upsertTeenAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	if req.ContentFilter == "" {
		req.ContentFilter = "strict"
	}
	if req.DailyLimitMins == 0 {
		req.DailyLimitMins = 60
	}

	ta := buildTeenAccount(userID, req)
	if err := h.svc.UpsertTeenAccount(c.Request.Context(), ta); err != nil {
		slog.Error("UpsertTeenAccount", "err", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to upsert teen account", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, ta, nil)
}

func (h *Handler) GetTeenAccount(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user ID", nil, nil)
		return
	}
	ta, err := h.svc.GetTeenAccount(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Teen account not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, ta, nil)
}

// ─── Media labels ─────────────────────────────────────────────────────────────

type addMediaLabelRequest struct {
	MediaAssetID string  `json:"media_asset_id" binding:"required"`
	LabelType    string  `json:"label_type" binding:"required"`
	Confidence   float32 `json:"confidence" binding:"required"`
	Source       string  `json:"source" binding:"required"`
}

func (h *Handler) AddMediaLabel(c *gin.Context) {
	if !hasScope(c.GetHeader("X-Scopes"), "admin") {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil, nil)
		return
	}
	var req addMediaLabelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	mediaAssetID, err := uuid.Parse(req.MediaAssetID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media_asset_id", nil, nil)
		return
	}
	label, err := h.svc.AddMediaLabel(c.Request.Context(), mediaAssetID, req.LabelType, req.Confidence, req.Source)
	if err != nil {
		slog.Error("AddMediaLabel", "err", err)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, label, nil)
}

func (h *Handler) GetMediaLabels(c *gin.Context) {
	mediaAssetID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}
	labels, err := h.svc.GetMediaLabels(c.Request.Context(), mediaAssetID)
	if err != nil {
		slog.Error("GetMediaLabels", "err", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get media labels", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": labels}, nil)
}

// ─── Strikes ──────────────────────────────────────────────────────────────────

type issueStrikeRequest struct {
	UserID      string  `json:"user_id" binding:"required"`
	Reason      string  `json:"reason" binding:"required"`
	ContentType string  `json:"content_type"`
	ContentID   *string `json:"content_id,omitempty"`
	Severity    string  `json:"severity" binding:"required"`
}

func (h *Handler) IssueStrike(c *gin.Context) {
	if !hasScope(c.GetHeader("X-Scopes"), "admin") {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil, nil)
		return
	}
	createdBy, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	var req issueStrikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user_id", nil, nil)
		return
	}
	var contentID *uuid.UUID
	if req.ContentID != nil {
		id, err := uuid.Parse(*req.ContentID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid content_id", nil, nil)
			return
		}
		contentID = &id
	}
	strike, err := h.svc.IssueStrike(c.Request.Context(), userID, req.Reason, req.ContentType, contentID, req.Severity, createdBy)
	if err != nil {
		slog.Error("IssueStrike", "err", err)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, strike, nil)
}

func (h *Handler) GetUserStrikes(c *gin.Context) {
	if !hasScope(c.GetHeader("X-Scopes"), "admin") {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil, nil)
		return
	}
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user ID", nil, nil)
		return
	}
	strikes, err := h.svc.GetUserStrikes(c.Request.Context(), userID)
	if err != nil {
		slog.Error("GetUserStrikes", "err", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get strikes", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": strikes}, nil)
}

// ─── Verification requests ────────────────────────────────────────────────────

type submitVerificationRequest struct {
	Type string            `json:"type" binding:"required"`
	Docs map[string]string `json:"docs"`
}

func (h *Handler) SubmitVerificationRequest(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	var req submitVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	vreq, err := h.svc.SubmitVerificationRequest(c.Request.Context(), userID, req.Type, req.Docs)
	if err != nil {
		slog.Error("SubmitVerificationRequest", "err", err)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, vreq, nil)
}

func (h *Handler) AdminListVerificationRequests(c *gin.Context) {
	if !hasScope(c.GetHeader("X-Scopes"), "admin") {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil, nil)
		return
	}
	status := c.Query("status")
	limit, offset := paginate(c)
	requests, err := h.svc.ListVerificationRequestsAdmin(c.Request.Context(), status, limit, offset)
	if err != nil {
		slog.Error("ListVerificationRequests", "err", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list verification requests", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": requests}, nil)
}

type reviewVerificationRequest struct {
	Status          string `json:"status" binding:"required"`
	RejectionReason string `json:"rejection_reason"`
}

func (h *Handler) ReviewVerificationRequest(c *gin.Context) {
	if !hasScope(c.GetHeader("X-Scopes"), "admin") {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil, nil)
		return
	}
	reviewerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid reviewer ID", nil, nil)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request ID", nil, nil)
		return
	}
	var req reviewVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	if err := h.svc.ReviewVerificationRequest(c.Request.Context(), id, req.Status, req.RejectionReason, reviewerID); err != nil {
		slog.Error("ReviewVerificationRequest", "err", err)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func paginate(c *gin.Context) (limit, offset int) {
	limit = 20
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}
	offset = 0
	if o := c.Query("offset"); o != "" {
		if val, err := strconv.Atoi(o); err == nil && val >= 0 {
			offset = val
		}
	}
	return
}

func buildTeenAccount(userID uuid.UUID, req upsertTeenAccountRequest) *postgres.TeenAccount {
	ta := &postgres.TeenAccount{
		UserID:           userID,
		DailyLimitMins:   req.DailyLimitMins,
		ContentFilter:    req.ContentFilter,
		DMRestricted:     req.DMRestricted,
		FollowerApproval: req.FollowerApproval,
		LocationHidden:   req.LocationHidden,
	}
	if req.GuardianID != nil {
		id, err := uuid.Parse(*req.GuardianID)
		if err == nil {
			ta.GuardianID = &id
		}
	}
	return ta
}

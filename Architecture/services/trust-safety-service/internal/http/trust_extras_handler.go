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

// ─── Keyword filters ──────────────────────────────────────────────────────────

type addKeywordFilterRequest struct {
	Scope   string  `json:"scope" binding:"required"`
	ScopeID *string `json:"scope_id,omitempty"`
	Keyword string  `json:"keyword" binding:"required"`
	Action  string  `json:"action"`
}

func (h *Handler) AddKeywordFilter(c *gin.Context) {
	addedBy, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req addKeywordFilterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	if req.Action == "" {
		req.Action = "hide"
	}
	var scopeID *uuid.UUID
	if req.ScopeID != nil {
		id, err := uuid.Parse(*req.ScopeID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid scope_id", nil)
			return
		}
		scopeID = &id
	}
	// Non-admins may only write their OWN user-scope filters. scope_id used
	// to be caller-supplied and unvalidated, which let any authenticated
	// caller plant filters on another user's list (or platform-wide).
	if !adminAllowed(c) {
		if req.Scope != "user" || scopeID == nil || *scopeID != addedBy {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Only your own user-scope filters may be written", nil)
			return
		}
	}
	filter, err := h.svc.AddKeywordFilter(c.Request.Context(), req.Scope, scopeID, req.Keyword, req.Action, addedBy)
	if err != nil {
		slog.Error("AddKeywordFilter", "err", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, filter, nil)
}

func (h *Handler) GetKeywordFilters(c *gin.Context) {
	// This endpoint used to answer with no auth at all. It now requires a
	// caller identity, and non-admins can read only their own user scope.
	// An admin-service token (admin family) is the identity and is an admin.
	isAdmin := viaAdminToken(c)
	var callerID uuid.UUID
	if !isAdmin {
		var err error
		callerID, err = uuid.Parse(c.GetHeader("X-User-Id"))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
			return
		}
		isAdmin = hasScope(c.GetHeader("X-Scopes"), "admin")
	}

	scope := c.Query("scope")
	var scopeID *uuid.UUID
	if sid := c.Query("scope_id"); sid != "" {
		id, err := uuid.Parse(sid)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid scope_id", nil)
			return
		}
		scopeID = &id
	}

	if isAdmin {
		if scope == "" {
			scope = "platform"
		}
	} else {
		// Non-admins see exactly their own user scope. An explicit request
		// for anything else is refused rather than silently rewritten.
		if (scope != "" && scope != "user") || (scopeID != nil && *scopeID != callerID) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Only your own user-scope filters may be read", nil)
			return
		}
		scope = "user"
		scopeID = &callerID
	}

	filters, err := h.svc.GetKeywordFilters(c.Request.Context(), scope, scopeID)
	if err != nil {
		slog.Error("GetKeywordFilters", "err", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get keyword filters", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": filters}, nil)
}

// ─── Teen accounts ────────────────────────────────────────────────────────────

type upsertTeenAccountRequest struct {
	GuardianID       *string `json:"guardian_id,omitempty"`
	DailyLimitMins   int     `json:"daily_limit_mins"`
	ContentFilter    string  `json:"content_filter"`
	DMRestricted     bool    `json:"dm_restricted"`
	FollowerApproval bool    `json:"follower_approval"`
	LocationHidden   bool    `json:"location_hidden"`
}

func (h *Handler) UpsertTeenAccount(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req upsertTeenAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to upsert teen account", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, ta, nil)
}

func (h *Handler) GetTeenAccount(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user ID", nil)
		return
	}
	ta, err := h.svc.GetTeenAccount(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Teen account not found", nil)
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
	if !adminAllowed(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil)
		return
	}
	var req addMediaLabelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	mediaAssetID, err := uuid.Parse(req.MediaAssetID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media_asset_id", nil)
		return
	}
	label, err := h.svc.AddMediaLabel(c.Request.Context(), mediaAssetID, req.LabelType, req.Confidence, req.Source)
	if err != nil {
		slog.Error("AddMediaLabel", "err", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, label, nil)
}

func (h *Handler) GetMediaLabels(c *gin.Context) {
	mediaAssetID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	labels, err := h.svc.GetMediaLabels(c.Request.Context(), mediaAssetID)
	if err != nil {
		slog.Error("GetMediaLabels", "err", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get media labels", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": labels}, nil)
}

// ─── Strikes ──────────────────────────────────────────────────────────────────
//
// Issuing and voiding live in strikes_handler.go (admin-service token only).

// GetUserStrikes lists the user's ACTIVE strikes (not voided, not expired).
func (h *Handler) GetUserStrikes(c *gin.Context) {
	if !adminAllowed(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil)
		return
	}
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user ID", nil)
		return
	}
	strikes, err := h.svc.GetUserStrikes(c.Request.Context(), userID)
	if err != nil {
		slog.Error("GetUserStrikes", "err", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get strikes", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req submitVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	vreq, err := h.svc.SubmitVerificationRequest(c.Request.Context(), userID, req.Type, req.Docs)
	if err != nil {
		slog.Error("SubmitVerificationRequest", "err", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, vreq, nil)
}

func (h *Handler) AdminListVerificationRequests(c *gin.Context) {
	if !adminAllowed(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil)
		return
	}
	status := c.Query("status")
	limit, offset := paginate(c)
	requests, err := h.svc.ListVerificationRequestsAdmin(c.Request.Context(), status, limit, offset)
	if err != nil {
		slog.Error("ListVerificationRequests", "err", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list verification requests", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": requests}, nil)
}

type reviewVerificationRequest struct {
	Status          string `json:"status" binding:"required"`
	RejectionReason string `json:"rejection_reason"`
}

func (h *Handler) ReviewVerificationRequest(c *gin.Context) {
	if !adminAllowed(c) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Admin scope required", nil)
		return
	}
	reviewerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid reviewer ID", nil)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request ID", nil)
		return
	}
	var req reviewVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	if err := h.svc.ReviewVerificationRequest(c.Request.Context(), id, req.Status, req.RejectionReason, reviewerID); err != nil {
		slog.Error("ReviewVerificationRequest", "err", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
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

package http

import (
	"net/http"
	"strconv"

	"github.com/facebook-like/suggestion-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler manages HTTP routes for the suggestion service.
type Handler struct {
	svc *service.Service
}

// New creates a new Handler.
func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes registers all suggestion endpoints.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/suggestions")
	{
		v1.GET("", h.GetSuggestions)
		v1.GET("/interstitial", h.GetInterstitialSuggestions)
		v1.POST("/impression", h.LogImpression)
		v1.POST("/action", h.RecordAction)
		v1.GET("/batch", h.TriggerBatch)
	}
}

// GetSuggestions returns ranked suggestions for the authenticated user.
func (h *Handler) GetSuggestions(c *gin.Context) {
	viewerID, ok := parseUserID(c)
	if !ok {
		return
	}

	suggType := c.DefaultQuery("type", "friend")
	if suggType != "friend" && suggType != "follow" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type must be 'friend' or 'follow'"})
		return
	}

	limit := 20
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	cursor := c.Query("cursor")
	surface := c.DefaultQuery("surface", "home")

	resp, err := h.svc.GetSuggestions(c.Request.Context(), viewerID, suggType, limit, cursor, surface)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get suggestions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// GetInterstitialSuggestions returns contextual suggestions after an action.
func (h *Handler) GetInterstitialSuggestions(c *gin.Context) {
	viewerID, ok := parseUserID(c)
	if !ok {
		return
	}

	triggerType := c.Query("trigger_type")
	if triggerType != "friend_accept" && triggerType != "follow" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trigger_type must be 'friend_accept' or 'follow'"})
		return
	}

	triggerUserIDStr := c.Query("trigger_user_id")
	triggerUserID, err := uuid.Parse(triggerUserIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid trigger_user_id"})
		return
	}

	limit := 5
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 20 {
		limit = l
	}

	resp, err := h.svc.GetInterstitialSuggestions(c.Request.Context(), viewerID, triggerType, triggerUserID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get interstitial suggestions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// LogImpression records suggestion views.
func (h *Handler) LogImpression(c *gin.Context) {
	viewerID, ok := parseUserID(c)
	if !ok {
		return
	}

	var req service.ImpressionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if err := h.svc.LogImpressions(c.Request.Context(), viewerID, &req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to log impressions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "recorded", "count": len(req.Items)}})
}

// RecordAction records a user action on a suggestion (hide, block, dismiss_category, etc.).
func (h *Handler) RecordAction(c *gin.Context) {
	viewerID, ok := parseUserID(c)
	if !ok {
		return
	}

	var req service.ActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	validActions := map[string]bool{
		"hide":             true,
		"block":            true,
		"dismiss":          true,
		"dismiss_category": true,
		"decline":          true,
		"friend_request":   true,
		"follow":           true,
	}
	if !validActions[req.Action] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action must be one of: hide, block, dismiss, dismiss_category, decline, friend_request, follow"})
		return
	}

	// dismiss_category requires signal_type
	if req.Action == "dismiss_category" && req.SignalType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dismiss_category requires signal_type field"})
		return
	}

	if err := h.svc.RecordAction(c.Request.Context(), viewerID, &req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record action"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "action_recorded"}})
}

// TriggerBatch manually triggers a full batch recomputation.
func (h *Handler) TriggerBatch(c *gin.Context) {
	go func() {
		h.svc.RunFullBatch(c.Request.Context())
	}()
	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{"status": "batch_started"}})
}

// parseUserID extracts the authenticated user ID from the X-User-Id header.
func parseUserID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-Id")
	if raw == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing X-User-Id header"})
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid X-User-Id"})
		return uuid.UUID{}, false
	}
	return id, true
}

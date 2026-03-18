package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Subscription Lifecycle
// ---------------------------------------------------------------------------

type PauseSubscriptionRequest struct {
	PauseUntil string `json:"pause_until" binding:"required"` // RFC3339
}

func (h *Handler) PauseSubscription(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	subID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid subscription ID", nil, nil)
		return
	}

	var req PauseSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	pauseUntil, err := time.Parse(time.RFC3339, req.PauseUntil)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_DATE", "pause_until must be RFC3339 format", nil, nil)
		return
	}

	if err := h.svc.PauseSubscription(c.Request.Context(), userID, subID, pauseUntil); err != nil {
		switch err.Error() {
		case "SUBSCRIPTION_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Subscription not found", nil, nil)
		case "SUBSCRIPTION_NOT_OWNED":
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "You do not own this subscription", nil, nil)
		case "SUBSCRIPTION_NOT_ACTIVE":
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Subscription is not active", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "paused"}, nil)
}

func (h *Handler) ResumeSubscription(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	subID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid subscription ID", nil, nil)
		return
	}

	if err := h.svc.ResumeSubscription(c.Request.Context(), userID, subID); err != nil {
		switch err.Error() {
		case "SUBSCRIPTION_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Subscription not found", nil, nil)
		case "SUBSCRIPTION_NOT_OWNED":
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "You do not own this subscription", nil, nil)
		case "SUBSCRIPTION_NOT_PAUSED":
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Subscription is not paused", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "resumed"}, nil)
}

type CancelSubscriptionRequest struct {
	Reason    string `json:"reason"`
	Immediate bool   `json:"immediate"`
}

func (h *Handler) CancelSubscription(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	subID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid subscription ID", nil, nil)
		return
	}

	var req CancelSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Allow empty body — defaults to cancel at period end with no reason.
		req = CancelSubscriptionRequest{}
	}

	var svcErr error
	if req.Immediate {
		svcErr = h.svc.CancelImmediately(c.Request.Context(), userID, subID, req.Reason)
	} else {
		svcErr = h.svc.CancelAtPeriodEnd(c.Request.Context(), userID, subID, req.Reason)
	}

	if svcErr != nil {
		switch svcErr.Error() {
		case "SUBSCRIPTION_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Subscription not found", nil, nil)
		case "SUBSCRIPTION_NOT_OWNED":
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "You do not own this subscription", nil, nil)
		case "SUBSCRIPTION_CANNOT_CANCEL", "SUBSCRIPTION_ALREADY_CANCELLED":
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", svcErr.Error(), nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", svcErr.Error(), nil, nil)
		}
		return
	}

	status := "cancelled_at_period_end"
	if req.Immediate {
		status = "cancelled"
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": status}, nil)
}

type UpgradeSubscriptionRequest struct {
	NewTierID string `json:"new_tier_id" binding:"required"`
}

func (h *Handler) UpgradeSubscription(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	subID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid subscription ID", nil, nil)
		return
	}

	var req UpgradeSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	newTierID, err := uuid.Parse(req.NewTierID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid tier ID", nil, nil)
		return
	}

	if err := h.svc.UpgradeSubscription(c.Request.Context(), userID, subID, newTierID); err != nil {
		switch err.Error() {
		case "SUBSCRIPTION_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Subscription not found", nil, nil)
		case "SUBSCRIPTION_NOT_OWNED":
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "You do not own this subscription", nil, nil)
		case "SUBSCRIPTION_NOT_ACTIVE":
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Subscription is not active", nil, nil)
		case "TIER_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "TIER_NOT_FOUND", "Tier not found", nil, nil)
		case "TIER_INACTIVE":
			api.Error(c.Writer, http.StatusBadRequest, "TIER_INACTIVE", "Tier is not active", nil, nil)
		case "TIER_CREATOR_MISMATCH":
			api.Error(c.Writer, http.StatusBadRequest, "TIER_CREATOR_MISMATCH", "Tier does not belong to this creator", nil, nil)
		case "INSUFFICIENT_BALANCE_FOR_UPGRADE":
			api.Error(c.Writer, http.StatusBadRequest, "INSUFFICIENT_BALANCE", "Insufficient balance for upgrade", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "upgraded"}, nil)
}

func (h *Handler) GetSubscriptionEvents(c *gin.Context) {
	_, ok := getUserID(c)
	if !ok {
		return
	}

	subID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid subscription ID", nil, nil)
		return
	}

	limit := 20
	if limitStr := c.Query("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if offsetStr := c.Query("offset"); offsetStr != "" {
		if n, err := strconv.Atoi(offsetStr); err == nil && n >= 0 {
			offset = n
		}
	}

	events, err := h.svc.GetSubscriptionEvents(c.Request.Context(), subID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if events == nil {
		events = []postgres.SubscriptionEvent{}
	}

	api.JSON(c.Writer, http.StatusOK, events, nil)
}

package http

import (
	"net/http"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CheckEntitlement handles GET /v1/monetization/entitlements?creator_id=X[&tier_id=Y]
// Subscriber is taken from X-User-Id (the standard gateway header).
// Used by client-side preflight ("can the user open this paywalled post").
func (h *Handler) CheckEntitlement(c *gin.Context) {
	subscriberID, ok := getUserID(c)
	if !ok {
		return
	}
	creatorIDStr := c.Query("creator_id")
	if creatorIDStr == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "creator_id required", nil)
		return
	}
	creatorID, err := uuid.Parse(creatorIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "creator_id must be a UUID", nil)
		return
	}
	req := service.EntitlementCheckRequest{
		SubscriberID: subscriberID,
		CreatorID:    creatorID,
	}
	if v := c.Query("tier_id"); v != "" {
		t, err := uuid.Parse(v)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "tier_id must be a UUID", nil)
			return
		}
		req.RequiredTierID = &t
	}
	ent, err := h.svc.CheckEntitlement(c.Request.Context(), req)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, ent, nil)
}

// BulkCheckEntitlements handles POST /v1/monetization/entitlements/check
// Body: {"checks":[{"subscriber_id":"...","creator_id":"...","required_tier_id":"..."}]}
// Internal-call shape: lets post-service / feed-service ask "for this
// caller, mark which of these N posts they can open" in one round trip.
func (h *Handler) BulkCheckEntitlements(c *gin.Context) {
	var req struct {
		Checks []service.EntitlementCheckRequest `json:"checks"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	results, err := h.svc.CheckEntitlementsBulk(c.Request.Context(), req.Checks)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"results": results}, nil)
}

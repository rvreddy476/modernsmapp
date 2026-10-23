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

const (
	// OpEntitlementsCheck is the operation a service token must carry to
	// ask the bulk entitlement question for arbitrary subscribers.
	OpEntitlementsCheck = "monetization:entitlements-check"
	// IssuerPostService would be the caller of the internal bulk route.
	// post-service does not call it today (it asks GET /entitlements per
	// viewer); the route exists so a feed-shaped caller has a guarded path.
	IssuerPostService = "post-service"
)

// entitlementsCheckCallers are the issuers whose tokens reach the internal bulk route.
var entitlementsCheckCallers = []string{IssuerPostService}

type bulkEntitlementRequest struct {
	Checks []service.EntitlementCheckRequest `json:"checks"`
}

// BulkCheckEntitlements handles POST /v1/monetization/entitlements/check
// Body: {"checks":[{"creator_id":"...","required_tier_id":"..."}]}
//
// The public route answers ONLY for the caller (X-User-Id): a row with no
// subscriber_id is the caller's, and a row naming any other subscriber
// refuses the whole request with 403. Until 23 Oct 2026 it answered for
// whatever subscriber_id the body named, so anyone could learn who is
// subscribed to whom, and at which tier. Service-to-service checks for
// other subscribers go through InternalBulkCheckEntitlements.
func (h *Handler) BulkCheckEntitlements(c *gin.Context) {
	callerID, ok := getUserID(c)
	if !ok {
		return
	}
	var req bulkEntitlementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	for i := range req.Checks {
		switch req.Checks[i].SubscriberID {
		case uuid.Nil:
			req.Checks[i].SubscriberID = callerID
		case callerID:
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN",
				"entitlements can only be checked for the caller", nil)
			return
		}
	}
	h.answerBulkEntitlements(c, req.Checks)
}

// InternalBulkCheckEntitlements handles
// POST /v1/monetization/internal/entitlements/check for a named service
// (requireServiceCaller: a signed token from entitlementsCheckCallers
// carrying OpEntitlementsCheck). Every row must name its subscriber.
func (h *Handler) InternalBulkCheckEntitlements(c *gin.Context) {
	var req bulkEntitlementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	for _, chk := range req.Checks {
		if chk.SubscriberID == uuid.Nil || chk.CreatorID == uuid.Nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "subscriber_id and creator_id are required on every check", nil)
			return
		}
	}
	h.answerBulkEntitlements(c, req.Checks)
}

func (h *Handler) answerBulkEntitlements(c *gin.Context, checks []service.EntitlementCheckRequest) {
	results, err := h.svc.CheckEntitlementsBulk(c.Request.Context(), checks)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"results": results}, nil)
}

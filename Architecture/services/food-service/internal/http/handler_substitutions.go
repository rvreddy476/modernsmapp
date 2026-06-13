package http

import (
	"net/http"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ProposeSubstitutionRequest is the partner request body. Either
// suggested_item_id (with a matching menu item) OR suggested_item_name
// (for a free-text "we can offer X" reply) is acceptable.
type ProposeSubstitutionRequest struct {
	OriginalItemID    uuid.UUID  `json:"original_item_id"`
	SuggestedItemID   *uuid.UUID `json:"suggested_item_id,omitempty"`
	SuggestedItemName *string    `json:"suggested_item_name,omitempty"`
	PriceDiff         float64    `json:"price_diff"`
	Note              *string    `json:"note,omitempty"`
}

// PartnerProposeSubstitution — POST /v1/food/partner/orders/:orderId/substitutions
func (h *Handler) PartnerProposeSubstitution(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", err.Error(), nil)
		return
	}
	var req ProposeSubstitutionRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if req.SuggestedItemID == nil && (req.SuggestedItemName == nil || *req.SuggestedItemName == "") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "suggested_item_id or suggested_item_name required", nil)
		return
	}
	sub, err := h.svc.ProposeSubstitution(c.Request.Context(), uid, postgres.ProposeSubstitutionInput{
		OrderID:           orderID,
		OriginalItemID:    req.OriginalItemID,
		SuggestedItemID:   req.SuggestedItemID,
		SuggestedItemName: req.SuggestedItemName,
		PriceDiff:         req.PriceDiff,
		Note:              req.Note,
		ProposedBy:        uid,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "SUBSTITUTION_PROPOSE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, sub, nil)
}

// RespondSubstitutionRequest captures the customer's approve/decline.
type RespondSubstitutionRequest struct {
	Response string `json:"response"` // approved | declined | cancelled
}

// RespondSubstitution — POST /v1/food/orders/:orderId/substitutions/:subId/respond
func (h *Handler) RespondSubstitution(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	subID, err := uuid.Parse(c.Param("subId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SUB_ID", err.Error(), nil)
		return
	}
	var req RespondSubstitutionRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	sub, err := h.svc.RespondToSubstitution(c.Request.Context(), uid, subID, req.Response)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "SUBSTITUTION_RESPOND_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, sub, nil)
}

// ListSubstitutions — GET /v1/food/orders/:orderId/substitutions
func (h *Handler) ListSubstitutions(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", err.Error(), nil)
		return
	}
	subs, err := h.svc.ListSubstitutions(c.Request.Context(), uid, orderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LIST_SUBSTITUTIONS_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"substitutions": subs}, nil)
}

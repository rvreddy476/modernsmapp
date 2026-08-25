package http

import (
	"net/http"

	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Production chat pass — group governance, request block/report, invitations
// and realtime entitlement issuance (directive §3.3, §3.4, §5.3).

func (h *Handler) LeaveConversation(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	if err := h.svc.LeaveConversation(c.Request.Context(), userID, convID); err != nil {
		h.log.Warn("failed to leave conversation", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "left"}, nil)
}

type transferOwnerRequest struct {
	NewOwnerID string `json:"new_owner_id" binding:"required,uuid"`
}

func (h *Handler) TransferOwnership(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	var req transferOwnerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	newOwnerID, err := uuid.Parse(req.NewOwnerID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid new_owner_id", nil, nil)
		return
	}
	if err := h.svc.TransferOwnershipGoverned(c.Request.Context(), userID, convID, newOwnerID); err != nil {
		h.log.Warn("failed to transfer ownership", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ownership transferred"}, nil)
}

type setMemberRoleRequest struct {
	Role string `json:"role" binding:"required,oneof=admin member"`
}

func (h *Handler) SetMemberRole(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid user ID format", nil, nil)
		return
	}
	var req setMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
		return
	}
	if err := h.svc.SetMemberRoleGoverned(c.Request.Context(), userID, convID, targetID, req.Role); err != nil {
		h.log.Warn("failed to set member role", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "role updated"}, nil)
}

func (h *Handler) ListGroupInvitations(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	invitations, err := h.svc.ListMyInvitations(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to list invitations", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL", "Failed to list invitations", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, invitations, nil)
}

func (h *Handler) AcceptGroupInvitation(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	invitationID, err := uuid.Parse(c.Param("invitationId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid invitation ID", nil, nil)
		return
	}
	if err := h.svc.AcceptGroupInvitation(c.Request.Context(), userID, invitationID); err != nil {
		h.log.Warn("failed to accept invitation", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "invitation accepted"}, nil)
}

func (h *Handler) DeclineGroupInvitation(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	invitationID, err := uuid.Parse(c.Param("invitationId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "Invalid invitation ID", nil, nil)
		return
	}
	if err := h.svc.DeclineGroupInvitation(c.Request.Context(), userID, invitationID); err != nil {
		h.log.Warn("failed to decline invitation", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "invitation declined"}, nil)
}

func (h *Handler) BlockMessageRequest(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	if err := h.svc.BlockRequest(c.Request.Context(), userID, convID); err != nil {
		h.log.Warn("failed to block message request", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "request blocked"}, nil)
}

func (h *Handler) ReportMessageRequest(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	if err := h.svc.ReportRequest(c.Request.Context(), userID, convID); err != nil {
		h.log.Warn("failed to report message request", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "request reported"}, nil)
}

// IssueSubscription hands an active member a short-lived, audience-bound
// conversation-room entitlement (scoped-rooms foundation, directive §5.3).
func (h *Handler) IssueSubscription(c *gin.Context) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	entitlement, err := h.svc.IssueSubscriptionEntitlement(c.Request.Context(), userID, convID)
	if err != nil {
		h.log.Warn("failed to issue subscription entitlement", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusForbidden, "SUBSCRIPTION_DENIED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, entitlement, nil)
}

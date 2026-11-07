package http

import (
	"net/http"

	"github.com/atpost/group-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type InviteRequest struct {
	UserID  string   `json:"user_id"`
	UserIDs []string `json:"user_ids"`
}

func (h *Handler) InviteUser(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	var req InviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	// Batch invite
	if len(req.UserIDs) > 0 {
		var ids []uuid.UUID
		for _, idStr := range req.UserIDs {
			id, err := uuid.Parse(idStr)
			if err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID: "+idStr, nil)
				return
			}
			ids = append(ids, id)
		}
		result, err := h.svc.InviteUsersBatch(c.Request.Context(), actorID, groupID, ids)
		if err != nil {
			handleServiceError(c, err)
			return
		}
		// Counts, not names — see AddPeopleResult. The reply used to be a flat
		// "invited" whatever happened, including when every single person had
		// been skipped, so a client could truthfully report inviting people it
		// had not invited.
		api.JSON(c.Writer, http.StatusOK, result, nil)
		return
	}

	// Single invite
	if req.UserID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "user_id or user_ids required", nil)
		return
	}

	inviteeID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid invitee user ID", nil)
		return
	}

	// "added" or "invited" — the caller should say which one happened, not
	// always report an invitation the person never had to accept.
	status, err := h.svc.InviteUser(c.Request.Context(), actorID, groupID, inviteeID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": status}, nil)
}

func (h *Handler) AcceptInvite(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	inviteID, err := uuid.Parse(c.Param("inviteId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid invite ID", nil)
		return
	}

	if err := h.svc.AcceptInvite(c.Request.Context(), actorID, inviteID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "accepted"}, nil)
}

func (h *Handler) RejectInvite(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	inviteID, err := uuid.Parse(c.Param("inviteId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid invite ID", nil)
		return
	}

	if err := h.svc.RejectInvite(c.Request.Context(), actorID, inviteID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "rejected"}, nil)
}

func (h *Handler) ListGroupInvites(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	invites, err := h.svc.ListGroupInvites(c.Request.Context(), actorID, groupID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if invites == nil {
		invites = []store.GroupInvite{}
	}
	api.JSON(c.Writer, http.StatusOK, invites, nil)
}

// --- Join Requests ---

func (h *Handler) CreateJoinRequest(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	jr, err := h.svc.CreateJoinRequest(c.Request.Context(), actorID, groupID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, jr, nil)
}

func (h *Handler) ListJoinRequests(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	limit, offset := parsePagination(c)

	requests, err := h.svc.ListJoinRequests(c.Request.Context(), actorID, groupID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if requests == nil {
		requests = []store.GroupJoinRequest{}
	}
	api.JSON(c.Writer, http.StatusOK, requests, nil)
}

func (h *Handler) ApproveJoinRequest(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	requestID, err := uuid.Parse(c.Param("requestId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid request ID", nil)
		return
	}

	if err := h.svc.ApproveJoinRequest(c.Request.Context(), actorID, requestID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "approved"}, nil)
}

func (h *Handler) RejectJoinRequest(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	requestID, err := uuid.Parse(c.Param("requestId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid request ID", nil)
		return
	}

	if err := h.svc.RejectJoinRequest(c.Request.Context(), actorID, requestID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "rejected"}, nil)
}

// ListMyInvites returns the caller's pending group invites enriched with
// group display fields.
func (h *Handler) ListMyInvites(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	invites, err := h.svc.ListMyInvites(c.Request.Context(), actorID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, invites, nil)
}

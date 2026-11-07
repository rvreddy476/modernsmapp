package http

import (
	"net/http"

	"github.com/atpost/group-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type UpdateRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

type BanRequest struct {
	Reason string `json:"reason"`
}

func (h *Handler) JoinGroup(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	status, err := h.svc.JoinGroup(c.Request.Context(), actorID, groupID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": status}, nil)
}

func (h *Handler) LeaveGroup(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	if err := h.svc.LeaveGroup(c.Request.Context(), actorID, groupID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "left"}, nil)
}

func (h *Handler) ListMembers(c *gin.Context) {
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

	members, err := h.svc.ListMembers(c.Request.Context(), actorID, groupID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if members == nil {
		members = []store.GroupMember{}
	}
	api.JSON(c.Writer, http.StatusOK, members, nil)
}

func (h *Handler) UpdateMemberRole(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	var req UpdateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	if err := h.svc.UpdateMemberRole(c.Request.Context(), actorID, groupID, targetID, req.Role); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "role_updated"}, nil)
}

func (h *Handler) RemoveMember(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	if err := h.svc.RemoveMember(c.Request.Context(), actorID, groupID, targetID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

func (h *Handler) BanMember(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	var banReq BanRequest
	// Body is optional; ignore bind errors
	_ = c.ShouldBindJSON(&banReq)

	if err := h.svc.BanMember(c.Request.Context(), actorID, groupID, targetID, banReq.Reason); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "banned"}, nil)
}

// --- Ban Management ---

func (h *Handler) UnbanMember(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	if err := h.svc.UnbanMember(c.Request.Context(), actorID, groupID, targetID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unbanned"}, nil)
}

func (h *Handler) ListBannedMembers(c *gin.Context) {
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
	members, err := h.svc.ListBannedMembers(c.Request.Context(), actorID, groupID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if members == nil {
		members = []store.GroupMember{}
	}
	api.JSON(c.Writer, http.StatusOK, members, nil)
}

// --- Member Stats ---

func (h *Handler) GetTopContributors(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}
	limit, _ := parsePagination(c)
	contributors, err := h.svc.GetTopContributors(c.Request.Context(), actorID, groupID, limit)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if contributors == nil {
		contributors = []store.MemberStats{}
	}
	api.JSON(c.Writer, http.StatusOK, contributors, nil)
}

func (h *Handler) GetMemberStats(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	stats, err := h.svc.GetMemberStats(c.Request.Context(), actorID, groupID, userID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

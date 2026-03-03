package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/atpost/group-service/internal/service"
	"github.com/atpost/group-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc *service.Service
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/groups")
	{
		v1.POST("", h.CreateGroup)
		v1.GET("/my", h.GetMyGroups)
		v1.GET("/discover", h.DiscoverGroups)
		v1.GET("/search", h.SearchGroups)

		// Invite actions (before :groupId to avoid conflict)
		v1.POST("/invites/:inviteId/accept", h.AcceptInvite)
		v1.POST("/invites/:inviteId/reject", h.RejectInvite)

		v1.GET("/:groupId", h.GetGroup)
		v1.PUT("/:groupId", h.UpdateGroup)
		v1.DELETE("/:groupId", h.DeleteGroup)
		v1.POST("/:groupId/join", h.JoinGroup)
		v1.POST("/:groupId/leave", h.LeaveGroup)
		v1.GET("/:groupId/members", h.ListMembers)
		v1.PUT("/:groupId/members/:userId/role", h.UpdateMemberRole)
		v1.DELETE("/:groupId/members/:userId", h.RemoveMember)
		v1.POST("/:groupId/invite", h.InviteUser)
		v1.GET("/:groupId/invites", h.ListGroupInvites)
		v1.GET("/:groupId/feed", h.GetGroupFeed)
		v1.POST("/:groupId/posts", h.CreateGroupPost)
	}
}

// --- Request structs ---

type CreateGroupRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
}

type UpdateGroupRequest struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Visibility    *string `json:"visibility"`
	AvatarMediaID *string `json:"avatar_media_id"`
	CoverMediaID  *string `json:"cover_media_id"`
}

type InviteRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

type UpdateRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

// --- Helpers ---

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

func parsePagination(c *gin.Context) (int, int) {
	limit := 20
	offset := 0
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	if o, err := strconv.Atoi(c.DefaultQuery("offset", "0")); err == nil && o >= 0 {
		offset = o
	}
	return limit, offset
}

// --- Handlers ---

func (h *Handler) CreateGroup(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	var req CreateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	visibility := req.Visibility
	if visibility == "" {
		visibility = "public"
	}

	group, err := h.svc.CreateGroup(c.Request.Context(), actorID, req.Name, req.Description, visibility)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, group, nil)
}

func (h *Handler) GetGroup(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	group, err := h.svc.GetGroup(c.Request.Context(), actorID, groupID)
	if err != nil {
		if err.Error() == "forbidden: not a member of this private group" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if group == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Group not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, group, nil)
}

func (h *Handler) UpdateGroup(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	var req UpdateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	// Fetch existing group to fill in defaults
	existing, err := h.svc.GetGroup(c.Request.Context(), actorID, groupID)
	if err != nil {
		if err.Error() == "forbidden: not a member of this private group" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if existing == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Group not found", nil, nil)
		return
	}

	name := existing.Name
	if req.Name != nil {
		name = *req.Name
	}
	desc := existing.Description
	if req.Description != nil {
		desc = *req.Description
	}
	visibility := existing.Visibility
	if req.Visibility != nil {
		visibility = *req.Visibility
	}
	avatar := existing.AvatarMediaID
	if req.AvatarMediaID != nil {
		parsed, err := uuid.Parse(*req.AvatarMediaID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid avatar media ID", nil, nil)
			return
		}
		avatar = &parsed
	}
	cover := existing.CoverMediaID
	if req.CoverMediaID != nil {
		parsed, err := uuid.Parse(*req.CoverMediaID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid cover media ID", nil, nil)
			return
		}
		cover = &parsed
	}

	if err := h.svc.UpdateGroup(c.Request.Context(), actorID, groupID, name, desc, avatar, cover, visibility); err != nil {
		if err.Error() == "forbidden: only admins can update the group" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) DeleteGroup(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	if err := h.svc.DeleteGroup(c.Request.Context(), actorID, groupID); err != nil {
		if err.Error() == "group not found" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
			return
		}
		if err.Error() == "forbidden: only the group creator can delete the group" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) JoinGroup(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	if err := h.svc.JoinGroup(c.Request.Context(), actorID, groupID); err != nil {
		if err.Error() == "group not found" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
			return
		}
		if err.Error() == "forbidden: private groups require an invite" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "joined"}, nil)
}

func (h *Handler) LeaveGroup(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	if err := h.svc.LeaveGroup(c.Request.Context(), actorID, groupID); err != nil {
		if err.Error() == "not a member of this group" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
			return
		}
		if err.Error() == "must transfer admin role first" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	limit, offset := parsePagination(c)

	members, err := h.svc.ListMembers(c.Request.Context(), actorID, groupID, limit, offset)
	if err != nil {
		if err.Error() == "group not found" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
			return
		}
		if err.Error() == "forbidden: not a member of this private group" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	var req UpdateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.UpdateMemberRole(c.Request.Context(), actorID, groupID, targetID, req.Role); err != nil {
		if err.Error() == "forbidden: only admins can update member roles" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	if err := h.svc.RemoveMember(c.Request.Context(), actorID, groupID, targetID); err != nil {
		if err.Error() == "forbidden: only admins or moderators can remove members" || err.Error() == "forbidden: moderators cannot remove admins" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		if err.Error() == "target user is not a member" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

func (h *Handler) InviteUser(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	var req InviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	inviteeID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid invitee user ID", nil, nil)
		return
	}

	if err := h.svc.InviteUser(c.Request.Context(), actorID, groupID, inviteeID); err != nil {
		if err.Error() == "forbidden: only members can invite users" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "invited"}, nil)
}

func (h *Handler) AcceptInvite(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	inviteID, err := uuid.Parse(c.Param("inviteId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid invite ID", nil, nil)
		return
	}

	if err := h.svc.AcceptInvite(c.Request.Context(), actorID, inviteID); err != nil {
		if err.Error() == "invite not found" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
			return
		}
		if err.Error() == "forbidden: this invite is not for you" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid invite ID", nil, nil)
		return
	}

	if err := h.svc.RejectInvite(c.Request.Context(), actorID, inviteID); err != nil {
		if err.Error() == "invite not found" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
			return
		}
		if err.Error() == "forbidden: this invite is not for you" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	invites, err := h.svc.ListGroupInvites(c.Request.Context(), actorID, groupID)
	if err != nil {
		if err.Error() == "forbidden: only members can view group invites" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if invites == nil {
		invites = []store.GroupInvite{}
	}
	api.JSON(c.Writer, http.StatusOK, invites, nil)
}

func (h *Handler) GetGroupFeed(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	limit, offset := parsePagination(c)

	posts, err := h.svc.GetGroupFeed(c.Request.Context(), actorID, groupID, limit, offset)
	if err != nil {
		if err.Error() == "group not found" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
			return
		}
		if err.Error() == "forbidden: not a member of this private group" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if posts == nil {
		posts = []store.GroupPost{}
	}
	api.JSON(c.Writer, http.StatusOK, posts, nil)
}

func (h *Handler) CreateGroupPost(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	var body json.RawMessage
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	postID, err := h.svc.CreateGroupPost(c.Request.Context(), actorID, groupID, body)
	if err != nil {
		if err.Error() == "forbidden: only members can post in the group" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, map[string]string{"post_id": postID.String()}, nil)
}

func (h *Handler) GetMyGroups(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	limit, offset := parsePagination(c)

	groups, err := h.svc.GetMyGroups(c.Request.Context(), actorID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if groups == nil {
		groups = []store.Group{}
	}
	api.JSON(c.Writer, http.StatusOK, groups, nil)
}

func (h *Handler) DiscoverGroups(c *gin.Context) {
	limit, offset := parsePagination(c)

	groups, err := h.svc.DiscoverGroups(c.Request.Context(), limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if groups == nil {
		groups = []store.Group{}
	}
	api.JSON(c.Writer, http.StatusOK, groups, nil)
}

func (h *Handler) SearchGroups(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Missing search query parameter 'q'", nil, nil)
		return
	}

	limit, offset := parsePagination(c)

	groups, err := h.svc.SearchGroups(c.Request.Context(), query, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if groups == nil {
		groups = []store.Group{}
	}
	api.JSON(c.Writer, http.StatusOK, groups, nil)
}

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
		v1.POST("/handle/check", h.CheckHandle)
		v1.GET("/by-handle/:handle", h.GetGroupByHandle)

		// Invite actions (before :groupId to avoid conflict)
		v1.POST("/invites/:inviteId/accept", h.AcceptInvite)
		v1.POST("/invites/:inviteId/reject", h.RejectInvite)

		v1.GET("/:groupId", h.GetGroup)
		v1.PUT("/:groupId", h.UpdateGroup)
		v1.DELETE("/:groupId", h.DeleteGroup)
		v1.POST("/:groupId/join", h.JoinGroup)
		v1.POST("/:groupId/leave", h.LeaveGroup)
		v1.POST("/:groupId/archive", h.ArchiveGroup)
		v1.GET("/:groupId/members", h.ListMembers)
		v1.PUT("/:groupId/members/:userId/role", h.UpdateMemberRole)
		v1.DELETE("/:groupId/members/:userId", h.RemoveMember)
		v1.POST("/:groupId/members/:userId/ban", h.BanMember)
		v1.POST("/:groupId/invite", h.InviteUser)
		v1.GET("/:groupId/invites", h.ListGroupInvites)
		v1.GET("/:groupId/feed", h.GetGroupFeed)
		v1.POST("/:groupId/posts", h.CreateGroupPost)

		// Join requests
		v1.POST("/:groupId/join-requests", h.CreateJoinRequest)
		v1.GET("/:groupId/join-requests", h.ListJoinRequests)
		v1.POST("/:groupId/join-requests/:requestId/approve", h.ApproveJoinRequest)
		v1.POST("/:groupId/join-requests/:requestId/reject", h.RejectJoinRequest)

		// Rules
		v1.GET("/:groupId/rules", h.GetGroupRules)
		v1.PUT("/:groupId/rules", h.UpdateGroupRules)

		// Posts moderation
		v1.DELETE("/:groupId/posts/:postId", h.DeleteGroupPost)
		v1.PUT("/:groupId/posts/:postId/pin", h.PinPost)
		v1.DELETE("/:groupId/posts/:postId/pin", h.UnpinPost)

		// Ban management
		v1.DELETE("/:groupId/members/:userId/ban", h.UnbanMember)
		v1.GET("/:groupId/members/banned", h.ListBannedMembers)

		// Media
		v1.GET("/:groupId/media", h.GetGroupMedia)

		// Word Blocklist
		v1.GET("/:groupId/word-blocklist", h.GetWordBlocklist)
		v1.POST("/:groupId/word-blocklist", h.AddWordToBlocklist)
		v1.DELETE("/:groupId/word-blocklist/:word", h.RemoveWordFromBlocklist)

		// Post Approval Queue
		v1.GET("/:groupId/approval-queue", h.GetApprovalQueue)
		v1.POST("/:groupId/approval-queue/:itemId/approve", h.ApproveQueuedPost)
		v1.POST("/:groupId/approval-queue/:itemId/reject", h.RejectQueuedPost)

		// Group Channels
		v1.GET("/:groupId/channels", h.ListGroupChannels)
		v1.POST("/:groupId/channels", h.CreateGroupChannel)
		v1.DELETE("/:groupId/channels/:channelId", h.DeleteGroupChannel)

		// Wiki
		v1.GET("/:groupId/wiki", h.ListWikiPages)
		v1.POST("/:groupId/wiki", h.CreateWikiPage)
		v1.PUT("/:groupId/wiki/:pageId", h.UpdateWikiPage)
		v1.DELETE("/:groupId/wiki/:pageId", h.DeleteWikiPage)
	}
}

// --- Request structs ---

type CreateGroupRequest struct {
	Name           string `json:"name" binding:"required"`
	Description    string `json:"description"`
	Visibility     string `json:"visibility"`
	Handle         string `json:"handle"`
	Category       string `json:"category"`
	PrivacyLevel   string `json:"privacy_level"`
	JoinMode       string `json:"join_mode"`
	WhoCanPost     string `json:"who_can_post"`
	WhoCanInvite   string `json:"who_can_invite"`
	Location       string `json:"location"`
	Language       string `json:"language"`
	IdempotencyKey string `json:"idempotency_key"`
}

type UpdateGroupRequest struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Visibility    *string `json:"visibility"`
	AvatarMediaID *string `json:"avatar_media_id"`
	CoverMediaID  *string `json:"cover_media_id"`
}

type InviteRequest struct {
	UserID  string   `json:"user_id"`
	UserIDs []string `json:"user_ids"`
}

type UpdateRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

type CheckHandleRequest struct {
	Handle string `json:"handle" binding:"required"`
}

type BanRequest struct {
	Reason string `json:"reason"`
}

type UpdateRulesRequest struct {
	Rules []RuleItem `json:"rules" binding:"required"`
}

type RuleItem struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
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

func handleServiceError(c *gin.Context, err error) {
	msg := err.Error()
	switch {
	case contains(msg, "forbidden"), contains(msg, "only admins"), contains(msg, "only the group"):
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", msg, nil, nil)
	case contains(msg, "not found"):
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", msg, nil, nil)
	case contains(msg, "not a member"):
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", msg, nil, nil)
	case contains(msg, "rate_limited"):
		api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", msg, nil, nil)
	case contains(msg, "already"), contains(msg, "is already"):
		api.Error(c.Writer, http.StatusConflict, "CONFLICT", msg, nil, nil)
	case contains(msg, "must be between"), contains(msg, "must contain"), contains(msg, "reserved"),
		contains(msg, "invalid"), contains(msg, "not allowed"), contains(msg, "maximum"),
		contains(msg, "no users"), contains(msg, "no longer pending"),
		contains(msg, "does not accept"):
		api.Error(c.Writer, http.StatusUnprocessableEntity, "VALIDATION_ERROR", msg, nil, nil)
	default:
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", msg, nil, nil)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
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

	params := service.CreateGroupParams{
		Name:           req.Name,
		Description:    req.Description,
		Handle:         req.Handle,
		Category:       req.Category,
		PrivacyLevel:   req.PrivacyLevel,
		JoinMode:       req.JoinMode,
		WhoCanPost:     req.WhoCanPost,
		WhoCanInvite:   req.WhoCanInvite,
		Location:       req.Location,
		Language:       req.Language,
		IdempotencyKey: req.IdempotencyKey,
	}

	group, err := h.svc.CreateGroup(c.Request.Context(), actorID, params)
	if err != nil {
		handleServiceError(c, err)
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
		handleServiceError(c, err)
		return
	}
	if group == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Group not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, group, nil)
}

func (h *Handler) GetGroupByHandle(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	handle := c.Param("handle")
	group, err := h.svc.GetGroupByHandle(c.Request.Context(), actorID, handle)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if group == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Group not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, group, nil)
}

func (h *Handler) CheckHandle(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	var req CheckHandleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	available, err := h.svc.CheckHandle(c.Request.Context(), actorID, req.Handle)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"handle":    req.Handle,
		"available": available,
	}, nil)
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

	existing, err := h.svc.GetGroup(c.Request.Context(), actorID, groupID)
	if err != nil {
		handleServiceError(c, err)
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
		handleServiceError(c, err)
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
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) ArchiveGroup(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	if err := h.svc.ArchiveGroup(c.Request.Context(), actorID, groupID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "archived"}, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
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

	// Batch invite
	if len(req.UserIDs) > 0 {
		var ids []uuid.UUID
		for _, idStr := range req.UserIDs {
			id, err := uuid.Parse(idStr)
			if err != nil {
				api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID: "+idStr, nil, nil)
				return
			}
			ids = append(ids, id)
		}
		if err := h.svc.InviteUsersBatch(c.Request.Context(), actorID, groupID, ids); err != nil {
			handleServiceError(c, err)
			return
		}
		api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "invited"}, nil)
		return
	}

	// Single invite
	if req.UserID == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "user_id or user_ids required", nil, nil)
		return
	}

	inviteeID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid invitee user ID", nil, nil)
		return
	}

	if err := h.svc.InviteUser(c.Request.Context(), actorID, groupID, inviteeID); err != nil {
		handleServiceError(c, err)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid invite ID", nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
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
		handleServiceError(c, err)
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
		handleServiceError(c, err)
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

// --- Join Requests ---

func (h *Handler) CreateJoinRequest(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid request ID", nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid request ID", nil, nil)
		return
	}

	if err := h.svc.RejectJoinRequest(c.Request.Context(), actorID, requestID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "rejected"}, nil)
}

// --- Rules ---

func (h *Handler) GetGroupRules(c *gin.Context) {
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	rules, err := h.svc.GetGroupRules(c.Request.Context(), groupID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if rules == nil {
		rules = []store.GroupRule{}
	}
	api.JSON(c.Writer, http.StatusOK, rules, nil)
}

func (h *Handler) UpdateGroupRules(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}

	var req UpdateRulesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	var rules []store.GroupRule
	for _, item := range req.Rules {
		rules = append(rules, store.GroupRule{
			Title:       item.Title,
			Description: item.Description,
		})
	}

	if err := h.svc.UpdateGroupRules(c.Request.Context(), actorID, groupID, rules); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "rules_updated"}, nil)
}

// --- Post Moderation ---

func (h *Handler) DeleteGroupPost(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}
	if err := h.svc.DeleteGroupPost(c.Request.Context(), actorID, groupID, postID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) PinPost(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}
	if err := h.svc.PinPost(c.Request.Context(), actorID, groupID, postID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "pinned"}, nil)
}

func (h *Handler) UnpinPost(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}
	if err := h.svc.UnpinPost(c.Request.Context(), actorID, groupID, postID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unpinned"}, nil)
}

// --- Ban Management ---

func (h *Handler) UnbanMember(c *gin.Context) {
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil, nil)
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

// --- Media ---

func (h *Handler) GetGroupMedia(c *gin.Context) {
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
	posts, err := h.svc.GetGroupMedia(c.Request.Context(), actorID, groupID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if posts == nil {
		posts = []store.GroupPost{}
	}
	api.JSON(c.Writer, http.StatusOK, posts, nil)
}

// ── Word Blocklist ───────────────────────────────────────────

func (h *Handler) GetWordBlocklist(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	words, err := h.svc.GetWordBlocklist(c.Request.Context(), actorID, groupID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if words == nil {
		words = []string{}
	}
	api.JSON(c.Writer, http.StatusOK, words, nil)
}

func (h *Handler) AddWordToBlocklist(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	var req struct {
		Word string `json:"word" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	if err := h.svc.AddWordToBlocklist(c.Request.Context(), actorID, groupID, req.Word); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) RemoveWordFromBlocklist(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	word := c.Param("word")
	if err := h.svc.RemoveWordFromBlocklist(c.Request.Context(), actorID, groupID, word); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

// ── Post Approval Queue ──────────────────────────────────────

func (h *Handler) GetApprovalQueue(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	limit, offset := parsePagination(c)
	items, err := h.svc.GetApprovalQueue(c.Request.Context(), actorID, groupID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if items == nil {
		items = []store.ApprovalQueueItem{}
	}
	api.JSON(c.Writer, http.StatusOK, items, nil)
}

func (h *Handler) ApproveQueuedPost(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid item id", nil, nil)
		return
	}
	if err := h.svc.ApprovePost(c.Request.Context(), actorID, groupID, itemID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) RejectQueuedPost(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid item id", nil, nil)
		return
	}
	if err := h.svc.RejectQueuedPost(c.Request.Context(), actorID, groupID, itemID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

// ── Group Channels ───────────────────────────────────────────

func (h *Handler) ListGroupChannels(c *gin.Context) {
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	channels, err := h.svc.ListGroupChannels(c.Request.Context(), groupID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if channels == nil {
		channels = []store.GroupChannel{}
	}
	api.JSON(c.Writer, http.StatusOK, channels, nil)
}

func (h *Handler) CreateGroupChannel(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	var req struct {
		Name        string `json:"name" binding:"required"`
		Type        string `json:"type" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	ch, err := h.svc.CreateGroupChannel(c.Request.Context(), actorID, groupID, req.Name, req.Type, req.Description)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, ch, nil)
}

func (h *Handler) DeleteGroupChannel(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	channelID, err := uuid.Parse(c.Param("channelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid channel id", nil, nil)
		return
	}
	if err := h.svc.DeleteGroupChannel(c.Request.Context(), actorID, groupID, channelID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

// ── Wiki ─────────────────────────────────────────────────────

func (h *Handler) ListWikiPages(c *gin.Context) {
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	pages, err := h.svc.ListWikiPages(c.Request.Context(), groupID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if pages == nil {
		pages = []store.WikiPage{}
	}
	api.JSON(c.Writer, http.StatusOK, pages, nil)
}

func (h *Handler) CreateWikiPage(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	var req struct {
		Title   string `json:"title" binding:"required"`
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	page, err := h.svc.CreateWikiPage(c.Request.Context(), actorID, groupID, req.Title, req.Content)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, page, nil)
}

func (h *Handler) UpdateWikiPage(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	pageID, err := uuid.Parse(c.Param("pageId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid page id", nil, nil)
		return
	}
	var req struct {
		Title   string `json:"title" binding:"required"`
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	page, err := h.svc.UpdateWikiPage(c.Request.Context(), actorID, groupID, pageID, req.Title, req.Content)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

func (h *Handler) DeleteWikiPage(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid group id", nil, nil)
		return
	}
	pageID, err := uuid.Parse(c.Param("pageId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid page id", nil, nil)
		return
	}
	if err := h.svc.DeleteWikiPage(c.Request.Context(), actorID, groupID, pageID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

package http

import (
	"encoding/json"
	"net/http"

	"github.com/atpost/group-service/internal/service"
	"github.com/atpost/group-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

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
	// GCC Phase 1 fields
	GroupType         string          `json:"group_type"`
	MaxMembers        int             `json:"max_members"`
	JoinQuestions     json.RawMessage `json:"join_questions"`
	TopicTags         []string        `json:"topic_tags"`
	CommentPermission string          `json:"comment_permission"`
	MemberListVisible *bool           `json:"member_list_visible"`
	LinkSharing       *bool           `json:"link_sharing"`
	IsMature          bool            `json:"is_mature"`
}

type UpdateGroupRequest struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Visibility    *string `json:"visibility"`
	AvatarMediaID *string `json:"avatar_media_id"`
	CoverMediaID  *string `json:"cover_media_id"`
	// GCC Phase 1 fields
	GroupType         *string         `json:"group_type"`
	MaxMembers        *int            `json:"max_members"`
	JoinQuestions     json.RawMessage `json:"join_questions"`
	TopicTags         []string        `json:"topic_tags"`
	CommentPermission *string         `json:"comment_permission"`
	MemberListVisible *bool           `json:"member_list_visible"`
	LinkSharing       *bool           `json:"link_sharing"`
}

type CheckHandleRequest struct {
	Handle string `json:"handle" binding:"required"`
}

// --- Handlers ---

func (h *Handler) CreateGroup(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	var req CreateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	// The Idempotency-Key request header is the canonical fallback used by the
	// rest of the platform (wallet-service top-up/send, dating-service premium
	// purchase, post-service, food-service, commerce-service all read it), and
	// every web client write already carries one. Requiring the snake_case body
	// field and ignoring the header made this the one create endpoint that a
	// correctly-behaving client could not satisfy.
	req.IdempotencyKey = resolveIdempotencyKey(c, req.IdempotencyKey)
	if req.IdempotencyKey == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "idempotency_key is required (send the idempotency_key body field or an Idempotency-Key header)", nil)
		return
	}

	params := service.CreateGroupParams{
		Name:              req.Name,
		Description:       req.Description,
		Handle:            req.Handle,
		Category:          req.Category,
		PrivacyLevel:      req.PrivacyLevel,
		JoinMode:          req.JoinMode,
		WhoCanPost:        req.WhoCanPost,
		WhoCanInvite:      req.WhoCanInvite,
		Location:          req.Location,
		Language:          req.Language,
		IdempotencyKey:    req.IdempotencyKey,
		GroupType:         req.GroupType,
		MaxMembers:        req.MaxMembers,
		JoinQuestions:     req.JoinQuestions,
		TopicTags:         req.TopicTags,
		CommentPermission: req.CommentPermission,
		MemberListVisible: req.MemberListVisible,
		LinkSharing:       req.LinkSharing,
		IsMature:          req.IsMature,
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	group, err := h.svc.GetGroup(c.Request.Context(), actorID, groupID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if group == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Group not found", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Group not found", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	var req UpdateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	existing, err := h.svc.GetGroup(c.Request.Context(), actorID, groupID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if existing == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Group not found", nil)
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
	// nil keeps the current avatar/cover: the service reads it under the
	// group's media lock, not from this earlier read.
	var avatar, cover *uuid.UUID
	if req.AvatarMediaID != nil {
		parsed, err := uuid.Parse(*req.AvatarMediaID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid avatar media ID", nil)
			return
		}
		avatar = &parsed
	}
	if req.CoverMediaID != nil {
		parsed, err := uuid.Parse(*req.CoverMediaID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid cover media ID", nil)
			return
		}
		cover = &parsed
	}

	if err := h.svc.UpdateGroup(c.Request.Context(), actorID, groupID, name, desc, avatar, cover, visibility); err != nil {
		handleServiceError(c, err)
		return
	}

	// Update GCC Phase 1 settings if any provided
	hasSettings := req.GroupType != nil || req.MaxMembers != nil || req.JoinQuestions != nil ||
		req.TopicTags != nil || req.CommentPermission != nil || req.MemberListVisible != nil || req.LinkSharing != nil
	if hasSettings {
		groupType := existing.GroupType
		if req.GroupType != nil {
			groupType = *req.GroupType
		}
		maxMembers := existing.MaxMembers
		if req.MaxMembers != nil {
			maxMembers = *req.MaxMembers
		}
		joinQuestions := existing.JoinQuestions
		if req.JoinQuestions != nil {
			joinQuestions = req.JoinQuestions
		}
		topicTags := existing.TopicTags
		if req.TopicTags != nil {
			topicTags = req.TopicTags
		}
		commentPermission := existing.CommentPermission
		if req.CommentPermission != nil {
			commentPermission = *req.CommentPermission
		}
		memberListVisible := existing.MemberListVisible
		if req.MemberListVisible != nil {
			memberListVisible = *req.MemberListVisible
		}
		linkSharing := existing.LinkSharing
		if req.LinkSharing != nil {
			linkSharing = *req.LinkSharing
		}

		if err := h.svc.UpdateGroupSettings(c.Request.Context(), actorID, groupID, groupType, maxMembers,
			joinQuestions, topicTags, commentPermission, memberListVisible, linkSharing); err != nil {
			handleServiceError(c, err)
			return
		}
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}

	if err := h.svc.ArchiveGroup(c.Request.Context(), actorID, groupID); err != nil {
		handleServiceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "archived"}, nil)
}

func (h *Handler) GetMyGroups(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	limit, offset := parsePagination(c)

	groups, err := h.svc.GetMyGroups(c.Request.Context(), actorID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if groups == nil {
		groups = []store.Group{}
	}
	api.JSON(c.Writer, http.StatusOK, groups, nil)
}

func (h *Handler) DiscoverGroups(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, offset := parsePagination(c)
	groupType := c.Query("type")

	groups, err := h.svc.DiscoverGroupsForUser(c.Request.Context(), actorID, limit, offset, groupType)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if groups == nil {
		groups = []service.DiscoveredGroup{}
	}
	api.JSON(c.Writer, http.StatusOK, groups, nil)
}

func (h *Handler) SearchGroups(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Missing search query parameter 'q'", nil)
		return
	}

	limit, offset := parsePagination(c)

	groups, err := h.svc.SearchGroups(c.Request.Context(), query, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if groups == nil {
		groups = []store.Group{}
	}
	api.JSON(c.Writer, http.StatusOK, groups, nil)
}

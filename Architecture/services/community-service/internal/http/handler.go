package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/community-service/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// authorizeViewerFromHeader runs the visibility gate using the
// X-User-Id header (may be empty for unauthenticated callers; the
// service handles nil viewer cleanly). Returns nil iff the viewer is
// allowed to read content from the community.
func (h *Handler) authorizeViewerFromHeader(c *gin.Context, communityID uuid.UUID) error {
	rawUser := c.GetHeader("X-User-Id")
	var viewer *uuid.UUID
	if u, err := uuid.Parse(rawUser); err == nil {
		viewer = &u
	}
	return h.svc.AuthorizeViewer(c.Request.Context(), communityID, viewer)
}

// writeAuthError maps service.AuthError sentinels onto the right HTTP
// status. Audit CC2-CC7: we use a single chokepoint so all the post +
// wiki handlers respond consistently when the actor isn't authorized.
// Returns true when the error was handled (caller should bail out).
func writeAuthError(c *gin.Context, err error) bool {
	var ae *service.AuthError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae {
	case service.ErrNotCommunityMember:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NOT_MEMBER", "not a community member", nil)
	case service.ErrMemberBanned:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "MEMBER_BANNED", "membership is banned or suspended", nil)
	case service.ErrInsufficientRole:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "INSUFFICIENT_ROLE", "role does not permit this action", nil)
	case service.ErrNotPostAuthor:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NOT_AUTHOR", "only the post author can perform this action", nil)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", ae.Error(), nil)
	}
	return true
}

type Handler struct {
	svc         *service.Service
	internalKey string
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// WithInternalKey sets the internal service key used to authenticate
// service-to-service requests via the X-Internal-Service-Key header.
func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Apply internal service key enforcement to all /v1 routes.
	// Health and metrics endpoints registered outside this group remain public.
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}

	v1 := r.Group("/v1/communities")
	{
		v1.POST("", h.CreateCommunity)
		v1.GET("/my", h.GetMyCommunities)
		v1.GET("/discover", h.DiscoverCommunities)
		v1.GET("/:communityId", h.GetCommunity)
		v1.PUT("/:communityId", h.UpdateCommunity)
		v1.DELETE("/:communityId", h.DeleteCommunity)
		v1.POST("/:communityId/join", h.JoinCommunity)
		v1.POST("/:communityId/leave", h.LeaveCommunity)
		v1.GET("/:communityId/members", h.ListMembers)
		v1.PUT("/:communityId/members/:userId/role", h.UpdateMemberRole)
		v1.POST("/:communityId/members/:userId/ban", h.BanMember)
		v1.DELETE("/:communityId/members/:userId/ban", h.UnbanMember)
		v1.GET("/:communityId/spaces", h.ListSpaces)
		v1.POST("/:communityId/spaces", h.CreateSpace)
		v1.PUT("/:communityId/spaces/:spaceId", h.UpdateSpace)
		v1.DELETE("/:communityId/spaces/:spaceId", h.DeleteSpace)
		v1.POST("/:communityId/spaces/:spaceId/quarantine", h.QuarantineSpace)
		v1.GET("/:communityId/join-requests", h.ListJoinRequests)
		v1.POST("/:communityId/join-requests/:requestId/approve", h.ApproveRequest)
		v1.POST("/:communityId/join-requests/:requestId/reject", h.RejectRequest)
		v1.GET("/:communityId/modlog", h.GetModLog)
	}

	// Events
	v1.GET("/:communityId/events", h.ListEvents)
	v1.POST("/:communityId/events", h.CreateEvent)

	// Post, engagement, and wiki routes
	h.registerPostRoutes(v1)
}

// --- Request structs ---

type CreateCommunityRequest struct {
	Handle          string          `json:"handle" binding:"required"`
	Name            string          `json:"name" binding:"required"`
	Description     string          `json:"description"`
	AvatarMediaID   *uuid.UUID      `json:"avatar_media_id"`
	BannerMediaID   *uuid.UUID      `json:"banner_media_id"`
	CommunityType   string          `json:"community_type"`
	Category        string          `json:"category"`
	Language        string          `json:"language"`
	JoinMode        string          `json:"join_mode"`
	EmailDomainGate *string         `json:"email_domain_gate"`
	JoinQuestions   json.RawMessage `json:"join_questions"`
	MemberDirectory *bool           `json:"member_directory"`
	CrossSpaceBans  *bool           `json:"cross_space_bans"`
	MaxSubSpaces    *int            `json:"max_sub_spaces"`
	Latitude        *float64        `json:"latitude"`
	Longitude       *float64        `json:"longitude"`
	LocationName    string          `json:"location_name"`
	Rules           []string        `json:"rules"`
	TopicTags       []string        `json:"topic_tags"`
}

type UpdateCommunityRequest struct {
	Name            *string          `json:"name"`
	Description     *string          `json:"description"`
	AvatarMediaID   *uuid.UUID       `json:"avatar_media_id"`
	BannerMediaID   *uuid.UUID       `json:"banner_media_id"`
	CommunityType   *string          `json:"community_type"`
	Category        *string          `json:"category"`
	Language        *string          `json:"language"`
	JoinMode        *string          `json:"join_mode"`
	EmailDomainGate *string          `json:"email_domain_gate"`
	JoinQuestions   json.RawMessage  `json:"join_questions"`
	MemberDirectory *bool            `json:"member_directory"`
	CrossSpaceBans  *bool            `json:"cross_space_bans"`
	MaxSubSpaces    *int             `json:"max_sub_spaces"`
	Latitude        *float64         `json:"latitude"`
	Longitude       *float64         `json:"longitude"`
	LocationName    *string          `json:"location_name"`
	Rules           []string         `json:"rules"`
	TopicTags       []string         `json:"topic_tags"`
}

type JoinCommunityRequest struct {
	Answers json.RawMessage `json:"answers"`
}

type CreateSpaceRequest struct {
	SpaceType       string     `json:"space_type"`
	LinkedGroupID   *uuid.UUID `json:"linked_group_id"`
	LinkedChannelID *uuid.UUID `json:"linked_channel_id"`
	Name            string     `json:"name" binding:"required"`
	Description     string     `json:"description"`
	SortOrder       int        `json:"sort_order"`
}

type UpdateSpaceRequest struct {
	Name            *string    `json:"name"`
	Description     *string    `json:"description"`
	SortOrder       *int       `json:"sort_order"`
	LinkedGroupID   *uuid.UUID `json:"linked_group_id"`
	LinkedChannelID *uuid.UUID `json:"linked_channel_id"`
}

type UpdateMemberRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

type BanMemberRequest struct {
	Reason string `json:"reason"`
}

type QuarantineSpaceRequest struct {
	Reason string `json:"reason"`
}

// --- Helpers ---

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
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
	case contains(msg, "forbidden"), contains(msg, "only admins"), contains(msg, "only the community"), contains(msg, "only moderators"), contains(msg, "only space managers"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", msg, nil)
	case contains(msg, "not found"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", msg, nil)
	case contains(msg, "not a member"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", msg, nil)
	case contains(msg, "rate_limited"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", msg, nil)
	case contains(msg, "already"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "CONFLICT", msg, nil)
	case contains(msg, "invalid"), contains(msg, "must be between"), contains(msg, "is required"):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "VALIDATION_ERROR", msg, nil)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", msg, nil)
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

func (h *Handler) CreateCommunity(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	var req CreateCommunityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	params := service.CreateCommunityParams{
		Handle:          req.Handle,
		Name:            req.Name,
		Description:     req.Description,
		AvatarMediaID:   req.AvatarMediaID,
		BannerMediaID:   req.BannerMediaID,
		CommunityType:   req.CommunityType,
		Category:        req.Category,
		Language:        req.Language,
		JoinMode:        req.JoinMode,
		EmailDomainGate: req.EmailDomainGate,
		JoinQuestions:   req.JoinQuestions,
		MemberDirectory: req.MemberDirectory,
		CrossSpaceBans:  req.CrossSpaceBans,
		MaxSubSpaces:    req.MaxSubSpaces,
		Latitude:        req.Latitude,
		Longitude:       req.Longitude,
		LocationName:    req.LocationName,
		Rules:           req.Rules,
		TopicTags:       req.TopicTags,
	}

	community, err := h.svc.CreateCommunity(c.Request.Context(), actorID, params)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, community, nil)
}

func (h *Handler) GetCommunity(c *gin.Context) {
	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	var viewerID *uuid.UUID
	if uid, err := uuid.Parse(c.GetHeader("X-User-Id")); err == nil {
		viewerID = &uid
	}

	community, err := h.svc.GetCommunity(c.Request.Context(), communityID, viewerID)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, community, nil)
}

func (h *Handler) UpdateCommunity(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	var req UpdateCommunityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	params := service.UpdateCommunityParams{
		Name:            req.Name,
		Description:     req.Description,
		AvatarMediaID:   req.AvatarMediaID,
		BannerMediaID:   req.BannerMediaID,
		CommunityType:   req.CommunityType,
		Category:        req.Category,
		Language:        req.Language,
		JoinMode:        req.JoinMode,
		EmailDomainGate: req.EmailDomainGate,
		JoinQuestions:   req.JoinQuestions,
		MemberDirectory: req.MemberDirectory,
		CrossSpaceBans:  req.CrossSpaceBans,
		MaxSubSpaces:    req.MaxSubSpaces,
		Latitude:        req.Latitude,
		Longitude:       req.Longitude,
		LocationName:    req.LocationName,
		Rules:           req.Rules,
		TopicTags:       req.TopicTags,
	}

	community, err := h.svc.UpdateCommunity(c.Request.Context(), communityID, actorID, params)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, community, nil)
}

func (h *Handler) DeleteCommunity(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	if err := h.svc.DeleteCommunity(c.Request.Context(), communityID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) JoinCommunity(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	var req JoinCommunityRequest
	// Answers are optional, ignore bind errors
	_ = c.ShouldBindJSON(&req)

	member, joinReq, err := h.svc.JoinCommunity(c.Request.Context(), communityID, actorID, req.Answers)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	if member != nil {
		api.JSON(c.Writer, http.StatusOK, map[string]any{"status": "joined", "member": member}, nil)
	} else {
		api.JSON(c.Writer, http.StatusAccepted, map[string]any{"status": "request_pending", "join_request": joinReq}, nil)
	}
}

func (h *Handler) LeaveCommunity(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	if err := h.svc.LeaveCommunity(c.Request.Context(), communityID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "left"}, nil)
}

func (h *Handler) ListMembers(c *gin.Context) {
	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	// Audit CC3: previously returned full member list (incl. banned and
	// pending) with no auth check on the caller. Treat the member list
	// like any other community content: gate non-public communities
	// behind active membership.
	if err := h.authorizeViewerFromHeader(c, communityID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "community not found", nil)
		return
	}

	limit, offset := parsePagination(c)
	members, err := h.svc.ListMembers(c.Request.Context(), communityID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, members, nil)
}

func (h *Handler) UpdateMemberRole(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	targetUserID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	var req UpdateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	if err := h.svc.UpdateMemberRole(c.Request.Context(), communityID, targetUserID, actorID, req.Role); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "role_updated"}, nil)
}

func (h *Handler) BanMember(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	targetUserID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	var req BanMemberRequest
	_ = c.ShouldBindJSON(&req)

	if err := h.svc.BanMember(c.Request.Context(), communityID, targetUserID, actorID, req.Reason); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "banned"}, nil)
}

func (h *Handler) UnbanMember(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	targetUserID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	if err := h.svc.UnbanMember(c.Request.Context(), communityID, targetUserID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unbanned"}, nil)
}

func (h *Handler) ListSpaces(c *gin.Context) {
	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	spaces, err := h.svc.ListSpaces(c.Request.Context(), communityID)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, spaces, nil)
}

func (h *Handler) CreateSpace(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	var req CreateSpaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	params := service.CreateSpaceParams{
		SpaceType:       req.SpaceType,
		LinkedGroupID:   req.LinkedGroupID,
		LinkedChannelID: req.LinkedChannelID,
		Name:            req.Name,
		Description:     req.Description,
		SortOrder:       req.SortOrder,
	}

	space, err := h.svc.CreateSpace(c.Request.Context(), communityID, actorID, params)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, space, nil)
}

func (h *Handler) UpdateSpace(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	spaceID, err := uuid.Parse(c.Param("spaceId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid space ID", nil)
		return
	}

	var req UpdateSpaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	params := service.UpdateSpaceParams{
		Name:            req.Name,
		Description:     req.Description,
		SortOrder:       req.SortOrder,
		LinkedGroupID:   req.LinkedGroupID,
		LinkedChannelID: req.LinkedChannelID,
	}

	space, err := h.svc.UpdateSpace(c.Request.Context(), communityID, spaceID, actorID, params)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, space, nil)
}

func (h *Handler) DeleteSpace(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	spaceID, err := uuid.Parse(c.Param("spaceId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid space ID", nil)
		return
	}

	if err := h.svc.DeleteSpace(c.Request.Context(), communityID, spaceID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) QuarantineSpace(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	spaceID, err := uuid.Parse(c.Param("spaceId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid space ID", nil)
		return
	}

	var req QuarantineSpaceRequest
	_ = c.ShouldBindJSON(&req)

	if err := h.svc.QuarantineSpace(c.Request.Context(), communityID, spaceID, actorID, req.Reason); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "quarantined"}, nil)
}

func (h *Handler) ListJoinRequests(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	limit, offset := parsePagination(c)
	requests, err := h.svc.ListJoinRequests(c.Request.Context(), communityID, actorID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, requests, nil)
}

func (h *Handler) ApproveRequest(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	requestID, err := uuid.Parse(c.Param("requestId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid request ID", nil)
		return
	}

	if err := h.svc.ApproveRequest(c.Request.Context(), communityID, requestID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "approved"}, nil)
}

func (h *Handler) RejectRequest(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	requestID, err := uuid.Parse(c.Param("requestId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid request ID", nil)
		return
	}

	if err := h.svc.RejectRequest(c.Request.Context(), communityID, requestID, actorID); err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "rejected"}, nil)
}

func (h *Handler) GetModLog(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	communityID, err := uuid.Parse(c.Param("communityId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid community ID", nil)
		return
	}

	limit, offset := parsePagination(c)
	entries, err := h.svc.GetModLog(c.Request.Context(), communityID, actorID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, entries, nil)
}

func (h *Handler) GetMyCommunities(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}

	limit, offset := parsePagination(c)
	communities, err := h.svc.GetMyCommunities(c.Request.Context(), actorID, limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, communities, nil)
}

func (h *Handler) DiscoverCommunities(c *gin.Context) {
	limit, offset := parsePagination(c)
	communities, err := h.svc.DiscoverCommunities(c.Request.Context(), limit, offset)
	if err != nil {
		handleServiceError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, communities, nil)
}

func (h *Handler) Health(c *gin.Context) {
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

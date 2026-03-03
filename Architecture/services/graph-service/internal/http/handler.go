package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/graph-service/internal/service"
	"github.com/atpost/graph-service/internal/store"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

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
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}

	v1 := r.Group("/v1/graph")
	{
		v1.POST("/follow", h.Follow)
		v1.POST("/unfollow", h.Unfollow)
		v1.POST("/block", h.Block)
		v1.GET("/relationship", h.GetRelationship)
		v1.GET("/counts/:userId", h.GetCounts)
		v1.GET("/followers/:userId", h.GetFollowers)
		v1.GET("/following/:userId", h.GetFollowing)
		v1.GET("/mutuals", h.GetMutualFollowers)

		// Mute
		v1.POST("/mute", h.Mute)
		v1.DELETE("/mute", h.Unmute)
		// Internal: blocked + muted union
		v1.GET("/blocked-and-muted", h.GetBlockedAndMuted)
		// Batch relationship lookup
		v1.POST("/relationships/batch", h.GetRelationshipBatch)

		// Friends
		v1.POST("/friend-request", h.SendFriendRequest)
		v1.POST("/friend-request/accept", h.AcceptFriendRequest)
		v1.POST("/friend-request/reject", h.RejectFriendRequest)
		v1.DELETE("/friend", h.RemoveFriend)
		v1.GET("/friends/:userId", h.GetFriends)
		v1.GET("/friend-requests", h.GetPendingRequests)
	}
}

type UserIDRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

// parseAuthAndBody extracts the authenticated user ID from the header and the target user ID from the JSON body.
func parseAuthAndBody(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	actorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return uuid.Nil, uuid.Nil, false
	}
	var req UserIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return uuid.Nil, uuid.Nil, false
	}
	targetID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid target user ID", nil, nil)
		return uuid.Nil, uuid.Nil, false
	}
	return actorID, targetID, true
}

// parsePaginatedUserID extracts a userId path param and limit/offset query params.
func parsePaginatedUserID(c *gin.Context) (uuid.UUID, int, int, bool) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return uuid.Nil, 0, 0, false
	}
	limit := 20
	offset := 0
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}
	if o, err := strconv.Atoi(c.DefaultQuery("offset", "0")); err == nil && o >= 0 {
		offset = o
	}
	return userID, limit, offset, true
}

func (h *Handler) Follow(c *gin.Context) {
	followerID, followeeID, ok := parseAuthAndBody(c)
	if !ok {
		return
	}
	if err := h.svc.Follow(c.Request.Context(), followerID, followeeID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "followed"}, nil)
}

func (h *Handler) Unfollow(c *gin.Context) {
	followerID, followeeID, ok := parseAuthAndBody(c)
	if !ok {
		return
	}
	if err := h.svc.Unfollow(c.Request.Context(), followerID, followeeID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unfollowed"}, nil)
}

func (h *Handler) Block(c *gin.Context) {
	blockerID, blockedID, ok := parseAuthAndBody(c)
	if !ok {
		return
	}
	if err := h.svc.Block(c.Request.Context(), blockerID, blockedID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "blocked"}, nil)
}

func (h *Handler) GetRelationship(c *gin.Context) {
	actorID, err := uuid.Parse(c.Query("user_id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user_id", nil, nil)
		return
	}
	targetID, err := uuid.Parse(c.Query("other_id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid other_id", nil, nil)
		return
	}
	rel, err := h.svc.GetRelationship(c.Request.Context(), actorID, targetID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rel, nil)
}

func (h *Handler) GetCounts(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}
	counts, err := h.svc.GetCounts(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, counts, nil)
}

func (h *Handler) GetFollowers(c *gin.Context) {
	userID, limit, offset, ok := parsePaginatedUserID(c)
	if !ok {
		return
	}
	ids, err := h.svc.GetFollowers(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	api.JSON(c.Writer, http.StatusOK, ids, nil)
}

func (h *Handler) GetFollowing(c *gin.Context) {
	userID, limit, offset, ok := parsePaginatedUserID(c)
	if !ok {
		return
	}
	ids, err := h.svc.GetFollowing(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	api.JSON(c.Writer, http.StatusOK, ids, nil)
}

func (h *Handler) GetMutualFollowers(c *gin.Context) {
	userIDStr := c.Query("user_id")
	otherIDStr := c.Query("other_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user_id", nil, nil)
		return
	}
	otherID, err := uuid.Parse(otherIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid other_id", nil, nil)
		return
	}
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}
	ids, err := h.svc.GetMutualFollowers(c.Request.Context(), userID, otherID, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	api.JSON(c.Writer, http.StatusOK, ids, nil)
}

// --- Friends ---

func (h *Handler) SendFriendRequest(c *gin.Context) {
	senderID, receiverID, ok := parseAuthAndBody(c)
	if !ok {
		return
	}
	if err := h.svc.SendFriendRequest(c.Request.Context(), senderID, receiverID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "request_sent"}, nil)
}

func (h *Handler) AcceptFriendRequest(c *gin.Context) {
	receiverID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	var req UserIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	senderID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid sender user ID", nil, nil)
		return
	}
	if err := h.svc.AcceptFriendRequest(c.Request.Context(), senderID, receiverID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "accepted"}, nil)
}

func (h *Handler) RejectFriendRequest(c *gin.Context) {
	receiverID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	var req UserIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	senderID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid sender user ID", nil, nil)
		return
	}
	if err := h.svc.RejectFriendRequest(c.Request.Context(), senderID, receiverID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "rejected"}, nil)
}

func (h *Handler) RemoveFriend(c *gin.Context) {
	actorID, targetID, ok := parseAuthAndBody(c)
	if !ok {
		return
	}
	if err := h.svc.RemoveFriend(c.Request.Context(), actorID, targetID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

func (h *Handler) GetFriends(c *gin.Context) {
	userID, limit, offset, ok := parsePaginatedUserID(c)
	if !ok {
		return
	}
	ids, err := h.svc.GetFriends(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	api.JSON(c.Writer, http.StatusOK, ids, nil)
}

func (h *Handler) GetPendingRequests(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	reqs, err := h.svc.GetPendingRequests(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if reqs == nil {
		reqs = []store.FriendRequest{}
	}
	api.JSON(c.Writer, http.StatusOK, reqs, nil)
}

// --- Mutes ---

func (h *Handler) Mute(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	mutedID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid target user ID", nil, nil)
		return
	}
	if err := h.svc.Mute(c.Request.Context(), userID, mutedID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) Unmute(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	mutedID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid target user ID", nil, nil)
		return
	}
	h.svc.Unmute(c.Request.Context(), userID, mutedID)
	c.Status(http.StatusNoContent)
}

func (h *Handler) GetBlockedAndMuted(c *gin.Context) {
	userIDStr := c.Query("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user_id", nil, nil)
		return
	}
	ids, err := h.svc.GetBlockedAndMuted(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	c.JSON(http.StatusOK, gin.H{"user_ids": ids})
}

func (h *Handler) GetRelationshipBatch(c *gin.Context) {
	var req struct {
		ViewerID  string   `json:"viewer_id"`
		TargetIDs []string `json:"target_ids"`
	}
	if err := c.BindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	viewerID, err := uuid.Parse(req.ViewerID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid viewer_id", nil, nil)
		return
	}
	targetIDs := make([]uuid.UUID, 0, len(req.TargetIDs))
	for _, id := range req.TargetIDs {
		if uid, err := uuid.Parse(id); err == nil {
			targetIDs = append(targetIDs, uid)
		}
	}
	result, err := h.svc.GetRelationshipBatch(c.Request.Context(), viewerID, targetIDs)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	c.JSON(http.StatusOK, result)
}

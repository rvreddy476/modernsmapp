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

		// Close Friends
		cfGroup := v1.Group("/close-friends")
		cfGroup.GET("", h.GetCloseFriends)
		cfGroup.POST("/:id", h.AddCloseFriend)
		cfGroup.DELETE("/:id", h.RemoveCloseFriend)

		// Circles
		circlesGroup := v1.Group("/circles")
		circlesGroup.POST("", h.CreateCircle)
		circlesGroup.GET("", h.ListCircles)
		circlesGroup.PUT("/:circleId", h.UpdateCircle)
		circlesGroup.DELETE("/:circleId", h.DeleteCircle)
		circlesGroup.GET("/:circleId/members", h.GetCircleMembers)
		circlesGroup.POST("/:circleId/members/:userId", h.AddCircleMember)
		circlesGroup.DELETE("/:circleId/members/:userId", h.RemoveCircleMember)

		// Relationship Labels
		labelsGroup := v1.Group("/labels")
		labelsGroup.GET("", h.ListRelationshipLabels)
		labelsGroup.PUT("/:userId", h.UpsertRelationshipLabel)
		labelsGroup.DELETE("/:userId", h.DeleteRelationshipLabel)

		// Favorites
		favsGroup := v1.Group("/favorites")
		favsGroup.GET("", h.GetFavorites)
		favsGroup.POST("/:userId", h.AddFavorite)
		favsGroup.DELETE("/:userId", h.RemoveFavorite)
	}
}

// getUserID extracts and parses the authenticated user ID from the X-User-Id header.
func getUserID(c *gin.Context) (uuid.UUID, bool) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return uuid.Nil, false
	}
	return userID, true
}

type UserIDRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

// parseAuthAndBody extracts the authenticated user ID from the header and the target user ID from the JSON body.
func parseAuthAndBody(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	actorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return uuid.Nil, uuid.Nil, false
	}
	var req UserIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return uuid.Nil, uuid.Nil, false
	}
	targetID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid target user ID", nil)
		return uuid.Nil, uuid.Nil, false
	}
	return actorID, targetID, true
}

// parsePaginatedUserID extracts a userId path param and limit/offset query params.
func parsePaginatedUserID(c *gin.Context) (uuid.UUID, int, int, bool) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
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
	// Audit HG2: bound offset to keep PostgreSQL's worst-case scan
	// cost finite. A user with 10M followers asked for page 100k by a
	// runaway client would scan ~2M rows per request. Real UIs don't
	// scroll past a few thousand entries; a true millions-scale browser
	// needs the cursor path, tracked separately.
	const maxOffset = 10000
	if offset > maxOffset {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "OFFSET_TOO_LARGE",
			"offset exceeds maximum; use cursor pagination for deep scrolls", nil)
		return uuid.Nil, 0, 0, false
	}
	return userID, limit, offset, true
}

func (h *Handler) Follow(c *gin.Context) {
	followerID, followeeID, ok := parseAuthAndBody(c)
	if !ok {
		return
	}
	if err := h.svc.Follow(c.Request.Context(), followerID, followeeID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "blocked"}, nil)
}

func (h *Handler) GetRelationship(c *gin.Context) {
	actorID, err := uuid.Parse(c.Query("user_id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user_id", nil)
		return
	}
	targetID, err := uuid.Parse(c.Query("other_id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid other_id", nil)
		return
	}
	rel, err := h.svc.GetRelationship(c.Request.Context(), actorID, targetID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rel, nil)
}

func (h *Handler) GetCounts(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	counts, err := h.svc.GetCounts(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, counts, nil)
}

func (h *Handler) GetFollowers(c *gin.Context) {
	userID, limit, offset, ok := parsePaginatedUserID(c)
	if !ok {
		return
	}
	// Audit HG4: hide follower/following lists from users the owner has
	// blocked (matches the Twitter-style "blocked users can't see your
	// connections" rule). The owner themselves and the API gateway
	// always pass — internal callers don't send X-User-Id.
	if !h.callerAllowedToReadConnections(c, userID) {
		api.JSON(c.Writer, http.StatusOK, []uuid.UUID{}, nil)
		return
	}
	ids, err := h.svc.GetFollowers(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
	if !h.callerAllowedToReadConnections(c, userID) {
		api.JSON(c.Writer, http.StatusOK, []uuid.UUID{}, nil)
		return
	}
	ids, err := h.svc.GetFollowing(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	api.JSON(c.Writer, http.StatusOK, ids, nil)
}

// callerAllowedToReadConnections returns false when the X-User-Id
// caller is non-empty, distinct from the owner whose connections are
// being viewed, and present in the owner's blocked set. Missing /
// invalid X-User-Id (internal callers, unauthenticated) and self-views
// always pass — the block rule only suppresses the blocked user's view.
func (h *Handler) callerAllowedToReadConnections(c *gin.Context, ownerID uuid.UUID) bool {
	rawCaller := c.GetHeader("X-User-Id")
	if rawCaller == "" {
		return true
	}
	callerID, err := uuid.Parse(rawCaller)
	if err != nil || callerID == ownerID {
		return true
	}
	blocked, err := h.svc.IsBlockedBy(c.Request.Context(), ownerID, callerID)
	if err != nil {
		// Fail open on graph-store errors — surfacing a 500 here would
		// break the entire profile screen for healthy callers.
		return true
	}
	return !blocked
}

func (h *Handler) GetMutualFollowers(c *gin.Context) {
	userIDStr := c.Query("user_id")
	otherIDStr := c.Query("other_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user_id", nil)
		return
	}
	otherID, err := uuid.Parse(otherIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid other_id", nil)
		return
	}
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}
	ids, err := h.svc.GetMutualFollowers(c.Request.Context(), userID, otherID, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "request_sent"}, nil)
}

func (h *Handler) AcceptFriendRequest(c *gin.Context) {
	receiverID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req UserIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	senderID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid sender user ID", nil)
		return
	}
	if err := h.svc.AcceptFriendRequest(c.Request.Context(), senderID, receiverID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "accepted"}, nil)
}

func (h *Handler) RejectFriendRequest(c *gin.Context) {
	receiverID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req UserIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	senderID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid sender user ID", nil)
		return
	}
	if err := h.svc.RejectFriendRequest(c.Request.Context(), senderID, receiverID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	reqs, err := h.svc.GetPendingRequests(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	mutedID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid target user ID", nil)
		return
	}
	if err := h.svc.Mute(c.Request.Context(), userID, mutedID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) Unmute(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	mutedID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid target user ID", nil)
		return
	}
	h.svc.Unmute(c.Request.Context(), userID, mutedID)
	c.Status(http.StatusNoContent)
}

func (h *Handler) GetBlockedAndMuted(c *gin.Context) {
	userIDStr := c.Query("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user_id", nil)
		return
	}
	ids, err := h.svc.GetBlockedAndMuted(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	c.JSON(http.StatusOK, gin.H{"user_ids": ids})
}

// ── Close Friends ───────────────────────────────────────────

func (h *Handler) GetCloseFriends(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	ids, err := h.svc.GetCloseFriends(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	api.JSON(c.Writer, http.StatusOK, ids, nil)
}

func (h *Handler) AddCloseFriend(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	friendID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid friend id", nil)
		return
	}
	if err := h.svc.AddCloseFriend(c.Request.Context(), userID, friendID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) RemoveCloseFriend(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	friendID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid friend id", nil)
		return
	}
	if err := h.svc.RemoveCloseFriend(c.Request.Context(), userID, friendID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

// ── Circles ─────────────────────────────────────────────────

func (h *Handler) CreateCircle(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req struct {
		Name  string  `json:"name" binding:"required"`
		Emoji *string `json:"emoji"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	circle, err := h.svc.CreateCircle(c.Request.Context(), userID, req.Name, req.Emoji)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, circle, nil)
}

func (h *Handler) ListCircles(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	circles, err := h.svc.ListCircles(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if circles == nil {
		circles = []store.Circle{}
	}
	api.JSON(c.Writer, http.StatusOK, circles, nil)
}

func (h *Handler) UpdateCircle(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	circleID, err := uuid.Parse(c.Param("circleId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid circle id", nil)
		return
	}
	var req struct {
		Name  string  `json:"name" binding:"required"`
		Emoji *string `json:"emoji"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	circle, err := h.svc.UpdateCircle(c.Request.Context(), circleID, userID, req.Name, req.Emoji)
	if err != nil {
		if err.Error() == "circle not found" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "circle not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, circle, nil)
}

func (h *Handler) DeleteCircle(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	circleID, err := uuid.Parse(c.Param("circleId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid circle id", nil)
		return
	}
	if err := h.svc.DeleteCircle(c.Request.Context(), circleID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) GetCircleMembers(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	circleID, err := uuid.Parse(c.Param("circleId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid circle id", nil)
		return
	}
	ids, err := h.svc.GetCircleMembers(c.Request.Context(), circleID, userID)
	if err != nil {
		if err.Error() == "circle not found" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "circle not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	api.JSON(c.Writer, http.StatusOK, ids, nil)
}

func (h *Handler) AddCircleMember(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	circleID, err := uuid.Parse(c.Param("circleId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid circle id", nil)
		return
	}
	memberID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return
	}
	if err := h.svc.AddCircleMember(c.Request.Context(), circleID, userID, memberID); err != nil {
		if err.Error() == "circle not found" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "circle not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) RemoveCircleMember(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	circleID, err := uuid.Parse(c.Param("circleId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid circle id", nil)
		return
	}
	memberID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return
	}
	if err := h.svc.RemoveCircleMember(c.Request.Context(), circleID, userID, memberID); err != nil {
		if err.Error() == "circle not found" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "circle not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

// ── Relationship Labels ──────────────────────────────────────

func (h *Handler) ListRelationshipLabels(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	labels, err := h.svc.ListRelationshipLabels(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if labels == nil {
		labels = []store.RelationshipLabel{}
	}
	api.JSON(c.Writer, http.StatusOK, labels, nil)
}

func (h *Handler) UpsertRelationshipLabel(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return
	}
	var req struct {
		Label string `json:"label" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if err := h.svc.UpsertRelationshipLabel(c.Request.Context(), userID, targetID, req.Label); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_LABEL", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) DeleteRelationshipLabel(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return
	}
	if err := h.svc.DeleteRelationshipLabel(c.Request.Context(), userID, targetID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

// ── Favorites ────────────────────────────────────────────────

func (h *Handler) GetFavorites(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	ids, err := h.svc.GetFavorites(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	api.JSON(c.Writer, http.StatusOK, ids, nil)
}

func (h *Handler) AddFavorite(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return
	}
	if err := h.svc.AddFavorite(c.Request.Context(), userID, targetID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) RemoveFavorite(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return
	}
	if err := h.svc.RemoveFavorite(c.Request.Context(), userID, targetID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) GetRelationshipBatch(c *gin.Context) {
	var req struct {
		ViewerID  string   `json:"viewer_id"`
		TargetIDs []string `json:"target_ids"`
	}
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	viewerID, err := uuid.Parse(req.ViewerID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid viewer_id", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	c.JSON(http.StatusOK, result)
}

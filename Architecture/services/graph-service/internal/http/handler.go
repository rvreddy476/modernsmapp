package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/graph-service/internal/permission"
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
		v1.DELETE("/block", h.Unblock)
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

		// Connections (formerly "friends" — spec §3.2/§19)
		v1.POST("/connection-request", h.SendConnectionRequest)
		v1.POST("/connection-request/accept", h.AcceptConnectionRequest)
		v1.POST("/connection-request/decline", h.DeclineConnectionRequest)
		v1.POST("/connection-request/cancel", h.CancelConnectionRequest)
		v1.POST("/connection-request/filter", h.FilterConnectionRequest)
		v1.POST("/connection-request/unfilter", h.UnfilterConnectionRequest)
		v1.DELETE("/connection", h.RemoveConnection)
		v1.GET("/connections/:userId", h.GetConnections)
		v1.GET("/connection-requests", h.GetPendingConnectionRequests)
		v1.GET("/connection-requests/filtered", h.GetFilteredConnectionRequests)
		v1.GET("/connection-requests/sent", h.GetSentConnectionRequests)

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

	// Permission check API (spec §9.8). Single source of truth for
	// "can actor do X to target" — used by clients to render buttons
	// and by other services (chat) to gate actions.
	perm := r.Group("/v1/permissions")
	{
		perm.GET("/check", h.CheckPermission)
		perm.POST("/check-batch", h.CheckPermissionBatch)
	}
}

// permissionTTLSeconds is the freshness budget callers may cache a decision
// for — it matches the privacy-settings cache TTL (spec §6.2).
const permissionTTLSeconds = 60

// CheckPermission resolves one actor→target tuple for the requested actions.
// The actor is the X-User-Id caller; target_user_id and a comma-separated
// actions list come from the query string.
func (h *Handler) CheckPermission(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	targetID, err := uuid.Parse(c.Query("target_user_id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid target_user_id", nil)
		return
	}
	actions := permission.ParseActions(strings.Split(c.Query("actions"), ","))
	if len(actions) == 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "no valid actions requested", nil)
		return
	}

	decisions, err := h.svc.ResolvePermissions(c.Request.Context(), actorID, targetID, actions)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"target_user_id": targetID,
		"decisions":      decisions,
		"computed_at":    time.Now().UTC(),
		"ttl_seconds":    permissionTTLSeconds,
	}, nil)
}

// CheckPermissionBatch resolves up to 50 targets for the X-User-Id actor.
func (h *Handler) CheckPermissionBatch(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	var req struct {
		TargetUserIDs []string `json:"target_user_ids"`
		Actions       []string `json:"actions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	actions := permission.ParseActions(req.Actions)
	if len(actions) == 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "no valid actions requested", nil)
		return
	}
	if len(req.TargetUserIDs) > 50 {
		req.TargetUserIDs = req.TargetUserIDs[:50]
	}

	results := make(map[string]map[permission.Action]permission.Decision, len(req.TargetUserIDs))
	for _, raw := range req.TargetUserIDs {
		targetID, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		decisions, err := h.svc.ResolvePermissions(c.Request.Context(), actorID, targetID, actions)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		results[raw] = decisions
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"results":     results,
		"computed_at": time.Now().UTC(),
		"ttl_seconds": permissionTTLSeconds,
	}, nil)
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
		if errors.Is(err, service.ErrRateLimited) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil)
			return
		}
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
	// HG2: prefer cursor pagination when supplied. Cursor is O(log n)
	// even on celebrities with millions of followers; offset is kept
	// for admin tools that need a stable index.
	if cursor := c.Query("cursor"); cursor != "" || c.Query("paginate") == "cursor" {
		edges, next, err := h.svc.GetFollowersCursor(c.Request.Context(), userID, limit, cursor)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		ids := make([]uuid.UUID, 0, len(edges))
		for _, e := range edges {
			ids = append(ids, e.UserID)
		}
		api.JSON(c.Writer, http.StatusOK, gin.H{"items": ids, "next_cursor": next}, nil)
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
	if cursor := c.Query("cursor"); cursor != "" || c.Query("paginate") == "cursor" {
		edges, next, err := h.svc.GetFollowingCursor(c.Request.Context(), userID, limit, cursor)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		ids := make([]uuid.UUID, 0, len(edges))
		for _, e := range edges {
			ids = append(ids, e.UserID)
		}
		api.JSON(c.Writer, http.StatusOK, gin.H{"items": ids, "next_cursor": next}, nil)
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

// --- Connections ---

// parseConnectionTarget extracts the authenticated user ID from the header
// and the counterparty user ID from a {"user_id": ...} JSON body.
func parseConnectionTarget(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	return parseAuthAndBody(c)
}

func (h *Handler) SendConnectionRequest(c *gin.Context) {
	senderID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req struct {
		UserID  string `json:"user_id" binding:"required"`
		Source  string `json:"source"`
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	receiverID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid target user ID", nil)
		return
	}
	if len(req.Message) > 280 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "MESSAGE_TOO_LONG", "connection request message exceeds 280 chars", nil)
		return
	}
	if err := h.svc.SendConnectionRequest(c.Request.Context(), senderID, receiverID, req.Source, req.Message); err != nil {
		if errors.Is(err, service.ErrRateLimited) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "request_sent"}, nil)
}

func (h *Handler) AcceptConnectionRequest(c *gin.Context) {
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
	if err := h.svc.AcceptConnectionRequest(c.Request.Context(), senderID, receiverID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "accepted"}, nil)
}

func (h *Handler) DeclineConnectionRequest(c *gin.Context) {
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
	if err := h.svc.DeclineConnectionRequest(c.Request.Context(), senderID, receiverID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "declined"}, nil)
}

// CancelConnectionRequest lets the sender withdraw their own pending request.
// X-User-Id is the sender; the body's user_id is the receiver.
func (h *Handler) CancelConnectionRequest(c *gin.Context) {
	senderID, receiverID, ok := parseConnectionTarget(c)
	if !ok {
		return
	}
	if err := h.svc.CancelConnectionRequest(c.Request.Context(), senderID, receiverID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "cancelled"}, nil)
}

func (h *Handler) RemoveConnection(c *gin.Context) {
	actorID, targetID, ok := parseConnectionTarget(c)
	if !ok {
		return
	}
	if err := h.svc.RemoveConnection(c.Request.Context(), actorID, targetID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

func (h *Handler) GetConnections(c *gin.Context) {
	userID, limit, offset, ok := parsePaginatedUserID(c)
	if !ok {
		return
	}
	ids, err := h.svc.GetConnections(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	api.JSON(c.Writer, http.StatusOK, ids, nil)
}

func (h *Handler) GetPendingConnectionRequests(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	reqs, err := h.svc.GetPendingConnectionRequests(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if reqs == nil {
		reqs = []store.ConnectionRequest{}
	}
	api.JSON(c.Writer, http.StatusOK, reqs, nil)
}

// GetSentConnectionRequests lists the X-User-Id caller's outgoing pending
// connection requests (spec §9.2).
func (h *Handler) GetSentConnectionRequests(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	reqs, err := h.svc.GetSentConnectionRequests(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if reqs == nil {
		reqs = []store.ConnectionRequest{}
	}
	api.JSON(c.Writer, http.StatusOK, reqs, nil)
}

// GetFilteredConnectionRequests lists the X-User-Id caller's auto-filtered
// (hidden) pending connection requests (P1.4).
func (h *Handler) GetFilteredConnectionRequests(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	reqs, err := h.svc.GetFilteredConnectionRequests(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if reqs == nil {
		reqs = []store.ConnectionRequest{}
	}
	api.JSON(c.Writer, http.StatusOK, reqs, nil)
}

// UnfilterConnectionRequest moves an auto-filtered request back into the
// caller's visible inbox. X-User-Id is the recipient; body user_id is the
// sender (P1.4).
func (h *Handler) UnfilterConnectionRequest(c *gin.Context) {
	receiverID, senderID, ok := parseAuthAndBody(c)
	if !ok {
		return
	}
	if err := h.svc.UnfilterConnectionRequest(c.Request.Context(), senderID, receiverID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"ok": true}, nil)
}

// FilterConnectionRequest marks a pending request as auto-filtered. INTERNAL —
// called by trust-safety-service after abuse scoring (P1.4).
func (h *Handler) FilterConnectionRequest(c *gin.Context) {
	var req struct {
		SenderID   string `json:"sender_id" binding:"required"`
		ReceiverID string `json:"receiver_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	senderID, err := uuid.Parse(req.SenderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid sender ID", nil)
		return
	}
	receiverID, err := uuid.Parse(req.ReceiverID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid receiver ID", nil)
		return
	}
	if err := h.svc.SetRequestFiltered(c.Request.Context(), senderID, receiverID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"ok": true}, nil)
}

// Unblock removes a block (X-User-Id is the blocker, body user_id the blocked).
func (h *Handler) Unblock(c *gin.Context) {
	blockerID, blockedID, ok := parseAuthAndBody(c)
	if !ok {
		return
	}
	if err := h.svc.Unblock(c.Request.Context(), blockerID, blockedID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unblocked"}, nil)
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
		switch {
		case errors.Is(err, service.ErrRateLimited):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many Trusted Circle changes. Try again later.", nil)
		case errors.Is(err, service.ErrCannotAddSelf):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CANNOT_ADD_SELF", err.Error(), nil)
		case errors.Is(err, service.ErrNotAFriend):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "NOT_A_FRIEND", err.Error(), nil)
		case errors.Is(err, service.ErrCircleCapReached):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "CIRCLE_CAP_REACHED", err.Error(), nil)
		case errors.Is(err, service.ErrAlreadyMember):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "ALREADY_MEMBER", err.Error(), nil)
		case errors.Is(err, service.ErrUserUnavailable):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "USER_UNAVAILABLE", err.Error(), nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
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

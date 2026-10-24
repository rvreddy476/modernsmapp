package http

import (
	"context"
	"errors"
	"fmt"
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
	// SR-3: when true, a mutating request from an unrecognised caller is
	// refused rather than logged. See canonical_guard.go.
	strictWriteSource bool
	warn              func(string, ...any)
	// relationships serves the two relationship read routes. It is svc in
	// production; tests substitute a fake to pin the wire contract.
	relationships relationshipReader
	// permissions serves /v1/permissions/check and /check-batch. It is svc in
	// production; tests substitute a fake to pin the response shape.
	permissions permissionResolver
	// connections serves the follower / following list routes and the
	// audience rule in front of them (connections_rule.go). It is svc in
	// production; tests substitute a fake.
	connections connectionsReader
	// legacyConnections serves the connection routes that still work after
	// connections were retired (24 Oct 2026): the reads of existing data and
	// the routes that only remove (decline, cancel, remove). It is svc in
	// production; tests substitute a fake.
	legacyConnections legacyConnectionStore
}

// legacyConnectionStore is the slice of the service the surviving
// connection routes need. Nothing in it creates a connection or a request.
type legacyConnectionStore interface {
	GetConnections(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error)
	GetPendingConnectionRequests(ctx context.Context, userID uuid.UUID) ([]store.ConnectionRequest, error)
	GetSentConnectionRequests(ctx context.Context, userID uuid.UUID) ([]store.ConnectionRequest, error)
	DeclineConnectionRequest(ctx context.Context, senderID, receiverID uuid.UUID) error
	CancelConnectionRequest(ctx context.Context, senderID, receiverID uuid.UUID) error
	RemoveConnection(ctx context.Context, userA, userB uuid.UUID) error
}

// connectionsReader is the slice of the service the connection-list routes
// need: the lists themselves and the three facts the audience rule asks.
type connectionsReader interface {
	IsBlockedBy(ctx context.Context, blockerID, candidateID uuid.UUID) (bool, error)
	IsFollowing(ctx context.Context, followerID, followeeID uuid.UUID) (bool, error)
	IsPrivateAccount(ctx context.Context, userID uuid.UUID) bool
	GetFollowers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error)
	GetFollowing(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error)
	GetFollowingIDs(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error)
}

// permissionResolver is the slice of the service the permission routes need.
type permissionResolver interface {
	ResolvePermissions(ctx context.Context, actorID, targetID uuid.UUID, actions []permission.Action) (map[permission.Action]permission.Decision, error)
}

// relationshipReader is the slice of the service the relationship read
// routes need.
type relationshipReader interface {
	GetRelationship(ctx context.Context, actorID, targetID uuid.UUID) (*service.Relationship, error)
	GetRelationshipBatch(ctx context.Context, viewerID uuid.UUID, targetIDs []uuid.UUID) (map[uuid.UUID]store.Relationship, error)
}

// WithCanonicalWriteSource enables the source guard on mutating routes.
// strict=false is a rollout aid only — it logs unknown callers and lets them
// through, which means the duplicate-writer hole is still open.
func (h *Handler) WithCanonicalWriteSource(strict bool, warn func(string, ...any)) *Handler {
	h.strictWriteSource = strict
	h.warn = warn
	return h
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc, relationships: svc, permissions: svc, connections: svc, legacyConnections: svc}
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
	// SR-3: attribute every mutating graph write to a named caller. The
	// internal key authenticates but is shared, so it cannot tell one service
	// from another — this is what makes a second, unreviewed graph writer
	// visible instead of silent.
	v1.Use(RequireCanonicalWriteSource(h.strictWriteSource, h.warn))
	{
		v1.POST("/follow", h.Follow)
		v1.POST("/unfollow", h.Unfollow)
		v1.POST("/block", h.Block)
		v1.DELETE("/block", h.Unblock)
		v1.GET("/relationship", h.GetRelationship)
		v1.GET("/counts/:userId", h.GetCounts)
		v1.GET("/followers/:userId", h.GetFollowers)
		v1.GET("/following/:userId", h.GetFollowing)
		// GetFollowingIDs is a slim variant used by search-service,
		// dating-service and live-service-v2 to drive author-affinity and
		// "followed creators". Returns up to 500 IDs in one shot, no
		// pagination overhead. It is mounted on the PUBLIC group, so it
		// sits behind the same audience rule as the paginated lists
		// (security sweep #7): the in-cluster callers name the owner as the
		// viewer or no viewer at all, and pass; a user the owner blocked or
		// a stranger to a private account gets the empty list.
		v1.GET("/:userId/following-ids", h.GetFollowingIDs)
		v1.GET("/mutuals", h.GetMutualFollowers)

		// Mute
		v1.POST("/mute", h.Mute)
		v1.DELETE("/mute", h.Unmute)
		// Viewer-scoped public contract. Internal callers that need to name a
		// subject use /v1/internal/graph below, which the gateway does not proxy.
		v1.GET("/blocked-and-muted", h.GetBlockedAndMuted)
		// Batch relationship lookup
		v1.POST("/relationships/batch", h.GetRelationshipBatch)
		// How many friends (mutual follows) the viewer shares with each
		// target, for a whole list in one query. The path keeps its old
		// "connections" name for existing callers; since 24 Oct 2026 it
		// counts mutual follows. POST because the input is a list of ids;
		// it reads, so it is registered in readOnlyPostRoutes.
		v1.POST("/connections/mutual-counts", h.GetMutualConnectionCounts)

		// Connections (formerly "friends" — spec §3.2/§19). RETIRED 24 Oct
		// 2026 (founder decision B: anyone can message anyone by their
		// settings; no connection requests anywhere). Sending and accepting
		// answer 410 CONNECTIONS_RETIRED, so nothing new is created. Existing
		// connections and pending requests stay readable, and the routes
		// that only remove (decline, cancel, filter, unfilter, remove) keep
		// working so old data can still be cleared.
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

		// Follow requests (private accounts). Same middleware + write-source
		// guard as the connection-request routes above: POST/DELETE mutate
		// and must name an approved writer; the GET is unguarded like every
		// other read.
		v1.POST("/follow-requests", h.RequestFollow)
		v1.GET("/follow-requests/incoming", h.ListIncomingFollowRequests)
		v1.POST("/follow-requests/:requesterId/accept", h.AcceptFollowRequest)
		v1.POST("/follow-requests/:requesterId/decline", h.DeclineFollowRequest)
		v1.DELETE("/follow-requests/:targetId", h.CancelFollowRequest)

		// REMOVED 21 Sep: /close-friends, /circles, /labels and /favorites.
		//
		// The product now has ONE relationship concept, Connections.
		// close-friends was the "Trusted Circle" second tier; circles,
		// labels and favorites were built and never called by any client
		// or service. All five tables were empty and are dropped in
		// migration 012.
	}

	internal := r.Group("/v1/internal/graph")
	internal.GET("/blocked-and-muted", h.GetBlockedAndMutedInternal)
	internal.POST("/blocked-any", h.BlockedAnyInternal)
	// Retired 24 Oct 2026 with the connection routes: answers 410.
	internal.POST("/connections/ensure", h.EnsureConnectionInternal)
	// Content gating for post/feed/comment services: may viewer view_posts /
	// comment against each target. Internal-key only; the gateway does not
	// proxy /v1/internal.
	internal.POST("/can", h.CanInternal)

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
// for.
//
// It is DERIVED from the server's own privacy cache rather than written down
// separately. It used to be a hard-coded 60 while the cache was also 60; when
// the cache dropped to 3 seconds to close the privacy hole in B-LB-2, a
// literal here would have kept telling every client it may hold a decision for
// a minute — so a user who set themselves to `no_one` would still be
// messageable by any client that believed the advertised budget, no matter how
// fresh the server had become.
//
// A number a client caches against must never be larger than the number the
// server refreshes at.
var permissionTTLSeconds = int(service.PrivacyCacheTTL.Seconds())

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

	decisions, err := h.permissions.ResolvePermissions(c.Request.Context(), actorID, targetID, actions)
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
		decisions, err := h.permissions.ResolvePermissions(c.Request.Context(), actorID, targetID, actions)
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

// boundActor resolves the actor of a viewer-scoped read that also accepts an
// explicit id (`user_id` on /relationship and /mutuals, `viewer_id` on
// /relationships/batch) without letting one user name another and read that
// person's blocks, mutes and follows (security sweep, 21 Oct 2026: the
// relationship contract promises "never 'blocked'" to the blocked party, and
// an explicit actor id handed exactly that away).
//
// The gateway stamps X-Graph-Write-Source on EVERY /v1/graph request, reads
// included, after stripping any client-supplied value — so its presence means
// edge traffic: the actor is the gateway-set X-User-Id (absent → 401) and an
// explicit id may only repeat it, else 400 VIEWER_BOUND, the rule
// GetBlockedAndMuted already applies. Without the header the caller is an
// in-cluster service behind the internal key (dating, feed, live) that names
// the actor explicitly, as before.
func boundActor(c *gin.Context, explicit string) (uuid.UUID, bool) {
	if c.GetHeader(GraphWriteSourceHeader) == "" {
		id, err := uuid.Parse(explicit)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user_id", nil)
			return uuid.Nil, false
		}
		return id, true
	}
	actor, ok := getUserID(c)
	if !ok {
		return uuid.Nil, false
	}
	if explicit != "" && explicit != actor.String() {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"VIEWER_BOUND", "user_id cannot name another viewer", nil)
		return uuid.Nil, false
	}
	return actor, true
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
	// status is "followed" (edge created, public target) or "requested"
	// (private target — a pending follow request was created instead).
	status, err := h.svc.Follow(c.Request.Context(), followerID, followeeID)
	if err != nil {
		if errors.Is(err, service.ErrRateLimited) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil)
			return
		}
		if errors.Is(err, service.ErrWrongEntityType) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "WRONG_ENTITY_TYPE", "follow is only valid against a page", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": status}, nil)
}

// ── Follow requests (private accounts) ───────────────────────

// RequestFollow handles POST /v1/graph/follow-requests {user_id: target}.
// X-User-Id is the requester. Public target → "following"; private target
// → "requested".
func (h *Handler) RequestFollow(c *gin.Context) {
	requesterID, targetID, ok := parseAuthAndBody(c)
	if !ok {
		return
	}
	status, err := h.svc.RequestFollow(c.Request.Context(), requesterID, targetID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrRateLimited):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil)
		case errors.Is(err, service.ErrWrongEntityType):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "WRONG_ENTITY_TYPE", "follow request is only valid against a user or page", nil)
		case errors.Is(err, service.ErrCannotFollowSelf):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CANNOT_FOLLOW_SELF", err.Error(), nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": status}, nil)
}

// parseFollowRequestParty extracts the X-User-Id caller and the counterparty
// named by the given path param.
func parseFollowRequestParty(c *gin.Context, param string) (uuid.UUID, uuid.UUID, bool) {
	callerID, ok := getUserID(c)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	otherID, err := uuid.Parse(c.Param(param))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid "+param, nil)
		return uuid.Nil, uuid.Nil, false
	}
	return callerID, otherID, true
}

func writeFollowRequestError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrNoPendingFollowRequest) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
}

// AcceptFollowRequest handles POST /v1/graph/follow-requests/:requesterId/accept.
// X-User-Id is the target approving the request.
func (h *Handler) AcceptFollowRequest(c *gin.Context) {
	targetID, requesterID, ok := parseFollowRequestParty(c, "requesterId")
	if !ok {
		return
	}
	if err := h.svc.AcceptFollowRequest(c.Request.Context(), targetID, requesterID); err != nil {
		writeFollowRequestError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "accepted"}, nil)
}

// DeclineFollowRequest handles POST /v1/graph/follow-requests/:requesterId/decline.
func (h *Handler) DeclineFollowRequest(c *gin.Context) {
	targetID, requesterID, ok := parseFollowRequestParty(c, "requesterId")
	if !ok {
		return
	}
	if err := h.svc.DeclineFollowRequest(c.Request.Context(), targetID, requesterID); err != nil {
		writeFollowRequestError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "declined"}, nil)
}

// CancelFollowRequest handles DELETE /v1/graph/follow-requests/:targetId.
// X-User-Id is the requester withdrawing their own request.
func (h *Handler) CancelFollowRequest(c *gin.Context) {
	requesterID, targetID, ok := parseFollowRequestParty(c, "targetId")
	if !ok {
		return
	}
	if err := h.svc.CancelFollowRequest(c.Request.Context(), requesterID, targetID); err != nil {
		writeFollowRequestError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "cancelled"}, nil)
}

// followRequestItem is the incoming-inbox row shape.
type followRequestItem struct {
	RequesterID uuid.UUID `json:"requester_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListIncomingFollowRequests handles GET /v1/graph/follow-requests/incoming
// ?limit=&cursor= for the X-User-Id caller, newest first.
// Response: {"data":[{"requester_id","created_at"}],"meta":{"next_cursor"}}.
func (h *Handler) ListIncomingFollowRequests(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}
	reqs, next, err := h.svc.ListIncomingFollowRequests(c.Request.Context(), userID, limit, c.Query("cursor"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	items := make([]followRequestItem, 0, len(reqs))
	for _, r := range reqs {
		items = append(items, followRequestItem{RequesterID: r.RequesterID, CreatedAt: r.CreatedAt})
	}
	api.JSON(c.Writer, http.StatusOK, items, &api.Meta{NextCursor: next})
}

// CanInternal handles POST /v1/internal/graph/can
// {"viewer_id","action":"view_posts"|"comment"|"mention"|"tag","target_ids":[...]}
// → {"data":{"<target_id>":true|false}}. Fails closed: a privacy-fetch
// failure resolves to deny inside the service; a graph-store failure is a
// 500 the caller must treat as deny.
func (h *Handler) CanInternal(c *gin.Context) {
	var req struct {
		ViewerID  string   `json:"viewer_id" binding:"required"`
		Action    string   `json:"action" binding:"required"`
		TargetIDs []string `json:"target_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	viewerID, err := uuid.Parse(req.ViewerID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid viewer_id", nil)
		return
	}
	action, known := permission.ParseCanAction(req.Action)
	if !known || !service.IsCanBatchAction(action) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "action must be view_posts, comment, mention or tag", nil)
		return
	}
	if len(req.TargetIDs) > store.MaxRelationshipBatch {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BATCH_TOO_LARGE",
			fmt.Sprintf("at most %d target_ids per call", store.MaxRelationshipBatch), nil)
		return
	}
	targetIDs := make([]uuid.UUID, 0, len(req.TargetIDs))
	for _, raw := range req.TargetIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			// Same rule as the relationship batch: a malformed id fails the
			// request rather than silently vanishing from a safety answer.
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid target_id", nil)
			return
		}
		targetIDs = append(targetIDs, id)
	}
	result, err := h.svc.CanBatch(c.Request.Context(), viewerID, action, targetIDs)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) Unfollow(c *gin.Context) {
	followerID, followeeID, ok := parseAuthAndBody(c)
	if !ok {
		return
	}
	if err := h.svc.Unfollow(c.Request.Context(), followerID, followeeID); err != nil {
		if errors.Is(err, service.ErrWrongEntityType) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "WRONG_ENTITY_TYPE", "unfollow is only valid against a page", nil)
			return
		}
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
		if errors.Is(err, service.ErrRateLimited) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "blocked"}, nil)
}

func (h *Handler) GetRelationship(c *gin.Context) {
	actorID, ok := boundActor(c, c.Query("user_id"))
	if !ok {
		return
	}
	targetID, err := uuid.Parse(c.Query("other_id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid other_id", nil)
		return
	}
	rel, err := h.relationships.GetRelationship(c.Request.Context(), actorID, targetID)
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
	ids, err := h.connections.GetFollowers(c.Request.Context(), userID, limit, offset)
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
	ids, err := h.connections.GetFollowing(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	api.JSON(c.Writer, http.StatusOK, ids, nil)
}

// GetFollowingIDs handles GET /v1/graph/:userId/following-ids?limit=500.
// Used by search-service, dating-service and live-service-v2 to fetch the
// follow graph slice that drives author-affinity boosts and "followed
// creators". Cap is 500 so the resulting OpenSearch `terms` array stays
// bounded. Same audience rule as the paginated lists (security sweep #7):
// a refused caller gets the empty page, not an error.
func (h *Handler) GetFollowingIDs(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid userId", nil)
		return
	}
	if !h.callerAllowedToReadConnections(c, userID) {
		api.JSON(c.Writer, http.StatusOK, gin.H{"items": []uuid.UUID{}, "count": 0}, nil)
		return
	}
	limit := 500
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 500 {
			limit = v
		}
	}
	ids, err := h.connections.GetFollowingIDs(c.Request.Context(), userID, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": ids, "count": len(ids)}, nil)
}

func (h *Handler) GetMutualFollowers(c *gin.Context) {
	otherIDStr := c.Query("other_id")
	userID, ok := boundActor(c, c.Query("user_id"))
	if !ok {
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

// CodeConnectionsRetired is the error code of every retired connection route.
const CodeConnectionsRetired = "CONNECTIONS_RETIRED"

// refuseConnectionsRetired answers a retired connection route: 410 with a
// stable code, without reading the body or touching the service, so no
// caller (an old app build, a stale service image) can create anything.
func refuseConnectionsRetired(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, CodeConnectionsRetired,
		"Connections are retired. Follow people, or message them as their settings allow.", nil)
}

// SendConnectionRequest is retired (24 Oct 2026): 410 CONNECTIONS_RETIRED.
// The service's SendConnectionRequest, and the auto-accept inside it when
// the other person had already asked, are no longer reachable.
func (h *Handler) SendConnectionRequest(c *gin.Context) {
	refuseConnectionsRetired(c)
}

// AcceptConnectionRequest is retired with sending: accepting a request that
// was pending before the retirement would still create a new connection.
// Such a request can be declined (receiver) or cancelled (sender).
func (h *Handler) AcceptConnectionRequest(c *gin.Context) {
	refuseConnectionsRetired(c)
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
	if err := h.legacyConnections.DeclineConnectionRequest(c.Request.Context(), senderID, receiverID); err != nil {
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
	if err := h.legacyConnections.CancelConnectionRequest(c.Request.Context(), senderID, receiverID); err != nil {
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
	if err := h.legacyConnections.RemoveConnection(c.Request.Context(), actorID, targetID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

// GetConnections lists a user's (legacy) connections. It sits behind the
// same audience rule as the follower / following lists
// (callerAllowedToReadConnections): the owner always; a signed-in user the
// owner blocked, or a non-follower of a private account, gets the empty
// list; a signed-out edge caller reads a public account's list only.
// Hidden accounts are left out of the list by the store.
func (h *Handler) GetConnections(c *gin.Context) {
	userID, limit, offset, ok := parsePaginatedUserID(c)
	if !ok {
		return
	}
	if !h.callerAllowedToReadConnections(c, userID) {
		api.JSON(c.Writer, http.StatusOK, []uuid.UUID{}, nil)
		return
	}
	ids, err := h.legacyConnections.GetConnections(c.Request.Context(), userID, limit, offset)
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
	reqs, err := h.legacyConnections.GetPendingConnectionRequests(c.Request.Context(), userID)
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
	reqs, err := h.legacyConnections.GetSentConnectionRequests(c.Request.Context(), userID)
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
		if errors.Is(err, service.ErrRateLimited) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil)
			return
		}
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
		if errors.Is(err, service.ErrRateLimited) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil)
			return
		}
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
	if err := h.svc.Unmute(c.Request.Context(), userID, mutedID); err != nil {
		if errors.Is(err, service.ErrRateLimited) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	c.Status(http.StatusNoContent)
}

// GetBlockedAndMuted returns the user IDs whose content must be withheld
// from the given viewer: blocks in BOTH directions, plus the viewer's own
// outgoing mutes. Reverse mutes are never included.
//
// There is deliberately no parameter to select a narrower set. An earlier
// revision offered `include=blocks`, which search used in order to keep
// muted accounts findable by name. That reversed the approved contract,
// and — worse — it made "return fewer suppressions than the caller
// probably needs" a one-word opt-in. Both callers now get the full set.
func (h *Handler) GetBlockedAndMuted(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	if requested := c.Query("user_id"); requested != "" && requested != userID.String() {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"VIEWER_BOUND", "user_id cannot name another viewer", nil)
		return
	}
	h.writeBlockedAndMuted(c, userID, true)
}

func (h *Handler) GetBlockedAndMutedInternal(c *gin.Context) {
	userID, err := uuid.Parse(c.Query("user_id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user_id", nil)
		return
	}
	h.writeBlockedAndMuted(c, userID, false)
}

// BlockedAnyInternal answers chat's group-roster gate (P0-5): which of the
// named candidates hold a block with user_id in either direction. Bounded to
// the group cap; only the matching subset of caller-supplied ids is returned,
// never the adjacency itself.
func (h *Handler) BlockedAnyInternal(c *gin.Context) {
	var req struct {
		UserID       uuid.UUID   `json:"user_id" binding:"required"`
		CandidateIDs []uuid.UUID `json:"candidate_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	const candidateCap = 1024
	if len(req.CandidateIDs) > candidateCap {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
			"too many candidate ids", nil)
		return
	}
	blocked, err := h.svc.BlockedWithAny(c.Request.Context(), req.UserID, req.CandidateIDs)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if blocked == nil {
		blocked = []uuid.UUID{}
	}
	c.JSON(http.StatusOK, gin.H{"blocked_user_ids": blocked})
}

func (h *Handler) writeBlockedAndMuted(c *gin.Context, userID uuid.UUID, envelope bool) {
	ids, err := h.svc.GetBlockedAndMuted(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if ids == nil {
		ids = []uuid.UUID{}
	}
	if envelope {
		api.JSON(c.Writer, http.StatusOK, gin.H{"user_ids": ids}, nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user_ids": ids})
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
	viewerID, ok := boundActor(c, req.ViewerID)
	if !ok {
		return
	}
	// M4-P0-1: an unparseable target used to be skipped silently. The caller
	// then received a map with no entry for it, which every existing consumer
	// reads as "no relationship" — i.e. no block, no restriction. A malformed
	// identifier must fail the request, not quietly widen an audience.
	if len(req.TargetIDs) > store.MaxRelationshipBatch {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BATCH_TOO_LARGE",
			fmt.Sprintf("at most %d target_ids per call", store.MaxRelationshipBatch), nil)
		return
	}
	targetIDs := make([]uuid.UUID, 0, len(req.TargetIDs))
	for _, id := range req.TargetIDs {
		uid, err := uuid.Parse(id)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID",
				"Invalid target_id", nil)
			return
		}
		targetIDs = append(targetIDs, uid)
	}
	result, err := h.relationships.GetRelationshipBatch(c.Request.Context(), viewerID, targetIDs)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	c.JSON(http.StatusOK, result)
}

// GetMutualConnectionCounts answers "N mutual friends" for a list of people
// at once (People you may know). Since connections were retired (24 Oct
// 2026) a friend is a mutual follow (store.MutualFollowCounts); the route
// and response shape are unchanged. The pre-existing /v1/graph/mutuals is
// per-pair and lists people who follow both, which is neither the right
// question nor the right shape for a grid of cards.
func (h *Handler) GetMutualConnectionCounts(c *gin.Context) {
	var req struct {
		ViewerID  string   `json:"viewer_id"`
		TargetIDs []string `json:"target_ids"`
	}
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	// Edge traffic answers for the signed-in viewer only, as
	// /relationships/batch does: a body viewer_id must not name someone else,
	// or anyone could map another person's connections through the counts.
	viewerID, ok := boundActor(c, req.ViewerID)
	if !ok {
		return
	}
	if len(req.TargetIDs) > store.MaxRelationshipBatch {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BATCH_TOO_LARGE",
			fmt.Sprintf("at most %d target_ids per call", store.MaxRelationshipBatch), nil)
		return
	}
	targetIDs := make([]uuid.UUID, 0, len(req.TargetIDs))
	for _, id := range req.TargetIDs {
		uid, err := uuid.Parse(id)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid target_id", nil)
			return
		}
		targetIDs = append(targetIDs, uid)
	}
	counts, err := h.svc.MutualFollowCounts(c.Request.Context(), viewerID, targetIDs)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// Every requested id is present, zero included: an absent key reads as
	// "unknown" on the client and would render as a blank line.
	out := make(map[string]int, len(targetIDs))
	for _, id := range targetIDs {
		out[id.String()] = counts[id]
	}
	c.JSON(http.StatusOK, gin.H{"counts": out})
}

// EnsureConnectionInternal is retired with the user-facing routes (24 Oct
// 2026): 410 CONNECTIONS_RETIRED. It made two users connections directly
// (no block check, no event). message-service called it when a message
// request was accepted until the DM consent model (20 Oct 2026), which made
// accepting open the chat and nothing more; that call was best-effort and
// only logged a failure, so an older message-service image still accepts.
// Nothing calls it now.
func (h *Handler) EnsureConnectionInternal(c *gin.Context) {
	refuseConnectionsRetired(c)
}

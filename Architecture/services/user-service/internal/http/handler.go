package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/httpclient"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/atpost/user-service/internal/events"
	"github.com/atpost/user-service/internal/presence"
	"github.com/atpost/user-service/internal/service"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc              *service.Service
	store            *store.Store
	graphURL         string
	presenceStore    *presence.Store
	graphClient      *http.Client
	internalKey      string
	internalRouteKey string
	dlqConsumer      *events.Consumer
}

func New(svc *service.Service, presenceStore *presence.Store, st *store.Store) *Handler {
	graphURL := os.Getenv("GRAPH_SERVICE_URL")
	if graphURL == "" {
		graphURL = "http://graph-service:8083"
	}
	h := &Handler{svc: svc, store: st, graphURL: graphURL, presenceStore: presenceStore}
	h.graphClient = httpclient.NewWithBreaker(5*time.Second, "user->graph")
	return h
}

// WithDLQConsumer wires the Kafka consumer so the /internal DLQ routes can
// list and replay parked events.
func (h *Handler) WithDLQConsumer(c *events.Consumer) *Handler {
	h.dlqConsumer = c
	return h
}

func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

// WithInternalRoutes enables the X-Internal-Service-Key gate on the
// service-to-service /internal/* routes only — without gating the
// gateway-facing /v1/* surface (those carry an end-user JWT, not the key).
func (h *Handler) WithInternalRoutes(key string) *Handler {
	h.internalRouteKey = key
	return h
}

// EnsureUser repairs the local app.users projection for :userId on demand.
// 200 — the row exists or was just rebuilt from profile-service.
// 404 — the user does not exist in the identity source either.
// 503 — the repair source is unreachable; the caller may retry or proceed.
func (h *Handler) EnsureUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return
	}
	if err := h.svc.EnsureUser(c.Request.Context(), id); err != nil {
		switch {
		case errors.Is(err, service.ErrUserNotInIdentity):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "USER_NOT_FOUND", "user does not exist", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "REPAIR_FAILED", err.Error(), nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

// ListDLQ returns parked (failed) Kafka events. ?all=true includes already-
// replayed entries; the default is unreplayed only.
func (h *Handler) ListDLQ(c *gin.Context) {
	entries, err := h.store.ListDLQ(c.Request.Context(), c.Query("all") != "true", 200)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if entries == nil {
		entries = []store.DLQEntry{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": entries, "count": len(entries)}, nil)
}

// ReplayDLQ re-dispatches one parked event through the consumer's handlers.
// Handlers are idempotent, so replaying an already-applied event is safe.
func (h *Handler) ReplayDLQ(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid dlq id", nil)
		return
	}
	if h.dlqConsumer == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "REPLAY_UNAVAILABLE", "replay not wired", nil)
		return
	}
	if err := h.dlqConsumer.ReplayOne(c.Request.Context(), id); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPLAY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true, "id": id}, nil)
}

// ProjectionHealth reports whether the app.users projection is converged with
// the identity master — master vs. local counts, reconcile status, DLQ depth.
func (h *Handler) ProjectionHealth(c *gin.Context) {
	report, err := h.svc.ProjectionHealth(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, report, nil)
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}

	// Service-to-service routes — gated independently of the global key so
	// they can be locked down even while /v1/* stays gateway-facing.
	internal := r.Group("/internal")
	if h.internalRouteKey != "" {
		internal.Use(sharedmiddleware.RequireInternalKey(h.internalRouteKey))
	}
	// Read-through projection repair: callers (e.g. graph-service) hit this
	// before an action that depends on app.users having the row.
	internal.POST("/users/:userId/ensure", h.EnsureUser)
	// Dead-letter queue: inspect + replay events the consumer failed on.
	internal.GET("/dlq", h.ListDLQ)
	internal.POST("/dlq/:id/replay", h.ReplayDLQ)
	// Projection health: master vs. local counts, reconcile + DLQ status.
	internal.GET("/projection/health", h.ProjectionHealth)

	v1 := r.Group("/v1/users")
	{
		v1.GET("/by-username/:username", h.GetUserByUsername)
		v1.GET("/:userId", h.GetUser)
		v1.GET("/:userId/channels", h.GetUserChannels)
		v1.GET("/:userId/links", h.GetUserLinks)
		v1.PUT("/me", h.UpdateMe)
		v1.GET("/me", h.GetMe)
		v1.PUT("/me/links", h.UpdateMyLinks)
		v1.GET("/me/settings", h.GetMySettings)
		v1.PUT("/me/settings", h.UpdateMySettings)

		// About
		v1.GET("/:userId/about", h.GetAbout)
		v1.GET("/:userId/about/:section", h.GetAboutSection)
		v1.PUT("/me/about/:section", h.UpsertAboutItem)
		v1.DELETE("/me/about/:section/:itemId", h.DeleteAboutItem)

		// Status/Mood
		v1.PATCH("/me/status", h.UpdateStatus)

		// Reputation & Endorsements
		v1.GET("/:userId/reputation", h.GetReputation)
		v1.GET("/:userId/endorsements", h.GetEndorsements)
		v1.POST("/:userId/endorse", h.EndorseUser)

		// Compatibility
		v1.GET("/:userId/compatibility", h.GetCompatibility)

		// Link Analytics
		v1.POST("/links/:platform/click", h.TrackLinkClick)
		v1.GET("/me/links/analytics", h.GetLinkAnalytics)

		// Presence
		v1.POST("/me/heartbeat", h.Heartbeat)
		v1.POST("/online/batch", h.GetOnlineStatusBatch)
		v1.GET("/:userId/online", h.GetOnlineStatus)
	}

	// Channels
	myChannels := r.Group("/v1/users/me/channels")
	{
		myChannels.POST("", h.CreateChannel)
		myChannels.GET("", h.ListMyChannels)
	}
	channelByID := r.Group("/v1/channels/:id")
	{
		channelByID.GET("", h.GetChannel)
		channelByID.PATCH("", h.UpdateChannel)
		channelByID.DELETE("", h.DeleteChannel)
		channelByID.POST("/subscribe", h.SubscribeToChannel)
		channelByID.DELETE("/subscribe", h.UnsubscribeFromChannel)
		channelByID.GET("/subscription", h.GetChannelSubscriptionStatus)
		channelByID.GET("/subscribers", h.ListChannelSubscribers)
	}

	// User subscriptions list
	v1.GET("/:userId/subscriptions", h.ListUserChannelSubscriptions)

	// Profile extras: pins, portfolio, QR codes, digital wellbeing
	h.registerProfileExtrasRoutes(r)

	// Onboarding
	r.POST("/v1/onboarding/ensure-publisher", h.EnsurePublisher)

	// Business Pages — use :id for all sub-paths (Gin requires uniform wildcard names)
	pages := r.Group("/v1/pages")
	{
		pages.GET("", h.DiscoverPages)
		pages.GET("/:id", h.GetBusinessPage)
		pages.PATCH("/:id", h.UpdateBusinessPage)
		pages.DELETE("/:id", h.DeleteBusinessPage)
		pages.POST("/:id/follow", h.FollowPage)
		pages.DELETE("/:id/follow", h.UnfollowPage)
		pages.GET("/:id/reviews", h.GetPageReviews)
		pages.POST("/:id/reviews", h.SubmitReview)
	}
	myPages := r.Group("/v1/users/me/pages")
	{
		myPages.POST("", h.CreateBusinessPage)
		myPages.GET("", h.ListMyBusinessPages)
	}
}

func (h *Handler) GetUserChannels(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	channels, err := h.svc.GetUserChannels(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if channels == nil {
		channels = []store.Channel{}
	}
	api.JSON(c.Writer, http.StatusOK, channels, nil)
}

func (h *Handler) GetUser(c *gin.Context) {
	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	u, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if u == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetUserByUsername(c *gin.Context) {
	username := c.Param("username")
	if username == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Username is required", nil)
		return
	}

	u, err := h.svc.GetUserByUsername(c.Request.Context(), username)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if u == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetMe(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	if userIDStr == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil)
		return
	}
	userID, _ := uuid.Parse(userIDStr)

	u, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch profile", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, u, nil)
}

type UpdateProfileRequest struct {
	DisplayName   string     `json:"display_name"`
	Bio           string     `json:"bio"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
	CoverMediaID  *uuid.UUID `json:"cover_media_id"`
	FirstName     *string    `json:"first_name"`
	LastName      *string    `json:"last_name"`
	Gender        *string    `json:"gender"`
	DoB           *time.Time `json:"dob"`
	Username      *string    `json:"username"`
	Category      *string    `json:"category"`
	Profession    *string    `json:"profession"`
	Website       *string    `json:"website"`
	Location      *string    `json:"location"`
}

func (h *Handler) UpdateMe(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	u, err := h.svc.UpdateUser(c.Request.Context(), userID,
		req.DisplayName, req.Bio, req.AvatarMediaID, req.CoverMediaID,
		req.FirstName, req.LastName, req.Gender,
		req.Username, req.Category, req.Profession, req.Website, req.Location,
		req.DoB,
	)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetUserLinks(c *gin.Context) {
	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	links, err := h.svc.GetUserLinks(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if links == nil {
		links = []store.UserLink{}
	}

	api.JSON(c.Writer, http.StatusOK, links, nil)
}

type UpdateLinksRequest struct {
	Links []LinkItem `json:"links"`
}

type LinkItem struct {
	Platform     string `json:"platform"`
	URL          string `json:"url"`
	DisplayLabel string `json:"display_label"`
	SortOrder    int    `json:"sort_order"`
}

func (h *Handler) UpdateMyLinks(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req UpdateLinksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	links := make([]store.UserLink, len(req.Links))
	for i, l := range req.Links {
		links[i] = store.UserLink{
			UserID:       userID,
			Platform:     l.Platform,
			URL:          l.URL,
			DisplayLabel: l.DisplayLabel,
			SortOrder:    l.SortOrder,
		}
	}

	if err := h.svc.UpdateUserLinks(c.Request.Context(), userID, links); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, links, nil)
}

func (h *Handler) GetMySettings(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	s, err := h.svc.GetSettings(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, s, nil)
}

func (h *Handler) UpdateMySettings(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req store.UserSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	req.UserID = userID

	s, err := h.svc.UpdateSettings(c.Request.Context(), &req)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, s, nil)
}

// --- About ---

// newGraphRequest builds a GET to graph-service carrying the internal service
// key. graph-service gates every /v1/graph route behind that key — a call
// without it returns 401, which the privacy-gate code reads as "no
// connections" and then silently marks everyone offline. Every graph call
// from this service MUST go through here.
func (h *Handler) newGraphRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if h.internalRouteKey != "" {
		req.Header.Set("X-Internal-Service-Key", h.internalRouteKey)
	}
	return req, nil
}

// resolveViewerAccess determines the viewer's access level by calling graph-service.
func (h *Handler) resolveViewerAccess(ctx *gin.Context, ownerID uuid.UUID) service.ViewerAccess {
	viewerIDStr := ctx.GetHeader("X-User-Id")
	viewerID, err := uuid.Parse(viewerIDStr)
	if err != nil {
		return service.ViewerAccess{}
	}
	if viewerID == ownerID {
		return service.ViewerAccess{IsSelf: true}
	}

	url := fmt.Sprintf("%s/v1/graph/relationship?user_id=%s&other_id=%s", h.graphURL, viewerID, ownerID)
	graphReq, err := h.newGraphRequest(ctx.Request.Context(), url)
	if err != nil {
		return service.ViewerAccess{}
	}
	resp, err := h.graphClient.Do(graphReq)
	if err != nil {
		return service.ViewerAccess{}
	}
	defer resp.Body.Close()

	var body struct {
		Data struct {
			FollowedBy bool `json:"followed_by"`
			IsFriend   bool `json:"is_friend"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return service.ViewerAccess{}
	}

	return service.ViewerAccess{
		IsFollower: body.Data.FollowedBy,
		IsFriend:   body.Data.IsFriend,
	}
}

func (h *Handler) GetAbout(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	access := h.resolveViewerAccess(c, userID)
	items, err := h.svc.GetAllAbout(c.Request.Context(), userID, access)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if items == nil {
		items = make(map[string][]store.AboutItem)
	}

	api.JSON(c.Writer, http.StatusOK, items, nil)
}

func (h *Handler) GetAboutSection(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	section := c.Param("section")

	access := h.resolveViewerAccess(c, userID)
	items, err := h.svc.GetAboutSection(c.Request.Context(), userID, section, access)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if items == nil {
		items = []store.AboutItem{}
	}

	api.JSON(c.Writer, http.StatusOK, items, nil)
}

type UpsertAboutRequest struct {
	ItemID     *string         `json:"item_id"`
	Data       json.RawMessage `json:"data" binding:"required"`
	Visibility string          `json:"visibility"`
	SortOrder  int             `json:"sort_order"`
}

func (h *Handler) UpsertAboutItem(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	section := c.Param("section")

	var req UpsertAboutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	visibility := req.Visibility
	if visibility == "" {
		visibility = "public"
	}

	var itemID uuid.UUID
	if req.ItemID != nil {
		itemID, err = uuid.Parse(*req.ItemID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid item_id", nil)
			return
		}
	}

	item := &store.AboutItem{
		UserID:     userID,
		Section:    section,
		ItemID:     itemID,
		Data:       req.Data,
		Visibility: visibility,
		SortOrder:  req.SortOrder,
	}

	result, err := h.svc.UpsertAboutItem(c.Request.Context(), item)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) DeleteAboutItem(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	section := c.Param("section")
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid item ID", nil)
		return
	}

	if err := h.svc.DeleteAboutItem(c.Request.Context(), userID, section, itemID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

// --- Status/Mood ---

type UpdateStatusRequest struct {
	StatusText  string     `json:"status_text"`
	StatusEmoji string     `json:"status_emoji"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

func (h *Handler) UpdateStatus(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req UpdateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	if err := h.svc.UpdateStatus(c.Request.Context(), userID, req.StatusText, req.StatusEmoji, req.ExpiresAt); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

// --- Reputation & Endorsements ---

func (h *Handler) GetReputation(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	rep, err := h.svc.GetReputation(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	summary, _ := h.svc.GetEndorsementSummary(c.Request.Context(), userID)
	if summary == nil {
		summary = []store.SkillEndorsementSummary{}
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"reputation":          rep,
		"endorsement_summary": summary,
	}, nil)
}

func (h *Handler) GetEndorsements(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	endorsements, err := h.svc.GetEndorsements(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if endorsements == nil {
		endorsements = []store.Endorsement{}
	}

	api.JSON(c.Writer, http.StatusOK, endorsements, nil)
}

type EndorseRequest struct {
	SkillTag string `json:"skill_tag" binding:"required"`
	Message  string `json:"message"`
}

func (h *Handler) EndorseUser(c *gin.Context) {
	fromUserID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	toUserID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	var req EndorseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	e := &store.Endorsement{
		FromUserID: fromUserID,
		ToUserID:   toUserID,
		SkillTag:   req.SkillTag,
		Message:    req.Message,
	}

	if err := h.svc.EndorseUser(c.Request.Context(), e); err != nil {
		if err.Error() == "CANNOT_ENDORSE_SELF" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CANNOT_ENDORSE_SELF", "Cannot endorse yourself", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, e, nil)
}

// --- Compatibility ---

func (h *Handler) GetCompatibility(c *gin.Context) {
	viewerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	otherID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	score, err := h.svc.GetCompatibility(c.Request.Context(), viewerID, otherID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]float64{"compatibility_score": score}, nil)
}

// --- Link Analytics ---

func (h *Handler) TrackLinkClick(c *gin.Context) {
	userIDStr := c.Query("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user_id query param", nil)
		return
	}
	platform := c.Param("platform")

	if err := h.svc.TrackLinkClick(c.Request.Context(), userID, platform); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "tracked"}, nil)
}

func (h *Handler) GetLinkAnalytics(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	analytics, err := h.svc.GetLinkAnalytics(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if analytics == nil {
		analytics = []store.LinkAnalytics{}
	}
	api.JSON(c.Writer, http.StatusOK, analytics, nil)
}

// --- Channels ---

type CreateChannelRequest struct {
	Handle          string `json:"handle" binding:"required"`
	Name            string `json:"name" binding:"required"`
	Description     string `json:"description"`
	Category        string `json:"category"`
	Country         string `json:"country"`
	Language        string `json:"language"`
	ContactEmail    string `json:"contact_email"`
	CollabStatus    string `json:"collab_status"`
	ContentSchedule string `json:"content_schedule"`
}

func (h *Handler) CreateChannel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req CreateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	ch := &store.Channel{
		UserID:          userID,
		Handle:          req.Handle,
		Name:            req.Name,
		Description:     req.Description,
		Category:        req.Category,
		Country:         req.Country,
		Language:        req.Language,
		ContactEmail:    req.ContactEmail,
		CollabStatus:    req.CollabStatus,
		ContentSchedule: req.ContentSchedule,
	}

	if err := h.svc.CreateChannel(c.Request.Context(), ch); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, ch, nil)
}

func (h *Handler) GetChannel(c *gin.Context) {
	handle := c.Param("id")
	detail, err := h.svc.GetChannel(c.Request.Context(), handle)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Channel not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, detail, nil)
}

func (h *Handler) ListMyChannels(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	channels, err := h.svc.GetUserChannels(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if channels == nil {
		channels = []store.Channel{}
	}
	api.JSON(c.Writer, http.StatusOK, channels, nil)
}

type UpdateChannelRequest struct {
	Name            *string    `json:"name"`
	Description     *string    `json:"description"`
	AvatarMediaID   *uuid.UUID `json:"avatar_media_id"`
	BannerMediaID   *uuid.UUID `json:"banner_media_id"`
	Category        *string    `json:"category"`
	Country         *string    `json:"country"`
	Language        *string    `json:"language"`
	ContactEmail    *string    `json:"contact_email"`
	CollabStatus    *string    `json:"collab_status"`
	ContentSchedule *string    `json:"content_schedule"`
}

func (h *Handler) UpdateChannel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	channelID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	var req UpdateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	upd := &store.ChannelUpdate{
		ID:              channelID,
		UserID:          userID,
		Name:            req.Name,
		Description:     req.Description,
		AvatarMediaID:   req.AvatarMediaID,
		BannerMediaID:   req.BannerMediaID,
		Category:        req.Category,
		Country:         req.Country,
		Language:        req.Language,
		ContactEmail:    req.ContactEmail,
		CollabStatus:    req.CollabStatus,
		ContentSchedule: req.ContentSchedule,
	}

	if err := h.svc.UpdateChannel(c.Request.Context(), upd); err != nil {
		if err.Error() == "CHANNEL_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Channel not found or not owned by you", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) DeleteChannel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	channelID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil)
		return
	}

	if err := h.svc.DeleteChannel(c.Request.Context(), channelID, userID); err != nil {
		if err.Error() == "CHANNEL_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Channel not found or not owned by you", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

// --- Onboarding ---

// EnsurePublisher handles POST /v1/onboarding/ensure-publisher.
// It atomically creates a handle and default channel if the user doesn't have them.
func (h *Handler) EnsurePublisher(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	result, err := h.svc.EnsurePublisher(c.Request.Context(), userID)
	if err != nil {
		if err.Error() == "user not found: "+userID.String() {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "USER_NOT_FOUND", "User not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to ensure publisher identity", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// --- Presence ---

// Heartbeat sets the calling user as online with a 90-second TTL.
// Clients should call this every 30 seconds to maintain online status.
func (h *Handler) Heartbeat(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	if err := h.presenceStore.SetOnline(c.Request.Context(), userID.String()); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update presence", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

// GetOnlineStatus returns whether the target user is online.
// Due to privacy rules, only circle members (mutual friends) receive a truthful answer.
// All other callers receive {"online": false} regardless of actual status (fail-closed).
func (h *Handler) GetOnlineStatus(c *gin.Context) {
	requesterIDStr := c.GetHeader("X-User-Id")
	requesterID, err := uuid.Parse(requesterIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	targetIDStr := c.Param("userId")
	targetID, err := uuid.Parse(targetIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	// Self-check: always truthful
	inCircle := requesterID == targetID

	if !inCircle {
		// Check mutual friendship (circle membership) via graph-service.
		// Fail-closed: if graph-service is unreachable, treat as not in circle.
		url := fmt.Sprintf("%s/v1/graph/relationship?user_id=%s&other_id=%s", h.graphURL, requesterID, targetID)
		graphReq, _ := h.newGraphRequest(c.Request.Context(), url)
		resp, err := h.graphClient.Do(graphReq)
		if err == nil {
			defer resp.Body.Close()
			var body struct {
				Data struct {
					IsFriend bool `json:"is_friend"`
				} `json:"data"`
			}
			if json.NewDecoder(resp.Body).Decode(&body) == nil {
				inCircle = body.Data.IsFriend
			}
		}
		// If err != nil, inCircle stays false (fail-closed for privacy)
	}

	if !inCircle {
		api.JSON(c.Writer, http.StatusOK, map[string]bool{"online": false}, nil)
		return
	}

	online, err := h.presenceStore.IsOnline(c.Request.Context(), targetID.String())
	if err != nil {
		// Fail-closed: on Redis error, report offline rather than leaking status
		api.JSON(c.Writer, http.StatusOK, map[string]bool{"online": false}, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]bool{"online": online}, nil)
}

// GetOnlineStatusBatch returns online status for many users in a single call,
// so a friends list does not need one request per friend. Privacy mirrors
// GetOnlineStatus: only the requester's connections (and the requester
// themselves) get a truthful answer; everyone else is reported offline
// (fail-closed). The batch is capped at 100 ids.
func (h *Handler) GetOnlineStatusBatch(c *gin.Context) {
	requesterID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body", nil)
		return
	}

	// Validate, dedupe and cap the batch.
	const maxBatch = 100
	targets := make([]string, 0, len(req.UserIDs))
	seen := make(map[string]struct{}, len(req.UserIDs))
	for _, idStr := range req.UserIDs {
		if len(targets) >= maxBatch {
			break
		}
		if _, dup := seen[idStr]; dup {
			continue
		}
		if _, perr := uuid.Parse(idStr); perr != nil {
			continue
		}
		seen[idStr] = struct{}{}
		targets = append(targets, idStr)
	}

	// Default every requested user to offline.
	result := make(map[string]bool, len(targets))
	for _, t := range targets {
		result[t] = false
	}
	if len(targets) == 0 {
		api.JSON(c.Writer, http.StatusOK, map[string]any{"online": result}, nil)
		return
	}

	// Privacy gate: a target is visible only if it is the requester or one of
	// their connections. Page through the requester's FULL connection list
	// (graph caps a page at 100) so a user with many friends is not silently
	// truncated to the first page. On any error the set stays as-is —
	// fail-closed, leak nothing.
	circle := map[string]struct{}{requesterID.String(): {}}
	for offset := 0; offset <= 5000; offset += 100 {
		graphURL := fmt.Sprintf("%s/v1/graph/connections/%s?limit=100&offset=%d", h.graphURL, requesterID, offset)
		graphReq, rerr := h.newGraphRequest(c.Request.Context(), graphURL)
		if rerr != nil {
			break
		}
		resp, derr := h.graphClient.Do(graphReq)
		if derr != nil {
			break
		}
		var body struct {
			Data []string `json:"data"`
		}
		decErr := json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if decErr != nil {
			break
		}
		for _, id := range body.Data {
			circle[id] = struct{}{}
		}
		if len(body.Data) < 100 {
			break
		}
	}

	// Resolve presence for the visible subset only — one Redis round-trip.
	visible := make([]string, 0, len(targets))
	for _, t := range targets {
		if _, ok := circle[t]; ok {
			visible = append(visible, t)
		}
	}
	if len(visible) > 0 {
		if online, oerr := h.presenceStore.AreOnline(c.Request.Context(), visible); oerr == nil {
			for id, on := range online {
				result[id] = on
			}
		}
	}

	api.JSON(c.Writer, http.StatusOK, map[string]any{"online": result}, nil)
}

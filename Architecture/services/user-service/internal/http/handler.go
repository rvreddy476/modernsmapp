package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/httpclient"
	"github.com/atpost/user-service/internal/presence"
	"github.com/atpost/user-service/internal/service"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc           *service.Service
	graphURL      string
	presenceStore *presence.Store
	graphClient   *http.Client
}

func New(svc *service.Service, presenceStore *presence.Store) *Handler {
	graphURL := os.Getenv("GRAPH_SERVICE_URL")
	if graphURL == "" {
		graphURL = "http://graph-service:8083"
	}
	h := &Handler{svc: svc, graphURL: graphURL, presenceStore: presenceStore}
	h.graphClient = httpclient.NewWithBreaker(5*time.Second, "user->graph")
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
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
		v1.GET("/:userId/online", h.GetOnlineStatus)
	}

	// Channels
	channels := r.Group("/v1/channels")
	{
		channels.GET("/:handle", h.GetChannel)
	}
	myChannels := r.Group("/v1/users/me/channels")
	{
		myChannels.POST("", h.CreateChannel)
		myChannels.GET("", h.ListMyChannels)
	}
	channelByID := r.Group("/v1/channels")
	{
		channelByID.PATCH("/:id", h.UpdateChannel)
		channelByID.DELETE("/:id", h.DeleteChannel)
	}

	// Channel Subscriptions
	channelSubs := r.Group("/v1/channels/:channelId")
	{
		channelSubs.POST("/subscribe", h.SubscribeToChannel)
		channelSubs.DELETE("/subscribe", h.UnsubscribeFromChannel)
		channelSubs.GET("/subscription", h.GetChannelSubscriptionStatus)
		channelSubs.GET("/subscribers", h.ListChannelSubscribers)
	}

	// User subscriptions list
	v1.GET("/:userId/subscriptions", h.ListUserChannelSubscriptions)

	// Profile extras: pins, portfolio, QR codes, digital wellbeing
	h.registerProfileExtrasRoutes(r)

	// Onboarding
	r.POST("/v1/onboarding/ensure-publisher", h.EnsurePublisher)

	// Business Pages
	pages := r.Group("/v1/pages")
	{
		pages.GET("/:handle", h.GetBusinessPage)
		pages.PATCH("/:handle", h.UpdateBusinessPage)
	}
	pageReviews := r.Group("/v1/pages/:handle/reviews")
	{
		pageReviews.GET("", h.GetPageReviews)
		pageReviews.POST("", h.SubmitReview)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}
	channels, err := h.svc.GetUserChannels(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	u, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if u == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetUserByUsername(c *gin.Context) {
	username := c.Param("username")
	if username == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Username is required", nil, nil)
		return
	}

	u, err := h.svc.GetUserByUsername(c.Request.Context(), username)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if u == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetMe(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	if userIDStr == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil, nil)
		return
	}
	userID, _ := uuid.Parse(userIDStr)

	u, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch profile", nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	u, err := h.svc.UpdateUser(c.Request.Context(), userID,
		req.DisplayName, req.Bio, req.AvatarMediaID, req.CoverMediaID,
		req.FirstName, req.LastName, req.Gender,
		req.Username, req.Category, req.Profession, req.Website, req.Location,
		req.DoB,
	)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetUserLinks(c *gin.Context) {
	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	links, err := h.svc.GetUserLinks(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req UpdateLinksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, links, nil)
}

func (h *Handler) GetMySettings(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	s, err := h.svc.GetSettings(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, s, nil)
}

func (h *Handler) UpdateMySettings(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req store.UserSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	req.UserID = userID

	s, err := h.svc.UpdateSettings(c.Request.Context(), &req)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, s, nil)
}

// --- About ---

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
	graphReq, err := http.NewRequestWithContext(ctx.Request.Context(), http.MethodGet, url, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	access := h.resolveViewerAccess(c, userID)
	items, err := h.svc.GetAllAbout(c.Request.Context(), userID, access)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}
	section := c.Param("section")

	access := h.resolveViewerAccess(c, userID)
	items, err := h.svc.GetAboutSection(c.Request.Context(), userID, section, access)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	section := c.Param("section")

	var req UpsertAboutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
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
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid item_id", nil, nil)
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
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) DeleteAboutItem(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	section := c.Param("section")
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid item ID", nil, nil)
		return
	}

	if err := h.svc.DeleteAboutItem(c.Request.Context(), userID, section, itemID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req UpdateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.UpdateStatus(c.Request.Context(), userID, req.StatusText, req.StatusEmoji, req.ExpiresAt); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

// --- Reputation & Endorsements ---

func (h *Handler) GetReputation(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	rep, err := h.svc.GetReputation(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	endorsements, err := h.svc.GetEndorsements(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	toUserID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	var req EndorseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
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
			api.Error(c.Writer, http.StatusBadRequest, "CANNOT_ENDORSE_SELF", "Cannot endorse yourself", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, e, nil)
}

// --- Compatibility ---

func (h *Handler) GetCompatibility(c *gin.Context) {
	viewerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	otherID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	score, err := h.svc.GetCompatibility(c.Request.Context(), viewerID, otherID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]float64{"compatibility_score": score}, nil)
}

// --- Link Analytics ---

func (h *Handler) TrackLinkClick(c *gin.Context) {
	userIDStr := c.Query("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user_id query param", nil, nil)
		return
	}
	platform := c.Param("platform")

	if err := h.svc.TrackLinkClick(c.Request.Context(), userID, platform); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "tracked"}, nil)
}

func (h *Handler) GetLinkAnalytics(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	analytics, err := h.svc.GetLinkAnalytics(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req CreateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, ch, nil)
}

func (h *Handler) GetChannel(c *gin.Context) {
	handle := c.Param("handle")
	detail, err := h.svc.GetChannel(c.Request.Context(), handle)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Channel not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, detail, nil)
}

func (h *Handler) ListMyChannels(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	channels, err := h.svc.GetUserChannels(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	channelID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	var req UpdateChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
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
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Channel not found or not owned by you", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) DeleteChannel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	channelID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid channel ID", nil, nil)
		return
	}

	if err := h.svc.DeleteChannel(c.Request.Context(), channelID, userID); err != nil {
		if err.Error() == "CHANNEL_NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Channel not found or not owned by you", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	result, err := h.svc.EnsurePublisher(c.Request.Context(), userID)
	if err != nil {
		if err.Error() == "user not found: "+userID.String() {
			api.Error(c.Writer, http.StatusNotFound, "USER_NOT_FOUND", "User not found", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to ensure publisher identity", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// --- Business Pages ---

type CreateBusinessPageRequest struct {
	PageHandle    string          `json:"page_handle" binding:"required"`
	PageName      string          `json:"page_name" binding:"required"`
	Category      string          `json:"category" binding:"required"`
	Description   string          `json:"description"`
	Address       string          `json:"address"`
	Lat           *float64        `json:"lat"`
	Lng           *float64        `json:"lng"`
	BusinessHours json.RawMessage `json:"business_hours"`
	Phone         string          `json:"phone"`
	Whatsapp      string          `json:"whatsapp"`
	BusinessEmail string          `json:"business_email"`
	Services      json.RawMessage `json:"services"`
	PriceRange    string          `json:"price_range"`
	BookingURL    string          `json:"booking_url"`
	MenuURLs      json.RawMessage `json:"menu_urls"`
	FAQ           json.RawMessage `json:"faq"`
}

func (h *Handler) CreateBusinessPage(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req CreateBusinessPageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	p := &store.BusinessPage{
		UserID:        userID,
		PageHandle:    req.PageHandle,
		PageName:      req.PageName,
		Category:      req.Category,
		Description:   req.Description,
		Address:       req.Address,
		Lat:           req.Lat,
		Lng:           req.Lng,
		BusinessHours: req.BusinessHours,
		Phone:         req.Phone,
		Whatsapp:      req.Whatsapp,
		BusinessEmail: req.BusinessEmail,
		Services:      req.Services,
		PriceRange:    req.PriceRange,
		BookingURL:    req.BookingURL,
		MenuURLs:      req.MenuURLs,
		FAQ:           req.FAQ,
	}

	if err := h.svc.CreateBusinessPage(c.Request.Context(), p); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, p, nil)
}

func (h *Handler) GetBusinessPage(c *gin.Context) {
	handle := c.Param("handle")
	page, err := h.svc.GetBusinessPage(c.Request.Context(), handle)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Business page not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

func (h *Handler) UpdateBusinessPage(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	pageID, err := uuid.Parse(c.Param("handle"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid page ID", nil, nil)
		return
	}

	var req CreateBusinessPageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	p := &store.BusinessPage{
		ID:            pageID,
		UserID:        userID,
		PageName:      req.PageName,
		Category:      req.Category,
		Description:   req.Description,
		Address:       req.Address,
		Lat:           req.Lat,
		Lng:           req.Lng,
		BusinessHours: req.BusinessHours,
		Phone:         req.Phone,
		Whatsapp:      req.Whatsapp,
		BusinessEmail: req.BusinessEmail,
		Services:      req.Services,
		PriceRange:    req.PriceRange,
		BookingURL:    req.BookingURL,
		MenuURLs:      req.MenuURLs,
		FAQ:           req.FAQ,
	}

	if err := h.svc.UpdateBusinessPage(c.Request.Context(), p); err != nil {
		if err.Error() == "PAGE_NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Page not found or not owned by you", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) GetPageReviews(c *gin.Context) {
	pageID, err := uuid.Parse(c.Param("handle"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid page ID", nil, nil)
		return
	}

	cursor := time.Now()
	if cursorStr := c.Query("cursor"); cursorStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, cursorStr); err == nil {
			cursor = t
		}
	}
	limit := 20
	if limitStr := c.Query("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil {
			limit = n
		}
	}

	reviews, err := h.svc.GetPageReviews(c.Request.Context(), pageID, cursor, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if reviews == nil {
		reviews = []store.BusinessReview{}
	}

	var meta *api.Meta
	if len(reviews) == limit {
		meta = &api.Meta{NextCursor: reviews[len(reviews)-1].CreatedAt.Format(time.RFC3339Nano)}
	}

	api.JSON(c.Writer, http.StatusOK, reviews, meta)
}

type SubmitReviewRequest struct {
	Rating     int    `json:"rating" binding:"required"`
	ReviewText string `json:"review_text"`
}

func (h *Handler) SubmitReview(c *gin.Context) {
	reviewerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	pageID, err := uuid.Parse(c.Param("handle"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid page ID", nil, nil)
		return
	}

	var req SubmitReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if req.Rating < 1 || req.Rating > 5 {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_RATING", "Rating must be between 1 and 5", nil, nil)
		return
	}

	r := &store.BusinessReview{
		PageID:     pageID,
		ReviewerID: reviewerID,
		Rating:     req.Rating,
		ReviewText: req.ReviewText,
	}

	if err := h.svc.SubmitReview(c.Request.Context(), r); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, r, nil)
}

func (h *Handler) ListMyBusinessPages(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	pages, err := h.svc.GetUserBusinessPages(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if pages == nil {
		pages = []store.BusinessPage{}
	}
	api.JSON(c.Writer, http.StatusOK, pages, nil)
}

// --- Presence ---

// Heartbeat sets the calling user as online with a 90-second TTL.
// Clients should call this every 30 seconds to maintain online status.
func (h *Handler) Heartbeat(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	if err := h.presenceStore.SetOnline(c.Request.Context(), userID.String()); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update presence", nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	targetIDStr := c.Param("userId")
	targetID, err := uuid.Parse(targetIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	// Self-check: always truthful
	inCircle := requesterID == targetID

	if !inCircle {
		// Check mutual friendship (circle membership) via graph-service.
		// Fail-closed: if graph-service is unreachable, treat as not in circle.
		url := fmt.Sprintf("%s/v1/graph/relationship?user_id=%s&other_id=%s", h.graphURL, requesterID, targetID)
		graphReq, _ := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, url, nil)
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

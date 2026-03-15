package http

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/atpost/identity-shared/api"
)

type Handler struct {
	svc ProfileService
	log *slog.Logger
}

type ProfileService interface {
	ListProfiles(ctx context.Context, limit, offset int) ([]store.Profile, int64, error)
	GetProfile(ctx context.Context, userID uuid.UUID) (*store.Profile, error)
	GetProfileByUsername(ctx context.Context, username string) (*store.Profile, error)
	UpdateProfile(ctx context.Context, userID uuid.UUID, params store.UpdateProfileParams) (*store.Profile, error)
	// Legacy links
	GetUserLinks(ctx context.Context, userID uuid.UUID) ([]store.UserLink, error)
	UpsertUserLinks(ctx context.Context, userID uuid.UUID, links []store.UserLink) error
	// New profile links
	GetProfileLinks(ctx context.Context, profileID uuid.UUID) ([]store.ProfileLink, error)
	CreateProfileLink(ctx context.Context, link *store.ProfileLink) (*store.ProfileLink, error)
	UpdateProfileLink(ctx context.Context, linkID, profileID uuid.UUID, title, url string, icon, category *string, sortOrder int, isPinned bool, visibility string) (*store.ProfileLink, error)
	DeleteProfileLink(ctx context.Context, linkID, profileID uuid.UUID) error
	IncrementLinkClick(ctx context.Context, linkID uuid.UUID) error
	// About
	GetAllAbout(ctx context.Context, userID uuid.UUID) ([]store.AboutItem, error)
	GetAboutBySection(ctx context.Context, userID uuid.UUID, section string) ([]store.AboutItem, error)
	UpsertAboutItem(ctx context.Context, item *store.AboutItem) (*store.AboutItem, error)
	DeleteAboutItem(ctx context.Context, userID uuid.UUID, section string, itemID uuid.UUID) error
	// Avatar/Cover
	UpdateAvatar(ctx context.Context, userID uuid.UUID, mediaID uuid.UUID) error
	UpdateCover(ctx context.Context, userID uuid.UUID, mediaID uuid.UUID) error
	// Follow
	FollowUser(ctx context.Context, followerID, followingID uuid.UUID) (*store.Follow, error)
	UnfollowUser(ctx context.Context, followerID, followingID uuid.UUID) error
	// Friend
	SendFriendRequest(ctx context.Context, requesterID, addresseeID uuid.UUID) (*store.Friendship, error)
	RespondToFriendRequest(ctx context.Context, userID, friendshipID uuid.UUID, accept bool) (*store.Friendship, error)
	// Social lists
	ListFollowers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FollowerEntry, int64, error)
	ListFollowing(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FollowerEntry, int64, error)
	ListFriends(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FriendEntry, int64, error)
	ListFriendRequests(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FriendRequestEntry, int64, error)
	ListSentFriendRequests(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FriendRequestEntry, int64, error)
	ListBlocks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.Block, int64, error)
	// Circle management
	CancelFriendRequest(ctx context.Context, requesterID, friendshipID uuid.UUID) error
	RemoveFriend(ctx context.Context, userID, friendID uuid.UUID) error
	// Block
	BlockUser(ctx context.Context, blockerID, blockedID uuid.UUID) error
	UnblockUser(ctx context.Context, blockerID, blockedID uuid.UUID) error
	// Relationship
	GetRelationship(ctx context.Context, viewerID, targetID uuid.UUID) (*store.RelationshipStatus, error)
	// Batch
	GetProfilesBatch(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]*store.Profile, error)
	// Module Profiles
	GetModuleProfile(ctx context.Context, userID uuid.UUID, module string) (*store.ModuleProfile, error)
	GetModuleProfiles(ctx context.Context, userID uuid.UUID) ([]store.ModuleProfile, error)
	UpsertModuleProfile(ctx context.Context, userID uuid.UUID, module string, params store.UpsertModuleProfileParams) (*store.ModuleProfile, error)
	DeleteModuleProfile(ctx context.Context, userID uuid.UUID, module string) error
	// Handle Change
	ChangeHandle(ctx context.Context, userID uuid.UUID, newUsername string) (*store.Profile, error)
	ResolveHandle(ctx context.Context, oldUsername string) (*uuid.UUID, *string, error)
	GetHandleHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.HandleHistoryEntry, error)
}

func New(svc ProfileService, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{svc: svc, log: logger}
}

func (h *Handler) RegisterRoutes(r *gin.Engine, auth gin.HandlerFunc, csrf gin.HandlerFunc) {
	v1 := r.Group("/v1/profiles")
	{
		v1.GET("/health", h.Health)
		v1.GET("/discover", h.DiscoverProfiles)
		v1.GET("/by-username/:username", h.GetProfileByUsername)
		v1.POST("/batch", h.GetProfilesBatch)
		v1.GET("/:userId", h.GetProfile)
		v1.GET("/:userId/links", h.GetUserLinks)
		v1.GET("/:userId/about", h.GetAllAbout)
		v1.GET("/:userId/about/:section", h.GetAboutBySection)
		// Public social lists
		v1.GET("/:userId/followers", h.ListFollowers)
		v1.GET("/:userId/following", h.ListFollowing)
		v1.GET("/:userId/friends", h.ListFriends)
		// Relationship (public GET — viewer ID from X-User-Id header, set by gateway or forwarded by BFF)
		v1.GET("/:userId/relationship", h.GetRelationship)
	}

	protected := v1.Group("")
	protected.Use(auth)
	{
		protected.GET("/me", h.GetMe)
		protected.PUT("/me", csrf, h.UpdateMe)
		// Legacy links (bulk PUT)
		protected.PUT("/me/links", csrf, h.UpdateMyLinks)
		// New profile links (individual CRUD)
		protected.GET("/me/profile-links", h.GetMyProfileLinks)
		protected.POST("/me/profile-links", csrf, h.CreateMyProfileLink)
		protected.PATCH("/me/profile-links/:linkId", csrf, h.UpdateMyProfileLink)
		protected.DELETE("/me/profile-links/:linkId", csrf, h.DeleteMyProfileLink)
		// Avatar / Cover
		protected.PUT("/me/avatar", csrf, h.UpdateMyAvatar)
		protected.PUT("/me/cover", csrf, h.UpdateMyCover)
		// About
		protected.PUT("/me/about/:section", csrf, h.UpsertMyAboutItem)
		protected.DELETE("/me/about/:section/:itemId", csrf, h.DeleteMyAboutItem)
		// Follow
		protected.POST("/:username/follow", csrf, h.FollowUser)
		protected.DELETE("/:username/follow", csrf, h.UnfollowUser)
		// Friend requests
		protected.POST("/:username/friend-request", csrf, h.SendFriendRequest)
		protected.PATCH("/friend-requests/:id", csrf, h.RespondToFriendRequest)
		protected.DELETE("/friend-requests/:id", csrf, h.CancelFriendRequest)
		// Social lists (own data)
		protected.GET("/me/friend-requests", h.ListFriendRequests)
		protected.GET("/me/sent-friend-requests", h.ListSentFriendRequests)
		protected.GET("/me/blocks", h.ListBlocks)
		// Circle management
		protected.DELETE("/:username/friend", csrf, h.RemoveFriend)
		// Block / Unblock
		protected.POST("/:username/block", csrf, h.BlockUser)
		protected.DELETE("/:username/block", csrf, h.UnblockUser)
		// Module Profiles
		protected.GET("/me/modules", h.GetMyModuleProfiles)
		protected.GET("/me/modules/:module", h.GetMyModuleProfile)
		protected.PUT("/me/modules/:module", csrf, h.UpsertMyModuleProfile)
		protected.DELETE("/me/modules/:module", csrf, h.DeleteMyModuleProfile)
		// Handle Change
		protected.PUT("/me/handle", csrf, h.ChangeHandle)
		protected.GET("/me/handle-history", h.GetHandleHistory)
	}

	// Public: link click tracking
	r.POST("/v1/links/:id/click", h.TrackLinkClick)

	// Public: handle redirect resolution
	v1.GET("/resolve-handle/:username", h.ResolveHandle)

	// Public: module profile for any user
	v1.GET("/:userId/modules/:module", h.GetUserModuleProfile)
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// resolveTargetUser resolves the :username path parameter to a profile.
// It first tries to parse it as a UUID (user_id lookup), then falls back to username lookup.
func (h *Handler) resolveTargetUser(ctx context.Context, identifier string) (*store.Profile, error) {
	if uid, err := uuid.Parse(identifier); err == nil {
		return h.svc.GetProfile(ctx, uid)
	}
	return h.svc.GetProfileByUsername(ctx, identifier)
}

func (h *Handler) DiscoverProfiles(c *gin.Context) {
	limit, offset := parsePagination(c)

	profiles, total, err := h.svc.ListProfiles(c.Request.Context(), limit, offset)
	if err != nil {
		h.log.Error("failed to list profiles", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	if profiles == nil {
		profiles = []store.Profile{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items": profiles,
		"meta": paginationMeta{
			Limit:   limit,
			Offset:  offset,
			Total:   total,
			HasNext: int64(offset+limit) < total,
		},
	}, nil)
}

// ---------------------------------------------------------------
// Profile
// ---------------------------------------------------------------

func (h *Handler) GetProfile(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		h.log.Warn("invalid user id", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	p, err := h.svc.GetProfile(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch profile", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if p == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Profile not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, p, nil)
}

func (h *Handler) GetProfileByUsername(c *gin.Context) {
	username := c.Param("username")
	if username == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Username is required", nil, nil)
		return
	}

	p, err := h.svc.GetProfileByUsername(c.Request.Context(), username)
	if err != nil {
		h.log.Error("failed to fetch profile by username", "err", err, "username", username, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if p == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Profile not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, p, nil)
}

func (h *Handler) GetMe(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		h.log.Warn("invalid user id header", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	p, err := h.svc.GetProfile(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch profile", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

type UpdateProfileRequest struct {
	DisplayName       string     `json:"display_name"`
	Bio               string     `json:"bio"`
	AvatarMediaID     *uuid.UUID `json:"avatar_media_id"`
	CoverMediaID      *uuid.UUID `json:"cover_media_id"`
	FirstName         *string    `json:"first_name"`
	LastName          *string    `json:"last_name"`
	PreferredName     *string    `json:"preferred_name"`
	Pronouns          *string    `json:"pronouns"`
	Gender            *string    `json:"gender"`
	DoB               *time.Time `json:"dob"`
	Username          *string    `json:"username"`
	Category          string     `json:"category"`
	Profession        string     `json:"profession"`
	Website           string     `json:"website"`
	Location          string     `json:"location"`
	StatusText        *string    `json:"status_text"`
	StatusEmoji       *string    `json:"status_emoji"`
	StatusExpiresAt   *time.Time `json:"status_expires_at"`
	ProfileThemeColor string     `json:"profile_theme_color"`
	IntroMediaURL     *string    `json:"intro_media_url"`
	IntroMediaType    *string    `json:"intro_media_type"`
	CTALabel          *string    `json:"cta_label"`
	CTAURL            *string    `json:"cta_url"`
	MemberSinceBadge  *bool      `json:"member_since_badge"`
	Timezone          *string    `json:"timezone"`
}

func (h *Handler) UpdateMe(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		h.log.Warn("invalid user id header", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	themeColor := req.ProfileThemeColor
	if themeColor == "" {
		themeColor = "#1A73E8"
	}

	params := store.UpdateProfileParams{
		DisplayName:       req.DisplayName,
		Bio:               req.Bio,
		AvatarMediaID:     req.AvatarMediaID,
		CoverMediaID:      req.CoverMediaID,
		FirstName:         req.FirstName,
		LastName:          req.LastName,
		PreferredName:     req.PreferredName,
		Pronouns:          req.Pronouns,
		Gender:            req.Gender,
		DoB:               req.DoB,
		Username:          req.Username,
		Category:          req.Category,
		Profession:        req.Profession,
		Website:           req.Website,
		Location:          req.Location,
		StatusText:        req.StatusText,
		StatusEmoji:       req.StatusEmoji,
		StatusExpiresAt:   req.StatusExpiresAt,
		ProfileThemeColor: themeColor,
		IntroMediaURL:     req.IntroMediaURL,
		IntroMediaType:    req.IntroMediaType,
		CTALabel:          req.CTALabel,
		CTAURL:            req.CTAURL,
		MemberSinceBadge:  req.MemberSinceBadge,
		Timezone:          req.Timezone,
	}

	p, err := h.svc.UpdateProfile(c.Request.Context(), userID, params)
	if err != nil {
		h.log.Error("failed to update profile", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, p, nil)
}

// ---------------------------------------------------------------
// Avatar / Cover
// ---------------------------------------------------------------

type UpdateMediaIDRequest struct {
	MediaID uuid.UUID `json:"media_id" binding:"required"`
}

func (h *Handler) UpdateMyAvatar(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req UpdateMediaIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.UpdateAvatar(c.Request.Context(), userID, req.MediaID); err != nil {
		h.log.Error("failed to update avatar", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok", "avatar_media_id": req.MediaID}, nil)
}

func (h *Handler) UpdateMyCover(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req UpdateMediaIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.UpdateCover(c.Request.Context(), userID, req.MediaID); err != nil {
		h.log.Error("failed to update cover", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok", "cover_media_id": req.MediaID}, nil)
}

// ---------------------------------------------------------------
// User Links
// ---------------------------------------------------------------

func (h *Handler) GetUserLinks(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	links, err := h.svc.GetUserLinks(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch user links", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
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
	userID, err := parseUserHeader(c)
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

	if err := h.svc.UpsertUserLinks(c.Request.Context(), userID, links); err != nil {
		h.log.Error("failed to update links", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

// ---------------------------------------------------------------
// User About
// ---------------------------------------------------------------

func (h *Handler) GetAllAbout(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	items, err := h.svc.GetAllAbout(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch about items", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	// Group by section for the response
	grouped := make(map[string][]store.AboutItem)
	for _, item := range items {
		grouped[item.Section] = append(grouped[item.Section], item)
	}

	api.JSON(c.Writer, http.StatusOK, grouped, nil)
}

func (h *Handler) GetAboutBySection(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	section := c.Param("section")
	items, err := h.svc.GetAboutBySection(c.Request.Context(), userID, section)
	if err != nil {
		h.log.Error("failed to fetch about section", "err", err, "user_id", userID, "section", section, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, items, nil)
}

type UpsertAboutItemRequest struct {
	ItemID     *uuid.UUID             `json:"item_id"`
	Data       map[string]interface{} `json:"data" binding:"required"`
	Visibility string                 `json:"visibility"`
	SortOrder  int                    `json:"sort_order"`
}

func (h *Handler) UpsertMyAboutItem(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	section := c.Param("section")
	var req UpsertAboutItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	visibility := req.Visibility
	if visibility == "" {
		visibility = "public"
	}

	itemID := uuid.Nil
	if req.ItemID != nil {
		itemID = *req.ItemID
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
		h.log.Error("failed to upsert about item", "err", err, "user_id", userID, "section", section, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) DeleteMyAboutItem(c *gin.Context) {
	userID, err := parseUserHeader(c)
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
		h.log.Error("failed to delete about item", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

// ---------------------------------------------------------------
// Profile Links (new table)
// ---------------------------------------------------------------

func (h *Handler) GetMyProfileLinks(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	links, err := h.svc.GetProfileLinks(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch profile links", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, links, nil)
}

type CreateProfileLinkRequest struct {
	Title      string  `json:"title" binding:"required"`
	URL        string  `json:"url" binding:"required"`
	Icon       *string `json:"icon"`
	Category   *string `json:"category"`
	SortOrder  int     `json:"sort_order"`
	IsPinned   bool    `json:"is_pinned"`
	Visibility string  `json:"visibility"`
}

func (h *Handler) CreateMyProfileLink(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req CreateProfileLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	visibility := req.Visibility
	if visibility == "" {
		visibility = "public"
	}

	link := &store.ProfileLink{
		ProfileID:  userID,
		Title:      req.Title,
		URL:        req.URL,
		Icon:       req.Icon,
		Category:   req.Category,
		SortOrder:  req.SortOrder,
		IsPinned:   req.IsPinned,
		Visibility: visibility,
	}

	result, err := h.svc.CreateProfileLink(c.Request.Context(), link)
	if err != nil {
		h.log.Error("failed to create profile link", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, result, nil)
}

type UpdateProfileLinkRequest struct {
	Title      string  `json:"title" binding:"required"`
	URL        string  `json:"url" binding:"required"`
	Icon       *string `json:"icon"`
	Category   *string `json:"category"`
	SortOrder  int     `json:"sort_order"`
	IsPinned   bool    `json:"is_pinned"`
	Visibility string  `json:"visibility"`
}

func (h *Handler) UpdateMyProfileLink(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	linkID, err := uuid.Parse(c.Param("linkId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid link ID", nil, nil)
		return
	}

	var req UpdateProfileLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	visibility := req.Visibility
	if visibility == "" {
		visibility = "public"
	}

	result, err := h.svc.UpdateProfileLink(c.Request.Context(), linkID, userID, req.Title, req.URL, req.Icon, req.Category, req.SortOrder, req.IsPinned, visibility)
	if err != nil {
		h.log.Error("failed to update profile link", "err", err, "user_id", userID, "link_id", linkID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if result == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Link not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) DeleteMyProfileLink(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	linkID, err := uuid.Parse(c.Param("linkId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid link ID", nil, nil)
		return
	}

	if err := h.svc.DeleteProfileLink(c.Request.Context(), linkID, userID); err != nil {
		h.log.Error("failed to delete profile link", "err", err, "user_id", userID, "link_id", linkID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

func (h *Handler) TrackLinkClick(c *gin.Context) {
	linkID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid link ID", nil, nil)
		return
	}

	if err := h.svc.IncrementLinkClick(c.Request.Context(), linkID); err != nil {
		h.log.Error("failed to track link click", "err", err, "link_id", linkID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

// ---------------------------------------------------------------
// Follow / Unfollow
// ---------------------------------------------------------------

func (h *Handler) FollowUser(c *gin.Context) {
	followerID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	identifier := c.Param("username")
	target, err := h.resolveTargetUser(c.Request.Context(), identifier)
	if err != nil {
		h.log.Error("failed to look up user", "err", err, "identifier", identifier, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if target == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}

	f, err := h.svc.FollowUser(c.Request.Context(), followerID, target.UserID)
	if err != nil {
		h.log.Error("failed to follow user", "err", err, "follower_id", followerID, "following_id", target.UserID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "FOLLOW_FAILED", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, f, nil)
}

func (h *Handler) UnfollowUser(c *gin.Context) {
	followerID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	identifier := c.Param("username")
	target, err := h.resolveTargetUser(c.Request.Context(), identifier)
	if err != nil {
		h.log.Error("failed to look up user", "err", err, "identifier", identifier, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if target == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}

	if err := h.svc.UnfollowUser(c.Request.Context(), followerID, target.UserID); err != nil {
		h.log.Error("failed to unfollow user", "err", err, "follower_id", followerID, "following_id", target.UserID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

// ---------------------------------------------------------------
// Friend Requests
// ---------------------------------------------------------------

func (h *Handler) SendFriendRequest(c *gin.Context) {
	requesterID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	identifier := c.Param("username")
	target, err := h.resolveTargetUser(c.Request.Context(), identifier)
	if err != nil {
		h.log.Error("failed to look up user", "err", err, "identifier", identifier, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if target == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}

	fr, err := h.svc.SendFriendRequest(c.Request.Context(), requesterID, target.UserID)
	if err != nil {
		h.log.Error("failed to send friend request", "err", err, "requester_id", requesterID, "addressee_id", target.UserID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "FRIEND_REQUEST_FAILED", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, fr, nil)
}

type RespondFriendRequestBody struct {
	Accept bool `json:"accept"`
}

func (h *Handler) RespondToFriendRequest(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	friendshipID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid friendship ID", nil, nil)
		return
	}

	var req RespondFriendRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	fr, err := h.svc.RespondToFriendRequest(c.Request.Context(), userID, friendshipID, req.Accept)
	if err != nil {
		h.log.Error("failed to respond to friend request", "err", err, "user_id", userID, "friendship_id", friendshipID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "FRIEND_REQUEST_FAILED", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, fr, nil)
}

// ---------------------------------------------------------------
// Social Lists
// ---------------------------------------------------------------

// paginationMeta builds the standard pagination meta object.
type paginationMeta struct {
	Limit   int   `json:"limit"`
	Offset  int   `json:"offset"`
	Total   int64 `json:"total"`
	HasNext bool  `json:"has_next"`
}

// parsePagination extracts limit and offset from query params with defaults.
func parsePagination(c *gin.Context) (limit, offset int) {
	limit = 20
	offset = 0

	if v := c.Query("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 100 {
		limit = 100
	}

	if v := c.Query("offset"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	return limit, offset
}

func (h *Handler) ListFollowers(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	limit, offset := parsePagination(c)

	entries, total, err := h.svc.ListFollowers(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("failed to list followers", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	if entries == nil {
		entries = []store.FollowerEntry{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items": entries,
		"meta": paginationMeta{
			Limit:   limit,
			Offset:  offset,
			Total:   total,
			HasNext: int64(offset+limit) < total,
		},
	}, nil)
}

func (h *Handler) ListFollowing(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	limit, offset := parsePagination(c)

	entries, total, err := h.svc.ListFollowing(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("failed to list following", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	if entries == nil {
		entries = []store.FollowerEntry{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items": entries,
		"meta": paginationMeta{
			Limit:   limit,
			Offset:  offset,
			Total:   total,
			HasNext: int64(offset+limit) < total,
		},
	}, nil)
}

func (h *Handler) ListFriends(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	limit, offset := parsePagination(c)

	entries, total, err := h.svc.ListFriends(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("failed to list friends", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	if entries == nil {
		entries = []store.FriendEntry{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items": entries,
		"meta": paginationMeta{
			Limit:   limit,
			Offset:  offset,
			Total:   total,
			HasNext: int64(offset+limit) < total,
		},
	}, nil)
}

func (h *Handler) ListFriendRequests(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	limit, offset := parsePagination(c)

	requests, total, err := h.svc.ListFriendRequests(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("failed to list friend requests", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	if requests == nil {
		requests = []store.FriendRequestEntry{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items": requests,
		"meta": paginationMeta{
			Limit:   limit,
			Offset:  offset,
			Total:   total,
			HasNext: int64(offset+limit) < total,
		},
	}, nil)
}

func (h *Handler) ListSentFriendRequests(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	limit, offset := parsePagination(c)

	requests, total, err := h.svc.ListSentFriendRequests(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("failed to list sent friend requests", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	if requests == nil {
		requests = []store.FriendRequestEntry{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items": requests,
		"meta": paginationMeta{
			Limit:   limit,
			Offset:  offset,
			Total:   total,
			HasNext: int64(offset+limit) < total,
		},
	}, nil)
}

func (h *Handler) CancelFriendRequest(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	friendshipID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid friendship ID", nil, nil)
		return
	}

	if err := h.svc.CancelFriendRequest(c.Request.Context(), userID, friendshipID); err != nil {
		h.log.Error("failed to cancel friend request", "err", err, "user_id", userID, "friendship_id", friendshipID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "CANCEL_FAILED", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

func (h *Handler) RemoveFriend(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	identifier := c.Param("username")
	target, err := h.resolveTargetUser(c.Request.Context(), identifier)
	if err != nil {
		h.log.Error("failed to look up user", "err", err, "identifier", identifier, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if target == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}

	if err := h.svc.RemoveFriend(c.Request.Context(), userID, target.UserID); err != nil {
		h.log.Error("failed to remove friend", "err", err, "user_id", userID, "friend_id", target.UserID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "REMOVE_FAILED", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

func (h *Handler) ListBlocks(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	limit, offset := parsePagination(c)

	blocks, total, err := h.svc.ListBlocks(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("failed to list blocks", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	if blocks == nil {
		blocks = []store.Block{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items": blocks,
		"meta": paginationMeta{
			Limit:   limit,
			Offset:  offset,
			Total:   total,
			HasNext: int64(offset+limit) < total,
		},
	}, nil)
}

// ---------------------------------------------------------------
// Block / Unblock
// ---------------------------------------------------------------

func (h *Handler) BlockUser(c *gin.Context) {
	blockerID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	identifier := c.Param("username")
	target, err := h.resolveTargetUser(c.Request.Context(), identifier)
	if err != nil {
		h.log.Error("failed to look up user", "err", err, "identifier", identifier, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if target == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}

	if err := h.svc.BlockUser(c.Request.Context(), blockerID, target.UserID); err != nil {
		h.log.Error("failed to block user", "err", err, "blocker_id", blockerID, "blocked_id", target.UserID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "BLOCK_FAILED", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

func (h *Handler) UnblockUser(c *gin.Context) {
	blockerID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	identifier := c.Param("username")
	target, err := h.resolveTargetUser(c.Request.Context(), identifier)
	if err != nil {
		h.log.Error("failed to look up user", "err", err, "identifier", identifier, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if target == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}

	if err := h.svc.UnblockUser(c.Request.Context(), blockerID, target.UserID); err != nil {
		h.log.Error("failed to unblock user", "err", err, "blocker_id", blockerID, "blocked_id", target.UserID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

// ---------------------------------------------------------------
// Relationship
// ---------------------------------------------------------------

func (h *Handler) GetRelationship(c *gin.Context) {
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	// Viewer ID is optional — set by API gateway auth or forwarded by BFF.
	// If absent, return empty relationship (anonymous viewer).
	viewerID, err := parseUserHeader(c)
	if err != nil || viewerID == targetID {
		api.JSON(c.Writer, http.StatusOK, gin.H{
			"following":               false,
			"followed_by":             false,
			"in_circle":               false,
			"circle_request_sent":     false,
			"circle_request_received": false,
			"blocked":                 false,
			"blocked_by":              false,
			"can_dm":                  false,
			"can_see_online":          false,
			"can_add_to_group":        false,
			"mutual_circle_count":     0,
		}, nil)
		return
	}

	rel, err := h.svc.GetRelationship(c.Request.Context(), viewerID, targetID)
	if err != nil {
		h.log.Error("failed to get relationship", "err", err, "viewer_id", viewerID, "target_id", targetID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, rel, nil)
}

// ---------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------

func parseUserHeader(c *gin.Context) (uuid.UUID, error) {
	return uuid.Parse(c.GetHeader("X-User-Id"))
}

// ---------------------------------------------------------------
// Batch profiles
// ---------------------------------------------------------------

func (h *Handler) GetProfilesBatch(c *gin.Context) {
	var req struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := c.BindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid body", nil, nil)
		return
	}
	ids := make([]uuid.UUID, 0, len(req.UserIDs))
	for _, id := range req.UserIDs {
		if uid, err := uuid.Parse(id); err == nil {
			ids = append(ids, uid)
		}
	}
	profiles, err := h.svc.GetProfilesBatch(c.Request.Context(), ids)
	if err != nil {
		h.log.Error("failed to get profiles batch", "err", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	c.JSON(http.StatusOK, profiles)
}

// ---------------------------------------------------------------
// Module Profiles
// ---------------------------------------------------------------

func (h *Handler) GetMyModuleProfiles(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	profiles, err := h.svc.GetModuleProfiles(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to get module profiles", "err", err, "user_id", userID)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if profiles == nil {
		profiles = []store.ModuleProfile{}
	}
	api.JSON(c.Writer, http.StatusOK, profiles, nil)
}

func (h *Handler) GetMyModuleProfile(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	module := c.Param("module")

	mp, err := h.svc.GetModuleProfile(c.Request.Context(), userID, module)
	if err != nil {
		h.log.Error("failed to get module profile", "err", err, "user_id", userID, "module", module)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	if mp == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Module profile not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, mp, nil)
}

func (h *Handler) UpsertMyModuleProfile(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	module := c.Param("module")

	var params store.UpsertModuleProfileParams
	if err := c.BindJSON(&params); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid body", nil, nil)
		return
	}

	mp, err := h.svc.UpsertModuleProfile(c.Request.Context(), userID, module, params)
	if err != nil {
		h.log.Error("failed to upsert module profile", "err", err, "user_id", userID, "module", module)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, mp, nil)
}

func (h *Handler) DeleteMyModuleProfile(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	module := c.Param("module")

	if err := h.svc.DeleteModuleProfile(c.Request.Context(), userID, module); err != nil {
		h.log.Error("failed to delete module profile", "err", err, "user_id", userID, "module", module)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (h *Handler) GetUserModuleProfile(c *gin.Context) {
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid user ID", nil, nil)
		return
	}
	module := c.Param("module")

	mp, err := h.svc.GetModuleProfile(c.Request.Context(), targetID, module)
	if err != nil {
		h.log.Error("failed to get user module profile", "err", err, "target_id", targetID, "module", module)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	if mp == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Module profile not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, mp, nil)
}

// ---------------------------------------------------------------
// Handle Change
// ---------------------------------------------------------------

func (h *Handler) ChangeHandle(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req struct {
		Username string `json:"username"`
	}
	if err := c.BindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid body", nil, nil)
		return
	}
	if req.Username == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "username is required", nil, nil)
		return
	}

	profile, err := h.svc.ChangeHandle(c.Request.Context(), userID, req.Username)
	if err != nil {
		h.log.Warn("handle change failed", "err", err, "user_id", userID)
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, profile, nil)
}

func (h *Handler) ResolveHandle(c *gin.Context) {
	username := c.Param("username")
	if username == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "username is required", nil, nil)
		return
	}

	userID, newUsername, err := h.svc.ResolveHandle(c.Request.Context(), username)
	if err != nil {
		h.log.Error("failed to resolve handle", "err", err, "username", username)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if userID == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "No redirect found for this handle", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"user_id":      userID.String(),
		"new_username": *newUsername,
	}, nil)
}

func (h *Handler) GetHandleHistory(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	limit, offset := parsePagination(c)

	history, err := h.svc.GetHandleHistory(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("failed to get handle history", "err", err, "user_id", userID)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if history == nil {
		history = []store.HandleHistoryEntry{}
	}
	api.JSON(c.Writer, http.StatusOK, history, nil)
}

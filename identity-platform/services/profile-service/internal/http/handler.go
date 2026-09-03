package http

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc         ProfileService
	log         *slog.Logger
	internalKey string
	// SR-4: block enforcement on profile surfaces. profile-service no longer
	// has its own block table (that copy was enforced by nobody), so this
	// asks graph-service, which is canonical. Nil means NOT CONFIGURED —
	// see blockedFromViewer, which refuses rather than serving unprotected.
	blocks BlockChecker
	// Canonical media ownership/readiness/moderation check for avatar and cover
	// references. Nil is fail-closed on those writes.
	media  ProfileMediaAuthority
	photos ProfilePhotoAccessChecker
	// Private-account display facts (is_private, follow_status). Nil means
	// the fields render as their zero values; see profile_privacy.go.
	privacy ProfilePrivacyResolver
}

// WithBlockChecker wires block denial on every profile read surface.
func (h *Handler) WithBlockChecker(b BlockChecker) *Handler {
	h.blocks = b
	return h
}

func (h *Handler) WithMediaAuthority(m ProfileMediaAuthority) *Handler {
	h.media = m
	return h
}

func (h *Handler) WithProfilePhotoAccess(checker ProfilePhotoAccessChecker) *Handler {
	h.photos = checker
	return h
}

// WithInternalKey enables the X-Internal-Service-Key gate on every
// gated /v1/profiles/* route. Audit UC1: without this, X-User-Id was
// a trust-the-caller header.
func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

type ProfileService interface {
	ListProfiles(ctx context.Context, limit, offset int) ([]store.Profile, int64, error)
	ListProfilesChangedSince(ctx context.Context, since time.Time, limit int) ([]store.Profile, error)
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
	FindProfileMediaOwner(ctx context.Context, mediaID uuid.UUID) (uuid.UUID, string, bool, error)
	// Follow
	FollowUser(ctx context.Context, followerID, followingID uuid.UUID) (*store.Follow, error)
	UnfollowUser(ctx context.Context, followerID, followingID uuid.UUID) error
	// Friend system retired — see graph-service connections; profile.friendships kept dormant for backfill
	// Social lists
	ListFollowers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FollowerEntry, int64, error)
	ListFollowing(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FollowerEntry, int64, error)
	ListFollowersCursor(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]store.FollowerEntry, string, error)
	ListFollowingCursor(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]store.FollowerEntry, string, error)
	ListBlocksCursor(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]store.Block, string, error)
	ListBlocks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.Block, int64, error)
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
	// Profile Stats
	GetProfileStats(ctx context.Context, userID uuid.UUID) (*store.ProfileStats, error)
	RecalculateProfileStats(ctx context.Context, userID uuid.UUID) (*store.ProfileStats, error)
}

func New(svc ProfileService, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{svc: svc, log: logger}
}

func (h *Handler) RegisterRoutes(r *gin.Engine, auth gin.HandlerFunc, csrf gin.HandlerFunc) {
	if h.internalKey != "" {
		r.Use(RequireInternalServiceKey(h.internalKey))
	}
	v1 := r.Group("/v1/profiles")
	{
		v1.GET("/health", h.Health)
		// Internal static routes must be registered before the public :userId
		// wildcard so a framework upgrade cannot reinterpret "internal" as a
		// public profile identifier.
		if h.internalKey != "" {
			v1.GET("/internal/changes", h.ListProfileChanges)
			v1.POST("/internal/media-access", h.ProfileMediaAccess)
		}
		v1.GET("/discover", h.DiscoverProfiles)
		v1.GET("/by-username/:username", h.GetProfileByUsername)
		v1.POST("/batch", h.GetProfilesBatch)
		v1.GET("/:userId", h.GetProfile)
		v1.GET("/:userId/links", h.GetUserLinks)
		v1.GET("/:userId/about", h.GetAllAbout)
		v1.GET("/:userId/about/:section", h.GetAboutBySection)
		// SR-3: the social graph is owned by graph-service. These read the
		// shadow `profile.follows` / `profile.blocks` tables, which disagree
		// with the canonical graph — see retired_graph_routes.go.
		v1.GET("/:userId/followers", retiredGraphRoute("GET /v1/graph/users/:userId/followers"))
		v1.GET("/:userId/following", retiredGraphRoute("GET /v1/graph/users/:userId/following"))
		// Friend system retired — see graph-service connections; profile.friendships kept dormant for backfill
		v1.GET("/:userId/relationship", retiredGraphRoute("GET /v1/graph/relationship/:userId"))
		// Profile Stats
		v1.GET("/:userId/stats", h.GetProfileStats)
	}

	protected := v1.Group("")
	protected.Use(auth)
	{
		protected.GET("/me", h.GetMe)
		protected.PUT("/me", csrf, h.UpdateMe)
		// Owner projection: unlike /:userId/about this includes non-public rows.
		protected.GET("/me/about", h.GetMyAbout)
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
		// SR-3: RETIRED. A block written here landed in `profile.blocks`,
		// which feed, search, chat and notifications never read — the user
		// was told they were protected and was not. graph-service owns the
		// graph; see retired_graph_routes.go.
		protected.POST("/:username/follow", csrf, retiredGraphRoute("POST /v1/graph/follow"))
		protected.DELETE("/:username/follow", csrf, retiredGraphRoute("DELETE /v1/graph/follow"))
		protected.GET("/me/blocks", retiredGraphRoute("GET /v1/graph/blocks"))
		protected.POST("/:username/block", csrf, retiredGraphRoute("POST /v1/graph/block"))
		protected.DELETE("/:username/block", csrf, retiredGraphRoute("DELETE /v1/graph/block"))
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

	// SR-4: discovery must not surface an account the viewer blocked, and
	// must not publish private fields to an unauthenticated browser.
	publicProfiles := h.filterBlockedProfiles(c, ToPublicProfiles(profiles))
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items": h.applyProfilePhotoPrivacyList(c, publicProfiles),
		"meta": paginationMeta{
			Limit:   limit,
			Offset:  offset,
			Total:   total,
			HasNext: int64(offset+limit) < total,
		},
	}, nil)
}

// ListProfileChanges feeds downstream projection reconcile jobs (e.g.
// user-service rebuilding app.users). Returns profiles updated at or after
// ?since= (RFC3339; omitted = epoch = full snapshot), oldest-change-first,
// plus next_since — the cursor the caller passes back to continue paging.
func (h *Handler) ListProfileChanges(c *gin.Context) {
	var since time.Time
	if raw := c.Query("since"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_SINCE", "since must be RFC3339", nil, nil)
			return
		}
		since = t
	}

	limit := 200
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}

	profiles, err := h.svc.ListProfilesChangedSince(c.Request.Context(), since, limit)
	if err != nil {
		h.log.Error("failed to list profile changes", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if profiles == nil {
		profiles = []store.Profile{}
	}

	// next_since = max updated_at in this page; a short page means caught up.
	next := since
	for i := range profiles {
		if profiles[i].UpdatedAt.After(next) {
			next = profiles[i].UpdatedAt
		}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items":       profiles,
		"count":       len(profiles),
		"next_since":  next,
		"server_time": time.Now().UTC(),
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
		writeProfileNotFound(c)
		return
	}
	// SR-4: a blocked viewer gets the same answer as a missing profile.
	if h.deniedByBlock(c, p.UserID) {
		return
	}

	// SR-4: never serialise store.Profile to a viewer. This route is
	// UNAUTHENTICATED and that struct carries the exact date of birth,
	// gender and timezone.
	api.JSON(c.Writer, http.StatusOK, h.applyProfilePrivacy(c, h.applyProfilePhotoPrivacy(c, ToPublicProfile(p))), nil)
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
		writeProfileNotFound(c)
		return
	}
	// SR-4: username lookup is the surface a harasser reaches for first —
	// they know the handle, not the UUID. Same denial, same DTO.
	if h.deniedByBlock(c, p.UserID) {
		return
	}

	api.JSON(c.Writer, http.StatusOK, h.applyProfilePrivacy(c, h.applyProfilePhotoPrivacy(c, ToPublicProfile(p))), nil)
}

// GetMe returns the caller's OWN profile, in full. The private fields are
// withheld from other viewers, not from their owner — a user must be able to
// see and correct their own date of birth.
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
	DisplayName   string     `json:"display_name"`
	Bio           string     `json:"bio"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
	CoverMediaID  *uuid.UUID `json:"cover_media_id"`
	FirstName     *string    `json:"first_name"`
	LastName      *string    `json:"last_name"`
	PreferredName *string    `json:"preferred_name"`
	Pronouns      *string    `json:"pronouns"`
	Gender        *string    `json:"gender"`
	DoB           *time.Time `json:"dob"`
	// SR-4: `username` is NOT settable here.
	//
	// A handle is an identity claim, not a display preference. Changing it
	// through the general profile update bypassed the dedicated
	// PUT /me/handle path and with it every protection that path carries:
	// the cooldown, the handle-history record, and the reservation of the
	// old handle. Without those, an account can rename itself repeatedly to
	// shed reputation, or take a handle someone else just released, and
	// nothing records that it happened. The field is deliberately absent
	// rather than ignored — a silently dropped value looks like a success.
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
	if req.AvatarMediaID != nil || req.CoverMediaID != nil {
		api.Error(c.Writer, http.StatusBadRequest, "DEDICATED_MEDIA_ROUTE_REQUIRED",
			"Use /v1/profiles/me/avatar or /v1/profiles/me/cover so media ownership and safety can be verified", nil, nil)
		return
	}
	if problems := validateProfileUpdate(req); len(problems) != 0 {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PROFILE", "Profile details are invalid", problems, nil)
		return
	}
	req.Website = normalizeProfileURL(req.Website)
	if req.CTAURL != nil && strings.TrimSpace(*req.CTAURL) != "" {
		normalized := normalizeProfileURL(*req.CTAURL)
		req.CTAURL = &normalized
	}

	themeColor := req.ProfileThemeColor
	if themeColor == "" {
		themeColor = "#1A73E8"
	}

	params := store.UpdateProfileParams{
		DisplayName:   req.DisplayName,
		Bio:           req.Bio,
		AvatarMediaID: req.AvatarMediaID,
		CoverMediaID:  req.CoverMediaID,
		FirstName:     req.FirstName,
		LastName:      req.LastName,
		PreferredName: req.PreferredName,
		Pronouns:      req.Pronouns,
		Gender:        req.Gender,
		DoB:           req.DoB,
		// SR-4: Username is intentionally not carried here — PUT /me/handle is
		// the only path that may change a handle. Passing nil leaves it unchanged.
		Username:          nil,
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
	if h.media == nil {
		api.Error(c.Writer, http.StatusServiceUnavailable, "MEDIA_AUTHORITY_UNAVAILABLE", "Profile image verification is unavailable", nil, nil)
		return
	}
	if err := h.media.RequireAttachable(c.Request.Context(), userID, req.MediaID, "avatar"); err != nil {
		h.log.Warn("avatar media authority refused reference", "err", err, "user_id", userID, "media_id", req.MediaID)
		api.Error(c.Writer, http.StatusConflict, "MEDIA_NOT_ATTACHABLE", "Choose an owned, ready and approved avatar image", nil, nil)
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
	if h.media == nil {
		api.Error(c.Writer, http.StatusServiceUnavailable, "MEDIA_AUTHORITY_UNAVAILABLE", "Profile image verification is unavailable", nil, nil)
		return
	}
	if err := h.media.RequireAttachable(c.Request.Context(), userID, req.MediaID, "cover"); err != nil {
		h.log.Warn("cover media authority refused reference", "err", err, "user_id", userID, "media_id", req.MediaID)
		api.Error(c.Writer, http.StatusConflict, "MEDIA_NOT_ATTACHABLE", "Choose an owned, ready and approved cover image", nil, nil)
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
	// LB-4: this is a PUBLIC per-user surface. It served unconditionally, so a
	// blocked account could read the links of the person who blocked them even
	// though the profile page itself was denied.
	userID, ok := h.publicTargetOrDeny(c)
	if !ok {
		return
	}

	links, err := h.svc.GetUserLinks(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch user links", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	// Links are user-supplied URLs that other people click. Unsafe schemes are
	// dropped rather than published — see SafePublicURL.
	api.JSON(c.Writer, http.StatusOK, PublicUserLinks(links), nil)
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

func (h *Handler) GetMyAbout(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	items, err := h.svc.GetAllAbout(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch owner about items", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if items == nil {
		items = []store.AboutItem{}
	}
	api.JSON(c.Writer, http.StatusOK, items, nil)
}

func (h *Handler) GetAllAbout(c *gin.Context) {
	// LB-4: public per-user surface — block denial applies.
	userID, ok := h.publicTargetOrDeny(c)
	if !ok {
		return
	}

	items, err := h.svc.GetAllAbout(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to fetch about items", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	// LB-4: this returned EVERY row regardless of `visibility`. About items
	// carry that column precisely because some of them are not public —
	// employer, education, relationship status, birthday — and all of them
	// were being published to anonymous callers.
	grouped := make(map[string][]PublicAboutItem)
	for _, item := range PublicAboutItems(items) {
		grouped[item.Section] = append(grouped[item.Section], item)
	}

	api.JSON(c.Writer, http.StatusOK, grouped, nil)
}

func (h *Handler) GetAboutBySection(c *gin.Context) {
	// LB-4: public per-user surface — block denial applies. This is also the
	// route that makes a per-section leak worst: a caller who knows which
	// section holds the sensitive field can request just that one.
	userID, ok := h.publicTargetOrDeny(c)
	if !ok {
		return
	}

	section := c.Param("section")
	items, err := h.svc.GetAboutBySection(c.Request.Context(), userID, section)
	if err != nil {
		h.log.Error("failed to fetch about section", "err", err, "user_id", userID, "section", section, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, PublicAboutItems(items), nil)
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
	if err := validateAboutItem(section, req.Data, req.Visibility); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ABOUT_ITEM", err.Error(), nil, nil)
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
	if err := validateProfileLink(req.Title, req.URL, req.Visibility); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PROFILE_LINK", err.Error(), nil, nil)
		return
	}
	req.URL = normalizeProfileURL(req.URL)

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
	if err := validateProfileLink(req.Title, req.URL, req.Visibility); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PROFILE_LINK", err.Error(), nil, nil)
		return
	}
	req.URL = normalizeProfileURL(req.URL)

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

// Friend system retired — see graph-service connections; profile.friendships kept dormant for backfill

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

	// HG2 follow-up: cursor pagination at celebrity scale. Triggered
	// by `?cursor=...` (any value, including empty cursor on page 1)
	// or `?paginate=cursor`. Legacy offset shape is preserved for
	// callers that haven't migrated.
	if c.Query("cursor") != "" || c.Query("paginate") == "cursor" {
		limit, _ := parsePagination(c)
		entries, next, err := h.svc.ListFollowersCursor(c.Request.Context(), userID, limit, c.Query("cursor"))
		if err != nil {
			h.log.Error("failed to list followers (cursor)", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
			return
		}
		if entries == nil {
			entries = []store.FollowerEntry{}
		}
		api.JSON(c.Writer, http.StatusOK, gin.H{
			"items":       entries,
			"next_cursor": next,
			"limit":       limit,
		}, nil)
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

	if c.Query("cursor") != "" || c.Query("paginate") == "cursor" {
		limit, _ := parsePagination(c)
		entries, next, err := h.svc.ListFollowingCursor(c.Request.Context(), userID, limit, c.Query("cursor"))
		if err != nil {
			h.log.Error("failed to list following (cursor)", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
			return
		}
		if entries == nil {
			entries = []store.FollowerEntry{}
		}
		api.JSON(c.Writer, http.StatusOK, gin.H{
			"items":       entries,
			"next_cursor": next,
			"limit":       limit,
		}, nil)
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

// Friend system retired — see graph-service connections; profile.friendships kept dormant for backfill

func (h *Handler) ListBlocks(c *gin.Context) {
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	// Cursor pagination mode — same trigger as the followers endpoints.
	if c.Query("cursor") != "" || c.Query("paginate") == "cursor" {
		limit, _ := parsePagination(c)
		blocks, next, err := h.svc.ListBlocksCursor(c.Request.Context(), userID, limit, c.Query("cursor"))
		if err != nil {
			h.log.Error("failed to list blocks (cursor)", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
			return
		}
		if blocks == nil {
			blocks = []store.Block{}
		}
		api.JSON(c.Writer, http.StatusOK, gin.H{
			"items":       blocks,
			"next_cursor": next,
			"limit":       limit,
		}, nil)
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
	// SR-4: batch lookup was the widest leak of all — a caller could post 100
	// user IDs and receive 100 full dates of birth in one response. Blocked
	// entries are omitted rather than refused: denying the whole request
	// because one entry is blocked lets a caller probe by bisection.
	publicProfiles := h.filterBlockedProfileMap(c, ToPublicProfileMap(profiles))
	c.JSON(http.StatusOK, h.applyProfilePrivacyMap(c, h.applyProfilePhotoPrivacyMap(c, publicProfiles)))
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
	// LB-4: public per-user surface — block denial applies. A module profile
	// (dating, commerce, …) is often MORE personal than the main profile, so
	// serving it to a blocked viewer while denying the profile page is the
	// worst version of a partial block.
	targetID, ok := h.publicTargetOrDeny(c)
	if !ok {
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

// ResolveHandle answers "this old handle now belongs to whom?".
//
// Module 3 CLB-2 — this is a target-resolving public surface and it was not
// block-gated.
//
// Every other public profile surface denies a blocked viewer, but this one
// took an OLD username and handed back the account's CURRENT username and its
// user id. That is worse than reading a profile: a harasser whose target
// renamed to escape them only needs the handle they already know, and this
// route tells them the new one. From there every gated surface is reachable
// with the identifier it just supplied.
//
// It was invisible to the route-completeness test because that test selected
// only patterns containing :userId, and this route resolves a username. The
// inventory in public_surface_test.go now classifies every public route by
// what it resolves, not by how its path happens to be spelled.
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
		// The SAME body a blocked viewer gets below. A distinguishable
		// "no redirect found" message would rebuild the oracle: a harasser
		// could tell "this handle was never used" from "this handle belongs
		// to someone who blocked me", which is the fact the block hides.
		writeProfileNotFound(c)
		return
	}

	// Symmetric block gate, applied BEFORE either identifier is emitted, and
	// fail-closed when the graph cannot be reached.
	if h.deniedByBlock(c, *userID) {
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

// ---------------------------------------------------------------
// Profile Stats
// ---------------------------------------------------------------

func (h *Handler) GetProfileStats(c *gin.Context) {
	// LB-4: public per-user surface — block denial applies. Follower and post
	// counts are exactly the signal a blocked person uses to keep watching.
	userID, ok := h.publicTargetOrDeny(c)
	if !ok {
		return
	}

	stats, err := h.svc.GetProfileStats(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("failed to get profile stats", "err", err, "user_id", userID)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

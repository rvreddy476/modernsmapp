package http

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/httpclient"
	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/user-service/internal/events"
	"github.com/atpost/user-service/internal/presence"
	"github.com/atpost/user-service/internal/service"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc *service.Service
	// users backs the user-object reads (GetUser, GetUserByUsername, GetMe).
	// It is svc in production; tests substitute an in-memory reader.
	users userReader
	// verifier admits sibling services to private profile fields. Nil admits
	// none. See profile_visibility.go.
	verifier         serviceTokenVerifier
	// blocks hides a profile across a block in either direction. It is
	// graphBlocks in production; nil (struct-literal test handlers) checks
	// nothing. See profile_blocks.go.
	blocks           blockChecker
	store            *store.Store
	graphURL         string
	presenceStore    presenceBackend
	graphClient      *http.Client
	internalRouteKey string
	dlqConsumer      *events.Consumer
	// pageAdmins is the platform-admin allowlist for page lifecycle actions
	// (approve/reject/suspend/disable + doc review), from PAGES_ADMIN_USER_IDS.
	pageAdmins map[string]bool
	// pageAdmin backs every platform page decision (legacy and admin-service
	// token paths) with its audit row. It is store in production; tests
	// substitute a fake. See admin_token.go.
	pageAdmin pageAdminStore
	// adminVerifier admits admin-service tokens (audience "social") to the
	// token-only family /v1/users/internal/admin. Nil admits none.
	adminVerifier *servicetoken.Verifier
}

func New(svc *service.Service, presenceStore *presence.Store, st *store.Store) *Handler {
	graphURL := os.Getenv("GRAPH_SERVICE_URL")
	if graphURL == "" {
		graphURL = "http://graph-service:8083"
	}
	h := &Handler{svc: svc, users: svc, store: st, graphURL: graphURL, presenceStore: presenceStore}
	h.graphClient = httpclient.NewWithBreaker(5*time.Second, "user->graph")
	h.blocks = graphBlocks{h: h}
	if st != nil {
		h.pageAdmin = st
	}
	h.pageAdmins = map[string]bool{}
	for _, id := range strings.Split(os.Getenv("PAGES_ADMIN_USER_IDS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			h.pageAdmins[id] = true
		}
	}
	return h
}

// isPageAdmin reports whether the caller (X-User-Id) is a platform page admin.
func (h *Handler) isPageAdmin(c *gin.Context) bool {
	return h.pageAdmins[c.GetHeader("X-User-Id")]
}

// WithDLQConsumer wires the Kafka consumer so the /internal DLQ routes can
// list and replay parked events.
func (h *Handler) WithDLQConsumer(c *events.Consumer) *Handler {
	h.dlqConsumer = c
	return h
}

// WithInternalRoutes enables the X-Internal-Service-Key gate on the
// service-to-service /internal/* routes only — without gating the
// gateway-facing /v1/* surface (those carry an end-user JWT, not the key).
func (h *Handler) WithInternalRoutes(key string) *Handler {
	h.internalRouteKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// No engine-wide internal-key gate: the gateway does not stamp the key on
	// /v1/users, /v1/pages or /v1/onboarding (user traffic must never carry the
	// credential that opens /internal), so such a gate would refuse every user
	// request. User routes trust the gateway-set X-User-Id; closing the
	// in-cluster forgery of that header needs a gateway-signed identity, which
	// is a platform design item, not a per-service key.

	// Service-to-service routes — gated independently of the global key so
	// they can be locked down even while /v1/* stays gateway-facing.
	internal := r.Group("/internal")
	// Fail closed: an unset key answers 503, never open. See internal_guard.go.
	internal.Use(requireInternalRouteKey(h.internalRouteKey))
	// Read-through projection repair: callers (e.g. graph-service) hit this
	// before an action that depends on app.users having the row.
	internal.POST("/users/:userId/ensure", h.EnsureUser)
	// Dead-letter queue: inspect + replay events the consumer failed on.
	internal.GET("/dlq", h.ListDLQ)
	internal.POST("/dlq/:id/replay", h.ReplayDLQ)
	// Projection health: master vs. local counts, reconcile + DLQ status.
	internal.GET("/projection/health", h.ProjectionHealth)
	// Page ownership: commerce-service checks a seller's business_page_id
	// before storing it (US-M3).
	internal.GET("/pages/:pageId/owner", h.GetPageOwner)
	// Module 1 P0-3: subscriber fan-out contract (internal-only —
	// subscriber identities never reach a public route).
	// SUPERSEDED 2026-09-12: post-service serves the same three routes with
	// the same JSON (its internal/http/channel_subscriptions_handler.go).
	// These stay up only until notification-service and feed-service switch
	// their base URL; then they go.
	internal.GET("/channels/by-owner/:userId", h.GetChannelByOwner)
	internal.GET("/channels/:channelId/subscriber-ids", h.ListSubscriberIDs)
	internal.GET("/users/:userId/subscribed-owner-ids", h.ListSubscribedOwners)

	// Admin console (admin-service tokens only): outside the /internal key
	// group, refused at the edge by the gateway's internal-path rule, and
	// judged ONLY by the token. See admin_token.go.
	h.registerAdminTokenRoutes(r.Group(InternalAdminPrefix))

	v1 := r.Group("/v1/users")
	{
		v1.GET("/by-username/:username", h.GetUserByUsername)
		v1.GET("/:userId", h.GetUser)
		// Reads of another account by id 404 while it is deactivated or
		// deletion-scheduled, as GetUser does (requireServableUser).
		v1.GET("/:userId/channels", h.requireServableUser, h.GetUserChannels)
		v1.GET("/:userId/links", h.requireServableUser, h.GetUserLinks)
		// SR-3: RETIRED — profile-service is the canonical writer for these
		// fields. See retired_profile_routes.go. The GET stays: a read is a
		// projection, and only the write created the divergence.
		v1.PUT("/me", retiredProfileWrite)
		v1.GET("/me", h.GetMe)
		v1.PUT("/me/links", h.UpdateMyLinks)
		v1.GET("/me/settings", h.GetMySettings)
		v1.PUT("/me/settings", h.UpdateMySettings)

		// About
		v1.GET("/:userId/about", h.requireServableUser, h.GetAbout)
		v1.GET("/:userId/about/:section", h.requireServableUser, h.GetAboutSection)
		// US-L2: RETIRED — profile-service writes About. See retired_social_routes.go.
		v1.PUT("/me/about/:section", retiredUserRoute(whyAboutWriteMoved, canonicalAboutUpsertRoute))
		v1.DELETE("/me/about/:section/:itemId", retiredUserRoute(whyAboutWriteMoved, canonicalAboutDeleteRoute))

		// Status/Mood
		v1.PATCH("/me/status", h.UpdateStatus)

		// Reputation & Endorsements (US-L1) and Compatibility (US-L2): RETIRED.
		v1.GET("/:userId/reputation", h.requireServableUser, retiredUserRoute(whyEndorsementsRetired, ""))
		v1.GET("/:userId/endorsements", h.requireServableUser, retiredUserRoute(whyEndorsementsRetired, ""))
		v1.POST("/:userId/endorse", h.requireServableUser, retiredUserRoute(whyEndorsementsRetired, ""))
		v1.GET("/:userId/compatibility", h.requireServableUser, retiredUserRoute(whyCompatibilityRetired, ""))

		// Link Analytics. The anonymous click counter is RETIRED (US-L4); the
		// owner's read of the counts stays.
		v1.POST("/links/:platform/click", retiredUserRoute(whyCounterRetired, ""))
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
		// RETIRED 2026-09-12: post-service owns Tube subscriptions (follow +
		// notify behind one button). See retired_subscription_routes.go.
		channelByID.POST("/subscribe", retiredSubscriptionRoute("POST /v1/channels/{ref}/subscribe"))
		channelByID.DELETE("/subscribe", retiredSubscriptionRoute("DELETE /v1/channels/{ref}/subscribe"))
		channelByID.GET("/subscription", retiredSubscriptionRoute("GET /v1/channels/{ref}/subscription"))
		// Subscriber identities never had a public reader in the Tube
		// contract; the internal fan-out route is the only reader.
		channelByID.GET("/subscribers", retiredSubscriptionRoute("GET /internal/channels/{channelId}/subscriber-ids"))
	}

	// User subscriptions list: RETIRED, see above.
	v1.GET("/:userId/subscriptions", retiredSubscriptionRoute("GET "+canonicalSubscriptionsRoute))

	// Profile extras: pins, portfolio, QR codes, digital wellbeing
	h.registerProfileExtrasRoutes(r)

	// Onboarding
	r.POST("/v1/onboarding/ensure-publisher", h.EnsurePublisher)

	// Business Pages — use :id for all sub-paths (Gin requires uniform wildcard names)
	pagesGrp := r.Group("/v1/pages")
	{
		pagesGrp.GET("", h.DiscoverPages)
		pagesGrp.GET("/:id", h.GetBusinessPage)
		pagesGrp.PATCH("/:id", h.UpdateBusinessPage)
		pagesGrp.DELETE("/:id", h.DeleteBusinessPage)
		pagesGrp.POST("/:id/follow", h.FollowPage)
		pagesGrp.DELETE("/:id/follow", h.UnfollowPage)
		pagesGrp.GET("/:id/reviews", h.GetPageReviews)
		pagesGrp.POST("/:id/reviews", h.SubmitReview)
		// Lifecycle (owner/admin submit; platform-admin approve/reject/suspend/disable)
		pagesGrp.POST("/:id/submit-review", h.SubmitPageForReview)
		pagesGrp.POST("/:id/approve", h.ApprovePage)
		pagesGrp.POST("/:id/reject", h.RejectPage)
		pagesGrp.POST("/:id/suspend", h.SuspendPage)
		pagesGrp.POST("/:id/disable", h.DisablePage)
		// Verification documents
		pagesGrp.GET("/:id/documents", h.ListPageDocuments)
		pagesGrp.POST("/:id/documents", h.AddPageDocument)
		pagesGrp.POST("/:id/documents/:docId/:action", h.ReviewPageDocument)
	}
	myPages := r.Group("/v1/users/me/pages")
	{
		myPages.POST("", h.CreateBusinessPage)
		myPages.GET("", h.ListMyBusinessPages)
	}
}

func (h *Handler) GetUser(c *gin.Context) {
	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}

	u, err := h.users.GetUser(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// A deactivated or deletion-scheduled account reads as missing to all but
	// its owner and service callers (US-M1), and so does an account on the
	// other side of a block (profile_blocks.go).
	if !h.admitProfileRead(c, u) {
		return
	}

	// Private fields (first/last name, dob, gender) go to the owner or a
	// service-token caller only. See profile_visibility.go.
	api.JSON(c.Writer, http.StatusOK, h.profileFor(c, u), nil)
}

func (h *Handler) GetUserByUsername(c *gin.Context) {
	username := c.Param("username")
	if username == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Username is required", nil)
		return
	}

	u, err := h.users.GetUserByUsername(c.Request.Context(), username)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !h.admitProfileRead(c, u) {
		return
	}

	// Same rule as GetUser: the owner is whoever the handle resolves to.
	api.JSON(c.Writer, http.StatusOK, h.profileFor(c, u), nil)
}

func (h *Handler) GetMe(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	if userIDStr == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil)
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	// /me is the owner by construction, so it returns every field.
	u, err := h.users.GetUser(c.Request.Context(), userID)
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

// GetMySettings and UpdateMySettings are RETIRED. See settings_moved.go.
//
// Both used to read and write this service's three-column privacy record.
// That record is not the one anything enforces, so a client that wrote to it
// was told 200 and protected by nothing.
func (h *Handler) GetMySettings(c *gin.Context) { settingsMoved(c) }

func (h *Handler) UpdateMySettings(c *gin.Context) { settingsMoved(c) }

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

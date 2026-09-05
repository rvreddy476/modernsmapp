package http

// The shop front's routes: a product's gallery, the landing page,
// favourites, the category strip, and the merchandiser's banner admin.
//
// Two of these replace nothing — `/home` and `/favourites` did not exist, and
// the app's first screen was a bare product grid because there was no other
// endpoint to open it on. The media routes replace a single-id POST that
// could attach an image but could not order, reorder or remove one, which is
// why `product_media` had zero rows: there was no way for a seller to
// assemble a gallery, so nothing did.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterStorefrontRoutes adds the shopper-facing storefront surface.
//
// `v1` is the /v1/commerce group; `r` is needed for the internal admin group,
// which carries its own key middleware.
func (h *Handler) RegisterStorefrontRoutes(r *gin.Engine, v1 *gin.RouterGroup) {
	// ── The landing page ─────────────────────────────────────
	v1.GET("/home", h.GetHome)

	// ── Product gallery (seller writes, public read) ─────────
	//
	// The read is registered in RegisterRoutes alongside the rest of the
	// catalogue; these are the writes it had none of.
	v1.PUT("/products/:productId/media/order", h.ReorderProductMedia)
	v1.DELETE("/products/:productId/media/:mediaId", h.DeleteProductMedia)

	// ── Favourites ───────────────────────────────────────────
	v1.GET("/favourites", h.ListFavourites)
	v1.POST("/favourites", h.AddFavourite)
	v1.DELETE("/favourites/:productId", h.RemoveFavourite)

	// ── Banner administration (internal key) ─────────────────
	//
	// A separate group from the onboarding admin one because it is registered
	// from here, but it carries the SAME middleware: merchandising is an ops
	// capability, not a seller one, and a seller who could write banners
	// could put their own storefront on every shopper's home screen.
	adm := r.Group("/v1/commerce/internal")
	if h.internalKey != "" {
		adm.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	// ── Commerce as a content authority for media-service ────
	//
	// media-service's delivery gate refuses a protected asset unless the
	// service that OWNS the content referencing it says yes. Its authority
	// list is post-service, chat-message-service and identity-profile — never
	// commerce — so every product photograph came back
	// `no_visible_post_or_story` for every shopper, and the catalogue drew
	// grey boxes however many images were attached.
	//
	// This is commerce's side of that contract, deliberately shaped exactly
	// like post-service's (`POST /v1/internal/media-access`, plus a `/batch`
	// form) so media-service needs only a registration, not a new protocol.
	adm.POST("/media-access", h.MediaAccess)
	adm.POST("/media-access/batch", h.MediaAccessBatch)

	adm.GET("/banners", h.AdminListBanners)
	adm.POST("/banners", h.AdminSaveBanner)
	adm.PUT("/banners/:bannerId", h.AdminSaveBannerByID)
	adm.DELETE("/banners/:bannerId", h.AdminDeleteBanner)
}

// optionalUserID reads the caller's id without refusing anonymous requests.
//
// The browse surfaces are public — a shopper who has not signed in must still
// see the catalogue — but a signed-in one should see their hearts filled in.
// getUserID writes a 401 and cannot be used here; returning uuid.Nil lets the
// favourites lookup skip itself, which is exactly what it should do when
// there is no user to have favourites.
func optionalUserID(c *gin.Context) uuid.UUID {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		return uuid.Nil
	}
	return id
}

// writeStorefrontError maps this file's sentinels onto statuses.
//
// Media refusals reuse internal/media's vocabulary so a seller attaching
// somebody else's photograph gets 403 and a seller attacking during a
// media-service outage gets 503 — the distinction internal/media exists to
// preserve, which a generic 500 would erase.
func writeStorefrontError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrTooManyMedia),
		errors.Is(err, service.ErrNoMedia),
		errors.Is(err, service.ErrDuplicateMedia),
		errors.Is(err, service.ErrInvalidBanner):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
	case errors.Is(err, service.ErrMediaNotOnProduct):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, service.ErrProductNotFound),
		errors.Is(err, postgres.ErrProductNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "product not found", nil)
	case errors.Is(err, service.ErrBannerNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "banner not found", nil)
	case errors.Is(err, service.ErrNotOrderOwner), errors.Is(err, service.ErrNotProductOwner):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FORBIDDEN", "not your product", nil)
	case errors.Is(err, media.ErrNotYourMedia):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "NOT_YOUR_MEDIA", err.Error(), nil)
	case errors.Is(err, media.ErrMediaNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "MEDIA_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, media.ErrMediaNotReady), errors.Is(err, media.ErrMediaNotPassed),
		errors.Is(err, media.ErrMediaWrongKind):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "MEDIA_NOT_USABLE", err.Error(), nil)
	case errors.Is(err, media.ErrMediaUnavailable):
		// The seller did nothing wrong; retrying may work. 503, never 403.
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable,
			"DEPENDENCY_UNAVAILABLE", "media could not be verified; retry", nil)
	default:
		handleErr(c, err)
	}
}

// ─── The landing page ───────────────────────────────────────────────────

// GetHome GET /v1/commerce/home — the shop's first screen.
//
//	{"banners":[…],"sections":[{"key","title","products":[…]}]}
//
// Public. A signed-in caller's X-User-Id fills in `is_favourite` on every
// product; an anonymous one simply does not get the field.
func (h *Handler) GetHome(c *gin.Context) {
	page, err := h.svc.Home(c.Request.Context(), optionalUserID(c))
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

// ─── Product gallery ────────────────────────────────────────────────────

// setProductMediaReq is the gallery write body.
//
// `media_ids` REPLACES the gallery, in the order given; the first is the
// cover. `media_id` is the original single-append shape and is retained so
// the existing caller keeps working — the two are mutually exclusive and the
// handler says so rather than guessing.
type setProductMediaReq struct {
	MediaIDs []uuid.UUID `json:"media_ids"`

	// Legacy single-append fields.
	MediaID   *uuid.UUID `json:"media_id"`
	MediaType string     `json:"media_type"`
	SortOrder int        `json:"sort_order"`
}

// ReorderProductMedia PUT /v1/commerce/products/:productId/media/order —
// seller only, owns the product.
//
//	{"media_ids":["…","…"]}   a permutation of the existing gallery
func (h *Handler) ReorderProductMedia(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	var req setProductMediaReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.ReorderProductMedia(c.Request.Context(), productID, userID, req.MediaIDs)
	if err != nil {
		writeStorefrontError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": out}, nil)
}

// DeleteProductMedia DELETE /v1/commerce/products/:productId/media/:mediaId —
// seller only, owns the product. Returns the gallery that remains, so the
// editor does not need a follow-up read.
func (h *Handler) DeleteProductMedia(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	mediaID, ok := parseUUID(c, "mediaId")
	if !ok {
		return
	}
	out, err := h.svc.DeleteProductMedia(c.Request.Context(), productID, userID, mediaID)
	if err != nil {
		writeStorefrontError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": out}, nil)
}

// ─── Favourites ─────────────────────────────────────────────────────────

type favouriteReq struct {
	ProductID uuid.UUID `json:"product_id" binding:"required"`
}

// ListFavourites GET /v1/commerce/favourites?limit=&cursor= — the shopper's
// hearted products, as full product summaries so the list renders with the
// same card component the grid uses.
func (h *Handler) ListFavourites(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	page, err := h.svc.ListFavourites(c.Request.Context(), userID, limit, c.Query("cursor"))
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, page, nil)
}

// AddFavourite POST /v1/commerce/favourites {"product_id":"…"}.
//
// Idempotent: hearting something twice is 200, not 409. A double-tap and the
// retry a flaky connection produces are the same request, and answering the
// second one with an error would make the client show a failure for a state
// it successfully reached.
func (h *Handler) AddFavourite(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req favouriteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.AddFavourite(c.Request.Context(), userID, req.ProductID); err != nil {
		writeStorefrontError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"product_id": req.ProductID, "is_favourite": true}, nil)
}

// RemoveFavourite DELETE /v1/commerce/favourites/:productId. Also idempotent
// — see the store.
func (h *Handler) RemoveFavourite(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	if err := h.svc.RemoveFavourite(c.Request.Context(), userID, productID); err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"product_id": productID, "is_favourite": false}, nil)
}

// ─── Media access ───────────────────────────────────────────────────────

type mediaAccessReq struct {
	// A blank viewer is the anonymous shopper, and is a real audience for a
	// product photograph — the page is public. It is NOT parsed with
	// getUserID, which would answer 401 and turn "not signed in" into an
	// outage for the whole catalogue.
	ViewerID string   `json:"viewer_id"`
	MediaID  string   `json:"media_id"`
	MediaIDs []string `json:"media_ids"`
}

func (r mediaAccessReq) viewer() uuid.UUID {
	id, err := uuid.Parse(r.ViewerID)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// MediaAccess POST /v1/commerce/internal/media-access — internal key only.
//
//	{"viewer_id":"…"|"", "media_id":"…"}  →  {"allowed":true}
func (h *Handler) MediaAccess(c *gin.Context) {
	var req mediaAccessReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	mediaID, err := uuid.Parse(req.MediaID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "media_id must be a UUID", nil)
		return
	}
	allowed, err := h.svc.MediaAccess(c.Request.Context(), req.viewer(), mediaID)
	if err != nil {
		// 5xx, never `allowed:false`. media-service distinguishes a resolved
		// denial from an unresolved one, and reporting a database blip as a
		// denial would make it cache "this image does not exist".
		handleErr(c, err)
		return
	}
	// The bare shape media-service decodes — not the shared envelope, whose
	// `data` wrapper its authorizer does not unwrap.
	c.JSON(http.StatusOK, gin.H{"allowed": allowed})
}

// MediaAccessBatch POST /v1/commerce/internal/media-access/batch.
//
//	{"viewer_id":"…"|"", "media_ids":["…"]}  →  {"allowed":{"…":true}}
func (h *Handler) MediaAccessBatch(c *gin.Context) {
	var req mediaAccessReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	ids := make([]uuid.UUID, 0, len(req.MediaIDs))
	for _, raw := range req.MediaIDs {
		if id, err := uuid.Parse(raw); err == nil {
			ids = append(ids, id)
		}
	}
	set, err := h.svc.MediaAccessBatch(c.Request.Context(), req.viewer(), ids)
	if err != nil {
		handleErr(c, err)
		return
	}
	allowed := make(map[string]bool, len(set))
	for id, ok := range set {
		allowed[id.String()] = ok
	}
	c.JSON(http.StatusOK, gin.H{"allowed": allowed})
}

// ─── Banner administration ──────────────────────────────────────────────

func (h *Handler) AdminListBanners(c *gin.Context) {
	out, err := h.svc.ListBanners(c.Request.Context())
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": out}, nil)
}

func (h *Handler) AdminSaveBanner(c *gin.Context) {
	var in service.BannerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	b, err := h.svc.SaveBanner(c.Request.Context(), in)
	if err != nil {
		writeStorefrontError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, b, nil)
}

// AdminSaveBannerByID PUT /v1/commerce/internal/banners/:bannerId.
//
// The path id wins over any id in the body: a PUT names its target in the
// URL, and letting a body id override it would let one request update a
// different banner than the one the caller addressed.
func (h *Handler) AdminSaveBannerByID(c *gin.Context) {
	id, ok := parseUUID(c, "bannerId")
	if !ok {
		return
	}
	var in service.BannerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	in.ID = &id
	b, err := h.svc.SaveBanner(c.Request.Context(), in)
	if err != nil {
		writeStorefrontError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, b, nil)
}

func (h *Handler) AdminDeleteBanner(c *gin.Context) {
	id, ok := parseUUID(c, "bannerId")
	if !ok {
		return
	}
	if err := h.svc.DeleteBanner(c.Request.Context(), id); err != nil {
		writeStorefrontError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

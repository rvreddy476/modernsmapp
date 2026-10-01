package http

// MStore engagement (pinned 1 Oct 2026, shop-engagement contract §1-4):
//
//	GET    /products/:productId/delivery-estimate?pincode=NNNNNN   public
//	PUT    /products/:productId/reaction   {"kind":"like"|"dislike"} auth
//	DELETE /products/:productId/reaction                             auth
//	POST   /products/:productId/share      {"channel":…}            public, 204
//	PUT    /reviews/:reviewId/vote         {"vote":"helpful"|"not_helpful"} auth
//	DELETE /reviews/:reviewId/vote                                   auth
//
// Buyer visibility (d107aa1f) on every one: the estimate is a READ and goes
// through productReadable like the other product reads (the owning seller
// may preview it); a like or a share is a public WRITE and requires the
// product to be live for shoppers with no owner exception; a vote resolves
// the review's product and answers 404 REVIEW_NOT_FOUND when it is hidden.
//
// A dislike count is never written to any response: a dislike is private to
// the shopper who cast it.

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterEngagementRoutes adds the engagement routes to /v1/commerce.
func (h *Handler) RegisterEngagementRoutes(v1 *gin.RouterGroup) {
	v1.GET("/products/:productId/delivery-estimate", h.GetDeliveryEstimate)
	v1.PUT("/products/:productId/reaction", h.PutProductReaction)
	v1.DELETE("/products/:productId/reaction", h.DeleteProductReaction)
	v1.POST("/products/:productId/share", h.PostProductShare)
	v1.PUT("/reviews/:reviewId/vote", h.PutReviewVote)
	v1.DELETE("/reviews/:reviewId/vote", h.DeleteReviewVote)
}

// ─── Rate limits ─────────────────────────────────────────────────────────

// windowLimiter admits at most max events per key per fixed window. In
// process, so per pod: the gateway's shared limiter is the global one, and
// this exists so one client cannot spin a public counter from a script.
type windowLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	m      map[string]*limiterBucket
}

type limiterBucket struct {
	start time.Time
	n     int
}

const limiterMaxKeys = 50000

func newWindowLimiter(max int, window time.Duration) *windowLimiter {
	return &windowLimiter{max: max, window: window, now: time.Now, m: map[string]*limiterBucket{}}
}

func (l *windowLimiter) allow(key string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.m[key]
	if !ok || now.Sub(b.start) >= l.window {
		if !ok && len(l.m) >= limiterMaxKeys {
			for k, old := range l.m {
				if now.Sub(old.start) >= l.window {
					delete(l.m, k)
				}
			}
		}
		l.m[key] = &limiterBucket{start: now, n: 1}
		return true
	}
	if b.n >= l.max {
		return false
	}
	b.n++
	return true
}

// engagementLimits are the three throttles the engagement writes use.
type engagementLimits struct {
	// writes: reaction and vote writes, per signed-in user. Favourites have
	// no service-side limit of their own; these get one because they move
	// public counters.
	writes *windowLimiter
	// shares: share requests per actor (user, else client IP); over it, 429.
	shares *windowLimiter
	// shareOnce: one COUNTED share per actor per product per window; a
	// repeat inside it still answers 204 and counts nothing, so tapping
	// Share twice is one share.
	shareOnce *windowLimiter
}

func newEngagementLimits() *engagementLimits {
	return &engagementLimits{
		writes:    newWindowLimiter(60, time.Minute),
		shares:    newWindowLimiter(20, time.Minute),
		shareOnce: newWindowLimiter(1, 10*time.Minute),
	}
}

func (l *engagementLimits) allowWrite(user uuid.UUID) bool {
	return l == nil || l.writes.allow(user.String())
}

func writeRateLimited(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED",
		"too many requests; please slow down", nil)
}

// requireProductLive answers 404 PRODUCT_NOT_FOUND (via handleErr) unless
// the product is live for shoppers.
func (h *Handler) requireProductLive(c *gin.Context, productID uuid.UUID) bool {
	if err := h.svc.RequireProductLive(c.Request.Context(), productID); err != nil {
		handleErr(c, err)
		return false
	}
	return true
}

// ─── Delivery estimate ───────────────────────────────────────────────────

// GetDeliveryEstimate GET /v1/commerce/products/:productId/delivery-estimate.
func (h *Handler) GetDeliveryEstimate(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	if !h.productReadable(c, id) {
		return
	}
	pin := strings.TrimSpace(c.Query("pincode"))
	source := service.PincodeFromQuery
	if pin == "" {
		viewer := optionalUserID(c)
		if viewer == uuid.Nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "PINCODE_REQUIRED",
				"enter a pincode to see when this can be delivered", nil)
			return
		}
		saved, found, err := h.svc.DefaultPincode(ctx, viewer)
		if err != nil {
			handleErr(c, err)
			return
		}
		if !found || saved == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "PINCODE_REQUIRED",
				"add a delivery address or enter a pincode", nil)
			return
		}
		pin, source = saved, service.PincodeFromDefaultAddress
	}
	// The pincode's shape (a query value or a saved address alike) is
	// checked once, in EstimateDelivery, before anything is looked up.
	est, err := h.svc.EstimateDelivery(ctx, id, pin, source)
	if err != nil {
		if errors.Is(err, service.ErrInvalidPincode) {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_PINCODE",
				"a pincode is six digits", nil)
			return
		}
		handleErr(c, err) // ErrCourierUnavailable → 503 COURIER_UNAVAILABLE
		return
	}
	api.JSON(c.Writer, http.StatusOK, est, nil)
}

// ─── Reactions ───────────────────────────────────────────────────────────

type reactionReq struct {
	Kind string `json:"kind"`
}

// PutProductReaction PUT /v1/commerce/products/:productId/reaction.
func (h *Handler) PutProductReaction(c *gin.Context) {
	ctx := c.Request.Context()
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	if !h.requireProductLive(c, id) {
		return
	}
	var req reactionReq
	if err := c.ShouldBindJSON(&req); err != nil || (req.Kind != postgres.ReactionLike && req.Kind != postgres.ReactionDislike) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_BODY",
			`kind must be "like" or "dislike"`, nil)
		return
	}
	if !h.limits.allowWrite(userID) {
		writeRateLimited(c)
		return
	}
	st, err := h.svc.ReactToProduct(ctx, id, userID, req.Kind)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

// DeleteProductReaction DELETE /v1/commerce/products/:productId/reaction.
func (h *Handler) DeleteProductReaction(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	if !h.requireProductLive(c, id) {
		return
	}
	if !h.limits.allowWrite(userID) {
		writeRateLimited(c)
		return
	}
	st, err := h.svc.ClearProductReaction(c.Request.Context(), id, userID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

// ─── Share ───────────────────────────────────────────────────────────────

type shareReq struct {
	Channel string `json:"channel"`
}

// PostProductShare POST /v1/commerce/products/:productId/share → 204.
func (h *Handler) PostProductShare(c *gin.Context) {
	ctx := c.Request.Context()
	id, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	if !h.requireProductLive(c, id) {
		return
	}
	var req shareReq
	if err := c.ShouldBindJSON(&req); err != nil || !service.ShareChannels[req.Channel] {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"channel must be one of native, whatsapp, copy_link, other", nil)
		return
	}
	actor := "ip:" + c.ClientIP()
	if u := optionalUserID(c); u != uuid.Nil {
		actor = "u:" + u.String()
	}
	if h.limits != nil {
		if !h.limits.shares.allow(actor) {
			writeRateLimited(c)
			return
		}
		if !h.limits.shareOnce.allow(actor + "|" + id.String()) {
			c.Status(http.StatusNoContent)
			return
		}
	}
	if err := h.svc.RecordShare(ctx, id); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ─── Review helpful votes ────────────────────────────────────────────────

type reviewVoteReq struct {
	Vote string `json:"vote"`
}

func writeReviewVoteError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrReviewNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "REVIEW_NOT_FOUND", "review not found", nil)
	case errors.Is(err, service.ErrCannotVoteOwnReview):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "CANNOT_VOTE_OWN_REVIEW",
			"you cannot vote on your own review", nil)
	case errors.Is(err, service.ErrInvalidVote):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_BODY",
			`vote must be "helpful" or "not_helpful"`, nil)
	default:
		handleErr(c, err)
	}
}

// PutReviewVote PUT /v1/commerce/reviews/:reviewId/vote.
func (h *Handler) PutReviewVote(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	reviewID, ok := parseUUID(c, "reviewId")
	if !ok {
		return
	}
	var req reviewVoteReq
	_ = c.ShouldBindJSON(&req) // a bad body is ErrInvalidVote, after the 404 check
	if !h.limits.allowWrite(userID) {
		writeRateLimited(c)
		return
	}
	st, err := h.svc.VoteOnReview(c.Request.Context(), reviewID, userID, req.Vote)
	if err != nil {
		writeReviewVoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

// DeleteReviewVote DELETE /v1/commerce/reviews/:reviewId/vote.
func (h *Handler) DeleteReviewVote(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	reviewID, ok := parseUUID(c, "reviewId")
	if !ok {
		return
	}
	if !h.limits.allowWrite(userID) {
		writeRateLimited(c)
		return
	}
	st, err := h.svc.ClearReviewVote(c.Request.Context(), reviewID, userID)
	if err != nil {
		writeReviewVoteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

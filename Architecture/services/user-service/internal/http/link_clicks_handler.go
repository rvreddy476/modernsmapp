package http

// Profile link click counter (replaces the retired anonymous counter, US-L4).
//
//	POST /v1/users/:userId/links/:platform/click
//
// The viewer must be signed in (gateway-set X-User-Id; 401 otherwise). The
// owner must be servable and no block may stand between viewer and owner
// (requireServableUser: 404 exactly like a missing account, 503 when the
// block check cannot be answered). An unknown platform is 404 LINK_NOT_FOUND.
//
// One click counts per viewer per link per hour, decided by a Redis SET NX
// on a hashed key, and only the per-link total is stored
// (user_links.click_count, read by the owner at GET /v1/users/me/links/analytics).
// The owner's own clicks do not count. Without a deduper, or when it errors,
// nothing counts: the numbers must not be inflatable by knocking Redis over.
// A click that is not counted is still 200, with counted=false; the response
// never says who else clicked.
//
// The old POST /v1/users/links/:platform/click stays 410 ROUTE_RETIRED: it
// named no owner and took no identity.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// LinkClickDedupeWindow is how long one viewer's click on one link stays
// counted once.
const LinkClickDedupeWindow = time.Hour

// linkClickStore is the slice of *store.Store the counter uses.
type linkClickStore interface {
	UserLinkExists(ctx context.Context, ownerID uuid.UUID, platform string) (bool, error)
	IncrementLinkClick(ctx context.Context, ownerID uuid.UUID, platform string) error
}

// clickDeduper answers true for the first click on key within window.
type clickDeduper interface {
	FirstClick(ctx context.Context, key string, window time.Duration) (bool, error)
}

type redisClickDeduper struct{ rdb *redis.Client }

func (d redisClickDeduper) FirstClick(ctx context.Context, key string, window time.Duration) (bool, error) {
	return d.rdb.SetNX(ctx, key, "1", window).Result()
}

// WithLinkClickDedupe wires the Redis deduper (main.go). Nil leaves none,
// which counts nothing.
func (h *Handler) WithLinkClickDedupe(rdb *redis.Client) *Handler {
	if rdb != nil {
		h.clickDedupe = redisClickDeduper{rdb: rdb}
	}
	return h
}

// withLinkClicks substitutes the store and deduper (tests).
func (h *Handler) withLinkClicks(s linkClickStore, d clickDeduper) *Handler {
	h.linkClicks = s
	h.clickDedupe = d
	return h
}

// linkClickKey is the dedupe key. The viewer is hashed so the key names no
// account; it expires with the window.
func linkClickKey(ownerID uuid.UUID, platform string, viewerID uuid.UUID) string {
	sum := sha256.Sum256([]byte(ownerID.String() + "\x00" + platform + "\x00" + viewerID.String()))
	return "user_link_click:" + hex.EncodeToString(sum[:])
}

// requireSignedInViewer refuses a request without a gateway-set identity.
func requireSignedInViewer(c *gin.Context) {
	if _, err := uuid.Parse(strings.TrimSpace(c.GetHeader("X-User-Id"))); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Sign in to continue", nil)
		c.Abort()
		return
	}
	c.Next()
}

// LinkClickResult is the response body's data.
type LinkClickResult struct {
	Counted bool `json:"counted"`
}

func (h *Handler) RecordLinkClick(c *gin.Context) {
	ctx := c.Request.Context()
	viewerID, err := uuid.Parse(strings.TrimSpace(c.GetHeader("X-User-Id")))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Sign in to continue", nil)
		return
	}
	ownerID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		respondUserNotFound(c)
		return
	}
	platform := c.Param("platform")
	if h.linkClicks == nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "LINK_CLICKS_UNAVAILABLE", "Link clicks are unavailable right now", nil)
		return
	}
	exists, err := h.linkClicks.UserLinkExists(ctx, ownerID, platform)
	if err != nil {
		slog.WarnContext(ctx, "link click: lookup failed", "err", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Link lookup failed", nil)
		return
	}
	if !exists {
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "LINK_NOT_FOUND", "Link not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, LinkClickResult{Counted: h.countLinkClick(ctx, ownerID, platform, viewerID)}, nil)
}

// countLinkClick counts one click unless it is the owner's, the viewer
// already clicked this link in the window, or the deduper is missing or
// failing. A failed increment is logged, never an error.
func (h *Handler) countLinkClick(ctx context.Context, ownerID uuid.UUID, platform string, viewerID uuid.UUID) bool {
	if viewerID == ownerID {
		return false
	}
	if h.clickDedupe == nil {
		return false
	}
	first, err := h.clickDedupe.FirstClick(ctx, linkClickKey(ownerID, platform, viewerID), LinkClickDedupeWindow)
	if err != nil {
		slog.WarnContext(ctx, "link click not counted: dedupe unavailable", "err", err)
		return false
	}
	if !first {
		return false
	}
	if err := h.linkClicks.IncrementLinkClick(ctx, ownerID, platform); err != nil {
		slog.WarnContext(ctx, "link click not counted", "err", err)
		return false
	}
	return true
}

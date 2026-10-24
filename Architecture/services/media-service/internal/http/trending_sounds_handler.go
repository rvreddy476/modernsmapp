package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/o11y/trace"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Trending sounds (2026-10-24; the rules are service/trending_sounds.go).
//
//	GET /v1/audio/trending?filter=all|rising&cursor=&limit=   signed in
//	  200 {"data": [{"rank": 1, "sound": <Sound>, "uses_7d": 42,
//	                 "uses_prev_7d": 10, "rising": true}],
//	       "meta": {"next_cursor": "<opaque>" | null,
//	                "updated_at": RFC3339 | null, "request_id": "…"}}
//	      rank is the sound's place in the whole ranking (on the rising
//	      list too); data is [] before the first run; limit defaults to 20
//	      and is held to 1..50.
//	  400 BAD_REQUEST     limit is not a number
//	  400 INVALID_FILTER  filter is neither all nor rising
//	  400 INVALID_CURSOR  the cursor is not one this route issued
//	  401 UNAUTHORIZED    signed out (authMW)
//	  503 DEPENDENCY_UNAVAILABLE  a block or audience lookup did not answer,
//	      or the ranking is not wired; retry
//
//	GET /v1/audio/:audioId gains "trending": {"rank": 3, "rising": false} | null.

// trendingSoundsService is the slice of the ranking the routes use.
type trendingSoundsService interface {
	List(ctx context.Context, viewerID uuid.UUID, filter, cursor string, limit int) (*service.TrendingPage, error)
	Badge(ctx context.Context, audioID uuid.UUID) (*service.TrendingBadge, error)
	Extras(ctx context.Context, audioID uuid.UUID) (*service.SoundExtras, error)
}

// GetSoundExtrasInternal — GET /v1/media/internal/sounds/:audioId/extras
//
// post-service, building its sound page (GET /v1/posts/by-sound/:id), which
// has already decided its viewer may hear the sound:
//
//	200 {"data": {"trending": {"rank": 3, "rising": false} | null,
//	              "cover_url": "https://…" | "/v1/media/<id>/serve/thumb_150" | null}}
//	400 BAD_REQUEST   bad id
//	404 NOT_FOUND     no such sound
//	503 SOUND_EXTRAS_UNAVAILABLE   the ranking is not wired or the store failed
func (h *Handler) GetSoundExtrasInternal(c *gin.Context) {
	ctx := c.Request.Context()
	audioID, err := uuid.Parse(c.Param("audioId"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil)
		return
	}
	if h.trending == nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "SOUND_EXTRAS_UNAVAILABLE", "Sound extras are unavailable; retry", nil)
		return
	}
	extras, err := h.trending.Extras(ctx, audioID)
	switch {
	case errors.Is(err, service.ErrSoundNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Audio track not found", nil)
	case err != nil:
		slog.Warn("sound extras: unavailable", "audio_track_id", audioID, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "SOUND_EXTRAS_UNAVAILABLE", "Sound extras are unavailable; retry", nil)
	default:
		api.JSON(c.Writer, http.StatusOK, extras, nil)
	}
}

// WithTrendingSounds wires the ranking. Without it the list answers 503 and
// every sound page carries "trending": null.
func (h *Handler) WithTrendingSounds(t *service.TrendingSounds) *Handler {
	if t != nil {
		h.trending = t
	}
	return h
}

type trendingMeta struct {
	NextCursor *string    `json:"next_cursor"`
	UpdatedAt  *time.Time `json:"updated_at"`
	RequestID  string     `json:"request_id,omitempty"`
}

type trendingResponse struct {
	Data []service.TrendingItem `json:"data"`
	Meta trendingMeta           `json:"meta"`
}

// GetTrendingSounds — GET /v1/audio/trending
func (h *Handler) GetTrendingSounds(c *gin.Context) {
	ctx := c.Request.Context()
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "limit must be a number", nil)
			return
		}
		if n < 1 {
			n = 1
		}
		limit = n
	}
	if h.trending == nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "Trending sounds are unavailable; retry", nil)
		return
	}
	page, err := h.trending.List(ctx, deliveryViewer(c), c.Query("filter"), c.Query("cursor"), limit)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTrendingFilter):
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_FILTER", "filter must be all or rising", nil)
		case errors.Is(err, service.ErrTrendingCursor):
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "cursor is not a value this endpoint issued", nil)
		case errors.Is(err, delivery.ErrDeliveryDenied):
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Sign in to see trending sounds", nil)
		case errors.Is(err, delivery.ErrDeliveryUnresolved):
			slog.Warn("trending sounds: list unresolved", "error", err)
			api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "Trending sounds could not be checked; retry", nil)
		default:
			slog.Error("trending sounds: list failed", "error", err)
			api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not list trending sounds", nil)
		}
		return
	}
	resp := trendingResponse{Data: page.Items, Meta: trendingMeta{UpdatedAt: page.UpdatedAt, RequestID: trace.RequestIDFrom(ctx)}}
	if resp.Data == nil {
		resp.Data = []service.TrendingItem{}
	}
	if page.NextCursor != "" {
		next := page.NextCursor
		resp.Meta.NextCursor = &next
	}
	c.JSON(http.StatusOK, resp)
}

// soundWithTrending is the sound page's body: the Sound shape, field for
// field, with "trending" after its last field.
type soundWithTrending struct {
	track    *postgres.AudioTrack
	trending *service.TrendingBadge
}

func (s soundWithTrending) MarshalJSON() ([]byte, error) {
	sound, err := json.Marshal(s.track)
	if err != nil {
		return nil, err
	}
	sound = bytes.TrimSpace(sound)
	if len(sound) < 2 || sound[0] != '{' || sound[len(sound)-1] != '}' {
		return nil, fmt.Errorf("sound is not a JSON object")
	}
	trending, err := json.Marshal(s.trending)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(sound)+len(trending)+16)
	out = append(out, sound[:len(sound)-1]...)
	if len(sound) > 2 {
		out = append(out, ',')
	}
	out = append(out, `"trending":`...)
	out = append(out, trending...)
	out = append(out, '}')
	return out, nil
}

// trendingBadge is the sound page's `trending`, or nil. A fault reading it
// costs the badge, never the page.
func (h *Handler) trendingBadge(ctx context.Context, audioID uuid.UUID) *service.TrendingBadge {
	if h.trending == nil {
		return nil
	}
	badge, err := h.trending.Badge(ctx, audioID)
	if err != nil {
		slog.Warn("trending sounds: badge unavailable", "audio_track_id", audioID, "error", err)
		return nil
	}
	return badge
}

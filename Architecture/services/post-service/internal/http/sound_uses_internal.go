package http

import (
	"context"
	"net/http"
	"strconv"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Trending sounds (2026-10-24): every sound's daily uses, for media-service's
// ranking worker (service/sound_uses.go has the counting rules).
//
//	GET /v1/internal/sound-uses?after=<sound uuid>&limit=<1..1000, default 500>
//	  200 {"as_of": RFC3339, "days": 14,
//	       "sounds": [{"audio_track_id": "<uuid>", "daily_uses": [14 ints, day 0 first]}],
//	       "next_after": "<uuid>" | null}
//	  400 {"error": "invalid_after"} | {"error": "invalid_limit"}
//	  503 {"error": "unresolved"}   retry; never read as "no uses"
//
// Under the internal-key middleware every route carries when
// INTERNAL_SERVICE_KEY is set; the gateway does not expose /internal/.
// Sound ids and counts only: no post, no author.

// soundUsesReader is the one read this route makes. *service.Service
// implements it; tests substitute a fake.
type soundUsesReader interface {
	SoundUses(ctx context.Context, after uuid.UUID, limit int) (*service.SoundUsesPage, error)
}

// SoundUsesInternal — GET /v1/internal/sound-uses
func (h *Handler) SoundUsesInternal(c *gin.Context) {
	var reader soundUsesReader
	if h.svc != nil {
		reader = h.svc
	}
	serveSoundUses(c, reader)
}

func serveSoundUses(c *gin.Context, reader soundUsesReader) {
	after := uuid.Nil
	if raw := c.Query("after"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_after"})
			return
		}
		after = id
	}
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_limit"})
			return
		}
		limit = n
	}
	if reader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "unresolved"})
		return
	}
	page, err := reader.SoundUses(c.Request.Context(), after, limit)
	if err != nil || page == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "unresolved"})
		return
	}
	c.JSON(http.StatusOK, page)
}

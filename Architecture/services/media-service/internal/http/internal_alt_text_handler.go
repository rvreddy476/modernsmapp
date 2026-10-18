package http

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PATCH /v1/media/internal/:mediaId/alt-text — service-to-service only
// (2026-10-18).
//
// post-service publishes a composer draft and writes the alt text the author
// typed onto each photo. The public PATCH /v1/media/:mediaId/alt-text needs
// the user's token, which the scheduled-publish worker never has; this is the
// same write behind the internal key, with the uploader named in the body.
// The store updates only a row whose uploader_id is that user, so naming
// somebody else's asset changes nothing and answers 404.
//
//	body {"uploader_id": "<uuid>", "alt_text": "...", "decorative": false}
//	200  {"status": "updated"}
//	400  BAD_REQUEST   bad id, bad body, nothing to write, alt_text too long
//	403  USER_CALLER_REFUSED  a gateway identity header is present
//	404  NOT_FOUND     no such asset, or not that uploader's
type internalAltTextService interface {
	UpdateAltTextWithDecorative(ctx context.Context, mediaID, userID uuid.UUID, altText string, decorative bool) error
}

// maxInternalAltTextRunes caps the description (the composer's own cap).
const maxInternalAltTextRunes = 1000

type internalAltTextRequest struct {
	UploaderID string `json:"uploader_id"`
	AltText    string `json:"alt_text"`
	Decorative bool   `json:"decorative"`
}

func (h *Handler) internalAltTextSvc() internalAltTextService {
	if h.internalAltText != nil {
		return h.internalAltText
	}
	return h.svc
}

func (h *Handler) UpdateAltTextInternal(c *gin.Context) {
	ctx := c.Request.Context()
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	var req internalAltTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "body must be {uploader_id, alt_text, decorative}", nil)
		return
	}
	uploaderID, err := uuid.Parse(strings.TrimSpace(req.UploaderID))
	if err != nil || uploaderID == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "uploader_id must be a user id", nil)
		return
	}
	alt := strings.TrimSpace(req.AltText)
	if alt == "" && !req.Decorative {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			"provide alt_text, or set decorative=true to mark the image as decorative", nil)
		return
	}
	if utf8.RuneCountInString(alt) > maxInternalAltTextRunes {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "alt_text is too long", nil)
		return
	}
	if err := h.internalAltTextSvc().UpdateAltTextWithDecorative(ctx, mediaID, uploaderID, alt, req.Decorative); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found or not owned by user", nil)
			return
		}
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

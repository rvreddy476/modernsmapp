package http

import (
	"context"
	"encoding/json"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
)

const OpDoorstepPhotoPrepare = "media:doorstep-photo.prepare"

func (h *Handler) requireDoorstepPhotoCaller() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
		if raw == "" && h.imageBytesLegacyKey {
			c.Next()
			return
		}
		if raw != "" && h.imageBytesVerifier != nil {
			v, err := h.imageBytesVerifier.Verify(raw, OpDoorstepPhotoPrepare, "")
			if err == nil && v.Issuer == IssuerDoorstepService {
				c.Next()
				return
			}
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "doorstep service token required", nil)
		c.Abort()
	}
}

// prepareDoorstepPhoto lets handler tests use the real caller/shape guards
// without a PostgreSQL connection.
func prepareDoorstepPhoto(c *gin.Context, prepare func(context.Context, uuid.UUID, uuid.UUID) (bool, error)) {
	c.Header("Cache-Control", "no-store, private")
	id, err := uuid.Parse(c.Param("mediaId"))
	var in struct {
		OwnerID uuid.UUID `json:"owner_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 4096))
	decoder.DisallowUnknownFields()
	if err != nil || id == uuid.Nil || decoder.Decode(&in) != nil || in.OwnerID == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, 400, "BAD_REQUEST", "media and owner ids required", nil)
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		api.ErrorWithContext(c.Request.Context(), c.Writer, 400, "BAD_REQUEST", "one JSON object required", nil)
		return
	}
	ok, err := prepare(c.Request.Context(), id, in.OwnerID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, 503, CodeMediaUnavailable, "media unavailable", nil)
		return
	}
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, 404, "MEDIA_NOT_FOUND", "media not available", nil)
		return
	}
	c.Status(204)
}
func (h *Handler) PrepareDoorstepPhoto(c *gin.Context) {
	prepareDoorstepPhoto(c, h.svc.PrepareDoorstepPhoto)
}

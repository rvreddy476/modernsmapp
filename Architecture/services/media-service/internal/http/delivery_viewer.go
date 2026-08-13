package http

import (
	"errors"
	"net/http"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 4 M4-P0-5 — viewer resolution and denial mapping for media reads.
//
// Every read endpoint now resolves a viewer and routes failures through
// writeDeliveryError. Before this the read routes were registered with the
// comment "public (media URLs need to be accessible for rendering)" and took no
// identity at all, so the bytes behind every restricted post and every story
// were fetchable by anyone with the id.

// deliveryViewer resolves the verified viewer from the edge-supplied header.
//
// Returns uuid.Nil when absent. That is NOT an error here: public media is
// legitimately readable without a session (an avatar on a public profile), and
// the Gate is what decides — an anonymous viewer simply fails the authorization
// step for anything protected. Rejecting anonymity at this layer would break
// public rendering; permitting it past the Gate would be the original bug.
func deliveryViewer(c *gin.Context) uuid.UUID {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		return uuid.Nil
	}
	return id
}

// writeDeliveryError maps a delivery outcome onto a response.
//
// The two shapes are kept distinct on purpose:
//
//   - A resolved denial is 404, identical to media that does not exist, so the
//     endpoint cannot be used to enumerate which assets are real or to probe
//     who has blocked whom.
//   - An unresolved authorization is 503. Answering 404 during a post-service
//     outage would tell the client the media is gone, and clients cache that.
func writeDeliveryError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, delivery.ErrDeliveryDenied):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound,
			"NOT_FOUND", "Media not found", nil)
	case errors.Is(err, delivery.ErrDeliveryUnresolved):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
			"DEPENDENCY_UNAVAILABLE", "Media access could not be determined; retry", nil)
	default:
		// Anything else (missing row, bad variant name) is also answered as
		// not-found: a distinct error here would separate "exists but you may
		// not have it" from "does not exist", which is the distinction the
		// denial path exists to hide.
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound,
			"NOT_FOUND", "Media not found", nil)
	}
}

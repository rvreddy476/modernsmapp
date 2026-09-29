package http

import (
	"context"
	"crypto/hmac"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Metadata reads (2026-09-29): GET /v1/media/:id, GET /v1/media/:id/status
// and GET /v1/audio/:audioId/url answer to the same audience as the bytes.
// The decision lives in service/record_read.go; this file is the wiring the
// handler tests pin.

// recordReadService is the slice of the service the three routes use.
type recordReadService interface {
	MediaForViewer(ctx context.Context, viewerID, mediaID uuid.UUID) (*postgres.MediaAsset, error)
	MediaForService(ctx context.Context, mediaID uuid.UUID) (*postgres.MediaAsset, error)
	StatusForViewer(ctx context.Context, viewerID, mediaID uuid.UUID) (*service.MediaStatusResponse, error)
	StatusForService(ctx context.Context, mediaID uuid.UUID) (*service.MediaStatusResponse, error)
	AudioTrackURLForViewer(ctx context.Context, viewerID, audioID uuid.UUID) (string, error)
}

func (h *Handler) recordsSvc() recordReadService {
	if h.records != nil {
		return h.records
	}
	return h.svc.RecordReads()
}

// internalKeyHeader is the credential an in-cluster service presents
// (shared/middleware RequireInternalKey reads the same header).
const internalKeyHeader = "X-Internal-Service-Key"

// trustedServiceCaller reports whether the request carries this service's
// internal key. Such a caller (post-service checking moderation before a
// publish, commerce-service checking ownership before a reference) is not a
// viewer and is answered without an audience decision.
//
// Safe on a public route only because the api-gateway strips a client-sent
// copy of the header and does NOT stamp its own onto /v1/media or /v1/audio
// (routepolicy.StampPolicy, guarded at gateway boot). A configured empty key
// never matches: there is no key to hold.
func (h *Handler) trustedServiceCaller(c *gin.Context) bool {
	if h.internalKey == "" {
		return false
	}
	presented := c.GetHeader(internalKeyHeader)
	if presented == "" {
		return false
	}
	return hmac.Equal([]byte(presented), []byte(h.internalKey))
}

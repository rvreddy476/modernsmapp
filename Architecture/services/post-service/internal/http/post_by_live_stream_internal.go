package http

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// Post by live stream (2026-10-02) — the video a live recording became.
//
//	GET /v1/internal/posts/by-live-stream/:streamId
//	  200 {"data": {"post_id": "<uuid>", "visibility": "unlisted", "deleted": false}}
//	  400 {"error": "invalid_stream_id"}
//	  404 {"error": "not_found"}     no post for the stream (yet)
//	  503 {"error": "unresolved"}    retry; never read as "no post"
//
// live-service-v2 calls it to fill live_streams.recording_post_id: the
// live.stream.vod_ready consumer creates the post here and nothing reports
// the id back. A soft-deleted post is answered with deleted = true so the
// caller stops asking and does not offer it.
//
// Registered in handler.go (RegisterRoutes) next to the by-media route, so it
// is behind the internal-key middleware every route carries when
// INTERNAL_SERVICE_KEY is set; the gateway refuses every path with an
// `internal` segment.

// liveStreamPostReader is the one read this route makes. *service.Service
// implements it; tests substitute a fake.
type liveStreamPostReader interface {
	LiveStreamPostRef(ctx context.Context, streamID uuid.UUID) (*postgres.LiveStreamPostRef, error)
}

// PostByLiveStreamInternal — GET /v1/internal/posts/by-live-stream/:streamId
func (h *Handler) PostByLiveStreamInternal(c *gin.Context) {
	var reader liveStreamPostReader
	if h.svc != nil {
		reader = h.svc
	}
	servePostByLiveStream(c, reader)
}

func servePostByLiveStream(c *gin.Context, reader liveStreamPostReader) {
	streamID, err := uuid.Parse(c.Param("streamId"))
	if err != nil || streamID == uuid.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_stream_id"})
		return
	}
	if reader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "unresolved"})
		return
	}
	ref, err := reader.LiveStreamPostRef(c.Request.Context(), streamID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "unresolved"})
		return
	}
	if ref == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"post_id":    ref.PostID.String(),
		"visibility": ref.Visibility,
		"deleted":    ref.Deleted,
	}})
}

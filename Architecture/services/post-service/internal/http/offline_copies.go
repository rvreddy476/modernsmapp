package http

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

/*
	Offline copies inside the app, never a file download (2026-10-02;
	service/offline_copies.go, migration 060).

	  POST   /v1/posts/:postId/offline   {device_id}
	           201 a copy was granted, 200 an active one was refreshed
	           -> {post_id, content_type, expires_at, recheck_after_seconds,
	               title, channel_name, duration_ms, poster_path,
	               media:{media_id, variant, path, mime, size_bytes},
	               captions:[{lang, label, path}],          (path serves WebVTT)
	               sound:{path, mime, start_ms, original_volume,
	                      overlay_volume} | null}
	             size_bytes is the exact length of what path serves, and is
	             left out when none is recorded (always, today, on sound).
	  POST   /v1/posts/offline/check     {device_id, post_ids:[...]}  (<= 100)
	           -> [{post_id, valid:true, expires_at, content_type}
	              |{post_id, valid:false, reason}]
	              reason: deleted | private | not_allowed | expired |
	                      blocked | revoked | unknown
	  GET    /v1/posts/offline?device_id=
	           -> [the grant's card, without media.path]
	  DELETE /v1/posts/:postId/offline   {device_id} or ?device_id=
	           -> {post_id, removed:true}   (idempotent)

	Status codes: 401 UNAUTHORIZED no identity; 400 INVALID_ID / INVALID_REQUEST
	a malformed id or body; 404 NOT_FOUND a post that does not exist or that
	the caller may not watch (private, blocked either way, scheduled for
	someone else, ...); 403 OFFLINE_NOT_ALLOWED the creator has not allowed it
	or the caller is not a member; 422 UNSUPPORTED_CONTENT not a video or a
	reel; 422 INVALID_DEVICE a missing or over-long device_id; 422
	INVALID_REQUEST more than 100 ids; 409 NOT_READY scheduled, processing or
	no rendition yet; 409 OFFLINE_LIMIT 100 active copies already; 503
	DEPENDENCY_UNAVAILABLE something the decision needs did not answer (retry;
	never a reason to delete a stored copy).

	/offline and /offline/check are static siblings of /:postId and are
	registered as such (handler.go), so "offline" is never read as a post id.
*/

// maxOfflineBodyBytes bounds the request bodies: 100 ids and a device id
// fit many times over.
const maxOfflineBodyBytes = 16 * 1024

type offlineGrantRequest struct {
	DeviceID string `json:"device_id"`
}

type offlineCheckRequest struct {
	DeviceID string   `json:"device_id"`
	PostIDs  []string `json:"post_ids"`
}

// offlineViewer resolves the caller; false = 401 already written.
func offlineViewer(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return uuid.Nil, false
	}
	return id, true
}

func writeOfflineError(c *gin.Context, err error) {
	status, code := service.OfflineErrorStatus(err)
	msg := err.Error()
	switch status {
	case http.StatusNotFound:
		msg = "Post not found"
	case http.StatusServiceUnavailable:
		msg = "Offline copies are unavailable right now; retry"
	case http.StatusInternalServerError:
		msg = "Internal server error"
	}
	switch {
	case errors.Is(err, service.ErrOfflineNotAllowed):
		msg = service.ErrOfflineNotAllowed.Error()
	case errors.Is(err, service.ErrOfflineNotReady):
		msg = service.ErrOfflineNotReady.Error()
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, msg, nil)
}

// GrantOfflineCopy is POST /v1/posts/:postId/offline.
func (h *Handler) GrantOfflineCopy(c *gin.Context) {
	viewerID, ok := offlineViewer(c)
	if !ok {
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxOfflineBodyBytes)
	var req offlineGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "A JSON body with device_id is required", nil)
		return
	}
	card, created, err := h.svc.GrantOfflineCopy(c.Request.Context(), viewerID, postID, req.DeviceID)
	if err != nil {
		writeOfflineError(c, err)
		return
	}
	// The card names URLs decided for this viewer; it is nobody else's.
	c.Header("Cache-Control", "private, no-store")
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	api.JSON(c.Writer, status, card, nil)
}

// CheckOfflineCopies is POST /v1/posts/offline/check.
func (h *Handler) CheckOfflineCopies(c *gin.Context) {
	viewerID, ok := offlineViewer(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxOfflineBodyBytes)
	var req offlineCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "A JSON body with device_id and post_ids is required", nil)
		return
	}
	items, err := h.svc.CheckOfflineCopies(c.Request.Context(), viewerID, req.DeviceID, req.PostIDs)
	if err != nil {
		writeOfflineError(c, err)
		return
	}
	if items == nil {
		items = []service.OfflineCheckItem{}
	}
	c.Header("Cache-Control", "private, no-store")
	api.JSON(c.Writer, http.StatusOK, items, nil)
}

// ListOfflineCopies is GET /v1/posts/offline?device_id=.
func (h *Handler) ListOfflineCopies(c *gin.Context) {
	viewerID, ok := offlineViewer(c)
	if !ok {
		return
	}
	cards, err := h.svc.ListOfflineCopies(c.Request.Context(), viewerID, c.Query("device_id"))
	if err != nil {
		writeOfflineError(c, err)
		return
	}
	if cards == nil {
		cards = []service.OfflineCard{}
	}
	c.Header("Cache-Control", "private, no-store")
	api.JSON(c.Writer, http.StatusOK, cards, nil)
}

// RemoveOfflineCopy is DELETE /v1/posts/:postId/offline. device_id comes
// from the JSON body or, for clients and proxies that drop a DELETE body,
// from the query string; the body wins when both are present.
func (h *Handler) RemoveOfflineCopy(c *gin.Context) {
	viewerID, ok := offlineViewer(c)
	if !ok {
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	deviceID := c.Query("device_id")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxOfflineBodyBytes)
	var req offlineGrantRequest
	if err := c.ShouldBindJSON(&req); err == nil {
		if strings.TrimSpace(req.DeviceID) != "" {
			deviceID = req.DeviceID
		}
	} else if !errors.Is(err, io.EOF) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "The body must be JSON with device_id, or pass ?device_id=", nil)
		return
	}
	res, err := h.svc.RemoveOfflineCopy(c.Request.Context(), viewerID, postID, deviceID)
	if err != nil {
		writeOfflineError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

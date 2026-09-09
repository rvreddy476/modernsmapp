package http

import (
	"net/http"

	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type profileMediaAccessRequest struct {
	ViewerID string `json:"viewer_id"`
	MediaID  string `json:"media_id" binding:"required"`
}

// ProfileMediaAccess is media-service's canonical authorization check for an
// avatar or cover. It resolves the current profile reference first, then the
// owner's live privacy policy. Every missing dependency fails closed.
//
// THE RESPONSE IS DELIBERATELY NOT THE SHARED ENVELOPE
//
//	{"viewer_id":"…"|"", "media_id":"…"}  →  {"allowed":true,"kind":"avatar"}
//
// media-service's content authorizer decodes `allowed` at the TOP level, and
// that is the contract post-service and commerce-service both answer. This
// handler used api.JSON, which wraps every body in `{"data":{…}}` — so
// media-service found no `allowed`, decoded the zero value, and read a
// successful 200 as a RESOLVED DENIAL. The effect was that every avatar in the
// product returned 404 on `/v1/media/:id/url` and `/v1/media/:id/serve` while
// both services logged a healthy exchange between them, which is why it
// survived so long: nothing anywhere reported an error.
//
// Errors below still use the shared envelope. Only the affirmative verdict is
// on media-service's wire contract, and media-service treats any non-2xx as a
// denial or an outage without reading the body.
func (h *Handler) ProfileMediaAccess(c *gin.Context) {
	var body profileMediaAccessRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "media_id is required", nil, nil)
		return
	}
	mediaID, err := uuid.Parse(body.MediaID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_MEDIA_ID", "Invalid media ID", nil, nil)
		return
	}
	viewerID := uuid.Nil
	if body.ViewerID != "" {
		viewerID, err = uuid.Parse(body.ViewerID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_VIEWER_ID", "Invalid viewer ID", nil, nil)
			return
		}
	}
	ownerID, kind, found, err := h.svc.FindProfileMediaOwner(c.Request.Context(), mediaID)
	if err != nil {
		api.Error(c.Writer, http.StatusServiceUnavailable, "PROFILE_MEDIA_UNRESOLVED", "Profile media authority unavailable", nil, nil)
		return
	}
	if !found {
		c.JSON(http.StatusOK, gin.H{"allowed": false})
		return
	}
	if h.photos == nil {
		api.Error(c.Writer, http.StatusServiceUnavailable, "PRIVACY_UNRESOLVED", "Profile privacy authority unavailable", nil, nil)
		return
	}
	allowed, err := h.photos.CanViewProfilePhoto(c.Request.Context(), viewerID, ownerID)
	if err != nil {
		h.log.Error("profile media privacy check failed", "err", err, "owner_id", ownerID, "media_id", mediaID)
		api.Error(c.Writer, http.StatusServiceUnavailable, "PRIVACY_UNRESOLVED", "Profile privacy authority unavailable", nil, nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"allowed": allowed,
		"kind":    kind,
	})
}

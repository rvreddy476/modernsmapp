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
		api.JSON(c.Writer, http.StatusOK, gin.H{"allowed": false}, nil)
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
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"allowed": allowed,
		"kind":    kind,
	}, nil)
}

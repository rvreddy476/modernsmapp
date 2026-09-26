package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PostVisibility — GET /v1/internal/posts/:id/visibility?viewer_id=
//
// Service-to-service (ws-gateway, before it lets a socket join the
// post:<id> room). Answers {visible: bool} with the same decision GET
// /v1/posts/:id makes; an unknown post or viewer is "not visible" rather
// than an error, so the caller fails closed on anything but a plain yes.
func (h *Handler) PostVisibility(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"visible": false, "reason": "invalid_post_id"})
		return
	}
	var viewer *uuid.UUID
	if raw := c.Query("viewer_id"); raw != "" {
		v, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"visible": false, "reason": "invalid_viewer_id"})
			return
		}
		viewer = &v
	}
	visible, err := h.svc.PostVisibleTo(c.Request.Context(), postID, viewer)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"visible": false, "reason": "unresolved"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"visible": visible})
}

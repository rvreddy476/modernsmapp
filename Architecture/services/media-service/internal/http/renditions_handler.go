package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterRenditionRoutes registers rendition-related endpoints.
func (h *Handler) RegisterRenditionRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	v1 := r.Group("/v1/media")
	{
		v1.GET("/:mediaId/renditions", h.GetRenditions)
		v1.POST("/:mediaId/frames", authMW, h.ExtractFrames)
	}
}

func (h *Handler) GetRenditions(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	resp, err := h.svc.GetRenditionStatus(c.Request.Context(), mediaID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

func (h *Handler) ExtractFrames(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	numFrames, _ := strconv.Atoi(c.DefaultQuery("count", "5"))

	resp, err := h.svc.ExtractFrames(c.Request.Context(), mediaID, userID, numFrames)
	if err != nil {
		if err.Error() == "forbidden: you do not own this media" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

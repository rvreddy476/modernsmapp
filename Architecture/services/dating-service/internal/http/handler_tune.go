package http

import (
	"net/http"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetTune returns the caller's Tune (or an empty one if not yet written).
func (h *Handler) GetTune(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	t, err := h.svc.GetTune(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, t, nil)
}

// PutTune upserts the caller's Tune.
func (h *Handler) PutTune(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body store.UpsertTuneParams
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	t, err := h.svc.UpsertTune(c.Request.Context(), userID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPSERT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, t, nil)
}

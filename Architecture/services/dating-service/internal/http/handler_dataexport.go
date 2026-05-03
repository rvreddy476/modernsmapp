// HTTP handlers for /v1/dating/data-export. Sprint 5 — DPDP §15.8.
package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// PostDataExport — POST /v1/dating/data-export.
//
// Rate-limited (1 per 7 days) at the service layer.
func (h *Handler) PostDataExport(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.RequestDataExport(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "EXPORT_REQUEST_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusAccepted, out, nil)
}

// GetMyDataExports — GET /v1/dating/data-export/me.
func (h *Handler) GetMyDataExports(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListMyExports(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// GET /v1/dating/data-export/:id/download (Dating plan lane D9).
package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// GetDataExportDownload returns a ready, unexpired export to its owner only.
// Anyone else, and any export that is pending, failed or expired, gets 404.
func (h *Handler) GetDataExportDownload(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	exportID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	doc, err := h.svc.DownloadDataExport(c.Request.Context(), userID, exportID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DOWNLOAD_FAILED")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", `attachment; filename="pulse-data-export-`+exportID.String()+`.json"`)
	c.Data(http.StatusOK, "application/json", doc)
}

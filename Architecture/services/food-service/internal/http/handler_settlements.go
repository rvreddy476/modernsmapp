package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AdminGenerateSettlementFile — POST /v1/food/admin/settlements/files
//
// Body: { kind: "restaurant"|"delivery", period_start, period_end }
// (period_* are YYYY-MM-DD; backend converts to date).
type AdminGenerateSettlementFileRequest struct {
	Kind        string `json:"kind"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
}

func (h *Handler) AdminGenerateSettlementFile(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	var req AdminGenerateSettlementFileRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	from, err := time.Parse("2006-01-02", req.PeriodStart)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "period_start must be YYYY-MM-DD", nil)
		return
	}
	to, err := time.Parse("2006-01-02", req.PeriodEnd)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "period_end must be YYYY-MM-DD", nil)
		return
	}
	f, err := h.svc.GenerateSettlementFile(c.Request.Context(), uid, req.Kind, from, to)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "SETTLEMENT_GEN_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, f, nil)
}

// AdminListSettlementFiles — GET /v1/food/admin/settlements/files?limit=N
func (h *Handler) AdminListSettlementFiles(c *gin.Context) {
	limit := 50
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 {
		limit = v
	}
	rows, err := h.svc.ListSettlementFiles(c.Request.Context(), limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "SETTLEMENT_LIST_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"files": rows}, nil)
}

// AdminDownloadSettlementFile — GET /v1/food/admin/settlements/files/:id/download
//
// MinIO-first: when the row has a file_url, redirects to a short-lived
// presigned GET so the browser fetches direct from MinIO without
// proxying through this service. Falls back to streaming the inline
// body when MinIO isn't wired or the row predates the offload.
func (h *Handler) AdminDownloadSettlementFile(c *gin.Context) {
	fileID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_FILE_ID", err.Error(), nil)
		return
	}
	dl, err := h.svc.GetSettlementDownload(c.Request.Context(), fileID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "FILE_NOT_FOUND", err.Error(), nil)
		return
	}
	if dl.PresignedURL != "" {
		c.Redirect(http.StatusFound, dl.PresignedURL)
		return
	}
	if len(dl.InlineBody) == 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "FILE_NOT_FOUND", "file has no body", nil)
		return
	}
	c.Writer.Header().Set("Content-Type", "text/csv")
	c.Writer.Header().Set("Content-Disposition",
		"attachment; filename=\"figo-"+dl.Kind+"-settlement-"+fileID.String()+".csv\"")
	_, _ = c.Writer.Write(dl.InlineBody)
}

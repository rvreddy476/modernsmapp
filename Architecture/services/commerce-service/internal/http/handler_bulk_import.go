// Phase F2.3 — bulk SKU upload HTTP handlers.
package http

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

func (h *Handler) RegisterBulkImportRoutes(v1 *gin.RouterGroup) {
	v1.POST("/seller/bulk-import/presigned-url", h.InitiateBulkUpload)
	v1.POST("/seller/bulk-import/:jobId/upload-complete", h.MarkBulkUploadComplete)
	v1.POST("/seller/bulk-import/:jobId/execute", h.ExecuteBulkImport)
	v1.GET("/seller/bulk-import", h.ListBulkImportJobs)
	v1.GET("/seller/bulk-import/:jobId", h.GetBulkImportJob)
	v1.GET("/seller/bulk-import/:jobId/errors.csv", h.GetBulkImportErrors)
}

type initiateBulkUploadReq struct {
	Filename string `json:"filename" binding:"required"`
}

// InitiateBulkUpload POST /v1/commerce/seller/bulk-import/presigned-url
// Returns {job_id, upload_url}. Seller PUTs the CSV at upload_url then
// calls upload-complete.
func (h *Handler) InitiateBulkUpload(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NO_SELLER", "seller account not found", nil)
		return
	}
	var req initiateBulkUploadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	job, uploadURL, err := h.svc.InitiateBulkUpload(c.Request.Context(), seller.ID, req.Filename)
	if err != nil {
		handleBulkImportErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, gin.H{
		"job_id":     job.ID,
		"upload_url": uploadURL,
		"job":        job,
	}, nil)
}

// MarkBulkUploadComplete POST /v1/commerce/seller/bulk-import/:jobId/upload-complete
// Seller signals the PUT is done; commerce-service enqueues validation.
func (h *Handler) MarkBulkUploadComplete(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	jobID, ok := parseUUID(c, "jobId")
	if !ok {
		return
	}
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NO_SELLER", "seller account not found", nil)
		return
	}
	if err := h.svc.MarkUploadComplete(c.Request.Context(), seller.ID, jobID); err != nil {
		handleBulkImportErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ExecuteBulkImport POST /v1/commerce/seller/bulk-import/:jobId/execute
// Seller approves the validated rows; worker upserts.
func (h *Handler) ExecuteBulkImport(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	jobID, ok := parseUUID(c, "jobId")
	if !ok {
		return
	}
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NO_SELLER", "seller account not found", nil)
		return
	}
	if err := h.svc.ExecuteImport(c.Request.Context(), seller.ID, jobID); err != nil {
		handleBulkImportErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ListBulkImportJobs(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NO_SELLER", "seller account not found", nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	out, err := h.svc.ListImportJobs(c.Request.Context(), seller.ID, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	if out == nil {
		out = []*postgres.ImportJob{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"jobs": out}, nil)
}

func (h *Handler) GetBulkImportJob(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	jobID, ok := parseUUID(c, "jobId")
	if !ok {
		return
	}
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NO_SELLER", "seller account not found", nil)
		return
	}
	job, err := h.svc.GetImportJob(c.Request.Context(), seller.ID, jobID)
	if err != nil {
		handleBulkImportErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, job, nil)
}

// GetBulkImportErrors GET /v1/commerce/seller/bulk-import/:jobId/errors.csv
// Returns a 302 to a presigned MinIO URL when the job produced errors;
// 204 if there are no errors.
func (h *Handler) GetBulkImportErrors(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	jobID, ok := parseUUID(c, "jobId")
	if !ok {
		return
	}
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NO_SELLER", "seller account not found", nil)
		return
	}
	url, err := h.svc.GetImportJobErrorsURL(c.Request.Context(), seller.ID, jobID, 5*time.Minute)
	if err != nil {
		handleBulkImportErr(c, err)
		return
	}
	if url == "" {
		c.Status(http.StatusNoContent)
		return
	}
	c.Redirect(http.StatusFound, url)
}

func handleBulkImportErr(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrImportJobNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, service.ErrImportJobNotOwner):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
	case errors.Is(err, service.ErrImportBadStatus):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "BAD_STATUS", err.Error(), nil)
	case errors.Is(err, service.ErrImportBlobMissing):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "BLOB_NOT_CONFIGURED", err.Error(), nil)
	default:
		handleErr(c, err)
	}
}

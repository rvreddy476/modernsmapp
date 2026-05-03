package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/rider-service/internal/http/middleware"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// createComplaintRequest is the body for POST /v1/rider/rides/:id/complain.
type createComplaintRequest struct {
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
}

// PostComplaint — POST /v1/rider/rides/:id/complain.
func (h *Handler) PostComplaint(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body createComplaintRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.CreateComplaint(c.Request.Context(), uid, rideID, service.CreateComplaintRequest{
		Category:    body.Category,
		Description: body.Description,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "COMPLAINT_CREATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
}

// GetMyComplaints — GET /v1/rider/complaints/me.
func (h *Handler) GetMyComplaints(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	out, err := h.svc.ListMyComplaints(c.Request.Context(), uid, limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "COMPLAINT_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// AdminListComplaints — GET /v1/rider/admin/complaints?status=.
func (h *Handler) AdminListComplaints(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "complaint.list")
	status := c.Query("status")
	limit, offset := readPaging(c, 100, 500)
	out, err := h.svc.ListComplaintsForAdmin(c.Request.Context(), status, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "COMPLAINT_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// updateComplaintStatusRequest is the admin patch body.
type updateComplaintStatusRequest struct {
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// AdminUpdateComplaint — POST /v1/rider/admin/complaints/:id/update-status.
func (h *Handler) AdminUpdateComplaint(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "complaint.update_status")
	c.Set(middleware.AuditTargetKindKey, "complaint")

	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	complaintID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body updateComplaintStatusRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.UpdateComplaintStatus(c.Request.Context(), complaintID, adminID, service.UpdateComplaintStatusRequest{
		Status: body.Status,
		Note:   body.Note,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "COMPLAINT_UPDATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

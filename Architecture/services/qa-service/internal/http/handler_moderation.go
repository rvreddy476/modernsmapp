package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// CreateReport files a report: 201 with the new report, or 200 with the
// reporter's open report on the same target when there already is one (a
// repeat is idempotent and files nothing). A target the reporter cannot see
// is 404 NOT_FOUND; reporting one's own content or oneself is 400.
func (h *Handler) CreateReport(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var body struct {
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Reason     string `json:"reason"`
		Details    string `json:"details"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	targetID, ok := parseUUIDString(c, body.TargetID, "target_id")
	if !ok {
		return
	}

	report, created, err := h.svc.CreateReport(c.Request.Context(), userID, body.TargetType, targetID, body.Reason, body.Details)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CREATE_FAILED")
		return
	}
	api.JSON(c.Writer, reportStatus(created), report, nil)
}

// reportStatus is 201 for a new report and 200 for the open one returned
// again.
func reportStatus(created bool) int {
	if created {
		return http.StatusCreated
	}
	return http.StatusOK
}

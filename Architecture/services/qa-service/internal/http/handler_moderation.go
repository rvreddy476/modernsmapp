package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

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

	report, err := h.svc.CreateReport(c.Request.Context(), userID, body.TargetType, targetID, body.Reason, body.Details)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "CREATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, report, nil)
}

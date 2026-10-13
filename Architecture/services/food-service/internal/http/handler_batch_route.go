package http

import (
	"errors"
	"github.com/atpost/food-service/internal/batchroute"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net/http"
)

func (h *Handler) PreviewBatchRoute(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", "Choose a valid order.", nil)
		return
	}
	result, err := h.svc.PreviewAcceptedBatchRoute(c.Request.Context(), uid, orderID)
	if err != nil {
		status, code, message := http.StatusServiceUnavailable, "BATCH_ROUTE_UNAVAILABLE", "Route suggestions are unavailable. Keep your current stop order."
		if errors.Is(err, pgx.ErrNoRows) {
			status, code, message = http.StatusNotFound, "BATCH_NOT_FOUND", "No eligible accepted batch was found."
		}
		if errors.Is(err, batchroute.ErrBusy) {
			status, code, message = http.StatusTooManyRequests, "BATCH_ROUTE_BUSY", "Please wait a moment before requesting another route suggestion."
			c.Header("Retry-After", "10")
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, message, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, result, nil)
}

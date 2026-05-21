package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListKitchenQueue — GET /v1/food/partner/restaurants/:restaurantId/kitchen-queue
//
// Returns CONFIRMED orders awaiting partner accept, sorted by
// accept_deadline_at ASC. Each row carries `seconds_to_breach` so the
// partner UI can render the count-down + breach warning without
// computing it client-side.
func (h *Handler) ListKitchenQueue(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	rid, err := uuid.Parse(c.Param("restaurantId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_RESTAURANT_ID", err.Error(), nil)
		return
	}
	rows, err := h.svc.ListKitchenQueue(c.Request.Context(), uid, rid)
	if err != nil {
		if err == pgx.ErrNoRows {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "RESTAURANT_NOT_FOUND", "restaurant not found or not owned by user", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "KITCHEN_QUEUE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"orders": rows}, nil)
}

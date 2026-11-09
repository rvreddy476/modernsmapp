package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/o11y/trace"
	"github.com/gin-gonic/gin"
)

// ListPartnerOrdersPage — GET /v1/food/partner/restaurants/:restaurantId/orders
// [?status=CONFIRMED,PREPARING][&limit=1..100][&cursor=<meta.next_cursor>]
//
// Food server lane (9 Nov 2026): the Kitchen app counts its active orders
// from a status-filtered list, paged by cursor, instead of from the latest 100
// orders of any status. With no query the answer is the original call's: the
// latest 100 orders, newest first, under data.items. meta.next_cursor names
// the next page and is absent on the last one.
//
// It replaces ListPartnerOrders on the route once handler.go is free to edit
// (the founder's admin-scope change is pending there); until then it is wired
// only in tests.
func (h *Handler) ListPartnerOrdersPage(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	filter, ok := partnerOrderFilterFromQuery(c)
	if !ok {
		return
	}
	orders, next, err := h.svc.ListPartnerOrdersPage(c.Request.Context(), userID, restaurantID, filter)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_PARTNER_ORDERS_FAILED", err.Error(), nil)
		return
	}
	meta := &api.Meta{RequestID: trace.RequestIDFrom(c.Request.Context())}
	if next != nil {
		meta.NextCursor = postgres.EncodeOrderCursor(*next)
	}
	// B8: totals carry *_paise siblings on the partner routes. Same data
	// shape as the original call, including items: null for no orders.
	api.JSON(c.Writer, http.StatusOK, map[string]any{"items": postgres.PartnerOrdersOf(orders)}, meta)
}

// partnerOrderFilterFromQuery reads status, limit and cursor, answering 400
// itself for any it cannot read. A bad value is refused, never ignored: a
// kitchen that asked for CONFIRMED orders must not be shown every order.
func partnerOrderFilterFromQuery(c *gin.Context) (postgres.PartnerOrderFilter, bool) {
	var f postgres.PartnerOrderFilter
	statuses, err := postgres.ParsePartnerOrderStatuses(c.QueryArray("status"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_STATUS", "status must name food order statuses, e.g. CONFIRMED,PREPARING", nil)
		return f, false
	}
	f.Statuses = statuses
	if raw := c.Query("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > postgres.PartnerOrdersDefaultLimit {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_LIMIT", "limit must be 1 to 100", nil)
			return f, false
		}
		f.Limit = limit
	}
	if raw := c.Query("cursor"); raw != "" {
		cursor, err := postgres.DecodeOrderCursor(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "cursor is not one this list returned", nil)
			return f, false
		}
		f.After = &cursor
	}
	return f, true
}

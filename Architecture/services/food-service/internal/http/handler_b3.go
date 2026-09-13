package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Wave 1 B3 routes and helpers.

func (h *Handler) registerB3Routes(admin *gin.RouterGroup) {
	admin.GET("/payout-accounts", h.AdminListPayoutAccounts)
}

// locationOwnedKeys are the restaurant fields PUT .../location owns. The
// generic PATCH refuses any of them (FOOD_USE_LOCATION_ROUTE).
var locationOwnedKeys = []string{
	"latitude", "longitude", "address_line1", "address_line2", "city", "state", "postal_code", "google_place_id",
}

// locationOwnedFields lists, in a fixed order, the location-owned keys present
// in a JSON body (null counts as present).
func locationOwnedFields(raw []byte) ([]string, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	fields := []string{}
	for _, k := range locationOwnedKeys {
		if _, ok := body[k]; ok {
			fields = append(fields, k)
		}
	}
	return fields, nil
}

// AdminListPayoutAccounts — GET /v1/food/admin/payout-accounts[?needs_review=true]
//
// Masked accounts with the shared-account review flag. Only the admin view
// carries the flag; an owner's own payout-account view never does.
func (h *Handler) AdminListPayoutAccounts(c *gin.Context) {
	ctx := c.Request.Context()
	page := paginationFromQuery(c)
	items, err := h.svc.AdminListPayoutAccounts(ctx, strings.EqualFold(c.Query("needs_review"), "true"), page)
	if err != nil {
		slog.ErrorContext(ctx, "food-service: admin payout accounts failed", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_PAYOUT_ACCOUNTS_FAILED", "payout accounts could not be listed", nil)
		return
	}
	api.JSONWithContext(ctx, c.Writer, http.StatusOK, paginatedResponse(items, page))
}

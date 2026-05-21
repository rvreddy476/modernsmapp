package http

import (
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetCapabilities — GET /v1/food/me/capabilities
//
// Returns a small object the mobile + web shells use to decide which
// role tabs to render. Calling this before showing the role selector
// avoids the customer ever calling /v1/food/admin/* (P0.5 follow-up).
//
// Response shape:
//
//	{
//	  "user_id": "<uuid>",
//	  "is_customer": true,
//	  "is_restaurant_owner": true,
//	  "is_delivery_partner": false,
//	  "is_admin": false,
//	  "is_moderator": false
//	}
//
// Restaurant + delivery flags are derived from the store
// (ListPartnerRestaurants / GetDeliveryProfile); admin + moderator are
// derived from the upstream X-Scopes header set by api-gateway.
func (h *Handler) GetCapabilities(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	scopes := c.GetHeader("X-Scopes")
	caps := gin.H{
		"user_id":             uid.String(),
		"is_customer":         true,
		"is_restaurant_owner": false,
		"is_delivery_partner": false,
		"is_admin":            hasAnyScope(scopes, "admin", "superadmin"),
		"is_moderator":        hasAnyScope(scopes, "moderator", "admin", "superadmin"),
	}
	if rests, err := h.svc.ListPartnerRestaurants(c.Request.Context(), uid); err == nil && len(rests) > 0 {
		caps["is_restaurant_owner"] = true
	}
	if profile, err := h.svc.GetDeliveryPartner(c.Request.Context(), uid); err == nil && profile != nil {
		caps["is_delivery_partner"] = true
	}
	api.JSON(c.Writer, http.StatusOK, caps, nil)
}

func hasAnyScope(scopes string, want ...string) bool {
	for _, scope := range strings.Fields(scopes) {
		for _, w := range want {
			if scope == w {
				return true
			}
		}
	}
	return false
}

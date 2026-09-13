package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Lane B8 routes: what the Feast Kitchen app needs on top of B1-B6.
//
// Every route acts only on the caller's own restaurant: a foreign restaurant,
// order, menu item, group, variant or add-on is 404 FOOD_NOT_FOUND. A step the
// owner has not saved yet is 404 FOOD_ONBOARDING_STEP_NOT_SAVED with
// details.step. Read-backs carry masked identifiers only.
// Golden responses: testdata/contracts/ (handler_b8_contract_test.go).

func (h *Handler) registerB8Routes(partner *gin.RouterGroup) {
	partner.GET("/restaurants/:restaurantId/readiness", h.GetRestaurantReadiness)
	partner.GET("/restaurants/:restaurantId/compliance", h.GetRestaurantCompliance)
	partner.GET("/restaurants/:restaurantId/location", h.GetRestaurantLocation)
	partner.GET("/restaurants/:restaurantId/operating-hours", h.GetRestaurantOperatingHours)
	partner.GET("/restaurants/:restaurantId/fssai", h.GetRestaurantFSSAI)

	partner.GET("/orders/:orderId", h.GetPartnerOrder)
	partner.GET("/menu/items/:itemId", h.GetPartnerMenuItem)

	partner.GET("/menu/items/:itemId/variants", h.ListMenuVariants)
	partner.POST("/menu/items/:itemId/variants", h.CreateMenuVariant)
	partner.PATCH("/menu/items/:itemId/variants/:variantId", h.UpdateMenuVariant)
	partner.PUT("/menu/items/:itemId/variants/:variantId", h.UpdateMenuVariant)
	partner.DELETE("/menu/items/:itemId/variants/:variantId", h.DeleteMenuVariant)

	partner.GET("/menu/items/:itemId/addon-groups", h.ListAddonGroups)
	partner.POST("/menu/items/:itemId/addon-groups", h.CreateAddonGroup)
	partner.PATCH("/menu/items/:itemId/addon-groups/:groupId", h.UpdateAddonGroup)
	partner.PUT("/menu/items/:itemId/addon-groups/:groupId", h.UpdateAddonGroup)
	partner.DELETE("/menu/items/:itemId/addon-groups/:groupId", h.DeleteAddonGroup)
	partner.POST("/menu/items/:itemId/addon-groups/:groupId/addons", h.CreateAddon)
	partner.PATCH("/menu/items/:itemId/addon-groups/:groupId/addons/:addonId", h.UpdateAddon)
	partner.PUT("/menu/items/:itemId/addon-groups/:groupId/addons/:addonId", h.UpdateAddon)
	partner.DELETE("/menu/items/:itemId/addon-groups/:groupId/addons/:addonId", h.DeleteAddon)
}

// writeB8Error adds the not-saved answer to the onboarding mapping
// (FieldError 422, pgx.ErrNoRows 404 FOOD_NOT_FOUND, generic 500).
func writeB8Error(c *gin.Context, err error) {
	var notSaved *postgres.StepNotSavedError
	if errors.As(err, &notSaved) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "FOOD_ONBOARDING_STEP_NOT_SAVED",
			"this onboarding step has not been saved yet", map[string]any{"step": notSaved.Step})
		return
	}
	writeOnboardingError(c, err)
}

func respondB8(c *gin.Context, status int, result any, err error) {
	if err != nil {
		writeB8Error(c, err)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, status, result)
}

func isFieldError(err error) bool {
	var fe *onboarding.FieldError
	return errors.As(err, &fe)
}

// uuidParams reads the caller and the named path ids, answering 400/401 itself.
func (h *Handler) uuidParams(c *gin.Context, names ...string) (uuid.UUID, []uuid.UUID, bool) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return uuid.Nil, nil, false
	}
	ids := make([]uuid.UUID, 0, len(names))
	for _, n := range names {
		id, ok := parseUUIDParam(c, n)
		if !ok {
			return uuid.Nil, nil, false
		}
		ids = append(ids, id)
	}
	return userID, ids, true
}

// ─── Restaurant profile PATCH (partial) ─────────────────────────────────────

// partnerRestaurantPatchKeys are the keys PATCH .../restaurants/:id writes.
var partnerRestaurantPatchKeys = map[string]bool{
	"legal_name": true, "display_name": true, "name": true, "slug": true, "description": true,
	"phone": true, "email": true, "min_order_amount": true, "packaging_fee": true,
}

// patchPresence reports which patchable keys a JSON object body carries and
// which of them are null. Keys match case-insensitively, as the struct decode
// does.
func patchPresence(raw []byte) (present, null map[string]bool, err error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, nil, err
	}
	present, null = map[string]bool{}, map[string]bool{}
	for k, v := range body {
		key := strings.ToLower(k)
		if !partnerRestaurantPatchKeys[key] {
			continue
		}
		present[key] = true
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			null[key] = true
		}
	}
	return present, null, nil
}

// ─── Onboarding read-backs ──────────────────────────────────────────────────

// GET /v1/food/partner/restaurants/:restaurantId/readiness
func (h *Handler) GetRestaurantReadiness(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	out, err := h.svc.GetRestaurantReadiness(c.Request.Context(), owner, restaurantID)
	respondB8(c, http.StatusOK, out, err)
}

// GET /v1/food/partner/restaurants/:restaurantId/compliance
func (h *Handler) GetRestaurantCompliance(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	out, err := h.svc.GetRestaurantCompliance(c.Request.Context(), owner, restaurantID)
	respondB8(c, http.StatusOK, out, err)
}

// GET /v1/food/partner/restaurants/:restaurantId/location
func (h *Handler) GetRestaurantLocation(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	out, err := h.svc.GetRestaurantLocation(c.Request.Context(), owner, restaurantID)
	respondB8(c, http.StatusOK, out, err)
}

// GET /v1/food/partner/restaurants/:restaurantId/operating-hours
func (h *Handler) GetRestaurantOperatingHours(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	out, err := h.svc.GetOperatingHours(c.Request.Context(), owner, restaurantID)
	respondB8(c, http.StatusOK, out, err)
}

// GET /v1/food/partner/restaurants/:restaurantId/fssai
func (h *Handler) GetRestaurantFSSAI(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	out, err := h.svc.GetRestaurantFSSAI(c.Request.Context(), owner, restaurantID)
	respondB8(c, http.StatusOK, out, err)
}

// ─── Partner single reads ───────────────────────────────────────────────────

// GET /v1/food/partner/orders/:orderId
func (h *Handler) GetPartnerOrder(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "orderId")
	if !ok {
		return
	}
	order, err := h.svc.GetPartnerOrder(c.Request.Context(), owner, ids[0])
	respondB8(c, http.StatusOK, postgres.PartnerOrderOf(order), err)
}

// GET /v1/food/partner/menu/items/:itemId (with variants and add-on groups)
func (h *Handler) GetPartnerMenuItem(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "itemId")
	if !ok {
		return
	}
	out, err := h.svc.GetPartnerMenuItem(c.Request.Context(), owner, ids[0])
	respondB8(c, http.StatusOK, out, err)
}

// ─── Variants and add-on groups ─────────────────────────────────────────────

type menuPriceBody struct {
	Name        *string  `json:"name"`
	Price       *float64 `json:"price"`
	PricePaise  *int64   `json:"price_paise"`
	IsAvailable *bool    `json:"is_available"`
	SortOrder   *int     `json:"sort_order"`
}

func (b menuPriceBody) input() postgres.MenuPriceInput {
	return postgres.MenuPriceInput{Name: b.Name, Price: b.Price, PricePaise: b.PricePaise, IsAvailable: b.IsAvailable, SortOrder: b.SortOrder}
}

type addonGroupBody struct {
	Name       *string `json:"name"`
	MinSelect  *int    `json:"min_select"`
	MaxSelect  *int    `json:"max_select"`
	IsRequired *bool   `json:"is_required"`
	SortOrder  *int    `json:"sort_order"`
}

func (b addonGroupBody) input() postgres.MenuAddonGroupInput {
	return postgres.MenuAddonGroupInput{Name: b.Name, MinSelect: b.MinSelect, MaxSelect: b.MaxSelect, IsRequired: b.IsRequired, SortOrder: b.SortOrder}
}

var deletedStatus = map[string]string{"status": "deleted"}

func (h *Handler) ListMenuVariants(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "itemId")
	if !ok {
		return
	}
	items, err := h.svc.ListMenuVariants(c.Request.Context(), owner, ids[0])
	respondB8(c, http.StatusOK, map[string]any{"items": items}, err)
}

func (h *Handler) CreateMenuVariant(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "itemId")
	if !ok {
		return
	}
	var body menuPriceBody
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.CreateMenuVariant(c.Request.Context(), owner, ids[0], body.input())
	respondB8(c, http.StatusCreated, out, err)
}

func (h *Handler) UpdateMenuVariant(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "itemId", "variantId")
	if !ok {
		return
	}
	var body menuPriceBody
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.UpdateMenuVariant(c.Request.Context(), owner, ids[0], ids[1], body.input())
	respondB8(c, http.StatusOK, out, err)
}

func (h *Handler) DeleteMenuVariant(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "itemId", "variantId")
	if !ok {
		return
	}
	err := h.svc.DeleteMenuVariant(c.Request.Context(), owner, ids[0], ids[1])
	respondB8(c, http.StatusOK, deletedStatus, err)
}

func (h *Handler) ListAddonGroups(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "itemId")
	if !ok {
		return
	}
	items, err := h.svc.ListAddonGroups(c.Request.Context(), owner, ids[0])
	respondB8(c, http.StatusOK, map[string]any{"items": items}, err)
}

func (h *Handler) CreateAddonGroup(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "itemId")
	if !ok {
		return
	}
	var body addonGroupBody
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.CreateAddonGroup(c.Request.Context(), owner, ids[0], body.input())
	respondB8(c, http.StatusCreated, out, err)
}

func (h *Handler) UpdateAddonGroup(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "itemId", "groupId")
	if !ok {
		return
	}
	var body addonGroupBody
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.UpdateAddonGroup(c.Request.Context(), owner, ids[0], ids[1], body.input())
	respondB8(c, http.StatusOK, out, err)
}

func (h *Handler) DeleteAddonGroup(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "itemId", "groupId")
	if !ok {
		return
	}
	err := h.svc.DeleteAddonGroup(c.Request.Context(), owner, ids[0], ids[1])
	respondB8(c, http.StatusOK, deletedStatus, err)
}

func (h *Handler) CreateAddon(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "itemId", "groupId")
	if !ok {
		return
	}
	var body menuPriceBody
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.CreateAddon(c.Request.Context(), owner, ids[0], ids[1], body.input())
	respondB8(c, http.StatusCreated, out, err)
}

func (h *Handler) UpdateAddon(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "itemId", "groupId", "addonId")
	if !ok {
		return
	}
	var body menuPriceBody
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.UpdateAddon(c.Request.Context(), owner, ids[0], ids[1], ids[2], body.input())
	respondB8(c, http.StatusOK, out, err)
}

func (h *Handler) DeleteAddon(c *gin.Context) {
	owner, ids, ok := h.uuidParams(c, "itemId", "groupId", "addonId")
	if !ok {
		return
	}
	err := h.svc.DeleteAddon(c.Request.Context(), owner, ids[0], ids[1], ids[2])
	respondB8(c, http.StatusOK, deletedStatus, err)
}

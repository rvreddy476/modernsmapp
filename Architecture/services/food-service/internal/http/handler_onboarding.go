package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Wave 1 B1 (restaurant onboarding) and B2 (payout accounts) routes.
//
// Auth: partner routes need X-User-Id and act only on a restaurant the caller
// owns (anything else is 404); delivery payout routes act on the caller's own
// delivery-partner profile; the document decision needs the admin scope.
// Response bodies carry masked identifiers only, and no error message echoes
// a submitted value. Golden responses: testdata/contracts/.

func (h *Handler) registerOnboardingRoutes(partner, delivery, admin *gin.RouterGroup) {
	partner.PUT("/restaurants/:restaurantId/compliance", h.PutRestaurantCompliance)
	partner.PUT("/restaurants/:restaurantId/location", h.PutRestaurantLocation)
	partner.PUT("/restaurants/:restaurantId/operating-hours", h.PutRestaurantOperatingHours)
	partner.PATCH("/restaurants/:restaurantId/accepting", h.PatchRestaurantAccepting)
	partner.PUT("/restaurants/:restaurantId/fssai", h.PutRestaurantFSSAI)
	partner.POST("/restaurants/:restaurantId/submit", h.SubmitRestaurant)
	partner.PUT("/restaurants/:restaurantId/payout-account", h.PutRestaurantPayoutAccount)
	partner.GET("/restaurants/:restaurantId/payout-account", h.GetRestaurantPayoutAccount)

	delivery.PUT("/payout-account", h.PutDeliveryPayoutAccount)
	delivery.GET("/payout-account", h.GetDeliveryPayoutAccount)

	admin.POST("/restaurants/:restaurantId/documents/:docId/decide", h.AdminDecideRestaurantDocument)
}

// writeOnboardingError maps onboarding, PII and ownership failures to stable
// codes. The fallback logs server-side and answers a generic 500.
func writeOnboardingError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	var fe *onboarding.FieldError
	var notReady *onboarding.NotReadyError
	switch {
	case errors.Is(err, service.ErrPIINotConfigured):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "PII_NOT_CONFIGURED",
			"identity and bank details cannot be accepted until encryption keys are configured", nil)
	case errors.As(err, &fe):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, fe.Code, fe.Message, map[string]any{"field": fe.Field})
	case errors.As(err, &notReady):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "FOOD_RESTAURANT_NOT_READY",
			"complete every onboarding step before submitting for review", map[string]any{"missing": notReady.Missing})
	case errors.Is(err, postgres.ErrRestaurantNotDraft):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "FOOD_RESTAURANT_NOT_DRAFT", err.Error(), nil)
	case errors.Is(err, postgres.ErrRestaurantNotLive):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "FOOD_RESTAURANT_NOT_LIVE", err.Error(), nil)
	case errors.Is(err, postgres.ErrFSSAIRequired):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "FOOD_FSSAI_REQUIRED", err.Error(), nil)
	case errors.Is(err, postgres.ErrDocumentExpired):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "FOOD_DOCUMENT_EXPIRED", err.Error(), nil)
	case errors.Is(err, service.ErrBankVerificationUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "FOOD_BANK_VERIFICATION_UNAVAILABLE", err.Error(), nil)
	case errors.Is(err, pgx.ErrNoRows):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "FOOD_NOT_FOUND", "not found", nil)
	default:
		slog.ErrorContext(ctx, "food-service: onboarding request failed", "path", c.FullPath(), "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "FOOD_ONBOARDING_FAILED", "the request could not be completed", nil)
	}
}

// isOnboardingError reports whether writeOnboardingError has a specific
// mapping for err (used by the pre-existing document and approval handlers).
func isOnboardingError(err error) bool {
	var fe *onboarding.FieldError
	return errors.As(err, &fe) || errors.Is(err, postgres.ErrFSSAIRequired) || errors.Is(err, pgx.ErrNoRows)
}

// bindOnboardingBody never echoes the decoder error: it can quote the body.
func bindOnboardingBody(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "request body is not valid JSON for this route", nil)
		return false
	}
	return true
}

func (h *Handler) ownerRestaurant(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	return userID, restaurantID, true
}

func respondOnboarding(c *gin.Context, result any, err error) {
	if err != nil {
		writeOnboardingError(c, err)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, result)
}

// PUT /v1/food/partner/restaurants/:restaurantId/compliance
func (h *Handler) PutRestaurantCompliance(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	var body struct {
		TaxCategory                 string `json:"tax_category"`
		LegalName                   string `json:"legal_name"`
		PAN                         string `json:"pan"`
		GSTIN                       string `json:"gstin"`
		SpecifiedPremisesDeclaredAt string `json:"specified_premises_declared_at"`
	}
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.SetRestaurantCompliance(c.Request.Context(), owner, restaurantID, onboarding.ComplianceInput{
		TaxCategory: body.TaxCategory, LegalName: body.LegalName, PAN: body.PAN, GSTIN: body.GSTIN,
		SpecifiedPremisesDeclaredAt: body.SpecifiedPremisesDeclaredAt,
	})
	respondOnboarding(c, out, err)
}

// PUT /v1/food/partner/restaurants/:restaurantId/location
func (h *Handler) PutRestaurantLocation(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	var body struct {
		Latitude         *float64 `json:"latitude"`
		Longitude        *float64 `json:"longitude"`
		AddressLine1     string   `json:"address_line1"`
		AddressLine2     string   `json:"address_line2"`
		City             string   `json:"city"`
		State            string   `json:"state"`
		PostalCode       string   `json:"postal_code"`
		GooglePlaceID    string   `json:"google_place_id"`
		DeliveryRadiusKM *float64 `json:"delivery_radius_km"`
	}
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.SetRestaurantLocation(c.Request.Context(), owner, restaurantID, onboarding.LocationInput{
		Latitude: body.Latitude, Longitude: body.Longitude, AddressLine1: body.AddressLine1, AddressLine2: body.AddressLine2,
		City: body.City, State: body.State, PostalCode: body.PostalCode, GooglePlaceID: body.GooglePlaceID,
		DeliveryRadiusKM: body.DeliveryRadiusKM,
	})
	respondOnboarding(c, out, err)
}

// PUT /v1/food/partner/restaurants/:restaurantId/operating-hours
func (h *Handler) PutRestaurantOperatingHours(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	var body struct {
		Windows []struct {
			DayOfWeek *int   `json:"day_of_week"`
			OpensAt   string `json:"opens_at"`
			ClosesAt  string `json:"closes_at"`
			IsClosed  bool   `json:"is_closed"`
		} `json:"windows"`
	}
	if !bindOnboardingBody(c, &body) {
		return
	}
	in := make([]postgres.OperatingHoursInput, 0, len(body.Windows))
	for _, w := range body.Windows {
		in = append(in, postgres.OperatingHoursInput{DayOfWeek: w.DayOfWeek, OpensAt: w.OpensAt, ClosesAt: w.ClosesAt, IsClosed: w.IsClosed})
	}
	out, err := h.svc.ReplaceOperatingHours(c.Request.Context(), owner, restaurantID, in)
	respondOnboarding(c, out, err)
}

// PATCH /v1/food/partner/restaurants/:restaurantId/accepting
func (h *Handler) PatchRestaurantAccepting(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	var body struct {
		IsAcceptingOrders *bool `json:"is_accepting_orders"`
	}
	if !bindOnboardingBody(c, &body) {
		return
	}
	if body.IsAcceptingOrders == nil {
		writeOnboardingError(c, &onboarding.FieldError{Code: onboarding.CodeAcceptingRequired, Field: "is_accepting_orders", Message: "is_accepting_orders is required"})
		return
	}
	out, err := h.svc.SetRestaurantAccepting(c.Request.Context(), owner, restaurantID, *body.IsAcceptingOrders)
	respondOnboarding(c, out, err)
}

// PUT /v1/food/partner/restaurants/:restaurantId/fssai
func (h *Handler) PutRestaurantFSSAI(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	var body struct {
		LicenceNumber string `json:"licence_number"`
		ExpiresAt     string `json:"expires_at"`
		MediaID       string `json:"media_id"`
	}
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.SubmitRestaurantFSSAI(c.Request.Context(), owner, restaurantID, onboarding.FSSAIInput{
		LicenceNumber: body.LicenceNumber, ExpiresAt: body.ExpiresAt, MediaID: body.MediaID,
	})
	respondOnboarding(c, out, err)
}

// POST /v1/food/partner/restaurants/:restaurantId/submit
func (h *Handler) SubmitRestaurant(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	out, err := h.svc.SubmitRestaurantForReview(c.Request.Context(), owner, restaurantID)
	respondOnboarding(c, out, err)
}

type payoutAccountBody struct {
	HolderName    string `json:"holder_name"`
	AccountNumber string `json:"account_number"`
	IFSC          string `json:"ifsc"`
}

func (b payoutAccountBody) input() onboarding.PayoutAccountInput {
	return onboarding.PayoutAccountInput{HolderName: b.HolderName, AccountNumber: b.AccountNumber, IFSC: b.IFSC}
}

// PUT /v1/food/partner/restaurants/:restaurantId/payout-account
func (h *Handler) PutRestaurantPayoutAccount(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	var body payoutAccountBody
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.PutRestaurantPayoutAccount(c.Request.Context(), owner, restaurantID, body.input())
	respondOnboarding(c, out, err)
}

// GET /v1/food/partner/restaurants/:restaurantId/payout-account
func (h *Handler) GetRestaurantPayoutAccount(c *gin.Context) {
	owner, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	out, err := h.svc.GetRestaurantPayoutAccount(c.Request.Context(), owner, restaurantID)
	respondOnboarding(c, out, err)
}

// PUT /v1/food/delivery/payout-account
func (h *Handler) PutDeliveryPayoutAccount(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	var body payoutAccountBody
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.PutDeliveryPayoutAccount(c.Request.Context(), userID, body.input())
	respondOnboarding(c, out, err)
}

// GET /v1/food/delivery/payout-account
func (h *Handler) GetDeliveryPayoutAccount(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetDeliveryPayoutAccount(c.Request.Context(), userID)
	respondOnboarding(c, out, err)
}

// POST /v1/food/admin/restaurants/:restaurantId/documents/:docId/decide
func (h *Handler) AdminDecideRestaurantDocument(c *gin.Context) {
	adminID, restaurantID, ok := h.ownerRestaurant(c)
	if !ok {
		return
	}
	documentID, ok := parseUUIDParam(c, "docId")
	if !ok {
		return
	}
	var body struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.AdminDecideRestaurantDocument(c.Request.Context(), adminID, restaurantID, documentID, body.Decision, body.Reason)
	respondOnboarding(c, out, err)
}

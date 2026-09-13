package http

import (
	"errors"
	"net/http"

	"github.com/atpost/food-service/internal/foodpii"
	"github.com/atpost/food-service/internal/riderkyc"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// Wave 1 B4: delivery-partner verification routes.
//
// Auth: /delivery routes act on the caller's own profile (X-User-Id); admin
// routes need the admin scope; the return route is PUBLIC (the browser lands
// there from DigiLocker) and only redirects; the dev authorize route exists
// only when ENV is local/dev AND the mock provider is selected. No response
// carries a state other than the start response's own, and none carries a
// verifier, a code, an Aadhaar number or a plaintext DL/RC.

// WithDigiLockerDevRoutes registers the mock provider's authorize route only
// outside production, and only with the mock selected.
func (h *Handler) WithDigiLockerDevRoutes(env string, mock bool) *Handler {
	h.devDigiLockerRoutes = mock && !foodpii.IsProduction(env)
	return h
}

func (h *Handler) registerRiderKYCRoutes(v1, delivery, admin *gin.RouterGroup) {
	delivery.POST("/kyc/digilocker/start", h.StartDigiLocker)
	delivery.POST("/kyc/digilocker/callback", h.CompleteDigiLocker)
	delivery.GET("/kyc/status", h.GetDeliveryKYCStatus)

	admin.GET("/delivery-partners/:partnerId/kyc", h.AdminGetDeliveryPartnerKYC)
	admin.POST("/delivery-partners/:partnerId/documents/:docId/decide", h.AdminDecideDeliveryPartnerDocument)

	v1.GET("/public/digilocker/return", h.DigiLockerReturn)
	if h.devDigiLockerRoutes {
		v1.GET("/dev/digilocker/authorize", h.DevDigiLockerAuthorize)
	}
}

// writeRiderKYCError maps the verification sentinels, then falls back to the
// onboarding mapping (field errors, PII 503, not found, expired document).
func writeRiderKYCError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	var notReady *riderkyc.NotReadyError
	type mapped struct {
		target  error
		status  int
		code    string
		message string
	}
	for _, m := range []mapped{
		{service.ErrDigiLockerNotConfigured, http.StatusServiceUnavailable, "FOOD_DIGILOCKER_NOT_CONFIGURED", "DigiLocker verification is not available yet"},
		{service.ErrDigiLockerProvider, http.StatusBadGateway, "FOOD_DIGILOCKER_PROVIDER_FAILED", "DigiLocker could not complete the verification; start again"},
		{postgres.ErrDigiLockerStateNotFound, http.StatusUnprocessableEntity, "FOOD_DIGILOCKER_STATE_INVALID", "this verification link is not recognised; start again"},
		{postgres.ErrDigiLockerStateUsed, http.StatusConflict, "FOOD_DIGILOCKER_STATE_USED", "this verification link has already been used; start again"},
		{postgres.ErrDigiLockerStateExpired, http.StatusGone, "FOOD_DIGILOCKER_STATE_EXPIRED", "this verification link has expired; start again"},
		{postgres.ErrDigiLockerStateNotYours, http.StatusForbidden, "FOOD_DIGILOCKER_STATE_NOT_YOURS", "this verification link was started by another account"},
		{postgres.ErrDocumentNumberInUse, http.StatusConflict, "FOOD_DOCUMENT_NUMBER_IN_USE", "this document is already registered to another delivery partner"},
	} {
		if errors.Is(err, m.target) {
			api.ErrorWithContext(ctx, c.Writer, m.status, m.code, m.message, nil)
			return
		}
	}
	if errors.As(err, &notReady) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "FOOD_DELIVERY_PARTNER_NOT_READY",
			"complete every verification step before approval", map[string]any{"missing": notReady.Missing})
		return
	}
	writeOnboardingError(c, err)
}

func isRiderKYCError(err error) bool {
	var notReady *riderkyc.NotReadyError
	return errors.As(err, &notReady) || errors.Is(err, pgx.ErrNoRows)
}

func respondRiderKYC(c *gin.Context, status int, result any, err error) {
	if err != nil {
		writeRiderKYCError(c, err)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, status, result)
}

// POST /v1/food/delivery/kyc/digilocker/start
func (h *Handler) StartDigiLocker(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.StartDigiLocker(c.Request.Context(), userID)
	respondRiderKYC(c, http.StatusOK, out, err)
}

// POST /v1/food/delivery/kyc/digilocker/callback
func (h *Handler) CompleteDigiLocker(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	var body struct {
		Code  string `json:"code"`
		State string `json:"state"`
	}
	if !bindOnboardingBody(c, &body) {
		return
	}
	out, err := h.svc.CompleteDigiLocker(c.Request.Context(), userID, body.Code, body.State)
	respondRiderKYC(c, http.StatusOK, out, err)
}

// GET /v1/food/delivery/kyc/status
func (h *Handler) GetDeliveryKYCStatus(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.DeliveryPartnerKYCForUser(c.Request.Context(), userID)
	respondRiderKYC(c, http.StatusOK, out, err)
}

// GET /v1/food/admin/delivery-partners/:partnerId/kyc
func (h *Handler) AdminGetDeliveryPartnerKYC(c *gin.Context) {
	partnerID, ok := parseUUIDParam(c, "partnerId")
	if !ok {
		return
	}
	out, err := h.svc.AdminDeliveryPartnerKYC(c.Request.Context(), partnerID)
	respondRiderKYC(c, http.StatusOK, out, err)
}

// POST /v1/food/admin/delivery-partners/:partnerId/documents/:docId/decide
func (h *Handler) AdminDecideDeliveryPartnerDocument(c *gin.Context) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	partnerID, ok := parseUUIDParam(c, "partnerId")
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
	out, err := h.svc.AdminDecideDeliveryPartnerDocument(c.Request.Context(), adminID, partnerID, documentID, body.Decision, body.Reason)
	respondRiderKYC(c, http.StatusOK, out, err)
}

// GET /v1/food/public/digilocker/return — public. 302 to the Rider App Link
// with code and state passed through.
func (h *Handler) DigiLockerReturn(c *gin.Context) {
	location, err := h.svc.DigiLockerAppLinkURL(c.Request.URL.Query())
	if err != nil {
		writeRiderKYCError(c, err)
		return
	}
	noStoreRedirect(c, location)
}

// GET /v1/food/dev/digilocker/authorize?state= — local/dev mock only.
func (h *Handler) DevDigiLockerAuthorize(c *gin.Context) {
	location, err := h.svc.DigiLockerDevReturnURL(c.Query("state"))
	if err != nil {
		writeRiderKYCError(c, err)
		return
	}
	noStoreRedirect(c, location)
}

func noStoreRedirect(c *gin.Context, location string) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Redirect(http.StatusFound, location)
}

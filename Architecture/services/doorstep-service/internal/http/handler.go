// Package http wires gin routes to doorstep-service.
package http

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/http/middleware"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MaxBodyBytes bounds every JSON body (a zone boundary is the largest).
const MaxBodyBytes = 1 << 20

// Handler is the doorstep-service HTTP layer.
type Handler struct {
	svc         *service.Service
	internalKey string
	verifier    *servicetoken.Verifier
}

// New constructs a Handler.
func New(svc *service.Service, internalKey string) *Handler {
	return &Handler{svc: svc, internalKey: internalKey}
}

// CheckInternalKey refuses a production process with no internal service
// key: every /v1/doorstep user route trusts X-User-Id from the gateway.
func CheckInternalKey(production bool, key string) error {
	if production && strings.TrimSpace(key) == "" {
		return errors.New("INTERNAL_SERVICE_KEY is required in production: /v1/doorstep trusts gateway identity headers")
	}
	return nil
}

// RegisterRoutes registers every /v1/doorstep route.
//
// Customer and professional routes sit behind X-Internal-Service-Key when a
// key is configured (main refuses production without one): only the gateway
// may set X-User-Id. The service never verifies a bearer token itself; the
// gateway is the only JWT verifier and applies the dormant-product gate
// (DOORSTEP_PUBLIC_ENABLED + DOORSTEP_PILOT_USER_IDS). The admin family is
// token-only and mounted outside the key group (admin_token.go).
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	d := r.Group("/v1/doorstep")
	if h.internalKey != "" {
		d.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	{
		// Catalogue (A1): internal key only, no user.
		d.GET("/catalogue", h.getCatalogue)
		d.GET("/categories/:slug", h.getCategory)
		d.GET("/services/:id", h.getService)
		d.POST("/serviceability", h.postServiceability)
		// Background-check vendor webhook (A2): signed, no identity; no
		// vendor is enabled, so every provider answers 404.
		d.POST("/webhooks/background-check/:provider", h.backgroundCheckWebhook)

		user := d.Group("")
		user.Use(middleware.GatewayIdentity())
		{
			// Quotes (A1).
			user.POST("/quotes", h.postQuote)
			user.GET("/quotes/:id", h.getQuote)

			// Professional onboarding (A2).
			h.registerProRoutes(user)

			// Addresses, slots, bookings, payments (A3).
			h.registerBookingRoutes(user)
		}
	}
	h.registerInternalAdminRoutes(r)
}

// ---- helpers ----------------------------------------------------------------

func writeJSON(c *gin.Context, status int, v any) {
	api.JSONWithContext(c.Request.Context(), c.Writer, status, v)
}

// writeErr writes an apperr.Error with its status and code, anything else as
// an opaque 500.
func writeErr(c *gin.Context, err error) {
	ctx := c.Request.Context()
	var ae *apperr.Error
	if errors.As(err, &ae) {
		var details any
		if ae.Details != nil {
			details = ae.Details
		}
		api.ErrorWithContext(ctx, c.Writer, ae.Status, ae.Code, ae.Message, details)
		return
	}
	slog.ErrorContext(ctx, "doorstep: unhandled error", "error", err, "path", c.Request.URL.Path)
	api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, apperr.CodeInternal, "internal error", nil)
}

// respond writes v with status, or the error.
func respond(c *gin.Context, status int, v any, err error) {
	if err != nil {
		writeErr(c, err)
		return
	}
	writeJSON(c, status, v)
}

// bindJSON strictly decodes the body into dst: unknown fields, trailing data
// and bodies over MaxBodyBytes are refused with 400.
func bindJSON(c *gin.Context, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(c, apperr.Invalid("", "request body is not valid JSON for this route: "+err.Error()))
		return false
	}
	if _, err := dec.Token(); err != io.EOF {
		writeErr(c, apperr.Invalid("", "request body must hold exactly one JSON object"))
		return false
	}
	return true
}

func uuidParam(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil || id == uuid.Nil {
		writeErr(c, apperr.Invalid(name, name+" must be a UUID"))
		return uuid.Nil, false
	}
	return id, true
}

func optionalUUIDQuery(c *gin.Context, name string) (*uuid.UUID, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		writeErr(c, apperr.Invalid(name, name+" must be a UUID"))
		return nil, false
	}
	return &id, true
}

func userID(c *gin.Context) (uuid.UUID, bool) {
	uid, ok := middleware.GetAuthenticatedUserID(c)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "authentication required", nil)
		return uuid.Nil, false
	}
	return uid, true
}

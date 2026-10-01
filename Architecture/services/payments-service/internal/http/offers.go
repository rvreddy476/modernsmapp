package http

// The bank-offer registry (Razorpay Offers, migration 014).
//
//	GET   /v1/payments/internal/offers?application=mstore&amount_minor=   payments:offers.read (service token)
//	GET   /v1/payments/internal/admin/offers?application_id=              payments:offers.manage (admin-service)
//	POST  /v1/payments/internal/admin/offers                              payments:offers.manage
//	PATCH /v1/payments/internal/admin/offers/:offerId                     payments:offers.manage
//
// The read is what commerce shows a buyer: active, applicable offers, public
// fields only, `{"data":{"items":[…]}}`, and an empty list while
// PAYMENTS_OFFERS_ENABLED is off. The admin family manages the registry; step-up
// for create and edit is enforced by admin-service before it signs the token
// (as for every admin write here), and every write appends a snapshot to the
// append-only payments.payment_offer_changes. There is no delete: deactivating
// is PATCH {"active": false}.
//
// PATCH reads each key in three states: ABSENT is "no change", a value is "set",
// and an explicit JSON null is "clear" — for the optional fields
// (max_discount_minor, ends_at, description, starts_at, min_amount_minor).
// application, provider and provider_offer_id cannot change and are refused.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// OpOffersRead is the service-token operation for the buyer-facing offer read.
const OpOffersRead = "payments:offers.read"

// OffersService is what the offer routes need from *service.Service.
type OffersService interface {
	ApplicableOffers(ctx context.Context, applicationID string, amountMinor int64) ([]service.PublicOffer, error)
	ListOffers(ctx context.Context, applicationID string) ([]postgres.PaymentOffer, error)
	CreateOffer(ctx context.Context, o postgres.PaymentOffer, w postgres.OfferWrite) (*postgres.PaymentOffer, error)
	UpdateOffer(ctx context.Context, id uuid.UUID, applicationID string, p postgres.OfferPatch, w postgres.OfferWrite) (*postgres.PaymentOffer, bool, error)
}

// WithOffers enables the offer routes. Nil leaves them unregistered.
func (h *Handler) WithOffers(svc OffersService) *Handler {
	h.offers = svc
	return h
}

// ListApplicableOffers GET /v1/payments/internal/offers?application=&amount_minor=
func (h *Handler) ListApplicableOffers(c *gin.Context) {
	ctx := c.Request.Context()
	app := strings.TrimSpace(c.Query("application"))
	if !postgres.ValidApplicationKey(app) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_APPLICATION_ID", "application is required and must be a registered key", nil)
		return
	}
	if !h.callerMayRead(c, app) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeApplicationNotAllowed, "this caller may not read this application", nil)
		return
	}
	var amount int64
	if v := strings.TrimSpace(c.Query("amount_minor")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_AMOUNT", "amount_minor must be a positive integer (paise)", nil)
			return
		}
		amount = n
	}
	items, err := h.offers.ApplicableOffers(ctx, app, amount)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "FETCH_FAILED", "could not read the offer registry", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}

// ─── Admin family ────────────────────────────────────────────────────

// PermOffersManage is the admin permission for the whole offer registry.
const PermOffersManage = "payments:offers.manage"

func (h *Handler) registerOfferAdminRoutes(g *gin.RouterGroup) {
	g.GET("/offers", h.requireAdminToken(PermOffersManage), h.AdminListOffers)
	g.POST("/offers", h.requireAdminToken(PermOffersManage), h.AdminCreateOffer)
	g.PATCH("/offers/:offerId", h.requireAdminToken(PermOffersManage), h.AdminUpdateOffer)
}

// AdminListOffers GET /offers?application_id=
func (h *Handler) AdminListOffers(c *gin.Context) {
	app, ok := adminApplication(c)
	if !ok {
		return
	}
	items, err := h.offers.ListOffers(c.Request.Context(), app)
	if err != nil {
		adminError(c, http.StatusInternalServerError, "FETCH_FAILED", "could not read the offer registry")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}

// opt is a three-state JSON field: absent (Set=false), null (Null=true), or a
// value.
type opt[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (o *opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Null = true
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}

type offerBody struct {
	Application      opt[string]    `json:"application"`
	Provider         opt[string]    `json:"provider"`
	ProviderOfferID  opt[string]    `json:"provider_offer_id"`
	Title            opt[string]    `json:"title"`
	Description      opt[string]    `json:"description"`
	PaymentMethod    opt[string]    `json:"payment_method"`
	DiscountType     opt[string]    `json:"discount_type"`
	DiscountValue    opt[int64]     `json:"discount_value"`
	MaxDiscountMinor opt[int64]     `json:"max_discount_minor"`
	MinAmountMinor   opt[int64]     `json:"min_amount_minor"`
	FundedBy         opt[string]    `json:"funded_by"`
	StartsAt         opt[time.Time] `json:"starts_at"`
	EndsAt           opt[time.Time] `json:"ends_at"`
	Active           opt[bool]      `json:"active"`
}

func decodeOfferBody(c *gin.Context) (offerBody, bool) {
	var b offerBody
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		adminError(c, http.StatusBadRequest, "INVALID_BODY", "body must be a JSON offer: "+err.Error())
		return b, false
	}
	return b, true
}

func offerWriteFor(c *gin.Context) (postgres.OfferWrite, bool) {
	actor, ok := adminActor(c)
	if !ok {
		adminError(c, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin")
		return postgres.OfferWrite{}, false
	}
	return postgres.OfferWrite{OperatorID: actor.String(), Credential: adminCredential}, true
}

// AdminCreateOffer POST /offers?application_id=
//
//	{"application":"mstore","provider":"razorpay","provider_offer_id":"offer_…",
//	 "title":"10% off with HDFC credit cards","description":"…","payment_method":"card",
//	 "discount_type":"percentage","discount_value":1000,"max_discount_minor":150000,
//	 "min_amount_minor":500000,"funded_by":"bank","starts_at":"…","ends_at":"…","active":true}
func (h *Handler) AdminCreateOffer(c *gin.Context) {
	w, ok := offerWriteFor(c)
	if !ok {
		return
	}
	scope, ok := adminApplication(c)
	if !ok {
		return
	}
	b, ok := decodeOfferBody(c)
	if !ok {
		return
	}
	required := func(set, null bool, field string) bool {
		if !set || null {
			adminError(c, http.StatusBadRequest, "INVALID_OFFER", field+" is required")
			return false
		}
		return true
	}
	if !required(b.Application.Set, b.Application.Null, "application") ||
		!required(b.ProviderOfferID.Set, b.ProviderOfferID.Null, "provider_offer_id") ||
		!required(b.Title.Set, b.Title.Null, "title") ||
		!required(b.PaymentMethod.Set, b.PaymentMethod.Null, "payment_method") ||
		!required(b.DiscountType.Set, b.DiscountType.Null, "discount_type") ||
		!required(b.DiscountValue.Set, b.DiscountValue.Null, "discount_value") ||
		!required(b.FundedBy.Set, b.FundedBy.Null, "funded_by") {
		return
	}
	if scope != "" && scope != b.Application.Value {
		adminError(c, http.StatusForbidden, CodeApplicationNotAllowed, "this admin may not register offers for that application")
		return
	}
	o := postgres.PaymentOffer{
		Application:     b.Application.Value,
		Provider:        "razorpay",
		ProviderOfferID: strings.TrimSpace(b.ProviderOfferID.Value),
		Title:           b.Title.Value,
		Description:     b.Description.Value,
		PaymentMethod:   b.PaymentMethod.Value,
		DiscountType:    b.DiscountType.Value,
		DiscountValue:   b.DiscountValue.Value,
		MinAmountMinor:  b.MinAmountMinor.Value,
		FundedBy:        b.FundedBy.Value,
		StartsAt:        time.Now().UTC(),
		Active:          true,
	}
	if b.Provider.Set && !b.Provider.Null {
		o.Provider = b.Provider.Value
	}
	if b.MaxDiscountMinor.Set && !b.MaxDiscountMinor.Null {
		v := b.MaxDiscountMinor.Value
		o.MaxDiscountMinor = &v
	}
	if b.StartsAt.Set && !b.StartsAt.Null {
		o.StartsAt = b.StartsAt.Value
	}
	if b.EndsAt.Set && !b.EndsAt.Null {
		v := b.EndsAt.Value
		o.EndsAt = &v
	}
	if b.Active.Set && !b.Active.Null {
		o.Active = b.Active.Value
	}
	out, err := h.offers.CreateOffer(c.Request.Context(), o, w)
	if offerWriteError(c, err) {
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// AdminUpdateOffer PATCH /offers/:offerId?application_id=
func (h *Handler) AdminUpdateOffer(c *gin.Context) {
	w, ok := offerWriteFor(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("offerId"))
	if err != nil {
		adminError(c, http.StatusBadRequest, "INVALID_ID", "offer id must be a UUID")
		return
	}
	scope, ok := adminApplication(c)
	if !ok {
		return
	}
	b, ok := decodeOfferBody(c)
	if !ok {
		return
	}
	if b.Application.Set || b.Provider.Set || b.ProviderOfferID.Set {
		adminError(c, http.StatusBadRequest, "OFFER_IMMUTABLE",
			"application, provider and provider_offer_id cannot change; register a new offer instead")
		return
	}
	var p postgres.OfferPatch
	notNull := func(o interface{ isNull() bool }, field string) bool {
		if o.isNull() {
			adminError(c, http.StatusBadRequest, "INVALID_OFFER", field+" cannot be cleared")
			return false
		}
		return true
	}
	if !notNull(b.Title, "title") || !notNull(b.PaymentMethod, "payment_method") ||
		!notNull(b.DiscountType, "discount_type") || !notNull(b.DiscountValue, "discount_value") ||
		!notNull(b.FundedBy, "funded_by") || !notNull(b.Active, "active") {
		return
	}
	setStr := func(o opt[string]) *string {
		if !o.Set {
			return nil
		}
		v := o.Value
		return &v
	}
	setI := func(o opt[int64]) *int64 {
		if !o.Set || o.Null {
			return nil
		}
		v := o.Value
		return &v
	}
	p.Title = setStr(b.Title)
	p.PaymentMethod = setStr(b.PaymentMethod)
	p.DiscountType = setStr(b.DiscountType)
	p.FundedBy = setStr(b.FundedBy)
	if b.Description.Set {
		v := b.Description.Value // null clears to ""
		p.Description = &v
	}
	p.DiscountValue = setI(b.DiscountValue)
	p.MaxDiscountMinor = setI(b.MaxDiscountMinor)
	p.ClearMaxDiscount = b.MaxDiscountMinor.Null
	if b.MinAmountMinor.Set {
		v := b.MinAmountMinor.Value // null clears to 0: any amount
		p.MinAmountMinor = &v
	}
	if b.StartsAt.Set && !b.StartsAt.Null {
		v := b.StartsAt.Value
		p.StartsAt = &v
	}
	p.ClearStartsAt = b.StartsAt.Null
	if b.EndsAt.Set && !b.EndsAt.Null {
		v := b.EndsAt.Value
		p.EndsAt = &v
	}
	p.ClearEndsAt = b.EndsAt.Null
	if b.Active.Set {
		v := b.Active.Value
		p.Active = &v
	}
	out, changed, err := h.offers.UpdateOffer(c.Request.Context(), id, scope, p, w)
	if offerWriteError(c, err) {
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"offer": out, "changed": changed}, nil)
}

func (o opt[T]) isNull() bool { return o.Null }

func offerWriteError(c *gin.Context, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, postgres.ErrInvalidOffer):
		adminError(c, http.StatusBadRequest, "INVALID_OFFER", strings.TrimPrefix(err.Error(), postgres.ErrInvalidOffer.Error()+": "))
	case errors.Is(err, postgres.ErrOfferExists):
		adminError(c, http.StatusConflict, "OFFER_EXISTS", "this Razorpay offer is already registered")
	case errors.Is(err, postgres.ErrApplicationNotFound):
		adminError(c, http.StatusUnprocessableEntity, CodeApplicationUnknown, "application is not registered")
	case errors.Is(err, postgres.ErrOfferNotFound):
		adminError(c, http.StatusNotFound, "NOT_FOUND", "offer not found")
	default:
		adminError(c, http.StatusInternalServerError, "WRITE_FAILED", "could not write the offer")
	}
	return true
}

package http

// Sell scope (migration 045) — the books-only seller path.
//
//	PUT  /v1/commerce/onboarding/step/sell-scope   SELLER  "What will you sell?"
//	     {sell_scope:"all_categories", gstin, business_name?}
//	     {sell_scope:"books_only", pan, business_name}
//	     200 {sell_scope, gstin?, business_name?, pan_masked?, pan_holder_type?}
//	GET  /v1/commerce/seller/sell-scope            SELLER  {sell_scope, has_gstin, book_category_ids, can_upgrade}
//	POST /v1/commerce/seller/sell-scope/upgrade    SELLER  {gstin} → the view above, now all_categories
//
// Refusals (writeSellScopeError), every one naming the field in details:
//
//	400 SELL_SCOPE_INVALID          sell_scope is neither value
//	400 SELL_SCOPE_FIELD_NOT_TAKEN  a GSTIN on the books path, a PAN on the other
//	400 GSTIN_REQUIRED / GSTIN_INVALID
//	400 PAN_REQUIRED / PAN_INVALID
//	400 BUSINESS_NAME_REQUIRED
//	400 GSTIN_PAN_MISMATCH          upgrade: the GSTIN is some other PAN's
//	409 SELL_SCOPE_LOCKED           the application has left draft
//	409 SELL_SCOPE_NOT_BOOKS_ONLY   upgrade of a shop that is not books-only
//	403 SELLER_SCOPE_BOOKS_ONLY     a books-only shop listing outside books, or
//	                                on a class that charges GST (create, patch,
//	                                submit)
//	400 DOCUMENT_BACK_NOT_ACCEPTED / DOCUMENT_NUMBER_NOT_ACCEPTED  documents step

import (
	"errors"
	"net/http"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

type saveSellScopeReq struct {
	SellScope    string  `json:"sell_scope" binding:"required"`
	GSTIN        *string `json:"gstin"`
	PAN          *string `json:"pan"`
	BusinessName *string `json:"business_name"`
}

// SaveSellScope PUT /v1/commerce/onboarding/step/sell-scope
func (h *Handler) SaveSellScope(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req saveSellScopeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.SaveSellScope(c.Request.Context(), userID, service.SellScopeInput{
		SellScope: req.SellScope, GSTIN: req.GSTIN, PAN: req.PAN, BusinessName: req.BusinessName,
	})
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// GetSellScope GET /v1/commerce/seller/sell-scope
func (h *Handler) GetSellScope(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.SellScope(c.Request.Context(), userID)
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

type upgradeSellScopeReq struct {
	GSTIN string `json:"gstin"`
}

// UpgradeSellScope POST /v1/commerce/seller/sell-scope/upgrade
func (h *Handler) UpgradeSellScope(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req upgradeSellScopeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.UpgradeSellScope(c.Request.Context(), userID, req.GSTIN)
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// sellScopeCodes maps each field refusal to its status and code. The
// message is the sentinel's own text, which never carries the refused value.
var sellScopeCodes = []struct {
	err    error
	status int
	code   string
}{
	{service.ErrSellScopeInvalid, http.StatusBadRequest, "SELL_SCOPE_INVALID"},
	{service.ErrSellScopeFieldNotTaken, http.StatusBadRequest, "SELL_SCOPE_FIELD_NOT_TAKEN"},
	{service.ErrGSTINRequired, http.StatusBadRequest, "GSTIN_REQUIRED"},
	{service.ErrGSTINInvalid, http.StatusBadRequest, "GSTIN_INVALID"},
	{service.ErrPANRequired, http.StatusBadRequest, "PAN_REQUIRED"},
	{service.ErrPANInvalid, http.StatusBadRequest, "PAN_INVALID"},
	{service.ErrBusinessNameRequired, http.StatusBadRequest, "BUSINESS_NAME_REQUIRED"},
	{service.ErrGSTINPANMismatch, http.StatusBadRequest, "GSTIN_PAN_MISMATCH"},
}

// writeSellScopeError answers the sell-scope refusals, reporting whether it
// did. Called from writeCommerceError, so every route that can raise one —
// the onboarding steps and the product writes alike — answers it the same way.
func writeSellScopeError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	w := c.Writer

	var books *service.SellerScopeBooksOnlyError
	if errors.As(err, &books) {
		api.ErrorWithContext(ctx, w, http.StatusForbidden, "SELLER_SCOPE_BOOKS_ONLY", books.Reason, gin.H{
			"field":      books.Field,
			"sell_scope": postgres.SellScopeBooksOnly,
			"how_to_fix": "list in a book category on GST 0%, or add a GSTIN with " +
				"POST /v1/commerce/seller/sell-scope/upgrade to sell in every category",
		})
		return true
	}

	var field *service.SellScopeFieldError
	if errors.As(err, &field) {
		for _, m := range sellScopeCodes {
			if errors.Is(field.Err, m.err) {
				api.ErrorWithContext(ctx, w, m.status, m.code, m.err.Error(), gin.H{"field": field.Field})
				return true
			}
		}
	}

	switch {
	case errors.Is(err, postgres.ErrSellScopeLocked):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "SELL_SCOPE_LOCKED", err.Error(), nil)
	case errors.Is(err, postgres.ErrSellScopeNotBooksOnly):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "SELL_SCOPE_NOT_BOOKS_ONLY", err.Error(), nil)
	case errors.Is(err, service.ErrDocumentBackNotAccepted):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "DOCUMENT_BACK_NOT_ACCEPTED", err.Error(),
			gin.H{"field": "back_media_id"})
	case errors.Is(err, service.ErrDocumentNumberNotAccepted):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "DOCUMENT_NUMBER_NOT_ACCEPTED",
			service.ErrDocumentNumberNotAccepted.Error(), gin.H{"field": "document_number"})
	default:
		return false
	}
	return true
}

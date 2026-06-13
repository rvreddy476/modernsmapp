// Phase F2.2 — RFQ HTTP handlers. Mounted under /v1/commerce/rfqs.
package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) RegisterRFQRoutes(v1 *gin.RouterGroup) {
	v1.POST("/rfqs", h.CreateRFQ)
	v1.GET("/rfqs", h.ListBuyerRFQs)
	v1.GET("/rfqs/:rfqId", h.GetRFQ)
	v1.POST("/rfqs/:rfqId/quote", h.SendRFQQuote)
	v1.POST("/rfqs/:rfqId/reject", h.RejectRFQ)
	v1.POST("/rfqs/:rfqId/quotes/:quoteId/accept", h.AcceptRFQQuote)
	v1.GET("/seller/rfqs", h.ListSellerRFQs)
}

type createRFQReq struct {
	OrganizationID *uuid.UUID `json:"organization_id"`
	SellerID       uuid.UUID  `json:"seller_id" binding:"required"`
	Message        *string    `json:"message"`
	Items          []struct {
		VariantID uuid.UUID `json:"variant_id" binding:"required"`
		Quantity  int       `json:"quantity" binding:"required"`
		Notes     *string   `json:"notes"`
	} `json:"items" binding:"required,min=1"`
}

func (h *Handler) CreateRFQ(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req createRFQReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	items := make([]service.RFQItemInput, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, service.RFQItemInput{
			VariantID: it.VariantID, Quantity: it.Quantity, Notes: it.Notes,
		})
	}
	r, rfqItems, err := h.svc.CreateRFQ(c.Request.Context(), service.CreateRFQInput{
		BuyerUserID:    userID,
		OrganizationID: req.OrganizationID,
		SellerID:       req.SellerID,
		Message:        req.Message,
		Items:          items,
	})
	if err != nil {
		handleRFQErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, gin.H{"rfq": r, "items": rfqItems}, nil)
}

func (h *Handler) ListBuyerRFQs(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	out, err := h.svc.ListBuyerRFQs(c.Request.Context(), userID, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	if out == nil {
		out = []*postgres.RFQ{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rfqs": out}, nil)
}

func (h *Handler) ListSellerRFQs(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NO_SELLER", "seller account not found", nil)
		return
	}
	status := c.Query("status")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	out, err := h.svc.ListSellerRFQs(c.Request.Context(), seller.ID, status, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	if out == nil {
		out = []*postgres.RFQ{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rfqs": out}, nil)
}

func (h *Handler) GetRFQ(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	rfqID, ok := parseUUID(c, "rfqId")
	if !ok {
		return
	}
	detail, err := h.svc.GetRFQ(c.Request.Context(), rfqID, userID)
	if err != nil {
		handleRFQErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, detail, nil)
}

type sendRFQQuoteReq struct {
	ValidityDays int `json:"validity_days" binding:"required"`
	LinePrices   []struct {
		RFQItemID uuid.UUID `json:"rfq_item_id" binding:"required"`
		UnitPrice float64   `json:"unit_price" binding:"required"`
	} `json:"line_prices" binding:"required,min=1"`
}

func (h *Handler) SendRFQQuote(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	rfqID, ok := parseUUID(c, "rfqId")
	if !ok {
		return
	}
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NO_SELLER", "seller account not found", nil)
		return
	}
	var req sendRFQQuoteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	lines := make([]postgres.RFQLinePrice, 0, len(req.LinePrices))
	for _, lp := range req.LinePrices {
		lines = append(lines, postgres.RFQLinePrice{
			RFQItemID: lp.RFQItemID, UnitPrice: lp.UnitPrice,
		})
	}
	q, err := h.svc.SendRFQQuote(c.Request.Context(), service.SendRFQQuoteInput{
		RFQID:        rfqID,
		SellerID:     seller.ID,
		LinePrices:   lines,
		ValidityDays: req.ValidityDays,
	})
	if err != nil {
		handleRFQErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, q, nil)
}

func (h *Handler) RejectRFQ(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	rfqID, ok := parseUUID(c, "rfqId")
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.RejectRFQ(c.Request.Context(), rfqID, userID, req.Reason); err != nil {
		handleRFQErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type acceptRFQQuoteReq struct {
	AddressID      uuid.UUID `json:"address_id" binding:"required"`
	PaymentMethod  string    `json:"payment_method"`
	PONumber       *string   `json:"po_number"`
	CostCenter     *string   `json:"cost_center"`
	InvoiceEmail   *string   `json:"invoice_email"`
	IdempotencyKey string    `json:"idempotency_key"`
}

func (h *Handler) AcceptRFQQuote(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	quoteID, ok := parseUUID(c, "quoteId")
	if !ok {
		return
	}
	var req acceptRFQQuoteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	order, err := h.svc.AcceptRFQQuote(c.Request.Context(), service.AcceptRFQQuoteInput{
		QuoteID:        quoteID,
		ActorUserID:    userID,
		AddressID:      req.AddressID,
		PaymentMethod:  req.PaymentMethod,
		PONumber:       req.PONumber,
		CostCenter:     req.CostCenter,
		InvoiceEmail:   req.InvoiceEmail,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		handleRFQErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, order, nil)
}

func handleRFQErr(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrRFQNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, service.ErrRFQForbidden):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
	case errors.Is(err, service.ErrRFQBadStatus):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "BAD_STATUS", err.Error(), nil)
	case errors.Is(err, service.ErrRFQQuoteExpired):
		api.ErrorWithContext(ctx, c.Writer, http.StatusGone, "QUOTE_EXPIRED", err.Error(), nil)
	case errors.Is(err, service.ErrRFQVariantSeller):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "VARIANT_NOT_SELLERS", err.Error(), nil)
	default:
		handleErr(c, err)
	}
}

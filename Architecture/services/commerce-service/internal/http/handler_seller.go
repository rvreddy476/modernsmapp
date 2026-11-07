// Seller route handlers: onboarding and the seller profile, COD
// remittances (admin settle and seller list), seller orders, fulfilment and
// products, earnings and their CSV export, the returns inbox and the payout
// preview. Moved out of handler.go unchanged.

package http

import (
	"encoding/csv"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Seller handlers ─────────────────────────────────────────────

type onboardSellerReq struct {
	SellerType  string  `json:"seller_type"`
	StoreName   string  `json:"store_name" binding:"required"`
	BrandName   *string `json:"brand_name"`
	Slug        string  `json:"slug"`
	Description *string `json:"description"`
	Email       string  `json:"email" binding:"required"`
	Phone       *string `json:"phone"`
	GSTNumber   *string `json:"gst_number"`
	State       *string `json:"state"`
	City        *string `json:"city"`
	PostalCode  *string `json:"postal_code"`
}

func (h *Handler) OnboardSeller(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req onboardSellerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	seller, err := h.svc.OnboardSeller(c.Request.Context(), service.OnboardSellerInput{
		UserID:      userID,
		SellerType:  req.SellerType,
		StoreName:   req.StoreName,
		BrandName:   req.BrandName,
		Slug:        req.Slug,
		Description: req.Description,
		Email:       req.Email,
		Phone:       req.Phone,
		GSTNumber:   req.GSTNumber,
		State:       req.State,
		City:        req.City,
		PostalCode:  req.PostalCode,
	})
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, seller, nil)
}

// GetMySellerProfile GET /v1/commerce/sellers/me
//
// 404 rather than 403 here, unlike every other seller route: this endpoint's
// whole question is "am I a seller", and a client asks it precisely because it
// does not know. "Not found" is the answer; "forbidden" would read as "your
// account is suspended" to an ordinary buyer who has never sold anything.
//
// The distinction that matters is the other one. This used to answer 404 for
// ANY error, so a database incident told every seller their account did not
// exist — and produced no log line while doing it.
func (h *Handler) GetMySellerProfile(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	switch {
	case errors.Is(err, postgres.ErrNoSellerRow):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "seller not found", nil)
		return
	case err != nil:
		slog.Error("commerce: could not read the caller's seller profile",
			"user_id", userID, "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError,
			"INTERNAL_ERROR", "could not read your seller account", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, seller, nil)
}

// AdminSettleCODRemittance marks a pending COD remittance as settled.
// Used by Ops after the seller has been paid out for cash the courier
// collected on their behalf. Optional `payout_batch_id` lets the row
// be associated with the payout batch that actually carried the funds.
//
//	POST /v1/commerce/internal/cod-remittances/:remittanceId/settle
//	  body: { "payout_batch_id": "<uuid?>" }
func (h *Handler) AdminSettleCODRemittance(c *gin.Context) {
	remittanceID, err := uuid.Parse(c.Param("remittanceId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REMITTANCE_ID", err.Error(), nil)
		return
	}
	// Money: the settle and its audit row carry the human who did it.
	actor, ok := requireActor(c)
	if !ok {
		return
	}
	var body struct {
		PayoutBatchID string `json:"payout_batch_id"`
	}
	_ = c.BindJSON(&body)
	var payoutBatchID uuid.UUID
	if body.PayoutBatchID != "" {
		if id, err := uuid.Parse(body.PayoutBatchID); err == nil {
			payoutBatchID = id
		}
	}
	if err := h.svc.SettleCODRemittance(c.Request.Context(), remittanceID, payoutBatchID, actor); err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"remittance_id": remittanceID, "status": "settled"}, nil)
}

// ListMyCODRemittances returns the seller's COD remittance ledger. Each row
// is one COD shipment whose cash has either been collected by the courier
// (status=pending) or transferred to the seller (status=settled).
//
//	GET /v1/commerce/seller/cod-remittances?status=pending&limit=20&offset=0
func (h *Handler) ListMyCODRemittances(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, sellerOK := h.sellerForCaller(c, userID)
	if !sellerOK {
		return
	}
	status := c.Query("status")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	items, total, err := h.svc.ListSellerCODRemittances(c.Request.Context(), seller.ID, status, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	if items == nil {
		items = []*postgres.CODRemittance{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}, nil)
}

// ListMySellerOrders returns orders for the authenticated seller's store.
func (h *Handler) ListMySellerOrders(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, sellerOK := h.sellerForCaller(c, userID)
	if !sellerOK {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	// Each row is the order header plus item_count and
	// seller_subtotal_minor; the bare header had neither, so the list
	// could not say how many parcels or how much money a row was.
	orders, err := h.svc.ListSellerOrderRows(c.Request.Context(), seller.ID, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, orders, nil)
}

// ListSellerFulfillment GET /v1/commerce/seller/fulfillment — Phase 4.2.
// Returns the seller's fulfillment queue with item-level + shipment state in
// a single payload. The `stage` query parameter filters into dashboard tabs:
// unshipped / in_transit / delivered / cancelled (default: all).
func (h *Handler) ListSellerFulfillment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, sellerOK := h.sellerForCaller(c, userID)
	if !sellerOK {
		return
	}
	stage := c.DefaultQuery("stage", "all")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	cards, err := h.svc.ListSellerFulfillment(c.Request.Context(), seller.ID, stage, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"orders": cards, "stage": stage}, nil)
}

// GetSellerOrderDetail GET /v1/commerce/seller/orders/:orderId — Phase 4.2.
// Returns one order from the seller's perspective (their items + their
// shipment + delivery snapshot). Forbidden if the seller has no items in
// the order.
func (h *Handler) GetSellerOrderDetail(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	seller, sellerOK := h.sellerForCaller(c, userID)
	if !sellerOK {
		return
	}
	card, err := h.svc.GetSellerOrderDetail(c.Request.Context(), seller.ID, orderID)
	if err != nil {
		if errors.Is(err, service.ErrOrderNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
			return
		}
		if errors.Is(err, service.ErrNotOrderOwner) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "no items for this seller", nil)
			return
		}
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, card, nil)
}

func (h *Handler) ListSellerProducts(c *gin.Context) {
	sellerID, ok := parseUUID(c, "sellerId")
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	products, err := h.svc.ListSellerProducts(c.Request.Context(), sellerID, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, products, nil)
}

// ListSellerEarnings GET /v1/commerce/seller/earnings — Phase 4.4.
// Returns delivered prepaid (non-COD) order items with gross / commission
// / fee / TDS / net broken out. COD earnings live in /seller/cod-remittances.
func (h *Handler) ListSellerEarnings(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, sellerOK := h.sellerForCaller(c, userID)
	if !sellerOK {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	earnings, err := h.svc.ListSellerEarnings(c.Request.Context(), seller.ID, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"earnings": earnings}, nil)
}

// ExportSellerEarningsCSV GET /v1/commerce/seller/earnings.csv — Phase 4.4.
// CSV download for accountants. Same data as the JSON endpoint, paginated
// large window (500 rows) — bigger statements should be batched offline.
func (h *Handler) ExportSellerEarningsCSV(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, sellerOK := h.sellerForCaller(c, userID)
	if !sellerOK {
		return
	}
	earnings, err := h.svc.ListSellerEarnings(c.Request.Context(), seller.ID, 500, 0)
	if err != nil {
		handleErr(c, err)
		return
	}
	c.Writer.Header().Set("Content-Type", "text/csv")
	c.Writer.Header().Set("Content-Disposition", "attachment; filename=\"earnings.csv\"")
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"delivered_at", "order_number", "order_item_id", "product", "sku", "qty",
		"gross", "commission", "platform_fee", "tds", "net", "payment_method", "status"})
	for _, e := range earnings {
		delivered := ""
		if e.DeliveredAt != nil {
			delivered = e.DeliveredAt.Format("2006-01-02")
		}
		paymentMethod := ""
		if e.PaymentMethod != nil {
			paymentMethod = *e.PaymentMethod
		}
		_ = w.Write([]string{
			delivered, e.OrderNumber, e.OrderItemID.String(), e.ProductTitle, e.SKU,
			strconv.Itoa(e.Quantity),
			strconv.FormatFloat(e.GrossAmount, 'f', 2, 64),
			strconv.FormatFloat(e.CommissionAmount, 'f', 2, 64),
			strconv.FormatFloat(e.PlatformFee, 'f', 2, 64),
			strconv.FormatFloat(e.TDSAmount, 'f', 2, 64),
			strconv.FormatFloat(e.NetAmount, 'f', 2, 64),
			paymentMethod, e.Status,
		})
	}
	w.Flush()
}

// ListSellerReturnsInbox GET /v1/commerce/seller/returns — Phase 4.3.
// Returns the seller's returns inbox with order + item joined inline.
// Optional ?status= filters to requested / approved / rejected / refunded.
func (h *Handler) ListSellerReturnsInbox(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, sellerOK := h.sellerForCaller(c, userID)
	if !sellerOK {
		return
	}
	status := c.Query("status")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	cards, err := h.svc.ListSellerReturns(c.Request.Context(), seller.ID, status, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"returns": cards, "status": status}, nil)
}

// ─── Payout preview handler ──────────────────────────────────────

func (h *Handler) PayoutPreview(c *gin.Context) {
	gross, _ := strconv.ParseFloat(c.Query("gross"), 64)
	commPct, _ := strconv.ParseFloat(c.Query("commission_pct"), 64)
	feePct, _ := strconv.ParseFloat(c.Query("platform_fee_pct"), 64)

	net, comm, fee, tds := h.svc.CalculateSellerPayout(gross, commPct, feePct)
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"gross":        gross,
		"commission":   comm,
		"platform_fee": fee,
		"tds":          tds,
		"net_payout":   net,
	}, nil)
}

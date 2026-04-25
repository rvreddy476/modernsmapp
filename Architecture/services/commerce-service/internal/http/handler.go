// Package http provides Gin HTTP handlers for commerce-service.
package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler wires all commerce HTTP routes.
type Handler struct {
	svc         *service.Service
	internalKey string
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	h.RegisterOnboardingRoutes(r)

	v1 := r.Group("/v1/commerce")

	// ── Catalog ──────────────────────────────────────────────
	v1.GET("/categories", h.ListCategories)
	v1.GET("/products/:productId", h.GetProduct)
	v1.POST("/products", h.CreateProduct)
	v1.GET("/products/:productId/reviews", h.GetProductReviews)
	v1.POST("/products/:productId/reviews", h.CreateReview)

	// ── Seller ───────────────────────────────────────────────
	v1.POST("/sellers/onboard", h.OnboardSeller)
	v1.GET("/sellers/me", h.GetMySellerProfile)
	v1.GET("/sellers/:sellerId/products", h.ListSellerProducts)
	v1.GET("/seller/orders", h.ListMySellerOrders)

	// ── Cart ─────────────────────────────────────────────────
	v1.GET("/cart", h.GetCart)
	v1.POST("/cart/items", h.AddToCart)
	v1.DELETE("/cart/items/:variantId", h.RemoveFromCart)

	// ── Orders ───────────────────────────────────────────────
	v1.POST("/orders/checkout", h.Checkout)
	v1.GET("/orders", h.ListOrders)
	v1.GET("/orders/:orderId", h.GetOrder)
	v1.GET("/orders/:orderId/items", h.GetOrderItems)
	v1.POST("/orders/:orderId/cancel", h.CancelOrder)
	v1.POST("/orders/:orderId/payment/confirm", h.ConfirmPayment)

	// ── Returns ──────────────────────────────────────────────
	v1.POST("/orders/:orderId/returns", h.CreateReturn)

	// ── Addresses ────────────────────────────────────────────
	v1.GET("/addresses", h.ListAddresses)
	v1.POST("/addresses", h.AddAddress)
	v1.PATCH("/addresses/:addressId", h.UpdateAddress)
	v1.DELETE("/addresses/:addressId", h.DeleteAddress)
	v1.POST("/addresses/:addressId/default", h.SetDefaultAddress)

	// ── My returns ───────────────────────────────────────────
	v1.GET("/me/returns", h.ListMyReturns)

	// ── Payout preview ───────────────────────────────────────
	v1.GET("/payout/preview", h.PayoutPreview)

	// ── Shipments + Invoices ─────────────────────────────────
	h.RegisterShipmentRoutes(v1)
}

// ─── helpers ─────────────────────────────────────────────────────

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-Id")
	id, err := uuid.Parse(raw)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid X-User-Id header", nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

func parseUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", "invalid "+param, nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

func handleErr(c *gin.Context, err error) {
	api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
}

// ─── Catalog handlers ────────────────────────────────────────────

func (h *Handler) ListCategories(c *gin.Context) {
	cats, err := h.svc.ListCategories(c.Request.Context())
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, cats, nil)
}

func (h *Handler) GetProduct(c *gin.Context) {
	id, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	p, variants, err := h.svc.GetProduct(c.Request.Context(), id)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"product": p, "variants": variants}, nil)
}

type createProductReq struct {
	CategoryID       *uuid.UUID         `json:"category_id"`
	TaxClassID       *uuid.UUID         `json:"tax_class_id"`
	Title            string             `json:"title" binding:"required"`
	ShortTitle       *string            `json:"short_title"`
	Description      *string            `json:"description"`
	ShortDescription *string            `json:"short_description"`
	ProductType      string             `json:"product_type"`
	Condition        string             `json:"condition"`
	ReturnPolicyType string             `json:"return_policy_type"`
	ReturnPolicyDays int                `json:"return_policy_days"`
	HSNCode          *string            `json:"hsn_code"`
	WeightGrams      *int               `json:"weight_grams"`
	Variants         []createVariantReq `json:"variants" binding:"required,min=1"`
}

type createVariantReq struct {
	SKU          string   `json:"sku" binding:"required"`
	Option1Name  *string  `json:"option_1_name"`
	Option1Value *string  `json:"option_1_value"`
	Option2Name  *string  `json:"option_2_name"`
	Option2Value *string  `json:"option_2_value"`
	Option3Name  *string  `json:"option_3_name"`
	Option3Value *string  `json:"option_3_value"`
	MRP          float64  `json:"mrp" binding:"required"`
	SellingPrice float64  `json:"selling_price" binding:"required"`
	CostPrice    *float64 `json:"cost_price"`
	StockQty     int      `json:"stock_qty"`
}

func (h *Handler) CreateProduct(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req createProductReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusForbidden, "NO_SELLER", "seller account not found", nil, nil)
		return
	}

	variants := make([]service.CreateVariantInput, len(req.Variants))
	for i, v := range req.Variants {
		variants[i] = service.CreateVariantInput{
			SKU:          v.SKU,
			Option1Name:  v.Option1Name,
			Option1Value: v.Option1Value,
			Option2Name:  v.Option2Name,
			Option2Value: v.Option2Value,
			Option3Name:  v.Option3Name,
			Option3Value: v.Option3Value,
			MRP:          v.MRP,
			SellingPrice: v.SellingPrice,
			CostPrice:    v.CostPrice,
			StockQty:     v.StockQty,
		}
	}

	p, err := h.svc.CreateProduct(c.Request.Context(), service.CreateProductInput{
		SellerID:         seller.ID,
		CategoryID:       req.CategoryID,
		TaxClassID:       req.TaxClassID,
		Title:            req.Title,
		ShortTitle:       req.ShortTitle,
		Description:      req.Description,
		ShortDescription: req.ShortDescription,
		ProductType:      req.ProductType,
		Condition:        req.Condition,
		ReturnPolicyType: req.ReturnPolicyType,
		ReturnPolicyDays: req.ReturnPolicyDays,
		HSNCode:          req.HSNCode,
		WeightGrams:      req.WeightGrams,
		Variants:         variants,
	})
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, p, nil)
}

func (h *Handler) GetProductReviews(c *gin.Context) {
	id, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	reviews, total, err := h.svc.GetProductReviews(c.Request.Context(), id, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"reviews": reviews, "total": total}, nil)
}

type createReviewReq struct {
	SellerID    uuid.UUID `json:"seller_id" binding:"required"`
	OrderItemID uuid.UUID `json:"order_item_id" binding:"required"`
	Rating      int       `json:"rating" binding:"required,min=1,max=5"`
	Title       *string   `json:"title"`
	Body        *string   `json:"body"`
}

func (h *Handler) CreateReview(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	var req createReviewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	r := &postgres.Review{
		ProductID:          productID,
		SellerID:           req.SellerID,
		ReviewerID:         userID,
		OrderItemID:        req.OrderItemID,
		Rating:             req.Rating,
		Title:              req.Title,
		Body:               req.Body,
		IsVerifiedPurchase: true,
		IsPublished:        true,
	}
	if err := h.svc.CreateReview(c.Request.Context(), r); err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, r, nil)
}

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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
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

func (h *Handler) GetMySellerProfile(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "seller not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, seller, nil)
}

// ListMySellerOrders returns orders for the authenticated seller's store.
func (h *Handler) ListMySellerOrders(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusForbidden, "NO_SELLER", "seller account not found", nil, nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	orders, err := h.svc.ListSellerOrders(c.Request.Context(), seller.ID, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, orders, nil)
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

// ─── Cart handlers ───────────────────────────────────────────────

func (h *Handler) GetCart(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	summary, err := h.svc.GetCart(c.Request.Context(), userID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, summary, nil)
}

type addToCartReq struct {
	VariantID uuid.UUID `json:"variant_id" binding:"required"`
	Quantity  int       `json:"quantity" binding:"required,min=1"`
}

func (h *Handler) AddToCart(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req addToCartReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	if err := h.svc.AddToCart(c.Request.Context(), userID, req.VariantID, req.Quantity); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "ADD_TO_CART_FAILED", err.Error(), nil, nil)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) RemoveFromCart(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	variantID, ok := parseUUID(c, "variantId")
	if !ok {
		return
	}
	if err := h.svc.RemoveFromCart(c.Request.Context(), userID, variantID); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ─── Order handlers ──────────────────────────────────────────────

type checkoutReq struct {
	AddressID      uuid.UUID `json:"address_id" binding:"required"`
	PaymentMethod  string    `json:"payment_method" binding:"required"`
	CouponCode     string    `json:"coupon_code"`
	GiftMessage    *string   `json:"gift_message"`
	IdempotencyKey string    `json:"idempotency_key"`
}

func (h *Handler) Checkout(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req checkoutReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	order, err := h.svc.Checkout(c.Request.Context(), service.CheckoutInput{
		UserID:         userID,
		AddressID:      req.AddressID,
		PaymentMethod:  req.PaymentMethod,
		CouponCode:     req.CouponCode,
		GiftMessage:    req.GiftMessage,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CHECKOUT_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, order, nil)
}

func (h *Handler) ListOrders(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	orders, err := h.svc.ListOrders(c.Request.Context(), userID, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, orders, nil)
}

func (h *Handler) GetOrder(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	order, err := h.svc.GetOrder(c.Request.Context(), orderID, userID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "order not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, order, nil)
}

func (h *Handler) GetOrderItems(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	order, items, err := h.svc.GetOrderWithItems(c.Request.Context(), orderID, userID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "order not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"order": order, "items": items}, nil)
}

type cancelOrderReq struct {
	Reason string `json:"reason"`
}

func (h *Handler) CancelOrder(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	var req cancelOrderReq
	_ = c.ShouldBindJSON(&req)

	if err := h.svc.CancelOrder(c.Request.Context(), orderID, userID, "customer", req.Reason); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CANCEL_FAILED", err.Error(), nil, nil)
		return
	}
	c.Status(http.StatusNoContent)
}

type confirmPaymentReq struct {
	PaymentID string `json:"payment_id" binding:"required"`
	Gateway   string `json:"gateway" binding:"required"`
}

func (h *Handler) ConfirmPayment(c *gin.Context) {
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	var req confirmPaymentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	if err := h.svc.ConfirmPayment(c.Request.Context(), orderID, req.PaymentID, req.Gateway); err != nil {
		handleErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ─── Return handlers ─────────────────────────────────────────────

type createReturnReq struct {
	OrderItemID       uuid.UUID `json:"order_item_id" binding:"required"`
	SellerID          uuid.UUID `json:"seller_id" binding:"required"`
	ReasonCode        string    `json:"reason_code" binding:"required"`
	ReasonDescription *string   `json:"reason_description"`
}

func (h *Handler) CreateReturn(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	var req createReturnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	ret, err := h.svc.CreateReturnRequest(c.Request.Context(), service.CreateReturnInput{
		OrderID:           orderID,
		OrderItemID:       req.OrderItemID,
		CustomerUserID:    userID,
		SellerID:          req.SellerID,
		ReasonCode:        req.ReasonCode,
		ReasonDescription: req.ReasonDescription,
	})
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, ret, nil)
}

// ─── Address handlers ────────────────────────────────────────────

func (h *Handler) ListAddresses(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	addrs, err := h.svc.GetAddresses(c.Request.Context(), userID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, addrs, nil)
}

type addAddressReq struct {
	AddressType  string  `json:"address_type"`
	FullName     string  `json:"full_name" binding:"required"`
	Phone        string  `json:"phone" binding:"required"`
	AddressLine1 string  `json:"address_line_1" binding:"required"`
	AddressLine2 *string `json:"address_line_2"`
	Landmark     *string `json:"landmark"`
	City         string  `json:"city" binding:"required"`
	State        string  `json:"state" binding:"required"`
	PostalCode   string  `json:"postal_code" binding:"required"`
	Country      string  `json:"country"`
	IsDefault    bool    `json:"is_default"`
}

func (h *Handler) AddAddress(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req addAddressReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	country := req.Country
	if country == "" {
		country = "IN"
	}
	addr := &postgres.CustomerAddress{
		UserID:       userID,
		AddressType:  req.AddressType,
		ContactName:  req.FullName,
		Phone:        req.Phone,
		AddressLine1: req.AddressLine1,
		AddressLine2: req.AddressLine2,
		Landmark:     req.Landmark,
		City:         req.City,
		State:        req.State,
		PostalCode:   req.PostalCode,
		Country:      country,
		IsDefault:    req.IsDefault,
	}
	if err := h.svc.AddAddress(c.Request.Context(), addr); err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, addr, nil)
}

func (h *Handler) UpdateAddress(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	addrID, ok := parseUUID(c, "addressId")
	if !ok {
		return
	}
	var req addAddressReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	country := req.Country
	if country == "" {
		country = "IN"
	}
	addressType := req.AddressType
	if addressType == "" {
		addressType = "home"
	}
	addr := &postgres.CustomerAddress{
		AddressType: addressType, ContactName: req.FullName, Phone: req.Phone,
		AddressLine1: req.AddressLine1, AddressLine2: req.AddressLine2,
		Landmark: req.Landmark, City: req.City, State: req.State,
		PostalCode: req.PostalCode, Country: country, IsDefault: req.IsDefault,
	}
	if err := h.svc.UpdateAddress(c.Request.Context(), addrID, userID, addr); err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) DeleteAddress(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	addrID, ok := parseUUID(c, "addressId")
	if !ok {
		return
	}
	if err := h.svc.DeleteAddress(c.Request.Context(), addrID, userID); err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) SetDefaultAddress(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	addrID, ok := parseUUID(c, "addressId")
	if !ok {
		return
	}
	if err := h.svc.SetDefaultAddress(c.Request.Context(), addrID, userID); err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ListMyReturns(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	returns, err := h.svc.ListMyReturns(c.Request.Context(), userID, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, returns, nil)
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

// Package http provides Gin HTTP handlers for commerce-service.
package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

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
	v1.GET("/products", h.ListProducts)
	v1.GET("/products/:productId", h.GetProduct)
	v1.POST("/products", h.CreateProduct)
	v1.GET("/products/:productId/media", h.ListProductMedia)
	v1.POST("/products/:productId/media", h.AddProductMedia)
	v1.GET("/products/:productId/attributes", h.GetProductAttributes)
	v1.PUT("/products/:productId/attributes", h.SetProductAttributes)
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
	v1.PATCH("/cart/items/by-variant/:variantId", h.UpdateCartItem)
	v1.DELETE("/cart/items/:variantId", h.RemoveFromCart)

	// ── Orders ───────────────────────────────────────────────
	v1.POST("/checkout/quote", h.CheckoutQuote)
	v1.GET("/serviceability", h.CheckServiceability)
	v1.POST("/orders/checkout", h.Checkout)
	v1.GET("/orders", h.ListOrders)
	v1.GET("/orders/:orderId", h.GetOrder)
	v1.GET("/orders/:orderId/items", h.GetOrderItems)
	v1.POST("/orders/:orderId/cancel", h.CancelOrder)
	v1.POST("/orders/:orderId/payment/confirm", h.ConfirmPayment)

	// ── Returns ──────────────────────────────────────────────
	v1.POST("/orders/:orderId/returns", h.CreateReturn)
	v1.POST("/returns/:returnId/approve", h.ApproveReturn)
	v1.POST("/returns/:returnId/reject", h.RejectReturn)
	v1.GET("/returns/:returnId", h.GetReturn)
	v1.POST("/reviews/:reviewId/seller-response", h.SellerRespondToReview)

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

	// ── COD remittances (seller-facing) ──────────────────────
	v1.GET("/seller/cod-remittances", h.ListMyCODRemittances)

	// ── Shipments + Invoices ─────────────────────────────────
	h.RegisterShipmentRoutes(v1)
}

// ─── helpers ─────────────────────────────────────────────────────

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-Id")
	id, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid X-User-Id header", nil)
		return uuid.Nil, false
	}
	return id, true
}

func parseUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAM", "invalid "+param, nil)
		return uuid.Nil, false
	}
	return id, true
}

func handleErr(c *gin.Context, err error) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
	CategoryID       *uuid.UUID `json:"category_id"`
	BrandID          *uuid.UUID `json:"brand_id"`
	TaxClassID       *uuid.UUID `json:"tax_class_id"`
	Title            string     `json:"title" binding:"required"`
	ShortTitle       *string    `json:"short_title"`
	Description      *string    `json:"description"`
	ShortDescription *string    `json:"short_description"`
	BrandName        *string    `json:"brand_name"`
	ManufacturerName *string    `json:"manufacturer_name"`
	ProductType      string     `json:"product_type"`
	Condition        string     `json:"condition"`
	ReturnPolicyType string     `json:"return_policy_type"`
	ReturnPolicyDays int        `json:"return_policy_days"`
	HSNCode          *string    `json:"hsn_code"`
	// Logistics + legal-metrology (Phase 3.1 — schema has the columns).
	PrimaryImageMediaID *uuid.UUID         `json:"primary_image_media_id"`
	VideoMediaID        *uuid.UUID         `json:"video_media_id"`
	WeightGrams         *int               `json:"weight_grams"`
	LengthCm            *float64           `json:"length_cm"`
	WidthCm             *float64           `json:"width_cm"`
	HeightCm            *float64           `json:"height_cm"`
	CountryOfOrigin     *string            `json:"country_of_origin"`
	WarrantyInfo        *string            `json:"warranty_info"`
	SearchKeywords      []string           `json:"search_keywords"`
	MetaTitle           *string            `json:"meta_title"`
	MetaDescription     *string            `json:"meta_description"`
	Variants            []createVariantReq `json:"variants" binding:"required,min=1"`
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NO_SELLER", "seller account not found", nil)
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
		SellerID:            seller.ID,
		CategoryID:          req.CategoryID,
		BrandID:             req.BrandID,
		TaxClassID:          req.TaxClassID,
		Title:               req.Title,
		ShortTitle:          req.ShortTitle,
		Description:         req.Description,
		ShortDescription:    req.ShortDescription,
		BrandName:           req.BrandName,
		ManufacturerName:    req.ManufacturerName,
		ProductType:         req.ProductType,
		Condition:           req.Condition,
		ReturnPolicyType:    req.ReturnPolicyType,
		ReturnPolicyDays:    req.ReturnPolicyDays,
		HSNCode:             req.HSNCode,
		PrimaryImageMediaID: req.PrimaryImageMediaID,
		VideoMediaID:        req.VideoMediaID,
		WeightGrams:         req.WeightGrams,
		LengthCm:            req.LengthCm,
		WidthCm:             req.WidthCm,
		HeightCm:            req.HeightCm,
		CountryOfOrigin:     req.CountryOfOrigin,
		WarrantyInfo:        req.WarrantyInfo,
		SearchKeywords:      req.SearchKeywords,
		MetaTitle:           req.MetaTitle,
		MetaDescription:     req.MetaDescription,
		Variants:            variants,
	})
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, p, nil)
}

// ─── Product Media + Attributes (Phase 3.1) ──────────────────

type addProductMediaReq struct {
	MediaID   uuid.UUID `json:"media_id" binding:"required"`
	MediaType string    `json:"media_type"`
	SortOrder int       `json:"sort_order"`
}

// AddProductMedia POST /v1/commerce/products/:productId/media — seller
// only. Attaches an already-uploaded media asset to the product gallery.
func (h *Handler) AddProductMedia(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	var req addProductMediaReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.AddProductMedia(c.Request.Context(), productID, userID, req.MediaID, req.MediaType, req.SortOrder)
	if err != nil {
		if errors.Is(err, service.ErrNotOrderOwner) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "not your product", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "ADD_MEDIA_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"media": out}, nil)
}

// ListProductMedia GET /v1/commerce/products/:productId/media — public.
func (h *Handler) ListProductMedia(c *gin.Context) {
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	out, err := h.svc.ListProductMedia(c.Request.Context(), productID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"media": out}, nil)
}

type setProductAttrsReq struct {
	Attributes []productAttrPayload `json:"attributes"`
}

type productAttrPayload struct {
	Name  string  `json:"name" binding:"required"`
	Value string  `json:"value" binding:"required"`
	Unit  *string `json:"unit"`
}

// SetProductAttributes PUT /v1/commerce/products/:productId/attributes —
// seller only. Replaces the product's structured spec block in one call.
func (h *Handler) SetProductAttributes(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	var req setProductAttrsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	attrs := make([]postgres.ProductAttribute, 0, len(req.Attributes))
	for _, a := range req.Attributes {
		attrs = append(attrs, postgres.ProductAttribute{
			Name: a.Name, Value: a.Value, Unit: a.Unit,
		})
	}
	out, err := h.svc.SetProductAttributes(c.Request.Context(), productID, userID, attrs)
	if err != nil {
		if errors.Is(err, service.ErrNotOrderOwner) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "not your product", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "SET_ATTRS_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"attributes": out}, nil)
}

// GetProductAttributes GET /v1/commerce/products/:productId/attributes — public.
func (h *Handler) GetProductAttributes(c *gin.Context) {
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	out, err := h.svc.GetProductAttributes(c.Request.Context(), productID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"attributes": out}, nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	r := &postgres.Review{
		ProductID:   productID,
		SellerID:    req.SellerID,
		ReviewerID:  userID,
		OrderItemID: req.OrderItemID,
		Rating:      req.Rating,
		Title:       req.Title,
		Body:        req.Body,
		// IsVerifiedPurchase is set by the service layer after it
		// validates the order item; callers may no longer self-declare it.
		IsPublished: true,
	}
	if err := h.svc.CreateReview(c.Request.Context(), r); err != nil {
		switch {
		case errors.Is(err, service.ErrReviewOrderItemInvalid):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "REVIEW_NOT_ELIGIBLE", err.Error(), nil)
		case errors.Is(err, service.ErrReviewItemNotDelivered):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REVIEW_ITEM_NOT_DELIVERED", err.Error(), nil)
		default:
			handleErr(c, err)
		}
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

func (h *Handler) GetMySellerProfile(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "seller not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, seller, nil)
}

// ListMyCODRemittances returns the seller's COD remittance ledger. Each row
// is one COD shipment whose cash has either been collected by the courier
// (status=pending) or transferred to the seller (status=settled).
//
//   GET /v1/commerce/seller/cod-remittances?status=pending&limit=20&offset=0
func (h *Handler) ListMyCODRemittances(c *gin.Context) {
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
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NO_SELLER", "seller account not found", nil)
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

// ListProducts is the customer-facing global product browse endpoint.
//   GET /v1/commerce/products?category={uuid}&q={text}&limit=20&offset=0
//
// Returns published + approved products only. category and q are optional.
// Response: { items: [...], total: int, limit: int, offset: int }
func (h *Handler) ListProducts(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var categoryID *uuid.UUID
	if cat := c.Query("category"); cat != "" {
		id, err := uuid.Parse(cat)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CATEGORY", "category must be a UUID", nil)
			return
		}
		categoryID = &id
	}
	query := c.Query("q")

	products, total, err := h.svc.ListProducts(c.Request.Context(), categoryID, query, limit, offset)
	if err != nil {
		handleErr(c, err)
		return
	}
	if products == nil {
		products = []*postgres.Product{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items":  products,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}, nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.AddToCart(c.Request.Context(), userID, req.VariantID, req.Quantity); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "ADD_TO_CART_FAILED", err.Error(), nil)
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

// updateCartItemReq is the body for PATCH /cart/items/by-variant/:variantId
// — Phase 1.2. Quantity 0 deletes the line; otherwise it is the absolute
// new quantity (atomic upsert with stock check, not a delta).
type updateCartItemReq struct {
	Quantity int `json:"quantity"`
}

// UpdateCartItem PATCH /v1/commerce/cart/items/by-variant/:variantId.
// Returns the updated cart summary so the client renders immediately
// without a separate GET.
func (h *Handler) UpdateCartItem(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	variantID, ok := parseUUID(c, "variantId")
	if !ok {
		return
	}
	var req updateCartItemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if req.Quantity < 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "quantity must be >= 0", nil)
		return
	}
	if err := h.svc.UpdateCartItem(c.Request.Context(), userID, variantID, req.Quantity); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "UPDATE_CART_FAILED", err.Error(), nil)
		return
	}
	summary, err := h.svc.GetCart(c.Request.Context(), userID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, summary, nil)
}

// ─── Order handlers ──────────────────────────────────────────────

type checkoutReq struct {
	AddressID      uuid.UUID `json:"address_id" binding:"required"`
	PaymentMethod  string    `json:"payment_method" binding:"required"`
	CouponCode     string    `json:"coupon_code"`
	GiftMessage    *string   `json:"gift_message"`
	IdempotencyKey string    `json:"idempotency_key"`
}

// quoteReq mirrors checkoutReq minus the persistence-only fields. Mobile
// and web call this BEFORE "Place order" so the customer sees server-
// authoritative totals; the client never recomputes pricing locally.
type quoteReq struct {
	AddressID     uuid.UUID `json:"address_id" binding:"required"`
	PaymentMethod string    `json:"payment_method" binding:"required"`
	CouponCode    string    `json:"coupon_code"`
}

// CheckServiceability GET /v1/commerce/serviceability — Phase 1.3.
// Customer-facing pincode + COD + ETA check used by the product detail
// page and checkout to replace the mobile pincode heuristic.
//
//	?pincode=560001&product_id=<uuid>[&variant_id=<uuid>][&seller_id=<uuid>][&payment_method=prepaid|cod]
func (h *Handler) CheckServiceability(c *gin.Context) {
	if _, ok := getUserID(c); !ok {
		return
	}
	pincode := c.Query("pincode")
	productIDStr := c.Query("product_id")
	if pincode == "" || productIDStr == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_QUERY", "pincode and product_id are required", nil)
		return
	}
	productID, err := uuid.Parse(productIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_QUERY", "invalid product_id", nil)
		return
	}
	in := service.ServiceabilityInput{
		Pincode:       pincode,
		ProductID:     productID,
		PaymentMethod: c.DefaultQuery("payment_method", "prepaid"),
	}
	if v := c.Query("variant_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			in.VariantID = id
		}
	}
	if v := c.Query("seller_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			in.SellerID = id
		}
	}
	res, err := h.svc.CheckServiceability(c.Request.Context(), in)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "SERVICEABILITY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// CheckoutQuote POST /v1/commerce/checkout/quote — Phase 1.1.
// Returns the same pricing the immediately-following Checkout call will
// produce, including per-line breakdown, unavailable items, and (Phase
// 1.3) serviceability + COD eligibility.
func (h *Handler) CheckoutQuote(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req quoteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	q, err := h.svc.Quote(c.Request.Context(), service.QuoteInput{
		UserID:        userID,
		AddressID:     req.AddressID,
		PaymentMethod: req.PaymentMethod,
		CouponCode:    req.CouponCode,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "QUOTE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, q, nil)
}

func (h *Handler) Checkout(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req checkoutReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CHECKOUT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, order, nil)
}

// ListOrders GET /v1/commerce/orders — Phase 2.1.
// Returns rich order cards (item count, seller count, first item) with
// keyset pagination. next_cursor lives on the meta envelope; clients
// thread it back as ?cursor=... on the next page. The legacy ?offset=
// query param is accepted but ignored — keyset is the only path now,
// since offset over orders required a table-scanning COUNT(*) per page.
func (h *Handler) ListOrders(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	cursor := c.Query("cursor")
	res, err := h.svc.ListOrderCards(c.Request.Context(), userID, limit, cursor)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "LIST_ORDERS_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res.Items, &api.Meta{NextCursor: res.NextCursor})
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CANCEL_FAILED", err.Error(), nil)
		return
	}
	c.Status(http.StatusNoContent)
}

// confirmPaymentReq is the customer-facing Razorpay confirmation body.
// PaymentIntentID is the commerce-side payments-service intent id; the
// three razorpay_* fields are what Razorpay returns on successful checkout
// and are forwarded to payments-service for HMAC verification.
type confirmPaymentReq struct {
	PaymentIntentID   uuid.UUID `json:"payment_intent_id" binding:"required"`
	RazorpayOrderID   string    `json:"razorpay_order_id" binding:"required"`
	RazorpayPaymentID string    `json:"razorpay_payment_id" binding:"required"`
	RazorpaySignature string    `json:"razorpay_signature" binding:"required"`
	AmountMinor       int64     `json:"amount_minor,omitempty"`
	Gateway           string    `json:"gateway,omitempty"`
}

func (h *Handler) ConfirmPayment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	var req confirmPaymentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	err := h.svc.ConfirmPayment(c.Request.Context(), orderID, userID, service.ConfirmPaymentInput{
		PaymentIntentID:   req.PaymentIntentID,
		RazorpayOrderID:   req.RazorpayOrderID,
		RazorpayPaymentID: req.RazorpayPaymentID,
		RazorpaySignature: req.RazorpaySignature,
		AmountMinor:       req.AmountMinor,
		Gateway:           req.Gateway,
	})
	if err != nil {
		// Map domain errors to specific HTTP codes — the old generic
		// handleErr swallowed wrong-user / wrong-state into 500, which
		// hid bugs and made misuse hard to debug.
		switch {
		case errors.Is(err, service.ErrOrderNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "ORDER_NOT_FOUND", err.Error(), nil)
		case errors.Is(err, service.ErrNotOrderOwner):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		case errors.Is(err, service.ErrOrderNotPaymentPending):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "ORDER_NOT_PAYMENT_PENDING", err.Error(), nil)
		case errors.Is(err, service.ErrPaymentVerifyFailed),
			errors.Is(err, service.ErrPaymentAmountMismatch):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "PAYMENT_VERIFY_FAILED", err.Error(), nil)
		case errors.Is(err, service.ErrStubGatewayInProd):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "STUB_GATEWAY_NOT_ALLOWED", err.Error(), nil)
		default:
			handleErr(c, err)
		}
		return
	}
	c.Status(http.StatusNoContent)
}

// ─── Return handlers ─────────────────────────────────────────────

// createReturnItem is one line of the multi-item return body (Phase 2.3).
type createReturnItem struct {
	OrderItemID       uuid.UUID `json:"order_item_id" binding:"required"`
	SellerID          uuid.UUID `json:"seller_id" binding:"required"`
	ReasonCode        string    `json:"reason_code" binding:"required"`
	ReasonDescription *string   `json:"reason_description"`
}

// createReturnReq accepts both the multi-item shape `{items:[...]}` and
// the legacy single-item top-level shape — Phase 2.3 lets the mobile app
// fold its current N-call fan-out into a single request without breaking
// the existing single-item callers.
type createReturnReq struct {
	Items           []createReturnItem `json:"items"`
	PickupAddressID *uuid.UUID         `json:"pickup_address_id"`
	// Legacy single-item top-level fields:
	OrderItemID       *uuid.UUID `json:"order_item_id"`
	SellerID          *uuid.UUID `json:"seller_id"`
	ReasonCode        string     `json:"reason_code"`
	ReasonDescription *string    `json:"reason_description"`
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	var items []service.BulkReturnItemInput
	multiItem := len(req.Items) > 0
	if multiItem {
		items = make([]service.BulkReturnItemInput, 0, len(req.Items))
		for _, it := range req.Items {
			items = append(items, service.BulkReturnItemInput{
				OrderItemID:       it.OrderItemID,
				SellerID:          it.SellerID,
				ReasonCode:        it.ReasonCode,
				ReasonDescription: it.ReasonDescription,
			})
		}
	} else if req.OrderItemID != nil && req.SellerID != nil {
		items = []service.BulkReturnItemInput{{
			OrderItemID:       *req.OrderItemID,
			SellerID:          *req.SellerID,
			ReasonCode:        req.ReasonCode,
			ReasonDescription: req.ReasonDescription,
		}}
	} else {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"either items[] or order_item_id+seller_id is required", nil)
		return
	}

	out, err := h.svc.CreateReturnRequestBulk(c.Request.Context(), orderID, userID, items)
	if err != nil && len(out) == 0 {
		handleErr(c, err)
		return
	}
	// Single-item legacy callers get the un-wrapped response. Multi-item
	// callers always get `{items:[...]}` so partial-success is observable.
	if !multiItem && len(out) == 1 {
		api.JSON(c.Writer, http.StatusCreated, out[0], nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, gin.H{"items": out}, nil)
}

// sellerResponseReq is the body for POST /reviews/:reviewId/seller-response.
type sellerResponseReq struct {
	Response string `json:"response" binding:"required"`
}

// SellerRespondToReview lets the seller of a product attach a public
// response to a customer review — Phase 2.4. Only that seller may post.
//
//	POST /v1/commerce/reviews/:reviewId/seller-response
func (h *Handler) SellerRespondToReview(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	reviewID, ok := parseUUID(c, "reviewId")
	if !ok {
		return
	}
	var req sellerResponseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.Response) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "response cannot be empty", nil)
		return
	}
	r, err := h.svc.AddSellerResponseToReview(c.Request.Context(), reviewID, actorID, req.Response)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrReviewNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		case errors.Is(err, service.ErrNotReviewSeller):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		default:
			handleErr(c, err)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, r, nil)
}

// GetReturn GET /v1/commerce/returns/:returnId — Phase 2.2.
// Customer or seller of the return only; mobile was previously listing
// /me/returns and filtering client-side because this endpoint didn't
// exist.
func (h *Handler) GetReturn(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	returnID, ok := parseUUID(c, "returnId")
	if !ok {
		return
	}
	r, err := h.svc.GetReturnRequest(c.Request.Context(), returnID, userID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrReturnNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		case errors.Is(err, service.ErrNotReturnParty):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		default:
			handleErr(c, err)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, r, nil)
}

// ApproveReturn is called by the seller (or admin) to approve a customer's
// return request. Books the reverse-pickup label and initiates the refund.
//
//   POST /v1/commerce/returns/:returnId/approve
func (h *Handler) ApproveReturn(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	returnID, ok := parseUUID(c, "returnId")
	if !ok {
		return
	}
	out, err := h.svc.ApproveReturn(c.Request.Context(), returnID, actorID)
	if err != nil {
		if errors.Is(err, service.ErrNotReturnSeller) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "APPROVE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

type rejectReturnReq struct {
	Reason string `json:"reason" binding:"required"`
}

// RejectReturn closes a return with the seller's stated reason.
//
//   POST /v1/commerce/returns/:returnId/reject
func (h *Handler) RejectReturn(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	returnID, ok := parseUUID(c, "returnId")
	if !ok {
		return
	}
	var req rejectReturnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.RejectReturn(c.Request.Context(), returnID, actorID, req.Reason)
	if err != nil {
		if errors.Is(err, service.ErrNotReturnSeller) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REJECT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
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

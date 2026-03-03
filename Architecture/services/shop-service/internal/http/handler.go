package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/atpost/shop-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc *service.Service
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/shop")
	{
		// Products
		v1.POST("/products", h.CreateProduct)
		v1.GET("/products", h.ListProducts)
		v1.GET("/products/:productId", h.GetProduct)
		v1.PUT("/products/:productId", h.UpdateProduct)
		v1.GET("/sellers/:sellerId/products", h.ListSellerProducts)

		// Cart
		v1.POST("/cart", h.AddToCart)
		v1.GET("/cart", h.GetCart)
		v1.DELETE("/cart/:productId", h.RemoveFromCart)
		v1.DELETE("/cart", h.ClearCart)

		// Orders
		v1.POST("/checkout", h.Checkout)
		v1.GET("/orders", h.ListOrders)
		v1.GET("/orders/:orderId", h.GetOrder)
		v1.PATCH("/orders/:orderId/status", h.UpdateOrderStatus)
	}
}

// --- Products ---

func (h *Handler) CreateProduct(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	sellerID, err := uuid.Parse(userID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_USER_ID", "invalid user id", nil, nil)
		return
	}

	var body struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Price       float64  `json:"price"`
		Currency    string   `json:"currency"`
		Category    string   `json:"category"`
		MediaIDs    []string `json:"media_ids"`
		Stock       int      `json:"stock"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	p, err := h.svc.CreateProduct(c.Request.Context(), &service.CreateProductInput{
		SellerID:    sellerID,
		Title:       body.Title,
		Description: body.Description,
		Price:       body.Price,
		Currency:    body.Currency,
		Category:    body.Category,
		MediaIDs:    body.MediaIDs,
		Stock:       body.Stock,
	})
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, p, nil)
}

func (h *Handler) GetProduct(c *gin.Context) {
	id, err := uuid.Parse(c.Param("productId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid product id", nil, nil)
		return
	}

	p, err := h.svc.GetProduct(c.Request.Context(), id)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "product not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

func (h *Handler) ListProducts(c *gin.Context) {
	category := c.Query("category")
	limit, offset := parsePagination(c)

	products, total, err := h.svc.ListProducts(c.Request.Context(), category, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"items": products,
		"total": total,
	}, nil)
}

func (h *Handler) UpdateProduct(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	sellerID, _ := uuid.Parse(userID)

	productID, err := uuid.Parse(c.Param("productId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid product id", nil, nil)
		return
	}

	var body struct {
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Category    string  `json:"category"`
		Status      string  `json:"status"`
		Price       float64 `json:"price"`
		Stock       int     `json:"stock"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	if err := h.svc.UpdateProduct(c.Request.Context(), productID, sellerID, &service.UpdateProductInput{
		Title:       body.Title,
		Description: body.Description,
		Category:    body.Category,
		Status:      body.Status,
		Price:       body.Price,
		Stock:       body.Stock,
	}); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) ListSellerProducts(c *gin.Context) {
	sellerID, err := uuid.Parse(c.Param("sellerId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid seller id", nil, nil)
		return
	}
	limit, offset := parsePagination(c)

	products, err := h.svc.ListSellerProducts(c.Request.Context(), sellerID, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": products}, nil)
}

// --- Cart ---

func (h *Handler) AddToCart(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	uid, _ := uuid.Parse(userID)

	var body struct {
		ProductID string `json:"product_id"`
		Quantity  int    `json:"quantity"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	productID, err := uuid.Parse(body.ProductID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PRODUCT_ID", "invalid product id", nil, nil)
		return
	}

	qty := body.Quantity
	if qty <= 0 {
		qty = 1
	}

	if err := h.svc.AddToCart(c.Request.Context(), uid, productID, qty); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CART_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "added"}, nil)
}

func (h *Handler) GetCart(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	uid, _ := uuid.Parse(userID)

	items, err := h.svc.GetCart(c.Request.Context(), uid)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "CART_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": items}, nil)
}

func (h *Handler) RemoveFromCart(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	uid, _ := uuid.Parse(userID)

	productID, err := uuid.Parse(c.Param("productId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid product id", nil, nil)
		return
	}

	if err := h.svc.RemoveFromCart(c.Request.Context(), uid, productID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "CART_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

func (h *Handler) ClearCart(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	uid, _ := uuid.Parse(userID)

	if err := h.svc.ClearCart(c.Request.Context(), uid); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "CART_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "cleared"}, nil)
}

// --- Orders ---

func (h *Handler) Checkout(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	uid, _ := uuid.Parse(userID)

	order, err := h.svc.Checkout(c.Request.Context(), uid)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CHECKOUT_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, order, nil)
}

func (h *Handler) GetOrder(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	uid, _ := uuid.Parse(userID)

	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid order id", nil, nil)
		return
	}

	order, err := h.svc.GetOrder(c.Request.Context(), orderID, uid)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, order, nil)
}

func (h *Handler) ListOrders(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	uid, _ := uuid.Parse(userID)

	role := c.DefaultQuery("role", "buyer")
	limit, offset := parsePagination(c)

	orders, err := h.svc.ListOrders(c.Request.Context(), uid, role, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": orders}, nil)
}

func (h *Handler) UpdateOrderStatus(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return
	}
	sellerID, _ := uuid.Parse(userID)

	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid order id", nil, nil)
		return
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}

	if err := h.svc.UpdateOrderStatus(c.Request.Context(), orderID, sellerID, body.Status); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "STATUS_UPDATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": body.Status}, nil)
}

// --- Helpers ---

func parsePagination(c *gin.Context) (int, int) {
	limit := 20
	offset := 0
	if v := c.Query("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}
	if v := c.Query("offset"); v != "" {
		if o, err := strconv.Atoi(v); err == nil && o >= 0 {
			offset = o
		}
	}
	return limit, offset
}

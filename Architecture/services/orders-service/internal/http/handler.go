package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/orders-service/internal/service"
	"github.com/atpost/orders-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

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
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	v1 := r.Group("/v1")
	{
		// Orders
		orders := v1.Group("/orders")
		orders.POST("", h.CreateOrder)
		orders.GET("", h.ListOrders)
		orders.GET("/:orderId", h.GetOrder)
		orders.PATCH("/:orderId/status", h.UpdateOrderStatus)
		orders.POST("/:orderId/disputes", h.OpenDispute)

		// Disputes
		disputes := v1.Group("/orders/disputes")
		disputes.PATCH("/:id/resolve", h.ResolveDispute)
		disputes.PATCH("/:id/status", h.UpdateDisputeStatus)

		// Bookings
		bookings := v1.Group("/bookings")
		bookings.POST("", h.CreateBooking)
		bookings.GET("", h.ListBookings)
		bookings.GET("/:bookingId", h.GetBooking)
		bookings.PATCH("/:bookingId/status", h.UpdateBookingStatus)

		// Returns & Refunds
		v1.POST("/orders/:orderId/return", h.CreateReturnRequest)
		v1.GET("/returns/:returnId", h.GetReturnRequest)
		v1.GET("/returns/buyer", h.ListBuyerReturns)
		v1.GET("/returns/seller", h.ListSellerReturns)
		v1.POST("/returns/:returnId/approve", h.ApproveReturn)
		v1.POST("/returns/:returnId/reject", h.RejectReturn)
		v1.POST("/returns/:returnId/received", h.MarkItemReceived)
		v1.POST("/returns/:returnId/tracking", h.UpdateReturnTracking)
	}
}

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	str := c.GetHeader("X-User-Id")
	if str == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(str)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

func parsePagination(c *gin.Context) (int, int) {
	limit, offset := 20, 0
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

// CreateOrder POST /v1/orders
func (h *Handler) CreateOrder(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		SellerID        string      `json:"seller_id" binding:"required"`
		ListingID       *string     `json:"listing_id"`
		Items           []itemInput `json:"items" binding:"required"`
		Total           float64     `json:"total" binding:"required"`
		Currency        string      `json:"currency"`
		ShippingAddress any         `json:"shipping_address"`
		Notes           string      `json:"notes"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	sellerID, err := uuid.Parse(body.SellerID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_SELLER_ID", "invalid seller_id", nil, nil)
		return
	}

	in := service.CreateOrderInput{
		BuyerID:         userID,
		SellerID:        sellerID,
		Total:           body.Total,
		Currency:        body.Currency,
		ShippingAddress: body.ShippingAddress,
		Notes:           body.Notes,
	}
	if body.ListingID != nil {
		id, err := uuid.Parse(*body.ListingID)
		if err == nil {
			in.ListingID = &id
		}
	}
	for _, it := range body.Items {
		lid, _ := uuid.Parse(it.ListingID)
		in.Items = append(in.Items, service.OrderItemInput{
			ListingID:       lid,
			Title:           it.Title,
			Quantity:        it.Quantity,
			PriceAtPurchase: it.Price,
			Currency:        it.Currency,
		})
	}

	order, err := h.svc.CreateOrder(c.Request.Context(), in)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, order, nil)
}

type itemInput struct {
	ListingID string  `json:"listing_id"`
	Title     string  `json:"title"`
	Quantity  int     `json:"quantity"`
	Price     float64 `json:"price"`
	Currency  string  `json:"currency"`
}

// GetOrder GET /v1/orders/:orderId
func (h *Handler) GetOrder(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid order id", nil, nil)
		return
	}
	order, err := h.svc.GetOrder(c.Request.Context(), orderID, userID)
	if err != nil {
		if err.Error() == "forbidden" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "access denied", nil, nil)
		} else {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "order not found", nil, nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, order, nil)
}

// ListOrders GET /v1/orders
func (h *Handler) ListOrders(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	role := c.DefaultQuery("role", "buyer")
	limit, offset := parsePagination(c)
	orders, err := h.svc.ListOrders(c.Request.Context(), userID, role, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, orders, nil)
}

// UpdateOrderStatus PATCH /v1/orders/:orderId/status
func (h *Handler) UpdateOrderStatus(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid order id", nil, nil)
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	order, err := h.svc.UpdateOrderStatus(c.Request.Context(), orderID, userID, body.Status)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, order, nil)
}

// OpenDispute POST /v1/orders/:orderId/disputes
func (h *Handler) OpenDispute(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid order id", nil, nil)
		return
	}
	var body struct {
		Reason       string   `json:"reason" binding:"required"`
		EvidenceURLs []string `json:"evidence_urls"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	dispute, err := h.svc.OpenDispute(c.Request.Context(), orderID, userID, body.Reason, body.EvidenceURLs)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "DISPUTE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, dispute, nil)
}

// ResolveDispute PATCH /v1/orders/disputes/:id/resolve
func (h *Handler) ResolveDispute(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid dispute id", nil, nil)
		return
	}
	var req struct {
		Resolution   string `json:"resolution"`
		RefundAmount int64  `json:"refund_amount"`
	}
	c.ShouldBindJSON(&req) //nolint:errcheck
	if err := h.svc.ResolveDispute(c.Request.Context(), id, req.Resolution, req.RefundAmount); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "RESOLVE_FAILED", err.Error(), nil, nil)
		return
	}
	c.Status(http.StatusNoContent)
}

// UpdateDisputeStatus PATCH /v1/orders/disputes/:id/status
func (h *Handler) UpdateDisputeStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid dispute id", nil, nil)
		return
	}
	var req struct {
		Status string `json:"status"`
		Notes  string `json:"notes"`
	}
	c.ShouldBindJSON(&req) //nolint:errcheck
	validStatuses := map[string]bool{"under_review": true, "resolved": true, "closed": true}
	if !validStatuses[req.Status] {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_STATUS", "invalid dispute status", nil, nil)
		return
	}
	if err := h.svc.UpdateDisputeStatus(c.Request.Context(), id, req.Status, req.Notes); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	c.Status(http.StatusNoContent)
}

// CreateBooking POST /v1/bookings
func (h *Handler) CreateBooking(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		ProviderID       string `json:"provider_id" binding:"required"`
		ServiceListingID string `json:"service_listing_id" binding:"required"`
		SlotStart        string `json:"slot_start" binding:"required"`
		SlotEnd          string `json:"slot_end" binding:"required"`
		Notes            string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	providerID, _ := uuid.Parse(body.ProviderID)
	listingID, _ := uuid.Parse(body.ServiceListingID)

	slotStart, err := time.Parse(time.RFC3339, body.SlotStart)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_SLOT_START", "use RFC3339 format", nil, nil)
		return
	}
	slotEnd, err := time.Parse(time.RFC3339, body.SlotEnd)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_SLOT_END", "use RFC3339 format", nil, nil)
		return
	}

	booking, err := h.svc.CreateBooking(c.Request.Context(), postgres.Booking{
		CustomerID:       userID,
		ProviderID:       providerID,
		ServiceListingID: listingID,
		SlotStart:        slotStart,
		SlotEnd:          slotEnd,
		Notes:            body.Notes,
	})
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, booking, nil)
}

// GetBooking GET /v1/bookings/:bookingId
func (h *Handler) GetBooking(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	bookingID, err := uuid.Parse(c.Param("bookingId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid booking id", nil, nil)
		return
	}
	b, err := h.svc.GetBooking(c.Request.Context(), bookingID, userID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "booking not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, b, nil)
}

// ListBookings GET /v1/bookings
func (h *Handler) ListBookings(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	role := c.DefaultQuery("role", "customer")
	limit, offset := parsePagination(c)
	bookings, err := h.svc.ListBookings(c.Request.Context(), userID, role, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, bookings, nil)
}

// UpdateBookingStatus PATCH /v1/bookings/:bookingId/status
func (h *Handler) UpdateBookingStatus(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	bookingID, err := uuid.Parse(c.Param("bookingId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid booking id", nil, nil)
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	b, err := h.svc.UpdateBookingStatus(c.Request.Context(), bookingID, userID, body.Status)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, b, nil)
}

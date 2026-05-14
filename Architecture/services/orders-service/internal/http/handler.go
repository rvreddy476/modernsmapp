package http

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/orders-service/internal/service"
	"github.com/atpost/orders-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc          *service.Service
	internalKey  string
	moderatorIDs map[uuid.UUID]struct{}
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc, moderatorIDs: parseModeratorIDs()}
}

func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

// parseModeratorIDs reads ORDERS_MODERATOR_USER_IDS (comma-separated
// UUIDs) into a set. Audit O4: dispute resolution + status mutation
// previously had no role check at all — once past the internal-key
// gate, every authenticated user could resolve any dispute and trigger
// refunds. Proper cross-service role lookup needs a user-service
// dependency this audit can't add. This allowlist is a stopgap that
// ships now and gives operators a real gate, matching the qa-service
// moderator pattern.
func parseModeratorIDs() map[uuid.UUID]struct{} {
	raw := strings.TrimSpace(os.Getenv("ORDERS_MODERATOR_USER_IDS"))
	if raw == "" {
		return nil
	}
	set := make(map[uuid.UUID]struct{})
	for _, part := range strings.Split(raw, ",") {
		id, err := uuid.Parse(strings.TrimSpace(part))
		if err == nil {
			set[id] = struct{}{}
		}
	}
	return set
}

// requireModerator gates a moderation handler. When the env allowlist
// is empty the entire moderation surface fails closed — explicit deny
// beats accidental allow-all.
func (h *Handler) requireModerator(c *gin.Context) bool {
	userID, ok := getUserID(c)
	if !ok {
		return false
	}
	if h.moderatorIDs == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "MODERATION_DISABLED",
			"dispute moderation surface is not configured on this deployment", nil)
		return false
	}
	if _, ok := h.moderatorIDs[userID]; !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NOT_MODERATOR",
			"only moderators can perform this action", nil)
		return false
	}
	return true
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(str)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	sellerID, err := uuid.Parse(body.SellerID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SELLER_ID", "invalid seller_id", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid order id", nil)
		return
	}
	order, err := h.svc.GetOrder(c.Request.Context(), orderID, userID)
	if err != nil {
		if err.Error() == "forbidden" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "access denied", nil)
		} else {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid order id", nil)
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	order, err := h.svc.UpdateOrderStatus(c.Request.Context(), orderID, userID, body.Status)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid order id", nil)
		return
	}
	var body struct {
		Reason       string   `json:"reason" binding:"required"`
		EvidenceURLs []string `json:"evidence_urls"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	dispute, err := h.svc.OpenDispute(c.Request.Context(), orderID, userID, body.Reason, body.EvidenceURLs)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "DISPUTE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, dispute, nil)
}

// ResolveDispute PATCH /v1/orders/disputes/:id/resolve
func (h *Handler) ResolveDispute(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid dispute id", nil)
		return
	}
	var req struct {
		Resolution   string `json:"resolution"`
		RefundAmount int64  `json:"refund_amount"`
	}
	c.ShouldBindJSON(&req) //nolint:errcheck
	if err := h.svc.ResolveDispute(c.Request.Context(), id, req.Resolution, req.RefundAmount); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "RESOLVE_FAILED", err.Error(), nil)
		return
	}
	c.Status(http.StatusNoContent)
}

// UpdateDisputeStatus PATCH /v1/orders/disputes/:id/status
func (h *Handler) UpdateDisputeStatus(c *gin.Context) {
	if !h.requireModerator(c) {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid dispute id", nil)
		return
	}
	var req struct {
		Status string `json:"status"`
		Notes  string `json:"notes"`
	}
	c.ShouldBindJSON(&req) //nolint:errcheck
	validStatuses := map[string]bool{"under_review": true, "resolved": true, "closed": true}
	if !validStatuses[req.Status] {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_STATUS", "invalid dispute status", nil)
		return
	}
	if err := h.svc.UpdateDisputeStatus(c.Request.Context(), id, req.Status, req.Notes); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	providerID, _ := uuid.Parse(body.ProviderID)
	listingID, _ := uuid.Parse(body.ServiceListingID)

	slotStart, err := time.Parse(time.RFC3339, body.SlotStart)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLOT_START", "use RFC3339 format", nil)
		return
	}
	slotEnd, err := time.Parse(time.RFC3339, body.SlotEnd)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLOT_END", "use RFC3339 format", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid booking id", nil)
		return
	}
	b, err := h.svc.GetBooking(c.Request.Context(), bookingID, userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "booking not found", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid booking id", nil)
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	b, err := h.svc.UpdateBookingStatus(c.Request.Context(), bookingID, userID, body.Status)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, b, nil)
}

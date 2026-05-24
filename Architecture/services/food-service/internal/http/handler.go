package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

	v1 := r.Group("/v1/food")
	{
		v1.GET("/home", h.Home)
		v1.GET("/cuisines", h.ListCuisines)
		v1.GET("/restaurants", h.ListRestaurants)
		v1.GET("/restaurants/:restaurantId", h.GetRestaurant)
		v1.GET("/restaurants/:restaurantId/menu", h.GetMenu)
		v1.GET("/search", h.Search)
		v1.GET("/menu-items/:itemId/reviews", h.ListItemReviews)
		v1.GET("/restaurants/:restaurantId/prep-time", h.GetRestaurantPrepTime)

		user := v1.Group("", h.requireAuthenticated())
		{
			user.GET("/me/capabilities", h.GetCapabilities)
			user.POST("/realtime/token", h.IssueRealtimeToken)
			user.POST("/cart/items", h.AddCartItem)
			user.GET("/cart", h.GetCart)
			user.PATCH("/cart/items/:cartItemId", h.UpdateCartItem)
			user.DELETE("/cart/items/:cartItemId", h.RemoveCartItem)
			user.DELETE("/cart", h.ClearCart)
			user.POST("/coupons/validate", h.ApplyCoupon)

			user.GET("/addresses", h.ListAddresses)
			user.POST("/addresses", h.CreateAddress)
			user.PATCH("/addresses/:addressId", h.UpdateAddress)
			user.DELETE("/addresses/:addressId", h.DeleteAddress)

			user.POST("/orders", h.PlaceOrder)
			user.GET("/orders", h.ListOrders)
			user.GET("/orders/:orderId", h.GetOrder)
			user.GET("/orders/:orderId/tracking", h.GetOrderTracking)
			user.POST("/orders/:orderId/payments/intents", h.CreatePaymentIntent)
			user.POST("/orders/:orderId/payments/confirm", h.ConfirmPayment)
			user.POST("/orders/:orderId/cancel", h.CancelOrder)
			user.GET("/orders/:orderId/substitutions", h.ListSubstitutions)
			user.POST("/orders/:orderId/substitutions/:subId/respond", h.RespondSubstitution)
			user.POST("/menu-items/:itemId/report", h.ReportMenuItem)
			user.POST("/orders/:orderId/verify-delivery", h.CustomerVerifyDeliveryOTP)
			user.POST("/support/tickets", h.CreateTicket)
			user.GET("/support/tickets/me", h.ListMyTickets)
			user.GET("/support/tickets/:ticketId", h.GetTicket)
			user.POST("/support/tickets/:ticketId/messages", h.AppendTicketMessage)
			user.POST("/refunds", h.CreateRefundRequest)
			user.POST("/menu-items/:itemId/reviews", h.CreateItemReview)
			user.GET("/orders/:orderId/invoice", h.CustomerGetInvoice)
			user.GET("/me/loyalty", h.GetMyLoyalty)
			user.POST("/me/loyalty/redeem", h.RedeemLoyalty)
			user.GET("/me/referral", h.GetMyReferralCode)
			user.POST("/me/referral/apply", h.ApplyReferralCode)
			user.GET("/orders/:orderId/messages", h.ListOrderMessages)
			user.POST("/orders/:orderId/messages", h.PostOrderMessage)
			user.POST("/orders/:orderId/messages/:msgId/read", h.MarkOrderMessageRead)
			user.POST("/orders/:orderId/ratings/restaurant", h.RateRestaurant)
			user.POST("/orders/:orderId/ratings/delivery", h.RateDelivery)
		}

		partner := v1.Group("/partner", h.requireAuthenticated())
		{
			partner.POST("/restaurants", h.CreatePartnerRestaurant)
			partner.GET("/restaurants", h.ListPartnerRestaurants)
			partner.GET("/restaurants/:restaurantId", h.GetPartnerRestaurant)
			partner.PATCH("/restaurants/:restaurantId", h.UpdatePartnerRestaurant)
			partner.POST("/restaurants/:restaurantId/documents", h.AddRestaurantDocument)
			partner.POST("/restaurants/:restaurantId/images", h.AddRestaurantImage)
			partner.GET("/restaurants/:restaurantId/menu/categories", h.ListMenuCategories)
			partner.POST("/restaurants/:restaurantId/menu/categories", h.CreateMenuCategory)
			partner.PATCH("/menu/categories/:categoryId", h.UpdateMenuCategory)
			partner.DELETE("/menu/categories/:categoryId", h.DeleteMenuCategory)
			partner.POST("/restaurants/:restaurantId/menu/items", h.CreateMenuItem)
			partner.PATCH("/menu/items/:itemId", h.UpdateMenuItem)
			partner.DELETE("/menu/items/:itemId", h.DeleteMenuItem)
			partner.PATCH("/menu/items/:itemId/availability", h.SetMenuItemAvailability)
			partner.GET("/restaurants/:restaurantId/orders", h.ListPartnerOrders)
			partner.GET("/restaurants/:restaurantId/kitchen-queue", h.ListKitchenQueue)
			partner.POST("/orders/:orderId/substitutions", h.PartnerProposeSubstitution)
			partner.POST("/orders/:orderId/verify-pickup", h.PartnerVerifyPickupOTP)
			partner.GET("/restaurants/:restaurantId/settlements", h.PartnerRestaurantSettlements)
			partner.GET("/restaurants/:restaurantId/reports/summary", h.PartnerRestaurantSummary)
			partner.POST("/orders/:orderId/accept", h.PartnerAcceptOrder)
			partner.POST("/orders/:orderId/reject", h.PartnerRejectOrder)
			partner.POST("/orders/:orderId/mark-preparing", h.PartnerMarkPreparing)
			partner.POST("/orders/:orderId/mark-ready", h.PartnerMarkReady)
		}

		deliveryOffers := v1.Group("/delivery/offers", h.requireAuthenticated())
		{
			deliveryOffers.GET("/me", h.ListMyDeliveryOffers)
			deliveryOffers.POST("/:offerId/accept", h.AcceptDeliveryOffer)
			deliveryOffers.POST("/:offerId/reject", h.RejectDeliveryOffer)
		}
		deliveryProof := v1.Group("/delivery/orders", h.requireAuthenticated())
		{
			deliveryProof.POST("/:orderId/proof", h.PartnerAttachProof)
		}

		delivery := v1.Group("/delivery", h.requireAuthenticated())
		{
			delivery.POST("/profile", h.UpsertDeliveryPartner)
			delivery.GET("/profile", h.GetDeliveryPartner)
			delivery.PATCH("/profile", h.UpsertDeliveryPartner)
			delivery.POST("/documents", h.AddDeliveryDocument)
			delivery.POST("/availability", h.SetDeliveryAvailability)
			delivery.GET("/assignments", h.ListDeliveryAssignments)
			delivery.GET("/assignments/current", h.GetCurrentDeliveryAssignment)
			delivery.GET("/assignments/:assignmentId/tracking", h.GetAssignmentTracking)
			delivery.POST("/assignments/:assignmentId/accept", h.DeliveryAcceptAssignment)
			delivery.POST("/assignments/:assignmentId/reject", h.DeliveryRejectAssignment)
			delivery.POST("/assignments/:assignmentId/arrived-restaurant", h.DeliveryArrivedRestaurant)
			delivery.POST("/assignments/:assignmentId/picked-up", h.DeliveryPickedUp)
			delivery.POST("/assignments/:assignmentId/arrived-customer", h.DeliveryArrivedCustomer)
			delivery.POST("/assignments/:assignmentId/delivered", h.DeliveryDelivered)
			delivery.POST("/location", h.UpdateDeliveryLocation)
			delivery.GET("/earnings", h.GetDeliveryEarnings)
			delivery.GET("/history", h.GetDeliveryHistory)
		}

		admin := v1.Group("/admin", h.requireAdminScope())
		{
			admin.GET("/dashboard", h.AdminDashboard)
			admin.GET("/moderation/queue", h.AdminListPendingModeration)
			admin.POST("/moderation/menu-items/:itemId", h.AdminModerateMenuItem)
			admin.GET("/support/tickets", h.AdminListTickets)
			admin.POST("/support/tickets/:ticketId/status", h.AdminSetTicketStatus)
			admin.GET("/refunds", h.AdminListRefunds)
			admin.POST("/refunds/:refundId/decide", h.AdminDecideRefund)
			admin.DELETE("/item-reviews/:reviewId", h.AdminHideItemReview)
			admin.GET("/reports/restaurant-sla", h.AdminRestaurantSLAReport)
			admin.GET("/reports/delivery-sla", h.AdminDeliverySLAReport)
			admin.GET("/reports/payment-recon", h.AdminPaymentReconReport)
			admin.GET("/reports/refunds", h.AdminRefundsReport)
			admin.GET("/reports/coupon-abuse", h.AdminCouponAbuseReport)
			admin.GET("/reports/compliance", h.AdminComplianceReport)
			admin.GET("/fraud/top", h.AdminTopFraudUsers)
			admin.POST("/settlements/files", h.AdminGenerateSettlementFile)
			admin.GET("/settlements/files", h.AdminListSettlementFiles)
			admin.GET("/settlements/files/:id/download", h.AdminDownloadSettlementFile)
			admin.GET("/restaurants/pending", h.AdminPendingRestaurants)
			admin.POST("/restaurants/:restaurantId/approve", h.AdminApproveRestaurant)
			admin.POST("/restaurants/:restaurantId/reject", h.AdminRejectRestaurant)
			admin.PATCH("/restaurants/:restaurantId/status", h.AdminSetRestaurantStatus)
			admin.GET("/delivery-partners/pending", h.AdminPendingDeliveryPartners)
			admin.POST("/delivery-partners/:partnerId/approve", h.AdminApproveDeliveryPartner)
			admin.POST("/delivery-partners/:partnerId/reject", h.AdminRejectDeliveryPartner)
			admin.PATCH("/delivery-partners/:partnerId/status", h.AdminSetDeliveryPartnerStatus)
			admin.GET("/orders", h.AdminListOrders)
			admin.GET("/orders/:orderId", h.AdminGetOrder)
			admin.POST("/orders/:orderId/cancel", h.AdminCancelOrder)
			admin.POST("/orders/:orderId/refund", h.AdminRefundOrder)
			admin.GET("/coupons", h.AdminListCoupons)
			admin.POST("/coupons", h.AdminCreateCoupon)
			admin.PATCH("/coupons/:couponId", h.AdminUpdateCoupon)
			admin.GET("/service-areas", h.AdminListServiceAreas)
			admin.POST("/service-areas", h.AdminCreateServiceArea)
			admin.PATCH("/service-areas/:areaId", h.AdminUpdateServiceArea)
			admin.POST("/settlements/generate", h.AdminGenerateSettlements)
			admin.GET("/settlements/restaurants", h.AdminListRestaurantSettlements)
			admin.POST("/settlements/restaurants/:settlementId/mark-paid", h.AdminMarkRestaurantSettlementPaid)
			admin.GET("/settlements/delivery-partners", h.AdminListDeliverySettlements)
			admin.POST("/settlements/delivery-partners/:settlementId/mark-paid", h.AdminMarkDeliverySettlementPaid)
			admin.GET("/audit-logs", h.AdminAuditLogs)
			admin.GET("/reports/orders", h.AdminOrderReport)
			admin.GET("/reports/revenue", h.AdminRevenueReport)
		}
	}
}

func (h *Handler) Home(c *gin.Context) {
	home, err := h.svc.Home(c.Request.Context(), c.Query("city"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_HOME_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, home)
}

func (h *Handler) ListCuisines(c *gin.Context) {
	cuisines, err := h.svc.ListCuisines(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_CUISINES_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": cuisines})
}

func (h *Handler) ListRestaurants(c *gin.Context) {
	restaurants, err := h.svc.ListRestaurants(c.Request.Context(), postgres.RestaurantFilter{
		Query: c.Query("q"),
		City:  c.Query("city"),
		Limit: parseLimit(c.DefaultQuery("limit", "20")),
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_RESTAURANTS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": restaurants})
}

func (h *Handler) Search(c *gin.Context) {
	restaurants, err := h.svc.ListRestaurants(c.Request.Context(), postgres.RestaurantFilter{
		Query: c.Query("q"),
		City:  c.Query("city"),
		Limit: parseLimit(c.DefaultQuery("limit", "20")),
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_SEARCH_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{
		"restaurants": restaurants,
		"dishes":      []any{},
	})
}

func (h *Handler) GetRestaurant(c *gin.Context) {
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	restaurant, err := h.svc.GetRestaurant(c.Request.Context(), restaurantID)
	if err != nil {
		status := http.StatusInternalServerError
		code := "FOOD_RESTAURANT_FAILED"
		message := err.Error()
		if errors.Is(err, pgx.ErrNoRows) {
			status = http.StatusNotFound
			code = "FOOD_RESTAURANT_NOT_FOUND"
			message = "restaurant not found"
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, message, nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, restaurant)
}

func (h *Handler) GetMenu(c *gin.Context) {
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	menu, err := h.svc.GetMenu(c.Request.Context(), restaurantID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_MENU_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"categories": menu})
}

func (h *Handler) GetCart(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	cart, err := h.svc.GetCart(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_CART_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, cart)
}

func (h *Handler) AddCartItem(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	var body struct {
		MenuItemID      string `json:"menu_item_id"`
		VariantID       string `json:"variant_id"`
		Quantity        int    `json:"quantity"`
		ItemInstruction string `json:"item_instruction"`
		ClearExisting   bool   `json:"clear_existing"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	menuItemID, err := uuid.Parse(body.MenuItemID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_MENU_ITEM", "invalid menu item id", nil)
		return
	}
	var variantID *uuid.UUID
	if body.VariantID != "" {
		parsed, err := uuid.Parse(body.VariantID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_VARIANT", "invalid variant id", nil)
			return
		}
		variantID = &parsed
	}
	cart, err := h.svc.AddCartItem(c.Request.Context(), userID, postgres.AddCartItemInput{
		MenuItemID:      menuItemID,
		VariantID:       variantID,
		Quantity:        body.Quantity,
		ItemInstruction: body.ItemInstruction,
		ClearExisting:   body.ClearExisting,
	})
	if err != nil {
		if errors.Is(err, postgres.ErrCartRestaurantConflict) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "FOOD_CART_RESTAURANT_CONFLICT", "cart contains items from another restaurant", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_CART_ADD_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, cart)
}

func (h *Handler) UpdateCartItem(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	itemID, ok := parseUUIDParam(c, "cartItemId")
	if !ok {
		return
	}
	var body struct {
		Quantity        int    `json:"quantity"`
		ItemInstruction string `json:"item_instruction"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	cart, err := h.svc.UpdateCartItem(c.Request.Context(), userID, itemID, postgres.UpdateCartItemInput{
		Quantity:        body.Quantity,
		ItemInstruction: body.ItemInstruction,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_CART_UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, cart)
}

func (h *Handler) RemoveCartItem(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	itemID, ok := parseUUIDParam(c, "cartItemId")
	if !ok {
		return
	}
	if err := h.svc.RemoveCartItem(c.Request.Context(), userID, itemID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_CART_REMOVE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]string{"status": "removed"})
}

func (h *Handler) ClearCart(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	if err := h.svc.ClearCart(c.Request.Context(), userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_CART_CLEAR_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]string{"status": "cleared"})
}

func (h *Handler) ApplyCoupon(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	cart, err := h.svc.ApplyCoupon(c.Request.Context(), userID, body.Code)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_COUPON_INVALID", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, cart)
}

func (h *Handler) ListAddresses(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	addresses, err := h.svc.ListAddresses(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADDRESSES_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": addresses})
}

func (h *Handler) CreateAddress(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	var body struct {
		Label        string   `json:"label"`
		ReceiverName string   `json:"receiver_name"`
		Phone        string   `json:"phone"`
		AddressLine1 string   `json:"address_line1"`
		AddressLine2 string   `json:"address_line2"`
		Landmark     string   `json:"landmark"`
		City         string   `json:"city"`
		State        string   `json:"state"`
		Country      string   `json:"country"`
		PostalCode   string   `json:"postal_code"`
		Latitude     *float64 `json:"latitude"`
		Longitude    *float64 `json:"longitude"`
		IsDefault    bool     `json:"is_default"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if body.AddressLine1 == "" || body.City == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADDRESS_INVALID", "address_line1 and city are required", nil)
		return
	}
	address, err := h.svc.CreateAddress(c.Request.Context(), userID, postgres.AddressInput{
		Label:        body.Label,
		ReceiverName: body.ReceiverName,
		Phone:        body.Phone,
		AddressLine1: body.AddressLine1,
		AddressLine2: body.AddressLine2,
		Landmark:     body.Landmark,
		City:         body.City,
		State:        body.State,
		Country:      body.Country,
		PostalCode:   body.PostalCode,
		Latitude:     body.Latitude,
		Longitude:    body.Longitude,
		IsDefault:    body.IsDefault,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADDRESS_CREATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, address)
}

func (h *Handler) UpdateAddress(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	addressID, ok := parseUUIDParam(c, "addressId")
	if !ok {
		return
	}
	var body struct {
		Label        string   `json:"label"`
		ReceiverName string   `json:"receiver_name"`
		Phone        string   `json:"phone"`
		AddressLine1 string   `json:"address_line1"`
		AddressLine2 string   `json:"address_line2"`
		Landmark     string   `json:"landmark"`
		City         string   `json:"city"`
		State        string   `json:"state"`
		Country      string   `json:"country"`
		PostalCode   string   `json:"postal_code"`
		Latitude     *float64 `json:"latitude"`
		Longitude    *float64 `json:"longitude"`
		IsDefault    bool     `json:"is_default"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	address, err := h.svc.UpdateAddress(c.Request.Context(), userID, addressID, postgres.AddressInput{
		Label:        body.Label,
		ReceiverName: body.ReceiverName,
		Phone:        body.Phone,
		AddressLine1: body.AddressLine1,
		AddressLine2: body.AddressLine2,
		Landmark:     body.Landmark,
		City:         body.City,
		State:        body.State,
		Country:      body.Country,
		PostalCode:   body.PostalCode,
		Latitude:     body.Latitude,
		Longitude:    body.Longitude,
		IsDefault:    body.IsDefault,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADDRESS_UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, address)
}

func (h *Handler) DeleteAddress(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	addressID, ok := parseUUIDParam(c, "addressId")
	if !ok {
		return
	}
	if err := h.svc.DeleteAddress(c.Request.Context(), userID, addressID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADDRESS_DELETE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) PlaceOrder(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required", nil)
		return
	}
	var body struct {
		AddressID           string `json:"address_id"`
		PaymentMethod       string `json:"payment_method"`
		CouponCode          string `json:"coupon_code"`
		CustomerInstruction string `json:"customer_instruction"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	addressID, err := uuid.Parse(body.AddressID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ADDRESS", "invalid address id", nil)
		return
	}
	order, err := h.svc.PlaceOrder(c.Request.Context(), userID, postgres.PlaceOrderInput{
		AddressID:           addressID,
		PaymentMethod:       body.PaymentMethod,
		CouponCode:          body.CouponCode,
		CustomerInstruction: body.CustomerInstruction,
	}, idempotencyKey)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ORDER_PLACE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, order)
}

func (h *Handler) ListOrders(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	orders, err := h.svc.ListOrders(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ORDERS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": orders})
}

func (h *Handler) GetOrder(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUIDParam(c, "orderId")
	if !ok {
		return
	}
	order, err := h.svc.GetOrder(c.Request.Context(), userID, orderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "FOOD_ORDER_NOT_FOUND", "order not found", nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, order)
}

func (h *Handler) GetOrderTracking(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUIDParam(c, "orderId")
	if !ok {
		return
	}
	tracking, err := h.svc.GetOrderTracking(c.Request.Context(), userID, orderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "FOOD_ORDER_TRACKING_NOT_FOUND", "tracking not found", nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, tracking)
}

func (h *Handler) CreatePaymentIntent(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUIDParam(c, "orderId")
	if !ok {
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required", nil)
		return
	}
	var body struct {
		Method string `json:"method"`
	}
	_ = c.ShouldBindJSON(&body)
	intent, err := h.svc.CreatePaymentIntent(c.Request.Context(), userID, orderID, body.Method, idempotencyKey)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_PAYMENT_INTENT_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, intent)
}

// ConfirmPayment finalises an online/wallet FiGo payment.
//
// P0.1 — every online (Razorpay) confirm MUST carry the Razorpay
// signature triple so the backend can hand it to payments-service for
// HMAC verification + amount check. Wallet confirms pass through the
// existing internal-charge path. Idempotency-Key header makes a
// duplicate confirm a no-op.
func (h *Handler) ConfirmPayment(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUIDParam(c, "orderId")
	if !ok {
		return
	}
	var body struct {
		// Legacy fields — kept for wallet flow; the online path no
		// longer trusts these to mark anything succeeded.
		ProviderPaymentID string `json:"provider_payment_id"`
		ProviderReference string `json:"provider_reference"`
		// Razorpay signature triple — required for ONLINE method.
		RazorpayOrderID   string `json:"razorpay_order_id"`
		RazorpayPaymentID string `json:"razorpay_payment_id"`
		RazorpaySignature string `json:"razorpay_signature"`
		AmountMinor       int64  `json:"amount_minor"`
	}
	_ = c.ShouldBindJSON(&body)
	idemKey := c.GetHeader("Idempotency-Key")
	order, err := h.svc.ConfirmPayment(c.Request.Context(), service.ConfirmPaymentInput{
		UserID:            userID,
		OrderID:           orderID,
		ProviderPaymentID: body.ProviderPaymentID,
		ProviderReference: body.ProviderReference,
		RazorpayOrderID:   body.RazorpayOrderID,
		RazorpayPaymentID: body.RazorpayPaymentID,
		RazorpaySignature: body.RazorpaySignature,
		AmountMinor:       body.AmountMinor,
		IdempotencyKey:    idemKey,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_PAYMENT_CONFIRM_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, order)
}

func (h *Handler) CancelOrder(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUIDParam(c, "orderId")
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	order, err := h.svc.CancelOrder(c.Request.Context(), userID, orderID, body.Reason)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ORDER_CANCEL_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, order)
}

func (h *Handler) RateRestaurant(c *gin.Context) {
	h.rateOrder(c, true)
}

func (h *Handler) RateDelivery(c *gin.Context) {
	h.rateOrder(c, false)
}

func (h *Handler) rateOrder(c *gin.Context, restaurant bool) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUIDParam(c, "orderId")
	if !ok {
		return
	}
	var body struct {
		Rating int    `json:"rating"`
		Review string `json:"review"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	var (
		result map[string]any
		err    error
	)
	if restaurant {
		result, err = h.svc.RateRestaurant(c.Request.Context(), userID, orderID, body.Rating, body.Review)
	} else {
		result, err = h.svc.RateDelivery(c.Request.Context(), userID, orderID, body.Rating, body.Review)
	}
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_RATING_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, result)
}

func (h *Handler) CreatePartnerRestaurant(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	var body struct {
		LegalName      string   `json:"legal_name"`
		DisplayName    string   `json:"display_name"`
		Name           string   `json:"name"`
		Slug           string   `json:"slug"`
		Description    string   `json:"description"`
		Phone          string   `json:"phone"`
		Email          string   `json:"email"`
		AddressLine1   string   `json:"address_line1"`
		AddressLine2   string   `json:"address_line2"`
		City           string   `json:"city"`
		State          string   `json:"state"`
		PostalCode     string   `json:"postal_code"`
		Latitude       *float64 `json:"latitude"`
		Longitude      *float64 `json:"longitude"`
		MinOrderAmount float64  `json:"min_order_amount"`
		PackagingFee   float64  `json:"packaging_fee"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	restaurant, err := h.svc.CreatePartnerRestaurant(c.Request.Context(), userID, postgres.PartnerRestaurantInput{
		LegalName:      body.LegalName,
		DisplayName:    body.DisplayName,
		Name:           body.Name,
		Slug:           body.Slug,
		Description:    body.Description,
		Phone:          body.Phone,
		Email:          body.Email,
		AddressLine1:   body.AddressLine1,
		AddressLine2:   body.AddressLine2,
		City:           body.City,
		State:          body.State,
		PostalCode:     body.PostalCode,
		Latitude:       body.Latitude,
		Longitude:      body.Longitude,
		MinOrderAmount: body.MinOrderAmount,
		PackagingFee:   body.PackagingFee,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_PARTNER_RESTAURANT_CREATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, restaurant)
}

func (h *Handler) ListPartnerRestaurants(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurants, err := h.svc.ListPartnerRestaurants(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_PARTNER_RESTAURANTS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": restaurants})
}

func (h *Handler) GetPartnerRestaurant(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	restaurant, err := h.svc.GetPartnerRestaurant(c.Request.Context(), userID, restaurantID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "FOOD_PARTNER_RESTAURANT_NOT_FOUND", "restaurant not found", nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, restaurant)
}

func (h *Handler) UpdatePartnerRestaurant(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	var body struct {
		LegalName      string   `json:"legal_name"`
		DisplayName    string   `json:"display_name"`
		Name           string   `json:"name"`
		Slug           string   `json:"slug"`
		Description    string   `json:"description"`
		Phone          string   `json:"phone"`
		Email          string   `json:"email"`
		AddressLine1   string   `json:"address_line1"`
		AddressLine2   string   `json:"address_line2"`
		City           string   `json:"city"`
		State          string   `json:"state"`
		PostalCode     string   `json:"postal_code"`
		Latitude       *float64 `json:"latitude"`
		Longitude      *float64 `json:"longitude"`
		MinOrderAmount float64  `json:"min_order_amount"`
		PackagingFee   float64  `json:"packaging_fee"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	restaurant, err := h.svc.UpdatePartnerRestaurant(c.Request.Context(), userID, restaurantID, postgres.PartnerRestaurantInput{
		LegalName:      body.LegalName,
		DisplayName:    body.DisplayName,
		Name:           body.Name,
		Slug:           body.Slug,
		Description:    body.Description,
		Phone:          body.Phone,
		Email:          body.Email,
		AddressLine1:   body.AddressLine1,
		AddressLine2:   body.AddressLine2,
		City:           body.City,
		State:          body.State,
		PostalCode:     body.PostalCode,
		Latitude:       body.Latitude,
		Longitude:      body.Longitude,
		MinOrderAmount: body.MinOrderAmount,
		PackagingFee:   body.PackagingFee,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_PARTNER_RESTAURANT_UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, restaurant)
}

func (h *Handler) AddRestaurantDocument(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	document, err := h.svc.AddRestaurantDocument(c.Request.Context(), userID, restaurantID, body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_RESTAURANT_DOCUMENT_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, document)
}

func (h *Handler) AddRestaurantImage(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	image, err := h.svc.AddRestaurantImage(c.Request.Context(), userID, restaurantID, body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_RESTAURANT_IMAGE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, image)
}

func (h *Handler) ListMenuCategories(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	categories, err := h.svc.ListMenuCategories(c.Request.Context(), userID, restaurantID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_MENU_CATEGORIES_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": categories})
}

func (h *Handler) CreateMenuCategory(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		SortOrder   int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	category, err := h.svc.CreateMenuCategory(c.Request.Context(), userID, restaurantID, postgres.MenuCategoryInput{
		Name: body.Name, Description: body.Description, SortOrder: body.SortOrder,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_MENU_CATEGORY_CREATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, category)
}

func (h *Handler) UpdateMenuCategory(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	categoryID, ok := parseUUIDParam(c, "categoryId")
	if !ok {
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		SortOrder   int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	category, err := h.svc.UpdateMenuCategory(c.Request.Context(), userID, categoryID, postgres.MenuCategoryInput{
		Name:        body.Name,
		Description: body.Description,
		SortOrder:   body.SortOrder,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_MENU_CATEGORY_UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, category)
}

func (h *Handler) DeleteMenuCategory(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	categoryID, ok := parseUUIDParam(c, "categoryId")
	if !ok {
		return
	}
	if err := h.svc.DeleteMenuCategory(c.Request.Context(), userID, categoryID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_MENU_CATEGORY_DELETE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) CreateMenuItem(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	var body struct {
		CategoryID         string   `json:"category_id"`
		Name               string   `json:"name"`
		Description        string   `json:"description"`
		FoodType           string   `json:"food_type"`
		BasePrice          float64  `json:"base_price"`
		DiscountPrice      *float64 `json:"discount_price"`
		ImageURL           string   `json:"image_url"`
		PreparationMinutes int      `json:"preparation_minutes"`
		IsRecommended      bool     `json:"is_recommended"`
		TaxPercentage      float64  `json:"tax_percentage"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	categoryID, err := uuid.Parse(body.CategoryID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CATEGORY", "invalid category id", nil)
		return
	}
	item, err := h.svc.CreateMenuItem(c.Request.Context(), userID, restaurantID, categoryID, postgres.MenuItemInput{
		Name:               body.Name,
		Description:        body.Description,
		FoodType:           body.FoodType,
		BasePrice:          body.BasePrice,
		DiscountPrice:      body.DiscountPrice,
		ImageURL:           body.ImageURL,
		PreparationMinutes: body.PreparationMinutes,
		IsRecommended:      body.IsRecommended,
		TaxPercentage:      body.TaxPercentage,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_MENU_ITEM_CREATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, item)
}

func (h *Handler) UpdateMenuItem(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	itemID, ok := parseUUIDParam(c, "itemId")
	if !ok {
		return
	}
	var body struct {
		Name               string   `json:"name"`
		Description        string   `json:"description"`
		FoodType           string   `json:"food_type"`
		BasePrice          float64  `json:"base_price"`
		DiscountPrice      *float64 `json:"discount_price"`
		ImageURL           string   `json:"image_url"`
		PreparationMinutes int      `json:"preparation_minutes"`
		IsRecommended      bool     `json:"is_recommended"`
		TaxPercentage      float64  `json:"tax_percentage"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	item, err := h.svc.UpdateMenuItem(c.Request.Context(), userID, itemID, postgres.MenuItemInput{
		Name:               body.Name,
		Description:        body.Description,
		FoodType:           body.FoodType,
		BasePrice:          body.BasePrice,
		DiscountPrice:      body.DiscountPrice,
		ImageURL:           body.ImageURL,
		PreparationMinutes: body.PreparationMinutes,
		IsRecommended:      body.IsRecommended,
		TaxPercentage:      body.TaxPercentage,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_MENU_ITEM_UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, item)
}

func (h *Handler) DeleteMenuItem(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	itemID, ok := parseUUIDParam(c, "itemId")
	if !ok {
		return
	}
	if err := h.svc.DeleteMenuItem(c.Request.Context(), userID, itemID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_MENU_ITEM_DELETE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) SetMenuItemAvailability(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	itemID, ok := parseUUIDParam(c, "itemId")
	if !ok {
		return
	}
	var body struct {
		IsAvailable bool `json:"is_available"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.SetMenuItemAvailability(c.Request.Context(), userID, itemID, body.IsAvailable); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_MENU_ITEM_AVAILABILITY_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handler) ListPartnerOrders(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	orders, err := h.svc.ListPartnerOrders(c.Request.Context(), userID, restaurantID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_PARTNER_ORDERS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": orders})
}

func (h *Handler) PartnerRestaurantSettlements(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	settlements, err := h.svc.PartnerRestaurantSettlements(c.Request.Context(), userID, restaurantID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_PARTNER_SETTLEMENTS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": settlements})
}

func (h *Handler) PartnerRestaurantSummary(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	summary, err := h.svc.PartnerRestaurantSummary(c.Request.Context(), userID, restaurantID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_PARTNER_REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, summary)
}

func (h *Handler) PartnerAcceptOrder(c *gin.Context) {
	h.partnerOrderStatus(c, "PREPARING")
}

func (h *Handler) PartnerRejectOrder(c *gin.Context) {
	h.partnerOrderStatus(c, "RESTAURANT_REJECTED")
}

func (h *Handler) PartnerMarkPreparing(c *gin.Context) {
	h.partnerOrderStatus(c, "PREPARING")
}

func (h *Handler) PartnerMarkReady(c *gin.Context) {
	h.partnerOrderStatus(c, "READY_FOR_PICKUP")
}

func (h *Handler) partnerOrderStatus(c *gin.Context, status string) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUIDParam(c, "orderId")
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required", nil)
		return
	}
	order, err := h.svc.PartnerUpdateOrderStatus(c.Request.Context(), userID, orderID, status, body.Reason, idempotencyKey)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_PARTNER_ORDER_STATUS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, order)
}

func (h *Handler) UpsertDeliveryPartner(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	var body struct {
		FullName      string `json:"full_name"`
		Phone         string `json:"phone"`
		Email         string `json:"email"`
		VehicleType   string `json:"vehicle_type"`
		VehicleNumber string `json:"vehicle_number"`
		City          string `json:"city"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	partner, err := h.svc.UpsertDeliveryPartner(c.Request.Context(), userID, postgres.DeliveryPartnerInput{
		FullName: body.FullName, Phone: body.Phone, Email: body.Email,
		VehicleType: body.VehicleType, VehicleNumber: body.VehicleNumber, City: body.City,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_DELIVERY_PROFILE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, partner)
}

func (h *Handler) GetDeliveryPartner(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	partner, err := h.svc.GetDeliveryPartner(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "FOOD_DELIVERY_PROFILE_NOT_FOUND", "delivery partner profile not found", nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, partner)
}

func (h *Handler) AddDeliveryDocument(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	document, err := h.svc.AddDeliveryDocument(c.Request.Context(), userID, body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_DELIVERY_DOCUMENT_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, document)
}

func (h *Handler) SetDeliveryAvailability(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	var body struct {
		IsOnline bool `json:"is_online"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	partner, err := h.svc.SetDeliveryAvailability(c.Request.Context(), userID, body.IsOnline)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_DELIVERY_AVAILABILITY_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, partner)
}

func (h *Handler) ListDeliveryAssignments(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	assignments, err := h.svc.ListDeliveryAssignments(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_DELIVERY_ASSIGNMENTS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": assignments})
}

func (h *Handler) GetCurrentDeliveryAssignment(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	assignment, err := h.svc.GetCurrentDeliveryAssignment(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "FOOD_DELIVERY_CURRENT_NOT_FOUND", "no active assignment", nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, assignment)
}

func (h *Handler) GetAssignmentTracking(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	assignmentID, ok := parseUUIDParam(c, "assignmentId")
	if !ok {
		return
	}
	tracking, err := h.svc.GetAssignmentTracking(c.Request.Context(), userID, assignmentID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "FOOD_ASSIGNMENT_TRACKING_NOT_FOUND", "tracking not found", nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, tracking)
}

func (h *Handler) DeliveryAcceptAssignment(c *gin.Context) {
	h.deliveryAssignmentStatus(c, "ACCEPTED")
}

func (h *Handler) DeliveryRejectAssignment(c *gin.Context) {
	h.deliveryAssignmentStatus(c, "REJECTED")
}

func (h *Handler) DeliveryArrivedRestaurant(c *gin.Context) {
	h.deliveryAssignmentStatus(c, "ARRIVED_AT_RESTAURANT")
}

func (h *Handler) DeliveryPickedUp(c *gin.Context) {
	h.deliveryAssignmentStatus(c, "PICKED_UP")
}

func (h *Handler) DeliveryArrivedCustomer(c *gin.Context) {
	h.deliveryAssignmentStatus(c, "ARRIVED_AT_CUSTOMER")
}

func (h *Handler) DeliveryDelivered(c *gin.Context) {
	h.deliveryAssignmentStatus(c, "DELIVERED")
}

func (h *Handler) deliveryAssignmentStatus(c *gin.Context, status string) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	assignmentID, ok := parseUUIDParam(c, "assignmentId")
	if !ok {
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required", nil)
		return
	}
	assignment, err := h.svc.DeliveryUpdateAssignment(c.Request.Context(), userID, assignmentID, status, idempotencyKey)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_DELIVERY_ASSIGNMENT_STATUS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, assignment)
}

func (h *Handler) UpdateDeliveryLocation(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	var body struct {
		Latitude       float64  `json:"latitude"`
		Longitude      float64  `json:"longitude"`
		AccuracyMeters *float64 `json:"accuracy_meters"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	location, err := h.svc.UpdateDeliveryLocation(c.Request.Context(), userID, body.Latitude, body.Longitude, body.AccuracyMeters)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_DELIVERY_LOCATION_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, location)
}

func (h *Handler) GetDeliveryEarnings(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	earnings, err := h.svc.DeliveryEarnings(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_DELIVERY_EARNINGS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, earnings)
}

func (h *Handler) GetDeliveryHistory(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	assignments, err := h.svc.DeliveryHistory(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_DELIVERY_HISTORY_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": assignments})
}

func (h *Handler) AdminDashboard(c *gin.Context) {
	if _, ok := h.currentUserID(c); !ok {
		return
	}
	dashboard, err := h.svc.AdminDashboard(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_DASHBOARD_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, dashboard)
}

func (h *Handler) AdminPendingRestaurants(c *gin.Context) {
	if _, ok := h.currentUserID(c); !ok {
		return
	}
	restaurants, err := h.svc.AdminPendingRestaurants(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_RESTAURANTS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": restaurants})
}

func (h *Handler) AdminApproveRestaurant(c *gin.Context) {
	h.adminRestaurantReview(c, true)
}

func (h *Handler) AdminRejectRestaurant(c *gin.Context) {
	h.adminRestaurantReview(c, false)
}

func (h *Handler) adminRestaurantReview(c *gin.Context, approve bool) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := h.svc.AdminApproveRestaurant(c.Request.Context(), adminID, restaurantID, approve, body.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_RESTAURANT_REVIEW_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handler) AdminSetRestaurantStatus(c *gin.Context) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	restaurantID, ok := parseUUIDParam(c, "restaurantId")
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.AdminSetRestaurantStatus(c.Request.Context(), adminID, restaurantID, body.Status, body.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_RESTAURANT_STATUS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handler) AdminPendingDeliveryPartners(c *gin.Context) {
	if _, ok := h.currentUserID(c); !ok {
		return
	}
	partners, err := h.svc.AdminPendingDeliveryPartners(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_DELIVERY_PARTNERS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": partners})
}

func (h *Handler) AdminApproveDeliveryPartner(c *gin.Context) {
	h.adminDeliveryPartnerReview(c, true)
}

func (h *Handler) AdminRejectDeliveryPartner(c *gin.Context) {
	h.adminDeliveryPartnerReview(c, false)
}

func (h *Handler) adminDeliveryPartnerReview(c *gin.Context, approve bool) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	partnerID, ok := parseUUIDParam(c, "partnerId")
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := h.svc.AdminApproveDeliveryPartner(c.Request.Context(), adminID, partnerID, approve, body.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_DELIVERY_PARTNER_REVIEW_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handler) AdminSetDeliveryPartnerStatus(c *gin.Context) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	partnerID, ok := parseUUIDParam(c, "partnerId")
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.AdminSetDeliveryPartnerStatus(c.Request.Context(), adminID, partnerID, body.Status, body.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_DELIVERY_STATUS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handler) AdminListOrders(c *gin.Context) {
	if _, ok := h.currentUserID(c); !ok {
		return
	}
	page := paginationFromQuery(c)
	orders, err := h.svc.AdminListOrders(c.Request.Context(), page)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_ORDERS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, paginatedResponse(orders, page))
}

func (h *Handler) AdminGetOrder(c *gin.Context) {
	if _, ok := h.currentUserID(c); !ok {
		return
	}
	orderID, ok := parseUUIDParam(c, "orderId")
	if !ok {
		return
	}
	order, err := h.svc.AdminGetOrder(c.Request.Context(), orderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "FOOD_ADMIN_ORDER_NOT_FOUND", "order not found", nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, order)
}

func (h *Handler) AdminCancelOrder(c *gin.Context) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUIDParam(c, "orderId")
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	order, err := h.svc.AdminCancelOrder(c.Request.Context(), adminID, orderID, body.Reason)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_ORDER_CANCEL_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, order)
}

func (h *Handler) AdminRefundOrder(c *gin.Context) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUIDParam(c, "orderId")
	if !ok {
		return
	}
	var body struct {
		Reason string  `json:"reason"`
		Amount float64 `json:"amount"`
	}
	_ = c.ShouldBindJSON(&body)
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required", nil)
		return
	}
	refund, err := h.svc.AdminRefundOrder(c.Request.Context(), adminID, orderID, body.Reason, body.Amount, idempotencyKey)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_ORDER_REFUND_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, refund)
}

func (h *Handler) AdminListCoupons(c *gin.Context) {
	if _, ok := h.currentUserID(c); !ok {
		return
	}
	coupons, err := h.svc.AdminListCoupons(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_COUPONS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": coupons})
}

func (h *Handler) AdminCreateCoupon(c *gin.Context) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	coupon, err := h.svc.AdminCreateCoupon(c.Request.Context(), adminID, body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_COUPON_CREATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, coupon)
}

func (h *Handler) AdminUpdateCoupon(c *gin.Context) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	couponID, ok := parseUUIDParam(c, "couponId")
	if !ok {
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	coupon, err := h.svc.AdminUpdateCoupon(c.Request.Context(), adminID, couponID, body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_COUPON_UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, coupon)
}

func (h *Handler) AdminListServiceAreas(c *gin.Context) {
	if _, ok := h.currentUserID(c); !ok {
		return
	}
	areas, err := h.svc.AdminListServiceAreas(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_SERVICE_AREAS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, map[string]any{"items": areas})
}

func (h *Handler) AdminCreateServiceArea(c *gin.Context) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	area, err := h.svc.AdminCreateServiceArea(c.Request.Context(), adminID, body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_SERVICE_AREA_CREATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, area)
}

func (h *Handler) AdminUpdateServiceArea(c *gin.Context) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	areaID, ok := parseUUIDParam(c, "areaId")
	if !ok {
		return
	}
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	area, err := h.svc.AdminUpdateServiceArea(c.Request.Context(), adminID, areaID, body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_SERVICE_AREA_UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, area)
}

func (h *Handler) AdminListRestaurantSettlements(c *gin.Context) {
	if _, ok := h.currentUserID(c); !ok {
		return
	}
	page := paginationFromQuery(c)
	settlements, err := h.svc.AdminListRestaurantSettlements(c.Request.Context(), page)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_SETTLEMENTS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, paginatedResponse(settlements, page))
}

func (h *Handler) AdminGenerateSettlements(c *gin.Context) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required", nil)
		return
	}
	var body struct {
		PeriodStart       string `json:"period_start"`
		PeriodEnd         string `json:"period_end"`
		RestaurantID      string `json:"restaurant_id"`
		DeliveryPartnerID string `json:"delivery_partner_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	input := postgres.SettlementGenerateInput{
		PeriodStart:    body.PeriodStart,
		PeriodEnd:      body.PeriodEnd,
		IdempotencyKey: idempotencyKey,
	}
	if body.RestaurantID != "" {
		id, err := uuid.Parse(body.RestaurantID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_RESTAURANT", "invalid restaurant id", nil)
			return
		}
		input.RestaurantID = &id
	}
	if body.DeliveryPartnerID != "" {
		id, err := uuid.Parse(body.DeliveryPartnerID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_DELIVERY_PARTNER", "invalid delivery partner id", nil)
			return
		}
		input.DeliveryPartnerID = &id
	}
	result, err := h.svc.AdminGenerateSettlements(c.Request.Context(), adminID, input)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_SETTLEMENT_GENERATE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, result)
}

func (h *Handler) AdminMarkRestaurantSettlementPaid(c *gin.Context) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	settlementID, ok := parseUUIDParam(c, "settlementId")
	if !ok {
		return
	}
	var body struct {
		Reference string `json:"reference"`
	}
	_ = c.ShouldBindJSON(&body)
	settlement, err := h.svc.AdminMarkRestaurantSettlementPaid(c.Request.Context(), adminID, settlementID, body.Reference)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_SETTLEMENT_PAID_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, settlement)
}

func (h *Handler) AdminListDeliverySettlements(c *gin.Context) {
	if _, ok := h.currentUserID(c); !ok {
		return
	}
	page := paginationFromQuery(c)
	settlements, err := h.svc.AdminListDeliverySettlements(c.Request.Context(), page)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_DELIVERY_SETTLEMENTS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, paginatedResponse(settlements, page))
}

func (h *Handler) AdminMarkDeliverySettlementPaid(c *gin.Context) {
	adminID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	settlementID, ok := parseUUIDParam(c, "settlementId")
	if !ok {
		return
	}
	var body struct {
		Reference string `json:"reference"`
	}
	_ = c.ShouldBindJSON(&body)
	settlement, err := h.svc.AdminMarkDeliverySettlementPaid(c.Request.Context(), adminID, settlementID, body.Reference)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ADMIN_DELIVERY_SETTLEMENT_PAY_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, settlement)
}

func (h *Handler) AdminAuditLogs(c *gin.Context) {
	if _, ok := h.currentUserID(c); !ok {
		return
	}
	page := paginationFromQuery(c)
	logs, err := h.svc.AdminAuditLogs(c.Request.Context(), page)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_AUDIT_LOGS_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, paginatedResponse(logs, page))
}

func (h *Handler) AdminOrderReport(c *gin.Context) {
	if _, ok := h.currentUserID(c); !ok {
		return
	}
	report, err := h.svc.AdminOrderReport(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_ORDER_REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, report)
}

func (h *Handler) AdminRevenueReport(c *gin.Context) {
	if _, ok := h.currentUserID(c); !ok {
		return
	}
	report, err := h.svc.AdminRevenueReport(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_REVENUE_REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, report)
}

func parseUUIDParam(c *gin.Context, name string) (uuid.UUID, bool) {
	value, err := uuid.Parse(c.Param(name))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid "+name, nil)
		return uuid.Nil, false
	}
	return value, true
}

func (h *Handler) currentUserID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-Id")
	if raw == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil)
		return uuid.Nil, false
	}
	userID, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_USER_ID", "invalid user id", nil)
		return uuid.Nil, false
	}
	return userID, true
}

func (h *Handler) requireAuthenticated() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := h.currentUserID(c); !ok {
			c.Abort()
			return
		}
		c.Next()
	}
}

func (h *Handler) requireAdminScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := h.currentUserID(c); !ok {
			c.Abort()
			return
		}
		if !hasScope(c.GetHeader("X-Scopes"), "admin") && !hasScope(c.GetHeader("X-Scopes"), "superadmin") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "admin scope required", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

func hasScope(scopes, target string) bool {
	for _, scope := range strings.Fields(scopes) {
		if scope == target {
			return true
		}
	}
	return false
}

func parseLimit(raw string) int {
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 50
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func parseOffset(raw string) int {
	offset, err := strconv.Atoi(raw)
	if err != nil || offset < 0 {
		return 0
	}
	return offset
}

func paginationFromQuery(c *gin.Context) postgres.Pagination {
	return postgres.Pagination{
		Limit:  parseLimit(c.DefaultQuery("limit", "50")),
		Offset: parseOffset(c.DefaultQuery("offset", "0")),
	}
}

func paginatedResponse(items any, page postgres.Pagination) map[string]any {
	return map[string]any{
		"items": items,
		"pagination": map[string]any{
			"limit":  page.Limit,
			"offset": page.Offset,
		},
	}
}

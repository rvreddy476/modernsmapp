package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
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
	v1 := r.Group("/v1/monetization")
	{
		// Wallet
		v1.GET("/wallet", h.GetWallet)

		// Transactions
		v1.GET("/transactions", h.GetTransactions)

		// Payout Methods
		v1.POST("/payout-methods", h.AddPayoutMethod)
		v1.DELETE("/payout-methods/:id", h.RemovePayoutMethod)
		v1.GET("/payout-methods", h.GetPayoutMethods)

		// Payouts
		v1.POST("/payouts", h.RequestPayout)
		v1.GET("/payouts", h.GetPayouts)

		// Tax Info
		v1.POST("/tax-info", h.SaveTaxInfo)

		// Creator Tiers
		v1.GET("/tiers", h.GetMyTiers)
		v1.POST("/tiers", h.CreateTier)
		v1.PATCH("/tiers/:id", h.UpdateTier)

		// Subscriptions
		v1.POST("/subscribe/:creatorId", h.Subscribe)
		v1.DELETE("/subscribe/:creatorId", h.Unsubscribe)

		// Dashboard
		v1.GET("/dashboard", h.GetDashboard)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	userIDStr := c.GetHeader("X-User-Id")
	if userIDStr == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil, nil)
		return uuid.Nil, false
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return uuid.Nil, false
	}
	return userID, true
}

// ---------------------------------------------------------------------------
// Wallet
// ---------------------------------------------------------------------------

func (h *Handler) GetWallet(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	wallet, err := h.svc.GetWallet(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, wallet, nil)
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

func (h *Handler) GetTransactions(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	cursor := c.Query("cursor")
	limit := 20
	if limitStr := c.Query("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	txns, err := h.svc.GetTransactions(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if txns == nil {
		txns = []postgres.Transaction{}
	}

	var meta *api.Meta
	if len(txns) == limit {
		meta = &api.Meta{NextCursor: txns[len(txns)-1].CreatedAt.Format(time.RFC3339Nano)}
	}

	api.JSON(c.Writer, http.StatusOK, txns, meta)
}

// ---------------------------------------------------------------------------
// Payout Methods
// ---------------------------------------------------------------------------

type AddPayoutMethodRequest struct {
	MethodType       string `json:"method_type" binding:"required"`
	DetailsEncrypted string `json:"details_encrypted" binding:"required"`
	IsDefault        bool   `json:"is_default"`
}

func (h *Handler) AddPayoutMethod(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req AddPayoutMethodRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	m := &postgres.PayoutMethod{
		UserID:           userID,
		MethodType:       req.MethodType,
		DetailsEncrypted: req.DetailsEncrypted,
		IsDefault:        req.IsDefault,
	}

	if err := h.svc.AddPayoutMethod(c.Request.Context(), m); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, m, nil)
}

func (h *Handler) RemovePayoutMethod(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	methodID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid payout method ID", nil, nil)
		return
	}

	if err := h.svc.RemovePayoutMethod(c.Request.Context(), userID, methodID); err != nil {
		if err.Error() == "PAYOUT_METHOD_NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Payout method not found", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) GetPayoutMethods(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	methods, err := h.svc.GetPayoutMethods(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if methods == nil {
		methods = []postgres.PayoutMethod{}
	}

	api.JSON(c.Writer, http.StatusOK, methods, nil)
}

// ---------------------------------------------------------------------------
// Payouts
// ---------------------------------------------------------------------------

type RequestPayoutRequest struct {
	Amount         float64 `json:"amount" binding:"required"`
	PayoutMethodID string  `json:"payout_method_id" binding:"required"`
}

func (h *Handler) RequestPayout(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req RequestPayoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	payoutMethodID, err := uuid.Parse(req.PayoutMethodID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid payout method ID", nil, nil)
		return
	}

	txn, err := h.svc.RequestPayout(c.Request.Context(), userID, req.Amount, payoutMethodID)
	if err != nil {
		switch err.Error() {
		case "INVALID_AMOUNT":
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_AMOUNT", "Amount must be greater than zero", nil, nil)
		case "WALLET_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "WALLET_NOT_FOUND", "Wallet not found", nil, nil)
		case "WALLET_FROZEN":
			api.Error(c.Writer, http.StatusForbidden, "WALLET_FROZEN", "Wallet is frozen", nil, nil)
		case "INSUFFICIENT_BALANCE":
			api.Error(c.Writer, http.StatusBadRequest, "INSUFFICIENT_BALANCE", "Insufficient balance for payout", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, txn, nil)
}

func (h *Handler) GetPayouts(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	cursor := c.Query("cursor")
	limit := 20
	if limitStr := c.Query("limit"); limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	txns, err := h.svc.GetPayouts(c.Request.Context(), userID, cursor, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if txns == nil {
		txns = []postgres.Transaction{}
	}

	var meta *api.Meta
	if len(txns) == limit {
		meta = &api.Meta{NextCursor: txns[len(txns)-1].CreatedAt.Format(time.RFC3339Nano)}
	}

	api.JSON(c.Writer, http.StatusOK, txns, meta)
}

// ---------------------------------------------------------------------------
// Tax Info
// ---------------------------------------------------------------------------

type SaveTaxInfoRequest struct {
	Country          string `json:"country" binding:"required"`
	TaxDataEncrypted string `json:"tax_data_encrypted" binding:"required"`
}

func (h *Handler) SaveTaxInfo(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req SaveTaxInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	t := &postgres.TaxInfo{
		UserID:             userID,
		Country:            req.Country,
		TaxDataEncrypted:   req.TaxDataEncrypted,
		VerificationStatus: "pending",
	}

	if err := h.svc.SaveTaxInfo(c.Request.Context(), t); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, t, nil)
}

// ---------------------------------------------------------------------------
// Creator Tiers
// ---------------------------------------------------------------------------

type CreateTierRequest struct {
	Name     string          `json:"name" binding:"required"`
	Price    float64         `json:"price" binding:"required"`
	Currency string          `json:"currency"`
	Perks    json.RawMessage `json:"perks"`
}

func (h *Handler) GetMyTiers(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	tiers, err := h.svc.GetCreatorTiers(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if tiers == nil {
		tiers = []postgres.CreatorTier{}
	}

	api.JSON(c.Writer, http.StatusOK, tiers, nil)
}

func (h *Handler) CreateTier(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req CreateTierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	currency := req.Currency
	if currency == "" {
		currency = "INR"
	}

	t := &postgres.CreatorTier{
		CreatorID: userID,
		Name:      req.Name,
		Price:     req.Price,
		Currency:  currency,
		Perks:     req.Perks,
		IsActive:  true,
	}

	if err := h.svc.CreateTier(c.Request.Context(), t); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, t, nil)
}

type UpdateTierRequest struct {
	Name     string          `json:"name"`
	Price    float64         `json:"price"`
	Currency string          `json:"currency"`
	Perks    json.RawMessage `json:"perks"`
	IsActive *bool           `json:"is_active"`
}

func (h *Handler) UpdateTier(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	tierID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid tier ID", nil, nil)
		return
	}

	var req UpdateTierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	t := &postgres.CreatorTier{
		ID:        tierID,
		CreatorID: userID,
		Name:      req.Name,
		Price:     req.Price,
		Currency:  req.Currency,
		Perks:     req.Perks,
		IsActive:  isActive,
	}

	if err := h.svc.UpdateTier(c.Request.Context(), t); err != nil {
		if err.Error() == "TIER_NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Tier not found or not owned by you", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

// ---------------------------------------------------------------------------
// Subscriptions
// ---------------------------------------------------------------------------

type SubscribeRequest struct {
	TierID string `json:"tier_id" binding:"required"`
}

func (h *Handler) Subscribe(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	creatorID, err := uuid.Parse(c.Param("creatorId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid creator ID", nil, nil)
		return
	}

	var req SubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	tierID, err := uuid.Parse(req.TierID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid tier ID", nil, nil)
		return
	}

	sub, err := h.svc.Subscribe(c.Request.Context(), userID, creatorID, tierID)
	if err != nil {
		switch err.Error() {
		case "CANNOT_SUBSCRIBE_TO_SELF":
			api.Error(c.Writer, http.StatusBadRequest, "CANNOT_SUBSCRIBE_TO_SELF", "Cannot subscribe to yourself", nil, nil)
		case "TIER_NOT_FOUND":
			api.Error(c.Writer, http.StatusNotFound, "TIER_NOT_FOUND", "Tier not found", nil, nil)
		case "TIER_INACTIVE":
			api.Error(c.Writer, http.StatusBadRequest, "TIER_INACTIVE", "Tier is not active", nil, nil)
		case "TIER_CREATOR_MISMATCH":
			api.Error(c.Writer, http.StatusBadRequest, "TIER_CREATOR_MISMATCH", "Tier does not belong to this creator", nil, nil)
		case "ALREADY_SUBSCRIBED":
			api.Error(c.Writer, http.StatusConflict, "ALREADY_SUBSCRIBED", "Already subscribed to this creator", nil, nil)
		case "INSUFFICIENT_BALANCE_OR_FROZEN":
			api.Error(c.Writer, http.StatusBadRequest, "INSUFFICIENT_BALANCE", "Insufficient balance or wallet is frozen", nil, nil)
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, sub, nil)
}

func (h *Handler) Unsubscribe(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	creatorID, err := uuid.Parse(c.Param("creatorId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid creator ID", nil, nil)
		return
	}

	if err := h.svc.Unsubscribe(c.Request.Context(), userID, creatorID); err != nil {
		if err.Error() == "SUBSCRIPTION_NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Active subscription not found", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unsubscribed"}, nil)
}

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

func (h *Handler) GetDashboard(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	dashboard, err := h.svc.GetDashboard(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, dashboard, nil)
}

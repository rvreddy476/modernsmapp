package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/store/postgres"
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
	v1 := r.Group("/v1/monetization")
	{
		// Creator earnings ledger (canonical name as of 2026-04-30, Phase 2 §D4).
		v1.GET("/creator-ledger", h.GetCreatorLedger)
		// Deprecated alias — returns the same payload but emits a Deprecation
		// header and a `_deprecated_use` JSON field. Will be removed after
		// 2026-10-30. See PHASE_2_DECISIONS.md §D4.
		v1.GET("/wallet", h.GetWalletDeprecated)

		// Transactions
		v1.GET("/transactions", h.GetTransactions)
		v1.POST("/internal/charge-and-credit", h.InternalChargeAndCredit)

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

		// Public: list a specific creator's active tiers (used by fans
		// when picking a tier on a creator's profile).
		v1.GET("/creators/:creatorId/tiers", h.GetCreatorTiersPublic)

		// Subscriptions
		v1.POST("/subscribe/:creatorId", h.Subscribe)
		v1.DELETE("/subscribe/:creatorId", h.Unsubscribe)

		// Dashboard
		v1.GET("/dashboard", h.GetDashboard)

		// Affiliate links
		v1.POST("/affiliate/links", h.CreateAffiliateLink)
		v1.GET("/affiliate/links", h.ListAffiliateLinks)
		v1.GET("/affiliate/links/:linkId", h.GetAffiliateLinkByID)
		v1.GET("/affiliate/:linkCode", h.GetAffiliateLinkByCode)
		v1.GET("/affiliate/conversions", h.ListAffiliateConversions)

		// Fundraisers
		v1.POST("/fundraisers", h.CreateFundraiser)
		v1.GET("/fundraisers", h.ListActiveFundraisers)
		v1.GET("/fundraisers/mine", h.ListMyFundraisers)
		v1.GET("/fundraisers/:fundraiserId", h.GetFundraiser)
		v1.PATCH("/fundraisers/:fundraiserId/pause", h.PauseFundraiser)
		v1.POST("/fundraisers/:fundraiserId/donate", h.Donate)
		v1.GET("/fundraisers/:fundraiserId/donations", h.GetDonationsByFundraiser)

		// Disputes
		v1.POST("/disputes", h.CreateDispute)
		v1.GET("/disputes", h.ListUserDisputes)
		v1.GET("/disputes/:id", h.GetDisputeByID)
		v1.PATCH("/disputes/:id", h.ResolveDisputeAdmin)

		// Refunds (admin)
		v1.POST("/refunds", h.ProcessRefund)

		// Fraud reviews (admin)
		v1.GET("/admin/fraud-reviews", h.ListPendingFraudReviews)
		v1.PATCH("/admin/fraud-reviews/:id", h.ResolveFraudReviewAdmin)

		// Admin wallet operations
		v1.POST("/admin/wallet/:userId/freeze", h.FreezeWallet)
		v1.POST("/admin/wallet/:userId/unfreeze", h.UnfreezeWallet)
		v1.POST("/admin/wallet/:userId/rebuild", h.RebuildWallet)

		// Subscription lifecycle
		v1.POST("/subscriptions/:id/pause", h.PauseSubscription)
		v1.POST("/subscriptions/:id/resume", h.ResumeSubscription)
		v1.POST("/subscriptions/:id/cancel", h.CancelSubscription)
		v1.POST("/subscriptions/:id/upgrade", h.UpgradeSubscription)
		v1.GET("/subscriptions/:id/events", h.GetSubscriptionEvents)

		// Tax profile & compliance
		v1.POST("/tax-profile", h.SaveTaxProfile)
		v1.GET("/tax-profile", h.GetTaxProfile)
		v1.GET("/tds-summary/:year", h.GetTDSSummary)
		v1.GET("/invoices", h.ListInvoices)

		// Payout statements
		v1.GET("/payout-statements", h.ListPayoutStatements)
		v1.GET("/payout-statements/:id", h.GetPayoutStatement)

		// Payout webhooks (no auth — signature verified externally)
		v1.POST("/webhooks/payout", h.HandlePayoutWebhook)

		// Entitlement checks (Tier 3c — used by post-service / clients)
		v1.GET("/entitlements", h.CheckEntitlement)
		v1.POST("/entitlements/check", h.BulkCheckEntitlements)

		// Tips / Super Chat (Tier 3d)
		v1.POST("/tips", h.SendTip)
		v1.GET("/tips/sent", h.ListSentTips)
		v1.GET("/tips/received", h.ListReceivedTips)
		v1.GET("/tips/post/:postId", h.ListTipsForPost)

		// Creator Fund (Tier 3a)
		cf := v1.Group("/creator-fund")
		{
			cf.GET("/status", h.GetCreatorFundStatus)
			cf.POST("/apply", h.ApplyCreatorFund)
			cf.GET("/earnings", h.GetCreatorFundEarnings)
			cf.GET("/rates", h.ListCreatorFundRates)
		}
		admin := v1.Group("/admin/creator-fund")
		{
			admin.GET("/rates", h.ListCreatorFundRatesAdmin)
			admin.PUT("/rates", h.SetCreatorFundRate)
			admin.POST("/:userId/suspend", h.SuspendCreatorFund)
			admin.POST("/:userId/unsuspend", h.UnsuspendCreatorFund)
			admin.POST("/settle", h.ForceSettleCreatorFund)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	userIDStr := c.GetHeader("X-User-Id")
	if userIDStr == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil)
		return uuid.Nil, false
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return uuid.Nil, false
	}
	return userID, true
}

// ---------------------------------------------------------------------------
// Creator earnings ledger (formerly "wallet")
// ---------------------------------------------------------------------------
//
// The on-disk table was renamed from `wallets` to `creator_ledger` on
// 2026-04-30 (Phase 2 §D4) to remove the ambiguity with the upcoming
// consumer wallet (lives in wallet-service). The HTTP route is renamed
// the same day:
//
//   - canonical:  GET /v1/monetization/creator-ledger  (handler: GetCreatorLedger)
//   - deprecated: GET /v1/monetization/wallet          (handler: GetWalletDeprecated)
//
// The deprecated route stays alive until 2026-10-30 so already-deployed
// clients keep working. It returns the same payload, but adds a
// Deprecation header and an inline `_deprecated_use` hint, and logs a
// warning per request for ops visibility.

// GetCreatorLedger returns the creator-earnings ledger row for the
// caller (auto-creating it on first access).
func (h *Handler) GetCreatorLedger(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	wallet, err := h.svc.GetWallet(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, wallet, nil)
}

// GetWalletDeprecated serves GET /v1/monetization/wallet for backwards
// compatibility. It returns the same payload as GetCreatorLedger plus a
// `_deprecated_use` field, and emits a Deprecation header so any caller
// running with strict HTTP middleware can surface the warning.
func (h *Handler) GetWalletDeprecated(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	wallet, err := h.svc.GetWallet(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// RFC 8594 / draft-ietf-httpapi-deprecation-header style hints.
	c.Writer.Header().Set("Deprecation", "true")
	c.Writer.Header().Set("Sunset", "Fri, 30 Oct 2026 00:00:00 GMT")
	c.Writer.Header().Set("Link", "</v1/monetization/creator-ledger>; rel=\"successor-version\"")

	slog.WarnContext(c.Request.Context(),
		"deprecated route called: /v1/monetization/wallet",
		"successor", "/v1/monetization/creator-ledger",
		"user_id", userID,
		"sunset", "2026-10-30",
	)

	// Return the wallet payload plus an explicit deprecation hint. We
	// build a flat map so existing clients that read fields like
	// `balance_paise` keep working.
	body := map[string]interface{}{
		"_deprecated_use": "/v1/monetization/creator-ledger",
		"_deprecated_since": "2026-04-30",
		"_sunset_after":     "2026-10-30",
	}
	if wallet != nil {
		body["user_id"] = wallet.UserID
		body["balance_paise"] = wallet.BalancePaise
		body["lifetime_earnings_paise"] = wallet.LifetimeEarningsPaise
		body["pending_payout_paise"] = wallet.PendingPayoutPaise
		body["currency"] = wallet.Currency
		body["is_frozen"] = wallet.IsFrozen
		body["created_at"] = wallet.CreatedAt
		body["updated_at"] = wallet.UpdatedAt
	}
	api.JSON(c.Writer, http.StatusOK, body, nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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

func (h *Handler) InternalChargeAndCredit(c *gin.Context) {
	var req struct {
		FromUserID    string `json:"from_user_id" binding:"required"`
		ToUserID      string `json:"to_user_id" binding:"required"`
		AmountPaise   int64  `json:"amount_paise" binding:"required"`
		Description   string `json:"description"`
		ReferenceID   string `json:"reference_id"`
		ReferenceType string `json:"reference_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	fromUserID, err := uuid.Parse(req.FromUserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_FROM_USER", "invalid from_user_id", nil)
		return
	}
	toUserID, err := uuid.Parse(req.ToUserID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_TO_USER", "invalid to_user_id", nil)
		return
	}
	description := req.Description
	if description == "" {
		description = "Internal wallet payment"
	}
	if req.ReferenceType != "" || req.ReferenceID != "" {
		description = description + " (" + req.ReferenceType + ":" + req.ReferenceID + ")"
	}
	if err := h.svc.ChargeAndCredit(c.Request.Context(), fromUserID, toUserID, req.AmountPaise, description); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "WALLET_CHARGE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"status":       "completed",
		"from_user_id": fromUserID,
		"to_user_id":   toUserID,
		"amount_paise": req.AmountPaise,
	}, nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	m := &postgres.PayoutMethod{
		UserID:           userID,
		MethodType:       req.MethodType,
		DetailsEncrypted: req.DetailsEncrypted,
		IsDefault:        req.IsDefault,
	}

	if err := h.svc.AddPayoutMethod(c.Request.Context(), m); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid payout method ID", nil)
		return
	}

	if err := h.svc.RemovePayoutMethod(c.Request.Context(), userID, methodID); err != nil {
		if err.Error() == "PAYOUT_METHOD_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Payout method not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
	AmountPaise    int64  `json:"amount_paise" binding:"required"`
	PayoutMethodID string `json:"payout_method_id" binding:"required"`
}

func (h *Handler) RequestPayout(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req RequestPayoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	payoutMethodID, err := uuid.Parse(req.PayoutMethodID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid payout method ID", nil)
		return
	}

	txn, err := h.svc.RequestPayout(c.Request.Context(), userID, req.AmountPaise, payoutMethodID)
	if err != nil {
		switch err.Error() {
		case "INVALID_AMOUNT":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_AMOUNT", "Amount must be greater than zero", nil)
		case "WALLET_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "WALLET_NOT_FOUND", "Wallet not found", nil)
		case "WALLET_FROZEN":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "WALLET_FROZEN", "Wallet is frozen", nil)
		case "INSUFFICIENT_BALANCE":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INSUFFICIENT_BALANCE", "Insufficient balance for payout", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	t := &postgres.TaxInfo{
		UserID:             userID,
		Country:            req.Country,
		TaxDataEncrypted:   req.TaxDataEncrypted,
		VerificationStatus: "pending",
	}

	if err := h.svc.SaveTaxInfo(c.Request.Context(), t); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, t, nil)
}

// ---------------------------------------------------------------------------
// Creator Tiers
// ---------------------------------------------------------------------------

type CreateTierRequest struct {
	Name       string          `json:"name" binding:"required"`
	PricePaise int64           `json:"price_paise" binding:"required"`
	Currency   string          `json:"currency"`
	Perks      json.RawMessage `json:"perks"`
}

func (h *Handler) GetMyTiers(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	tiers, err := h.svc.GetCreatorTiers(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if tiers == nil {
		tiers = []postgres.CreatorTier{}
	}

	api.JSON(c.Writer, http.StatusOK, tiers, nil)
}

// GetCreatorTiersPublic returns the active tiers of any creator. No
// auth header required — this is what powers the fan-side tier picker
// on a creator's profile. Inactive tiers are filtered out so a fan
// can't subscribe to a sunset tier.
func (h *Handler) GetCreatorTiersPublic(c *gin.Context) {
	creatorID, err := uuid.Parse(c.Param("creatorId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid creator ID", nil)
		return
	}
	tiers, err := h.svc.GetCreatorTiers(c.Request.Context(), creatorID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]postgres.CreatorTier, 0, len(tiers))
	for _, t := range tiers {
		if t.IsActive {
			out = append(out, t)
		}
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

func (h *Handler) CreateTier(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req CreateTierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	currency := req.Currency
	if currency == "" {
		currency = "INR"
	}

	t := &postgres.CreatorTier{
		CreatorID:  userID,
		Name:       req.Name,
		PricePaise: req.PricePaise,
		Currency:   currency,
		Perks:      req.Perks,
		IsActive:   true,
	}

	if err := h.svc.CreateTier(c.Request.Context(), t); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, t, nil)
}

type UpdateTierRequest struct {
	Name       string          `json:"name"`
	PricePaise int64           `json:"price_paise"`
	Currency   string          `json:"currency"`
	Perks      json.RawMessage `json:"perks"`
	IsActive   *bool           `json:"is_active"`
}

func (h *Handler) UpdateTier(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	tierID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid tier ID", nil)
		return
	}

	var req UpdateTierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	t := &postgres.CreatorTier{
		ID:         tierID,
		CreatorID:  userID,
		Name:       req.Name,
		PricePaise: req.PricePaise,
		Currency:   req.Currency,
		Perks:      req.Perks,
		IsActive:   isActive,
	}

	if err := h.svc.UpdateTier(c.Request.Context(), t); err != nil {
		if err.Error() == "TIER_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Tier not found or not owned by you", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid creator ID", nil)
		return
	}

	var req SubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	tierID, err := uuid.Parse(req.TierID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid tier ID", nil)
		return
	}

	idempotencyKey := c.GetHeader("X-Idempotency-Key")

	sub, err := h.svc.Subscribe(c.Request.Context(), userID, creatorID, tierID, idempotencyKey)
	if err != nil {
		switch err.Error() {
		case "CANNOT_SUBSCRIBE_TO_SELF":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CANNOT_SUBSCRIBE_TO_SELF", "Cannot subscribe to yourself", nil)
		case "TIER_NOT_FOUND":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "TIER_NOT_FOUND", "Tier not found", nil)
		case "TIER_INACTIVE":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "TIER_INACTIVE", "Tier is not active", nil)
		case "TIER_CREATOR_MISMATCH":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "TIER_CREATOR_MISMATCH", "Tier does not belong to this creator", nil)
		case "ALREADY_SUBSCRIBED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "ALREADY_SUBSCRIBED", "Already subscribed to this creator", nil)
		case "INSUFFICIENT_BALANCE_OR_FROZEN":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INSUFFICIENT_BALANCE", "Insufficient balance or wallet is frozen", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid creator ID", nil)
		return
	}

	if err := h.svc.Unsubscribe(c.Request.Context(), userID, creatorID); err != nil {
		if err.Error() == "SUBSCRIPTION_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Active subscription not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, dashboard, nil)
}

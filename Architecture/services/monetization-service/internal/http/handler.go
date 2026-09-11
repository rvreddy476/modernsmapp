package http

import (
	"crypto/hmac"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc            *service.Service
	internalKey    string
	writesEnabled  bool
	payoutsEnabled bool
	// maintenance mirrors MONETIZATION_MAINTENANCE (reviewer correction
	// B, 12 Sep 2026): the admin routes are the only open writes, every
	// other financial write answers 503 MAINTENANCE, and every admin
	// route must carry the internal service key as well as the scope
	// header. It wins over writesEnabled.
	maintenance bool
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

// WithWritesEnabled controls the financial launch boundary. It defaults to
// false: Module 6 exposes only the authoritative creator-ledger reads. Money
// movement is enabled later only after its separate KYC/provider/reconciliation
// checkpoint.
func (h *Handler) WithWritesEnabled(enabled bool) *Handler {
	h.writesEnabled = enabled
	return h
}

// WithPayoutsEnabled mirrors MONETIZATION_PAYOUTS_ENABLED (plan Phase 3C)
// for the two things the HTTP layer decides on it: the estimate label on
// earnings and statements, and whether a provider webhook is processed
// or merely stored. The refusal of a withdrawal itself is the service's.
func (h *Handler) WithPayoutsEnabled(enabled bool) *Handler {
	h.payoutsEnabled = enabled
	return h
}

// WithMaintenance puts the boundary into maintenance mode: admin routes
// only (key + scope), reads as in beta, everything else 503 MAINTENANCE.
// See runmode.Resolve for what the process does with the flag.
func (h *Handler) WithMaintenance(on bool) *Handler {
	h.maintenance = on
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	v1 := r.Group("/v1/monetization")
	v1.Use(h.launchBoundary())
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
			cf.GET("/quality-bands", h.ListCreatorFundQualityBands)
			// Period statements: which period, and how much came from
			// each of the three streams.
			cf.GET("/statements", h.ListCreatorFundStatements)
			cf.GET("/statements/:periodKey", h.GetCreatorFundStatement)
		}
		admin := v1.Group("/admin/creator-fund")
		{
			admin.GET("/rates", h.ListCreatorFundRatesAdmin)
			admin.PUT("/rates", h.SetCreatorFundRate)
			admin.PUT("/quality-bands", h.SetCreatorFundQualityBand)
			admin.POST("/:userId/suspend", h.SuspendCreatorFund)
			admin.POST("/:userId/unsuspend", h.UnsuspendCreatorFund)
			// Re-measure one day (accrual only — moves no money).
			admin.POST("/settle", h.ForceAccrueCreatorFundDay)
			// The payment run. Same call the scheduled worker makes.
			admin.POST("/settle-period", h.SettleCreatorFundPeriod)
			admin.POST("/:userId/settle-period", h.SettleCreatorFundPeriodForCreator)
			// Corrections (Phase 2A): reverse one accrual row; if it was
			// credited the net comes back through a keyed adjustment.
			// Behind hasAdminScope, and — like every admin route — behind
			// the beta boundary until MONETIZATION_WRITES_ENABLED=true.
			admin.POST("/earnings/:id/reverse", h.ReverseCreatorFundEarning)
			admin.GET("/earnings/:id", h.GetCreatorFundEarningAdmin)
			// The fund cap per settlement period (Phase 2C). Lowering below
			// what has accrued is refused; nothing already earned is reduced.
			admin.GET("/budgets", h.ListCreatorFundBudgets)
			admin.PUT("/budgets", h.SetCreatorFundBudget)
		}
	}
}

// The beta reads, and the line they are drawn on (plan Phase 3C): the
// RULES of payment are open, the ESTIMATES are open and labelled as such,
// and everything that moves money or exposes admin, tax or entitlement
// state is closed.
//
// A creator is entitled to know what a thousand views is worth and what
// curve their score is scaled by before they decide what to publish —
// those are the same for everyone and publishing them costs nothing.
// Hence rates, quality-bands and status.
//
// earnings and statements open with this phase, on one condition: while
// payouts are off every such response carries "estimate": true and
// "withdrawable": false (see stampEstimate), because the first number a
// creator sees is the number they believe they are owed, and a figure
// that may still move must say so on its face.
//
// Rules are method plus gin route PATTERN, matched on c.FullPath() —
// the registered pattern, parameters and all — never on the request URL,
// so `/creator-fund/statements/:periodKey` can be opened without also
// opening whatever else happens to start with that prefix. A request
// that matches no registered route has an empty FullPath and is refused.
type betaRule struct {
	method  string
	pattern string
}

var betaReadOnlyRules = []betaRule{
	// The creator's own recorded ledger and its history.
	{http.MethodGet, "/v1/monetization/creator-ledger"},
	{http.MethodGet, "/v1/monetization/wallet"}, // deprecated read-only alias
	{http.MethodGet, "/v1/monetization/transactions"},
	{http.MethodGet, "/v1/monetization/payouts"},

	// The pay rules. Public, identical for every creator, no amounts.
	{http.MethodGet, "/v1/monetization/creator-fund/rates"},
	{http.MethodGet, "/v1/monetization/creator-fund/quality-bands"},
	// Whether the caller is in the programme at all. Per-caller, but it
	// carries eligibility, not money.
	{http.MethodGet, "/v1/monetization/creator-fund/status"},

	// The estimates. Labelled while payouts are off.
	{http.MethodGet, "/v1/monetization/creator-fund/earnings"},
	{http.MethodGet, "/v1/monetization/creator-fund/statements"},
	{http.MethodGet, "/v1/monetization/creator-fund/statements/:periodKey"},
}

// betaRuleAllows reports whether a method and registered route pattern
// are open in beta. An empty pattern (no route matched) is never open.
func betaRuleAllows(method, pattern string) bool {
	if pattern == "" {
		return false
	}
	pattern = strings.TrimSuffix(pattern, "/")
	for _, r := range betaReadOnlyRules {
		if r.method == method && r.pattern == pattern {
			return true
		}
	}
	return false
}

// adminRoutePrefix is the registered-pattern prefix of every admin route.
// Matched on c.FullPath(), never on the request URL.
const adminRoutePrefix = "/v1/monetization/admin/"

// isAdminPattern reports whether a registered route pattern is an admin
// route. An empty pattern (no route matched) is never one.
func isAdminPattern(pattern string) bool {
	return pattern != "" && strings.HasPrefix(pattern, adminRoutePrefix)
}

// launchBoundary fails closed while financial products are not launched.
// The rule list is intentionally exact: adding a new GET does not
// accidentally publish admin, tax, entitlement, or settlement data.
//
// Maintenance mode (checked first, so it wins over writesEnabled): the
// beta reads stay open; an admin route is let through only when the
// request carries the internal service key — the scope header is an
// operator identity claim recorded for audit, not authentication, and
// a container that merely reaches the port must not be able to call a
// correction; everything else answers 503 MAINTENANCE.
func (h *Handler) launchBoundary() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.maintenance {
			pattern := c.FullPath()
			switch {
			case isAdminPattern(pattern):
				if h.internalKey == "" {
					// runmode refuses to boot this way; fail closed anyway.
					c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
						"error": gin.H{
							"code":    "MAINTENANCE_KEY_UNSET",
							"message": "Maintenance mode requires INTERNAL_SERVICE_KEY; admin routes are closed.",
						},
					})
					return
				}
				if !hmac.Equal([]byte(c.GetHeader("X-Internal-Service-Key")), []byte(h.internalKey)) {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
						"error": gin.H{
							"code":    "UNAUTHORIZED",
							"message": "internal service key required on admin routes in maintenance mode",
						},
					})
					return
				}
				c.Next()
			case betaRuleAllows(c.Request.Method, pattern):
				c.Next()
			default:
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
					"error": gin.H{
						"code":    "MAINTENANCE",
						"message": "Monetization is in maintenance: only admin corrections are being served.",
					},
				})
			}
			return
		}
		if h.writesEnabled {
			c.Next()
			return
		}
		if !betaRuleAllows(c.Request.Method, c.FullPath()) {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": gin.H{
					"code":    "MONETIZATION_NOT_LAUNCHED",
					"message": "Money actions are not available in this beta.",
				},
			})
			return
		}
		c.Next()
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

// GetCreatorLedger returns the caller's recorded creator-earnings ledger. A
// creator with no activity receives a zero-value, has_activity=false view;
// this read never creates financial state.
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

	if !wallet.HasActivity {
		api.JSON(c.Writer, http.StatusOK, map[string]any{
			"user_id":                 wallet.UserID,
			"balance_paise":           int64(0),
			"lifetime_earnings_paise": int64(0),
			"pending_payout_paise":    int64(0),
			"currency":                wallet.Currency,
			"is_frozen":               false,
			"has_activity":            false,
			"created_at":              nil,
			"updated_at":              nil,
		}, nil)
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
		"_deprecated_use":   "/v1/monetization/creator-ledger",
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
		body["has_activity"] = wallet.HasActivity
		if wallet.HasActivity {
			body["created_at"] = wallet.CreatedAt
			body["updated_at"] = wallet.UpdatedAt
		} else {
			body["created_at"] = nil
			body["updated_at"] = nil
		}
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

// AddPayoutMethodRequest is either a bank account (plan Phase 4D:
// holder_name, account_number, ifsc; the number is validated, encrypted
// and registered with the provider) or one of the legacy opaque types
// (details_encrypted as the client supplied it).
type AddPayoutMethodRequest struct {
	MethodType       string `json:"method_type" binding:"required"`
	DetailsEncrypted string `json:"details_encrypted"`
	IsDefault        bool   `json:"is_default"`

	HolderName    string `json:"holder_name"`
	AccountNumber string `json:"account_number"`
	IFSC          string `json:"ifsc"`
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

	if req.MethodType == service.PayoutMethodTypeBankAccount {
		m, err := h.svc.AddBankPayoutMethod(c.Request.Context(), userID, service.BankPayoutMethodInput{
			HolderName:    req.HolderName,
			AccountNumber: req.AccountNumber,
			IFSC:          req.IFSC,
			IsDefault:     req.IsDefault,
		})
		if err != nil {
			reply := func(status int, code, msg string) {
				api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, msg, nil)
			}
			switch {
			case errors.Is(err, service.ErrInvalidIFSC):
				reply(http.StatusBadRequest, "INVALID_IFSC", "IFSC must be four letters, a zero and six alphanumerics")
			case errors.Is(err, service.ErrInvalidBankAccount):
				reply(http.StatusBadRequest, "INVALID_BANK_ACCOUNT", "Account number must be 9 to 18 digits")
			case errors.Is(err, service.ErrInvalidHolderName):
				reply(http.StatusBadRequest, "INVALID_HOLDER_NAME", "Account holder name is required")
			case errors.Is(err, service.ErrBankDetailsRejected):
				reply(http.StatusUnprocessableEntity, "BANK_DETAILS_REJECTED", "The payout provider refused these bank details")
			case errors.Is(err, service.ErrBankCaptureNotConfigured):
				reply(http.StatusServiceUnavailable, "BANK_CAPTURE_NOT_CONFIGURED", "Bank accounts cannot be stored on this deployment yet")
			default:
				reply(http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
			}
			return
		}
		api.JSON(c.Writer, http.StatusCreated, m, nil)
		return
	}

	if req.DetailsEncrypted == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "details_encrypted is required for this method type", nil)
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

	// The gate pipeline (plan Phase 3A). Every refusal names its gate, so
	// a creator is told what to fix rather than that something failed.
	outcome, err := h.svc.RequestPayoutWithKey(c.Request.Context(), userID, req.AmountPaise, payoutMethodID, c.GetHeader("X-Idempotency-Key"))
	if err != nil {
		reply := func(status int, code, message string) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, message, nil)
		}
		switch {
		case errors.Is(err, service.ErrPayoutsNotEnabled):
			reply(http.StatusServiceUnavailable, "PAYOUTS_NOT_ENABLED", "Withdrawals are not available in this beta. Your earnings are estimates until they are.")
		case errors.Is(err, service.ErrInvalidAmount):
			reply(http.StatusBadRequest, "INVALID_AMOUNT", "Amount must be greater than zero and within the payout limit")
		case errors.Is(err, service.ErrMinimumPayoutNotMet):
			reply(http.StatusBadRequest, "MINIMUM_PAYOUT_NOT_MET", "The minimum withdrawal is Rs 100")
		case errors.Is(err, service.ErrKYCNotVerified):
			reply(http.StatusForbidden, "KYC_NOT_VERIFIED", "Your tax profile has not been verified")
		case errors.Is(err, service.ErrPayoutMethodNotFound):
			reply(http.StatusNotFound, "PAYOUT_METHOD_NOT_FOUND", "Payout method not found")
		case errors.Is(err, service.ErrPayoutMethodNotVerified):
			reply(http.StatusForbidden, "PAYOUT_METHOD_NOT_VERIFIED", "Payout method has not been verified")
		case errors.Is(err, service.ErrWalletNotFound):
			reply(http.StatusNotFound, "WALLET_NOT_FOUND", "Wallet not found")
		case errors.Is(err, service.ErrWalletFrozen):
			reply(http.StatusForbidden, "WALLET_FROZEN", "Wallet is frozen")
		case errors.Is(err, service.ErrInsufficientBalance):
			reply(http.StatusBadRequest, "INSUFFICIENT_BALANCE", "Insufficient balance for payout")
		default:
			reply(http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		}
		return
	}

	if outcome.Held {
		// Not an error: the request is recorded and a reviewer will see
		// it. No money moved. 202 rather than 201, so a client can tell
		// "held for review" from "on its way" without parsing the body.
		api.JSON(c.Writer, http.StatusAccepted, gin.H{
			"code":        "PAYOUT_HELD",
			"message":     "Your withdrawal has been held for review. No money has moved.",
			"hold_reason": outcome.HoldReason,
			"request":     outcome.Request,
			"replayed":    outcome.Replayed,
		}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, outcome, nil)
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

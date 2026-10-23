package http

// Paid creator memberships on payments-service (phase 1 contract, pinned
// 23 Oct 2026), and the switch that opens them.
//
// MEMBERSHIPS_ENABLED opens the membership routes and the entitlement routes
// EVEN WHILE MONETIZATION_WRITES_ENABLED is off; the creator fund, tips and
// payouts stay behind the writes flag. With MEMBERSHIPS_ENABLED off the
// membership routes answer 503 MEMBERSHIPS_NOT_ENABLED whatever the writes
// flag says (no membership money exists outside payments-service any more),
// and the entitlement routes follow the writes flag as before. Maintenance
// mode closes all of it.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// WithMembershipsEnabled mirrors MEMBERSHIPS_ENABLED (runmode.Mode).
func (h *Handler) WithMembershipsEnabled(on bool) *Handler {
	h.membershipsEnabled = on
	return h
}

// membershipRoutes are the routes MEMBERSHIPS_ENABLED opens, as method +
// registered gin pattern.
var membershipRoutes = []betaRule{
	{http.MethodGet, "/v1/monetization/tiers"},
	{http.MethodPost, "/v1/monetization/tiers"},
	{http.MethodPatch, "/v1/monetization/tiers/:id"},
	{http.MethodGet, "/v1/monetization/creators/:creatorId/tiers"},
	{http.MethodGet, "/v1/monetization/creators/:creatorId/membership-eligibility"},
	{http.MethodPost, "/v1/monetization/subscribe/:creatorId"},
	{http.MethodDelete, "/v1/monetization/subscribe/:creatorId"},
	{http.MethodPost, "/v1/monetization/subscriptions/:id/pause"},
	{http.MethodPost, "/v1/monetization/subscriptions/:id/resume"},
	{http.MethodPost, "/v1/monetization/subscriptions/:id/cancel"},
	{http.MethodPost, "/v1/monetization/subscriptions/:id/upgrade"},
	{http.MethodPost, "/v1/monetization/subscriptions/:id/renew"},
	{http.MethodGet, "/v1/monetization/subscriptions/:id/events"},
	{http.MethodGet, "/v1/monetization/memberships"},
	{http.MethodGet, "/v1/monetization/memberships/payments/:paymentId"},
	{http.MethodPost, "/v1/monetization/memberships/payments/:paymentId/verify"},
}

// entitlementRoutes are opened by MEMBERSHIPS_ENABLED or by writes.
var entitlementRoutes = []betaRule{
	{http.MethodGet, "/v1/monetization/entitlements"},
	{http.MethodPost, "/v1/monetization/entitlements/check"},
	{http.MethodPost, "/v1/monetization/internal/entitlements/check"},
}

func ruleIn(rules []betaRule, method, pattern string) bool {
	if pattern == "" {
		return false
	}
	pattern = strings.TrimSuffix(pattern, "/")
	for _, r := range rules {
		if r.method == method && r.pattern == pattern {
			return true
		}
	}
	return false
}

// membershipGate is the boundary's membership step: handled=true when the
// route is a membership or entitlement route (then allow says what to do).
func (h *Handler) membershipGate(method, pattern string) (handled, allow bool, code string) {
	switch {
	case ruleIn(membershipRoutes, method, pattern):
		return true, h.membershipsEnabled, "MEMBERSHIPS_NOT_ENABLED"
	case ruleIn(entitlementRoutes, method, pattern):
		return true, h.membershipsEnabled || h.writesEnabled, "MONETIZATION_NOT_LAUNCHED"
	}
	return false, false, ""
}

func (h *Handler) registerMembershipRoutes(v1 *gin.RouterGroup) {
	v1.POST("/subscriptions/:id/renew", h.RenewSubscription)
	v1.GET("/memberships", h.ListMyMemberships)
	v1.GET("/memberships/payments/:paymentId", h.GetMembershipPayment)
	v1.POST("/memberships/payments/:paymentId/verify", h.VerifyMembershipPayment)
	v1.GET("/creators/:creatorId/membership-eligibility", h.GetMembershipEligibility)
}

// idempotencyKey reads Idempotency-Key (the contract's header), falling
// back to the older X-Idempotency-Key.
func idempotencyKey(c *gin.Context) (string, bool) {
	k := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if k == "" {
		k = strings.TrimSpace(c.GetHeader("X-Idempotency-Key"))
	}
	if len(k) > 200 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key is at most 200 characters", nil)
		return "", false
	}
	return k, true
}

// writeMembershipError answers a membership refusal with its own code.
func writeMembershipError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	e := func(status int, code, msg string) { api.ErrorWithContext(ctx, c.Writer, status, code, msg, nil) }
	var ne *service.NotEligibleError
	switch {
	case errors.As(err, &ne):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "MEMBERSHIPS_NOT_ELIGIBLE",
			"You can't offer memberships yet", gin.H{"missing": ne.Missing})
	case errors.Is(err, service.ErrMembershipPaymentsUnavailable):
		e(http.StatusServiceUnavailable, "MEMBERSHIP_PAYMENTS_UNAVAILABLE", "Payments are unavailable right now; try again")
	case errors.Is(err, service.ErrPaymentSessionUnavailable):
		e(http.StatusBadGateway, "PAYMENT_SESSION_UNAVAILABLE", "Checkout could not be opened; try again")
	case errors.Is(err, service.ErrCheckoutExpired):
		e(http.StatusConflict, "CHECKOUT_EXPIRED", "This checkout expired; start again with a new Idempotency-Key")
	case errors.Is(err, postgres.ErrAlreadySubscribed):
		e(http.StatusConflict, "ALREADY_SUBSCRIBED", "Already subscribed to this creator")
	case errors.Is(err, service.ErrIdempotencyKeyReused):
		e(http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "This idempotency key was already used for a different subscription")
	case errors.Is(err, postgres.ErrSubscriptionNotFound):
		e(http.StatusNotFound, "NOT_FOUND", "Subscription not found")
	case errors.Is(err, postgres.ErrSubscriptionNotOwned):
		e(http.StatusForbidden, "FORBIDDEN", "You do not own this subscription")
	case errors.Is(err, postgres.ErrNotRenewable):
		e(http.StatusConflict, "SUBSCRIPTION_NOT_RENEWABLE", "This membership can't be renewed")
	case errors.Is(err, postgres.ErrRenewalAlreadyPaid):
		e(http.StatusConflict, "RENEWAL_ALREADY_PAID", "This period is already paid")
	case errors.Is(err, postgres.ErrTierInactive):
		e(http.StatusBadRequest, "TIER_INACTIVE", "Tier is not active")
	case errors.Is(err, postgres.ErrSubscriptionStateChanged):
		e(http.StatusConflict, "SUBSCRIPTION_STATE_CHANGED", "The subscription changed while this request was running; nothing was charged")
	case errors.Is(err, service.ErrMembershipPaymentNotFound):
		e(http.StatusNotFound, "NOT_FOUND", "Payment not found")
	case errors.Is(err, postgres.ErrRefundNotAllowed):
		e(http.StatusConflict, "REFUND_NOT_ALLOWED", "This payment can't be refunded")
	default:
		switch err.Error() {
		case "CANNOT_SUBSCRIBE_TO_SELF":
			e(http.StatusBadRequest, "CANNOT_SUBSCRIBE_TO_SELF", "Cannot subscribe to yourself")
		case "TIER_NOT_FOUND":
			e(http.StatusNotFound, "TIER_NOT_FOUND", "Tier not found")
		case "TIER_INACTIVE":
			e(http.StatusBadRequest, "TIER_INACTIVE", "Tier is not active")
		case "TIER_CREATOR_MISMATCH":
			e(http.StatusBadRequest, "TIER_CREATOR_MISMATCH", "Tier does not belong to this creator")
		case "SUBSCRIPTION_NOT_FOUND":
			e(http.StatusNotFound, "NOT_FOUND", "Subscription not found")
		case "SUBSCRIPTION_NOT_OWNED":
			e(http.StatusForbidden, "FORBIDDEN", "You do not own this subscription")
		case "SUBSCRIPTION_NOT_ACTIVE":
			e(http.StatusBadRequest, "BAD_REQUEST", "Subscription is not active")
		case "SUBSCRIPTION_STATE_CHANGED":
			e(http.StatusConflict, "SUBSCRIPTION_STATE_CHANGED", "The subscription changed while this request was running; nothing was charged")
		default:
			e(http.StatusInternalServerError, "INTERNAL_ERROR", "Something went wrong")
		}
	}
}

// ---------------------------------------------------------------------------
// Join, renew, upgrade
// ---------------------------------------------------------------------------

// SubscribeRequest is a join: the tier to join.
type SubscribeRequest struct {
	TierID string `json:"tier_id" binding:"required"`
}

// Subscribe — POST /v1/monetization/subscribe/:creatorId {tier_id} with
// Idempotency-Key → 201 checkout. Creates a pending_payment subscription and
// its join payment; access starts only on the signed payment event.
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "tier_id is required", nil)
		return
	}
	tierID, err := uuid.Parse(req.TierID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid tier ID", nil)
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	co, err := h.svc.Subscribe(c.Request.Context(), userID, creatorID, tierID, key)
	if err != nil {
		writeMembershipError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, co, nil)
}

// RenewSubscription — POST /v1/monetization/subscriptions/:id/renew → 201
// checkout (kind renewal).
func (h *Handler) RenewSubscription(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	subID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid subscription ID", nil)
		return
	}
	co, err := h.svc.Renew(c.Request.Context(), userID, subID)
	if err != nil {
		writeMembershipError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, co, nil)
}

// UpgradeSubscriptionRequest names the tier to move to. tier_id is the
// contract's field; new_tier_id is accepted from older clients.
type UpgradeSubscriptionRequest struct {
	TierID    string `json:"tier_id"`
	NewTierID string `json:"new_tier_id"`
}

// UpgradeSubscription — POST /v1/monetization/subscriptions/:id/upgrade
// {tier_id}. Up with a difference: 201 checkout (kind upgrade), the tier
// changes on the signed event. Up with no difference: 200 {status:"upgraded"}.
// Down: 200 {status:"scheduled", effective_at}, nothing charged.
func (h *Handler) UpgradeSubscription(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	subID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid subscription ID", nil)
		return
	}
	var req UpgradeSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "tier_id is required", nil)
		return
	}
	raw := req.TierID
	if raw == "" {
		raw = req.NewTierID
	}
	newTierID, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid tier ID", nil)
		return
	}
	change, err := h.svc.UpgradeSubscription(c.Request.Context(), userID, subID, newTierID)
	if err != nil {
		writeMembershipError(c, err)
		return
	}
	if change.Checkout != nil {
		api.JSON(c.Writer, http.StatusCreated, change.Checkout, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, change, nil)
}

// ---------------------------------------------------------------------------
// Status, callback, my memberships, eligibility
// ---------------------------------------------------------------------------

// GetMembershipPayment — GET /v1/monetization/memberships/payments/:paymentId
// (payer only; 404 otherwise).
func (h *Handler) GetMembershipPayment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	paymentID, err := uuid.Parse(c.Param("paymentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Payment not found", nil)
		return
	}
	v, err := h.svc.GetMembershipPayment(c.Request.Context(), userID, paymentID)
	if err != nil {
		writeMembershipError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, v, nil)
}

// VerifyMembershipPayment — POST .../memberships/payments/:paymentId/verify
// {razorpay_order_id, razorpay_payment_id, razorpay_signature}. ADVISORY:
// it may move the payment to confirming; it never grants access.
func (h *Handler) VerifyMembershipPayment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	paymentID, err := uuid.Parse(c.Param("paymentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Payment not found", nil)
		return
	}
	var cb service.MembershipCallback
	if err := c.ShouldBindJSON(&cb); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid callback body", nil)
		return
	}
	v, err := h.svc.VerifyMembershipPayment(c.Request.Context(), userID, paymentID, cb)
	if err != nil {
		writeMembershipError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, v, nil)
}

// ListMyMemberships — GET /v1/monetization/memberships.
func (h *Handler) ListMyMemberships(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListMyMemberships(c.Request.Context(), userID)
	if err != nil {
		writeMembershipError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// GetMembershipEligibility — GET /v1/monetization/creators/:creatorId/
// membership-eligibility. The creator only (caller == creatorId); anyone
// else 403.
func (h *Handler) GetMembershipEligibility(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	creatorID, err := uuid.Parse(c.Param("creatorId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid creator ID", nil)
		return
	}
	if creatorID != userID {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "Only the creator can see their eligibility", nil)
		return
	}
	el, err := h.svc.MembershipEligibility(c.Request.Context(), creatorID)
	if err != nil {
		writeMembershipError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, el, nil)
}

// ---------------------------------------------------------------------------
// Support refund (admin-service token family)
// ---------------------------------------------------------------------------

// MembershipRefundPath is support's refund route, in the token-only admin
// family (aud monetization, iss admin-service, scope monetization:refund.issue,
// act = the admin).
const MembershipRefundPath = "/memberships/payments/:paymentId/refund"

// isMembershipAdminRoute: the token-family route is judged by
// MEMBERSHIPS_ENABLED (membershipAdminBoundary), not the writes flag.
func isMembershipAdminRoute(rt adminRoute) bool {
	return rt.method == http.MethodPost && rt.path == MembershipRefundPath
}

// membershipAdminBoundary: maintenance closes the route (a refund is not a
// correction); otherwise it is open exactly when memberships are.
func (h *Handler) membershipAdminBoundary() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.maintenance {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
				"code": "MAINTENANCE", "message": "Monetization is in maintenance: only admin corrections are being served.",
			}})
			return
		}
		if !h.membershipsEnabled {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
				"code": "MEMBERSHIPS_NOT_ENABLED", "message": "Memberships are not enabled.",
			}})
			return
		}
		c.Next()
	}
}

type membershipRefundRequest struct {
	Reason string `json:"reason"`
}

// RefundMembershipPayment — POST /v1/monetization/internal/admin/memberships/
// payments/:paymentId/refund {reason}. Queues and files a full refund under
// membership-refund:{paymentId}; the period it bought is removed when
// payments reports the refund settled (payment.refunded).
func (h *Handler) RefundMembershipPayment(c *gin.Context) {
	actor, ok := tokenActor(c)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil)
		return
	}
	paymentID, err := uuid.Parse(c.Param("paymentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Payment not found", nil)
		return
	}
	var req membershipRefundRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "reason is required", nil)
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if len(reason) > 500 {
		reason = reason[:500]
	}
	v, err := h.svc.RefundMembershipPayment(c.Request.Context(), actor, paymentID, reason)
	if err != nil {
		writeMembershipError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusAccepted, v, nil)
}

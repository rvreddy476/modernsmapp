package http

// The payments HTTP surface after Commerce P0.
//
// What this file used to be, and why it changed:
//
// Every route under /v1/payments was authenticated by a single header,
// X-Internal-Service-Key, which the API gateway injected into EVERY request
// it proxied. Since /v1/payments was in the gateway's route table, any
// authenticated end user arrived here holding full service authority. From a
// browser you could:
//
//	POST  /v1/payments/intents  {reference_id: <your order>, amount_minor: 1}
//	PATCH /v1/payments/intents/<id>/status {old:"pending", new:"succeeded"}
//
// and commerce would mark a ₹10,000 order paid. No PSP was contacted at any
// point in that chain. The refund route was reachable the same way, and
// InitiateRefund permitted the PAYER as actor, so a buyer could also refund
// their own completed purchase.
//
// Four changes close it:
//
//	LB-1  The gateway no longer proxies /v1/payments at all.
//	A2    Callers authenticate with an audience-scoped, per-service Ed25519
//	      token that pins issuer, subject, audience, expiry, not-before, key
//	      id AND an operation/reference-type allowlist — so food-service
//	      cannot act on a commerce order even with a valid token of its own.
//	A1    PATCH /status is GONE. No client-reachable route can assert that a
//	      payment succeeded. Terminal state comes only from a
//	      signature-verified webhook or a server-side provider fetch.
//	A3    The webhook fails CLOSED on a missing secret, a missing signature,
//	      a missing provider event id, and a dedupe error; and its inbox
//	      row, money effect and outbox row commit in one transaction.

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/paymentmethod"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ServiceAuthHeader carries the caller's service token.
const ServiceAuthHeader = "X-Service-Authorization"

type Handler struct {
	svc      *service.Service
	verifier *servicetoken.Verifier
	provider gateway.Provider
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// WithServiceAuth installs the A2 verifier. Without it the service refuses
// to register its authenticated routes at all — see RegisterRoutes.
func (h *Handler) WithServiceAuth(v *servicetoken.Verifier) *Handler {
	h.verifier = v
	return h
}

// WithProvider installs the PSP adapter used for webhook verification.
func (h *Handler) WithProvider(p gateway.Provider) *Handler {
	h.provider = p
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) error {
	// The webhook is deliberately outside the service-auth gate: a PSP
	// cannot present our service token. It is authenticated by the
	// provider's own signature over the raw body, checked inside the
	// adapter, and it fails closed.
	r.POST("/v1/payments/webhook", h.HandleWebhook)

	if h.verifier == nil || h.verifier.Callers() == 0 {
		// A2: refuse to expose the money surface with no caller allowlist.
		// A permissive default here would silently restore the old
		// "anything with the shared key wins" behaviour.
		return errors.New(
			"payments: refusing to register /v1/payments routes without a service-token verifier " +
				"and at least one registered caller (Commerce P0 A2)")
	}

	v1 := r.Group("/v1/payments")
	v1.Use(h.requireServiceToken())
	{
		v1.POST("/intents", h.requireOp(servicetoken.OpIntentCreate), h.InitiatePayment)
		v1.GET("/intents/:id", h.requireOp(servicetoken.OpIntentRead), h.GetIntent)
		v1.GET("/intents", h.requireOp(servicetoken.OpIntentRead), h.ListByReference)
		v1.POST("/intents/:id/verify", h.requireOp(servicetoken.OpIntentRead), h.VerifyIntent)
		v1.POST("/intents/:id/refund", h.requireOp(servicetoken.OpRefundCreate), h.InitiateRefund)

		// A1: PATCH /intents/:id/status is REMOVED and must never return.
		// It let a caller assert `succeeded` with no PSP proof and no
		// ownership check, and Service.UpdateStatus published
		// payment.succeeded straight off that transition. The route now
		// answers 410 so a stale caller fails loudly instead of silently
		// falling through to a 404 that reads like a deploy problem.
		v1.PATCH("/intents/:id/status", h.GoneClientStatusMutation)
	}
	return nil
}

// ─── A2 service authentication ───────────────────────────────────────

type authCtxKey struct{}

func (h *Handler) requireServiceToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		// A user token must never reach this surface. If the edge ever
		// proxies here again by mistake, this is the second line of defence
		// behind the gateway's route policy.
		if c.GetHeader("X-User-Id") != "" && c.GetHeader(ServiceAuthHeader) == "" {
			slog.Warn("payments: a user-identified request reached the service surface",
				"path", c.Request.URL.Path)
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden,
				"FORBIDDEN", "payments is not reachable by end users", nil)
			c.Abort()
			return
		}
		raw := strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
		if raw == "" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized,
				"UNAUTHORIZED", "service token required", nil)
			c.Abort()
			return
		}
		c.Set("service_token_raw", raw)
		c.Next()
	}
}

// requireOp verifies the token for one operation and, where the request
// names a reference type, for that reference type too.
func (h *Handler) requireOp(op string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, _ := c.Get("service_token_raw")
		token, _ := raw.(string)
		refType := refTypeFromRequest(c)

		v, err := h.verifier.Verify(token, op, refType)
		if err != nil {
			// Deliberately terse outward: the caller learns it was refused,
			// not which claim failed.
			slog.Warn("payments: service token refused",
				"op", op, "ref_type", refType, "reason", err.Error(), "path", c.Request.URL.Path)
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden,
				"FORBIDDEN", "service token rejected", nil)
			c.Abort()
			return
		}
		c.Set("caller_domain", v.Issuer)
		c.Next()
	}
}

// refTypeFromRequest extracts the reference type the caller is acting on,
// so the allowlist can be applied per request rather than per key.
func refTypeFromRequest(c *gin.Context) string {
	if q := c.Query("ref_type"); q != "" {
		return q
	}
	if c.Request.Method == http.MethodPost && strings.HasSuffix(c.Request.URL.Path, "/intents") {
		// Body is read by the handler; the reference type is re-checked
		// there against the caller's allowlist.
		return ""
	}
	return ""
}

func callerDomain(c *gin.Context) string {
	if v, ok := c.Get("caller_domain"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ─── Intents ─────────────────────────────────────────────────────────

// InitiatePayment POST /v1/payments/intents — SERVICE ONLY.
//
// The amount still arrives in the body, but the body now comes from
// commerce-service, which derives it from the order it owns. The client
// never speaks to this endpoint, so there is no path by which a buyer
// chooses what their own order costs.
func (h *Handler) InitiatePayment(c *gin.Context) {
	caller := callerDomain(c)
	var body struct {
		PayerID        string `json:"payer_id" binding:"required"`
		PayeeID        string `json:"payee_id" binding:"required"`
		ReferenceType  string `json:"reference_type" binding:"required"`
		ReferenceID    string `json:"reference_id" binding:"required"`
		AmountMinor    int64  `json:"amount_minor" binding:"required"`
		Currency       string `json:"currency"`
		Method         string `json:"method" binding:"required"`
		IdempotencyKey string `json:"idempotency_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	// A2: the reference type must be one this caller is allowed to act on.
	// Without this a valid food-service token could open an intent against
	// reference_type=order.
	tokenRaw, _ := c.Get("service_token_raw")
	if _, err := h.verifier.Verify(tokenRaw.(string), servicetoken.OpIntentCreate, body.ReferenceType); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden,
			"FORBIDDEN", "caller may not create intents for this reference type", nil)
		return
	}
	if body.AmountMinor <= 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"amount_minor must be positive", nil)
		return
	}
	// A5 / C3-LB-3: the launch vocabulary, refused at the edge.
	//
	// COD keeps its own error code because clients render dedicated copy for
	// it; everything else outside the vocabulary gets the generic refusal.
	// Neither becomes a payment intent: there is no PSP leg to open, and
	// letting one exist would resurrect the deduct-stock-at-checkout path the
	// fence exists to remove.
	if strings.EqualFold(strings.TrimSpace(body.Method), "cod") {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"COD_NOT_SUPPORTED", "cash on delivery is not enabled (Commerce P0 is prepaid-only)", nil)
		return
	}
	if err := paymentmethod.Validate(body.Method); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"PAYMENT_METHOD_NOT_SUPPORTED", err.Error(), nil)
		return
	}

	payerID, err := uuid.Parse(body.PayerID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "invalid payer_id", nil)
		return
	}
	payeeID, err := uuid.Parse(body.PayeeID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "invalid payee_id", nil)
		return
	}
	refID, err := uuid.Parse(body.ReferenceID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "invalid reference_id", nil)
		return
	}

	// B4: an intent with no owning domain is one that every authorised
	// service may later read and refund. Refuse before creating it rather
	// than stamping it afterwards and hoping the stamp lands.
	if caller == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden,
			"FORBIDDEN", "caller identity is required", nil)
		return
	}

	intent, err := h.svc.InitiatePayment(c.Request.Context(), service.InitiateInput{
		PayerID:        payerID,
		PayeeID:        payeeID,
		ReferenceType:  body.ReferenceType,
		ReferenceID:    refID,
		AmountMinor:    body.AmountMinor,
		Currency:       body.Currency,
		Method:         body.Method,
		IdempotencyKey: body.IdempotencyKey,
		// B4: the owner travels INTO the insert. The best-effort
		// StampOwnerDomain call that used to sit after this one is gone —
		// it could fail, log a warning, and still return 201 with an
		// unowned intent.
		OwnerDomain: caller,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INITIATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, h.withClientSession(intent), nil)
}

// GetIntent GET /v1/payments/intents/:id — SERVICE ONLY, owner-scoped.
func (h *Handler) GetIntent(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid intent id", nil)
		return
	}
	if !h.ownsIntent(c, id) {
		return
	}
	intent, err := h.svc.GetIntent(c.Request.Context(), id)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "intent not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, h.withClientSession(intent), nil)
}

// withClientSession attaches what the client SDK needs to open checkout.
//
// The publishable key travels from the server that created the provider
// order rather than being compiled into the app. Those two must agree — an
// app built against a test key cannot open a sheet for an order created
// against a live key — and sourcing it from here makes disagreement
// impossible rather than merely unlikely.
//
// Nothing secret is added. The key is publishable and the order handle is
// one the client necessarily learns anyway. The AMOUNT is deliberately
// absent: LB-4 is that the client never names what it is paying, and the
// provider order already fixes it.
//
// A provider that cannot derive a session from the order id (Cashfree needs
// a stored payment_session_id) contributes nothing, and the field is simply
// absent — the app then reports that it cannot open a sheet rather than
// opening one that will fail.
func (h *Handler) withClientSession(intent *postgres.PaymentIntent) map[string]any {
	out := map[string]any{
		"id":             intent.ID,
		"status":         intent.Status,
		"amount_minor":   intent.AmountMinor(),
		"currency":       intent.Currency,
		"provider_ref":   intent.ProviderRef,
		"reference_id":   intent.ReferenceID,
		"reference_type": intent.ReferenceType,
		"payer_id":       intent.PayerID,
		"payee_id":       intent.PayeeID,
	}
	if h.provider != nil && intent.ProviderRef != "" {
		if session := h.provider.ClientSession(intent.ProviderRef); len(session) > 0 {
			out["client_session"] = session
		}
	}
	return out
}

// ownsIntent enforces the per-domain boundary and writes the error response
// itself. Returns false when the caller must be refused.
//
// B4 — this check used to FAIL OPEN. The condition was:
//
//	if owner != "" && caller != "" && owner != caller { refuse }
//
// so an intent whose owner stamp had failed (an empty `owner_domain`) was
// readable and refundable by every authorised service on the cluster. The
// stamp was a separate best-effort UPDATE issued after the 201 was decided,
// which made "empty owner" a state reachable by a single transient error, not
// a theoretical one. food-service could then read or refund a commerce
// payment: real money moved, and two domains' ledgers corrupted.
//
// It now fails CLOSED in all three directions — unknown caller, unowned
// intent, mismatched pair. Empty is refused rather than waved through.
func (h *Handler) ownsIntent(c *gin.Context, id uuid.UUID) bool {
	caller := callerDomain(c)
	// 404 throughout, never 403: a caller from another domain must not be
	// able to distinguish "not yours" from "does not exist".
	deny := func(reason, owner string) bool {
		slog.Warn("payments: intent access refused",
			"intent_id", id, "reason", reason, "owner", owner, "caller", caller)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "intent not found", nil)
		return false
	}

	if caller == "" {
		return deny("caller has no verified service identity", "")
	}
	owner, err := h.svc.IntentOwnerDomain(c.Request.Context(), id)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "intent not found", nil)
		return false
	}
	if owner == "" {
		// Legacy rows pre-date migration 007's backfill, and any row whose
		// owner is genuinely unknown is one whose authority cannot be
		// established. Refusing is the only safe reading.
		return deny("intent has no owner domain", "")
	}
	if owner != caller {
		return deny("cross-domain access", owner)
	}
	return true
}

// GoneClientStatusMutation replaces the removed PATCH /status route.
func (h *Handler) GoneClientStatusMutation(c *gin.Context) {
	slog.Warn("payments: a caller attempted the removed client status mutation",
		"path", c.Request.URL.Path, "caller", callerDomain(c))
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, "GONE",
		"payment status cannot be asserted by a caller; terminal state comes only from a "+
			"signature-verified provider webhook or a server-side provider fetch", nil)
}

// InitiateRefund POST /v1/payments/intents/:id/refund — SERVICE ONLY.
//
// Returns 202: a refund has been ACCEPTED and made durable. It has not been
// paid. The old handler returned 200 with a "refunded" intent before the
// provider had been asked, which is what let a provider outage produce a
// ledger that lied.
func (h *Handler) InitiateRefund(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid intent id", nil)
		return
	}
	if !h.ownsIntent(c, id) {
		return
	}
	var body struct {
		Reason string `json:"reason"`
		// AmountMinor must be explicit. The old "0 means refund everything"
		// convention turned a serialisation slip into a full refund.
		AmountMinor int64 `json:"amount_minor" binding:"required"`
		// IdempotencyKey is deterministic and caller-derived.
		IdempotencyKey string `json:"idempotency_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	cmd, err := h.svc.RequestRefund(c.Request.Context(), service.RefundRequest{
		IntentID:               id,
		AmountMinor:            body.AmountMinor,
		Reason:                 body.Reason,
		ProviderIdempotencyKey: body.IdempotencyKey,
		CallerDomain:           callerDomain(c),
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REFUND_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusAccepted, gin.H{
		"command_id":   cmd.ID,
		"intent_id":    cmd.IntentID,
		"amount_minor": cmd.AmountMinor,
		"status":       cmd.Status,
		"note":         "refund accepted and durable; settlement is confirmed by the provider webhook",
	}, nil)
}

// VerifyIntent POST /v1/payments/intents/:id/verify — ADVISORY.
//
// A1/R-3: this no longer mutates anything. It reports whether a client
// callback is authentic so the app can stop spinning; the caller must not
// treat a positive verdict as payment.
func (h *Handler) VerifyIntent(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid intent id", nil)
		return
	}
	if !h.ownsIntent(c, id) {
		return
	}
	var body struct {
		RazorpayOrderID   string `json:"razorpay_order_id" binding:"required"`
		RazorpayPaymentID string `json:"razorpay_payment_id" binding:"required"`
		RazorpaySignature string `json:"razorpay_signature" binding:"required"`
		AmountMinor       int64  `json:"amount_minor,omitempty"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	result, err := h.svc.VerifyIntent(c.Request.Context(), id,
		body.RazorpayOrderID, body.RazorpayPaymentID, body.RazorpaySignature, body.AmountMinor)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "VERIFY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// ListByReference GET /v1/payments/intents?ref_type=order&ref_id=uuid
func (h *Handler) ListByReference(c *gin.Context) {
	refType := c.Query("ref_type")
	refIDStr := c.Query("ref_id")
	if refType == "" || refIDStr == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "MISSING_PARAMS", "ref_type and ref_id required", nil)
		return
	}
	refID, err := uuid.Parse(refIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REF_ID", "invalid ref_id", nil)
		return
	}
	intents, err := h.svc.ListByReference(c.Request.Context(), refType, refID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, intents, nil)
}

// ─── Webhook ─────────────────────────────────────────────────────────

// HandleWebhook POST /v1/payments/webhook
//
// Fail-closed at every step (LB-6, A3):
//
//	no provider adapter        -> 503, never "accept and hope"
//	no/blank signature         -> 401
//	bad signature              -> 401
//	missing provider event id  -> 400  (R-5: an empty inbox key masks every
//	                                    later payment once one event takes it)
//	dedupe/effect error        -> 500, so the provider RETRIES
//
// The last one matters most. The old handler logged a dedupe failure and
// processed anyway, and inserted the inbox row before applying the effect —
// so a crash in between converted the provider's retry into a silent 200
// with the money never recorded.
func (h *Handler) HandleWebhook(c *gin.Context) {
	if h.provider == nil {
		slog.Error("payments: webhook received with no provider adapter configured")
		c.Status(http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	ev, err := h.provider.VerifyWebhook(c.Request.Context(), c.Request.Header, body)
	switch {
	case errors.Is(err, gateway.ErrSignatureInvalid):
		// Never log the body or the signature: one is PII-bearing, the
		// other is secret-adjacent.
		slog.Warn("payments: webhook signature verification failed", "provider", h.provider.Name())
		c.Status(http.StatusUnauthorized)
		return
	case errors.Is(err, gateway.ErrReplayWindowExpired):
		slog.Warn("payments: webhook outside the replay window", "provider", h.provider.Name())
		c.Status(http.StatusUnauthorized)
		return
	case errors.Is(err, gateway.ErrMissingEventID):
		slog.Error("payments: webhook has no provider event id; refusing to process without a dedupe key",
			"provider", h.provider.Name())
		c.Status(http.StatusBadRequest)
		return
	case err != nil:
		slog.Error("payments: webhook could not be verified", "provider", h.provider.Name(), "error", err)
		c.Status(http.StatusBadRequest)
		return
	}

	err = h.svc.ApplyWebhook(c.Request.Context(), service.WebhookInput{
		Provider:          h.provider.Name(),
		EventID:           ev.EventID,
		EventType:         ev.Type,
		ProviderOrderID:   ev.ProviderOrderID,
		ProviderPaymentID: ev.ProviderPaymentID,
		ProviderRefundID:  ev.ProviderRefundID,
		AmountMinor:       ev.Amount.Minor,
		// B2: the currency travels with the amount. An INR intent settled by
		// an event denominated in something else is a mismatch, and the
		// comparison now happens inside the terminal transaction.
		Currency: ev.Amount.Currency,
	})
	switch {
	case errors.Is(err, service.ErrWebhookDuplicate):
		// Genuinely already applied. 200 stops the retries.
		c.Status(http.StatusOK)
		return
	case err != nil:
		// 500 so the provider retries. Acknowledging an event we failed to
		// apply is the one outcome that silently loses money.
		slog.Error("payments: webhook effect failed; returning 500 so the provider retries",
			"provider", h.provider.Name(), "event_id", ev.EventID, "error", err)
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusOK)
}

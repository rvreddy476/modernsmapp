// Premium (lane P2): one-off passes and Boost, paid through payments-service.
//
//   - PremiumCatalogue: the server-side price list.
//   - CreatePremiumPurchase: idempotent per (user, key); prices the purchase
//     from the catalogue, opens a payments intent under application dating
//     and relays the public checkout session. It never grants anything.
//   - PremiumPurchasePayment: confirming | paid | failed plus refund status,
//     for the purchase's own buyer only.
//   - MyPremium: per-feature entitlements with expires_at, and the Boost
//     balance.
//   - OnPremiumPaymentApplied: the payment consumer's post-commit hook.
//   - SendPremiumExpiryReminders: "expiring soon", once per purchase.
//
// The grant itself is store.ApplyPremiumPaymentEvent, from a payments event.
package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/dating-service/internal/payments"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/paymentmethod"
	"github.com/google/uuid"
)

// PremiumPaymentsClient is the part of *payments.Client the service uses.
type PremiumPaymentsClient interface {
	CreateIntent(ctx context.Context, in payments.CreateIntentInput) (*payments.Intent, error)
}

var (
	// ErrPremiumUnavailable: no payments client (DATING_SERVICE_TOKEN_KEY /
	// KID unset or unusable). HTTP 503 PREMIUM_UNAVAILABLE.
	ErrPremiumUnavailable = errors.New("premium purchases are unavailable")
	// ErrPremiumCheckoutUnavailable: outside local/dev, payments returned an
	// intent with no usable checkout session (the stub gateway). HTTP 503
	// PREMIUM_UNAVAILABLE.
	ErrPremiumCheckoutUnavailable = errors.New("payments returned no checkout session")
	// ErrPremiumProductUnknown: not a catalogue product. HTTP 400 INVALID_PRODUCT.
	ErrPremiumProductUnknown = errors.New("unknown premium product")
	// ErrPremiumMethodInvalid: not a launch payment method. HTTP 400
	// PAYMENT_METHOD_INVALID.
	ErrPremiumMethodInvalid = errors.New("payment method is invalid")
	// ErrPremiumIdempotencyKeyRequired: missing or longer than 128. HTTP 400.
	ErrPremiumIdempotencyKeyRequired = errors.New("idempotency_key is required (1-128 characters)")
)

// PremiumExpiryReminderLead is how long before a pass expires the reminder
// is sent.
const PremiumExpiryReminderLead = 72 * time.Hour

// SetPremiumPayments wires the payments client. env is ENV: outside
// local/dev an intent without a checkout session is refused.
func (s *Service) SetPremiumPayments(c PremiumPaymentsClient, env string) {
	s.premiumPayments = c
	s.premiumLocalEnv = payments.IsLocalEnv(env)
}

// SetConsentPolicyVersion overrides the policy-version stamp used when
// logging consent. Defaults to "v1.0-2026-04-29".
func (s *Service) SetConsentPolicyVersion(v string) {
	if v != "" {
		s.consentPolicyVersion = v
	}
}

// consentPolicy returns the configured policy version (or default).
func (s *Service) consentPolicy() string {
	if s.consentPolicyVersion != "" {
		return s.consentPolicyVersion
	}
	return "v1.0-2026-04-29"
}

// PremiumCatalogue is GET /v1/dating/premium/catalogue.
func (s *Service) PremiumCatalogue() []payments.Product {
	return payments.Catalogue()
}

// PremiumPurchaseInput is the client's request: a product, a key and an
// optional method. There is deliberately no amount.
type PremiumPurchaseInput struct {
	Product        string
	IdempotencyKey string
	Method         string
}

// PremiumPurchaseResult is the purchase plus, while it can still be paid, the
// public checkout session exactly as food relays it (omitted when payments
// attached none).
type PremiumPurchaseResult struct {
	Purchase      *store.PremiumPurchase  `json:"purchase"`
	ClientSession *payments.ClientSession `json:"client_session,omitempty"`
}

// CreatePremiumPurchase opens (or, for the same key, re-opens) a purchase.
// created reports whether this call inserted it.
func (s *Service) CreatePremiumPurchase(ctx context.Context, userID uuid.UUID, in PremiumPurchaseInput) (*PremiumPurchaseResult, bool, error) {
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" || len(key) > 128 {
		return nil, false, ErrPremiumIdempotencyKeyRequired
	}
	product, ok := payments.LookupProduct(strings.TrimSpace(in.Product))
	if !ok {
		return nil, false, ErrPremiumProductUnknown
	}
	method := in.Method
	if strings.TrimSpace(method) == "" {
		method = paymentmethod.UPI
	}
	if err := paymentmethod.Validate(method); err != nil {
		return nil, false, ErrPremiumMethodInvalid
	}
	if s.premiumPayments == nil {
		return nil, false, ErrPremiumUnavailable
	}
	purchase, created, err := s.store.CreateOrGetPremiumPurchase(ctx, userID, product, method, key)
	if err != nil {
		return nil, false, err
	}
	out := &PremiumPurchaseResult{Purchase: purchase}
	if purchase.Status != payments.StatusCreated && purchase.Status != payments.StatusConfirming {
		// Paid, failed or refunded: nothing left to pay.
		return out, created, nil
	}
	// The amount is the purchase's, priced from the catalogue when it was
	// created; the client cannot influence it. A retry sends the same
	// deterministic payments key and gets the same intent back.
	upstream, err := s.premiumPayments.CreateIntent(ctx, payments.CreateIntentInput{
		PurchaseID:  purchase.ID,
		PayerID:     userID,
		AmountMinor: purchase.AmountMinor,
		Currency:    purchase.Currency,
		Method:      purchase.Method,
	})
	if err != nil {
		return nil, created, err
	}
	purchase, err = s.store.AttachPremiumIntent(ctx, userID, purchase.ID, upstream.ID, upstream.ProviderRef)
	if err != nil {
		return nil, created, err
	}
	out.Purchase = purchase
	out.ClientSession = upstream.PublicClientSession()
	if out.ClientSession == nil && !s.premiumLocalEnv {
		slog.Error("dating: payments returned an intent with no checkout session outside local/dev; refusing (stub gateway?)",
			"purchase_id", purchase.ID, "intent_id", upstream.ID)
		return nil, created, ErrPremiumCheckoutUnavailable
	}
	return out, created, nil
}

// PremiumPaymentStatus is GET /v1/dating/premium/purchases/:id/payment.
type PremiumPaymentStatus struct {
	PurchaseID uuid.UUID `json:"purchase_id"`
	Product    string    `json:"product"`
	// Status is confirming | paid | failed.
	Status      string `json:"status"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	// RefundStatus is null, partially_refunded or refunded.
	RefundStatus *string   `json:"refund_status"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// PremiumPurchasePayment reports a purchase's payment to its own buyer.
// Another user's purchase is store.ErrPurchaseNotFound (404). It never writes
// and never asks payments-service: `paid` comes only from an applied event.
func (s *Service) PremiumPurchasePayment(ctx context.Context, userID, purchaseID uuid.UUID) (*PremiumPaymentStatus, error) {
	st, err := s.store.PremiumPurchasePayment(ctx, userID, purchaseID)
	if err != nil {
		return nil, err
	}
	status, refund := payments.CustomerStatus(payments.PurchasePaymentSnapshot{Status: st.Status, CaptureApplied: st.CaptureApplied})
	out := &PremiumPaymentStatus{
		PurchaseID: st.PurchaseID, Product: st.Product, Status: status,
		AmountMinor: st.AmountMinor, Currency: st.Currency, UpdatedAt: st.UpdatedAt.UTC(),
	}
	if refund != "" {
		out.RefundStatus = &refund
	}
	return out, nil
}

// PremiumPassView is the user's pass. Null when they never held one.
type PremiumPassView struct {
	Product   string    `json:"product"`
	Active    bool      `json:"active"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PremiumFeatureEntitlement is one premium feature.
type PremiumFeatureEntitlement struct {
	Feature string `json:"feature"`
	Active  bool   `json:"active"`
	// ExpiresAt is the pass expiry; null when the user never held a pass.
	ExpiresAt *time.Time `json:"expires_at"`
}

// MyPremiumResponse is GET /v1/dating/premium/me.
type MyPremiumResponse struct {
	IsPremium    bool                        `json:"is_premium"`
	Pass         *PremiumPassView            `json:"pass"`
	Entitlements []PremiumFeatureEntitlement `json:"entitlements"`
	BoostBalance int                         `json:"boost_balance"`
}

// MyPremium returns the user's premium state. A pass is active only while
// expires_at > now(); there is no "forever".
func (s *Service) MyPremium(ctx context.Context, userID uuid.UUID) (*MyPremiumResponse, error) {
	if userID == uuid.Nil {
		return nil, errors.New("invalid: user_id required")
	}
	ent, err := s.store.GetPremiumEntitlement(ctx, userID)
	if err != nil {
		return nil, err
	}
	active := ent.PassActive && ent.PassExpiresAt != nil
	out := &MyPremiumResponse{IsPremium: active, BoostBalance: ent.BoostBalance}
	var expires *time.Time
	if ent.PassExpiresAt != nil {
		e := ent.PassExpiresAt.UTC()
		expires = &e
		out.Pass = &PremiumPassView{Product: ent.PassProduct, Active: active, ExpiresAt: e}
	}
	for _, f := range payments.PassFeatures {
		out.Entitlements = append(out.Entitlements, PremiumFeatureEntitlement{Feature: f, Active: active, ExpiresAt: expires})
	}
	return out, nil
}

// OnPremiumPaymentApplied is the payment consumer's post-commit hook. It only
// announces; the entitlement change already committed.
func (s *Service) OnPremiumPaymentApplied(ctx context.Context, a payments.Applied) {
	product, _ := payments.LookupProduct(a.Product)
	switch a.Decision.Effect {
	case payments.EffectGrant:
		if err := s.store.RecordConsent(ctx, a.UserID, "payments", true, s.consentPolicy()); err != nil {
			slog.Warn("dating: record payments consent failed", "user_id", a.UserID, "error", err)
		}
		if product.Kind == payments.KindPass && a.PassExpiresAt != nil && s.producer != nil {
			if err := s.producer.PublishPremiumSubscribed(ctx, a.UserID, a.Product, *a.PassExpiresAt, "payments"); err != nil {
				slog.Warn("dating: publish premium.subscribed failed; pass granted", "user_id", a.UserID, "error", err)
			}
		}
	case payments.EffectMarkFailed:
		if s.producer != nil {
			if err := s.producer.PublishPremiumPaymentFailure(ctx, a.UserID, a.Product, "payment_failed"); err != nil {
				slog.Warn("dating: publish premium.payment_failure failed", "user_id", a.UserID, "error", err)
			}
		}
	case payments.EffectRevoke, payments.EffectPartialRevoke:
		if product.Kind == payments.KindPass && a.PassExpiresAt != nil && !a.PassExpiresAt.After(time.Now()) && s.producer != nil {
			if err := s.producer.PublishPremiumExpired(ctx, a.UserID, a.Product); err != nil {
				slog.Warn("dating: publish premium.expired failed; pass revoked", "user_id", a.UserID, "error", err)
			}
		}
	}
}

// SendPremiumExpiryReminders claims passes expiring within
// PremiumExpiryReminderLead and emits dating.premium.expiring_soon once per
// purchase. The claim commits before the publish, so a failed publish loses
// that reminder rather than repeating it.
func (s *Service) SendPremiumExpiryReminders(ctx context.Context, limit int) (int, error) {
	if s.producer == nil {
		return 0, nil
	}
	due, err := s.store.ClaimPremiumExpiryReminders(ctx, PremiumExpiryReminderLead, limit)
	if err != nil {
		return 0, err
	}
	for _, r := range due {
		if err := s.producer.PublishPremiumExpiringSoon(ctx, r.UserID, r.PurchaseID, r.Product, r.ExpiresAt); err != nil {
			slog.Warn("dating: publish premium.expiring_soon failed; reminder not re-sent", "purchase_id", r.PurchaseID, "error", err)
		}
	}
	return len(due), nil
}

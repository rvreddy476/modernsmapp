package service

// Captain subscription checkout through payments-service (application
// mopedu, reference type mopedu_subscription, reference id = the
// subscription row). POST /v1/rider/subscriptions/checkout opens (or
// re-opens) the intent for the plan price; the row is activated ONLY by the
// consumer applying the signed payment.succeeded event
// (store.ApplyRidePaymentEvent). The free trial (price 0) activates here,
// once per partner ever. A renewal opens a new checkout row that, on
// capture, starts where the current period ends.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/payments"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// Subscription checkout error codes.
const (
	CodePlanNotFound          = "PLAN_NOT_FOUND"
	CodeTrialAlreadyUsed      = "TRIAL_ALREADY_USED"
	CodeSubscriptionProofGone = "SUBSCRIPTION_PROOF_GONE"
	CodePartnerNotEligible    = "PARTNER_NOT_ELIGIBLE"
)

// SubscriptionCheckout is the checkout response.
type SubscriptionCheckout struct {
	SubscriptionID uuid.UUID               `json:"subscription_id"`
	IntentID       *uuid.UUID              `json:"intent_id"`
	AmountPaise    int64                   `json:"amount_paise"`
	Currency       string                  `json:"currency"`
	ClientSession  *payments.ClientSession `json:"client_session"`
	// Status is pending_payment until the signed capture activates the row;
	// trial when the free plan activated instantly.
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// SubscriptionPaymentStatus is GET /v1/rider/subscriptions/me/payment.
type SubscriptionPaymentStatus struct {
	// Status: pending (no intent yet) | confirming (intent open, waiting for
	// the signed capture) | paid | failed.
	Status         string     `json:"status"`
	SubscriptionID uuid.UUID  `json:"subscription_id"`
	IntentID       *uuid.UUID `json:"intent_id"`
	AmountPaise    int64      `json:"amount_paise"`
	// ExpiresAt is the paid period's end; null until paid.
	ExpiresAt *time.Time `json:"expires_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// CheckoutSubscription creates (or reuses the open) checkout row for the
// plan and opens the payments intent; the trial plan activates instantly.
func (s *Service) CheckoutSubscription(ctx context.Context, userID uuid.UUID, planCode, method string) (*SubscriptionCheckout, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	planCode = strings.TrimSpace(planCode)
	if planCode == "" {
		return nil, fmt.Errorf("invalid: plan_code required")
	}
	partner, err := s.GetMyPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	if partner.Status == "suspended" || partner.Status == "blocked" || partner.Status == "rejected" {
		return nil, payErr(http.StatusForbidden, CodePartnerNotEligible, "partner is "+partner.Status)
	}
	plan, err := s.store.GetPlanByCode(ctx, planCode)
	if err != nil {
		if errors.Is(err, store.ErrPlanNotFound) {
			return nil, payErr(http.StatusNotFound, CodePlanNotFound, "plan not found")
		}
		return nil, err
	}
	if !plan.IsActive {
		return nil, payErr(http.StatusNotFound, CodePlanNotFound, "plan is not active")
	}
	amountPaise := int64(math.Round(plan.PriceAmount * 100))
	if amountPaise <= 0 {
		return s.activateTrial(ctx, partner, plan)
	}
	method = strings.TrimSpace(method)
	if err := validateOnlineMethod(method); err != nil {
		return nil, err
	}
	if s.payments == nil {
		return nil, payErr(http.StatusServiceUnavailable, CodePaymentsUnavailable, "online payments are not available")
	}
	sub, err := s.store.GetOpenCheckout(ctx, partner.ID, plan.ID)
	if errors.Is(err, store.ErrSubscriptionNotFound) {
		in := store.CreateCheckoutInput{PartnerID: partner.ID, PlanID: plan.ID, AmountPaise: amountPaise}
		// A renewal while a period is running extends from its expiry.
		if current, cerr := s.store.GetActiveSubscription(ctx, partner.ID); cerr == nil {
			id := current.ID
			in.RenewsSubscriptionID = &id
		}
		sub, err = s.store.CreateCheckoutSubscription(ctx, in)
	}
	if err != nil {
		return nil, err
	}
	intent, err := s.payments.CreateSubscriptionIntent(ctx, payments.CreateIntentInput{
		ReferenceID: sub.ID, PayerID: userID, AmountMinor: sub.AmountPaise, Method: method,
		IdempotencyKey: payments.SubscriptionIntentKey(sub.ID, method),
	})
	if err != nil {
		return nil, mapPaymentsError(err)
	}
	if intent.AmountMinor != sub.AmountPaise {
		return nil, fmt.Errorf("payments echoed amount %d for a %d checkout", intent.AmountMinor, sub.AmountPaise)
	}
	bound, err := s.store.BindSubscriptionIntent(ctx, sub.ID, intent.ID, intent.ProviderRef, method)
	if err != nil {
		if errors.Is(err, store.ErrCheckoutNotBindable) {
			return nil, payErr(http.StatusConflict, CodePaymentNotPending, "the checkout changed; refresh")
		}
		return nil, err
	}
	cs := intent.PublicClientSession()
	if cs == nil && !s.paymentsLocalEnv {
		slog.Error("rider: payments returned a subscription intent with no checkout session outside local/dev; refusing",
			"subscription_id", sub.ID, "intent_id", intent.ID)
		return nil, payErr(http.StatusServiceUnavailable, CodePaymentsCheckoutUnavailable, "checkout is not available")
	}
	id := intent.ID
	return &SubscriptionCheckout{SubscriptionID: bound.ID, IntentID: &id, AmountPaise: bound.AmountPaise, Currency: payments.CurrencyINR,
		ClientSession: cs, Status: bound.Status}, nil
}

// activateTrial activates the free plan instantly, once per partner ever.
func (s *Service) activateTrial(ctx context.Context, partner *store.Partner, plan *store.SubscriptionPlan) (*SubscriptionCheckout, error) {
	sub, err := s.store.ActivateTrialSubscription(ctx, partner.ID, plan.ID, s.now(), plan.BillingPeriodDays)
	if err != nil {
		if errors.Is(err, store.ErrTrialAlreadyUsed) {
			return nil, payErr(http.StatusConflict, CodeTrialAlreadyUsed, "the free trial can be used once; choose a paid plan")
		}
		return nil, err
	}
	if perr := s.producer.PublishSubscriptionActivated(ctx, sub.ID, partner.ID, partner.UserID, plan.ID, sub.Status, sub.StartsAt, sub.ExpiresAt); perr != nil {
		slog.Warn("rider: publish subscription.activated failed", "subscription_id", sub.ID, "error", perr)
	}
	exp := sub.ExpiresAt
	return &SubscriptionCheckout{SubscriptionID: sub.ID, AmountPaise: 0, Currency: payments.CurrencyINR, Status: sub.Status, ExpiresAt: &exp}, nil
}

// GetMySubscriptionPayment reports the partner's latest checkout. It never
// asks payments-service; paid comes only from the applied signed event.
func (s *Service) GetMySubscriptionPayment(ctx context.Context, userID uuid.UUID) (*SubscriptionPaymentStatus, error) {
	partner, err := s.GetMyPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	sub, err := s.store.LatestCheckout(ctx, partner.ID)
	if err != nil {
		if errors.Is(err, store.ErrSubscriptionNotFound) {
			return nil, fmt.Errorf("not_found: no subscription checkout")
		}
		return nil, err
	}
	st := &SubscriptionPaymentStatus{Status: payments.SubscriptionPaymentPending, SubscriptionID: sub.ID, IntentID: sub.IntentID, AmountPaise: sub.AmountPaise, UpdatedAt: sub.UpdatedAt}
	if sub.PaymentStatus != nil {
		st.Status = *sub.PaymentStatus
	}
	if st.Status == payments.SubscriptionPaymentPaid {
		exp := sub.ExpiresAt
		st.ExpiresAt = &exp
	}
	return st, nil
}

// onSubscriptionPaymentApplied runs after the consumer committed a
// subscription effect: the existing subscription-activated event (with
// partner_user_id) and a realtime frame on the partner topic.
func (s *Service) onSubscriptionPaymentApplied(ctx context.Context, a payments.Applied) {
	sub, err := s.store.GetSubscription(ctx, a.TargetID)
	if err != nil {
		slog.Warn("rider: subscription after payment event not readable", "subscription_id", a.TargetID, "error", err)
		return
	}
	partner, err := s.store.GetPartner(ctx, sub.PartnerID)
	if err != nil {
		return
	}
	status := ""
	if sub.PaymentStatus != nil {
		status = *sub.PaymentStatus
	}
	s.publishRealtime(ctx, "rider.partner."+partner.ID.String()+".offers", "rider.subscription.payment.updated",
		map[string]any{"subscription_id": sub.ID, "status": sub.Status, "payment_status": status, "expires_at": sub.ExpiresAt})
	if a.Decision.Effect != payments.EffectActivateSubscription {
		return
	}
	if perr := s.producer.PublishSubscriptionActivated(ctx, sub.ID, partner.ID, partner.UserID, sub.PlanID, sub.Status, sub.StartsAt, sub.ExpiresAt); perr != nil {
		slog.Warn("rider: publish subscription.activated failed", "subscription_id", sub.ID, "error", perr)
	}
}

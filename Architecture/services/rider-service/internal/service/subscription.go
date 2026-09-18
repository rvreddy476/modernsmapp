package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// SubscribeOperation is the idempotency-table operation label.
const SubscribeOperation = "subscribe"

// allowedPaymentMethods covers the values rider_subscription_payments.payment_method
// takes on the legacy Subscribe route: the wallet only. "manual" (a proof
// upload verified by an admin) is gone; upi / card go through
// CheckoutSubscription and payments-service.
var allowedPaymentMethods = map[string]bool{
	"wallet": true,
}

// ErrProofRouteGone answers the retired payment-proof route.
var ErrProofRouteGone = payErr(http.StatusGone, CodeSubscriptionProofGone,
	"payment proofs are no longer accepted; pay in the app through POST /v1/rider/subscriptions/checkout")

// SubscribeResult is the response shape from Subscribe.
type SubscribeResult struct {
	PaymentID      uuid.UUID  `json:"payment_id"`
	SubscriptionID *uuid.UUID `json:"subscription_id,omitempty"`
	Status         string     `json:"status"`
	AmountPaise    int64      `json:"amount_paise"`
	UPIIntentURL   string     `json:"upi_intent_url,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

// ListPlans returns every active plan. Public (read-only) surface for the
// partner onboarding screen.
func (s *Service) ListPlans(ctx context.Context) ([]store.SubscriptionPlan, error) {
	return s.store.ListActivePlans(ctx)
}

// GetMySubscription returns the partner's current subscription (active /
// trial / grace_period). 404 (not_found:) when nothing matches.
func (s *Service) GetMySubscription(ctx context.Context, userID uuid.UUID) (*store.PartnerSubscription, error) {
	p, err := s.GetMyPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	sub, err := s.store.GetActiveSubscription(ctx, p.ID)
	if err != nil {
		if errors.Is(err, store.ErrSubscriptionNotFound) {
			return nil, fmt.Errorf("not_found: no active subscription")
		}
		return nil, err
	}
	return sub, nil
}

// Subscribe is the centerpiece of Sprint 1. Creates a subscription_payment row
// and (for wallet payments) also activates the subscription.
//
// Payment-method behavior:
//   - wallet — debit via wallet-service /v1/wallet/internal/debit. On success
//     mark payment verified + activate subscription. On failure mark payment
//     failed and surface the error.
//   - upi    — return a UPI intent URL the partner opens; payment stays
//     pending until /payment-proof is uploaded (admin verifies in S3).
//   - manual — return a payment id; partner uploads proof out-of-band; admin
//     verifies in S3.
//
// Idempotent on idempotencyKey via rider_idempotency. A replay returns the
// cached response body (so the original payment_id is preserved).
func (s *Service) Subscribe(ctx context.Context, userID uuid.UUID, planID uuid.UUID, paymentMethod, idempotencyKey string) (*SubscribeResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	if planID == uuid.Nil {
		return nil, fmt.Errorf("invalid: plan_id required")
	}
	switch {
	case paymentMethod == "upi" || paymentMethod == "card":
		return nil, fmt.Errorf("invalid: pay by %s through POST /v1/rider/subscriptions/checkout", paymentMethod)
	case !allowedPaymentMethods[paymentMethod]:
		return nil, fmt.Errorf("invalid: payment_method must be wallet (upi and card go through /subscriptions/checkout)")
	}
	if idempotencyKey == "" {
		return nil, fmt.Errorf("invalid: idempotency_key required")
	}

	reqFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s", userID, planID, paymentMethod))))

	// Idempotency replay: if we've seen this key for this user + Subscribe,
	// return the cached response body verbatim.
	if existing, err := s.store.FindIdempotency(ctx, idempotencyKey, userID, SubscribeOperation, reqFingerprint); err == nil {
		var cached SubscribeResult
		if len(existing.ResponseBody) > 0 {
			if err := json.Unmarshal(existing.ResponseBody, &cached); err == nil && cached.PaymentID != uuid.Nil {
				return &cached, nil
			}
		}
		// No body cached — fall through and re-derive. The unique-key insert
		// below will be a no-op so we end up with the same payment id.
	} else if !errors.Is(err, store.ErrIdempotencyKeyNotFound) {
		return nil, err
	}

	partner, err := s.GetMyPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	plan, err := s.store.GetPlan(ctx, planID)
	if err != nil {
		if errors.Is(err, store.ErrPlanNotFound) {
			return nil, fmt.Errorf("invalid: plan_id not found")
		}
		return nil, err
	}
	if !plan.IsActive {
		return nil, fmt.Errorf("invalid: plan is not active")
	}
	// draft is OK — we accept subscriptions before approval; admin verifies
	// during the partner-approval step. Block only suspended/blocked.
	if partner.Status == "suspended" || partner.Status == "blocked" {
		return nil, fmt.Errorf("forbidden: partner is suspended or blocked")
	}

	amountPaise := int64(math.Round(plan.PriceAmount * 100))
	payment, err := s.store.CreateSubscriptionPayment(ctx, store.CreateSubscriptionPaymentInput{
		PartnerID:     partner.ID,
		PlanID:        plan.ID,
		Amount:        plan.PriceAmount,
		CurrencyCode:  plan.CurrencyCode,
		PaymentMethod: paymentMethod,
		Status:        "pending",
	})
	if err != nil {
		return nil, fmt.Errorf("create payment: %w", err)
	}
	if perr := s.producer.PublishSubscriptionPaymentSubmitted(ctx, payment.ID, partner.ID, plan.ID, plan.PriceAmount, plan.CurrencyCode, paymentMethod); perr != nil {
		slog.Warn("rider: publish payment.submitted failed", "payment_id", payment.ID, "error", perr)
	}

	res := &SubscribeResult{
		PaymentID:   payment.ID,
		Status:      "pending",
		AmountPaise: amountPaise,
	}

	switch paymentMethod {
	case "wallet":
		// A zero-cost (trial) plan skips the wallet and activates instantly,
		// once per partner ever (rider_partners.trial_used_at).
		if amountPaise == 0 {
			sub, activateErr := s.store.ActivateTrialSubscription(ctx, partner.ID, plan.ID, s.now(), plan.BillingPeriodDays)
			if activateErr != nil {
				_ = s.store.MarkPaymentFailed(ctx, payment.ID, activateErr.Error())
				if errors.Is(activateErr, store.ErrTrialAlreadyUsed) {
					return nil, payErr(http.StatusConflict, CodeTrialAlreadyUsed, "the free trial can be used once; choose a paid plan")
				}
				return nil, activateErr
			}
			if _, verr := s.store.MarkPaymentVerified(ctx, payment.ID, nil, &sub.ID); verr != nil {
				return nil, fmt.Errorf("mark payment verified: %w", verr)
			}
			if perr := s.producer.PublishSubscriptionActivated(ctx, sub.ID, partner.ID, partner.UserID, plan.ID, sub.Status, sub.StartsAt, sub.ExpiresAt); perr != nil {
				slog.Warn("rider: publish subscription.activated failed", "subscription_id", sub.ID, "error", perr)
			}
			res.SubscriptionID = &sub.ID
			res.Status = sub.Status
			res.ExpiresAt = &sub.ExpiresAt
		} else {
			if s.wallet == nil {
				return nil, fmt.Errorf("wallet client not configured")
			}
			debit, derr := s.wallet.DebitForSubscription(ctx, partner.UserID, amountPaise, payment.ID, idempotencyKey)
			if derr != nil {
				_ = s.store.MarkPaymentFailed(ctx, payment.ID, derr.Error())
				return nil, fmt.Errorf("wallet debit: %w", derr)
			}
			sub, activateErr := s.activateSubscriptionWithPlan(ctx, partner, plan, payment.ID, &debit.TransactionID)
			if activateErr != nil {
				// Best-effort refund; log loudly on failure (rule: no silent fail).
				if rerr := s.wallet.RefundSubscription(ctx, debit.TransactionID, amountPaise, "rider activation failed"); rerr != nil {
					slog.Error("rider: wallet refund after activation-failure also failed",
						"payment_id", payment.ID, "wallet_txn", debit.TransactionID, "error", rerr)
				}
				_ = s.store.MarkPaymentFailed(ctx, payment.ID, activateErr.Error())
				return nil, activateErr
			}
			res.SubscriptionID = &sub.ID
			res.Status = sub.Status
			res.ExpiresAt = &sub.ExpiresAt
		}
	}

	if body, merr := json.Marshal(res); merr == nil {
		_ = s.store.RecordIdempotency(ctx, idempotencyKey, userID, SubscribeOperation, reqFingerprint, &payment.ID, body)
	}
	return res, nil
}

// SubmitPaymentProof updates a manual / UPI payment with the proof URL the
// partner uploaded out-of-band. Status flips to `submitted` (admin verifies
// in S3).
//
// Retired (launch safety): a subscription is paid in the app through
// CheckoutSubscription and settled by the signed payment event; no proof, no
// admin. The route answers 410 with the pointer; nothing is written.
func (s *Service) SubmitPaymentProof(_ context.Context, _, _ uuid.UUID, _ string) (*store.SubscriptionPayment, error) {
	return nil, ErrProofRouteGone
}

// ActivateSubscription is the public hook used by admin (S3) and the wallet
// path to flip a payment to `verified` and create / update the subscription.
//
// activatedBy is the actor id (admin user) recorded on the payment row.
// Pass uuid.Nil for system / wallet-driven activations.
func (s *Service) ActivateSubscription(ctx context.Context, paymentID uuid.UUID, activatedBy uuid.UUID) (*store.PartnerSubscription, error) {
	pay, err := s.store.GetSubscriptionPayment(ctx, paymentID)
	if err != nil {
		if errors.Is(err, store.ErrPaymentNotFound) {
			return nil, fmt.Errorf("not_found: payment")
		}
		return nil, err
	}
	partner, err := s.store.GetPartner(ctx, pay.PartnerID)
	if err != nil {
		return nil, err
	}
	plan, err := s.store.GetPlan(ctx, pay.PlanID)
	if err != nil {
		return nil, err
	}
	return s.activateSubscriptionWithPlan(ctx, partner, plan, pay.ID, pay.WalletTxnID)
}

// activateSubscriptionWithPlan is the shared core. Creates a subscription row
// with status='trial' for the trial plan or 'active' otherwise, expires_at
// = now() + plan.billing_period_days, marks the payment verified, and emits
// EventRiderSubscriptionActivated.
func (s *Service) activateSubscriptionWithPlan(ctx context.Context, partner *store.Partner, plan *store.SubscriptionPlan, paymentID uuid.UUID, walletTxnID *uuid.UUID) (*store.PartnerSubscription, error) {
	now := time.Now().UTC()
	expiry := now.AddDate(0, 0, plan.BillingPeriodDays)
	status := "active"
	if plan.Code == "trial_7d" || plan.PriceAmount == 0 {
		status = "trial"
	}
	sub, err := s.store.CreateSubscription(ctx, store.CreateSubscriptionInput{
		PartnerID: partner.ID,
		PlanID:    plan.ID,
		Status:    status,
		StartsAt:  now,
		ExpiresAt: expiry,
	})
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}
	if _, err := s.store.MarkPaymentVerified(ctx, paymentID, walletTxnID, &sub.ID); err != nil {
		return nil, fmt.Errorf("mark payment verified: %w", err)
	}
	if perr := s.producer.PublishSubscriptionPaymentVerified(ctx, paymentID, partner.ID, plan.ID, plan.PriceAmount, plan.CurrencyCode, "wallet"); perr != nil {
		slog.Warn("rider: publish payment.verified failed", "payment_id", paymentID, "error", perr)
	}
	if perr := s.producer.PublishSubscriptionActivated(ctx, sub.ID, partner.ID, partner.UserID, plan.ID, sub.Status, sub.StartsAt, sub.ExpiresAt); perr != nil {
		slog.Warn("rider: publish subscription.activated failed", "subscription_id", sub.ID, "error", perr)
	}
	return sub, nil
}

// buildUPIIntent constructs an upi:// intent URL targeting AtPost's pool VPA.
// The mobile app passes this through to the partner's UPI app (BHIM, GPay…).
//
// Spec: https://www.npci.org.in/PDF/npci/upi/upi-linking-specs.pdf §3.
func buildUPIIntent(amountPaise int64, paymentID uuid.UUID) string {
	q := url.Values{}
	q.Set("pa", "atpostwallet@partnerbank")
	q.Set("pn", "AtPost Mopedu")
	q.Set("tn", "Mopedu Subscription")
	q.Set("am", fmt.Sprintf("%.2f", float64(amountPaise)/100.0))
	q.Set("cu", "INR")
	q.Set("tr", "rider-"+paymentID.String())
	return "upi://pay?" + q.Encode()
}

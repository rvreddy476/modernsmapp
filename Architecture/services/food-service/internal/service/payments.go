package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// PaymentsClient is the part of *payments.Client the service uses.
type PaymentsClient interface {
	CreateIntent(ctx context.Context, in payments.CreateIntentInput) (*payments.Intent, error)
	VerifyCallback(ctx context.Context, intentID uuid.UUID, providerOrderID, providerPaymentID, signature string, expectedMinor int64) (*payments.CallbackVerdict, error)
	Refund(ctx context.Context, intentID uuid.UUID, amountMinor int64, reason, idempotencyKey string) (*payments.RefundAccepted, error)
}

var (
	// ErrPaymentsNotConfigured: no payments client is wired. HTTP 503.
	ErrPaymentsNotConfigured = errors.New("payments client is not configured")
	// ErrPaymentCallbackIncomplete: the checkout signature triple is missing. HTTP 400.
	ErrPaymentCallbackIncomplete = errors.New("provider order id, payment id and signature are required")
	// ErrPaymentCallbackMismatch: payments echoed a different order, payer or
	// amount than this order. HTTP 409 FOOD_PAYMENT_CALLBACK_MISMATCH.
	ErrPaymentCallbackMismatch = errors.New("payment callback does not belong to this order")
	// ErrPaymentNotVerified: payments did not verify the callback. HTTP 400.
	ErrPaymentNotVerified = errors.New("payment callback was not verified")
	// ErrPaymentIntentMissing: the order has no payments intent recorded. HTTP 409.
	ErrPaymentIntentMissing = errors.New("order has no payments intent")
)

// Confirm-call states.
const (
	// PaymentStateConfirming: the callback looked genuine; the order becomes
	// paid when payment.succeeded arrives. HTTP 202.
	PaymentStateConfirming = "confirming"
	// PaymentStatePaid: the payment was already captured. HTTP 200.
	PaymentStatePaid = "paid"
	// PaymentStateNotRequired: a cash-on-delivery order (flag on). HTTP 200.
	PaymentStateNotRequired = "not_required"
)

// WithPayments wires the payments-service client.
func (s *Service) WithPayments(c PaymentsClient) *Service {
	s.payments = c
	return s
}

// WithPaymentFlags overrides the env-derived launch switches.
func (s *Service) WithPaymentFlags(f payments.Flags) *Service {
	s.payFlags = f
	return s
}

func (s *Service) CreatePaymentIntent(ctx context.Context, userID, orderID uuid.UUID, method, idempotencyKey string) (map[string]any, error) {
	details, err := s.store.WalletPaymentChargeDetails(ctx, userID, orderID)
	if err != nil {
		return nil, err
	}
	raw := method
	if strings.TrimSpace(raw) == "" {
		// Fall back to what was chosen at checkout. A legacy COD/WALLET order
		// still goes through the flag gate below.
		raw = details.PaymentInstrument
		if raw == "" && (details.PaymentMethod == payments.StoreCOD || details.PaymentMethod == payments.StoreWallet) {
			raw = details.PaymentMethod
		}
	}
	resolved, err := payments.ResolveMethod(raw, s.payFlags)
	if err != nil {
		return nil, err
	}
	if resolved.Store == payments.StoreOnline && s.payments == nil {
		return nil, ErrPaymentsNotConfigured
	}
	intent, err := s.store.CreatePaymentIntent(ctx, userID, orderID, resolved.Store, resolved.Instrument, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if resolved.Store != payments.StoreOnline {
		return intent, nil
	}
	// The amount is the order's, in paise, read from the database; the client
	// cannot influence it.
	upstream, err := s.payments.CreateIntent(ctx, payments.CreateIntentInput{
		OrderID:     orderID,
		PayerID:     details.UserID,
		PayeeID:     details.RestaurantOwnerID,
		AmountMinor: details.AmountMinor,
		Method:      resolved.Instrument,
	})
	if err != nil {
		return nil, err
	}
	rawIntent := map[string]any{
		"payments_intent_id": upstream.ID.String(),
		"provider_ref":       upstream.ProviderRef,
		"amount_minor":       upstream.AmountMinor,
		"method":             upstream.Method,
		"status":             upstream.Status,
	}
	if err := s.store.AttachPaymentProviderReference(ctx, userID, orderID, upstream.ID.String(), upstream.ProviderRef, rawIntent); err != nil {
		return nil, err
	}
	intent["payment_intent"] = upstream
	intent["provider_payment_id"] = upstream.ID.String()
	intent["provider_order_id"] = upstream.ProviderRef
	return intent, nil
}

// ConfirmPaymentInput is the client's checkout callback. AmountMinor is
// accepted for wire compatibility and IGNORED: verify is sent the order's own
// total.
type ConfirmPaymentInput struct {
	UserID            uuid.UUID
	OrderID           uuid.UUID
	ProviderPaymentID string
	ProviderReference string
	RazorpayOrderID   string
	RazorpayPaymentID string
	RazorpaySignature string
	AmountMinor       int64
	IdempotencyKey    string
}

// ConfirmPaymentResult is the confirm-call response.
type ConfirmPaymentResult struct {
	State    string          `json:"state"`
	Verified bool            `json:"verified"`
	Order    *postgres.Order `json:"order"`
}

// ConfirmPayment NEVER marks an online order paid. It asks payments-service
// to verify the callback (advisory; with the dev stub gateway this is also
// what settles the intent and publishes payment.succeeded), refuses a
// callback that belongs to another order, payer or amount, and reports
// `confirming`. The order is confirmed only by the payment-event consumer;
// `paid` is returned when that has already happened.
func (s *Service) ConfirmPayment(ctx context.Context, in ConfirmPaymentInput) (*ConfirmPaymentResult, error) {
	details, err := s.store.WalletPaymentChargeDetails(ctx, in.UserID, in.OrderID)
	if err != nil {
		return nil, err
	}
	if details.PaymentStatus == "CAPTURED" {
		return s.confirmResult(ctx, in, PaymentStatePaid, false)
	}

	switch details.PaymentMethod {
	case payments.StoreCOD:
		if !s.payFlags.CODEnabled {
			return nil, fmt.Errorf("%w: cash on delivery is not enabled", payments.ErrPaymentMethodUnavailable)
		}
		return s.confirmResult(ctx, in, PaymentStateNotRequired, false)
	case payments.StoreWallet:
		if !s.payFlags.WalletEnabled {
			return nil, fmt.Errorf("%w: wallet payments are not enabled", payments.ErrPaymentMethodUnavailable)
		}
		// The monetization charge is a synchronous server-to-server transfer,
		// so a successful charge is authoritative.
		if err := s.chargeWalletForFoodOrder(ctx, details); err != nil {
			return nil, err
		}
		o, err := s.store.MarkWalletPaid(ctx, in.UserID, in.OrderID)
		if err != nil {
			return nil, err
		}
		s.emitPaymentEvent(ctx, "food.order.payment_succeeded", o.ID, o.RestaurantID, o)
		return &ConfirmPaymentResult{State: PaymentStatePaid, Verified: true, Order: o}, nil
	}

	if in.RazorpayOrderID == "" || in.RazorpayPaymentID == "" || in.RazorpaySignature == "" {
		return nil, ErrPaymentCallbackIncomplete
	}
	if s.payments == nil {
		return nil, ErrPaymentsNotConfigured
	}
	pd, err := s.store.PaymentIntegrationDetails(ctx, in.OrderID)
	if err != nil {
		return nil, err
	}
	intentID, err := uuid.Parse(pd.ProviderPaymentID)
	if err != nil {
		return nil, ErrPaymentIntentMissing
	}
	verdict, err := s.payments.VerifyCallback(ctx, intentID, in.RazorpayOrderID, in.RazorpayPaymentID, in.RazorpaySignature, pd.AmountMinor)
	if err != nil {
		return nil, err
	}
	if verdict.ReferenceType != payments.RefTypeFoodOrder || verdict.ReferenceID != in.OrderID ||
		verdict.PayerID != details.UserID || verdict.AmountMinor != pd.AmountMinor {
		return nil, ErrPaymentCallbackMismatch
	}
	if !verdict.Verified {
		return nil, ErrPaymentNotVerified
	}

	// Verify may have settled synchronously (the dev stub), and the consumer
	// may already have applied payment.succeeded. Report, never write.
	state := PaymentStateConfirming
	if after, err := s.store.WalletPaymentChargeDetails(ctx, in.UserID, in.OrderID); err == nil && after.PaymentStatus == "CAPTURED" {
		state = PaymentStatePaid
	}
	return s.confirmResult(ctx, in, state, true)
}

func (s *Service) confirmResult(ctx context.Context, in ConfirmPaymentInput, state string, verified bool) (*ConfirmPaymentResult, error) {
	o, err := s.store.GetOrder(ctx, in.UserID, in.OrderID)
	if err != nil {
		return nil, err
	}
	return &ConfirmPaymentResult{State: state, Verified: verified, Order: o}, nil
}

// AdminRefundOrder requests a refund. The store moves a full refund to
// REFUND_PENDING through the guard; payments is then asked with an explicit
// amount_minor and a key derived from the refund row, so a retry (same
// Idempotency-Key, or a new one while the order is REFUND_PENDING) resubmits
// the SAME refund. REFUNDED is written only by the payment.refunded event.
func (s *Service) AdminRefundOrder(ctx context.Context, adminID, orderID uuid.UUID, reason string, amount float64, idempotencyKey string) (map[string]any, error) {
	plan, err := s.store.AdminRequestRefund(ctx, adminID, orderID, reason, amount, idempotencyKey)
	if err != nil {
		return nil, err
	}
	return s.submitRefundPlan(ctx, orderID, plan, reason)
}

// submitRefundPlan submits a durable refund request (admin or system) to
// payments with its deterministic key, or reverses a wallet charge. It never
// marks a card/UPI refund done: the payment.refunded event does.
func (s *Service) submitRefundPlan(ctx context.Context, orderID uuid.UUID, plan *postgres.RefundPlan, reason string) (map[string]any, error) {
	body := plan.Body()
	if plan.Status == "PROCESSED" {
		return body, nil
	}
	switch plan.PaymentMethod {
	case payments.StoreOnline:
		if s.payments == nil {
			return nil, ErrPaymentsNotConfigured
		}
		intentID, err := uuid.Parse(plan.IntentID)
		if err != nil {
			return nil, ErrPaymentIntentMissing
		}
		key := "food_refund:" + orderID.String() + ":" + plan.RefundID.String()
		accepted, err := s.payments.Refund(ctx, intentID, plan.AmountMinor, reason, key)
		if err != nil {
			// The request is durable and REFUND_PENDING; retrying resubmits it
			// under the same key.
			return nil, err
		}
		if err := s.store.MarkRefundSubmitted(ctx, plan.RefundID, map[string]any{
			"payments_command_id": accepted.CommandID.String(),
			"payments_status":     accepted.Status,
			"idempotency_key":     key,
		}); err != nil {
			return nil, err
		}
		body["status"] = "SUBMITTED"
	case payments.StoreWallet:
		// Reversing money that already moved stays enabled with the wallet
		// flag off.
		details, err := s.store.PaymentIntegrationDetails(ctx, orderID)
		if err != nil {
			return nil, err
		}
		if err := s.reverseWalletForFoodRefund(ctx, details, float64(plan.AmountMinor)/100); err != nil {
			return nil, err
		}
		if err := s.store.FinalizeWalletRefund(ctx, plan.RefundID); err != nil {
			return nil, err
		}
		body["status"] = "PROCESSED"
	default:
		return nil, fmt.Errorf("%w: %s order", postgres.ErrRefundNotEligible, plan.PaymentMethod)
	}
	s.emit(ctx, "food.order."+orderID.String(), "food.order.refund_requested", body)
	return body, nil
}

// OnPaymentEventApplied is the payment consumer's post-commit hook.
func (s *Service) OnPaymentEventApplied(ctx context.Context, a payments.Applied) {
	var eventType string
	switch a.Decision.Effect {
	case payments.EffectConfirm:
		eventType = "food.order.payment_succeeded"
	case payments.EffectMarkFailed:
		eventType = "food.order.payment_failed"
	case payments.EffectRefund, payments.EffectPartialRefund:
		eventType = "food.order.refunded"
	default:
		return
	}
	var data any = map[string]any{"id": a.OrderID, "outcome": a.Decision.Outcome}
	if o, err := s.store.AdminGetOrder(ctx, a.OrderID); err == nil {
		data = o
	}
	s.emitPaymentEvent(ctx, eventType, a.OrderID, a.RestaurantID, data)
}

func (s *Service) emitPaymentEvent(ctx context.Context, eventType string, orderID, restaurantID uuid.UUID, data any) {
	s.emit(ctx, "food.order."+orderID.String(), eventType, data)
	if restaurantID != uuid.Nil {
		s.publishRealtime(ctx, "food.restaurant."+restaurantID.String()+".orders", eventType, data)
	}
	s.publishRealtime(ctx, "food.admin.live_orders", eventType, data)
}

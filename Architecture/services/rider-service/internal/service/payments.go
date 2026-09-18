package service

// Online ride payments through payments-service (application mopedu,
// reference type mopedu_ride).
//
// The customer opens an intent for a completed ride's final total (or for
// an outstanding fee), pays in the app, and polls the status. Nothing here
// marks a payment paid: the intent echo binds the intent and moves the row
// to confirming, the callback verdict is advisory, and only the consumer's
// signed events (payments.Consumer -> store.ApplyRidePaymentEvent) move a
// row to succeeded, failed or refunded. Refunds are admin-requested and
// settle the same way.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/payments"
	"github.com/atpost/rider-service/internal/pricing"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/shared/paymentmethod"
	"github.com/google/uuid"
)

// PaymentsClient is the part of *payments.Client the service uses.
type PaymentsClient interface {
	CreateIntent(ctx context.Context, in payments.CreateIntentInput) (*payments.Intent, error)
	// CreateSubscriptionIntent opens a captain subscription checkout
	// (reference type mopedu_subscription).
	CreateSubscriptionIntent(ctx context.Context, in payments.CreateIntentInput) (*payments.Intent, error)
	VerifyCallback(ctx context.Context, intentID uuid.UUID, in payments.CallbackRequest) (*payments.CallbackVerdict, error)
	Refund(ctx context.Context, intentID uuid.UUID, amountMinor int64, reason, idempotencyKey string) (*payments.RefundAccepted, error)
}

// SetPayments wires the payments client. env is ENV: outside local/dev an
// intent without a checkout session is refused.
func (s *Service) SetPayments(c PaymentsClient, env string) {
	s.payments = c
	s.paymentsLocalEnv = payments.IsLocalEnv(env)
}

// PaymentError is a typed refusal on the payments routes: the handler
// answers Status with Code.
type PaymentError struct {
	Status  int
	Code    string
	Message string
}

func (e *PaymentError) Error() string { return e.Code + ": " + e.Message }

// AsPaymentError unwraps a PaymentError.
func AsPaymentError(err error) (*PaymentError, bool) {
	var pe *PaymentError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

func payErr(status int, code, msg string) error {
	return &PaymentError{Status: status, Code: code, Message: msg}
}

// Payment error codes.
const (
	CodePaymentsUnavailable         = "PAYMENTS_UNAVAILABLE"
	CodePaymentsCheckoutUnavailable = "PAYMENTS_CHECKOUT_UNAVAILABLE"
	CodePaymentsRefused             = "PAYMENTS_REFUSED"
	CodePaymentMethodInvalid        = "PAYMENT_METHOD_INVALID"
	CodeRideNotCompleted            = "RIDE_NOT_COMPLETED"
	CodePaymentNotOnline            = "PAYMENT_NOT_ONLINE"
	CodePaymentNotPending           = "PAYMENT_NOT_PENDING"
	CodePaymentAlreadyPaid          = "PAYMENT_ALREADY_PAID"
	CodePaymentNoIntent             = "PAYMENT_NO_INTENT"
	CodeCallbackMismatch            = "PAYMENT_CALLBACK_MISMATCH"
	CodeOutstandingNotPayable       = "OUTSTANDING_NOT_PAYABLE"
	CodeRefundCashPayment           = "REFUND_CASH_PAYMENT"
	CodeRefundNotRefundable         = "REFUND_NOT_REFUNDABLE"
	CodeRefundExceedsRemaining      = "REFUND_EXCEEDS_REMAINING"
)

// RidePaymentIntent is the intent response: what the app needs to open
// checkout. Status is the intent's ("pending" until paid).
type RidePaymentIntent struct {
	IntentID      uuid.UUID               `json:"intent_id"`
	AmountPaise   int64                   `json:"amount_paise"`
	Currency      string                  `json:"currency"`
	ClientSession *payments.ClientSession `json:"client_session"`
	Status        string                  `json:"status"`
}

// RidePaymentStatus is GET /rides/:id/payment.
type RidePaymentStatus struct {
	Method        string     `json:"method"`
	Status        string     `json:"status"`
	AmountPaise   int64      `json:"amount_paise"`
	RefundedPaise int64      `json:"refunded_paise"`
	IntentID      *uuid.UUID `json:"intent_id"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func paymentStatusOf(p *store.RidePayment) *RidePaymentStatus {
	return &RidePaymentStatus{
		Method: p.PaymentMethod, Status: payments.PublicStatus(p.PaymentMethod, p.Status),
		AmountPaise: p.AmountPaise, RefundedPaise: p.RefundedPaise, IntentID: p.IntentID, UpdatedAt: p.UpdatedAt,
	}
}

var onlineMethods = map[string]bool{payments.MethodUPI: true, payments.MethodCard: true}

func validateOnlineMethod(method string) error {
	method = strings.TrimSpace(method)
	if err := paymentmethod.Validate(method); err != nil || !onlineMethods[method] {
		return payErr(http.StatusBadRequest, CodePaymentMethodInvalid, "method must be upi or card")
	}
	return nil
}

// mapPaymentsError turns a payments client error into the route's answer.
func mapPaymentsError(err error) error {
	switch {
	case errors.Is(err, payments.ErrPaymentsUnavailable):
		return payErr(http.StatusServiceUnavailable, CodePaymentsUnavailable, "payments service unavailable; try again")
	case errors.Is(err, payments.ErrRefused):
		return payErr(http.StatusBadGateway, CodePaymentsRefused, "payments refused the request")
	}
	return err
}

// loadOwnCompletedRide loads the ride for its customer.
func (s *Service) loadOwnRide(ctx context.Context, customerID, rideID uuid.UUID) (*store.Ride, error) {
	if customerID == uuid.Nil || rideID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id and ride id required")
	}
	ride, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, fmt.Errorf("not_found: ride")
		}
		return nil, err
	}
	if ride.CustomerUserID != customerID {
		return nil, fmt.Errorf("forbidden: ride does not belong to user")
	}
	return ride, nil
}

// rideFinalTotal is the ride's final total from fare_breakdown.total_paise,
// falling back to the payment row when a legacy ride has no breakdown.
func rideFinalTotal(ride *store.Ride, pay *store.RidePayment) int64 {
	if len(ride.FareBreakdown) > 0 {
		var b pricing.Breakdown
		if json.Unmarshal(ride.FareBreakdown, &b) == nil && b.TotalPaise > 0 {
			return b.TotalPaise
		}
	}
	return pay.AmountPaise
}

// CreateRidePaymentIntent opens (or re-opens: the payments key is
// deterministic per ride and method) the intent for a completed ride's
// final total. Customer only; the ride must be completed and its online
// payment pending, confirming or failed.
func (s *Service) CreateRidePaymentIntent(ctx context.Context, customerID, rideID uuid.UUID, method string) (*RidePaymentIntent, error) {
	method = strings.TrimSpace(method)
	if err := validateOnlineMethod(method); err != nil {
		return nil, err
	}
	ride, err := s.loadOwnRide(ctx, customerID, rideID)
	if err != nil {
		return nil, err
	}
	if ride.Status != "completed" {
		return nil, payErr(http.StatusConflict, CodeRideNotCompleted, "the ride is not completed")
	}
	pay, err := s.store.GetRidePaymentByRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRidePaymentNotFound) {
			return nil, payErr(http.StatusConflict, CodePaymentNotPending, "the ride has no payment to settle")
		}
		return nil, err
	}
	if pay.PaymentMethod == payments.MethodCash {
		return nil, payErr(http.StatusConflict, CodePaymentNotOnline, "the ride is paid in cash to the captain")
	}
	switch pay.Status {
	case payments.StatusPending, payments.StatusConfirming, payments.StatusFailed:
	default:
		return nil, payErr(http.StatusConflict, CodePaymentNotPending, "the ride payment is "+payments.PublicStatus(pay.PaymentMethod, pay.Status))
	}
	if s.payments == nil {
		return nil, payErr(http.StatusServiceUnavailable, CodePaymentsUnavailable, "online payments are not available")
	}
	amount := rideFinalTotal(ride, pay)
	if amount != pay.AmountPaise {
		return nil, fmt.Errorf("ride payment amount %d does not match the fare breakdown total %d", pay.AmountPaise, amount)
	}
	intent, err := s.payments.CreateIntent(ctx, payments.CreateIntentInput{
		ReferenceID: rideID, PayerID: customerID, AmountMinor: amount, Method: method,
		IdempotencyKey: payments.RideIntentKey(rideID, method),
	})
	if err != nil {
		return nil, mapPaymentsError(err)
	}
	if _, err := s.store.BindRidePaymentIntent(ctx, pay.ID, intent.ID, intent.ProviderRef, method); err != nil {
		if errors.Is(err, store.ErrPaymentNotBindable) {
			return nil, payErr(http.StatusConflict, CodePaymentNotPending, "the ride payment changed; refresh")
		}
		return nil, err
	}
	return s.intentResponse(ctx, intent, "ride", rideID)
}

func (s *Service) intentResponse(_ context.Context, intent *payments.Intent, kind string, id uuid.UUID) (*RidePaymentIntent, error) {
	cs := intent.PublicClientSession()
	if cs == nil && !s.paymentsLocalEnv {
		slog.Error("rider: payments returned an intent with no checkout session outside local/dev; refusing (stub gateway?)",
			kind+"_id", id, "intent_id", intent.ID)
		return nil, payErr(http.StatusServiceUnavailable, CodePaymentsCheckoutUnavailable, "checkout is not available")
	}
	status := intent.Status
	if status == "" {
		status = "pending"
	}
	return &RidePaymentIntent{IntentID: intent.ID, AmountPaise: intent.AmountMinor, Currency: payments.CurrencyINR, ClientSession: cs, Status: status}, nil
}

// GetRidePaymentStatus reports the ride's payment to its customer or its
// assigned partner. It never writes and never asks payments-service.
func (s *Service) GetRidePaymentStatus(ctx context.Context, userID, rideID uuid.UUID) (*RidePaymentStatus, error) {
	if userID == uuid.Nil || rideID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id and ride id required")
	}
	ride, err := s.store.GetRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRideNotFound) {
			return nil, fmt.Errorf("not_found: ride")
		}
		return nil, err
	}
	if ride.CustomerUserID != userID {
		partner, err := s.store.GetPartnerByUserID(ctx, userID)
		if err != nil || partner == nil || ride.PartnerID == nil || *ride.PartnerID != partner.ID {
			return nil, fmt.Errorf("forbidden: not authorized to view this payment")
		}
	}
	pay, err := s.store.GetRidePaymentByRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRidePaymentNotFound) {
			return nil, fmt.Errorf("not_found: ride payment")
		}
		return nil, err
	}
	return paymentStatusOf(pay), nil
}

// SwitchRidePaymentToCash turns an unpaid online payment (pending,
// confirming or failed) into a cash payment the captain then confirms.
// Never after paid.
func (s *Service) SwitchRidePaymentToCash(ctx context.Context, customerID, rideID uuid.UUID) (*RidePaymentStatus, error) {
	if _, err := s.loadOwnRide(ctx, customerID, rideID); err != nil {
		return nil, err
	}
	pay, err := s.store.GetRidePaymentByRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRidePaymentNotFound) {
			return nil, payErr(http.StatusConflict, CodePaymentNotPending, "the ride has no payment to settle")
		}
		return nil, err
	}
	switch pay.Status {
	case payments.StatusSucceeded, payments.StatusRefunded, payments.StatusPartiallyRefunded:
		return nil, payErr(http.StatusConflict, CodePaymentAlreadyPaid, "the ride payment is already "+payments.PublicStatus(pay.PaymentMethod, pay.Status))
	}
	if pay.PaymentMethod == payments.MethodCash {
		return nil, payErr(http.StatusConflict, CodePaymentNotOnline, "the ride is already a cash payment")
	}
	switched, err := s.store.SwitchRidePaymentToCash(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrPaymentNotSwitchable) {
			return nil, payErr(http.StatusConflict, CodePaymentNotPending, "the ride payment changed; refresh")
		}
		return nil, err
	}
	s.publishRealtime(ctx, "rider.ride."+rideID.String(), "rider.ride.payment.updated",
		map[string]any{"ride_id": rideID, "method": switched.PaymentMethod, "status": payments.PublicStatus(switched.PaymentMethod, switched.Status)})
	return paymentStatusOf(switched), nil
}

// RidePaymentCallback is the client checkout callback.
type RidePaymentCallback struct {
	ProviderOrderID   string
	ProviderPaymentID string
	Signature         string
}

// RidePaymentCallbackResult is ADVISORY: Verified reports the signature
// check; Status is the row's public status, which the callback never
// changes. Only the signed payment.succeeded event moves it to paid.
type RidePaymentCallbackResult struct {
	Verified bool   `json:"verified"`
	Advisory bool   `json:"advisory"`
	Status   string `json:"status"`
}

// VerifyRidePaymentCallback relays the app's checkout callback to
// payments-service for an advisory signature check. It writes nothing.
func (s *Service) VerifyRidePaymentCallback(ctx context.Context, customerID, rideID uuid.UUID, cb RidePaymentCallback) (*RidePaymentCallbackResult, error) {
	if _, err := s.loadOwnRide(ctx, customerID, rideID); err != nil {
		return nil, err
	}
	pay, err := s.store.GetRidePaymentByRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRidePaymentNotFound) {
			return nil, fmt.Errorf("not_found: ride payment")
		}
		return nil, err
	}
	if pay.IntentID == nil {
		return nil, payErr(http.StatusConflict, CodePaymentNoIntent, "no payment intent is open for this ride")
	}
	if s.payments == nil {
		return nil, payErr(http.StatusServiceUnavailable, CodePaymentsUnavailable, "online payments are not available")
	}
	verdict, err := s.payments.VerifyCallback(ctx, *pay.IntentID, payments.CallbackRequest{
		ProviderOrderID: strings.TrimSpace(cb.ProviderOrderID), ProviderPaymentID: strings.TrimSpace(cb.ProviderPaymentID),
		Signature: strings.TrimSpace(cb.Signature), ExpectedAmountMinor: pay.AmountPaise,
	})
	if err != nil {
		return nil, mapPaymentsError(err)
	}
	// A genuine signature for another ride, another payer or another
	// application is refused, whatever payments said about it.
	if verdict.Verified && (verdict.ReferenceID != rideID || verdict.PayerID != customerID ||
		(verdict.ApplicationID != "" && verdict.ApplicationID != payments.ApplicationID)) {
		return nil, payErr(http.StatusUnprocessableEntity, CodeCallbackMismatch, "the callback does not belong to this ride")
	}
	// Deliberately no write: the row stays confirming until the consumer
	// applies the signed capture.
	return &RidePaymentCallbackResult{Verified: verdict.Verified, Advisory: true, Status: payments.PublicStatus(pay.PaymentMethod, pay.Status)}, nil
}

// --- Outstanding fees paid directly ----------------------------------------

// ListMyOutstanding lists the customer's pending fees no active ride has
// reserved.
func (s *Service) ListMyOutstanding(ctx context.Context, customerID uuid.UUID) ([]store.CustomerOutstanding, error) {
	if customerID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id required")
	}
	out, err := s.store.ListPendingOutstanding(ctx, customerID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []store.CustomerOutstanding{}
	}
	return out, nil
}

// CreateOutstandingPaymentIntent opens the intent to pay one pending,
// unreserved outstanding fee directly. The row settles on the signed
// payment.succeeded event.
func (s *Service) CreateOutstandingPaymentIntent(ctx context.Context, customerID, outstandingID uuid.UUID, method string) (*RidePaymentIntent, error) {
	method = strings.TrimSpace(method)
	if err := validateOnlineMethod(method); err != nil {
		return nil, err
	}
	if customerID == uuid.Nil || outstandingID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id and outstanding id required")
	}
	o, err := s.store.GetOutstanding(ctx, outstandingID)
	if err != nil {
		if errors.Is(err, store.ErrOutstandingNotFound) {
			return nil, fmt.Errorf("not_found: outstanding fee")
		}
		return nil, err
	}
	if o.CustomerUserID != customerID {
		return nil, fmt.Errorf("forbidden: outstanding fee does not belong to user")
	}
	if o.Status != store.OutstandingPending || o.SettledByRideID != nil {
		return nil, payErr(http.StatusConflict, CodeOutstandingNotPayable, "the fee is "+o.Status+" or charged on an active ride")
	}
	if s.payments == nil {
		return nil, payErr(http.StatusServiceUnavailable, CodePaymentsUnavailable, "online payments are not available")
	}
	intent, err := s.payments.CreateIntent(ctx, payments.CreateIntentInput{
		ReferenceID: outstandingID, PayerID: customerID, AmountMinor: o.AmountPaise, Method: method,
		IdempotencyKey: payments.OutstandingIntentKey(outstandingID, method),
	})
	if err != nil {
		return nil, mapPaymentsError(err)
	}
	if _, err := s.store.BindOutstandingIntent(ctx, outstandingID, customerID, intent.ID, method); err != nil {
		if errors.Is(err, store.ErrOutstandingNotFound) {
			return nil, payErr(http.StatusConflict, CodeOutstandingNotPayable, "the fee changed; refresh")
		}
		return nil, err
	}
	return s.intentResponse(ctx, intent, "outstanding", outstandingID)
}

// --- Admin: refunds and lists ----------------------------------------------

// RefundRidePayment files a refund of an online ride payment (admin,
// rider:payments.settle). amountPaise 0 refunds the full remaining amount.
// The row is `accepted` when payments took the command; it becomes
// `refunded` only from the payment.refunded event.
func (s *Service) RefundRidePayment(ctx context.Context, adminID, rideID uuid.UUID, amountPaise int64, reason string) (*store.RideRefund, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("invalid: reason required")
	}
	if amountPaise < 0 {
		return nil, fmt.Errorf("invalid: amount_paise must be positive")
	}
	if rideID == uuid.Nil {
		return nil, fmt.Errorf("invalid: ride id required")
	}
	if s.payments == nil {
		return nil, payErr(http.StatusServiceUnavailable, CodePaymentsUnavailable, "online payments are not available")
	}
	refund, _, err := s.store.CreateRideRefund(ctx, store.CreateRideRefundInput{RideID: rideID, AmountPaise: amountPaise, Reason: reason, RequestedBy: adminID})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRidePaymentNotFound):
			return nil, fmt.Errorf("not_found: ride payment")
		case errors.Is(err, store.ErrRefundCashPayment):
			return nil, payErr(http.StatusUnprocessableEntity, CodeRefundCashPayment, "cash payments cannot be refunded here; waive the outstanding fee or settle manually")
		case errors.Is(err, store.ErrRefundNotRefundable):
			return nil, payErr(http.StatusConflict, CodeRefundNotRefundable, "the payment is not captured, or already fully refunded")
		case errors.Is(err, store.ErrRefundExceedsRemaining):
			return nil, payErr(http.StatusUnprocessableEntity, CodeRefundExceedsRemaining, "amount exceeds the remaining refundable amount")
		}
		return nil, err
	}
	acc, err := s.payments.Refund(ctx, refund.IntentID, refund.AmountPaise, reason, payments.RefundKey(refund.ID))
	if err != nil {
		mapped := mapPaymentsError(err)
		if _, ferr := s.store.MarkRideRefundFailed(ctx, refund.ID, err.Error()); ferr != nil {
			slog.Error("rider: mark refund failed", "refund_id", refund.ID, "error", ferr)
		}
		return nil, mapped
	}
	accepted, err := s.store.MarkRideRefundAccepted(ctx, refund.ID, acc.CommandID.String())
	if err != nil {
		return nil, err
	}
	s.emitAdminAction(ctx, adminID, "ride_payment.refund", "ride", rideID, reason)
	return accepted, nil
}

// ListRideRefunds is the admin refund list.
func (s *Service) ListRideRefunds(ctx context.Context, status string, rideID *uuid.UUID, limit, offset int) ([]store.RideRefund, error) {
	return s.store.ListRideRefunds(ctx, status, rideID, limit, offset)
}

// ListRidePaymentsAdmin is the admin ride-payments list (cursor-paged).
func (s *Service) ListRidePaymentsAdmin(ctx context.Context, f store.RidePaymentFilter) ([]store.AdminRidePayment, string, error) {
	return s.store.ListRidePaymentsAdmin(ctx, f)
}

// OnRidePaymentApplied runs after the consumer committed an effect: a
// realtime frame on the ride topic so the app stops polling, and, when the
// signed capture marked a ride payment or an outstanding fee paid, the
// rider.ride.payment_paid Kafka event notification-service pushes from.
func (s *Service) OnRidePaymentApplied(ctx context.Context, a payments.Applied) {
	if a.Target == payments.TargetSubscription {
		s.onSubscriptionPaymentApplied(ctx, a)
		return
	}
	if a.RideID == uuid.Nil {
		return
	}
	switch a.Decision.Effect {
	case payments.EffectRefundDuplicate:
		// Rule (b): the store filed the refund of the second capture in the
		// event's transaction; send it to payments now.
		s.sendDuplicateCaptureRefund(ctx, a.RefundID)
	case payments.EffectSettleOutstanding:
		// Rule (c): a cancellation fee paid directly is checked against the
		// ride's cancellation facts as soon as it is settled.
		if _, err := s.EvaluateCancellationFeeRefund(ctx, a.TargetID); err != nil {
			slog.Warn("rider: cancellation-fee refund evaluation failed", "outstanding_id", a.TargetID, "error", err)
		}
	}
	frame := map[string]any{"ride_id": a.RideID, "target": a.Target, "target_id": a.TargetID, "status": a.Status, "outcome": string(a.Decision.Outcome)}
	paid := events.RidePaymentPaidPayload{RideID: a.RideID.String(), CustomerUserID: a.CustomerID.String(), PaidAt: s.now()}
	var partnerID *uuid.UUID
	switch a.Target {
	case payments.TargetRide:
		if pay, err := s.store.GetRidePaymentByRide(ctx, a.RideID); err == nil {
			frame["method"] = pay.PaymentMethod
			frame["status"] = payments.PublicStatus(pay.PaymentMethod, pay.Status)
			paid.AmountPaise, paid.Method = pay.AmountPaise, pay.PaymentMethod
			pid := pay.PartnerID
			partnerID = &pid
		}
	case payments.TargetOutstanding:
		if o, err := s.store.GetOutstanding(ctx, a.TargetID); err == nil {
			paid.AmountPaise = o.AmountPaise
			if o.IntentMethod != nil {
				paid.Method = *o.IntentMethod
			}
		}
		if ride, err := s.store.GetRide(ctx, a.RideID); err == nil {
			partnerID = ride.PartnerID
		}
	}
	s.publishRealtime(ctx, "rider.ride."+a.RideID.String(), "rider.ride.payment.updated", frame)

	if a.Decision.Effect != payments.EffectMarkPaid && a.Decision.Effect != payments.EffectSettleOutstanding {
		return
	}
	if partnerID != nil {
		paid.PartnerID = partnerID.String()
		if p, err := s.store.GetPartner(ctx, *partnerID); err == nil {
			paid.PartnerUserID = p.UserID.String()
		}
	}
	if err := s.producer.PublishRidePaymentPaid(ctx, paid); err != nil {
		slog.Warn("rider: publish ride.payment_paid failed", "ride_id", a.RideID, "target", a.Target, "error", err)
	}
}

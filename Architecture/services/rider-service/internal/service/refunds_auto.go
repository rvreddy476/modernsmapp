package service

// Automatic refunds by rule (payments/rules.go), executed through the same
// refund path an admin uses (rider_ride_refunds -> payments Refund ->
// accepted; refunded only from the signed payment.refunded event), with
// requested_by = payments.SystemActorID and the rule as reason code. Never
// through admin approval. Discretionary refunds (RefundRidePayment) are
// admin-only and unchanged.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/rider-service/internal/payments"
	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// sendRuleRefund sends the payments Refund command for a filed rule refund
// row (requested -> accepted, or failed with the reason). Without a payments
// client the row is marked failed so it is visible, never silently dropped.
func (s *Service) sendRuleRefund(ctx context.Context, refund *store.RideRefund) (*store.RideRefund, error) {
	if refund.Status != store.RefundRequested {
		return refund, nil
	}
	if s.payments == nil {
		slog.Error("rider: RULE REFUND CANNOT BE SENT — payments client not configured", "refund_id", refund.ID, "rule", refund.RuleCode)
		return s.store.MarkRideRefundFailed(ctx, refund.ID, "payments client not configured")
	}
	acc, err := s.payments.Refund(ctx, refund.IntentID, refund.AmountPaise, refund.Reason, payments.RefundKey(refund.ID))
	if err != nil {
		slog.Error("rider: rule refund refused by payments", "refund_id", refund.ID, "rule", refund.RuleCode, "error", err)
		if _, ferr := s.store.MarkRideRefundFailed(ctx, refund.ID, err.Error()); ferr != nil {
			slog.Error("rider: mark rule refund failed", "refund_id", refund.ID, "error", ferr)
		}
		return nil, mapPaymentsError(err)
	}
	accepted, err := s.store.MarkRideRefundAccepted(ctx, refund.ID, acc.CommandID.String())
	if err != nil {
		return nil, err
	}
	slog.Info("rider: rule refund accepted by payments", "refund_id", refund.ID, "rule", refund.RuleCode, "amount_paise", refund.AmountPaise)
	return accepted, nil
}

// autoRefundOnCancel is rule (a): a ride the captain (or admin / system)
// cancelled after the customer paid online is refunded in full. A cash
// ride, an unpaid one or a customer cancellation never reaches payments.
func (s *Service) autoRefundOnCancel(ctx context.Context, rideID uuid.UUID, by string) (*store.RideRefund, error) {
	pay, err := s.store.GetRidePaymentByRide(ctx, rideID)
	if err != nil {
		if errors.Is(err, store.ErrRidePaymentNotFound) {
			return nil, nil
		}
		return nil, err
	}
	inFlight, err := s.store.SumInFlightRefunds(ctx, pay.ID)
	if err != nil {
		return nil, err
	}
	amount, fire := payments.CaptainCancelRefund(payments.CaptainCancelFacts{
		CancelledByKind: by, PaymentMethod: pay.PaymentMethod, PaymentStatus: pay.Status,
		AmountPaise: pay.AmountPaise, RefundedPaise: pay.RefundedPaise, InFlightPaise: inFlight,
	})
	if !fire {
		return nil, nil
	}
	refund, _, err := s.store.CreateRideRefund(ctx, store.CreateRideRefundInput{
		RideID: rideID, AmountPaise: amount, Reason: payments.RuleCaptainCancel, RequestedBy: payments.SystemActorID, RuleCode: payments.RuleCaptainCancel,
	})
	if err != nil {
		if errors.Is(err, store.ErrRuleRefundExists) {
			return nil, nil
		}
		return nil, fmt.Errorf("file captain-cancel refund: %w", err)
	}
	return s.sendRuleRefund(ctx, refund)
}

// EvaluateCancellationFeeRefund is rule (c): a cancellation fee the
// customer paid online directly whose cancellation is inside the free
// window or partner-caused is refunded. Called when the fee's own intent
// settles it, and re-callable whenever the ride's cancellation facts are
// corrected. nil, nil when the rule does not fire.
func (s *Service) EvaluateCancellationFeeRefund(ctx context.Context, outstandingID uuid.UUID) (*store.RideRefund, error) {
	o, err := s.store.GetOutstanding(ctx, outstandingID)
	if err != nil {
		return nil, err
	}
	ride, err := s.store.GetRideCancellationFacts(ctx, o.RideID)
	if err != nil {
		return nil, err
	}
	freeSeconds := 0
	if ride.CityID != nil {
		if rule, rerr := s.store.GetFareRule(ctx, *ride.CityID, ride.VehicleType); rerr == nil {
			freeSeconds = rule.PricingRule().CancelFreeSeconds
		}
	}
	amount, fire := payments.CancellationFeeRefund(payments.CancellationFeeFacts{
		Reason: o.Reason, OutstandingStatus: o.Status, SettledOnline: o.SettledIntentID != nil, AmountPaise: o.AmountPaise,
		CancelledByKind: ride.CancelledByKind, AssignedAt: ride.AssignedAt, CancelledAt: ride.CancelledAt, CancelFreeSeconds: freeSeconds,
	})
	if !fire || amount <= 0 {
		return nil, nil
	}
	refund, err := s.store.CreateOutstandingRefund(ctx, store.CreateOutstandingRefundInput{
		OutstandingID: o.ID, Reason: payments.RuleCancellationFee, RequestedBy: payments.SystemActorID, RuleCode: payments.RuleCancellationFee,
	})
	if err != nil {
		if errors.Is(err, store.ErrRuleRefundExists) || errors.Is(err, store.ErrRefundNotRefundable) {
			return nil, nil
		}
		return nil, fmt.Errorf("file cancellation-fee refund: %w", err)
	}
	return s.sendRuleRefund(ctx, refund)
}

// sendDuplicateCaptureRefund is rule (b)'s second half: the store filed the
// refund row in the capture's transaction; this sends it to payments.
func (s *Service) sendDuplicateCaptureRefund(ctx context.Context, refundID uuid.UUID) {
	if refundID == uuid.Nil {
		return
	}
	refund, err := s.store.GetRideRefund(ctx, refundID)
	if err != nil {
		slog.Error("rider: duplicate-capture refund row not readable", "refund_id", refundID, "error", err)
		return
	}
	if _, err := s.sendRuleRefund(ctx, refund); err != nil {
		slog.Error("rider: duplicate-capture refund not sent", "refund_id", refundID, "error", err)
	}
}

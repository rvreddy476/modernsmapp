package service

// Parked refunds: the reason code, the alarm gauge, and the operator's view.

import (
	"context"
	"errors"
	"log/slog"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/obs"
	"github.com/atpost/payments-service/internal/store/postgres"
)

// Reason codes a refund command is parked with. The closed vocabulary lives in
// obs.RefundReasonCodes; these are its names.
const (
	RefundFailProviderRejected         = "provider_rejected"
	RefundFailPaymentNotFound          = "payment_not_found"
	RefundFailAmbiguousPayment         = "ambiguous_payment"
	RefundFailInvalidPaymentID         = "invalid_payment_id"
	RefundFailStubOnRealProvider       = "stub_on_real_provider"
	RefundFailIntentNotFound           = "intent_not_found"
	RefundFailNoProviderReference      = "no_provider_reference"
	RefundFailNoProviderAdapter        = "no_provider_adapter"
	RefundFailAlreadyRefundedUnmatched = "already_refunded_unmatched"
)

// errAmbiguousPayment is an unresolvable refund with more than one candidate
// payment, or an intent that already holds a different one.
var errAmbiguousPayment = errors.New("refund cannot be placed: the payment to refund is ambiguous")

// refundFailureCode classifies an error that parks a refund.
func refundFailureCode(err error) string {
	switch {
	case errors.Is(err, errAmbiguousPayment):
		return RefundFailAmbiguousPayment
	case errors.Is(err, errRefundUnresolvable):
		return RefundFailPaymentNotFound
	case errors.Is(err, gateway.ErrNotAPaymentID):
		return RefundFailInvalidPaymentID
	default:
		return RefundFailProviderRejected
	}
}

// refreshRefundAttentionGauge sets payments_refunds_needs_attention from the
// database. Every replica runs the worker and reports the same number.
func (s *Service) refreshRefundAttentionGauge(ctx context.Context) {
	n, err := s.store.CountRefundsNeedingAttention(ctx)
	if err != nil {
		slog.Warn("payments: could not count parked refunds for the alarm gauge", "error", err)
		return
	}
	obs.SetRefundsNeedingAttention(n)
}

// ListRefundsNeedingAttention is the operator list of parked commands.
func (s *Service) ListRefundsNeedingAttention(ctx context.Context, f postgres.NeedsAttentionFilter) ([]postgres.NeedsAttentionRefund, *postgres.RefundCursor, error) {
	return s.store.ListRefundsNeedingAttention(ctx, f)
}

// ResolveRefundCommand closes a parked command. A replay returns the stored
// resolution and writes nothing.
func (s *Service) ResolveRefundCommand(ctx context.Context, in postgres.ResolveRefundInput) (*postgres.RefundResolution, error) {
	res, err := s.store.ResolveRefundCommand(ctx, in)
	if err != nil {
		return nil, err
	}
	if !res.Replayed {
		slog.Warn("payments: parked refund resolved by an operator",
			"command_id", res.CommandID, "intent_id", res.IntentID, "resolution", res.Resolution,
			"operator_id", res.ResolvedBy, "amount_minor", res.AmountMinor,
			"refund_event_emitted", res.RefundEventEmitted)
		s.refreshRefundAttentionGauge(ctx)
	}
	return res, nil
}

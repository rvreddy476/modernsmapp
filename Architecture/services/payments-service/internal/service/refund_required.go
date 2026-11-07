package service

// The refund-required worker (migration 018): money owed back on a capture the
// intent could not accept — a late capture on a FAILED intent, or a duplicate
// capture on a paid one — is refunded by payments-service itself, not left on
// a list for an operator.
//
//	claim (SKIP LOCKED, lease)  →  provider refund of THAT payment, keyed by
//	the row id  →  submitted  →  the provider's signed refund webhook resolves
//	the row (store.ApplyRefundWebhookAtomically)
//
// The webhook, not this worker, marks the row resolved: a refund call that
// returned 200 is a promise, and only the provider's signed event is proof.
// Behind PAYMENTS_REFUND_REQUIRED_WORKER (config.RefundRequiredWorker).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/store/postgres"
)

// Reasons the worker parks an owed capture instead of refunding it.
const (
	owedFailNoAmount    = "no_amount"
	owedFailNoPaymentID = "no_payment_id"
	owedFailNoCurrency  = "no_currency"
)

// owedRefundRoute is how an owed capture is refunded.
type owedRefundRoute int

const (
	owedRouteProvider owedRefundRoute = iota // the provider's refund API
	owedRouteStub                            // a stub deployment settles it locally
	owedRoutePark                            // cannot be refunded as the data stands
	owedRouteNothing                         // nothing is left to refund
)

// routeOwedRefund decides how to refund an owed capture, and the park code
// when it cannot be. It has no I/O.
func routeOwedRefund(o postgres.OwedCapture, stubSettlement, haveProvider bool) (owedRefundRoute, string) {
	pay := strings.TrimSpace(o.ProviderPaymentID)
	switch {
	case o.AmountMinor <= 0:
		return owedRoutePark, owedFailNoAmount
	case o.RemainingMinor() == 0:
		return owedRouteNothing, ""
	case pay == "" || strings.HasPrefix(pay, "event:") || strings.HasPrefix(pay, postgres.OwedKeyIntentPrefix):
		// The capture event carried no payment id, or the duplicate intent
		// holds none; the row is keyed by the event or the intent, which no
		// provider can refund.
		return owedRoutePark, owedFailNoPaymentID
	case gateway.IsStubOrderRef(o.ProviderOrderID):
		if stubSettlement {
			return owedRouteStub, ""
		}
		return owedRoutePark, RefundFailStubOnRealProvider
	case !haveProvider:
		return owedRoutePark, RefundFailNoProviderAdapter
	}
	return owedRouteProvider, ""
}

// RunRefundRequiredWorker refunds owed captures until ctx is cancelled.
func (s *Service) RunRefundRequiredWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	slog.Info("payments: refund-required worker started", "interval", interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.drainRefundRequired(ctx)
		}
	}
}

func (s *Service) drainRefundRequired(ctx context.Context) {
	rows, err := s.store.ClaimDueRefundRequired(ctx, 25)
	if err != nil {
		slog.Warn("payments: claim refund-required rows failed", "error", err)
		return
	}
	for _, o := range rows {
		s.attemptOwedRefund(ctx, o)
	}
}

func (s *Service) attemptOwedRefund(ctx context.Context, o postgres.OwedCapture) {
	route, code := routeOwedRefund(o, s.stubSettlement, s.provider != nil)
	switch route {
	case owedRouteNothing:
		return
	case owedRoutePark:
		s.parkOwedRefund(ctx, o, code, "the owed capture cannot be refunded as recorded")
		return
	}
	money := gateway.Money{Minor: o.RemainingMinor(), Currency: o.Currency}
	if strings.TrimSpace(money.Currency) == "" {
		if intent, err := s.store.GetIntent(ctx, o.IntentID); err == nil && intent != nil {
			money.Currency = intent.Currency
		}
	}
	if strings.TrimSpace(money.Currency) == "" {
		s.parkOwedRefund(ctx, o, owedFailNoCurrency, "neither the capture nor its intent records a currency")
		return
	}

	if route == owedRouteStub {
		s.settleStubOwedRefund(ctx, o, money)
		return
	}

	// The SAME key on every attempt and every replica: an ambiguous timeout
	// followed by a retry is one refund at the provider (A6).
	res, err := s.provider.Refund(ctx, o.ProviderPaymentID, money, o.IdempotencyKey())
	if err != nil {
		if gateway.ClassifyRefundError(err) == gateway.RefundAlreadyRefunded {
			s.settleOwedAlreadyRefunded(ctx, o)
			return
		}
		reason := "placing the refund: " + gateway.RedactError(err)
		if gateway.ClassifyRefundError(err) != gateway.RefundRetryable {
			s.parkOwedRefund(ctx, o, refundFailureCode(err), reason)
			return
		}
		slog.Warn("payments: owed-capture refund attempt failed; will retry",
			"refund_required_id", o.ID, "attempt", o.Attempts, "error", reason)
		if merr := s.store.MarkRefundRequiredAttemptFailed(ctx, o.ID, reason); merr != nil {
			slog.Warn("payments: could not record the failed owed-capture refund", "refund_required_id", o.ID, "error", merr)
		}
		return
	}
	if err := s.store.MarkRefundRequiredSubmitted(ctx, o.ID, res.ProviderRefundID); err != nil {
		slog.Warn("payments: could not mark the owed-capture refund submitted", "refund_required_id", o.ID, "error", err)
		return
	}
	slog.Info("payments: owed-capture refund submitted; the signed refund webhook resolves it",
		"refund_required_id", o.ID, "intent_id", o.IntentID, "reason", o.Reason,
		"provider_payment_id", o.ProviderPaymentID, "provider_refund_id", res.ProviderRefundID,
		"amount_minor", money.Minor)
}

// settleOwedAlreadyRefunded handles the provider refusing because the payment
// is already fully refunded (a lost response, or a refund placed elsewhere).
// The provider's own refunds of the payment are fetched — a server-initiated
// provider fetch — and each processed one is applied through ApplyWebhook,
// the refund webhook's own transaction, which credits the owed row once per
// refund id and resolves it when they cover the payment.
func (s *Service) settleOwedAlreadyRefunded(ctx context.Context, o postgres.OwedCapture) {
	lister, ok := s.provider.(gateway.RefundLister)
	if !ok {
		s.parkOwedRefund(ctx, o, RefundFailAlreadyRefundedUnmatched,
			"provider reports the payment fully refunded but cannot list its refunds")
		return
	}
	refunds, err := lister.FetchPaymentRefunds(ctx, o.ProviderPaymentID)
	if err != nil {
		_ = s.store.MarkRefundRequiredAttemptFailed(ctx, o.ID, "listing the payment's refunds: "+gateway.RedactError(err))
		return
	}
	applied := 0
	for _, r := range refunds {
		if r.State != gateway.StateRefunded || r.ProviderRefundID == "" {
			continue
		}
		err := s.ApplyWebhook(ctx, WebhookInput{
			Provider:          o.Provider,
			EventID:           "refund_required_settle_" + r.ProviderRefundID,
			EventType:         "refund.processed",
			ProviderOrderID:   o.ProviderOrderID,
			ProviderPaymentID: o.ProviderPaymentID,
			ProviderRefundID:  r.ProviderRefundID,
			AmountMinor:       r.Amount.Minor,
			Currency:          r.Amount.Currency,
		})
		if err != nil && !errors.Is(err, ErrWebhookDuplicate) {
			slog.Error("payments: settling an already-refunded owed capture failed; retried on the next claim",
				"refund_required_id", o.ID, "provider_refund_id", r.ProviderRefundID, "error", err)
			return
		}
		applied++
	}
	if applied == 0 {
		s.parkOwedRefund(ctx, o, RefundFailAlreadyRefundedUnmatched, fmt.Sprintf(
			"provider reports payment %s fully refunded, but none of its %d refund(s) is processed",
			o.ProviderPaymentID, len(refunds)))
	}
}

// settleStubOwedRefund settles an owed capture on a stub deployment. No
// provider holds a stub payment, so the refund is final as soon as it is
// made; it is applied through ApplyWebhook, the same transaction a real
// refund event takes, under a refund id derived from the row so a retry is a
// duplicate, not a second refund.
func (s *Service) settleStubOwedRefund(ctx context.Context, o postgres.OwedCapture, money gateway.Money) {
	refundID := "rfnd_stub_" + o.IdempotencyKey()
	err := s.ApplyWebhook(ctx, WebhookInput{
		Provider:          o.Provider,
		EventID:           "stub_refund_" + refundID,
		EventType:         "refund.processed",
		ProviderOrderID:   o.ProviderOrderID,
		ProviderPaymentID: o.ProviderPaymentID,
		ProviderRefundID:  refundID,
		AmountMinor:       money.Minor,
		Currency:          money.Currency,
	})
	if err != nil && !errors.Is(err, ErrWebhookDuplicate) {
		slog.Error("payments: stub owed-capture refund failed; retried on the next claim",
			"refund_required_id", o.ID, "error", err)
	}
}

// parkOwedRefund stops the worker on a row and says so at ERROR: the money is
// still owed and a person must look.
func (s *Service) parkOwedRefund(ctx context.Context, o postgres.OwedCapture, code, reason string) {
	parked, err := s.store.ParkRefundRequired(ctx, o.ID, code, reason)
	if err != nil {
		slog.Warn("payments: could not park the owed capture; it will be attempted again",
			"refund_required_id", o.ID, "error", err)
		return
	}
	if parked {
		slog.Error("payments: OWED CAPTURE NEEDS ATTENTION — not refunded automatically; the money is still owed",
			"refund_required_id", o.ID, "intent_id", o.IntentID, "reason", o.Reason,
			"provider_payment_id", o.ProviderPaymentID, "amount_minor", o.AmountMinor,
			"reason_code", code, "detail", reason)
	}
}

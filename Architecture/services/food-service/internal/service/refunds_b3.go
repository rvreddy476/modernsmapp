package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Wave 1 B3: refunds of paid orders the SLA worker or the restaurant rejects.
// The store records the request and moves the order to REFUND_PENDING in the
// rejecting transaction; here it is submitted. A failed submission stays
// PENDING and ResubmitPendingSystemRefunds retries it from the SLA worker, so
// the money is never stranded silently.

// systemRefundRetryAge leaves a just-created request to its first submission.
const systemRefundRetryAge = time.Minute

// submitRejectedOrderRefund submits the refund requested with a rejection.
// Nothing was paid when there is no open request.
func (s *Service) submitRejectedOrderRefund(ctx context.Context, orderID uuid.UUID, reason string) {
	plan, err := s.store.OpenRefundPlan(ctx, orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "food-service: could not read the refund of a rejected order; the SLA worker will retry",
			"order_id", orderID, "error", err)
		return
	}
	if plan.Status != "PENDING" {
		return
	}
	if _, err := s.submitRefundPlan(ctx, orderID, plan, reason); err != nil {
		slog.ErrorContext(ctx, "food-service: refund of a rejected paid order not submitted; the SLA worker will retry",
			"order_id", orderID, "refund_id", plan.RefundID, "error", err)
	}
}

// ResubmitPendingSystemRefunds submits system refund requests that payments
// never accepted. It returns how many were submitted.
func (s *Service) ResubmitPendingSystemRefunds(ctx context.Context) (int, error) {
	plans, err := s.store.ListUnsubmittedSystemRefunds(ctx, systemRefundRetryAge, 50)
	if err != nil {
		return 0, err
	}
	submitted := 0
	for i := range plans {
		p := plans[i]
		if _, err := s.submitRefundPlan(ctx, p.OrderID, &p, "system refund resubmitted"); err != nil {
			slog.WarnContext(ctx, "food-service: system refund resubmission failed",
				"order_id", p.OrderID, "refund_id", p.RefundID, "error", err)
			continue
		}
		submitted++
	}
	return submitted, nil
}

// AdminListPayoutAccounts is the admin payout-account view, the only one that
// carries the shared-account review flag.
func (s *Service) AdminListPayoutAccounts(ctx context.Context, needsReviewOnly bool, page postgres.Pagination) ([]postgres.AdminPayoutAccount, error) {
	return s.store.AdminListPayoutAccounts(ctx, needsReviewOnly, page)
}

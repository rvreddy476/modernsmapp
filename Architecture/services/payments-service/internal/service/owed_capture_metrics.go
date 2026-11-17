package service

// The owed-capture alarm (com-2, com-15). payments.refund_required rows are
// money a buyer is owed back; these gauges are what pages when one waits.
// They are refreshed from the database on their own loop, NOT by the
// refund-required worker: a pod running with the worker off (the incident
// override) must still report the backlog, or the alarm goes quiet exactly
// when nothing is refunding.

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/payments-service/internal/obs"
)

// Labels of payments_owed_refund_failures_total and
// payments_owed_refunds_issued_total set by the worker itself (park codes are
// the rest of obs.OwedRefundFailureReasons).
const (
	owedFailRetryable  = "retryable"
	owedFailRefundList = "refund_list_failed"

	owedIssuedSubmitted       = "submitted"
	owedIssuedStubSettled     = "stub_settled"
	owedIssuedAlreadyRefunded = "already_refunded"
)

// RunOwedCaptureGauges refreshes the owed-capture gauges until ctx is
// cancelled, once at start and then every interval.
func (s *Service) RunOwedCaptureGauges(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	s.refreshOwedCaptureGauges(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.refreshOwedCaptureGauges(ctx)
		}
	}
}

// refreshOwedCaptureGauges sets the gauges from the database. A failed read
// leaves the last values (stale, never falsely zero) and says so.
func (s *Service) refreshOwedCaptureGauges(ctx context.Context) {
	b, err := s.store.OwedCaptureBacklog(ctx)
	if err != nil {
		slog.Warn("payments: could not count owed captures for the alarm gauges", "error", err)
		return
	}
	obs.SetOwedCaptures(b.Unrefunded, b.Parked, b.OldestAgeSeconds)
}

package workers

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/service"
)

// ---------------------------------------------------------------------------
// The payout rail's two workers (plan Phase 4C)
// ---------------------------------------------------------------------------
//
// Both are thin: the decisions live in service.SubmitPayouts and
// service.ReconcilePayouts so the integration tests drive exactly the
// code the tickers do. Neither starts unless payouts are enabled AND the
// RazorpayX client is configured (see StartAll).

const (
	// payoutSubmitInterval: how often requested rows are reserved and
	// reserved rows are submitted.
	payoutSubmitInterval = 5 * time.Minute
	// payoutReconcileInterval: how often submitted and processing rows
	// not heard about are fetched from the provider.
	payoutReconcileInterval = 10 * time.Minute
)

func runPayoutSubmitter(ctx context.Context, svc *service.Service) {
	slog.Info("payout submitter started", "interval", payoutSubmitInterval)
	ticker := time.NewTicker(payoutSubmitInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := svc.SubmitPayouts(ctx); err != nil {
				slog.Error("payout submitter: pass failed", "error", err)
			}
		}
	}
}

func runPayoutReconciler(ctx context.Context, svc *service.Service) {
	slog.Info("payout reconciler started", "interval", payoutReconcileInterval)
	ticker := time.NewTicker(payoutReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := svc.ReconcilePayouts(ctx); err != nil {
				slog.Error("payout reconciler: pass failed", "error", err)
			}
		}
	}
}

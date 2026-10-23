package workers

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/events"
	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// holdAgeLimit is the age after which unreleased balance holds are cleaned up.
const holdAgeLimit = 30 * 24 * time.Hour

// StartAll launches all background workers and blocks until ctx is cancelled.
// Call this in a goroutine: go workers.StartAll(ctx, store, producer, svc, payoutsEnabled).
// svc may be nil; if it is, the creator-fund workers are skipped (used in
// some bootstrap test setups that don't construct the service layer).
//
// payoutsEnabled mirrors MONETIZATION_PAYOUTS_ENABLED (plan Phase 3C):
// while it is false the payout submitter and reconciler do not start, so
// nothing touches a payout_requests row in the background. They also do
// not start when payouts are enabled but the RazorpayX client is not
// configured (svc.PayoutRailEnabled false): the rail is off and says so.
// Everything else — renewals, holds, fundraisers, the fund's accrual and
// settlement — is unaffected by it.
func StartAll(ctx context.Context, store *postgres.Store, producer *events.Producer, svc *service.Service, payoutsEnabled bool) {
	slog.Info("starting monetization background workers", "payouts_enabled", payoutsEnabled)

	go runSubscriptionRenewal(ctx, store, producer)
	go runStaleHoldCleanup(ctx, store)
	go runFundraiserExpiry(ctx, store, producer)
	// Grace ends → cancelled; cancelled_at_period_end at period end → expired.
	go runSubscriptionExpiry(ctx, store, producer)
	go runPauseResume(ctx, store, producer)
	go runLedgerReconciliation(ctx, store)
	go runStuckTransactionDetector(ctx, store)
	switch {
	case !payoutsEnabled:
		slog.Info("payout workers not started: MONETIZATION_PAYOUTS_ENABLED is false")
	case svc == nil || !svc.PayoutRailEnabled():
		slog.Warn("payout workers not started: payouts are enabled but the RazorpayX client is not configured (rail off)")
	default:
		go runPayoutSubmitter(ctx, svc)
		go runPayoutReconciler(ctx, svc)
	}
	if svc != nil {
		// Capture is continuous, payment is periodic. The accrual worker
		// measures each day and moves nothing; the settlement worker is
		// the only scheduled thing that credits a wallet from the fund.
		go runCreatorFundAccrual(ctx, svc)
		go runCreatorFundPeriodSettlement(ctx, store, svc)
		go runEligibilityEvaluator(ctx, svc)
	}

	<-ctx.Done()
	slog.Info("monetization workers stopped")
}

// The PayoutProcessor that used to live here — set 'processing', sleep two
// seconds, set 'paid', publish a Kafka event nothing consumed — was
// replaced in plan Phase 4C by the submitter and reconciler in
// payout_rail.go, which move money through a real provider.

// ---------------------------------------------------------------------------
// StaleHoldCleanup — runs every 6 hours
// ---------------------------------------------------------------------------

func runStaleHoldCleanup(ctx context.Context, store *postgres.Store) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanupStaleHolds(ctx, store)
		}
	}
}

func cleanupStaleHolds(ctx context.Context, store *postgres.Store) {
	cutoff := time.Now().Add(-holdAgeLimit)
	released, err := store.ReleaseStaleHolds(ctx, cutoff)
	if err != nil {
		slog.Error("stale hold cleanup: failed", "error", err)
		return
	}
	if released > 0 {
		slog.Info("stale hold cleanup: released holds", "count", released)
	}
}

// ---------------------------------------------------------------------------
// FundraiserExpiry — runs every hour
// ---------------------------------------------------------------------------

func runFundraiserExpiry(ctx context.Context, store *postgres.Store, producer *events.Producer) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expireFundraisers(ctx, store, producer)
		}
	}
}

func expireFundraisers(ctx context.Context, store *postgres.Store, producer *events.Producer) {
	expired, err := store.CloseExpiredFundraisers(ctx, time.Now())
	if err != nil {
		slog.Error("fundraiser expiry: failed", "error", err)
		return
	}

	for _, id := range expired {
		slog.Info("fundraiser expired", "fundraiser_id", id)
		// Publish analytics event for fundraiser completion.
		if pubErr := producer.PublishWalletCredited(ctx, uuid.New(), uuid.Nil, 0, "INR", "fundraiser_completed:"+id.String()); pubErr != nil {
			slog.Warn("fundraiser expiry: publish event", "error", pubErr)
		}
	}
}

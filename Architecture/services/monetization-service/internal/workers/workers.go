package workers

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/events"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// autoRenewThreshold is how far in advance to attempt renewal (1 day).
const autoRenewThreshold = 24 * time.Hour

// payoutAutoApproveLimit is the maximum amount (in minor currency units) that
// is auto-approved without manual review.
const payoutAutoApproveLimit = 10_000.0

// holdAgeLimit is the age after which unreleased balance holds are cleaned up.
const holdAgeLimit = 30 * 24 * time.Hour

// StartAll launches all background workers and blocks until ctx is cancelled.
// Call this in a goroutine: go workers.StartAll(ctx, store, producer).
func StartAll(ctx context.Context, store *postgres.Store, producer *events.Producer) {
	slog.Info("starting monetization background workers")

	go runSubscriptionRenewal(ctx, store, producer)
	go runPayoutProcessor(ctx, store, producer)
	go runStaleHoldCleanup(ctx, store)
	go runFundraiserExpiry(ctx, store, producer)

	<-ctx.Done()
	slog.Info("monetization workers stopped")
}

// ---------------------------------------------------------------------------
// SubscriptionRenewal — runs every hour
// ---------------------------------------------------------------------------

func runSubscriptionRenewal(ctx context.Context, store *postgres.Store, producer *events.Producer) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewSubscriptions(ctx, store, producer)
		}
	}
}

func renewSubscriptions(ctx context.Context, store *postgres.Store, producer *events.Producer) {
	cutoff := time.Now().Add(autoRenewThreshold)
	subs, err := store.GetSubscriptionsDueForRenewal(ctx, cutoff)
	if err != nil {
		slog.Error("subscription renewal: query failed", "error", err)
		return
	}

	for _, sub := range subs {
		// Determine new period end based on billing period.
		tier, err := store.GetCreatorTier(ctx, sub.TierID)
		if err != nil || tier == nil {
			slog.Warn("subscription renewal: tier not found", "tier_id", sub.TierID, "sub_id", sub.ID)
			continue
		}

		newPeriodEnd := extendPeriod(sub.CurrentPeriodEnd, tier.BillingPeriod)

		// Attempt to charge subscriber → credit creator.
		chargeErr := store.ChargeAndCredit(ctx, sub.SubscriberID.String(), sub.CreatorID.String(), sub.Price, "Subscription renewal: "+tier.Name)
		if chargeErr != nil {
			// Charge failed — mark payment_failed.
			slog.Warn("subscription renewal: charge failed",
				"sub_id", sub.ID, "subscriber_id", sub.SubscriberID, "error", chargeErr)
			if updateErr := store.SetSubscriptionPaymentFailed(ctx, sub.ID); updateErr != nil {
				slog.Error("subscription renewal: set payment_failed", "sub_id", sub.ID, "error", updateErr)
			}
			if pubErr := producer.PublishSubscriptionExpired(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, "payment_failed"); pubErr != nil {
				slog.Warn("subscription renewal: publish expired event", "error", pubErr)
			}
			continue
		}

		// Charge succeeded — extend period.
		if updateErr := store.ExtendSubscriptionPeriod(ctx, sub.ID, newPeriodEnd); updateErr != nil {
			slog.Error("subscription renewal: extend period failed", "sub_id", sub.ID, "error", updateErr)
			continue
		}

		if pubErr := producer.PublishSubscriptionRenewed(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, newPeriodEnd, sub.Price, sub.Currency); pubErr != nil {
			slog.Warn("subscription renewal: publish renewed event", "error", pubErr)
		}
		slog.Info("subscription renewed", "sub_id", sub.ID, "new_period_end", newPeriodEnd)
	}
}

// extendPeriod returns the new end time by advancing oldEnd by the billing period.
func extendPeriod(oldEnd time.Time, billingPeriod string) time.Time {
	switch billingPeriod {
	case "quarterly":
		return oldEnd.AddDate(0, 3, 0)
	case "yearly":
		return oldEnd.AddDate(1, 0, 0)
	default: // monthly
		return oldEnd.AddDate(0, 1, 0)
	}
}

// ---------------------------------------------------------------------------
// PayoutProcessor — runs every 30 minutes
// ---------------------------------------------------------------------------

func runPayoutProcessor(ctx context.Context, store *postgres.Store, producer *events.Producer) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processPayouts(ctx, store, producer)
		}
	}
}

func processPayouts(ctx context.Context, store *postgres.Store, producer *events.Producer) {
	// Find pending payout requests older than 24 hours.
	reviewCutoff := time.Now().Add(-24 * time.Hour)
	requests, err := store.GetPendingPayoutRequests(ctx, reviewCutoff)
	if err != nil {
		slog.Error("payout processor: query failed", "error", err)
		return
	}

	for _, req := range requests {
		if req.Amount > payoutAutoApproveLimit {
			// Hold for manual review.
			slog.Info("payout held for manual review", "request_id", req.ID, "amount", req.Amount)
			if updateErr := store.SetPayoutRequestStatus(ctx, req.ID, "held"); updateErr != nil {
				slog.Error("payout processor: set held status", "request_id", req.ID, "error", updateErr)
			}
			continue
		}

		// Auto-approve: set to processing.
		if updateErr := store.SetPayoutRequestStatus(ctx, req.ID, "processing"); updateErr != nil {
			slog.Error("payout processor: set processing status", "request_id", req.ID, "error", updateErr)
			continue
		}

		if pubErr := producer.PublishPayoutRequested(ctx, req.TransactionID, req.UserID, req.Amount, req.Currency, req.PayoutMethodID()); pubErr != nil {
			slog.Warn("payout processor: publish payout.requested", "error", pubErr)
		}

		// Mock payment gateway delay (2 seconds in a background routine per request).
		go func(r postgres.PayoutRequest) {
			time.Sleep(2 * time.Second)
			finalizePayout(ctx, store, producer, r)
		}(req)
	}
}

func finalizePayout(ctx context.Context, store *postgres.Store, producer *events.Producer, req postgres.PayoutRequest) {
	if updateErr := store.SetPayoutRequestPaid(ctx, req.ID); updateErr != nil {
		slog.Error("payout finalize: set paid status", "request_id", req.ID, "error", updateErr)
		return
	}

	if pubErr := producer.PublishPayoutProcessed(ctx, req.TransactionID, req.UserID, req.Amount, req.Currency); pubErr != nil {
		slog.Warn("payout finalize: publish payout.processed", "error", pubErr)
	}
	slog.Info("payout processed", "request_id", req.ID, "amount", req.Amount)
}

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

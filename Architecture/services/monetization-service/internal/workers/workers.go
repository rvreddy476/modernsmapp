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

// autoRenewThreshold is how far in advance to attempt renewal (1 day).
const autoRenewThreshold = 24 * time.Hour

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
	go runGracePeriodExpiry(ctx, store, producer)
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
		// reference_type='subscription' is what makes this renewal show up
		// on the creator's monthly statement. Without it a renewal looked
		// identical to a tip credit on the wallet, and the subscription
		// line under-reported everything past month one.
		chargeErr := store.ChargeAndCreditRef(ctx, sub.SubscriberID.String(), sub.CreatorID.String(), sub.PricePaise,
			"Subscription renewal: "+tier.Name, "subscription", sub.ID.String())
		if chargeErr != nil {
			// Charge failed — use retry/grace/cancel state machine.
			slog.Warn("subscription renewal: charge failed",
				"sub_id", sub.ID, "subscriber_id", sub.SubscriberID, "error", chargeErr)
			handleRenewalFailure(ctx, store, producer, sub)
			continue
		}

		// Charge succeeded — extend period.
		if updateErr := store.ExtendSubscriptionPeriod(ctx, sub.ID, newPeriodEnd); updateErr != nil {
			slog.Error("subscription renewal: extend period failed", "sub_id", sub.ID, "error", updateErr)
			continue
		}

		if pubErr := producer.PublishSubscriptionRenewed(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, newPeriodEnd, sub.PricePaise, sub.Currency); pubErr != nil {
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

// ---------------------------------------------------------------------------
// handleRenewalFailure — retry/grace/cancel state machine for failed charges
// ---------------------------------------------------------------------------

// maxRenewalRetries is the number of payment retries before entering grace.
const maxRenewalRetries = 3

// gracePeriodDuration is the length of the grace period after max retries.
const gracePeriodDuration = 7 * 24 * time.Hour

func handleRenewalFailure(ctx context.Context, store *postgres.Store, producer *events.Producer, sub postgres.SubscriptionForRenewal) {
	newRetryCount, err := store.IncrementRetryCount(ctx, sub.ID)
	if err != nil {
		slog.Error("renewal failure: increment retry count", "sub_id", sub.ID, "error", err)
		return
	}

	if newRetryCount < maxRenewalRetries {
		// Set to past_due
		if updateErr := store.SetSubscriptionStatus(ctx, sub.ID, "past_due"); updateErr != nil {
			slog.Error("renewal failure: set past_due", "sub_id", sub.ID, "error", updateErr)
			return
		}
		if logErr := store.InsertSubscriptionEvent(ctx, sub.ID, "payment_failed", "active", "past_due", nil); logErr != nil {
			slog.Warn("renewal failure: log event", "error", logErr)
		}
		slog.Info("subscription set to past_due", "sub_id", sub.ID, "retry_count", newRetryCount)
		return
	}

	// Max retries reached — enter grace period (7 days)
	graceEnd := time.Now().Add(gracePeriodDuration)
	if updateErr := store.SetSubscriptionGracePeriod(ctx, sub.ID, graceEnd); updateErr != nil {
		slog.Error("renewal failure: set grace period", "sub_id", sub.ID, "error", updateErr)
		return
	}
	if logErr := store.InsertSubscriptionEvent(ctx, sub.ID, "grace_started", "past_due", "grace", nil); logErr != nil {
		slog.Warn("renewal failure: log grace event", "error", logErr)
	}
	if pubErr := producer.PublishSubscriptionGraceStarted(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, graceEnd, newRetryCount); pubErr != nil {
		slog.Warn("renewal failure: publish grace_started", "error", pubErr)
	}
	slog.Info("subscription entered grace period", "sub_id", sub.ID, "grace_end", graceEnd)
}

// ---------------------------------------------------------------------------
// GracePeriodExpiry — runs every hour, cancels expired grace periods
// ---------------------------------------------------------------------------

func runGracePeriodExpiry(ctx context.Context, store *postgres.Store, producer *events.Producer) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expireGracePeriods(ctx, store, producer)
		}
	}
}

func expireGracePeriods(ctx context.Context, store *postgres.Store, producer *events.Producer) {
	subs, err := store.GetGracePeriodExpired(ctx, time.Now())
	if err != nil {
		slog.Error("grace period expiry: query failed", "error", err)
		return
	}

	for _, sub := range subs {
		if updateErr := store.SetSubscriptionStatus(ctx, sub.ID, "cancelled"); updateErr != nil {
			slog.Error("grace period expiry: cancel subscription", "sub_id", sub.ID, "error", updateErr)
			continue
		}
		if logErr := store.InsertSubscriptionEvent(ctx, sub.ID, "grace_expired", "grace", "cancelled", nil); logErr != nil {
			slog.Warn("grace period expiry: log event", "error", logErr)
		}
		if pubErr := producer.PublishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, "revoked"); pubErr != nil {
			slog.Warn("grace period expiry: publish entitlement changed", "error", pubErr)
		}
		if pubErr := producer.PublishSubscriptionExpired(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, "grace_expired"); pubErr != nil {
			slog.Warn("grace period expiry: publish expired event", "error", pubErr)
		}
		slog.Info("grace period expired, subscription cancelled", "sub_id", sub.ID)
	}
}

// ---------------------------------------------------------------------------
// PauseResume — runs every hour, resumes paused subscriptions past pause_until
// ---------------------------------------------------------------------------

func runPauseResume(ctx context.Context, store *postgres.Store, producer *events.Producer) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			resumePausedSubscriptions(ctx, store, producer)
		}
	}
}

func resumePausedSubscriptions(ctx context.Context, store *postgres.Store, producer *events.Producer) {
	subs, err := store.GetPausedSubscriptionsToResume(ctx, time.Now())
	if err != nil {
		slog.Error("pause resume: query failed", "error", err)
		return
	}

	for _, sub := range subs {
		if updateErr := store.ResumeSubscription(ctx, sub.ID); updateErr != nil {
			slog.Error("pause resume: resume subscription", "sub_id", sub.ID, "error", updateErr)
			continue
		}
		if logErr := store.InsertSubscriptionEvent(ctx, sub.ID, "auto_resumed", "paused", "active", nil); logErr != nil {
			slog.Warn("pause resume: log event", "error", logErr)
		}
		if pubErr := producer.PublishSubscriptionResumed(ctx, sub.ID, sub.SubscriberID, sub.CreatorID); pubErr != nil {
			slog.Warn("pause resume: publish resumed event", "error", pubErr)
		}
		slog.Info("paused subscription auto-resumed", "sub_id", sub.ID)
	}
}

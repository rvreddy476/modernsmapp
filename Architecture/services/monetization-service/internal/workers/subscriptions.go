package workers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Subscription workers: renewal (with the retry → grace → cancel state
// machine), period-end and grace-end expiry, and auto-resume. This is the
// ONE implementation of the renewal state machine.

// autoRenewThreshold is how far in advance to attempt renewal (1 day).
const autoRenewThreshold = 24 * time.Hour

// renewalRetryInterval is how long a past_due subscription waits after a
// failed charge before the next attempt. With the first attempt a day
// before the period ends, three attempts fit inside the paid period.
const renewalRetryInterval = 6 * time.Hour

// maxRenewalRetries is the number of failed charges that moves a
// subscription from past_due into grace.
const maxRenewalRetries = 3

// gracePeriodDuration is the length of the grace period after max retries.
const gracePeriodDuration = 7 * 24 * time.Hour

// subscriptionStore is what the subscription workers read and write.
// *postgres.Store satisfies it; tests use a fake.
type subscriptionStore interface {
	GetSubscriptionsDueForRenewal(ctx context.Context, before, retryBefore time.Time) ([]postgres.SubscriptionForRenewal, error)
	GetCreatorTierWithBillingPeriod(ctx context.Context, tierID uuid.UUID) (*postgres.CreatorTier, error)
	RenewSubscription(ctx context.Context, in postgres.RenewalInput) error
	MarkRenewalPastDue(ctx context.Context, subID uuid.UUID, expectedPeriodEnd time.Time, retryCount int, now time.Time) error
	EnterRenewalGrace(ctx context.Context, subID uuid.UUID, expectedPeriodEnd time.Time, retryCount int, now, graceEnd time.Time) error
	CancelAtPeriodEnd(ctx context.Context, subID uuid.UUID, reason string) error
	InsertSubscriptionEvent(ctx context.Context, subID uuid.UUID, eventType, oldStatus, newStatus string, metadata json.RawMessage) error
	GetGracePeriodExpired(ctx context.Context, now time.Time) ([]postgres.Subscription, error)
	SetSubscriptionStatus(ctx context.Context, subID uuid.UUID, status string) error
	ExpireEndedCancellations(ctx context.Context, now time.Time) ([]postgres.Subscription, error)
	GetPausedSubscriptionsToResume(ctx context.Context, now time.Time) ([]postgres.Subscription, error)
	ResumeSubscription(ctx context.Context, subID uuid.UUID) error
}

// subscriptionPublisher is the slice of *events.Producer the workers use.
type subscriptionPublisher interface {
	PublishSubscriptionRenewed(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, newPeriodEnd time.Time, amountPaise int64, currency string) error
	PublishSubscriptionGraceStarted(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, graceEnd time.Time, retryCount int) error
	PublishSubscriptionExpired(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, reason string) error
	PublishSubscriptionResumed(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID) error
	PublishEntitlementChanged(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, action string) error
}

// every runs fn on each tick of interval until ctx ends.
func every(ctx context.Context, interval time.Duration, fn func(now time.Time)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(time.Now())
		}
	}
}

// ---------------------------------------------------------------------------
// SubscriptionRenewal — runs every hour
// ---------------------------------------------------------------------------

func runSubscriptionRenewal(ctx context.Context, store subscriptionStore, pub subscriptionPublisher) {
	every(ctx, time.Hour, func(now time.Time) { renewSubscriptions(ctx, store, pub, now) })
}

// nextMonthlyPeriod is the next billing period: monthly is the only billing
// period offered, so it is one calendar month. It starts where the paid
// period ended — or now, if that is already past (a subscription that sat
// paused or past_due beyond its end does not pay for the gap).
func nextMonthlyPeriod(oldEnd, now time.Time) (start, end time.Time) {
	start = oldEnd
	if now.After(start) {
		start = now
	}
	return start, start.AddDate(0, 1, 0)
}

func renewSubscriptions(ctx context.Context, store subscriptionStore, pub subscriptionPublisher, now time.Time) {
	subs, err := store.GetSubscriptionsDueForRenewal(ctx, now.Add(autoRenewThreshold), now.Add(-renewalRetryInterval))
	if err != nil {
		slog.Error("subscription renewal: query failed", "error", err)
		return
	}
	for _, sub := range subs {
		renewOne(ctx, store, pub, sub, now)
	}
}

func renewOne(ctx context.Context, store subscriptionStore, pub subscriptionPublisher, sub postgres.SubscriptionForRenewal, now time.Time) {
	// The tier the next period is on: a scheduled downgrade's, or this one.
	tierID, price, downgrade := sub.TierID, sub.PricePaise, false
	if sub.PendingTierID != nil && sub.PendingPricePaise != nil {
		tierID, price, downgrade = *sub.PendingTierID, *sub.PendingPricePaise, true
	}

	tier, err := store.GetCreatorTierWithBillingPeriod(ctx, tierID)
	if err != nil {
		slog.Warn("subscription renewal: tier lookup failed", "tier_id", tierID, "sub_id", sub.ID, "error", err)
		return
	}
	if tier == nil || !tier.IsActive {
		// A retired tier is not sold again: the member keeps what they paid
		// for and the subscription ends with the period.
		if err := store.CancelAtPeriodEnd(ctx, sub.ID, "tier_inactive"); err != nil {
			slog.Error("subscription renewal: cancel at period end for inactive tier", "sub_id", sub.ID, "error", err)
			return
		}
		meta, _ := json.Marshal(map[string]string{"reason": "tier_inactive", "tier_id": tierID.String()})
		if err := store.InsertSubscriptionEvent(ctx, sub.ID, "renewal_skipped_tier_inactive", sub.Status, "cancelled_at_period_end", meta); err != nil {
			slog.Warn("subscription renewal: log event", "error", err)
		}
		slog.Info("subscription not renewed: tier inactive", "sub_id", sub.ID, "tier_id", tierID)
		return
	}
	if bp := tier.BillingPeriod; bp != "" && bp != "monthly" {
		// Only monthly is offered. Charging this tier's price for any other
		// period length would be guessing; refuse loudly instead.
		slog.Error("subscription renewal: unsupported billing period, not renewed", "sub_id", sub.ID, "tier_id", tierID, "billing_period", bp)
		return
	}

	start, end := nextMonthlyPeriod(sub.CurrentPeriodEnd, now)
	err = store.RenewSubscription(ctx, postgres.RenewalInput{
		SubscriptionID:    sub.ID,
		SubscriberID:      sub.SubscriberID,
		CreatorID:         sub.CreatorID,
		ExpectedPeriodEnd: sub.CurrentPeriodEnd,
		NewPeriodStart:    start,
		NewPeriodEnd:      end,
		TierID:            tier.ID,
		TierName:          tier.Name,
		PricePaise:        price,
		Currency:          sub.Currency,
		IdempotencyKey:    postgres.RenewalIdempotencyKey(sub.ID, sub.CurrentPeriodEnd),
		// reference_type='subscription' (set by the store) is what puts the
		// renewal on the creator's monthly statement.
		Description: "Subscription renewal: " + tier.Name,
	})
	switch {
	case err == nil:
	case errors.Is(err, postgres.ErrRenewalAlreadyApplied), errors.Is(err, postgres.ErrSubscriptionStateChanged):
		// Another attempt already renewed (or changed) this period. Nothing
		// was charged by this one.
		slog.Info("subscription renewal: already renewed or changed, skipped", "sub_id", sub.ID, "reason", err)
		return
	case errors.Is(err, postgres.ErrInsufficientFunds):
		slog.Warn("subscription renewal: charge failed", "sub_id", sub.ID, "subscriber_id", sub.SubscriberID)
		handleRenewalFailure(ctx, store, pub, sub, now)
		return
	default:
		// A database error is not a payment failure: retry next tick
		// without counting it against the subscriber.
		slog.Error("subscription renewal: renewal transaction failed", "sub_id", sub.ID, "error", err)
		return
	}

	eventType, action := "renewed", "renewed"
	if downgrade {
		eventType, action = "downgraded", "downgraded"
	}
	meta, _ := json.Marshal(map[string]any{"tier_id": tier.ID.String(), "amount_paise": price, "new_period_end": end.Format(time.RFC3339)})
	if err := store.InsertSubscriptionEvent(ctx, sub.ID, eventType, sub.Status, "active", meta); err != nil {
		slog.Warn("subscription renewal: log event", "error", err)
	}
	if err := pub.PublishSubscriptionRenewed(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, end, price, sub.Currency); err != nil {
		slog.Warn("subscription renewal: publish renewed event", "error", err)
	}
	// A renewal can restore access (past_due beyond the period end) or
	// change the tier (a downgrade applied): tell the caches.
	if err := pub.PublishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, action); err != nil {
		slog.Warn("subscription renewal: publish entitlement changed", "error", err)
	}
	slog.Info("subscription renewed", "sub_id", sub.ID, "new_period_end", end, "downgrade_applied", downgrade)
}

// handleRenewalFailure is the retry/grace state machine for a failed
// charge: the first maxRenewalRetries-1 failures leave it past_due (retried
// after renewalRetryInterval); the last one starts grace. Grace expiry
// (expireGracePeriods) then cancels.
func handleRenewalFailure(ctx context.Context, store subscriptionStore, pub subscriptionPublisher, sub postgres.SubscriptionForRenewal, now time.Time) {
	retries := sub.RetryCount + 1
	if retries < maxRenewalRetries {
		if err := store.MarkRenewalPastDue(ctx, sub.ID, sub.CurrentPeriodEnd, retries, now); err != nil {
			slog.Error("renewal failure: set past_due", "sub_id", sub.ID, "error", err)
			return
		}
		meta, _ := json.Marshal(map[string]int{"retry_count": retries})
		if err := store.InsertSubscriptionEvent(ctx, sub.ID, "payment_failed", sub.Status, "past_due", meta); err != nil {
			slog.Warn("renewal failure: log event", "error", err)
		}
		slog.Info("subscription set to past_due", "sub_id", sub.ID, "retry_count", retries)
		return
	}

	graceEnd := now.Add(gracePeriodDuration)
	if err := store.EnterRenewalGrace(ctx, sub.ID, sub.CurrentPeriodEnd, retries, now, graceEnd); err != nil {
		slog.Error("renewal failure: set grace period", "sub_id", sub.ID, "error", err)
		return
	}
	meta, _ := json.Marshal(map[string]any{"retry_count": retries, "grace_period_end": graceEnd.Format(time.RFC3339)})
	if err := store.InsertSubscriptionEvent(ctx, sub.ID, "grace_started", sub.Status, "grace", meta); err != nil {
		slog.Warn("renewal failure: log grace event", "error", err)
	}
	if err := pub.PublishSubscriptionGraceStarted(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, graceEnd, retries); err != nil {
		slog.Warn("renewal failure: publish grace_started", "error", err)
	}
	// Grace's access window is grace_period_end, not the period end.
	if err := pub.PublishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, "grace_started"); err != nil {
		slog.Warn("renewal failure: publish entitlement changed", "error", err)
	}
	slog.Info("subscription entered grace period", "sub_id", sub.ID, "grace_end", graceEnd)
}

// ---------------------------------------------------------------------------
// SubscriptionExpiry — runs every hour: grace ends → cancelled, and
// cancelled_at_period_end reaching its period end → expired
// ---------------------------------------------------------------------------

func runSubscriptionExpiry(ctx context.Context, store subscriptionStore, pub subscriptionPublisher) {
	every(ctx, time.Hour, func(now time.Time) {
		expireGracePeriods(ctx, store, pub, now)
		expireEndedCancellations(ctx, store, pub, now)
	})
}

func expireGracePeriods(ctx context.Context, store subscriptionStore, pub subscriptionPublisher, now time.Time) {
	subs, err := store.GetGracePeriodExpired(ctx, now)
	if err != nil {
		slog.Error("grace period expiry: query failed", "error", err)
		return
	}
	for _, sub := range subs {
		if err := store.SetSubscriptionStatus(ctx, sub.ID, "cancelled"); err != nil {
			slog.Error("grace period expiry: cancel subscription", "sub_id", sub.ID, "error", err)
			continue
		}
		if err := store.InsertSubscriptionEvent(ctx, sub.ID, "grace_expired", "grace", "cancelled", nil); err != nil {
			slog.Warn("grace period expiry: log event", "error", err)
		}
		if err := pub.PublishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, "revoked"); err != nil {
			slog.Warn("grace period expiry: publish entitlement changed", "error", err)
		}
		if err := pub.PublishSubscriptionExpired(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, "grace_expired"); err != nil {
			slog.Warn("grace period expiry: publish expired event", "error", err)
		}
		slog.Info("grace period expired, subscription cancelled", "sub_id", sub.ID)
	}
}

// expireEndedCancellations closes subscriptions cancelled at period end
// once that period is over: cancelled_at_period_end → expired.
func expireEndedCancellations(ctx context.Context, store subscriptionStore, pub subscriptionPublisher, now time.Time) {
	subs, err := store.ExpireEndedCancellations(ctx, now)
	if err != nil {
		slog.Error("cancellation expiry: failed", "error", err)
		return
	}
	for _, sub := range subs {
		if err := store.InsertSubscriptionEvent(ctx, sub.ID, "period_ended", "cancelled_at_period_end", "expired", nil); err != nil {
			slog.Warn("cancellation expiry: log event", "error", err)
		}
		if err := pub.PublishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, "revoked"); err != nil {
			slog.Warn("cancellation expiry: publish entitlement changed", "error", err)
		}
		if err := pub.PublishSubscriptionExpired(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, "cancelled_at_period_end"); err != nil {
			slog.Warn("cancellation expiry: publish expired event", "error", err)
		}
		slog.Info("cancelled subscription reached period end, expired", "sub_id", sub.ID)
	}
}

// ---------------------------------------------------------------------------
// PauseResume — runs every hour, resumes paused subscriptions past pause_until
// ---------------------------------------------------------------------------

func runPauseResume(ctx context.Context, store subscriptionStore, pub subscriptionPublisher) {
	every(ctx, time.Hour, func(now time.Time) { resumePausedSubscriptions(ctx, store, pub, now) })
}

func resumePausedSubscriptions(ctx context.Context, store subscriptionStore, pub subscriptionPublisher, now time.Time) {
	subs, err := store.GetPausedSubscriptionsToResume(ctx, now)
	if err != nil {
		slog.Error("pause resume: query failed", "error", err)
		return
	}
	for _, sub := range subs {
		if err := store.ResumeSubscription(ctx, sub.ID); err != nil {
			slog.Error("pause resume: resume subscription", "sub_id", sub.ID, "error", err)
			continue
		}
		if err := store.InsertSubscriptionEvent(ctx, sub.ID, "auto_resumed", "paused", "active", nil); err != nil {
			slog.Warn("pause resume: log event", "error", err)
		}
		if err := pub.PublishSubscriptionResumed(ctx, sub.ID, sub.SubscriberID, sub.CreatorID); err != nil {
			slog.Warn("pause resume: publish resumed event", "error", err)
		}
		if err := pub.PublishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, "resumed"); err != nil {
			slog.Warn("pause resume: publish entitlement changed", "error", err)
		}
		slog.Info("paused subscription auto-resumed", "sub_id", sub.ID)
	}
}

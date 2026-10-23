package workers

// Membership workers (phase 1 contract, 23 Oct 2026). They run when
// MEMBERSHIPS_ENABLED is on, whatever MONETIZATION_WRITES_ENABLED says
// (runmode.Mode.StartMembershipWorkers), and NONE of them charges anyone:
// renewal is by reminder, the member taps Pay.
//
//   - pending sweep: a join unpaid for 30 minutes → expired (its open
//     payment rows failed; a capture that still arrives is refunded as late);
//   - period end: active / past_due past current_period_end → grace for
//     3 days (reminders);
//   - expiry: grace past grace_period_end, cancelled_at_period_end and
//     paused past their period end → expired; a counted row leaves its
//     tier's subscriber_count; membership.ended + entitlement revoked;
//   - reminders: membership.renewal_due 3 days before the period end and on
//     the day, once each per period (membership_renewal_reminders);
//   - refunds: queued refunds (late captures, support) filed with payments
//     under their deterministic keys, retried with backoff;
//   - auto-resume: a pause whose pause_until has passed.
//
// The wallet-charging renewal worker and its retry/past_due machine were
// removed with migration 025.

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/events"
	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Pass intervals.
const (
	membershipPassInterval = 5 * time.Minute
	refundPassInterval     = 2 * time.Minute
	resumePassInterval     = time.Hour
)

// membershipStore is what the membership workers read and write.
// *postgres.Store satisfies it; tests use membershiptest.Store.
type membershipStore interface {
	SweepStalePendingJoins(ctx context.Context, cutoff time.Time) ([]postgres.Subscription, error)
	StartGraceForEndedPeriods(ctx context.Context, now time.Time) ([]postgres.Subscription, error)
	ExpireEndedMemberships(ctx context.Context, now time.Time) ([]postgres.ExpiredMembership, error)
	DueRenewalReminders(ctx context.Context, now time.Time) ([]postgres.ReminderDue, error)
	ClaimRenewalReminder(ctx context.Context, subID uuid.UUID, periodEnd time.Time, daysLeft int) (bool, error)
	ReleaseRenewalReminder(ctx context.Context, subID uuid.UUID, periodEnd time.Time, daysLeft int) error
	GetPausedSubscriptionsToResume(ctx context.Context, now time.Time) ([]postgres.Subscription, error)
	ResumeSubscription(ctx context.Context, subID uuid.UUID) error
	InsertSubscriptionEvent(ctx context.Context, subID uuid.UUID, eventType, oldStatus, newStatus string, metadata json.RawMessage) error
}

// membershipPublisher is the slice of *events.Producer the workers use.
type membershipPublisher interface {
	PublishMembershipEvent(ctx context.Context, eventType, key string, p events.MembershipPayload) error
	PublishEntitlementChanged(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, action string) error
	PublishSubscriptionResumed(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID) error
}

// refundSubmitter files queued refunds (*service.Service).
type refundSubmitter interface {
	SubmitDueMembershipRefunds(ctx context.Context) (int, error)
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

// StartMembershipWorkers launches the membership workers and blocks until
// ctx ends. Call it in a goroutine.
func StartMembershipWorkers(ctx context.Context, store *postgres.Store, producer *events.Producer, svc *service.Service) {
	slog.Info("starting membership workers (pending sweep, period end → grace → expired, reminders, refunds, auto-resume)")
	var pub membershipPublisher = nopPublisher{}
	if producer != nil {
		pub = producer
	}
	go every(ctx, membershipPassInterval, func(now time.Time) { runMembershipPass(ctx, store, pub, now) })
	if svc != nil {
		go every(ctx, refundPassInterval, func(time.Time) { submitRefunds(ctx, svc) })
	}
	go every(ctx, resumePassInterval, func(now time.Time) { resumePausedSubscriptions(ctx, store, pub, now) })
	<-ctx.Done()
	slog.Info("membership workers stopped")
}

type nopPublisher struct{}

func (nopPublisher) PublishMembershipEvent(context.Context, string, string, events.MembershipPayload) error {
	return nil
}
func (nopPublisher) PublishEntitlementChanged(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string) error {
	return nil
}
func (nopPublisher) PublishSubscriptionResumed(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}

// runMembershipPass is one pass, in order: sweep pending joins, start grace,
// expire, remind.
func runMembershipPass(ctx context.Context, store membershipStore, pub membershipPublisher, now time.Time) {
	sweepPendingJoins(ctx, store, now)
	startGrace(ctx, store, pub, now)
	expireEnded(ctx, store, pub, now)
	sendRenewalReminders(ctx, store, pub, now)
}

func logEvent(ctx context.Context, store membershipStore, subID uuid.UUID, eventType, from, to string, meta map[string]any) {
	var raw json.RawMessage
	if meta != nil {
		raw, _ = json.Marshal(meta)
	}
	if err := store.InsertSubscriptionEvent(ctx, subID, eventType, from, to, raw); err != nil {
		slog.Warn("membership worker: log subscription event", "sub_id", subID, "event", eventType, "error", err)
	}
}

func sweepPendingJoins(ctx context.Context, store membershipStore, now time.Time) {
	subs, err := store.SweepStalePendingJoins(ctx, now.Add(-membership.PendingTTL))
	if err != nil {
		slog.Error("membership worker: pending sweep failed", "error", err)
		return
	}
	for _, sub := range subs {
		// Never activated: no access to revoke, nothing to notify.
		logEvent(ctx, store, sub.ID, "pending_expired", membership.StatusPendingPayment, membership.StatusExpired, nil)
	}
	if len(subs) > 0 {
		slog.Info("membership worker: stale pending joins expired", "count", len(subs))
	}
}

func startGrace(ctx context.Context, store membershipStore, pub membershipPublisher, now time.Time) {
	subs, err := store.StartGraceForEndedPeriods(ctx, now)
	if err != nil {
		slog.Error("membership worker: start grace failed", "error", err)
		return
	}
	for _, sub := range subs {
		meta := map[string]any{}
		if sub.GracePeriodEnd != nil {
			meta["grace_period_end"] = sub.GracePeriodEnd.UTC().Format(time.RFC3339)
		}
		logEvent(ctx, store, sub.ID, "grace_started", membership.StatusActive, membership.StatusGrace, meta)
		// Access now runs to grace_period_end: tell the caches.
		if err := pub.PublishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, "grace_started"); err != nil {
			slog.Warn("membership worker: publish entitlement changed", "sub_id", sub.ID, "error", err)
		}
	}
}

func expireEnded(ctx context.Context, store membershipStore, pub membershipPublisher, now time.Time) {
	ended, err := store.ExpireEndedMemberships(ctx, now)
	if err != nil {
		slog.Error("membership worker: expiry failed", "error", err)
		return
	}
	for i := range ended {
		e := &ended[i]
		logEvent(ctx, store, e.ID, "expired", e.From, membership.StatusExpired, nil)
		if err := pub.PublishEntitlementChanged(ctx, e.ID, e.SubscriberID, e.CreatorID, membership.EntitlementRevoked); err != nil {
			slog.Warn("membership worker: publish entitlement changed", "sub_id", e.ID, "error", err)
		}
		if err := pub.PublishMembershipEvent(ctx, membership.EventEnded, service.EndedEventKey(e.ID, e.CurrentPeriodEnd), service.MembershipEndedPayload(&e.Subscription)); err != nil {
			slog.Warn("membership worker: publish membership.ended", "sub_id", e.ID, "error", err)
		}
	}
	if len(ended) > 0 {
		slog.Info("membership worker: memberships expired", "count", len(ended))
	}
}

// sendRenewalReminders claims each due reminder, then publishes it. A
// publish that fails releases the claim, so the next pass sends it: once
// each per period, never silently dropped.
func sendRenewalReminders(ctx context.Context, store membershipStore, pub membershipPublisher, now time.Time) {
	due, err := store.DueRenewalReminders(ctx, now)
	if err != nil {
		slog.Error("membership worker: reminder query failed", "error", err)
		return
	}
	for i := range due {
		r := &due[i]
		claimed, err := store.ClaimRenewalReminder(ctx, r.ID, r.CurrentPeriodEnd, r.DaysLeft)
		if err != nil {
			slog.Error("membership worker: claim reminder", "sub_id", r.ID, "error", err)
			continue
		}
		if !claimed {
			continue
		}
		payload := service.RenewalDuePayload(&r.Subscription, r.RenewalPricePaise, r.DaysLeft)
		if err := pub.PublishMembershipEvent(ctx, membership.EventRenewalDue, service.RenewalDueEventKey(r.ID, r.CurrentPeriodEnd, r.DaysLeft), payload); err != nil {
			slog.Warn("membership worker: publish renewal_due; will retry", "sub_id", r.ID, "error", err)
			if rerr := store.ReleaseRenewalReminder(ctx, r.ID, r.CurrentPeriodEnd, r.DaysLeft); rerr != nil {
				slog.Error("membership worker: release reminder claim", "sub_id", r.ID, "error", rerr)
			}
		}
	}
}

func submitRefunds(ctx context.Context, svc refundSubmitter) {
	if n, err := svc.SubmitDueMembershipRefunds(ctx); err != nil {
		slog.Error("membership worker: refund pass failed", "error", err)
	} else if n > 0 {
		slog.Info("membership worker: refunds filed", "count", n)
	}
}

func resumePausedSubscriptions(ctx context.Context, store membershipStore, pub membershipPublisher, now time.Time) {
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
		logEvent(ctx, store, sub.ID, "auto_resumed", membership.StatusPaused, membership.StatusActive, nil)
		if err := pub.PublishSubscriptionResumed(ctx, sub.ID, sub.SubscriberID, sub.CreatorID); err != nil {
			slog.Warn("pause resume: publish resumed event", "error", err)
		}
		if err := pub.PublishEntitlementChanged(ctx, sub.ID, sub.SubscriberID, sub.CreatorID, "resumed"); err != nil {
			slog.Warn("pause resume: publish entitlement changed", "error", err)
		}
	}
}

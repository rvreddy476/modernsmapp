// Package jobs holds the rider-service background-job implementations.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §15. Each job is a single function that
// the cron framework wraps with a rider_cron_runs row + idempotency
// guard. Jobs MUST be idempotent on re-run; the wallet-touching ones
// rely on the per-payment idempotency key + DB cooldown columns, the
// notification-emitting ones rely on per-bucket dedupe rows.
//
// File-by-file:
//   - subscription_expiry.go — Sprint 4 jobs 1-3 (expiring reminders,
//     grace transition, wallet auto-renewal).
//   - document_expiry.go     — job 4 (doc expiry reminders).
//   - ride_cleanup.go        — jobs 5-6 (stale ride sweep + offer expiry).
//   - metrics.go             — job 7 (partner metrics recalc).
//   - fraud.go               — job 8 (fraud-score recalc + auto-suspend).
//   - revenue.go             — job 9 (daily revenue rollup).
//   - admin_summary.go       — job 10 (daily admin queue summary email).
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/rider-service/internal/wallet"
	"github.com/google/uuid"
)

// SubscriptionPublisher is the subset of the event-publisher contract the
// subscription-expiry / grace / renewal jobs need.
type SubscriptionPublisher interface {
	PublishSubscriptionGracePeriod(ctx context.Context, payload events.SubscriptionGracePayload) error
	PublishSubscriptionExpired(ctx context.Context, payload events.SubscriptionGracePayload) error
	PublishSubscriptionRenewed(ctx context.Context, payload events.SubscriptionRenewedPayload) error
	PublishSubscriptionRenewalFailed(ctx context.Context, payload events.SubscriptionRenewalFailedPayload) error
}

// SubscriptionExpiringPublisher is the contract for the
// "subscription-expiring" reminder. Lives separately so callers can pass
// a struct that already implements the existing PublishSubscription*
// methods on the main producer.
type SubscriptionExpiringPublisher interface {
	PublishSubscriptionGracePeriod(ctx context.Context, payload events.SubscriptionGracePayload) error
}

// RunSubscriptionExpiryChecker emits a "subscription expiring" event for
// each subscription within 7d/3d/1d of expiry, deduped via
// rider_doc_reminders_sent (we re-use that table — bucket key includes
// "sub:" prefix to namespace from doc reminders).
//
// Idempotent: ON CONFLICT (document_id, bucket) makes a re-run a no-op.
// Returns the count of reminders newly sent.
func RunSubscriptionExpiryChecker(ctx context.Context, st *store.Store, pub SubscriptionExpiringPublisher) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("subscription expiry: store required")
	}
	subs, err := st.ListExpiringSubscriptions(ctx, 7*24*time.Hour)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	sent := 0
	for i := range subs {
		s := &subs[i]
		// Pick the tightest bucket the row falls into.
		until := s.ExpiresAt.Sub(now)
		var bucket string
		switch {
		case until <= 24*time.Hour:
			bucket = "1d"
		case until <= 3*24*time.Hour:
			bucket = "3d"
		case until <= 7*24*time.Hour:
			bucket = "7d"
		default:
			continue
		}
		// We re-purpose rider_doc_reminders_sent as the dedupe table; the
		// "document_id" slot stores the subscription id and the bucket
		// label is namespaced "sub:<bucket>" so it never collides with a
		// real document reminder.
		newlyInserted, err := st.MarkReminderSent(ctx, s.PartnerID, s.ID, s.ExpiresAt, "sub:"+bucket)
		if err != nil {
			slog.Warn("rider job: subscription expiry mark sent failed",
				"subscription_id", s.ID, "bucket", bucket, "error", err)
			continue
		}
		if !newlyInserted {
			continue
		}
		// Reuse the grace-period payload shape — it has all the fields
		// notification-service expects for the expiring push.
		payload := events.SubscriptionGracePayload{
			SubscriptionID: s.ID.String(),
			PartnerID:      s.PartnerID.String(),
			PlanID:         s.PlanID.String(),
			ExpiresAt:      s.ExpiresAt,
			OccurredAt:     time.Now().UTC(),
		}
		if pub != nil {
			if perr := pub.PublishSubscriptionGracePeriod(ctx, payload); perr != nil {
				slog.Warn("rider job: subscription expiry publish failed",
					"subscription_id", s.ID, "bucket", bucket, "error", perr)
			}
		}
		sent++
	}
	return sent, nil
}

// RunGracePeriodTransition flips active->grace_period and
// grace_period->expired in two SQL statements. Each transition emits one
// event. Idempotent: each UPDATE has a "WHERE status = expected" predicate
// so a second pass within the same minute is a no-op.
func RunGracePeriodTransition(ctx context.Context, st *store.Store, pub SubscriptionPublisher) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("grace period transition: store required")
	}
	flippedToGrace, err := st.FlipToGracePeriod(ctx)
	if err != nil {
		return 0, fmt.Errorf("flip to grace: %w", err)
	}
	flippedToExpired, err := st.FlipToExpired(ctx)
	if err != nil {
		return len(flippedToGrace), fmt.Errorf("flip to expired: %w", err)
	}
	for i := range flippedToGrace {
		s := &flippedToGrace[i]
		var graceEnds time.Time
		if s.GraceEndsAt != nil {
			graceEnds = *s.GraceEndsAt
		}
		payload := events.SubscriptionGracePayload{
			SubscriptionID: s.ID.String(),
			PartnerID:      s.PartnerID.String(),
			PlanID:         s.PlanID.String(),
			ExpiresAt:      s.ExpiresAt,
			GraceEndsAt:    graceEnds,
			OccurredAt:     time.Now().UTC(),
		}
		if pub != nil {
			if perr := pub.PublishSubscriptionGracePeriod(ctx, payload); perr != nil {
				slog.Warn("rider job: grace period publish failed",
					"subscription_id", s.ID, "error", perr)
			}
		}
	}
	// Second arm: grace_period -> expired. Distinct event type so
	// notification-service can switch on it.
	for i := range flippedToExpired {
		s := &flippedToExpired[i]
		var graceEnds time.Time
		if s.GraceEndsAt != nil {
			graceEnds = *s.GraceEndsAt
		}
		payload := events.SubscriptionGracePayload{
			SubscriptionID: s.ID.String(),
			PartnerID:      s.PartnerID.String(),
			PlanID:         s.PlanID.String(),
			ExpiresAt:      s.ExpiresAt,
			GraceEndsAt:    graceEnds,
			OccurredAt:     time.Now().UTC(),
		}
		if pub != nil {
			if perr := pub.PublishSubscriptionExpired(ctx, payload); perr != nil {
				slog.Warn("rider job: expired publish failed",
					"subscription_id", s.ID, "error", perr)
			}
		}
	}
	return len(flippedToGrace) + len(flippedToExpired), nil
}

// RunSubscriptionAutoRenewal walks subscriptions where auto_renew=true and
// expires_at is within the lookahead window. For each candidate it
// debits the partner's wallet for the plan price; on success it extends
// expires_at and resets renewal_failure_count; on failure it bumps the
// counter and (at 3) flips auto_renew=false.
//
// Idempotent: the renewal_attempted_at cooldown is the per-row guard so
// a second pass an hour later doesn't re-debit. The wallet-side
// idempotency key includes the subscription id + cycle counter so a
// retry within the cooldown returns the same wallet response (no
// duplicate debit).
func RunSubscriptionAutoRenewal(ctx context.Context, st *store.Store, w wallet.Client, pub SubscriptionPublisher) (int, error) {
	if st == nil || w == nil {
		return 0, fmt.Errorf("subscription auto-renewal: store + wallet required")
	}
	candidates, err := st.ListAutoRenewCandidates(ctx, 12*time.Hour, 1*time.Hour)
	if err != nil {
		return 0, err
	}
	processed := 0
	for i := range candidates {
		s := &candidates[i]
		plan, err := st.GetPlan(ctx, s.PlanID)
		if err != nil {
			slog.Warn("rider job: auto-renewal plan lookup failed",
				"subscription_id", s.ID, "plan_id", s.PlanID, "error", err)
			continue
		}
		amountPaise := int64(plan.PriceAmount * 100)
		// The idempotency key combines the subscription id + the previous
		// expires_at unix-epoch — that gives us a fresh key per cycle so a
		// successful renewal does NOT collide with the next cycle's
		// attempt; a same-cycle retry returns the cached debit.
		idemKey := fmt.Sprintf("rider-auto-renew-%s-%s",
			s.ID.String(), strconv.FormatInt(s.ExpiresAt.Unix(), 10))

		// Look up the partner so we can hit wallet with the user_id.
		partner, perr := st.GetPartner(ctx, s.PartnerID)
		if perr != nil {
			slog.Warn("rider job: auto-renewal partner lookup failed",
				"subscription_id", s.ID, "partner_id", s.PartnerID, "error", perr)
			continue
		}
		debit, derr := w.DebitForSubscription(ctx, partner.UserID, amountPaise, s.ID, idemKey)
		if derr != nil {
			newCount, autoDisabled, ierr := st.IncrementRenewalFailure(ctx, s.ID, 3)
			if ierr != nil {
				slog.Warn("rider job: increment failure write failed",
					"subscription_id", s.ID, "error", ierr)
			}
			payload := events.SubscriptionRenewalFailedPayload{
				SubscriptionID: s.ID.String(),
				PartnerID:      s.PartnerID.String(),
				PlanID:         s.PlanID.String(),
				AmountPaise:    amountPaise,
				FailureCount:   newCount,
				AutoRenewOff:   autoDisabled,
				Reason:         derr.Error(),
				OccurredAt:     time.Now().UTC(),
			}
			if pub != nil {
				if perr := pub.PublishSubscriptionRenewalFailed(ctx, payload); perr != nil {
					slog.Warn("rider job: renewal-failed publish failed",
						"subscription_id", s.ID, "error", perr)
				}
			}
			processed++
			continue
		}
		newExpiry, rerr := st.RenewSubscription(ctx, s.ID)
		if rerr != nil {
			slog.Error("rider job: renew db write failed AFTER successful debit — manual reconciliation required",
				"subscription_id", s.ID, "wallet_txn_id", debit.TransactionID, "error", rerr)
			// Mark attempted to avoid spinning.
			_ = st.MarkRenewalAttempted(ctx, s.ID)
			continue
		}
		txnIDStr := uuid.UUID{}.String()
		if debit != nil && debit.TransactionID != uuid.Nil {
			txnIDStr = debit.TransactionID.String()
		}
		payload := events.SubscriptionRenewedPayload{
			SubscriptionID: s.ID.String(),
			PartnerID:      s.PartnerID.String(),
			PlanID:         s.PlanID.String(),
			AmountPaise:    amountPaise,
			NewExpiresAt:   newExpiry,
			WalletTxnID:    txnIDStr,
			OccurredAt:     time.Now().UTC(),
		}
		if pub != nil {
			if perr := pub.PublishSubscriptionRenewed(ctx, payload); perr != nil {
				slog.Warn("rider job: renewed publish failed",
					"subscription_id", s.ID, "error", perr)
			}
		}
		processed++
	}
	return processed, nil
}

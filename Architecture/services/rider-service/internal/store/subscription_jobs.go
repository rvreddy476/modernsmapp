// Subscription job helpers — Sprint 4. Backs the subscription-expiry,
// grace-period transition, and wallet auto-renewal jobs.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §15. The store owns the SQL; the
// service.RunSubscription* funcs orchestrate the per-row reminder dedupe
// and wallet calls.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListExpiringSubscriptions returns active subscriptions whose expires_at
// falls within the lookback window. The cron job emits a "subscription
// expiring" event per row; the dedupe per-bucket lives in
// rider_doc_reminders_sent (we re-use the bucket dedupe table).
//
// We deliberately don't include grace_period rows here — they get a
// dedicated EventRiderSubscriptionGracePeriod when they're flipped.
func (s *Store) ListExpiringSubscriptions(ctx context.Context, within time.Duration) ([]PartnerSubscription, error) {
	if within <= 0 {
		within = 24 * time.Hour
	}
	const q = `
        SELECT id, partner_id, plan_id, status, starts_at, expires_at, grace_ends_at,
               leads_used, fair_use_used, auto_renew, cancelled_at, created_at, updated_at
        FROM rider_partner_subscriptions
        WHERE status = 'active'
          AND expires_at <= NOW() + ($1::int * INTERVAL '1 second')
          AND expires_at > NOW()
        ORDER BY expires_at ASC
        LIMIT 1000`
	rows, err := s.db.Query(ctx, q, int(within.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("list expiring subscriptions: %w", err)
	}
	defer rows.Close()
	var out []PartnerSubscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	return out, rows.Err()
}

// FlipToGracePeriod moves rows that have just hit their expires_at from
// 'active' (or 'trial') to 'grace_period'. Sets grace_ends_at = expires_at +
// plan.grace_period_days. Returns the rows that were flipped (so the job
// can emit per-row events).
//
// The UPDATE ... RETURNING shape makes this idempotent: a re-run a minute
// later finds zero rows and is a no-op.
func (s *Store) FlipToGracePeriod(ctx context.Context) ([]PartnerSubscription, error) {
	const q = `
        UPDATE rider_partner_subscriptions s
        SET status        = 'grace_period',
            grace_ends_at = s.expires_at + (p.grace_period_days * INTERVAL '1 day'),
            updated_at    = NOW()
        FROM rider_subscription_plans p
        WHERE s.plan_id = p.id
          AND s.status IN ('trial','active')
          AND s.expires_at <= NOW()
        RETURNING s.id, s.partner_id, s.plan_id, s.status, s.starts_at, s.expires_at,
                  s.grace_ends_at, s.leads_used, s.fair_use_used, s.auto_renew,
                  s.cancelled_at, s.created_at, s.updated_at`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("flip to grace period: %w", err)
	}
	defer rows.Close()
	var out []PartnerSubscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	return out, rows.Err()
}

// FlipToExpired moves grace_period rows whose grace_ends_at has passed to
// 'expired'. Returns the rows flipped.
func (s *Store) FlipToExpired(ctx context.Context) ([]PartnerSubscription, error) {
	const q = `
        UPDATE rider_partner_subscriptions
        SET status     = 'expired',
            updated_at = NOW()
        WHERE status = 'grace_period'
          AND grace_ends_at IS NOT NULL
          AND grace_ends_at <= NOW()
        RETURNING id, partner_id, plan_id, status, starts_at, expires_at, grace_ends_at,
                  leads_used, fair_use_used, auto_renew, cancelled_at, created_at, updated_at`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("flip to expired: %w", err)
	}
	defer rows.Close()
	var out []PartnerSubscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	return out, rows.Err()
}

// ListAutoRenewCandidates returns active/grace_period subscriptions where
// auto_renew=true and expires_at is within the lookahead window. We also
// guard against re-attempting too quickly — renewal_attempted_at must be
// older than the cooldown OR be null.
func (s *Store) ListAutoRenewCandidates(ctx context.Context, within, cooldown time.Duration) ([]PartnerSubscription, error) {
	if within <= 0 {
		within = 12 * time.Hour
	}
	if cooldown <= 0 {
		cooldown = 1 * time.Hour
	}
	const q = `
        SELECT id, partner_id, plan_id, status, starts_at, expires_at, grace_ends_at,
               leads_used, fair_use_used, auto_renew, cancelled_at, created_at, updated_at
        FROM rider_partner_subscriptions
        WHERE auto_renew = TRUE
          AND status IN ('active','grace_period')
          AND expires_at <= NOW() + ($1::int * INTERVAL '1 second')
          AND (renewal_attempted_at IS NULL OR
               renewal_attempted_at <= NOW() - ($2::int * INTERVAL '1 second'))
        ORDER BY expires_at ASC
        LIMIT 500`
	rows, err := s.db.Query(ctx, q, int(within.Seconds()), int(cooldown.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("list auto-renew candidates: %w", err)
	}
	defer rows.Close()
	var out []PartnerSubscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	return out, rows.Err()
}

// MarkRenewalAttempted stamps renewal_attempted_at = NOW() so the cooldown
// guard works on the next pass.
func (s *Store) MarkRenewalAttempted(ctx context.Context, subscriptionID uuid.UUID) error {
	const q = `
        UPDATE rider_partner_subscriptions
        SET renewal_attempted_at = NOW(),
            updated_at           = NOW()
        WHERE id = $1`
	tag, err := s.db.Exec(ctx, q, subscriptionID)
	if err != nil {
		return fmt.Errorf("mark renewal attempted: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

// RenewSubscription extends the subscription's expires_at by the plan's
// billing_period_days, sets status='active', resets grace_ends_at,
// renewal_failure_count, and stamps renewal_attempted_at = NOW().
// Returns the new expires_at.
func (s *Store) RenewSubscription(ctx context.Context, subscriptionID uuid.UUID) (time.Time, error) {
	const q = `
        UPDATE rider_partner_subscriptions s
        SET status                = 'active',
            expires_at            = (CASE WHEN s.expires_at < NOW() THEN NOW() ELSE s.expires_at END) +
                                     (p.billing_period_days * INTERVAL '1 day'),
            grace_ends_at         = NULL,
            renewal_failure_count = 0,
            renewal_attempted_at  = NOW(),
            updated_at            = NOW()
        FROM rider_subscription_plans p
        WHERE s.plan_id = p.id AND s.id = $1
        RETURNING s.expires_at`
	var newExpiry time.Time
	row := s.db.QueryRow(ctx, q, subscriptionID)
	if err := row.Scan(&newExpiry); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, ErrSubscriptionNotFound
		}
		return time.Time{}, fmt.Errorf("renew subscription: %w", err)
	}
	return newExpiry, nil
}

// IncrementRenewalFailure bumps renewal_failure_count + sets
// renewal_attempted_at = NOW(). When the count reaches autoDisableAt
// (typically 3) it also flips auto_renew=false. Returns the new failure
// count and a flag indicating whether auto_renew was disabled by this call.
func (s *Store) IncrementRenewalFailure(ctx context.Context, subscriptionID uuid.UUID, autoDisableAt int) (int, bool, error) {
	if autoDisableAt <= 0 {
		autoDisableAt = 3
	}
	const q = `
        UPDATE rider_partner_subscriptions
        SET renewal_failure_count = renewal_failure_count + 1,
            renewal_attempted_at  = NOW(),
            auto_renew            = CASE WHEN renewal_failure_count + 1 >= $2 THEN FALSE ELSE auto_renew END,
            updated_at            = NOW()
        WHERE id = $1
        RETURNING renewal_failure_count, NOT auto_renew`
	var newCount int
	var disabledNow bool
	row := s.db.QueryRow(ctx, q, subscriptionID, autoDisableAt)
	if err := row.Scan(&newCount, &disabledNow); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, ErrSubscriptionNotFound
		}
		return 0, false, fmt.Errorf("increment renewal failure: %w", err)
	}
	return newCount, disabledNow && newCount >= autoDisableAt, nil
}

// RidesStuckFilter is the input for the multi-bucket stuck-ride scanner
// used by RunStaleRideCleanup.
type RidesStuckFilter struct {
	Status    string
	OlderThan time.Duration
	Limit     int
}

// ListStuckRides returns rides whose status has been the input value for
// longer than OlderThan. Uses the latest rider_ride_status_history row's
// created_at as the baseline so a ride that bounced through statuses
// recently isn't flagged.
func (s *Store) ListStuckRides(ctx context.Context, f RidesStuckFilter) ([]Ride, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 200
	}
	const q = `
        SELECT id, customer_user_id, partner_id, vehicle_id, city_id, vehicle_type, status,
               pickup_address, ST_Y(pickup_location::geometry), ST_X(pickup_location::geometry),
               drop_address, ST_Y(drop_location::geometry), ST_X(drop_location::geometry),
               estimated_distance_km, estimated_duration_min, estimated_fare, payment_method,
               otp_expires_at, requested_at, created_at, updated_at
        FROM rider_rides
        WHERE status = $1::rider_ride_status
          AND updated_at <= NOW() - ($2::int * INTERVAL '1 second')
        ORDER BY updated_at ASC
        LIMIT $3`
	rows, err := s.db.Query(ctx, q, f.Status, int(f.OlderThan.Seconds()), f.Limit)
	if err != nil {
		return nil, fmt.Errorf("list stuck rides: %w", err)
	}
	defer rows.Close()
	var out []Ride
	for rows.Next() {
		r, err := scanRide(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

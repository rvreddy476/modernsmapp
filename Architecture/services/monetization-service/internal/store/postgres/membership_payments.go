package postgres

// Paid creator memberships on payments-service (migration 025).
//
// One membership_payments row is one paid period. Creating it never moves
// money: the service opens a payments intent for it, and the period is
// applied ONLY by ApplyMembershipPaymentEvent, from the signed payments event,
// in one transaction with the inbox row (paymentevents.ApplyOnce). The rules
// are internal/membership's Decide; this file locks the rows, asks it, and
// writes what it says.
//
// Lock order everywhere: the subscription row, then its payment rows.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrSubscriptionNotFound / ErrSubscriptionNotOwned: the subscription
	// does not exist, or is someone else's.
	ErrSubscriptionNotFound = errors.New("SUBSCRIPTION_NOT_FOUND")
	ErrSubscriptionNotOwned = errors.New("SUBSCRIPTION_NOT_OWNED")
	// ErrNotRenewable: the subscription cannot be renewed in its state.
	ErrNotRenewable = errors.New("SUBSCRIPTION_NOT_RENEWABLE")
	// ErrRenewalAlreadyPaid: this period's renewal is already paid.
	ErrRenewalAlreadyPaid = errors.New("RENEWAL_ALREADY_PAID")
	// ErrTierInactive: the tier the next period would be on is retired.
	ErrTierInactive = errors.New("TIER_INACTIVE")
	// ErrMembershipPaymentNotFound: no such membership payment.
	ErrMembershipPaymentNotFound = errors.New("MEMBERSHIP_PAYMENT_NOT_FOUND")
	// ErrIntentMismatch: the row is already bound to another intent.
	ErrIntentMismatch = errors.New("MEMBERSHIP_INTENT_MISMATCH")
	// ErrRefundNotAllowed: the payment is not a paid, applied one, or a
	// refund is already in flight or done.
	ErrRefundNotAllowed = errors.New("MEMBERSHIP_REFUND_NOT_ALLOWED")
)

const idxOnePendingJoin = "idx_subscriptions_one_pending"

// MembershipCheckout is a subscription and the payment row a checkout is for.
// Replayed: both already existed (an idempotent retry).
type MembershipCheckout struct {
	Sub      Subscription
	Payment  membership.Payment
	Replayed bool
}

// ToMembershipSub converts a subscription row to the decision table's view.
func ToMembershipSub(s *Subscription) membership.Sub {
	return membership.Sub{
		ID: s.ID, SubscriberID: s.SubscriberID, CreatorID: s.CreatorID, TierID: s.TierID, TierName: s.TierName,
		PricePaise: s.PricePaise, Currency: s.Currency, Status: s.Status,
		CurrentPeriodStart: s.CurrentPeriodStart, CurrentPeriodEnd: s.CurrentPeriodEnd,
		GracePeriodEnd: s.GracePeriodEnd, PendingTierID: s.PendingTierID, PendingPricePaise: s.PendingPricePaise,
		Counted: s.Counted, ActivatedAt: s.ActivatedAt, CreatedAt: s.CreatedAt,
	}
}

const paymentColumns = `id, subscription_id, payer_id, creator_id, kind, tier_id, tier_name, tier_price_paise,
	from_tier_id, from_tier_name, from_price_paise, amount_paise, fee_paise, creator_share_paise, currency,
	period_start, period_end, granted_start, granted_end, COALESCE(intent_id::text, ''), status, late_capture,
	COALESCE(refund_status, ''), COALESCE(refund_key, ''), COALESCE(refund_cause, ''), refund_paise, refunded_paise,
	COALESCE(refund_last_error, ''), created_at`

func scanPayment(row pgx.Row) (*membership.Payment, error) {
	var p membership.Payment
	if err := row.Scan(&p.ID, &p.SubscriptionID, &p.PayerID, &p.CreatorID, &p.Kind, &p.TierID, &p.TierName, &p.TierPricePaise,
		&p.FromTierID, &p.FromTierName, &p.FromPricePaise, &p.AmountPaise, &p.FeePaise, &p.CreatorSharePaise, &p.Currency,
		&p.PeriodStart, &p.PeriodEnd, &p.GrantedStart, &p.GrantedEnd, &p.IntentID, &p.Status, &p.LateCapture,
		&p.RefundStatus, &p.RefundKey, &p.RefundCause, &p.RefundPaise, &p.RefundedPaise, &p.RefundError, &p.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

func lockPairTx(ctx context.Context, tx pgx.Tx, subscriberID, creatorID uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`,
		"membership_pair:"+subscriberID.String()+":"+creatorID.String())
	return err
}

func insertPaymentTx(ctx context.Context, tx pgx.Tx, p *membership.Payment) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	if p.Currency == "" {
		p.Currency = membership.Currency
	}
	p.FeePaise, p.CreatorSharePaise = membership.Split(p.AmountPaise)
	p.Status = membership.PayCreated
	_, err := tx.Exec(ctx, `
		INSERT INTO membership_payments (id, subscription_id, payer_id, creator_id, kind, tier_id, tier_name, tier_price_paise,
			from_tier_id, from_tier_name, from_price_paise, amount_paise, fee_paise, creator_share_paise, currency,
			period_start, period_end, status, idempotency_key, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, 'created', $18, $19, $19)
	`, p.ID, p.SubscriptionID, p.PayerID, p.CreatorID, p.Kind, p.TierID, p.TierName, p.TierPricePaise,
		p.FromTierID, p.FromTierName, p.FromPricePaise, p.AmountPaise, p.FeePaise, p.CreatorSharePaise, p.Currency,
		p.PeriodStart, p.PeriodEnd, membership.IntentKey(p.ID), p.CreatedAt)
	return err
}

func reopenFailedTx(ctx context.Context, tx pgx.Tx, p *membership.Payment) error {
	if p.Status != membership.PayFailed {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE membership_payments SET status = 'created', failure_reason = NULL, failed_at = NULL, updated_at = NOW()
		WHERE id = $1 AND status = 'failed'
	`, p.ID); err != nil {
		return err
	}
	p.Status = membership.PayCreated
	return nil
}

func latestJoinPaymentTx(ctx context.Context, tx pgx.Tx, subID uuid.UUID) (*membership.Payment, error) {
	return scanPayment(tx.QueryRow(ctx, `SELECT `+paymentColumns+` FROM membership_payments
		WHERE subscription_id = $1 AND kind = 'join' ORDER BY created_at DESC LIMIT 1`, subID))
}

// ---------------------------------------------------------------------------
// Checkouts: rows created, nothing charged
// ---------------------------------------------------------------------------

// JoinInput is a first join.
type JoinInput struct {
	SubscriberID uuid.UUID
	CreatorID    uuid.UUID
	Tier         CreatorTier
	// ClientKey is the client's Idempotency-Key; may be empty.
	ClientKey string
	Now       time.Time
}

// CreateMembershipJoin creates a pending_payment subscription and its join
// payment row, serialised per (subscriber, creator):
//
//   - a client key already used by this subscriber replays its checkout
//     (another creator or tier under the key is ErrIdempotencyKeyTaken);
//   - a live subscription is ErrAlreadySubscribed;
//   - a pending join for the same tier younger than PendingTTL is replayed;
//     any other pending join (stale, or another tier) is replaced: set
//     expired, its open payment rows failed. If one of those is captured
//     later it is a late capture and refunded.
func (s *Store) CreateMembershipJoin(ctx context.Context, in JoinInput) (*MembershipCheckout, error) {
	out, err := s.createMembershipJoinTx(ctx, in)
	switch uniqueViolation(err) {
	case "":
		return out, err
	case idxOneLiveSubscription:
		return nil, ErrAlreadySubscribed
	case idxSubscriberIdempotencyKey, legacyIdxSubscriptionIdemKey:
		return nil, ErrIdempotencyKeyTaken
	case idxOnePendingJoin:
		return nil, ErrSubscriptionStateChanged
	default:
		return nil, err
	}
}

func (s *Store) createMembershipJoinTx(ctx context.Context, in JoinInput) (*MembershipCheckout, error) {
	now := membership.Truncate(in.Now)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := lockPairTx(ctx, tx, in.SubscriberID, in.CreatorID); err != nil {
		return nil, err
	}

	replay := func(sub *Subscription) (*MembershipCheckout, error) {
		pay, err := latestJoinPaymentTx(ctx, tx, sub.ID)
		if err != nil {
			return nil, fmt.Errorf("join payment of %s: %w", sub.ID, err)
		}
		// Only a join still waiting may be retried; an expired one stays failed.
		if sub.Status == membership.StatusPendingPayment {
			if err := reopenFailedTx(ctx, tx, pay); err != nil {
				return nil, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &MembershipCheckout{Sub: *sub, Payment: *pay, Replayed: true}, nil
	}

	if in.ClientKey != "" {
		sub, err := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions
			WHERE subscriber_id = $1 AND idempotency_key = $2`, in.SubscriberID, in.ClientKey))
		switch {
		case err == nil:
			if sub.CreatorID != in.CreatorID || sub.TierID != in.Tier.ID {
				return nil, ErrIdempotencyKeyTaken
			}
			return replay(sub)
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, err
		}
	}

	var live bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM subscriptions
		WHERE subscriber_id = $1 AND creator_id = $2 AND status = ANY($3))`,
		in.SubscriberID, in.CreatorID, LiveSubscriptionStatuses).Scan(&live); err != nil {
		return nil, err
	}
	if live {
		return nil, ErrAlreadySubscribed
	}

	pending, err := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions
		WHERE subscriber_id = $1 AND creator_id = $2 AND status = 'pending_payment' FOR UPDATE`, in.SubscriberID, in.CreatorID))
	switch {
	case err == nil:
		if pending.TierID == in.Tier.ID && pending.CreatedAt.After(now.Add(-membership.PendingTTL)) {
			return replay(pending)
		}
		if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status = 'expired' WHERE id = $1`, pending.ID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE membership_payments SET status = 'failed', failure_reason = 'replaced',
			failed_at = $2, updated_at = $2 WHERE subscription_id = $1 AND status IN ('created', 'confirming')`, pending.ID, now); err != nil {
			return nil, err
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}

	end := membership.Truncate(membership.PeriodEnd(now))
	sub := Subscription{
		ID: uuid.New(), SubscriberID: in.SubscriberID, CreatorID: in.CreatorID, TierID: in.Tier.ID, TierName: in.Tier.Name,
		PricePaise: in.Tier.PricePaise, Currency: in.Tier.Currency, Status: membership.StatusPendingPayment,
		CurrentPeriodStart: now, CurrentPeriodEnd: end, CreatedAt: now, IdempotencyKey: in.ClientKey,
	}
	if sub.Currency == "" {
		sub.Currency = membership.Currency
	}
	var iKey *string
	if in.ClientKey != "" {
		iKey = &in.ClientKey
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO subscriptions (id, subscriber_id, creator_id, tier_id, tier_name, price, currency, status,
			current_period_start, current_period_end, created_at, idempotency_key, counted)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending_payment', $8, $9, $8, $10, FALSE)
	`, sub.ID, sub.SubscriberID, sub.CreatorID, sub.TierID, sub.TierName, sub.PricePaise, sub.Currency, now, end, iKey); err != nil {
		return nil, err
	}
	pay := membership.Payment{
		SubscriptionID: sub.ID, PayerID: in.SubscriberID, CreatorID: in.CreatorID, Kind: membership.KindJoin,
		TierID: in.Tier.ID, TierName: in.Tier.Name, TierPricePaise: in.Tier.PricePaise,
		AmountPaise: in.Tier.PricePaise, Currency: sub.Currency, PeriodStart: now, PeriodEnd: end, CreatedAt: now,
	}
	if err := insertPaymentTx(ctx, tx, &pay); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &MembershipCheckout{Sub: sub, Payment: pay}, nil
}

// RenewalInput asks for the next period of one subscription.
type RenewalInput struct {
	SubscriptionID uuid.UUID
	SubscriberID   uuid.UUID
	Now            time.Time
}

// CreateMembershipRenewal opens (or replays) the renewal payment for the
// subscription's current period end. The next period is on the scheduled
// downgrade's tier and price when one is set, else on the current tier at
// the subscription's locked price; that tier must still be active. One row
// per period: a failed one is reopened, a paid one is ErrRenewalAlreadyPaid.
func (s *Store) CreateMembershipRenewal(ctx context.Context, in RenewalInput) (*MembershipCheckout, error) {
	now := membership.Truncate(in.Now)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	sub, err := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE id = $1 FOR UPDATE`, in.SubscriptionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	if sub.SubscriberID != in.SubscriberID {
		return nil, ErrSubscriptionNotOwned
	}
	if !membership.CanRenew(sub.Status, sub.ActivatedAt != nil, sub.CurrentPeriodEnd, now) {
		return nil, ErrNotRenewable
	}
	if sub.Status == membership.StatusExpired {
		var other bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM subscriptions
			WHERE subscriber_id = $1 AND creator_id = $2 AND id <> $3 AND status = ANY($4))`,
			sub.SubscriberID, sub.CreatorID, sub.ID, LiveSubscriptionStatuses).Scan(&other); err != nil {
			return nil, err
		}
		if other {
			return nil, ErrAlreadySubscribed
		}
	}
	tierID, price := sub.TierID, sub.PricePaise
	if sub.PendingTierID != nil && sub.PendingPricePaise != nil {
		tierID, price = *sub.PendingTierID, *sub.PendingPricePaise
	}
	var (
		tierName string
		active   bool
	)
	err = tx.QueryRow(ctx, `SELECT name, is_active FROM creator_tiers WHERE id = $1`, tierID).Scan(&tierName, &active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return nil, ErrTierInactive
	}
	if err != nil {
		return nil, err
	}

	base := sub.CurrentPeriodEnd
	existing, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentColumns+` FROM membership_payments
		WHERE subscription_id = $1 AND kind = 'renewal' AND period_start = $2 AND status <> 'refunded' AND NOT late_capture
		FOR UPDATE`, sub.ID, base))
	switch {
	case err == nil:
		if existing.Status == membership.PayPaid {
			return nil, ErrRenewalAlreadyPaid
		}
		if err := reopenFailedTx(ctx, tx, existing); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &MembershipCheckout{Sub: *sub, Payment: *existing, Replayed: true}, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	pay := membership.Payment{
		SubscriptionID: sub.ID, PayerID: sub.SubscriberID, CreatorID: sub.CreatorID, Kind: membership.KindRenewal,
		TierID: tierID, TierName: tierName, TierPricePaise: price, AmountPaise: price, Currency: sub.Currency,
		PeriodStart: base, PeriodEnd: membership.Truncate(membership.PeriodEnd(membership.RenewalStart(base, now))), CreatedAt: now,
	}
	if err := insertPaymentTx(ctx, tx, &pay); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &MembershipCheckout{Sub: *sub, Payment: pay}, nil
}

// UpgradeCheckoutInput is a paid move up: ChargePaise (> 0) is the pro-rata
// difference computed by the service against the row it read.
type UpgradeCheckoutInput struct {
	SubscriptionID    uuid.UUID
	SubscriberID      uuid.UUID
	ExpectedTierID    uuid.UUID
	ExpectedPeriodEnd time.Time
	NewTier           CreatorTier
	ChargePaise       int64
	Now               time.Time
}

// CreateMembershipUpgrade opens (or replays) the payment for an upgrade
// difference. The subscription must still be active on ExpectedTierID and
// end at ExpectedPeriodEnd (else ErrSubscriptionStateChanged). An open
// upgrade to the same tier younger than PendingTTL is replayed; any other
// open upgrade of this subscription is superseded (failed). The tier changes
// only when the payment's signed event is applied.
func (s *Store) CreateMembershipUpgrade(ctx context.Context, in UpgradeCheckoutInput) (*MembershipCheckout, error) {
	if in.ChargePaise <= 0 {
		return nil, fmt.Errorf("upgrade charge must be positive")
	}
	now := membership.Truncate(in.Now)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	sub, err := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE id = $1 FOR UPDATE`, in.SubscriptionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return nil, err
	}
	if sub.SubscriberID != in.SubscriberID {
		return nil, ErrSubscriptionNotOwned
	}
	if sub.Status != membership.StatusActive || sub.TierID != in.ExpectedTierID || !sub.CurrentPeriodEnd.Equal(in.ExpectedPeriodEnd) {
		return nil, ErrSubscriptionStateChanged
	}

	rows, err := tx.Query(ctx, `SELECT `+paymentColumns+` FROM membership_payments
		WHERE subscription_id = $1 AND kind = 'upgrade' AND status IN ('created', 'confirming', 'failed') AND NOT late_capture
		ORDER BY created_at DESC FOR UPDATE`, sub.ID)
	if err != nil {
		return nil, err
	}
	var open []membership.Payment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		open = append(open, *p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range open {
		p := &open[i]
		same := p.TierID == in.NewTier.ID && p.FromTierID != nil && *p.FromTierID == in.ExpectedTierID &&
			p.PeriodEnd.Equal(in.ExpectedPeriodEnd) && p.CreatedAt.After(now.Add(-membership.PendingTTL))
		if same {
			if err := reopenFailedTx(ctx, tx, p); err != nil {
				return nil, err
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &MembershipCheckout{Sub: *sub, Payment: *p, Replayed: true}, nil
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE membership_payments SET status = 'failed', failure_reason = 'superseded',
		failed_at = $2, updated_at = $2 WHERE subscription_id = $1 AND kind = 'upgrade' AND status IN ('created', 'confirming')`, sub.ID, now); err != nil {
		return nil, err
	}
	from := in.ExpectedTierID
	pay := membership.Payment{
		SubscriptionID: sub.ID, PayerID: sub.SubscriberID, CreatorID: sub.CreatorID, Kind: membership.KindUpgrade,
		TierID: in.NewTier.ID, TierName: in.NewTier.Name, TierPricePaise: in.NewTier.PricePaise,
		FromTierID: &from, FromTierName: sub.TierName, FromPricePaise: sub.PricePaise,
		AmountPaise: in.ChargePaise, Currency: sub.Currency, PeriodStart: now, PeriodEnd: sub.CurrentPeriodEnd, CreatedAt: now,
	}
	if err := insertPaymentTx(ctx, tx, &pay); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &MembershipCheckout{Sub: *sub, Payment: pay}, nil
}

// SetMembershipPaymentIntent binds the payments intent to its row. Binding
// the same intent again is a no-op; another intent is ErrIntentMismatch.
func (s *Store) SetMembershipPaymentIntent(ctx context.Context, paymentID, intentID uuid.UUID, providerOrderID string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE membership_payments SET intent_id = $2, provider_order_id = COALESCE(NULLIF($3, ''), provider_order_id), updated_at = NOW()
		WHERE id = $1 AND (intent_id IS NULL OR intent_id = $2)
	`, paymentID, intentID, providerOrderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrIntentMismatch
	}
	return nil
}

// MarkMembershipPaymentConfirming records an advisory verified callback:
// created → confirming. It never marks anything paid.
func (s *Store) MarkMembershipPaymentConfirming(ctx context.Context, paymentID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE membership_payments SET status = 'confirming', updated_at = NOW()
		WHERE id = $1 AND status = 'created'`, paymentID)
	return err
}

// GetMembershipPayment returns one payment row, or nil.
func (s *Store) GetMembershipPayment(ctx context.Context, paymentID uuid.UUID) (*membership.Payment, error) {
	p, err := scanPayment(s.db.QueryRow(ctx, `SELECT `+paymentColumns+` FROM membership_payments WHERE id = $1`, paymentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// ListSubscriberMemberships returns the subscriber's memberships: live,
// pending, and ended ones whose period ended after since (newest first).
func (s *Store) ListSubscriberMemberships(ctx context.Context, subscriberID uuid.UUID, since time.Time) ([]Subscription, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+subscriptionColumns+` FROM subscriptions
		WHERE subscriber_id = $1
		  AND (status = ANY($2) OR status = 'pending_payment' OR (status = 'expired' AND activated_at IS NOT NULL AND current_period_end > $3))
		ORDER BY current_period_end DESC
		LIMIT 200
	`, subscriberID, LiveSubscriptionStatuses, since)
	if err != nil {
		return nil, err
	}
	return scanSubscriptions(rows)
}

// ---------------------------------------------------------------------------
// The signed payment event
// ---------------------------------------------------------------------------

// membershipInbox is membership_payment_inbox, the dedupe half of ApplyOnce.
type membershipInbox struct{}

func (membershipInbox) Claim(ctx context.Context, tx pgx.Tx, c paymentevents.Claim) (bool, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO membership_payment_inbox (event_id, event_type, intent_id, payment_id, amount_minor, currency)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''))
		ON CONFLICT (event_id) DO NOTHING`,
		c.EventID, c.EventType, c.IntentID, c.ReferenceID, c.AmountMinor, c.Currency)
	if err != nil {
		return false, fmt.Errorf("record payment event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ApplyMembershipPaymentEvent applies one payments event exactly once: the
// inbox row, the decision and every write it implies commit together or not
// at all. A duplicate event id is OutcomeDuplicate with nothing written; an
// unknown payment is recorded as OutcomePaymentNotFound and committed.
func (s *Store) ApplyMembershipPaymentEvent(ctx context.Context, ev membership.Event, now time.Time) (membership.Applied, error) {
	var applied membership.Applied
	if strings.TrimSpace(ev.EventID) == "" {
		return applied, paymentevents.ErrNoEventID
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return applied, err
	}
	defer tx.Rollback(ctx)

	claim := paymentevents.Claim{EventID: ev.EventID, EventType: ev.EventType, IntentID: ev.IntentID,
		ReferenceID: ev.PaymentID, AmountMinor: ev.AmountMinor, Currency: ev.Currency}
	err = paymentevents.ApplyOnce(ctx, tx, membershipInbox{}, claim, func(ctx context.Context, tx pgx.Tx) error {
		return applyMembershipEffectTx(ctx, tx, ev, now, &applied)
	})
	switch {
	case errors.Is(err, paymentevents.ErrDuplicate):
		applied.Decision = membership.Decision{Outcome: membership.OutcomeDuplicate}
		return applied, nil
	case err != nil:
		return applied, err
	}
	return applied, tx.Commit(ctx)
}

func recordInboxOutcomeTx(ctx context.Context, tx pgx.Tx, eventID string, d membership.Decision) error {
	_, err := tx.Exec(ctx, `UPDATE membership_payment_inbox SET outcome = $2, detail = NULLIF($3, '') WHERE event_id = $1`,
		eventID, string(d.Outcome), d.Detail)
	return err
}

func applyMembershipEffectTx(ctx context.Context, tx pgx.Tx, ev membership.Event, now time.Time, applied *membership.Applied) error {
	now = membership.Truncate(now)
	// The subscription first (the lock order every path uses), then the row.
	var subID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT subscription_id FROM membership_payments WHERE id = $1`, ev.PaymentID).Scan(&subID)
	if errors.Is(err, pgx.ErrNoRows) {
		applied.Decision = membership.Decision{Outcome: membership.OutcomePaymentNotFound, Detail: "no such membership payment"}
		return recordInboxOutcomeTx(ctx, tx, ev.EventID, applied.Decision)
	}
	if err != nil {
		return err
	}
	sub, err := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE id = $1 FOR UPDATE`, subID))
	if err != nil {
		return fmt.Errorf("lock subscription %s: %w", subID, err)
	}
	pay, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentColumns+` FROM membership_payments WHERE id = $1 FOR UPDATE`, ev.PaymentID))
	if err != nil {
		return fmt.Errorf("lock membership payment %s: %w", ev.PaymentID, err)
	}
	var otherLive bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM subscriptions
		WHERE subscriber_id = $1 AND creator_id = $2 AND id <> $3 AND status = ANY($4))`,
		sub.SubscriberID, sub.CreatorID, sub.ID, LiveSubscriptionStatuses).Scan(&otherLive); err != nil {
		return err
	}
	msub := ToMembershipSub(sub)
	d := membership.Decide(*pay, msub, otherLive, ev, now)
	applied.Decision, applied.Payment, applied.Sub = d, *pay, msub

	if d.Payment != nil {
		if err := writePaymentTx(ctx, tx, d.Payment, now); err != nil {
			return err
		}
		applied.Payment = *d.Payment
	}
	if d.Sub != nil {
		if err := writeSubTx(ctx, tx, d.Sub); err != nil {
			return err
		}
		applied.Sub = *d.Sub
	}
	for _, td := range d.Tiers {
		if _, err := tx.Exec(ctx, `UPDATE creator_tiers SET subscriber_count = GREATEST(subscriber_count + $2, 0), updated_at = NOW() WHERE id = $1`,
			td.TierID, td.Delta); err != nil {
			return err
		}
	}
	if d.CreditCreatorPaise > 0 {
		desc := fmt.Sprintf("Membership %s: %s", pay.Kind, pay.TierName)
		if err := creditMembershipEarningTx(ctx, tx, pay.CreatorID, pay.ID, d.CreditCreatorPaise, pay.Currency, desc, now); err != nil {
			return err
		}
	}
	if d.ReverseCreatorPaise > 0 || d.ReverseEarningRow {
		if err := reverseMembershipEarningTx(ctx, tx, pay.CreatorID, pay.ID, d.ReverseCreatorPaise, pay.Currency, d.ReverseEarningRow, now); err != nil {
			return err
		}
	}
	if d.Outcome != membership.OutcomeMismatch {
		meta, _ := json.Marshal(map[string]any{"payment_id": pay.ID.String(), "kind": pay.Kind, "event_id": ev.EventID, "detail": d.Detail})
		if _, err := tx.Exec(ctx, `INSERT INTO subscription_events (id, subscription_id, event_type, old_status, new_status, metadata, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, uuid.New(), sub.ID, "payment_"+string(d.Outcome), sub.Status, applied.Sub.Status, meta, now); err != nil {
			return err
		}
	}
	return recordInboxOutcomeTx(ctx, tx, ev.EventID, d)
}

func writePaymentTx(ctx context.Context, tx pgx.Tx, p *membership.Payment, now time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE membership_payments SET
			status = $2,
			late_capture = $3,
			refund_next_attempt_at = CASE WHEN $4 = 'requested' AND refund_status IS DISTINCT FROM 'requested' THEN $12 ELSE refund_next_attempt_at END,
			refund_status = NULLIF($4, ''),
			refund_key = NULLIF($5, ''),
			refund_cause = NULLIF($6, ''),
			refund_paise = $7,
			refunded_paise = $8,
			refund_last_error = NULLIF($9, ''),
			granted_start = $10,
			granted_end = $11,
			paid_at = CASE WHEN $2 = 'paid' AND paid_at IS NULL THEN $12 ELSE paid_at END,
			failed_at = CASE WHEN $2 = 'failed' THEN $12 ELSE failed_at END,
			refunded_at = CASE WHEN $2 = 'refunded' THEN $12 ELSE refunded_at END,
			updated_at = $12
		WHERE id = $1
	`, p.ID, p.Status, p.LateCapture, p.RefundStatus, p.RefundKey, p.RefundCause, p.RefundPaise, p.RefundedPaise,
		p.RefundError, p.GrantedStart, p.GrantedEnd, now)
	return err
}

func writeSubTx(ctx context.Context, tx pgx.Tx, s *membership.Sub) error {
	_, err := tx.Exec(ctx, `
		UPDATE subscriptions SET
			status = $2, current_period_start = $3, current_period_end = $4,
			tier_id = $5, tier_name = $6, price = $7, grace_period_end = $8,
			pending_tier_id = $9, pending_price = $10, counted = $11, activated_at = $12,
			retry_count = 0, payment_failed_at = NULL,
			cancelled_at = CASE WHEN $2 = 'active' THEN NULL ELSE cancelled_at END,
			cancellation_reason = CASE WHEN $2 = 'active' THEN NULL ELSE cancellation_reason END
		WHERE id = $1
	`, s.ID, s.Status, s.CurrentPeriodStart, s.CurrentPeriodEnd, s.TierID, s.TierName, s.PricePaise,
		s.GracePeriodEnd, s.PendingTierID, s.PendingPricePaise, s.Counted, s.ActivatedAt)
	return err
}

// creditMembershipEarningTx records the creator's share as earnings on the
// creator ledger: balance and lifetime earnings, plus a type='earning',
// reference_type='subscription' transaction (what the period statement
// counts as subscription revenue), reference_id = the payment. No debit:
// the money came through payments-service. Payouts stay off; the balance is
// what the platform owes.
func creditMembershipEarningTx(ctx context.Context, tx pgx.Tx, creatorID, paymentID uuid.UUID, amount int64, currency, desc string, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO creator_ledger (user_id, balance, lifetime_earnings, pending_payout, currency, is_frozen, created_at, updated_at)
		VALUES ($1, 0, 0, 0, 'INR', false, $2, $2) ON CONFLICT (user_id) DO NOTHING
	`, creatorID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE creator_ledger SET balance = balance + $2, lifetime_earnings = lifetime_earnings + $2, updated_at = $3
		WHERE user_id = $1
	`, creatorID, amount, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO transactions (id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at)
		VALUES ($1, $2, 'earning', $3, $4, 'completed', 'subscription', $5, $6, $7)
	`, uuid.New(), creatorID, amount, currency, paymentID.String(), desc, now)
	return err
}

// reverseMembershipEarningTx takes a refund's creator share back out of
// earnings (a type='refund' transaction) and, for a full refund, marks the
// payment's earning row reversed so the period statement drops it.
func reverseMembershipEarningTx(ctx context.Context, tx pgx.Tx, creatorID, paymentID uuid.UUID, amount int64, currency string, markRow bool, now time.Time) error {
	if amount > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE creator_ledger SET balance = balance - $2, lifetime_earnings = GREATEST(lifetime_earnings - $2, 0), updated_at = $3
			WHERE user_id = $1
		`, creatorID, amount, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO transactions (id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at)
			VALUES ($1, $2, 'refund', $3, $4, 'completed', 'subscription_refund', $5, 'Membership refund', $6)
		`, uuid.New(), creatorID, amount, currency, paymentID.String(), now); err != nil {
			return err
		}
	}
	if markRow {
		if _, err := tx.Exec(ctx, `
			UPDATE transactions SET status = 'reversed'
			WHERE wallet_id = $1 AND type = 'earning' AND reference_type = 'subscription' AND reference_id = $2 AND status = 'completed'
		`, creatorID, paymentID.String()); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Refunds
// ---------------------------------------------------------------------------

// SupportRefundInput is support's refund decision for one applied payment.
type SupportRefundInput struct {
	PaymentID   uuid.UUID
	RequestedBy uuid.UUID
	Reason      string
	Now         time.Time
}

// RequestMembershipSupportRefund queues a full refund (of what is not
// already refunded) under membership-refund:{paymentId}. Only a paid,
// applied payment with no refund in flight qualifies (ErrRefundNotAllowed);
// asking again while one is queued or pending returns it unchanged.
func (s *Store) RequestMembershipSupportRefund(ctx context.Context, in SupportRefundInput) (*membership.Payment, error) {
	now := membership.Truncate(in.Now)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	p, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentColumns+` FROM membership_payments WHERE id = $1 FOR UPDATE`, in.PaymentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMembershipPaymentNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.RefundCause == membership.CauseSupport && (p.RefundStatus == membership.RefundRequested || p.RefundStatus == membership.RefundPending) {
		return p, nil
	}
	if p.Status != membership.PayPaid || p.LateCapture || p.IntentID == "" ||
		(p.RefundStatus != "" && p.RefundStatus != membership.RefundFailed) {
		return nil, ErrRefundNotAllowed
	}
	amount := p.AmountPaise - p.RefundedPaise
	if amount <= 0 {
		return nil, ErrRefundNotAllowed
	}
	key := membership.SupportRefundKey(p.ID)
	if _, err := tx.Exec(ctx, `
		UPDATE membership_payments SET refund_status = 'requested', refund_cause = 'support', refund_key = $2,
			refund_paise = $3, refund_reason = $4, refund_requested_by = $5, refund_next_attempt_at = $6,
			refund_last_error = NULL, updated_at = $6
		WHERE id = $1
	`, p.ID, key, amount, in.Reason, in.RequestedBy, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	p.RefundStatus, p.RefundCause, p.RefundKey, p.RefundPaise, p.RefundError = membership.RefundRequested, membership.CauseSupport, key, amount, ""
	return p, nil
}

// MembershipRefundDue is one queued refund to file with payments.
type MembershipRefundDue struct {
	PaymentID   uuid.UUID
	IntentID    uuid.UUID
	Key         string
	AmountPaise int64
	Cause       string
	Reason      string
	Attempts    int
}

// DueMembershipRefunds lists queued refunds whose next attempt is due.
func (s *Store) DueMembershipRefunds(ctx context.Context, now time.Time, limit int) ([]MembershipRefundDue, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, intent_id, refund_key, refund_paise, COALESCE(refund_cause, ''), COALESCE(refund_reason, ''), refund_attempts
		FROM membership_payments
		WHERE refund_status = 'requested' AND intent_id IS NOT NULL AND refund_key IS NOT NULL
		  AND (refund_next_attempt_at IS NULL OR refund_next_attempt_at <= $1)
		ORDER BY refund_next_attempt_at NULLS FIRST
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MembershipRefundDue
	for rows.Next() {
		var r MembershipRefundDue
		if err := rows.Scan(&r.PaymentID, &r.IntentID, &r.Key, &r.AmountPaise, &r.Cause, &r.Reason, &r.Attempts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkMembershipRefundSubmitted: payments accepted the command (requested →
// pending). The money is back only on payment.refunded.
func (s *Store) MarkMembershipRefundSubmitted(ctx context.Context, paymentID, commandID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE membership_payments SET refund_status = 'pending', refund_command_id = $2,
		refund_attempts = refund_attempts + 1, refund_last_error = NULL, updated_at = NOW()
		WHERE id = $1 AND refund_status = 'requested'`, paymentID, commandID)
	return err
}

// MarkMembershipRefundRetry: payments could not be reached; try again at next.
func (s *Store) MarkMembershipRefundRetry(ctx context.Context, paymentID uuid.UUID, next time.Time, lastError string) error {
	_, err := s.db.Exec(ctx, `UPDATE membership_payments SET refund_attempts = refund_attempts + 1,
		refund_next_attempt_at = $2, refund_last_error = $3, updated_at = NOW()
		WHERE id = $1 AND refund_status = 'requested'`, paymentID, next, lastError)
	return err
}

// MarkMembershipRefundFailed: payments refused the refund (4xx). It needs a
// person; it is not retried.
func (s *Store) MarkMembershipRefundFailed(ctx context.Context, paymentID uuid.UUID, lastError string) error {
	_, err := s.db.Exec(ctx, `UPDATE membership_payments SET refund_status = 'failed', refund_attempts = refund_attempts + 1,
		refund_last_error = $2, updated_at = NOW()
		WHERE id = $1 AND refund_status = 'requested'`, paymentID, lastError)
	return err
}

// ---------------------------------------------------------------------------
// Workers
// ---------------------------------------------------------------------------

// SweepStalePendingJoins expires every pending_payment join created at or
// before cutoff and fails its open payment rows. A capture that arrives
// later for one of them is a late capture and is refunded.
func (s *Store) SweepStalePendingJoins(ctx context.Context, cutoff time.Time) ([]Subscription, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		UPDATE subscriptions SET status = 'expired'
		WHERE id IN (
			SELECT id FROM subscriptions
			WHERE status = 'pending_payment' AND created_at <= $1
			ORDER BY created_at LIMIT 500
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+subscriptionColumns, cutoff)
	if err != nil {
		return nil, err
	}
	subs, err := scanSubscriptions(rows)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(subs))
	for _, sub := range subs {
		ids = append(ids, sub.ID)
	}
	if len(ids) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE membership_payments SET status = 'failed', failure_reason = 'expired',
			failed_at = NOW(), updated_at = NOW()
			WHERE subscription_id = ANY($1) AND status IN ('created', 'confirming')`, ids); err != nil {
			return nil, err
		}
	}
	return subs, tx.Commit(ctx)
}

// StartGraceForEndedPeriods moves every active or past_due membership whose
// paid period has ended into grace until period end + GracePeriod. Nothing
// is charged: renewal is by reminder.
func (s *Store) StartGraceForEndedPeriods(ctx context.Context, now time.Time) ([]Subscription, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE subscriptions SET status = 'grace', grace_period_end = current_period_end + $2::interval
		WHERE id IN (
			SELECT id FROM subscriptions
			WHERE status IN ('active', 'past_due') AND current_period_end <= $1
			ORDER BY current_period_end LIMIT 500
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+subscriptionColumns, now, fmt.Sprintf("%d seconds", int64(membership.GracePeriod/time.Second)))
	if err != nil {
		return nil, err
	}
	return scanSubscriptions(rows)
}

// ExpiredMembership is one membership the expiry pass ended.
type ExpiredMembership struct {
	Subscription
	// From is the status it was in: grace, cancelled_at_period_end or paused.
	From string
	// WasCounted: it was counted in its tier's subscriber_count, and has now
	// left it.
	WasCounted bool
}

// ExpireEndedMemberships ends every membership whose access is over: grace
// past grace_period_end, cancelled_at_period_end and paused past their period
// end. Each leaves its tier's subscriber_count if it was counted, in the same
// transaction.
func (s *Store) ExpireEndedMemberships(ctx context.Context, now time.Time) ([]ExpiredMembership, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		WITH due AS (
			SELECT id, status AS from_status, counted AS was_counted FROM subscriptions
			WHERE (status = 'grace' AND COALESCE(grace_period_end, current_period_end) <= $1)
			   OR (status IN ('cancelled_at_period_end', 'paused') AND current_period_end <= $1)
			ORDER BY current_period_end LIMIT 500
			FOR UPDATE SKIP LOCKED
		)
		UPDATE subscriptions s SET status = 'expired', counted = FALSE, grace_period_end = NULL,
			pending_tier_id = NULL, pending_price = NULL
		FROM due WHERE s.id = due.id
		RETURNING s.id, s.subscriber_id, s.creator_id, s.tier_id, s.tier_name, s.price, s.currency,
			s.current_period_end, due.from_status, due.was_counted
	`, now)
	if err != nil {
		return nil, err
	}
	var out []ExpiredMembership
	for rows.Next() {
		var e ExpiredMembership
		if err := rows.Scan(&e.ID, &e.SubscriberID, &e.CreatorID, &e.TierID, &e.TierName, &e.PricePaise, &e.Currency,
			&e.CurrentPeriodEnd, &e.From, &e.WasCounted); err != nil {
			rows.Close()
			return nil, err
		}
		e.Status = membership.StatusExpired
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, e := range out {
		if !e.WasCounted {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE creator_tiers SET subscriber_count = GREATEST(subscriber_count - 1, 0), updated_at = NOW() WHERE id = $1`, e.TierID); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit(ctx)
}

// ReminderDue is one renewal_due reminder to send.
type ReminderDue struct {
	Subscription
	DaysLeft int
	// RenewalPricePaise is what the next period costs.
	RenewalPricePaise int64
}

// DueRenewalReminders lists the reminders not yet sent: 3 days before the
// period end (days_left 3), and from 24 h before it through grace (days_left
// 0, "ends today"). A membership scheduled to end (cancelled_at_period_end)
// is not reminded.
func (s *Store) DueRenewalReminders(ctx context.Context, now time.Time) ([]ReminderDue, error) {
	rows, err := s.db.Query(ctx, `
		WITH c AS (
			SELECT `+subscriptionColumns+`,
			       CASE WHEN current_period_end <= $1 + $2::interval THEN 0 ELSE 3 END AS days_left
			FROM subscriptions
			WHERE status IN ('active', 'past_due', 'grace') AND activated_at IS NOT NULL
			  AND current_period_end <= $1 + $3::interval
			  AND current_period_end > $1 - $3::interval
		)
		SELECT c.* FROM c
		WHERE NOT EXISTS (SELECT 1 FROM membership_renewal_reminders r
		                  WHERE r.subscription_id = c.id AND r.period_end = c.current_period_end AND r.days_left = c.days_left)
		ORDER BY c.current_period_end LIMIT 500
	`, now, fmt.Sprintf("%d seconds", int64(membership.ReminderDay/time.Second)),
		fmt.Sprintf("%d seconds", int64(membership.ReminderLead/time.Second)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReminderDue
	for rows.Next() {
		var (
			r    ReminderDue
			iKey *string
		)
		sub := &r.Subscription
		if err := rows.Scan(&sub.ID, &sub.SubscriberID, &sub.CreatorID, &sub.TierID, &sub.TierName,
			&sub.PricePaise, &sub.Currency, &sub.Status, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.CreatedAt,
			&iKey, &sub.GracePeriodEnd, &sub.PauseUntil, &sub.PendingTierID, &sub.PendingPricePaise,
			&sub.ActivatedAt, &sub.Counted, &r.DaysLeft); err != nil {
			return nil, err
		}
		r.RenewalPricePaise = sub.PricePaise
		if sub.PendingPricePaise != nil {
			r.RenewalPricePaise = *sub.PendingPricePaise
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClaimRenewalReminder records that the reminder for (subscription, period
// end, days_left) is being sent; false when it already was.
func (s *Store) ClaimRenewalReminder(ctx context.Context, subID uuid.UUID, periodEnd time.Time, daysLeft int) (bool, error) {
	tag, err := s.db.Exec(ctx, `INSERT INTO membership_renewal_reminders (subscription_id, period_end, days_left)
		VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, subID, periodEnd, daysLeft)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ReleaseRenewalReminder drops a claim whose publish failed, so the next
// pass sends it.
func (s *Store) ReleaseRenewalReminder(ctx context.Context, subID uuid.UUID, periodEnd time.Time, daysLeft int) error {
	_, err := s.db.Exec(ctx, `DELETE FROM membership_renewal_reminders WHERE subscription_id = $1 AND period_end = $2 AND days_left = $3`,
		subID, periodEnd, daysLeft)
	return err
}

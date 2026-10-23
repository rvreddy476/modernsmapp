package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Creator memberships: tiers and the subscriptions to them. Since migration
// 025 no membership money moves inside this database: a join, a renewal and
// an upgrade difference are paid through payments-service and applied from
// the signed event (membership_payments.go). This file keeps the state
// around it: partial tier edits, one live subscription per creator, and the
// tier changes that cost nothing.

var (
	// ErrTierNotFound: no tier with that id owned by that creator.
	ErrTierNotFound = errors.New("TIER_NOT_FOUND")
	// ErrTierLimit: the creator already has the maximum number of active tiers.
	ErrTierLimit = errors.New("TIER_LIMIT")
	// ErrAlreadySubscribed: the subscriber already holds a live subscription
	// to this creator (idx_subscriptions_one_live).
	ErrAlreadySubscribed = errors.New("ALREADY_SUBSCRIBED")
	// ErrIdempotencyKeyTaken: the subscriber already used this idempotency
	// key (idx_subscriptions_subscriber_idempotency). The caller re-reads.
	ErrIdempotencyKeyTaken = errors.New("IDEMPOTENCY_KEY_TAKEN")
	// ErrSubscriptionStateChanged: the row is no longer in the state the
	// caller read (renewed, cancelled, tier changed) — nothing was written.
	ErrSubscriptionStateChanged = errors.New("SUBSCRIPTION_STATE_CHANGED")
	// ErrRenewalAlreadyApplied: this period's renewal key is already recorded,
	// so the charge has already been taken. Nothing was written.
	ErrRenewalAlreadyApplied = errors.New("RENEWAL_ALREADY_APPLIED")
)

// Index names from migration 024. A unique violation is mapped by name.
const (
	idxOneLiveSubscription       = "idx_subscriptions_one_live"
	idxSubscriberIdempotencyKey  = "idx_subscriptions_subscriber_idempotency"
	legacyIdxSubscriptionIdemKey = "idx_subscriptions_idempotency_key"
)

// LiveSubscriptionStatuses are the statuses in which a subscription still
// belongs to its subscriber: at most one per (subscriber, creator), and the
// only ones an entitlement check will consider. Keep in step with the
// partial unique index in migration 024.
var LiveSubscriptionStatuses = []string{"active", "cancelled_at_period_end", "paused", "past_due", "grace"}

// IsLiveSubscriptionStatus reports whether status is one of the live set.
func IsLiveSubscriptionStatus(status string) bool {
	for _, s := range LiveSubscriptionStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// subscriptionColumns is the one column list every Subscription read uses,
// in scanSubscription's order.
const subscriptionColumns = `id, subscriber_id, creator_id, tier_id, tier_name, price, currency, status,
	current_period_start, current_period_end, created_at, idempotency_key,
	grace_period_end, pause_until, pending_tier_id, pending_price, activated_at, counted`

func scanSubscription(row pgx.Row) (*Subscription, error) {
	var (
		sub  Subscription
		iKey *string
	)
	if err := row.Scan(
		&sub.ID, &sub.SubscriberID, &sub.CreatorID, &sub.TierID, &sub.TierName,
		&sub.PricePaise, &sub.Currency, &sub.Status, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.CreatedAt,
		&iKey, &sub.GracePeriodEnd, &sub.PauseUntil, &sub.PendingTierID, &sub.PendingPricePaise,
		&sub.ActivatedAt, &sub.Counted,
	); err != nil {
		return nil, err
	}
	if iKey != nil {
		sub.IdempotencyKey = *iKey
	}
	return &sub, nil
}

func scanSubscriptions(rows pgx.Rows) ([]Subscription, error) {
	defer rows.Close()
	var subs []Subscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		subs = append(subs, *sub)
	}
	return subs, rows.Err()
}

// uniqueViolation returns the constraint name of a unique violation, or "".
func uniqueViolation(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName
	}
	return ""
}

// ---------------------------------------------------------------------------
// Tiers
// ---------------------------------------------------------------------------

// TierPatch is a partial tier update: a nil field is left as it is.
// Perks, when non-nil, is the complete replacement JSON array.
type TierPatch struct {
	Name       *string
	PricePaise *int64
	Currency   *string
	Perks      json.RawMessage
	IsActive   *bool
}

// lockCreatorTiers serialises tier writes for one creator for the rest of
// tx, so two concurrent creates cannot both see "two active" and both
// insert a third and a fourth.
func lockCreatorTiers(ctx context.Context, tx pgx.Tx, creatorID uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, "creator_tiers:"+creatorID.String())
	return err
}

func countActiveTiersTx(ctx context.Context, tx pgx.Tx, creatorID uuid.UUID, exclude uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM creator_tiers WHERE creator_id = $1 AND is_active AND id <> $2
	`, creatorID, exclude).Scan(&n)
	return n, err
}

// CreateTierLimited inserts t unless the creator already has maxActive
// active tiers (ErrTierLimit). The count and the insert run under one
// per-creator lock.
func (s *Store) CreateTierLimited(ctx context.Context, t *CreatorTier, maxActive int) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := lockCreatorTiers(ctx, tx, t.CreatorID); err != nil {
		return err
	}
	if t.IsActive {
		n, err := countActiveTiersTx(ctx, tx, t.CreatorID, uuid.Nil)
		if err != nil {
			return err
		}
		if n >= maxActive {
			return ErrTierLimit
		}
	}

	now := time.Now()
	t.CreatedAt, t.UpdatedAt = now, now
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.Perks == nil {
		t.Perks = json.RawMessage("[]")
	}
	t.BillingPeriod = "monthly"
	if _, err := tx.Exec(ctx, `
		INSERT INTO creator_tiers (id, creator_id, name, price, currency, perks, subscriber_count, is_active, billing_period, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'monthly', $9, $10)
	`, t.ID, t.CreatorID, t.Name, t.PricePaise, t.Currency, t.Perks,
		t.SubscriberCount, t.IsActive, t.CreatedAt, t.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateTierPartial changes only the fields p sets, on a tier the creator
// owns (ErrTierNotFound otherwise), and returns the row as it now is.
// Activating a tier counts against maxActive (ErrTierLimit).
func (s *Store) UpdateTierPartial(ctx context.Context, creatorID, tierID uuid.UUID, p TierPatch, maxActive int) (*CreatorTier, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if err := lockCreatorTiers(ctx, tx, creatorID); err != nil {
		return nil, err
	}
	var wasActive bool
	err = tx.QueryRow(ctx, `SELECT is_active FROM creator_tiers WHERE id = $1 AND creator_id = $2 FOR UPDATE`, tierID, creatorID).Scan(&wasActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTierNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.IsActive != nil && *p.IsActive && !wasActive {
		n, err := countActiveTiersTx(ctx, tx, creatorID, tierID)
		if err != nil {
			return nil, err
		}
		if n >= maxActive {
			return nil, ErrTierLimit
		}
	}

	var perks any
	if p.Perks != nil {
		perks = p.Perks
	}
	var t CreatorTier
	err = tx.QueryRow(ctx, `
		UPDATE creator_tiers SET
			name      = COALESCE($3, name),
			price     = COALESCE($4, price),
			currency  = COALESCE($5, currency),
			perks     = COALESCE($6::jsonb, perks),
			is_active = COALESCE($7, is_active),
			updated_at = NOW()
		WHERE id = $1 AND creator_id = $2
		RETURNING id, creator_id, name, price, currency, perks, subscriber_count, is_active, COALESCE(billing_period, 'monthly'), created_at, updated_at
	`, tierID, creatorID, p.Name, p.PricePaise, p.Currency, perks, p.IsActive).Scan(
		&t.ID, &t.CreatorID, &t.Name, &t.PricePaise, &t.Currency,
		&t.Perks, &t.SubscriberCount, &t.IsActive, &t.BillingPeriod, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &t, nil
}

// ---------------------------------------------------------------------------
// Subscription reads
// ---------------------------------------------------------------------------

// GetLiveSubscription returns the subscriber's live subscription to the
// creator (any of LiveSubscriptionStatuses), or nil. The partial unique
// index guarantees there is at most one.
func (s *Store) GetLiveSubscription(ctx context.Context, subscriberID, creatorID uuid.UUID) (*Subscription, error) {
	sub, err := scanSubscription(s.db.QueryRow(ctx, `
		SELECT `+subscriptionColumns+`
		FROM subscriptions
		WHERE subscriber_id = $1 AND creator_id = $2 AND status = ANY($3)
		ORDER BY current_period_end DESC
		LIMIT 1
	`, subscriberID, creatorID, LiveSubscriptionStatuses))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return sub, err
}

// ---------------------------------------------------------------------------
// Tier changes without money
// ---------------------------------------------------------------------------

// moveTierCountTx moves one counted subscription from one tier's
// subscriber_count to another's.
func moveTierCountTx(ctx context.Context, tx pgx.Tx, fromTierID, toTierID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE creator_tiers SET subscriber_count = GREATEST(subscriber_count - 1, 0), updated_at = NOW() WHERE id = $1
	`, fromTierID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE creator_tiers SET subscriber_count = subscriber_count + 1, updated_at = NOW() WHERE id = $1
	`, toTierID)
	return err
}

// UpgradeInput is an immediate move to another tier that costs nothing (a
// pro-rata difference of zero). A paid upgrade is a membership payment
// (CreateMembershipUpgrade) and changes the tier only on its signed event.
type UpgradeInput struct {
	SubscriptionID    uuid.UUID
	ExpectedTierID    uuid.UUID
	ExpectedPeriodEnd time.Time
	NewTierID         uuid.UUID
	NewTierName       string
	NewPricePaise     int64
}

// UpgradeSubscription changes the tier with no charge, in ONE transaction.
// The row must still be active, on ExpectedTierID, ending at
// ExpectedPeriodEnd (else ErrSubscriptionStateChanged).
func (s *Store) UpgradeSubscription(ctx context.Context, in UpgradeInput) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var counted bool
	err = tx.QueryRow(ctx, `
		SELECT counted FROM subscriptions
		WHERE id = $1 AND tier_id = $2 AND current_period_end = $3 AND status = 'active'
		FOR UPDATE
	`, in.SubscriptionID, in.ExpectedTierID, in.ExpectedPeriodEnd).Scan(&counted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSubscriptionStateChanged
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE subscriptions
		SET tier_id = $2, tier_name = $3, price = $4, pending_tier_id = NULL, pending_price = NULL
		WHERE id = $1
	`, in.SubscriptionID, in.NewTierID, in.NewTierName, in.NewPricePaise); err != nil {
		return err
	}
	if counted {
		if err := moveTierCountTx(ctx, tx, in.ExpectedTierID, in.NewTierID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SetPendingTier schedules (or, with nil, clears) the tier the next period
// renews on. Nothing is charged or refunded. Conditional on the row still
// being active on expectedTierID.
func (s *Store) SetPendingTier(ctx context.Context, subID, expectedTierID uuid.UUID, pendingTierID *uuid.UUID, pendingPricePaise *int64) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE subscriptions SET pending_tier_id = $3, pending_price = $4
		WHERE id = $1 AND tier_id = $2 AND status = 'active'
	`, subID, expectedTierID, pendingTierID, pendingPricePaise)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSubscriptionStateChanged
	}
	return nil
}

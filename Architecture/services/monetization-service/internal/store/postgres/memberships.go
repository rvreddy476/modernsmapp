package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Creator memberships: tiers and the subscriptions to them (correctness
// round, 23 Oct 2026). Payment still moves wallet to wallet inside this
// database; a later phase moves the charge onto payments-service. What this
// file fixes is the state around the charge: partial tier edits, one live
// subscription per creator, renewal and upgrade charged in the same
// transaction as the state change they pay for.

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
	grace_period_end, pause_until, pending_tier_id, pending_price`

func scanSubscription(row pgx.Row) (*Subscription, error) {
	var (
		sub  Subscription
		iKey *string
	)
	if err := row.Scan(
		&sub.ID, &sub.SubscriberID, &sub.CreatorID, &sub.TierID, &sub.TierName,
		&sub.PricePaise, &sub.Currency, &sub.Status, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.CreatedAt,
		&iKey, &sub.GracePeriodEnd, &sub.PauseUntil, &sub.PendingTierID, &sub.PendingPricePaise,
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
// Money movement inside a membership transaction
// ---------------------------------------------------------------------------

// debitCreditTx moves amountPaise from one creator_ledger row to another
// and writes the two transactions rows, all on tx. ErrInsufficientFunds
// when the payer's balance is short or the wallet is frozen.
func debitCreditTx(ctx context.Context, tx pgx.Tx, fromUserID, toUserID uuid.UUID, amountPaise int64, currency, description, referenceType, referenceID string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE creator_ledger SET balance = balance - $2, updated_at = NOW()
		WHERE user_id = $1 AND balance >= $2 AND is_frozen = false
	`, fromUserID, amountPaise)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInsufficientFunds
	}
	tag, err = tx.Exec(ctx, `
		UPDATE creator_ledger SET balance = balance + $2, lifetime_earnings = lifetime_earnings + $2, updated_at = NOW()
		WHERE user_id = $1
	`, toUserID, amountPaise)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("credit: no creator_ledger row for %s", toUserID)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transactions (id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at)
		VALUES ($1, $2, 'subscription_payment', $3, $4, 'completed', $5, $6, $7, NOW())
	`, uuid.New(), fromUserID, amountPaise, currency, referenceType, referenceID, description); err != nil {
		return fmt.Errorf("debit transaction record: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transactions (id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at)
		VALUES ($1, $2, 'earning', $3, $4, 'completed', $5, $6, $7, NOW())
	`, uuid.New(), toUserID, amountPaise, currency, referenceType, referenceID, description); err != nil {
		return fmt.Errorf("credit transaction record: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Renewal
// ---------------------------------------------------------------------------

// RenewalIdempotencyKey is the per-period key a renewal is recorded under:
// the subscription and the period end it renews FROM. Two attempts at the
// same period produce the same key, so only one can ever charge.
func RenewalIdempotencyKey(subscriptionID uuid.UUID, periodEnd time.Time) string {
	return fmt.Sprintf("renewal:%s:%d", subscriptionID, periodEnd.UTC().UnixMicro())
}

// RenewalInput is one period's renewal: what to charge and what the row
// becomes. TierID/TierName/PricePaise are the tier the next period is on
// (a scheduled downgrade's pending tier, or the current one).
type RenewalInput struct {
	SubscriptionID    uuid.UUID
	SubscriberID      uuid.UUID
	CreatorID         uuid.UUID
	ExpectedPeriodEnd time.Time
	NewPeriodStart    time.Time
	NewPeriodEnd      time.Time
	TierID            uuid.UUID
	TierName          string
	PricePaise        int64
	Currency          string
	IdempotencyKey    string
	Description       string
}

// RenewSubscription charges one period and extends the subscription in ONE
// transaction. The row is locked and must still end at ExpectedPeriodEnd in
// active or past_due (else ErrSubscriptionStateChanged); the period key is
// inserted before the charge (a duplicate is ErrRenewalAlreadyApplied); a
// short balance is ErrInsufficientFunds. Any failure rolls back all of it,
// so the charge and the extension land together or not at all.
func (s *Store) RenewSubscription(ctx context.Context, in RenewalInput) error {
	if in.PricePaise <= 0 {
		return fmt.Errorf("renewal amount must be positive")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var (
		status    string
		oldTierID uuid.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT status, tier_id FROM subscriptions
		WHERE id = $1 AND current_period_end = $2
		FOR UPDATE
	`, in.SubscriptionID, in.ExpectedPeriodEnd).Scan(&status, &oldTierID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSubscriptionStateChanged
	}
	if err != nil {
		return err
	}
	if status != "active" && status != "past_due" {
		return ErrSubscriptionStateChanged
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO subscription_renewals (subscription_id, period_end, idempotency_key, amount, currency, new_period_end)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING
	`, in.SubscriptionID, in.ExpectedPeriodEnd, in.IdempotencyKey, in.PricePaise, in.Currency, in.NewPeriodEnd)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRenewalAlreadyApplied
	}

	if err := debitCreditTx(ctx, tx, in.SubscriberID, in.CreatorID, in.PricePaise, in.Currency,
		in.Description, "subscription", in.SubscriptionID.String()); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE subscriptions SET
			current_period_start = $2,
			current_period_end   = $3,
			status               = 'active',
			retry_count          = 0,
			payment_failed_at    = NULL,
			grace_period_end     = NULL,
			tier_id              = $4,
			tier_name            = $5,
			price                = $6,
			pending_tier_id      = NULL,
			pending_price        = NULL
		WHERE id = $1
	`, in.SubscriptionID, in.NewPeriodStart, in.NewPeriodEnd, in.TierID, in.TierName, in.PricePaise); err != nil {
		return err
	}
	if oldTierID != in.TierID {
		if err := moveTierCountTx(ctx, tx, oldTierID, in.TierID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

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

// MarkRenewalPastDue records a failed renewal charge: status past_due,
// retry_count = retryCount, payment_failed_at = now. Conditional on the row
// still being the period and retry count the caller read.
func (s *Store) MarkRenewalPastDue(ctx context.Context, subID uuid.UUID, expectedPeriodEnd time.Time, retryCount int, now time.Time) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE subscriptions
		SET status = 'past_due', retry_count = $3, payment_failed_at = $4
		WHERE id = $1 AND current_period_end = $2 AND status IN ('active', 'past_due') AND retry_count = $3 - 1
	`, subID, expectedPeriodEnd, retryCount, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSubscriptionStateChanged
	}
	return nil
}

// EnterRenewalGrace records the failure that exhausts the retries: status
// grace until graceEnd. Same conditions as MarkRenewalPastDue.
func (s *Store) EnterRenewalGrace(ctx context.Context, subID uuid.UUID, expectedPeriodEnd time.Time, retryCount int, now, graceEnd time.Time) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE subscriptions
		SET status = 'grace', retry_count = $3, payment_failed_at = $4, grace_period_end = $5
		WHERE id = $1 AND current_period_end = $2 AND status IN ('active', 'past_due') AND retry_count = $3 - 1
	`, subID, expectedPeriodEnd, retryCount, now, graceEnd)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSubscriptionStateChanged
	}
	return nil
}

// ExpireEndedCancellations moves every cancelled_at_period_end subscription
// whose period has ended to expired, and returns the rows it moved.
func (s *Store) ExpireEndedCancellations(ctx context.Context, now time.Time) ([]Subscription, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE subscriptions SET status = 'expired'
		WHERE id IN (
			SELECT id FROM subscriptions
			WHERE status = 'cancelled_at_period_end' AND current_period_end <= $1
			ORDER BY current_period_end
			LIMIT 500
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+subscriptionColumns, now)
	if err != nil {
		return nil, err
	}
	return scanSubscriptions(rows)
}

// ---------------------------------------------------------------------------
// Tier changes
// ---------------------------------------------------------------------------

// UpgradeInput is an immediate move to a higher tier: ChargePaise (may be
// zero) is taken and the tier changes, period end unchanged.
type UpgradeInput struct {
	SubscriptionID    uuid.UUID
	SubscriberID      uuid.UUID
	CreatorID         uuid.UUID
	ExpectedTierID    uuid.UUID
	ExpectedPeriodEnd time.Time
	NewTierID         uuid.UUID
	NewTierName       string
	NewPricePaise     int64
	ChargePaise       int64
	Currency          string
	Description       string
}

// UpgradeSubscription takes the pro-rata charge and changes the tier in ONE
// transaction. The row must still be active, on ExpectedTierID, ending at
// ExpectedPeriodEnd (else ErrSubscriptionStateChanged) — so a replayed
// upgrade finds the tier already moved and charges nothing.
func (s *Store) UpgradeSubscription(ctx context.Context, in UpgradeInput) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var one int
	err = tx.QueryRow(ctx, `
		SELECT 1 FROM subscriptions
		WHERE id = $1 AND tier_id = $2 AND current_period_end = $3 AND status = 'active'
		FOR UPDATE
	`, in.SubscriptionID, in.ExpectedTierID, in.ExpectedPeriodEnd).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSubscriptionStateChanged
	}
	if err != nil {
		return err
	}
	if in.ChargePaise > 0 {
		if err := debitCreditTx(ctx, tx, in.SubscriberID, in.CreatorID, in.ChargePaise, in.Currency,
			in.Description, "subscription", in.SubscriptionID.String()); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE subscriptions
		SET tier_id = $2, tier_name = $3, price = $4, pending_tier_id = NULL, pending_price = NULL
		WHERE id = $1
	`, in.SubscriptionID, in.NewTierID, in.NewTierName, in.NewPricePaise); err != nil {
		return err
	}
	if err := moveTierCountTx(ctx, tx, in.ExpectedTierID, in.NewTierID); err != nil {
		return err
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

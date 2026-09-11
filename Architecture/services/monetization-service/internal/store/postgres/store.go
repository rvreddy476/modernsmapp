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
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInsufficientFunds is returned when a wallet has insufficient balance for a debit.
var ErrInsufficientFunds = errors.New("insufficient funds")

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

// Wallet represents a row in the creator-earnings ledger.
//
// NAMING (Phase 2 §D4, 2026-04-30): the underlying table was renamed
// from `wallets` to `creator_ledger`. The Go type name `Wallet` is
// retained as an exported alias to avoid forcing a coordinated change
// across every service that already imports `postgres.Wallet`. New
// code should reference `CreatorLedgerEntry`. The `Wallet` alias will
// be removed after 2026-10-30.
type Wallet struct {
	UserID                uuid.UUID `json:"user_id"`
	BalancePaise          int64     `json:"balance_paise"`
	LifetimeEarningsPaise int64     `json:"lifetime_earnings_paise"`
	PendingPayoutPaise    int64     `json:"pending_payout_paise"`
	Currency              string    `json:"currency"`
	IsFrozen              bool      `json:"is_frozen"`
	HasActivity           bool      `json:"has_activity"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// CreatorLedgerEntry is the canonical name for a creator-earnings
// ledger row. It is an alias of Wallet for the duration of the
// deprecation window. Prefer this name in new code.
type CreatorLedgerEntry = Wallet

// Transaction represents a financial transaction on a wallet.
type Transaction struct {
	ID            uuid.UUID `json:"id"`
	WalletID      uuid.UUID `json:"wallet_id"`
	Type          string    `json:"type"` // earning, payout, refund, adjustment, subscription_payment
	AmountPaise   int64     `json:"amount_paise"`
	Currency      string    `json:"currency"`
	Status        string    `json:"status"` // pending, completed, failed
	ReferenceType string    `json:"reference_type,omitempty"`
	ReferenceID   string    `json:"reference_id,omitempty"`
	Description   string    `json:"description,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// PayoutMethod represents a user's payout method.
//
// DetailsEncrypted is never serialised: for a bank_account method it is
// the AES-GCM ciphertext of the full account number (plan Phase 4D), and
// nothing a client is shown should carry it. What a creator sees of a
// bank account is the last four digits, the IFSC and the holder name.
type PayoutMethod struct {
	ID               uuid.UUID `json:"id"`
	UserID           uuid.UUID `json:"user_id"`
	MethodType       string    `json:"method_type"` // upi, bank_transfer, bank_account, paypal
	DetailsEncrypted string    `json:"-"`
	IsDefault        bool      `json:"is_default"`
	IsVerified       bool      `json:"is_verified"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`

	// Bank capture (Phase 4D). RzpFundAccountID is the provider's fund
	// account once one exists; VerifiedAt is set when the provider's
	// fund-account validation (penny drop) reported the account active.
	RzpFundAccountID *string    `json:"-"`
	IFSC             string     `json:"ifsc,omitempty"`
	AccountLast4     string     `json:"account_last4,omitempty"`
	HolderName       string     `json:"holder_name,omitempty"`
	VerifiedAt       *time.Time `json:"verified_at,omitempty"`
}

const payoutMethodColumns = `id, user_id, method_type, details_encrypted, is_default, is_verified, created_at, updated_at,
	rzp_fund_account_id, ifsc, account_last4, holder_name, verified_at`

func scanPayoutMethod(row pgx.Row) (*PayoutMethod, error) {
	var (
		m                          PayoutMethod
		ifsc, last4, holder, faID *string
	)
	if err := row.Scan(
		&m.ID, &m.UserID, &m.MethodType, &m.DetailsEncrypted, &m.IsDefault, &m.IsVerified, &m.CreatedAt, &m.UpdatedAt,
		&faID, &ifsc, &last4, &holder, &m.VerifiedAt,
	); err != nil {
		return nil, err
	}
	m.RzpFundAccountID = faID
	if ifsc != nil {
		m.IFSC = *ifsc
	}
	if last4 != nil {
		m.AccountLast4 = *last4
	}
	if holder != nil {
		m.HolderName = *holder
	}
	return &m, nil
}

// Subscription represents a subscription between a subscriber and a creator.
type Subscription struct {
	ID                 uuid.UUID `json:"id"`
	SubscriberID       uuid.UUID `json:"subscriber_id"`
	CreatorID          uuid.UUID `json:"creator_id"`
	TierID             uuid.UUID `json:"tier_id"`
	TierName           string    `json:"tier_name"`
	PricePaise         int64     `json:"price_paise"`
	Currency           string    `json:"currency"`
	Status             string    `json:"status"` // active, cancelled, expired, paused
	CurrentPeriodStart time.Time `json:"current_period_start"`
	CurrentPeriodEnd   time.Time `json:"current_period_end"`
	CreatedAt          time.Time `json:"created_at"`
	IdempotencyKey     string    `json:"idempotency_key,omitempty"`
}

// CreatorTier represents a creator's subscription tier.
type CreatorTier struct {
	ID              uuid.UUID       `json:"id"`
	CreatorID       uuid.UUID       `json:"creator_id"`
	Name            string          `json:"name"`
	PricePaise      int64           `json:"price_paise"`
	Currency        string          `json:"currency"`
	Perks           json.RawMessage `json:"perks"`
	SubscriberCount int             `json:"subscriber_count"`
	IsActive        bool            `json:"is_active"`
	BillingPeriod   string          `json:"billing_period,omitempty"` // monthly, quarterly, yearly
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// TaxInfo represents a user's tax information.
type TaxInfo struct {
	ID                 uuid.UUID `json:"id"`
	UserID             uuid.UUID `json:"user_id"`
	Country            string    `json:"country"`
	TaxDataEncrypted   string    `json:"tax_data_encrypted"`
	VerificationStatus string    `json:"verification_status"` // pending, verified, rejected
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// AuditLogEntry represents an append-only audit log entry.
type AuditLogEntry struct {
	ID          uuid.UUID       `json:"id"`
	TableName   string          `json:"table_name"`
	Operation   string          `json:"operation"`
	OldData     json.RawMessage `json:"old_data,omitempty"`
	NewData     json.RawMessage `json:"new_data,omitempty"`
	PerformerID uuid.UUID       `json:"performer_id"`
	IPAddress   string          `json:"ip_address,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// Dashboard aggregates wallet info, recent transactions, and tier stats.
type Dashboard struct {
	Wallet       *Wallet       `json:"wallet"`
	Transactions []Transaction `json:"recent_transactions"`
	Tiers        []CreatorTier `json:"tiers"`
}

// Account represents a double-entry ledger account.
type Account struct {
	ID           uuid.UUID `json:"id"`
	OwnerID      uuid.UUID `json:"owner_id"`
	AccountType  string    `json:"account_type"`
	BalancePaise int64     `json:"balance_paise"`
	Currency     string    `json:"currency"`
	CreatedAt    time.Time `json:"created_at"`
}

// LedgerEntry represents an immutable double-entry ledger entry.
type LedgerEntry struct {
	ID              uuid.UUID  `json:"id"`
	DebitAccountID  uuid.UUID  `json:"debit_account_id"`
	CreditAccountID uuid.UUID  `json:"credit_account_id"`
	AmountPaise     int64      `json:"amount_paise"`
	Currency        string     `json:"currency"`
	ReferenceType   string     `json:"reference_type"`
	ReferenceID     *uuid.UUID `json:"reference_id,omitempty"`
	IdempotencyKey  string     `json:"idempotency_key,omitempty"`
	Description     string     `json:"description"`
	CreatedAt       time.Time  `json:"created_at"`
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// DBTX is the query surface shared by the pool and a transaction, so a
// store function can run either autocommit or inside a caller's
// transaction without two copies of its SQL. *pgxpool.Pool and pgx.Tx
// both satisfy it.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// WithTx runs fn inside one transaction: committed when fn returns nil,
// rolled back when it returns an error or panics. The *Tx store methods
// are meant to be composed under it — a budget debit, a carry update and
// an earnings insert, or a reversal and the adjustment that gives the
// money back, must commit or vanish together.
func (s *Store) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
// Wallet
// ---------------------------------------------------------------------------

// GetWallet returns a wallet for the given user, or nil if not found.
func (s *Store) GetWallet(ctx context.Context, userID uuid.UUID) (*Wallet, error) {
	var w Wallet
	err := s.db.QueryRow(ctx, `
		SELECT user_id, balance, lifetime_earnings, pending_payout, currency, is_frozen, created_at, updated_at
		FROM creator_ledger
		WHERE user_id = $1
	`, userID).Scan(
		&w.UserID, &w.BalancePaise, &w.LifetimeEarningsPaise, &w.PendingPayoutPaise,
		&w.Currency, &w.IsFrozen, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	w.HasActivity = true
	return &w, nil
}

// EnsureWallet creates a wallet for the user if one does not already exist, then returns it.
func (s *Store) EnsureWallet(ctx context.Context, userID uuid.UUID) (*Wallet, error) {
	now := time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO creator_ledger (user_id, balance, lifetime_earnings, pending_payout, currency, is_frozen, created_at, updated_at)
		VALUES ($1, 0, 0, 0, 'INR', false, $2, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, now)
	if err != nil {
		return nil, err
	}
	return s.GetWallet(ctx, userID)
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

// GetTransactions returns transactions for a user's wallet using cursor-based pagination.
// The cursor is a created_at timestamp; results are returned in descending order.
func (s *Store) GetTransactions(ctx context.Context, userID uuid.UUID, cursor time.Time, limit int) ([]Transaction, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at
		FROM transactions
		WHERE wallet_id = $1 AND created_at < $2
		ORDER BY created_at DESC
		LIMIT $3
	`, userID, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var txns []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(
			&t.ID, &t.WalletID, &t.Type, &t.AmountPaise, &t.Currency,
			&t.Status, &t.ReferenceType, &t.ReferenceID, &t.Description, &t.CreatedAt,
		); err != nil {
			return nil, err
		}
		txns = append(txns, t)
	}
	return txns, rows.Err()
}

// GetTransactionsByType returns transactions of a specific type for a user's wallet.
func (s *Store) GetTransactionsByType(ctx context.Context, userID uuid.UUID, txType string, cursor time.Time, limit int) ([]Transaction, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at
		FROM transactions
		WHERE wallet_id = $1 AND type = $2 AND created_at < $3
		ORDER BY created_at DESC
		LIMIT $4
	`, userID, txType, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var txns []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(
			&t.ID, &t.WalletID, &t.Type, &t.AmountPaise, &t.Currency,
			&t.Status, &t.ReferenceType, &t.ReferenceID, &t.Description, &t.CreatedAt,
		); err != nil {
			return nil, err
		}
		txns = append(txns, t)
	}
	return txns, rows.Err()
}

// CreateTransaction inserts a new transaction.
func (s *Store) CreateTransaction(ctx context.Context, t *Transaction) error {
	now := time.Now()
	t.CreatedAt = now
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO transactions (id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, t.ID, t.WalletID, t.Type, t.AmountPaise, t.Currency, t.Status,
		t.ReferenceType, t.ReferenceID, t.Description, t.CreatedAt)
	return err
}

// ---------------------------------------------------------------------------
// Payout Methods
// ---------------------------------------------------------------------------

// GetPayoutMethods returns all payout methods for a user.
func (s *Store) GetPayoutMethods(ctx context.Context, userID uuid.UUID) ([]PayoutMethod, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+payoutMethodColumns+`
		FROM payout_methods
		WHERE user_id = $1
		ORDER BY is_default DESC, created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var methods []PayoutMethod
	for rows.Next() {
		m, err := scanPayoutMethod(rows)
		if err != nil {
			return nil, err
		}
		methods = append(methods, *m)
	}
	return methods, rows.Err()
}

// AddPayoutMethod adds a new payout method for a user.
func (s *Store) AddPayoutMethod(ctx context.Context, m *PayoutMethod) error {
	now := time.Now()
	m.CreatedAt = now
	m.UpdatedAt = now
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	var ifsc, last4, holder *string
	if m.IFSC != "" {
		ifsc = &m.IFSC
	}
	if m.AccountLast4 != "" {
		last4 = &m.AccountLast4
	}
	if m.HolderName != "" {
		holder = &m.HolderName
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO payout_methods
			(id, user_id, method_type, details_encrypted, is_default, is_verified, created_at, updated_at,
			 rzp_fund_account_id, ifsc, account_last4, holder_name, verified_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, m.ID, m.UserID, m.MethodType, m.DetailsEncrypted, m.IsDefault, m.IsVerified, m.CreatedAt, m.UpdatedAt,
		m.RzpFundAccountID, ifsc, last4, holder, m.VerifiedAt)
	return err
}

// RemovePayoutMethod deletes a payout method by ID, scoped to user.
func (s *Store) RemovePayoutMethod(ctx context.Context, userID, methodID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM payout_methods WHERE id = $1 AND user_id = $2
	`, methodID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("PAYOUT_METHOD_NOT_FOUND")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Payouts
// ---------------------------------------------------------------------------
//
// The withdrawal path lives in payout_requests.go as *Tx functions that
// Service.RequestPayout composes under one transaction (plan Phase 3A).
// The old Store.RequestPayout — lock, move, one transactions row, no
// payout_requests row, no gates — was removed with it.

// ---------------------------------------------------------------------------
// Creator Tiers
// ---------------------------------------------------------------------------

// GetCreatorTiers returns all tiers for a creator.
func (s *Store) GetCreatorTiers(ctx context.Context, creatorID uuid.UUID) ([]CreatorTier, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, creator_id, name, price, currency, perks, subscriber_count, is_active, created_at, updated_at
		FROM creator_tiers
		WHERE creator_id = $1
		ORDER BY price ASC
	`, creatorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tiers []CreatorTier
	for rows.Next() {
		var t CreatorTier
		if err := rows.Scan(
			&t.ID, &t.CreatorID, &t.Name, &t.PricePaise, &t.Currency,
			&t.Perks, &t.SubscriberCount, &t.IsActive, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		tiers = append(tiers, t)
	}
	return tiers, rows.Err()
}

// GetCreatorTier returns a single tier by ID.
func (s *Store) GetCreatorTier(ctx context.Context, tierID uuid.UUID) (*CreatorTier, error) {
	var t CreatorTier
	err := s.db.QueryRow(ctx, `
		SELECT id, creator_id, name, price, currency, perks, subscriber_count, is_active, created_at, updated_at
		FROM creator_tiers
		WHERE id = $1
	`, tierID).Scan(
		&t.ID, &t.CreatorID, &t.Name, &t.PricePaise, &t.Currency,
		&t.Perks, &t.SubscriberCount, &t.IsActive, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// CreateTier creates a new creator tier.
func (s *Store) CreateTier(ctx context.Context, t *CreatorTier) error {
	now := time.Now()
	t.CreatedAt = now
	t.UpdatedAt = now
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.Perks == nil {
		t.Perks = json.RawMessage("[]")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO creator_tiers (id, creator_id, name, price, currency, perks, subscriber_count, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, t.ID, t.CreatorID, t.Name, t.PricePaise, t.Currency, t.Perks,
		t.SubscriberCount, t.IsActive, t.CreatedAt, t.UpdatedAt)
	return err
}

// UpdateTier updates an existing creator tier.
func (s *Store) UpdateTier(ctx context.Context, t *CreatorTier) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE creator_tiers
		SET name = $3, price = $4, currency = $5, perks = $6, is_active = $7, updated_at = NOW()
		WHERE id = $1 AND creator_id = $2
	`, t.ID, t.CreatorID, t.Name, t.PricePaise, t.Currency, t.Perks, t.IsActive)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("TIER_NOT_FOUND")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Subscriptions
// ---------------------------------------------------------------------------

// Subscribe creates a subscription, charges the subscriber wallet, and credits the creator wallet.
// All operations are performed within a single DB transaction.
// idempotencyKey is optional; if non-empty it is stored on the subscription row.
func (s *Store) Subscribe(ctx context.Context, subscriberID, creatorID, tierID uuid.UUID, tierName string, pricePaise int64, currency string, idempotencyKey string) (*Subscription, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	now := time.Now()
	periodEnd := now.AddDate(0, 1, 0) // 1 month subscription period

	// Deduct from subscriber wallet
	tag, err := tx.Exec(ctx, `
		UPDATE creator_ledger SET balance = balance - $2, updated_at = NOW()
		WHERE user_id = $1 AND balance >= $2 AND is_frozen = false
	`, subscriberID, pricePaise)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, errors.New("INSUFFICIENT_BALANCE_OR_FROZEN")
	}

	// Credit creator wallet (lifetime_earnings too)
	_, err = tx.Exec(ctx, `
		UPDATE creator_ledger SET balance = balance + $2, lifetime_earnings = lifetime_earnings + $2, updated_at = NOW()
		WHERE user_id = $1
	`, creatorID, pricePaise)
	if err != nil {
		return nil, err
	}

	// Create subscription record
	var iKey *string
	if idempotencyKey != "" {
		iKey = &idempotencyKey
	}
	sub := &Subscription{
		ID:                 uuid.New(),
		SubscriberID:       subscriberID,
		CreatorID:          creatorID,
		TierID:             tierID,
		TierName:           tierName,
		PricePaise:         pricePaise,
		Currency:           currency,
		Status:             "active",
		CurrentPeriodStart: now,
		CurrentPeriodEnd:   periodEnd,
		CreatedAt:          now,
		IdempotencyKey:     idempotencyKey,
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO subscriptions (id, subscriber_id, creator_id, tier_id, tier_name, price, currency, status, current_period_start, current_period_end, created_at, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, sub.ID, sub.SubscriberID, sub.CreatorID, sub.TierID, sub.TierName,
		sub.PricePaise, sub.Currency, sub.Status, sub.CurrentPeriodStart, sub.CurrentPeriodEnd, sub.CreatedAt, iKey)
	if err != nil {
		return nil, err
	}

	// Create subscriber payment transaction
	_, err = tx.Exec(ctx, `
		INSERT INTO transactions (id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at)
		VALUES ($1, $2, 'subscription_payment', $3, $4, 'completed', 'subscription', $5, $6, $7)
	`, uuid.New(), subscriberID, pricePaise, currency, sub.ID.String(),
		"Subscription to "+tierName, now)
	if err != nil {
		return nil, err
	}

	// Create creator earning transaction
	_, err = tx.Exec(ctx, `
		INSERT INTO transactions (id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at)
		VALUES ($1, $2, 'earning', $3, $4, 'completed', 'subscription', $5, $6, $7)
	`, uuid.New(), creatorID, pricePaise, currency, sub.ID.String(),
		"Earning from subscription", now)
	if err != nil {
		return nil, err
	}

	// Increment tier subscriber count
	_, err = tx.Exec(ctx, `
		UPDATE creator_tiers SET subscriber_count = subscriber_count + 1, updated_at = NOW()
		WHERE id = $1
	`, tierID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return sub, nil
}

// Unsubscribe cancels an active subscription between a subscriber and a creator.
func (s *Store) Unsubscribe(ctx context.Context, subscriberID, creatorID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Find and cancel the active subscription
	var tierID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE subscriptions
		SET status = 'cancelled'
		WHERE subscriber_id = $1 AND creator_id = $2 AND status = 'active'
		RETURNING tier_id
	`, subscriberID, creatorID).Scan(&tierID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("SUBSCRIPTION_NOT_FOUND")
		}
		return err
	}

	// Decrement tier subscriber count
	_, err = tx.Exec(ctx, `
		UPDATE creator_tiers SET subscriber_count = GREATEST(subscriber_count - 1, 0), updated_at = NOW()
		WHERE id = $1
	`, tierID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetSubscription returns the active subscription between a subscriber and a creator.
func (s *Store) GetSubscription(ctx context.Context, subscriberID, creatorID uuid.UUID) (*Subscription, error) {
	var sub Subscription
	err := s.db.QueryRow(ctx, `
		SELECT id, subscriber_id, creator_id, tier_id, tier_name, price, currency, status, current_period_start, current_period_end, created_at
		FROM subscriptions
		WHERE subscriber_id = $1 AND creator_id = $2 AND status = 'active'
	`, subscriberID, creatorID).Scan(
		&sub.ID, &sub.SubscriberID, &sub.CreatorID, &sub.TierID, &sub.TierName,
		&sub.PricePaise, &sub.Currency, &sub.Status, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &sub, nil
}

// GetSubscriptionByIdempotencyKey returns a subscription matching the given idempotency key,
// or nil if no matching subscription exists.
func (s *Store) GetSubscriptionByIdempotencyKey(ctx context.Context, key string) (*Subscription, error) {
	var sub Subscription
	err := s.db.QueryRow(ctx, `
		SELECT id, subscriber_id, creator_id, tier_id, tier_name, price, currency, status, current_period_start, current_period_end, created_at
		FROM subscriptions
		WHERE idempotency_key = $1
	`, key).Scan(
		&sub.ID, &sub.SubscriberID, &sub.CreatorID, &sub.TierID, &sub.TierName,
		&sub.PricePaise, &sub.Currency, &sub.Status, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	sub.IdempotencyKey = key
	return &sub, nil
}

// ChargeAndCredit atomically debits fromUserID and credits toUserID in a single transaction.
// Returns ErrInsufficientFunds if fromUserID has insufficient balance.
func (s *Store) ChargeAndCredit(ctx context.Context, fromUserID, toUserID string, amountPaise int64, description string) error {
	return s.ChargeAndCreditRef(ctx, fromUserID, toUserID, amountPaise, description, "", "")
}

// ChargeAndCreditRef is ChargeAndCredit with provenance on the two
// transaction rows it writes.
//
// This exists because the period statement has to be able to tell a
// creator's subscription revenue apart from their tip revenue, and both
// arrive on the same wallet as type='earning'. Store.Subscribe already
// stamped reference_type='subscription'; the RENEWAL worker went through
// plain ChargeAndCredit and stamped nothing, so every renewal after the
// first month was invisible to any query that asked "what did
// subscriptions earn this creator". Passing the reference through fixes
// that without touching a single amount: same transfer, same wallets,
// same money, now attributable.
func (s *Store) ChargeAndCreditRef(ctx context.Context, fromUserID, toUserID string, amountPaise int64, description, referenceType, referenceID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Atomic debit: only succeeds if balance is sufficient
	var newBalance int64
	err = tx.QueryRow(ctx,
		`UPDATE creator_ledger SET balance = balance - $2, updated_at = NOW() WHERE user_id = $1 AND balance >= $2 AND is_frozen = false RETURNING balance`,
		fromUserID, amountPaise,
	).Scan(&newBalance)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInsufficientFunds
		}
		return err
	}

	// Credit
	_, err = tx.Exec(ctx,
		`UPDATE creator_ledger SET balance = balance + $2, lifetime_earnings = lifetime_earnings + $2, updated_at = NOW() WHERE user_id = $1`,
		toUserID, amountPaise,
	)
	if err != nil {
		return err
	}

	// Record debit transaction
	_, err = tx.Exec(ctx,
		`INSERT INTO transactions (id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at)
		 SELECT $1, user_id, 'subscription_payment', $3, currency, 'completed', $5, $6, $4, NOW() FROM creator_ledger WHERE user_id = $2`,
		uuid.New(), fromUserID, amountPaise, description, referenceType, referenceID,
	)
	if err != nil {
		return fmt.Errorf("debit transaction record: %w", err)
	}

	// Record credit transaction
	_, err = tx.Exec(ctx,
		`INSERT INTO transactions (id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at)
		 SELECT $1, user_id, 'earning', $3, currency, 'completed', $5, $6, $4, NOW() FROM creator_ledger WHERE user_id = $2`,
		uuid.New(), toUserID, amountPaise, description, referenceType, referenceID,
	)
	if err != nil {
		return fmt.Errorf("credit transaction record: %w", err)
	}

	return tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
// Tax Info
// ---------------------------------------------------------------------------

// SaveTaxInfo upserts tax information for a user.
func (s *Store) SaveTaxInfo(ctx context.Context, t *TaxInfo) error {
	now := time.Now()
	t.UpdatedAt = now
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
		t.CreatedAt = now
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO tax_info (id, user_id, country, tax_data_encrypted, verification_status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id) DO UPDATE SET
			country = EXCLUDED.country,
			tax_data_encrypted = EXCLUDED.tax_data_encrypted,
			verification_status = EXCLUDED.verification_status,
			updated_at = EXCLUDED.updated_at
	`, t.ID, t.UserID, t.Country, t.TaxDataEncrypted, t.VerificationStatus, t.CreatedAt, t.UpdatedAt)
	return err
}

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

// GetDashboard returns wallet, recent transactions, and creator tier stats for a user.
func (s *Store) GetDashboard(ctx context.Context, userID uuid.UUID) (*Dashboard, error) {
	wallet, err := s.GetWallet(ctx, userID)
	if err != nil {
		return nil, err
	}

	txns, err := s.GetTransactions(ctx, userID, time.Now(), 10)
	if err != nil {
		return nil, err
	}
	if txns == nil {
		txns = []Transaction{}
	}

	tiers, err := s.GetCreatorTiers(ctx, userID)
	if err != nil {
		return nil, err
	}
	if tiers == nil {
		tiers = []CreatorTier{}
	}

	return &Dashboard{
		Wallet:       wallet,
		Transactions: txns,
		Tiers:        tiers,
	}, nil
}

// ---------------------------------------------------------------------------
// Audit Log
// ---------------------------------------------------------------------------

// WriteAuditLog appends an entry to the monetization_audit_log table.
func (s *Store) WriteAuditLog(ctx context.Context, entry *AuditLogEntry) error {
	now := time.Now()
	entry.CreatedAt = now
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO monetization_audit_log (id, table_name, operation, old_data, new_data, performer_id, ip_address, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, entry.ID, entry.TableName, entry.Operation, entry.OldData, entry.NewData,
		entry.PerformerID, entry.IPAddress, entry.CreatedAt)
	return err
}

// ---------------------------------------------------------------------------
// Analytics (cross-schema query)
// ---------------------------------------------------------------------------

// QueryViewScoreTotal returns the sum of view_score_total from analytics.v_creator_daily_metrics_v1
// for the given creator within the date range.
func (s *Store) QueryViewScoreTotal(ctx context.Context, creatorID uuid.UUID, since, until time.Time) (float64, error) {
	var total float64
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(view_score_total), 0)
		FROM analytics.v_creator_daily_metrics_v1
		WHERE creator_id = $1 AND day_bucket >= $2 AND day_bucket <= $3`,
		creatorID, since, until,
	).Scan(&total)
	return total, err
}

// ---------------------------------------------------------------------------
// Accounts (double-entry ledger)
// ---------------------------------------------------------------------------

// EnsureAccount creates an account for the given owner and type if one does not exist, then returns it.
func (s *Store) EnsureAccount(ctx context.Context, ownerID uuid.UUID, accountType string) (*Account, error) {
	return s.EnsureAccountTx(ctx, s.db, ownerID, accountType)
}

// EnsureAccountTx is EnsureAccount on the caller's transaction, so an
// account created for a ledger leg vanishes with the leg if the
// transaction rolls back.
func (s *Store) EnsureAccountTx(ctx context.Context, db DBTX, ownerID uuid.UUID, accountType string) (*Account, error) {
	now := time.Now()
	_, err := db.Exec(ctx, `
		INSERT INTO accounts (owner_id, account_type, balance_paise, currency, created_at)
		VALUES ($1, $2, 0, 'INR', $3)
		ON CONFLICT (owner_id, account_type) DO NOTHING
	`, ownerID, accountType, now)
	if err != nil {
		return nil, err
	}
	return s.GetAccountTx(ctx, db, ownerID, accountType)
}

// GetAccount returns the account for the given owner and type, or nil if not found.
func (s *Store) GetAccount(ctx context.Context, ownerID uuid.UUID, accountType string) (*Account, error) {
	return s.GetAccountTx(ctx, s.db, ownerID, accountType)
}

// GetAccountTx is GetAccount on the caller's transaction.
func (s *Store) GetAccountTx(ctx context.Context, db DBTX, ownerID uuid.UUID, accountType string) (*Account, error) {
	var a Account
	err := db.QueryRow(ctx, `
		SELECT id, owner_id, account_type, balance_paise, currency, created_at
		FROM accounts
		WHERE owner_id = $1 AND account_type = $2
	`, ownerID, accountType).Scan(
		&a.ID, &a.OwnerID, &a.AccountType, &a.BalancePaise, &a.Currency, &a.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

// InsertLedgerEntry inserts an immutable ledger entry and atomically updates the debit and credit account balances.
func (s *Store) InsertLedgerEntry(ctx context.Context, entry *LedgerEntry) error {
	return s.WithTx(ctx, func(tx pgx.Tx) error {
		return s.InsertLedgerEntryTx(ctx, tx, entry)
	})
}

// InsertLedgerEntryTx is InsertLedgerEntry on the caller's transaction:
// the leg and both balance moves commit with whatever the caller is
// recording alongside them.
func (s *Store) InsertLedgerEntryTx(ctx context.Context, db DBTX, entry *LedgerEntry) error {
	now := time.Now()
	entry.CreatedAt = now
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}

	_, err := db.Exec(ctx, `
		INSERT INTO ledger_entries (id, debit_account_id, credit_account_id, amount_paise, currency, reference_type, reference_id, idempotency_key, description, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, entry.ID, entry.DebitAccountID, entry.CreditAccountID, entry.AmountPaise, entry.Currency,
		entry.ReferenceType, entry.ReferenceID, entry.IdempotencyKey, entry.Description, entry.CreatedAt)
	if err != nil {
		return err
	}

	// Debit account: decrease balance
	_, err = db.Exec(ctx, `
		UPDATE accounts SET balance_paise = balance_paise - $2 WHERE id = $1
	`, entry.DebitAccountID, entry.AmountPaise)
	if err != nil {
		return err
	}

	// Credit account: increase balance
	_, err = db.Exec(ctx, `
		UPDATE accounts SET balance_paise = balance_paise + $2 WHERE id = $1
	`, entry.CreditAccountID, entry.AmountPaise)
	return err
}

// GetLedgerEntries returns ledger entries for a given account (debit or credit side), ordered by created_at DESC.
func (s *Store) GetLedgerEntries(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]LedgerEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, debit_account_id, credit_account_id, amount_paise, currency, reference_type, reference_id, idempotency_key, description, created_at
		FROM ledger_entries
		WHERE debit_account_id = $1 OR credit_account_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, accountID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(
			&e.ID, &e.DebitAccountID, &e.CreditAccountID, &e.AmountPaise, &e.Currency,
			&e.ReferenceType, &e.ReferenceID, &e.IdempotencyKey, &e.Description, &e.CreatedAt,
		); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// RebuildBalanceFromLedger recalculates an account's balance from ledger entries.
// Returns the computed balance in paise.
func (s *Store) RebuildBalanceFromLedger(ctx context.Context, accountID uuid.UUID) (int64, error) {
	var creditSum, debitSum int64

	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_paise), 0) FROM ledger_entries WHERE credit_account_id = $1
	`, accountID).Scan(&creditSum)
	if err != nil {
		return 0, err
	}

	err = s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_paise), 0) FROM ledger_entries WHERE debit_account_id = $1
	`, accountID).Scan(&debitSum)
	if err != nil {
		return 0, err
	}

	return creditSum - debitSum, nil
}

// CreateBalanceSnapshot records a point-in-time balance snapshot for an account.
func (s *Store) CreateBalanceSnapshot(ctx context.Context, accountID uuid.UUID, balancePaise int64) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO balance_snapshots (account_id, balance_paise, snapshot_at)
		VALUES ($1, $2, NOW())
	`, accountID, balancePaise)
	return err
}

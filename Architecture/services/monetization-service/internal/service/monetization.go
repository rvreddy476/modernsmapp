package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// EntitlementPublisher is a tiny seam over the events.Producer so the
// service package doesn't take a hard dependency on the events
// package. Wired in main.go. nil means "don't publish" — used for
// tests and for the legacy code path before this seam existed.
type EntitlementPublisher interface {
	PublishEntitlementChanged(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, action string) error
}

type Service struct {
	store           *postgres.Store
	rdb             *redis.Client
	creatorFundCfg  CreatorFundConfig
	entitlementPub  EntitlementPublisher
}

func New(s *postgres.Store, rdb *redis.Client) *Service {
	return &Service{store: s, rdb: rdb, creatorFundCfg: DefaultCreatorFundConfig()}
}

// WithCreatorFundConfig overrides the default creator-fund config (env-driven
// thresholds and platform fee). Returns the same Service for chaining.
func (s *Service) WithCreatorFundConfig(cfg CreatorFundConfig) *Service {
	s.creatorFundCfg = cfg
	return s
}

// WithEntitlementPublisher wires the Kafka producer used to publish
// entitlement.changed events on subscribe/unsubscribe. Nil-safe.
func (s *Service) WithEntitlementPublisher(p EntitlementPublisher) *Service {
	s.entitlementPub = p
	return s
}

// CreatorFundConfigSnapshot returns the active config (for the status
// endpoint and for tests).
func (s *Service) CreatorFundConfigSnapshot() CreatorFundConfig {
	return s.creatorFundCfg
}

// publishEntitlementChanged is a nil-safe internal helper used by
// Subscribe/Unsubscribe to fan out cache-invalidation events.
// Failures are logged silently (no return) — the event is best-effort
// since the database write is the source of truth and a TTL-only
// cache will eventually resolve to the right state anyway.
func (s *Service) publishEntitlementChanged(ctx context.Context, subscriptionID, subscriberID, creatorID uuid.UUID, action string) {
	if s.entitlementPub == nil {
		return
	}
	_ = s.entitlementPub.PublishEntitlementChanged(ctx, subscriptionID, subscriberID, creatorID, action)
}

// ---------------------------------------------------------------------------
// Wallet
// ---------------------------------------------------------------------------

// GetWallet returns the wallet for a user, auto-creating it on first access.
func (s *Service) GetWallet(ctx context.Context, userID uuid.UUID) (*postgres.Wallet, error) {
	wallet, err := s.store.GetWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	if wallet == nil {
		// Auto-create wallet on first access
		wallet, err = s.store.EnsureWallet(ctx, userID)
		if err != nil {
			return nil, err
		}
	}
	return wallet, nil
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

// GetTransactions returns paginated transactions for a user's wallet.
func (s *Service) GetTransactions(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]postgres.Transaction, error) {
	t := parseCursor(cursor)
	return s.store.GetTransactions(ctx, userID, t, limit)
}

// GetPayouts returns paginated payout transactions for a user's wallet.
func (s *Service) GetPayouts(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]postgres.Transaction, error) {
	t := parseCursor(cursor)
	return s.store.GetTransactionsByType(ctx, userID, "payout", t, limit)
}

// CreateTransaction creates a new transaction record.
func (s *Service) CreateTransaction(ctx context.Context, tx *postgres.Transaction) error {
	return s.store.CreateTransaction(ctx, tx)
}

// ---------------------------------------------------------------------------
// Payout Methods
// ---------------------------------------------------------------------------

// GetPayoutMethods returns all payout methods for a user.
func (s *Service) GetPayoutMethods(ctx context.Context, userID uuid.UUID) ([]postgres.PayoutMethod, error) {
	return s.store.GetPayoutMethods(ctx, userID)
}

// AddPayoutMethod adds a new payout method.
func (s *Service) AddPayoutMethod(ctx context.Context, m *postgres.PayoutMethod) error {
	return s.store.AddPayoutMethod(ctx, m)
}

// RemovePayoutMethod removes a payout method by ID, scoped to the user.
func (s *Service) RemovePayoutMethod(ctx context.Context, userID, methodID uuid.UUID) error {
	return s.store.RemovePayoutMethod(ctx, userID, methodID)
}

// ---------------------------------------------------------------------------
// Payouts
// ---------------------------------------------------------------------------

// RequestPayout validates the request and creates a payout transaction.
// Validates: wallet exists, not frozen, sufficient balance.
func (s *Service) RequestPayout(ctx context.Context, userID uuid.UUID, amountPaise int64, payoutMethodID uuid.UUID) (*postgres.Transaction, error) {
	if amountPaise <= 0 || amountPaise > maxAmountPaise {
		return nil, fmt.Errorf("amount out of valid range: %w", ErrInvalidAmount)
	}

	// Ensure wallet exists
	wallet, err := s.store.GetWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	if wallet == nil {
		return nil, fmt.Errorf("WALLET_NOT_FOUND")
	}
	if wallet.IsFrozen {
		return nil, fmt.Errorf("WALLET_FROZEN")
	}
	if wallet.BalancePaise < amountPaise {
		return nil, fmt.Errorf("INSUFFICIENT_BALANCE")
	}

	return s.store.RequestPayout(ctx, userID, amountPaise, payoutMethodID)
}

// ---------------------------------------------------------------------------
// Creator Tiers
// ---------------------------------------------------------------------------

// GetCreatorTiers returns all tiers for a creator.
func (s *Service) GetCreatorTiers(ctx context.Context, creatorID uuid.UUID) ([]postgres.CreatorTier, error) {
	return s.store.GetCreatorTiers(ctx, creatorID)
}

// CreateTier creates a new creator tier.
func (s *Service) CreateTier(ctx context.Context, t *postgres.CreatorTier) error {
	return s.store.CreateTier(ctx, t)
}

// UpdateTier updates an existing creator tier.
func (s *Service) UpdateTier(ctx context.Context, t *postgres.CreatorTier) error {
	return s.store.UpdateTier(ctx, t)
}

// ---------------------------------------------------------------------------
// Subscriptions
// ---------------------------------------------------------------------------

// ErrInvalidAmount is returned when an amount is out of valid range.
var ErrInvalidAmount = errors.New("invalid amount")

const maxAmountPaise int64 = 100_000_000 // 1,000,000.00 INR in paise

// Subscribe validates the tier exists and is active, then creates the subscription.
// Charges the subscriber wallet and credits the creator wallet.
// idempotencyKey is optional; if non-empty, a duplicate call with the same key returns the existing subscription.
func (s *Service) Subscribe(ctx context.Context, subscriberID, creatorID uuid.UUID, tierID uuid.UUID, idempotencyKey string) (*postgres.Subscription, error) {
	if subscriberID == creatorID {
		return nil, fmt.Errorf("CANNOT_SUBSCRIBE_TO_SELF")
	}

	// Idempotency check: return cached result if key already used
	if idempotencyKey != "" {
		existing, err := s.store.GetSubscriptionByIdempotencyKey(ctx, idempotencyKey)
		if err == nil && existing != nil {
			return existing, nil
		}
	}

	// Validate tier exists and is active
	tier, err := s.store.GetCreatorTier(ctx, tierID)
	if err != nil {
		return nil, err
	}
	if tier == nil {
		return nil, fmt.Errorf("TIER_NOT_FOUND")
	}
	if !tier.IsActive {
		return nil, fmt.Errorf("TIER_INACTIVE")
	}
	if tier.CreatorID != creatorID {
		return nil, fmt.Errorf("TIER_CREATOR_MISMATCH")
	}

	// Amount bounds validation
	if tier.PricePaise <= 0 || tier.PricePaise > maxAmountPaise {
		return nil, fmt.Errorf("amount out of valid range: %w", ErrInvalidAmount)
	}

	// Check for existing active subscription
	existing, err := s.store.GetSubscription(ctx, subscriberID, creatorID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("ALREADY_SUBSCRIBED")
	}

	// Ensure both wallets exist
	_, err = s.store.EnsureWallet(ctx, subscriberID)
	if err != nil {
		return nil, err
	}
	_, err = s.store.EnsureWallet(ctx, creatorID)
	if err != nil {
		return nil, err
	}

	sub, err := s.store.Subscribe(ctx, subscriberID, creatorID, tier.ID, tier.Name, tier.PricePaise, tier.Currency, idempotencyKey)
	if err != nil {
		return nil, err
	}
	// Tier 1a: invalidate downstream caches immediately so a fan who
	// just subscribed sees gated content on the next read instead of
	// waiting for the TTL to expire.
	s.publishEntitlementChanged(ctx, sub.ID, subscriberID, creatorID, "granted")
	return sub, nil
}

// Unsubscribe cancels an active subscription.
func (s *Service) Unsubscribe(ctx context.Context, subscriberID, creatorID uuid.UUID) error {
	if err := s.store.Unsubscribe(ctx, subscriberID, creatorID); err != nil {
		return err
	}
	s.publishEntitlementChanged(ctx, uuid.Nil, subscriberID, creatorID, "revoked")
	return nil
}

// GetSubscription returns the active subscription between a subscriber and a creator.
func (s *Service) GetSubscription(ctx context.Context, subscriberID, creatorID uuid.UUID) (*postgres.Subscription, error) {
	return s.store.GetSubscription(ctx, subscriberID, creatorID)
}

// ---------------------------------------------------------------------------
// Tax Info
// ---------------------------------------------------------------------------

// SaveTaxInfo saves or updates tax information for a user.
func (s *Service) SaveTaxInfo(ctx context.Context, t *postgres.TaxInfo) error {
	return s.store.SaveTaxInfo(ctx, t)
}

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

// GetDashboard returns an aggregated view of wallet, recent transactions, and tier stats.
// Ensures the wallet exists before fetching the dashboard.
func (s *Service) GetDashboard(ctx context.Context, userID uuid.UUID) (*postgres.Dashboard, error) {
	// Ensure wallet exists
	_, err := s.store.EnsureWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.store.GetDashboard(ctx, userID)
}

// ---------------------------------------------------------------------------
// Audit Log
// ---------------------------------------------------------------------------

// WriteAuditLog writes an entry to the audit log.
func (s *Service) WriteAuditLog(ctx context.Context, entry *postgres.AuditLogEntry) error {
	return s.store.WriteAuditLog(ctx, entry)
}

// ---------------------------------------------------------------------------
// View Earnings
// ---------------------------------------------------------------------------

// ProcessViewEarnings calculates and credits view-based earnings for a creator
// over the specified period (e.g. "7d", "30d"). It queries the analytics
// daily summary table for aggregated view scores, applies an earnings rate,
// and creates a wallet transaction if earnings are positive.
func (s *Service) ProcessViewEarnings(ctx context.Context, creatorID uuid.UUID, period string) error {
	// Parse period string (e.g. "7d", "30d") into days.
	if !strings.HasSuffix(period, "d") {
		return fmt.Errorf("INVALID_PERIOD: expected format like '7d' or '30d', got %q", period)
	}
	daysStr := strings.TrimSuffix(period, "d")
	days, err := strconv.Atoi(daysStr)
	if err != nil || days <= 0 {
		return fmt.Errorf("INVALID_PERIOD: %q is not a positive number of days", daysStr)
	}

	// Determine the date range.
	now := time.Now().UTC()
	startDate := now.AddDate(0, 0, -days)

	// Query analytics.content_daily_summary (PostgreSQL) for the creator's total view score.
	viewScoreTotal, err := s.store.QueryViewScoreTotal(ctx, creatorID, startDate, now)
	if err != nil {
		return fmt.Errorf("query view_score_total: %w", err)
	}

	// Calculate earnings in paise: 1 mill per 1000 view score = 0.1 paise per view score.
	// Using integer math: earningsPaise = viewScoreTotal / 10 (rounded down).
	earningsPaise := int64(viewScoreTotal / 10)

	if earningsPaise <= 0 {
		return nil
	}

	// Ensure the creator's wallet exists.
	wallet, err := s.store.EnsureWallet(ctx, creatorID)
	if err != nil {
		return fmt.Errorf("ensure wallet: %w", err)
	}

	// Create a view_earnings transaction.
	tx := &postgres.Transaction{
		ID:          uuid.New(),
		WalletID:    wallet.UserID,
		Type:        "view_earnings",
		AmountPaise: earningsPaise,
		Currency:    wallet.Currency,
		Description: fmt.Sprintf("View earnings for %s period (%d days, %.2f view score)", period, days, viewScoreTotal),
		CreatedAt:   time.Now(),
	}
	if err := s.store.CreateTransaction(ctx, tx); err != nil {
		return fmt.Errorf("create view_earnings transaction: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Ledger
// ---------------------------------------------------------------------------

// CreateLedgerEntry creates a double-entry ledger entry between two accounts.
// It ensures both accounts exist before inserting the entry.
func (s *Service) CreateLedgerEntry(ctx context.Context, debitOwnerID uuid.UUID, debitAccountType string, creditOwnerID uuid.UUID, creditAccountType string, amountPaise int64, currency, referenceType string, referenceID *uuid.UUID, idempotencyKey, description string) error {
	if amountPaise <= 0 {
		return fmt.Errorf("ledger entry amount must be positive: %w", ErrInvalidAmount)
	}

	debitAccount, err := s.store.EnsureAccount(ctx, debitOwnerID, debitAccountType)
	if err != nil {
		return fmt.Errorf("ensure debit account: %w", err)
	}

	creditAccount, err := s.store.EnsureAccount(ctx, creditOwnerID, creditAccountType)
	if err != nil {
		return fmt.Errorf("ensure credit account: %w", err)
	}

	entry := &postgres.LedgerEntry{
		DebitAccountID:  debitAccount.ID,
		CreditAccountID: creditAccount.ID,
		AmountPaise:     amountPaise,
		Currency:        currency,
		ReferenceType:   referenceType,
		ReferenceID:     referenceID,
		IdempotencyKey:  idempotencyKey,
		Description:     description,
	}
	return s.store.InsertLedgerEntry(ctx, entry)
}

// ---------------------------------------------------------------------------
// Admin wallet operations
// ---------------------------------------------------------------------------

// FreezeWallet freezes a user's wallet.
func (s *Service) FreezeWallet(ctx context.Context, userID uuid.UUID) error {
	return s.store.FreezeWallet(ctx, userID)
}

// UnfreezeWallet unfreezes a user's wallet.
func (s *Service) UnfreezeWallet(ctx context.Context, userID uuid.UUID) error {
	return s.store.UnfreezeWallet(ctx, userID)
}

// RebuildWallet recalculates a wallet balance from ledger entries.
func (s *Service) RebuildWallet(ctx context.Context, userID uuid.UUID) (int64, error) {
	return s.store.RebuildWalletFromLedger(ctx, userID)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseCursor parses a RFC3339Nano cursor string into a time.Time.
// If the cursor is empty or invalid, returns time.Now() as the default.
func parseCursor(cursor string) time.Time {
	if cursor == "" {
		return time.Now()
	}
	t, err := time.Parse(time.RFC3339Nano, cursor)
	if err != nil {
		return time.Now()
	}
	return t
}

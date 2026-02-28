package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/facebook-like/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	store *postgres.Store
	rdb   *redis.Client
}

func New(s *postgres.Store, rdb *redis.Client) *Service {
	return &Service{store: s, rdb: rdb}
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
func (s *Service) RequestPayout(ctx context.Context, userID uuid.UUID, amount float64, payoutMethodID uuid.UUID) (*postgres.Transaction, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("INVALID_AMOUNT")
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
	if wallet.Balance < amount {
		return nil, fmt.Errorf("INSUFFICIENT_BALANCE")
	}

	return s.store.RequestPayout(ctx, userID, amount, payoutMethodID)
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

// Subscribe validates the tier exists and is active, then creates the subscription.
// Charges the subscriber wallet and credits the creator wallet.
func (s *Service) Subscribe(ctx context.Context, subscriberID, creatorID uuid.UUID, tierID uuid.UUID) (*postgres.Subscription, error) {
	if subscriberID == creatorID {
		return nil, fmt.Errorf("CANNOT_SUBSCRIBE_TO_SELF")
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

	return s.store.Subscribe(ctx, subscriberID, creatorID, tier.ID, tier.Name, tier.Price, tier.Currency)
}

// Unsubscribe cancels an active subscription.
func (s *Service) Unsubscribe(ctx context.Context, subscriberID, creatorID uuid.UUID) error {
	return s.store.Unsubscribe(ctx, subscriberID, creatorID)
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

	// Calculate earnings: 1 mill per 1000 view score = 0.001 per view score.
	const earningsRate = 0.001
	earnings := viewScoreTotal * earningsRate

	if earnings <= 0 {
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
		Amount:      earnings,
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

package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/client/razorpayx"
	"github.com/atpost/monetization-service/internal/payments"
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
	store          *postgres.Store
	rdb            *redis.Client
	creatorFundCfg CreatorFundConfig
	entitlementPub EntitlementPublisher

	// payoutsEnabled is the beta boundary at the service layer (plan
	// Phase 3C, MONETIZATION_PAYOUTS_ENABLED). Off: RequestPayout refuses
	// with PAYOUTS_NOT_ENABLED before any gate, and accrual against a
	// period with no budget row is tolerated (uncapped, warned once). On:
	// withdrawals run the full pipeline and a period with no budget row
	// refuses to accrue (ErrNoBudget), because an accrual is then a claim
	// on real money.
	payoutsEnabled bool
	// tdsSection is what a tds_ledger row is recorded under
	// (MONETIZATION_TDS_SECTION, default 194-O).
	tdsSection string
	// tdsApply mirrors MONETIZATION_TDS_APPLY (default false). Founder
	// decision, 12 Sep 2026: TDS is COMPUTED and RECORDED on every payout
	// (tds_ledger keeps the gross and the computed amount, so the yearly
	// threshold keeps accumulating) but is NOT DEDUCTED from the transfer
	// until a later tax module decides per user. Off: the request carries
	// tds_paise 0 and net = gross. On: the computed amount is withheld.
	tdsApply bool

	// The payout rail (plan Phase 4B/4C). payoutClient is nil unless
	// payouts are enabled AND all four RazorpayX credentials are set, in
	// which case main wires it with the webhook secret. bankKey is the
	// AES-256 key bank account numbers are stored under (Phase 4D); empty
	// means bank capture is refused.
	payoutClient  razorpayx.API
	webhookSecret string
	bankKey       []byte

	// tipBlocks answers "is there a block between sender and creator?"
	// before any tip touches the ledger (security sweep #16, tips.go). nil
	// means GRAPH_SERVICE_URL is unset, and every tip is refused.
	tipBlocks TipBlockChecker

	// members is the storage the membership rules (tiers, subscriptions,
	// entitlement; membership.go) use. The *postgres.Store in production;
	// an in-memory fake in tests (WithMembershipStore).
	members MembershipStore
	// now is the membership rules' clock; nil means time.Now (WithClock).
	now func() time.Time

	// Paid memberships on payments-service (membership_payments.go).
	// membershipPayments nil: the membership money routes answer 503.
	membershipPayments payments.API
	membershipLocalEnv bool
	membershipEvents   MembershipEventPublisher
	eligibility        EligibilitySources
	// profiles names creators and members on the membership reads
	// (membership_reads.go); nil answers ids only.
	profiles ProfileSource
}

func New(s *postgres.Store, rdb *redis.Client) *Service {
	svc := &Service{store: s, rdb: rdb, creatorFundCfg: DefaultCreatorFundConfig(), tdsSection: DefaultTDSSection}
	if s != nil {
		svc.members = s
	}
	return svc
}

// WithPayoutsEnabled opens or closes the withdrawal path. Defaults to
// closed. Returns the same Service for chaining.
func (s *Service) WithPayoutsEnabled(enabled bool) *Service {
	s.payoutsEnabled = enabled
	return s
}

// PayoutsEnabled reports whether the withdrawal path is open.
func (s *Service) PayoutsEnabled() bool { return s.payoutsEnabled }

// WithTDSSection sets the section TDS entries are recorded under. An
// empty value keeps the default.
func (s *Service) WithTDSSection(section string) *Service {
	if section != "" {
		s.tdsSection = section
	}
	return s
}

// WithTDSApply turns deduction at payout on or off. The calculation and
// the ledger record happen either way. Defaults to off.
func (s *Service) WithTDSApply(apply bool) *Service {
	s.tdsApply = apply
	return s
}

// TDSApply reports whether TDS is deducted from a payout.
func (s *Service) TDSApply() bool { return s.tdsApply }

// TDSSection is the configured TDS section (default 194-O).
func (s *Service) TDSSection() string {
	if s.tdsSection == "" {
		return DefaultTDSSection
	}
	return s.tdsSection
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

// GetWallet returns the creator ledger without mutating financial state. A
// creator with no recorded activity receives an explicit zero-value view; the
// first financial write (when separately launched) creates the durable row.
func (s *Service) GetWallet(ctx context.Context, userID uuid.UUID) (*postgres.Wallet, error) {
	wallet, err := s.store.GetWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	if wallet == nil {
		return &postgres.Wallet{
			UserID:      userID,
			Currency:    "INR",
			HasActivity: false,
		}, nil
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

// ChargeAndCredit atomically debits one wallet and credits another. It is used
// by internal service integrations that need monetization-service to remain the
// wallet ledger owner.
func (s *Service) ChargeAndCredit(ctx context.Context, fromUserID, toUserID uuid.UUID, amountPaise int64, description string) error {
	if fromUserID == uuid.Nil || toUserID == uuid.Nil {
		return fmt.Errorf("INVALID_USER")
	}
	if amountPaise <= 0 || amountPaise > maxAmountPaise {
		return fmt.Errorf("amount out of valid range: %w", ErrInvalidAmount)
	}
	if _, err := s.store.EnsureWallet(ctx, fromUserID); err != nil {
		return fmt.Errorf("ensure payer wallet: %w", err)
	}
	if _, err := s.store.EnsureWallet(ctx, toUserID); err != nil {
		return fmt.Errorf("ensure payee wallet: %w", err)
	}
	return s.store.ChargeAndCredit(ctx, fromUserID.String(), toUserID.String(), amountPaise, description)
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
//
// RequestPayout lives in payout.go (plan Phase 3A): the gated pipeline
// that ends in the payout_requests insert.

// Creator tiers, subscribe and unsubscribe live in membership.go; the
// subscription lifecycle in subscription.go; entitlement in entitlement.go.

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

// Admin wallet operations (freeze, unfreeze, rebuild) are in admin_console.go:
// each commits its audit row with the change.

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

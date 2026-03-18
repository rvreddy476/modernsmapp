package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

// FraudReview represents a fraud review queue entry.
type FraudReview struct {
	ID         uuid.UUID  `json:"id"`
	CreatorID  uuid.UUID  `json:"creator_id"`
	ReviewType string     `json:"review_type"` // self_subscription, velocity, new_creator_hold, manual
	RiskScore  int        `json:"risk_score"`
	Status     string     `json:"status"` // pending, investigating, cleared, action_taken
	Notes      *string    `json:"notes,omitempty"`
	ReviewerID *uuid.UUID `json:"reviewer_id,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Dispute represents a user-initiated dispute on a transaction.
type Dispute struct {
	ID              uuid.UUID  `json:"id"`
	UserID          uuid.UUID  `json:"user_id"`
	TransactionID   uuid.UUID  `json:"transaction_id"`
	Reason          string     `json:"reason"`
	Description     *string    `json:"description,omitempty"`
	Status          string     `json:"status"` // open, investigating, resolved_refund, resolved_denied
	ResolutionNotes *string    `json:"resolution_notes,omitempty"`
	ResolvedBy      *uuid.UUID `json:"resolved_by,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
}

// Refund represents a refund on a transaction.
type Refund struct {
	ID            uuid.UUID  `json:"id"`
	TransactionID uuid.UUID  `json:"transaction_id"`
	DisputeID     *uuid.UUID `json:"dispute_id,omitempty"`
	AmountPaise   int64      `json:"amount_paise"`
	Reason        string     `json:"reason"`
	Status        string     `json:"status"` // pending, processed, failed
	ProcessedAt   *time.Time `json:"processed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// ---------------------------------------------------------------------------
// Fraud Reviews
// ---------------------------------------------------------------------------

// CreateFraudReview inserts a new fraud review.
func (s *Store) CreateFraudReview(ctx context.Context, review *FraudReview) (*FraudReview, error) {
	if review.ID == uuid.Nil {
		review.ID = uuid.New()
	}
	review.CreatedAt = time.Now()

	_, err := s.db.Exec(ctx, `
		INSERT INTO fraud_reviews (id, creator_id, review_type, risk_score, status, notes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, review.ID, review.CreatorID, review.ReviewType, review.RiskScore, review.Status, review.Notes, review.CreatedAt)
	if err != nil {
		return nil, err
	}
	return review, nil
}

// ListPendingFraudReviews returns fraud reviews with status 'pending'.
func (s *Store) ListPendingFraudReviews(ctx context.Context, limit, offset int) ([]FraudReview, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, creator_id, review_type, risk_score, status, notes, reviewer_id, resolved_at, created_at
		FROM fraud_reviews
		WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reviews []FraudReview
	for rows.Next() {
		var r FraudReview
		if err := rows.Scan(
			&r.ID, &r.CreatorID, &r.ReviewType, &r.RiskScore, &r.Status,
			&r.Notes, &r.ReviewerID, &r.ResolvedAt, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		reviews = append(reviews, r)
	}
	return reviews, rows.Err()
}

// ResolveFraudReview updates the status of a fraud review.
func (s *Store) ResolveFraudReview(ctx context.Context, reviewID uuid.UUID, status, notes string, reviewerID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE fraud_reviews
		SET status = $2, notes = $3, reviewer_id = $4, resolved_at = NOW()
		WHERE id = $1
	`, reviewID, status, notes, reviewerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("FRAUD_REVIEW_NOT_FOUND")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Disputes
// ---------------------------------------------------------------------------

// CreateDispute inserts a new dispute.
func (s *Store) CreateDispute(ctx context.Context, dispute *Dispute) (*Dispute, error) {
	if dispute.ID == uuid.Nil {
		dispute.ID = uuid.New()
	}
	dispute.CreatedAt = time.Now()
	dispute.Status = "open"

	_, err := s.db.Exec(ctx, `
		INSERT INTO disputes (id, user_id, transaction_id, reason, description, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, dispute.ID, dispute.UserID, dispute.TransactionID, dispute.Reason, dispute.Description, dispute.Status, dispute.CreatedAt)
	if err != nil {
		return nil, err
	}
	return dispute, nil
}

// GetDispute returns a dispute by ID.
func (s *Store) GetDispute(ctx context.Context, disputeID uuid.UUID) (*Dispute, error) {
	var d Dispute
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, transaction_id, reason, description, status, resolution_notes, resolved_by, created_at, resolved_at
		FROM disputes
		WHERE id = $1
	`, disputeID).Scan(
		&d.ID, &d.UserID, &d.TransactionID, &d.Reason, &d.Description,
		&d.Status, &d.ResolutionNotes, &d.ResolvedBy, &d.CreatedAt, &d.ResolvedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

// ListUserDisputes returns disputes for a user with pagination.
func (s *Store) ListUserDisputes(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Dispute, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, transaction_id, reason, description, status, resolution_notes, resolved_by, created_at, resolved_at
		FROM disputes
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var disputes []Dispute
	for rows.Next() {
		var d Dispute
		if err := rows.Scan(
			&d.ID, &d.UserID, &d.TransactionID, &d.Reason, &d.Description,
			&d.Status, &d.ResolutionNotes, &d.ResolvedBy, &d.CreatedAt, &d.ResolvedAt,
		); err != nil {
			return nil, err
		}
		disputes = append(disputes, d)
	}
	return disputes, rows.Err()
}

// ListOpenDisputes returns disputes with status 'open' or 'investigating' (admin view).
func (s *Store) ListOpenDisputes(ctx context.Context, limit, offset int) ([]Dispute, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, transaction_id, reason, description, status, resolution_notes, resolved_by, created_at, resolved_at
		FROM disputes
		WHERE status IN ('open', 'investigating')
		ORDER BY created_at ASC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var disputes []Dispute
	for rows.Next() {
		var d Dispute
		if err := rows.Scan(
			&d.ID, &d.UserID, &d.TransactionID, &d.Reason, &d.Description,
			&d.Status, &d.ResolutionNotes, &d.ResolvedBy, &d.CreatedAt, &d.ResolvedAt,
		); err != nil {
			return nil, err
		}
		disputes = append(disputes, d)
	}
	return disputes, rows.Err()
}

// ResolveDispute updates the status and resolution of a dispute.
func (s *Store) ResolveDispute(ctx context.Context, disputeID uuid.UUID, status, notes string, resolvedBy uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE disputes
		SET status = $2, resolution_notes = $3, resolved_by = $4, resolved_at = NOW()
		WHERE id = $1
	`, disputeID, status, notes, resolvedBy)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("DISPUTE_NOT_FOUND")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Refunds
// ---------------------------------------------------------------------------

// CreateRefund inserts a new refund.
func (s *Store) CreateRefund(ctx context.Context, refund *Refund) (*Refund, error) {
	if refund.ID == uuid.Nil {
		refund.ID = uuid.New()
	}
	refund.CreatedAt = time.Now()

	_, err := s.db.Exec(ctx, `
		INSERT INTO refunds (id, transaction_id, dispute_id, amount_paise, reason, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, refund.ID, refund.TransactionID, refund.DisputeID, refund.AmountPaise, refund.Reason, refund.Status, refund.CreatedAt)
	if err != nil {
		return nil, err
	}
	return refund, nil
}

// GetRefundByTransaction returns a refund for a given transaction ID.
func (s *Store) GetRefundByTransaction(ctx context.Context, txnID uuid.UUID) (*Refund, error) {
	var r Refund
	err := s.db.QueryRow(ctx, `
		SELECT id, transaction_id, dispute_id, amount_paise, reason, status, processed_at, created_at
		FROM refunds
		WHERE transaction_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, txnID).Scan(
		&r.ID, &r.TransactionID, &r.DisputeID, &r.AmountPaise, &r.Reason,
		&r.Status, &r.ProcessedAt, &r.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// ---------------------------------------------------------------------------
// Admin wallet + operational queries
// ---------------------------------------------------------------------------

// FreezeWallet sets is_frozen=true for a user's wallet.
func (s *Store) FreezeWallet(ctx context.Context, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE wallets SET is_frozen = true, updated_at = NOW() WHERE user_id = $1
	`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("WALLET_NOT_FOUND")
	}
	return nil
}

// UnfreezeWallet sets is_frozen=false for a user's wallet.
func (s *Store) UnfreezeWallet(ctx context.Context, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE wallets SET is_frozen = false, updated_at = NOW() WHERE user_id = $1
	`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("WALLET_NOT_FOUND")
	}
	return nil
}

// GetStuckTransactions returns transactions with status 'pending' created before olderThan.
func (s *Store) GetStuckTransactions(ctx context.Context, olderThan time.Time) ([]Transaction, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at
		FROM transactions
		WHERE status = 'pending' AND created_at < $1
		ORDER BY created_at ASC
		LIMIT 500
	`, olderThan)
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

// GetStalePayouts returns payout requests with status 'in_flight' processed before olderThan.
func (s *Store) GetStalePayouts(ctx context.Context, olderThan time.Time) ([]PayoutRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, transaction_id, amount, currency, status, payout_method_id, requested_at
		FROM payout_requests
		WHERE status = 'in_flight' AND processed_at < $1
		ORDER BY requested_at ASC
		LIMIT 500
	`, olderThan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []PayoutRequest
	for rows.Next() {
		var r PayoutRequest
		if err := rows.Scan(
			&r.ID, &r.UserID, &r.TransactionID, &r.AmountPaise, &r.Currency, &r.Status,
			&r.payoutMethodID, &r.RequestedAt,
		); err != nil {
			return nil, err
		}
		requests = append(requests, r)
	}
	return requests, rows.Err()
}

// GetTransactionByID returns a single transaction by ID.
func (s *Store) GetTransactionByID(ctx context.Context, txnID uuid.UUID) (*Transaction, error) {
	var t Transaction
	err := s.db.QueryRow(ctx, `
		SELECT id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at
		FROM transactions
		WHERE id = $1
	`, txnID).Scan(
		&t.ID, &t.WalletID, &t.Type, &t.AmountPaise, &t.Currency,
		&t.Status, &t.ReferenceType, &t.ReferenceID, &t.Description, &t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// CreditWallet adds amount to a user's wallet balance (used for refunds).
func (s *Store) CreditWallet(ctx context.Context, userID uuid.UUID, amountPaise int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE wallets SET balance = balance + $2, updated_at = NOW() WHERE user_id = $1
	`, userID, amountPaise)
	return err
}

// RebuildWalletFromLedger recalculates a wallet balance from ledger entries.
// It sums all credits minus debits for accounts owned by the user.
func (s *Store) RebuildWalletFromLedger(ctx context.Context, userID uuid.UUID) (int64, error) {
	var balance int64
	err := s.db.QueryRow(ctx, `
		WITH user_accounts AS (
			SELECT id FROM accounts WHERE owner_id = $1 AND account_type = 'user_wallet'
		)
		SELECT COALESCE(
			(SELECT COALESCE(SUM(le.amount_paise), 0) FROM ledger_entries le JOIN user_accounts ua ON le.credit_account_id = ua.id), 0
		) - COALESCE(
			(SELECT COALESCE(SUM(le.amount_paise), 0) FROM ledger_entries le JOIN user_accounts ua ON le.debit_account_id = ua.id), 0
		)
	`, userID).Scan(&balance)
	if err != nil {
		return 0, err
	}

	// Update wallet balance to match ledger
	_, err = s.db.Exec(ctx, `
		UPDATE wallets SET balance = $2, updated_at = NOW() WHERE user_id = $1
	`, userID, balance)
	if err != nil {
		return 0, err
	}

	return balance, nil
}

// GetAllWallets returns all wallets (used by reconciliation worker).
func (s *Store) GetAllWallets(ctx context.Context, limit, offset int) ([]Wallet, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, balance, lifetime_earnings, pending_payout, currency, is_frozen, created_at, updated_at
		FROM wallets
		ORDER BY user_id
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var wallets []Wallet
	for rows.Next() {
		var w Wallet
		if err := rows.Scan(
			&w.UserID, &w.BalancePaise, &w.LifetimeEarningsPaise, &w.PendingPayoutPaise,
			&w.Currency, &w.IsFrozen, &w.CreatedAt, &w.UpdatedAt,
		); err != nil {
			return nil, err
		}
		wallets = append(wallets, w)
	}
	return wallets, rows.Err()
}

// GetLedgerBalanceForUser computes the net balance from ledger entries for a user's wallet accounts.
func (s *Store) GetLedgerBalanceForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	var balance int64
	err := s.db.QueryRow(ctx, `
		WITH user_accounts AS (
			SELECT id FROM accounts WHERE owner_id = $1 AND account_type = 'user_wallet'
		)
		SELECT COALESCE(
			(SELECT COALESCE(SUM(le.amount_paise), 0) FROM ledger_entries le JOIN user_accounts ua ON le.credit_account_id = ua.id), 0
		) - COALESCE(
			(SELECT COALESCE(SUM(le.amount_paise), 0) FROM ledger_entries le JOIN user_accounts ua ON le.debit_account_id = ua.id), 0
		)
	`, userID).Scan(&balance)
	return balance, err
}

package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PayoutBatch represents a batch of payouts sent to the payment provider.
type PayoutBatch struct {
	ID              uuid.UUID  `json:"id"`
	Status          string     `json:"status"`
	TotalPaise      int64      `json:"total_paise"`
	PayoutCount     int        `json:"payout_count"`
	ProviderBatchID *string    `json:"provider_batch_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	SettledAt       *time.Time `json:"settled_at,omitempty"`
}

// PayoutStatement represents a periodic earnings statement for a creator.
type PayoutStatement struct {
	ID                  uuid.UUID  `json:"id"`
	UserID              uuid.UUID  `json:"user_id"`
	PeriodStart         time.Time  `json:"period_start"`
	PeriodEnd           time.Time  `json:"period_end"`
	TotalEarningsPaise  int64      `json:"total_earnings_paise"`
	TotalDeductionsPaise int64     `json:"total_deductions_paise"`
	TotalPayoutPaise    int64      `json:"total_payout_paise"`
	PDFMediaID          *uuid.UUID `json:"pdf_media_id,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

// CreatePayoutBatch inserts a new payout batch.
func (s *Store) CreatePayoutBatch(ctx context.Context, batch *PayoutBatch) (*PayoutBatch, error) {
	if batch.ID == uuid.Nil {
		batch.ID = uuid.New()
	}
	batch.CreatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO payout_batches (id, status, total_paise, payout_count, provider_batch_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, batch.ID, batch.Status, batch.TotalPaise, batch.PayoutCount, batch.ProviderBatchID, batch.CreatedAt)
	if err != nil {
		return nil, err
	}
	return batch, nil
}

// GetPayoutBatch returns a payout batch by ID.
func (s *Store) GetPayoutBatch(ctx context.Context, batchID uuid.UUID) (*PayoutBatch, error) {
	var b PayoutBatch
	err := s.db.QueryRow(ctx, `
		SELECT id, status, total_paise, payout_count, provider_batch_id, created_at, settled_at
		FROM payout_batches
		WHERE id = $1
	`, batchID).Scan(&b.ID, &b.Status, &b.TotalPaise, &b.PayoutCount, &b.ProviderBatchID, &b.CreatedAt, &b.SettledAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

// AddPayoutToBatch assigns a payout request to a batch and marks it as batched.
func (s *Store) AddPayoutToBatch(ctx context.Context, requestID, batchID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payout_requests SET batch_id = $2, status = 'batched' WHERE id = $1
	`, requestID, batchID)
	return err
}

// SettlePayoutBatch marks a batch as settled and records the settlement time.
func (s *Store) SettlePayoutBatch(ctx context.Context, batchID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payout_batches SET status = 'settled', settled_at = NOW() WHERE id = $1
	`, batchID)
	return err
}

// FailPayoutBatch marks a batch as failed.
func (s *Store) FailPayoutBatch(ctx context.Context, batchID uuid.UUID, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payout_batches SET status = 'failed' WHERE id = $1
	`, batchID)
	return err
}

// CreatePayoutStatement inserts a new payout statement record.
func (s *Store) CreatePayoutStatement(ctx context.Context, stmt *PayoutStatement) (*PayoutStatement, error) {
	if stmt.ID == uuid.Nil {
		stmt.ID = uuid.New()
	}
	stmt.CreatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO payout_statements (id, user_id, period_start, period_end, total_earnings_paise, total_deductions_paise, total_payout_paise, pdf_media_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, stmt.ID, stmt.UserID, stmt.PeriodStart, stmt.PeriodEnd,
		stmt.TotalEarningsPaise, stmt.TotalDeductionsPaise, stmt.TotalPayoutPaise,
		stmt.PDFMediaID, stmt.CreatedAt)
	if err != nil {
		return nil, err
	}
	return stmt, nil
}

// ListPayoutStatements returns paginated payout statements for a user.
func (s *Store) ListPayoutStatements(ctx context.Context, userID uuid.UUID, limit, offset int) ([]PayoutStatement, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, period_start, period_end, total_earnings_paise, total_deductions_paise, total_payout_paise, pdf_media_id, created_at
		FROM payout_statements
		WHERE user_id = $1
		ORDER BY period_end DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stmts []PayoutStatement
	for rows.Next() {
		var st PayoutStatement
		if err := rows.Scan(&st.ID, &st.UserID, &st.PeriodStart, &st.PeriodEnd,
			&st.TotalEarningsPaise, &st.TotalDeductionsPaise, &st.TotalPayoutPaise,
			&st.PDFMediaID, &st.CreatedAt); err != nil {
			return nil, err
		}
		stmts = append(stmts, st)
	}
	return stmts, rows.Err()
}

// GetPayoutStatement returns a single payout statement by ID.
func (s *Store) GetPayoutStatement(ctx context.Context, stmtID uuid.UUID) (*PayoutStatement, error) {
	var st PayoutStatement
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, period_start, period_end, total_earnings_paise, total_deductions_paise, total_payout_paise, pdf_media_id, created_at
		FROM payout_statements
		WHERE id = $1
	`, stmtID).Scan(&st.ID, &st.UserID, &st.PeriodStart, &st.PeriodEnd,
		&st.TotalEarningsPaise, &st.TotalDeductionsPaise, &st.TotalPayoutPaise,
		&st.PDFMediaID, &st.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &st, nil
}

// SetPayoutRequestProviderRef sets the provider reference on a payout request.
func (s *Store) SetPayoutRequestProviderRef(ctx context.Context, requestID uuid.UUID, ref string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payout_requests SET provider_reference = $2 WHERE id = $1
	`, requestID, ref)
	return err
}

// SetPayoutRequestFailure marks a payout request as failed with a reason.
func (s *Store) SetPayoutRequestFailure(ctx context.Context, requestID uuid.UUID, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payout_requests SET status = 'failed', failure_reason = $2 WHERE id = $1
	`, requestID, reason)
	return err
}

// GetPayoutsForBatching returns approved payout requests ready to be batched.
func (s *Store) GetPayoutsForBatching(ctx context.Context, limit int) ([]PayoutRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, transaction_id, amount, currency, status, payout_method_id, requested_at
		FROM payout_requests
		WHERE status = 'approved'
		ORDER BY requested_at ASC
		LIMIT $1
	`, limit)
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

// GetPayoutRequestByProviderRef returns a payout request by its provider reference.
func (s *Store) GetPayoutRequestByProviderRef(ctx context.Context, ref string) (*PayoutRequest, error) {
	var r PayoutRequest
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, transaction_id, amount, currency, status, payout_method_id, requested_at
		FROM payout_requests
		WHERE provider_reference = $1
	`, ref).Scan(
		&r.ID, &r.UserID, &r.TransactionID, &r.AmountPaise, &r.Currency, &r.Status,
		&r.payoutMethodID, &r.RequestedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// CheckKYCVerified checks whether a creator_tax_profiles row exists with verified_at IS NOT NULL.
func (s *Store) CheckKYCVerified(ctx context.Context, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM creator_tax_profiles
			WHERE user_id = $1 AND verified_at IS NOT NULL
		)
	`, userID).Scan(&exists)
	return exists, err
}

// SumEarnings returns the total completed earning transactions for a user within a date range.
func (s *Store) SumEarnings(ctx context.Context, userID uuid.UUID, start, end time.Time) (int64, error) {
	var total int64
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM transactions
		WHERE wallet_id = $1 AND type = 'earning' AND status = 'completed'
		  AND created_at >= $2 AND created_at < $3
	`, userID, start, end).Scan(&total)
	return total, err
}

// SumDeductions returns the total completed deduction transactions (payout, tds, gst) for a user within a date range.
func (s *Store) SumDeductions(ctx context.Context, userID uuid.UUID, start, end time.Time) (int64, error) {
	var total int64
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM transactions
		WHERE wallet_id = $1 AND type IN ('payout', 'adjustment') AND status = 'completed'
		  AND created_at >= $2 AND created_at < $3
	`, userID, start, end).Scan(&total)
	return total, err
}

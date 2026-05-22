package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// FraudScore mirrors food.fraud_scores.
type FraudScore struct {
	ID         uuid.UUID `json:"id"`
	UserID     uuid.UUID `json:"user_id"`
	Signal     string    `json:"signal"`
	Score      float64   `json:"score"`
	Detail     []byte    `json:"detail,omitempty"`
	ComputedAt string    `json:"computed_at"`
}

// RecordFraudScore appends one signal row.
func (s *Store) RecordFraudScore(ctx context.Context, userID uuid.UUID, signal string, score float64, detail map[string]any) error {
	body, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO food.fraud_scores (user_id, signal, score, detail)
		VALUES ($1, $2, $3, $4)
	`, userID, signal, score, body)
	return err
}

// TopFraudUsersRow is one row in the admin "highest-risk users" view.
type TopFraudUsersRow struct {
	UserID     string  `json:"user_id"`
	TotalScore float64 `json:"total_score"`
	Signals    int     `json:"signals"`
}

// TopFraudUsers returns users with the highest aggregate score in the
// last `windowHours` hours. Admin queue uses this to triage.
func (s *Store) TopFraudUsers(ctx context.Context, windowHours, limit int) ([]TopFraudUsersRow, error) {
	if windowHours <= 0 {
		windowHours = 168 // 7 days
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id::text, SUM(score)::float8 AS total_score, COUNT(*)::int AS signals
		FROM food.fraud_scores
		WHERE computed_at > NOW() - ($1::int * INTERVAL '1 hour')
		GROUP BY user_id
		HAVING SUM(score) > 0
		ORDER BY total_score DESC
		LIMIT $2
	`, windowHours, limit)
	if err != nil {
		return nil, fmt.Errorf("top fraud users: %w", err)
	}
	defer rows.Close()
	var out []TopFraudUsersRow
	for rows.Next() {
		var r TopFraudUsersRow
		if err := rows.Scan(&r.UserID, &r.TotalScore, &r.Signals); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecentRefundsByUser is one of the fraud signals — customers who
// requested multiple refunds in a short window.
type RecentRefundsByUserRow struct {
	UserID         string  `json:"user_id"`
	RefundsCount   int     `json:"refunds_count"`
	TotalRefunded  float64 `json:"total_refunded"`
}

// CustomerCancellationsRow backs the cancellation_pattern signal —
// customers who cancelled a high count of orders recently.
type CustomerCancellationsRow struct {
	UserID            string `json:"user_id"`
	CancellationCount int    `json:"cancellation_count"`
}

// RecentCustomerCancellations returns users with >= 2 customer-initiated
// cancellations in the window. The >=2 floor keeps one-off legitimate
// cancellations out of the fraud queue.
func (s *Store) RecentCustomerCancellations(ctx context.Context, windowHours int) ([]CustomerCancellationsRow, error) {
	if windowHours <= 0 {
		windowHours = 336 // 14d
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id::text, COUNT(*)::int
		FROM food.orders
		WHERE status = 'CANCELLED_BY_CUSTOMER'
		  AND placed_at > NOW() - ($1::int * INTERVAL '1 hour')
		GROUP BY user_id
		HAVING COUNT(*) >= 2
		ORDER BY 2 DESC
		LIMIT 500
	`, windowHours)
	if err != nil {
		return nil, fmt.Errorf("recent customer cancellations: %w", err)
	}
	defer rows.Close()
	var out []CustomerCancellationsRow
	for rows.Next() {
		var r CustomerCancellationsRow
		if err := rows.Scan(&r.UserID, &r.CancellationCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RecentRefundsByUser(ctx context.Context, windowHours int) ([]RecentRefundsByUserRow, error) {
	if windowHours <= 0 {
		windowHours = 720 // 30d
	}
	rows, err := s.db.Query(ctx, `
		SELECT
			customer_id::text,
			COUNT(*)::int,
			COALESCE(SUM(amount), 0)::float8
		FROM food.refund_requests
		WHERE status IN ('approved','processed')
		  AND created_at > NOW() - ($1::int * INTERVAL '1 hour')
		GROUP BY customer_id
		ORDER BY 2 DESC
		LIMIT 500
	`, windowHours)
	if err != nil {
		return nil, fmt.Errorf("recent refunds by user: %w", err)
	}
	defer rows.Close()
	var out []RecentRefundsByUserRow
	for rows.Next() {
		var r RecentRefundsByUserRow
		if err := rows.Scan(&r.UserID, &r.RefundsCount, &r.TotalRefunded); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

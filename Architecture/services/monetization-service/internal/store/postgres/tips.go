package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Tip is one fan→creator transfer. Optionally anchored to a post
// (Super Thanks-style) or a live stream (Super Chat-style).
type Tip struct {
	ID            uuid.UUID  `json:"id"`
	SenderID      uuid.UUID  `json:"sender_id"`
	RecipientID   uuid.UUID  `json:"recipient_id"`
	AmountPaise   int64      `json:"amount_paise"`
	Currency      string     `json:"currency"`
	Message       string     `json:"message,omitempty"`
	PostID        *uuid.UUID `json:"post_id,omitempty"`
	StreamID      *uuid.UUID `json:"stream_id,omitempty"`
	Status        string     `json:"status"`
	FailureReason string     `json:"failure_reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// InsertTip records a tip row. Upstream (the service layer) is
// responsible for charging/crediting wallets and writing transaction
// rows in the same logical operation; this just records the
// fan-facing artefact.
func (s *Store) InsertTip(ctx context.Context, t *Tip) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	if t.Status == "" {
		t.Status = "completed"
	}
	if t.Currency == "" {
		t.Currency = "INR"
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO tips (id, sender_id, recipient_id, amount_paise, currency,
		                  message, post_id, stream_id, status, failure_reason, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`,
		t.ID, t.SenderID, t.RecipientID, t.AmountPaise, t.Currency,
		nullableTipString(t.Message), t.PostID, t.StreamID, t.Status,
		nullableTipString(t.FailureReason), t.CreatedAt,
	)
	return err
}

// GetTip returns one tip by ID, or nil if not found.
func (s *Store) GetTip(ctx context.Context, tipID uuid.UUID) (*Tip, error) {
	var t Tip
	err := s.db.QueryRow(ctx, `
		SELECT id, sender_id, recipient_id, amount_paise, currency,
		       COALESCE(message, ''), post_id, stream_id, status,
		       COALESCE(failure_reason, ''), created_at
		FROM tips WHERE id = $1
	`, tipID).Scan(
		&t.ID, &t.SenderID, &t.RecipientID, &t.AmountPaise, &t.Currency,
		&t.Message, &t.PostID, &t.StreamID, &t.Status,
		&t.FailureReason, &t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// ListTipsBySender returns the caller's outgoing tips, paginated by
// created_at descending (cursor = last seen created_at).
func (s *Store) ListTipsBySender(ctx context.Context, senderID uuid.UUID, cursor time.Time, limit int) ([]Tip, error) {
	return s.queryTips(ctx,
		`WHERE sender_id = $1 AND created_at < $2 AND status = 'completed'`,
		senderID, cursor, limit,
	)
}

// ListTipsByRecipient returns the creator's incoming tips.
func (s *Store) ListTipsByRecipient(ctx context.Context, recipientID uuid.UUID, cursor time.Time, limit int) ([]Tip, error) {
	return s.queryTips(ctx,
		`WHERE recipient_id = $1 AND created_at < $2 AND status = 'completed'`,
		recipientID, cursor, limit,
	)
}

// ListTipsForPost returns recent tips on a single post (Super Thanks
// view). Public — the post author chose to accept tips on it.
func (s *Store) ListTipsForPost(ctx context.Context, postID uuid.UUID, cursor time.Time, limit int) ([]Tip, error) {
	return s.queryTips(ctx,
		`WHERE post_id = $1 AND created_at < $2 AND status = 'completed'`,
		postID, cursor, limit,
	)
}

// SumTipsToRecipientSince is used by the daily-cap rate limiter +
// the creator dashboard "tips this month" tile. Returns total paise
// received by recipient since `since`.
func (s *Store) SumTipsToRecipientSince(ctx context.Context, recipientID uuid.UUID, since time.Time) (int64, error) {
	var total int64
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_paise), 0)::BIGINT
		 FROM tips
		 WHERE recipient_id = $1 AND created_at >= $2 AND status = 'completed'`,
		recipientID, since,
	).Scan(&total)
	return total, err
}

// SumTipsFromSenderToRecipientSince is used by the per-(sender,
// recipient) daily cap that prevents a single fan from pumping
// thousands of rupees at one creator over a few minutes.
func (s *Store) SumTipsFromSenderToRecipientSince(ctx context.Context, senderID, recipientID uuid.UUID, since time.Time) (int64, error) {
	var total int64
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_paise), 0)::BIGINT
		 FROM tips
		 WHERE sender_id = $1 AND recipient_id = $2
		   AND created_at >= $3 AND status = 'completed'`,
		senderID, recipientID, since,
	).Scan(&total)
	return total, err
}

func (s *Store) queryTips(ctx context.Context, where string, id uuid.UUID, cursor time.Time, limit int) ([]Tip, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, sender_id, recipient_id, amount_paise, currency,
		       COALESCE(message, ''), post_id, stream_id, status,
		       COALESCE(failure_reason, ''), created_at
		FROM tips
	`+where+`
		ORDER BY created_at DESC
		LIMIT $3
	`, id, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tips []Tip
	for rows.Next() {
		var t Tip
		if err := rows.Scan(
			&t.ID, &t.SenderID, &t.RecipientID, &t.AmountPaise, &t.Currency,
			&t.Message, &t.PostID, &t.StreamID, &t.Status,
			&t.FailureReason, &t.CreatedAt,
		); err != nil {
			return nil, err
		}
		tips = append(tips, t)
	}
	return tips, rows.Err()
}

func nullableTipString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

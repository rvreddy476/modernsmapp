package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// BumpRecipient records or refreshes a recipient entry for the user. Either
// recipientUserID or recipientPhone must be set (the schema PK uses
// COALESCE so the first non-null wins).
func (s *Store) BumpRecipient(ctx context.Context, userID uuid.UUID, recipientUserID *uuid.UUID, recipientPhone, label *string) error {
	if recipientUserID == nil && (recipientPhone == nil || *recipientPhone == "") {
		return fmt.Errorf("recipient: at least one of user_id / phone required")
	}
	const q = `
        INSERT INTO wallet.recipients (user_id, recipient_user_id, recipient_phone, label, last_sent_at, send_count)
        VALUES ($1, $2, $3, $4, now(), 1)
        ON CONFLICT (user_id, COALESCE(recipient_user_id::text, recipient_phone))
        DO UPDATE SET
            label = COALESCE(EXCLUDED.label, wallet.recipients.label),
            last_sent_at = now(),
            send_count = wallet.recipients.send_count + 1`
	if _, err := s.db.Exec(ctx, q, userID, recipientUserID, recipientPhone, label); err != nil {
		return fmt.Errorf("bump recipient: %w", err)
	}
	return nil
}

// ListRecipients returns the user's frequent recipients ordered by send_count
// DESC (top 20). Used by the home-screen "send again" UX.
func (s *Store) ListRecipients(ctx context.Context, userID uuid.UUID, limit int) ([]Recipient, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	const q = `
        SELECT user_id, recipient_user_id, recipient_phone, label, last_sent_at, send_count
        FROM wallet.recipients WHERE user_id = $1
        ORDER BY send_count DESC, last_sent_at DESC NULLS LAST
        LIMIT $2`
	rows, err := s.db.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recipients: %w", err)
	}
	defer rows.Close()
	var out []Recipient
	for rows.Next() {
		var r Recipient
		if err := rows.Scan(&r.UserID, &r.RecipientUserID, &r.RecipientPhone, &r.Label, &r.LastSentAt, &r.SendCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

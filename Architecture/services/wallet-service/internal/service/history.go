package service

import (
	"context"
	"fmt"

	"github.com/atpost/wallet-service/internal/store"
	"github.com/google/uuid"
)

// HistoryFilter drives ListHistory pagination + filtering. All fields
// optional; empty cursor = newest page.
type HistoryFilter struct {
	Type      string
	Direction string
	Cursor    string
	Limit     int
}

// HistoryPage is the structured response: items + the next cursor (empty
// string = no more pages).
type HistoryPage struct {
	Items      []store.Transaction `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

// ListHistory paginates the user's transactions. Cursor format is
// RFC3339Nano of the last item's created_at. Limit caps at 100.
func (s *Service) ListHistory(ctx context.Context, userID uuid.UUID, filter HistoryFilter) (*HistoryPage, error) {
	items, next, err := s.store.ListTransactions(ctx, userID, filter.Type, filter.Direction, filter.Cursor, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}
	return &HistoryPage{Items: items, NextCursor: next}, nil
}

// GetTransactionDetail returns one transaction scoped to a user.
func (s *Service) GetTransactionDetail(ctx context.Context, userID, txID uuid.UUID) (*store.Transaction, error) {
	return s.store.GetTransaction(ctx, userID, txID)
}

// ListFrequentRecipients fetches up to 20 of the user's most-used recipients.
func (s *Service) ListFrequentRecipients(ctx context.Context, userID uuid.UUID) ([]store.Recipient, error) {
	return s.store.ListRecipients(ctx, userID, 20)
}

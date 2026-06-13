package service

import (
	"context"
	"fmt"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// resolveOrderRole returns the role the user has on the order, or
// empty + an error when the user is unrelated. Admin override is
// handled at the handler layer via X-Scopes.
func (s *Service) resolveOrderRole(ctx context.Context, orderID, userID uuid.UUID) (string, error) {
	m, err := s.store.OrderPartyMembership(ctx, orderID, userID)
	if err != nil {
		return "", err
	}
	switch {
	case m.IsCustomer:
		return "customer", nil
	case m.IsRestaurantOwner:
		return "restaurant", nil
	case m.IsDeliveryPartner:
		return "delivery", nil
	}
	return "", fmt.Errorf("forbidden: not a party to this order")
}

// AppendOrderMessage resolves the author's role + appends the message.
// `isAdmin` lets the handler force role=admin without a party check.
func (s *Service) AppendOrderMessage(ctx context.Context, orderID, authorID uuid.UUID, body string, isAdmin bool) (*postgres.OrderMessage, error) {
	role := "admin"
	if !isAdmin {
		r, err := s.resolveOrderRole(ctx, orderID, authorID)
		if err != nil {
			return nil, err
		}
		role = r
	}
	m, err := s.store.AppendOrderMessage(ctx, orderID, authorID, role, body)
	if err != nil {
		return nil, err
	}
	s.publishRealtime(ctx, "food.order."+orderID.String(), "food.order.message", m)
	return m, nil
}

// ListOrderMessages returns the full thread. Visibility: party-only or
// admin override (X-Scopes).
func (s *Service) ListOrderMessages(ctx context.Context, orderID, viewerID uuid.UUID, isAdmin bool) ([]postgres.OrderMessage, error) {
	if !isAdmin {
		if _, err := s.resolveOrderRole(ctx, orderID, viewerID); err != nil {
			return nil, err
		}
	}
	return s.store.ListOrderMessages(ctx, orderID)
}

// MarkOrderMessageRead — recipient app calls this when the message is
// displayed. Idempotent at the store layer.
func (s *Service) MarkOrderMessageRead(ctx context.Context, messageID, viewerID uuid.UUID, role string) error {
	return s.store.MarkMessageRead(ctx, messageID, viewerID, role)
}

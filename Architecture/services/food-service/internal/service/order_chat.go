package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MaxMessageBodyRunes caps one order-chat or support-ticket message (audit
// L6). Only the gateway's 10 MB body limit applied before.
const MaxMessageBodyRunes = 2000

var (
	// ErrNotOrderParty: the caller is not the customer, the restaurant owner
	// or the rider inside the drop-off window of this order (audit M3).
	// HTTP 403 FOOD_ORDER_CHAT_FORBIDDEN.
	ErrNotOrderParty = errors.New("forbidden: not a party to this order")
	// ErrOrderMessageNotFound: the message does not exist on this order.
	// HTTP 404 FOOD_ORDER_MESSAGE_NOT_FOUND.
	ErrOrderMessageNotFound = errors.New("message not found on this order")
	// ErrMessageBodyInvalid: blank, or longer than MaxMessageBodyRunes.
	// HTTP 400 FOOD_MESSAGE_BODY_INVALID.
	ErrMessageBodyInvalid = errors.New("message body is required and at most 2000 characters")
)

// ValidateMessageBody is the one body rule for order chat and ticket
// messages: not blank, at most MaxMessageBodyRunes characters.
func ValidateMessageBody(body string) error {
	if strings.TrimSpace(body) == "" || utf8.RuneCountInString(body) > MaxMessageBodyRunes {
		return ErrMessageBodyInvalid
	}
	return nil
}

// resolveOrderRole returns the role the user has on the order, or
// ErrNotOrderParty when the user is unrelated. The role is always decided
// here, never taken from the client. Admins never reach this: they use the
// admin-service token routes (isAdmin=true from those handlers only).
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
	return "", ErrNotOrderParty
}

// AppendOrderMessage resolves the author's role + appends the message.
// isAdmin is set only by the admin-service token routes (audit M3).
func (s *Service) AppendOrderMessage(ctx context.Context, orderID, authorID uuid.UUID, body string, isAdmin bool) (*postgres.OrderMessage, error) {
	if err := ValidateMessageBody(body); err != nil {
		return nil, err
	}
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

// ListOrderMessages returns the full thread. Visibility: party-only, or an
// admin on the admin-service token routes.
func (s *Service) ListOrderMessages(ctx context.Context, orderID, viewerID uuid.UUID, isAdmin bool) ([]postgres.OrderMessage, error) {
	if !isAdmin {
		if _, err := s.resolveOrderRole(ctx, orderID, viewerID); err != nil {
			return nil, err
		}
	}
	return s.store.ListOrderMessages(ctx, orderID)
}

// MarkOrderMessageRead — recipient app calls this when the message is
// displayed. Idempotent at the store layer. The message must belong to
// orderID, and the caller must be a party to that order now; the role
// stamped is the server's (audit M3), whatever the client sends.
func (s *Service) MarkOrderMessageRead(ctx context.Context, orderID, messageID, viewerID uuid.UUID) error {
	msgOrder, err := s.store.OrderMessageOrderID(ctx, messageID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOrderMessageNotFound
		}
		return err
	}
	if msgOrder != orderID {
		return ErrOrderMessageNotFound
	}
	role, err := s.resolveOrderRole(ctx, orderID, viewerID)
	if err != nil {
		return err
	}
	return s.store.MarkMessageRead(ctx, orderID, messageID, viewerID, role)
}

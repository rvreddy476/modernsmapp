package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OrderMessage mirrors food.order_messages.
type OrderMessage struct {
	ID         uuid.UUID `json:"id"`
	OrderID    uuid.UUID `json:"order_id"`
	AuthorID   uuid.UUID `json:"author_id"`
	AuthorRole string    `json:"author_role"`
	Body       string    `json:"body"`
	ReadBy     []byte    `json:"read_by"`
	CreatedAt  string    `json:"created_at"`
}

// AppendOrderMessage authorizes via role check at the service layer
// and just inserts the row here.
func (s *Store) AppendOrderMessage(ctx context.Context, orderID, authorID uuid.UUID, authorRole, body string) (*OrderMessage, error) {
	if authorRole != "customer" && authorRole != "restaurant" && authorRole != "delivery" && authorRole != "admin" {
		return nil, fmt.Errorf("invalid role: %s", authorRole)
	}
	var m OrderMessage
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.order_messages (order_id, author_id, author_role, body)
		VALUES ($1, $2, $3, $4)
		RETURNING id, order_id, author_id, author_role, body, read_by, created_at::text
	`, orderID, authorID, authorRole, body).Scan(
		&m.ID, &m.OrderID, &m.AuthorID, &m.AuthorRole, &m.Body, &m.ReadBy, &m.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &m, nil
}

// ListOrderMessages returns the conversation in chronological order.
// Visibility check (party-only) lives at the service layer.
func (s *Store) ListOrderMessages(ctx context.Context, orderID uuid.UUID) ([]OrderMessage, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, order_id, author_id, author_role, body, read_by, created_at::text
		FROM food.order_messages
		WHERE order_id = $1
		ORDER BY created_at
	`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrderMessage
	for rows.Next() {
		var m OrderMessage
		if err := rows.Scan(&m.ID, &m.OrderID, &m.AuthorID, &m.AuthorRole,
			&m.Body, &m.ReadBy, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// OrderMessageOrderID is the order a chat message belongs to; pgx.ErrNoRows
// when the message does not exist. MarkOrderMessageRead resolves the
// caller's party role on THIS order before stamping a read (audit M3).
func (s *Store) OrderMessageOrderID(ctx context.Context, messageID uuid.UUID) (uuid.UUID, error) {
	var orderID uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT order_id FROM food.order_messages WHERE id = $1`, messageID).Scan(&orderID)
	return orderID, err
}

// MarkMessageRead appends a {role,user_id,at} triple to read_by. The
// JSONB array is treated as a set — duplicate (role,user_id) pairs are
// suppressed. The caller has already been resolved as a party to orderID
// (the role is the server's, never the client's); the message must belong
// to that order.
func (s *Store) MarkMessageRead(ctx context.Context, orderID, messageID, userID uuid.UUID, role string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE food.order_messages
		SET read_by = read_by || jsonb_build_array(
			jsonb_build_object('role', $2::text, 'user_id', $3::text, 'at', NOW()::text)
		)
		WHERE id = $1 AND order_id = $4
		  AND NOT EXISTS (
			SELECT 1 FROM jsonb_array_elements(read_by) e
			WHERE e->>'user_id' = $3::text
		  )
	`, messageID, role, userID.String(), orderID)
	return err
}

// OrderPartyMembership is the result of looking up "what role does
// this user have on this order?" — used for authz before append.
type OrderPartyMembership struct {
	IsCustomer        bool
	IsRestaurantOwner bool
	IsDeliveryPartner bool
}

// riderChatParty is the rider's side of the chat (audit M3): a rider is a
// party only while one of their assignments on the order is inside the
// drop-off privacy window (DropVisible: accepted, and the order still on its
// way). A rider who was released, reassigned, or has delivered the order no
// longer reads or writes the thread, which carries the customer's phone and
// gate codes.
func riderChatParty(assignmentStatuses []string, orderStatus string) bool {
	for _, st := range assignmentStatuses {
		if DropVisible(st, orderStatus) {
			return true
		}
	}
	return false
}

func (s *Store) OrderPartyMembership(ctx context.Context, orderID, userID uuid.UUID) (*OrderPartyMembership, error) {
	var m OrderPartyMembership
	var orderStatus string
	var riderStatuses []string
	err := s.db.QueryRow(ctx, `
		SELECT
			o.user_id = $2,
			EXISTS(SELECT 1 FROM food.restaurants r WHERE r.id = o.restaurant_id AND r.owner_user_id = $2),
			o.status::text,
			COALESCE(ARRAY(
				SELECT da.status::text FROM food.delivery_assignments da
				JOIN food.delivery_partners dp ON dp.id = da.delivery_partner_id
				WHERE da.order_id = o.id AND dp.user_id = $2
			), '{}')
		FROM food.orders o
		WHERE o.id = $1
	`, orderID, userID).Scan(&m.IsCustomer, &m.IsRestaurantOwner, &orderStatus, &riderStatuses)
	if errors.Is(err, pgx.ErrNoRows) {
		return &m, nil // no order: party to nothing
	}
	if err != nil {
		return nil, err
	}
	m.IsDeliveryPartner = riderChatParty(riderStatuses, orderStatus)
	return &m, nil
}

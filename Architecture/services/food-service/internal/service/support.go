package service

import (
	"context"
	"log/slog"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// CreateTicket records a customer-raised ticket and fans out an
// admin-side notification via the support topic.
func (s *Service) CreateTicket(ctx context.Context, in postgres.CreateTicketInput) (*postgres.Ticket, error) {
	t, err := s.store.CreateTicket(ctx, in)
	if err != nil {
		return nil, err
	}
	s.publishRealtime(ctx, "food.admin.support", "food.ticket.created", t)
	return t, nil
}

// ListMyTickets returns the caller's ticket inbox.
func (s *Service) ListMyTickets(ctx context.Context, customerID uuid.UUID) ([]postgres.Ticket, error) {
	return s.store.ListMyTickets(ctx, customerID)
}

// AppendTicketMessage adds a message to a ticket. Visibility:
// the customer who created the ticket OR an admin may post. isAdmin is set
// only by the admin-service token routes (audit M3), never from X-Scopes.
func (s *Service) AppendTicketMessage(ctx context.Context, ticketID, authorID uuid.UUID, isAdmin bool, body string) (*postgres.TicketMessage, error) {
	if err := ValidateMessageBody(body); err != nil {
		return nil, err
	}
	t, _, err := s.store.GetTicketWithMessages(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if !isAdmin && t.CustomerID != authorID {
		return nil, errForbidden
	}
	m, err := s.store.AppendTicketMessage(ctx, ticketID, authorID, isAdmin, body)
	if err != nil {
		return nil, err
	}
	s.publishRealtime(ctx, "food.ticket."+ticketID.String(), "food.ticket.message", m)
	return m, nil
}

// GetTicket returns the ticket + its conversation. Customer must be
// the owner OR an admin.
func (s *Service) GetTicket(ctx context.Context, viewerID uuid.UUID, ticketID uuid.UUID, isAdmin bool) (*postgres.Ticket, []postgres.TicketMessage, error) {
	t, msgs, err := s.store.GetTicketWithMessages(ctx, ticketID)
	if err != nil {
		return nil, nil, err
	}
	if !isAdmin && t.CustomerID != viewerID {
		return nil, nil, errForbidden
	}
	return t, msgs, nil
}

// AdminListTickets returns the support queue filtered by status.
func (s *Service) AdminListTickets(ctx context.Context, status string, limit int) ([]postgres.Ticket, error) {
	return s.store.ListTicketsForAdmin(ctx, status, limit)
}

// AdminListRefunds returns the refunds queue filtered by status.
func (s *Service) AdminListRefunds(ctx context.Context, status string, limit int) ([]postgres.RefundRequest, error) {
	return s.store.ListRefundsForAdmin(ctx, status, limit)
}

// SetTicketStatus is the admin transition.
func (s *Service) SetTicketStatus(ctx context.Context, adminID, ticketID uuid.UUID, status string) error {
	if err := s.store.SetTicketStatus(ctx, adminID, ticketID, status); err != nil {
		return err
	}
	s.publishRealtime(ctx, "food.ticket."+ticketID.String(), "food.ticket.status_changed", map[string]any{
		"ticket_id": ticketID.String(),
		"status":    status,
	})
	return nil
}

// CreateRefundRequest validates ownership and available balance transactionally.
func (s *Service) CreateRefundRequest(ctx context.Context, customerID, orderID uuid.UUID, ticketID *uuid.UUID, amount float64, reason string) (*postgres.RefundRequest, error) {
	r, err := s.store.CreateRefundRequest(ctx, customerID, orderID, ticketID, amount, reason)
	if err != nil {
		return nil, err
	}
	s.publishRealtime(ctx, "food.admin.refunds", "food.refund.requested", r)
	s.publishRealtime(ctx, "food.order."+orderID.String(), "food.refund.requested", r)
	return r, nil
}

// DecideRefund records the verdict and executable refund atomically. Submission
// failures are durable and retried by the existing refund worker; approval never
// means processed until the authoritative payment event settles the refund.
func (s *Service) DecideRefund(ctx context.Context, adminID, refundID uuid.UUID, status, reason string) error {
	if err := s.store.DecideRefund(ctx, adminID, refundID, status, reason); err != nil {
		return err
	}
	if status == "approved" {
		plan, err := s.store.SupportRefundPlan(ctx, refundID)
		if err == nil {
			_, err = s.submitRefundPlan(ctx, plan.OrderID, plan, reason)
		}
		if err != nil {
			slog.WarnContext(ctx, "food-service: approved support refund awaits retry", "refund_request_id", refundID, "error", err)
		}
	}
	eventType := "food.refund.approved"
	if status == "rejected" {
		eventType = "food.refund.rejected"
	}
	s.publishRealtime(ctx, "food.admin.refunds", eventType, map[string]any{
		"refund_id": refundID.String(),
		"status":    status,
	})
	return nil
}

var errForbidden = newServiceError("forbidden")

type serviceError struct{ msg string }

func (e *serviceError) Error() string          { return e.msg }
func newServiceError(msg string) *serviceError { return &serviceError{msg: msg} }

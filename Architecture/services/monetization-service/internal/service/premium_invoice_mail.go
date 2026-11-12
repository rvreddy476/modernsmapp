package service

import (
	"context"

	"github.com/google/uuid"
)

// PremiumInvoiceMail is what notification-service's invoice e-mail names
// (GET /v1/monetization/internal/premium/invoices/:id?user_id=). Only the
// facts the e-mail prints: no supplier block, no buyer name, no tax split.
// A credit note's total is negative.
type PremiumInvoiceMail struct {
	ID             uuid.UUID `json:"id"`
	UserID         uuid.UUID `json:"user_id"`
	Number         string    `json:"number"`
	Kind           string    `json:"kind"`
	Date           string    `json:"date"`
	PassID         uuid.UUID `json:"pass_id"`
	Description    string    `json:"description"`
	TotalPaise     int64     `json:"total_paise"`
	Currency       string    `json:"currency"`
	OriginalNumber string    `json:"original_number,omitempty"`
}

// InvoiceForMail answers one of userID's documents for the buyer's e-mail.
// The owner check is the same as the buyer's own route (ownInvoice): a
// document of another buyer is ErrPremiumInvoiceNotFound, so a mailer that
// pairs an event's user with someone else's invoice gets nothing to send.
func (p *Premium) InvoiceForMail(ctx context.Context, userID, id uuid.UUID) (*PremiumInvoiceMail, error) {
	inv, err := p.ownInvoice(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return &PremiumInvoiceMail{ID: inv.ID, UserID: inv.UserID, Number: inv.Number, Kind: inv.Kind, Date: inv.InvoiceDate,
		PassID: inv.PassID, Description: inv.Description, TotalPaise: inv.TotalPaise, Currency: inv.Currency,
		OriginalNumber: inv.OriginalNumber}, nil
}

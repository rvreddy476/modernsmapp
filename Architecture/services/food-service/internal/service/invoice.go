package service

import (
	"context"
	"fmt"

	"github.com/atpost/shared/invoice"
	"github.com/google/uuid"
)

// GenerateOrderInvoice produces a GST invoice HTML for an order. Two
// guarantees:
//
//   1. Idempotent — calling it twice for the same order returns the
//      same invoice_number (allocated lazily on first call and
//      persisted on food.orders).
//   2. Refuses non-DELIVERED orders — a draft / pending order has no
//      legitimate tax invoice.
func (s *Service) GenerateOrderInvoice(ctx context.Context, userID, orderID uuid.UUID) ([]byte, string, string, error) {
	d, err := s.store.GetInvoiceData(ctx, userID, orderID)
	if err != nil {
		return nil, "", "", err
	}
	fy := invoice.FinancialYear(d.PlacedAt)
	num := d.InvoiceNumber
	if num == "" {
		allocated, aerr := s.store.AllocateInvoiceNumber(ctx, orderID, fy)
		if aerr != nil {
			return nil, "", "", fmt.Errorf("allocate invoice number: %w", aerr)
		}
		num = allocated
	}

	inv := invoice.Invoice{
		Number:      num,
		Date:        d.PlacedAt,
		OrderNumber: d.OrderNumber,
		OrderDate:   d.PlacedAt,
		Seller: invoice.Party{
			Name:  d.RestaurantName,
			GSTIN: d.RestaurantGSTIN,
			Address: invoice.Address{
				Line1: d.RestaurantAddrLine,
				City:  d.RestaurantCity,
				State: d.RestaurantState,
			},
		},
		Buyer: invoice.Party{
			Name: d.BuyerName,
			Address: invoice.Address{
				Line1: d.BuyerAddrLine,
				City:  d.BuyerCity,
				State: d.BuyerState,
			},
		},
		ShipTo: invoice.Address{
			Line1: d.BuyerAddrLine,
			City:  d.BuyerCity,
			State: d.BuyerState,
		},
		Subtotal:        d.Subtotal,
		ShippingCharges: d.DeliveryFee + d.PackagingFee,
		CouponCode:      d.CouponCode,
		CouponDiscount:  d.CouponDiscount,
		GrandTotal:      d.GrandTotal,
		Currency:        "INR",
	}
	// Translate FiGo line items into invoice.LineItem. tax_percentage
	// is stored as a single number; the renderer splits it CGST+SGST
	// for intra-state and IGST for inter-state on its own.
	for _, it := range d.Items {
		taxable := it.LineTotal - it.TaxAmount
		half := it.TaxPct / 2
		li := invoice.LineItem{
			Title:     it.Name,
			HSN:       it.HSN,
			Quantity:  it.Quantity,
			UnitPrice: it.UnitPrice,
			Taxable:   taxable,
			CGSTPct:   half,
			SGSTPct:   half,
			IGSTPct:   it.TaxPct,
		}
		inv.Items = append(inv.Items, li)
	}
	inv.ApplyGST()
	body, ctype, err := invoice.HTMLRenderer{}.Render(inv)
	if err != nil {
		return nil, "", "", fmt.Errorf("render invoice: %w", err)
	}
	return body, ctype, num, nil
}

package service

import (
	"context"
	"fmt"

	"github.com/atpost/food-service/internal/foodinvoice"
	"github.com/atpost/shared/invoice"
	"github.com/google/uuid"
)

// GetOrderInvoice builds an order's tax invoice (Wave 1 B3) from the GST
// breakdown stored on the order, grouped by liable party:
//
//  1. Idempotent numbering: numbers are allocated on the first pull and
//     stored on food.orders; the platform section uses the platform series,
//     the restaurant section that restaurant's own series. An order placed
//     before B3 keeps its single legacy number.
//  2. Taxable value is the pre-tax line total. The legacy path used to
//     subtract the tax from a total that never contained it.
//  3. Every invoice carries "Tax rates pending adviser confirmation" while a
//     rate is unconfirmed.
func (s *Service) GetOrderInvoice(ctx context.Context, userID, orderID uuid.UUID) (*foodinvoice.Document, error) {
	d, err := s.store.GetInvoiceData(ctx, userID, orderID)
	if err != nil {
		return nil, err
	}
	fy := invoice.FinancialYear(d.PlacedAt)
	data := foodinvoice.Data{
		OrderID: orderID.String(), OrderNumber: d.OrderNumber, PlacedAt: d.PlacedAt,
		Restaurant: foodinvoice.Party{Name: d.RestaurantName, LegalName: d.RestaurantLegalName, GSTIN: d.RestaurantGSTIN,
			AddressLine: d.RestaurantAddrLine, City: d.RestaurantCity, State: d.RestaurantState},
		Buyer:            foodinvoice.Party{Name: d.BuyerName, AddressLine: d.BuyerAddrLine, City: d.BuyerCity, State: d.BuyerState},
		FinalAmountPaise: d.FinalAmountPaise,
	}
	if d.Breakdown == nil {
		num := d.InvoiceNumber
		if num == "" {
			if num, err = s.store.AllocateInvoiceNumber(ctx, orderID, fy); err != nil {
				return nil, fmt.Errorf("allocate invoice number: %w", err)
			}
		}
		data.LegacyInvoiceNumber = num
		for _, it := range d.Items {
			data.Legacy = append(data.Legacy, foodinvoice.LegacyItem{Name: it.Name, HSN: it.HSN, Quantity: int64(it.Quantity),
				UnitPricePaise: it.UnitPricePaise, LineTotalPaise: it.LineTotalPaise, TaxPaise: it.TaxAmountPaise, TaxPercent: it.TaxPct})
		}
		return foodinvoice.Build(data)
	}
	needPlatform, needRestaurant := false, false
	for _, l := range d.Breakdown.Lines {
		if l.LiableParty == foodinvoice.IssuerRestaurant {
			needRestaurant = true
		} else {
			needPlatform = true
		}
	}
	platformNo, restaurantNo := d.PlatformInvoiceNumber, d.RestaurantInvoiceNumber
	if (needPlatform && platformNo == "") || (needRestaurant && restaurantNo == "") {
		if platformNo, restaurantNo, err = s.store.AllocateOrderInvoiceNumbers(ctx, orderID, d.RestaurantID, fy, needPlatform, needRestaurant); err != nil {
			return nil, fmt.Errorf("allocate invoice numbers: %w", err)
		}
	}
	data.Breakdown = d.Breakdown
	data.PlatformInvoiceNumber, data.RestaurantInvoiceNumber = platformNo, restaurantNo
	data.Descriptions = make(map[string]foodinvoice.LineDescription, len(d.Lines))
	for ref, l := range d.Lines {
		data.Descriptions[ref] = foodinvoice.LineDescription{Description: l.Name, Quantity: l.Quantity, UnitPricePaise: l.UnitPricePaise}
	}
	return foodinvoice.Build(data)
}

// PrimaryInvoiceNumber is the X-Invoice-Number header value: the platform
// section's number when there is one, otherwise the restaurant's.
func PrimaryInvoiceNumber(doc *foodinvoice.Document) string {
	for _, issuer := range []string{foodinvoice.IssuerPlatform, foodinvoice.IssuerRestaurant} {
		if n := SectionInvoiceNumber(doc, issuer); n != "" {
			return n
		}
	}
	return ""
}

// SectionInvoiceNumber is the invoice number of one issuer's section, or "".
func SectionInvoiceNumber(doc *foodinvoice.Document, issuer string) string {
	for _, sec := range doc.Sections {
		if sec.Issuer == issuer && sec.InvoiceNumber != "" {
			return sec.InvoiceNumber
		}
	}
	return ""
}

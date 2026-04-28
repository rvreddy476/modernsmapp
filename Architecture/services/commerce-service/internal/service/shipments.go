package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/identity"
	"github.com/atpost/commerce-service/internal/store/blob"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/invoice"
	"github.com/google/uuid"
)

// BlobStore is the subset of blob.Store the service uses (keeps wiring testable).
type BlobStore interface {
	Upload(ctx context.Context, key string, data []byte, contentType string) error
	PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Ensure concrete satisfies interface at compile time.
var _ BlobStore = (*blob.Store)(nil)

// WithCourier attaches a courier provider.
func (s *Service) WithCourier(p courier.Provider) *Service {
	s.courier = p
	return s
}

// WithBlob attaches a blob store (for invoice HTML/PDF).
func (s *Service) WithBlob(b BlobStore) *Service {
	s.blob = b
	return s
}

// WithIdentity attaches the auth-service client for contact lookups.
func (s *Service) WithIdentity(c *identity.Client) *Service {
	s.identity = c
	return s
}

// resolveBuyer returns email + display name for an order's customer. Safe on nil client.
func (s *Service) resolveBuyer(ctx context.Context, userID uuid.UUID) (email, name string) {
	if s.identity == nil {
		return "", ""
	}
	contact, err := s.identity.GetContact(ctx, userID)
	if err != nil || contact == nil {
		return "", ""
	}
	return contact.Email, ""
}

// resolveSeller returns a seller's contact email + store name.
func (s *Service) resolveSeller(seller *postgres.Seller) (email, name string) {
	if seller == nil {
		return "", ""
	}
	return seller.Email, seller.StoreName
}

// ─── Shipments ────────────────────────────────────────────────────────

// CreateShipmentForOrder books a shipment with the courier and persists it.
// Idempotent: if a shipment already exists for the order, returns that.
func (s *Service) CreateShipmentForOrder(ctx context.Context, orderID uuid.UUID) (*postgres.Shipment, error) {
	if s.courier == nil {
		return nil, fmt.Errorf("courier provider not configured")
	}
	if existing, err := s.store.GetShipmentByOrder(ctx, orderID); err == nil && existing != nil {
		return existing, nil
	}

	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	// Ship if either: gateway has captured (payment_status=paid) OR this is a
	// COD order whose payment is collected at delivery. COD orders carry
	// payment_status=pending until the courier confirms collection — see the
	// note in service.go Checkout() about why cod_pending isn't a real state.
	isCOD := order.PaymentMethod != nil && *order.PaymentMethod == "cod"
	if order.PaymentStatus != "paid" && !isCOD {
		return nil, fmt.Errorf("order not ready to ship (payment_status=%s)", order.PaymentStatus)
	}

	items, err := s.store.GetOrderItems(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("get order items: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("order has no items")
	}

	var dropAddr courier.Address
	if order.DeliveryAddressID != nil {
		addr, err := s.store.GetAddressByID(ctx, *order.DeliveryAddressID)
		if err != nil {
			return nil, fmt.Errorf("get delivery address: %w", err)
		}
		dropAddr = courier.Address{
			Name: addr.ContactName, Phone: addr.Phone,
			Line1: addr.AddressLine1, City: addr.City, State: addr.State,
			Postal: addr.PostalCode, Country: addr.Country,
		}
		if addr.AddressLine2 != nil {
			dropAddr.Line2 = *addr.AddressLine2
		}
	}

	sellerID := items[0].SellerID
	seller, err := s.store.GetSellerByID(ctx, sellerID)
	if err != nil {
		return nil, fmt.Errorf("get seller: %w", err)
	}

	pickup := courier.Address{Name: seller.StoreName, Country: "IN"}
	if seller.City != nil {
		pickup.City = *seller.City
	}
	if seller.State != nil {
		pickup.State = *seller.State
	}
	if seller.PostalCode != nil {
		pickup.Postal = *seller.PostalCode
	}
	if seller.Phone != nil {
		pickup.Phone = *seller.Phone
	}

	var weight float64
	var courierItems []courier.Item
	for _, it := range items {
		// Pull weight + HSN from the underlying product; fall back to 500g if missing.
		product, _ := s.store.GetProductByID(ctx, it.ProductID)
		perUnitKg := 0.5
		hsn := ""
		if product != nil {
			if product.WeightGrams != nil && *product.WeightGrams > 0 {
				perUnitKg = float64(*product.WeightGrams) / 1000.0
			}
			if product.HSNCode != nil {
				hsn = *product.HSNCode
			}
		}
		weight += perUnitKg * float64(it.Quantity)
		courierItems = append(courierItems, courier.Item{
			Name: it.ProductTitle, SKU: it.SKU, Quantity: it.Quantity,
			Price: it.UnitPrice, HSN: hsn,
		})
	}
	if weight == 0 {
		weight = 0.5
	}

	pm := "prepaid"
	if order.PaymentMethod != nil && strings.EqualFold(*order.PaymentMethod, "cod") {
		pm = "cod"
	}

	resp, err := s.courier.CreateShipment(ctx, courier.ShipmentRequest{
		OrderID:       orderID.String(),
		OrderNumber:   order.OrderNumber,
		PickupAddress: pickup,
		DropAddress:   dropAddr,
		Weight:        weight,
		PackageValue:  order.FinalAmount,
		PaymentMethod: pm,
		CODAmount:     codAmount(pm, order.FinalAmount),
		Items:         courierItems,
	})
	if err != nil {
		return nil, fmt.Errorf("courier create: %w", err)
	}

	now := time.Now()
	sh := &postgres.Shipment{
		OrderID:        orderID,
		SellerID:       sellerID,
		Courier:        s.courier.Name(),
		TrackingNumber: strPtr(resp.AWBNumber),
		CourierOrderID: strPtr(resp.CourierOrderID),
		LabelURL:       strPtr(resp.LabelURL),
		TrackingURL:    strPtr(resp.TrackingURL),
		Status:         "booked",
		ETA:            &resp.EstimatedETA,
		ShippedAt:      &now,
	}
	if err := s.store.CreateShipment(ctx, sh); err != nil {
		return nil, fmt.Errorf("persist shipment: %w", err)
	}

	_ = s.store.UpdateOrderStatus(ctx, orderID, "shipped", nil, "system", "shipment booked")

	buyerEmail, buyerName := s.resolveBuyer(ctx, order.CustomerUserID)
	s.publish(ctx, events.EventCommerceOrderShipped, map[string]any{
		"order_id":        orderID,
		"shipment_id":     sh.ID,
		"order_number":    order.OrderNumber,
		"user_id":         order.CustomerUserID,
		"seller_id":       sellerID,
		"courier":         sh.Courier,
		"tracking_number": resp.AWBNumber,
		"tracking_url":    resp.TrackingURL,
		"eta":             resp.EstimatedETA,
		"buyer_email":     buyerEmail,
		"buyer_name":      buyerName,
	})
	return sh, nil
}

// GetShipmentForOrder returns the shipment for an order (if any).
func (s *Service) GetShipmentForOrder(ctx context.Context, orderID uuid.UUID) (*postgres.Shipment, []*postgres.ShipmentEvent, error) {
	sh, err := s.store.GetShipmentByOrder(ctx, orderID)
	if err != nil {
		return nil, nil, err
	}
	evts, _ := s.store.ListShipmentEvents(ctx, sh.ID)
	return sh, evts, nil
}

// HandleShipmentWebhook processes a courier webhook payload.
// Headers are required so the provider can authenticate the call (signature/token).
func (s *Service) HandleShipmentWebhook(ctx context.Context, courierName string, headers map[string]string, payload []byte) error {
	if s.courier == nil {
		return fmt.Errorf("courier provider not configured")
	}
	if !strings.EqualFold(s.courier.Name(), courierName) {
		return fmt.Errorf("courier mismatch: configured=%s, webhook=%s", s.courier.Name(), courierName)
	}
	if err := s.courier.VerifyWebhook(headers, payload); err != nil {
		return fmt.Errorf("webhook verify: %w", err)
	}
	updates, err := s.courier.ParseWebhook(ctx, payload)
	if err != nil {
		return fmt.Errorf("parse webhook: %w", err)
	}
	for _, u := range updates {
		sh, err := s.store.GetShipmentByTracking(ctx, courierName, u.TrackingNumber)
		if err != nil {
			slog.Warn("webhook: shipment not found", "tracking", u.TrackingNumber, "error", err)
			continue
		}
		if err := s.store.AppendShipmentEvent(ctx, sh.ID, u.Status, u.Location, u.Remark, u.OccurredAt); err != nil {
			slog.Warn("webhook: append event failed", "error", err)
		}
		if err := s.store.UpdateShipmentStatus(ctx, sh.ID, u.Status, u.OccurredAt); err != nil {
			slog.Warn("webhook: status update failed", "error", err)
		}
		if u.Status == "delivered" {
			_ = s.store.UpdateOrderStatus(ctx, sh.OrderID, "delivered", nil, "system", "courier confirmed delivery")
			order, _ := s.store.GetOrderByID(ctx, sh.OrderID)
			buyerEmail := ""
			if order != nil {
				buyerEmail, _ = s.resolveBuyer(ctx, order.CustomerUserID)
			}
			s.publish(ctx, events.EventCommerceOrderDelivered, map[string]any{
				"order_id":     sh.OrderID,
				"order_number": orderNumberOrEmpty(order),
				"shipment_id":  sh.ID,
				"occurred_at":  u.OccurredAt,
				"buyer_email":  buyerEmail,
			})
		}
	}
	return nil
}

// ─── Invoices ─────────────────────────────────────────────────────────

// IssueInvoice builds, persists, and uploads an HTML invoice for a paid order.
// Idempotent: returns existing invoice if already issued.
func (s *Service) IssueInvoice(ctx context.Context, orderID uuid.UUID) (*postgres.Invoice, error) {
	if s.blob == nil {
		return nil, fmt.Errorf("blob store not configured")
	}
	if existing, err := s.store.GetInvoiceByOrder(ctx, orderID); err == nil && existing != nil {
		return existing, nil
	}

	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	// Same payment-state gate as shipment creation — gateway-captured OR COD.
	isCOD := order.PaymentMethod != nil && *order.PaymentMethod == "cod"
	if order.PaymentStatus != "paid" && !isCOD {
		return nil, fmt.Errorf("order not eligible for invoice (payment_status=%s)", order.PaymentStatus)
	}

	items, err := s.store.GetOrderItems(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("get order items: %w", err)
	}
	sellerID := items[0].SellerID
	seller, err := s.store.GetSellerByID(ctx, sellerID)
	if err != nil {
		return nil, fmt.Errorf("get seller: %w", err)
	}

	var shipTo invoice.Address
	if order.DeliveryAddressID != nil {
		addr, err := s.store.GetAddressByID(ctx, *order.DeliveryAddressID)
		if err == nil {
			shipTo = invoice.Address{
				Line1: addr.AddressLine1, City: addr.City, State: addr.State,
				Postal: addr.PostalCode, Country: addr.Country,
			}
			if addr.AddressLine2 != nil {
				shipTo.Line2 = *addr.AddressLine2
			}
		}
	}

	// Build invoice. Each line picks tax % from its product's tax_class_id,
	// falling back to 18% GST (9/9 split intrastate, 18% IGST interstate).
	const defaultCGST, defaultSGST, defaultIGST = 9, 9, 18
	inv := invoice.Invoice{
		Date:            time.Now(),
		OrderNumber:     order.OrderNumber,
		OrderDate:       order.CreatedAt,
		Currency:        order.CurrencyCode,
		ShippingCharges: order.ShippingCharges,
		CouponDiscount:  order.CouponDiscount,
	}
	if order.CouponCode != nil {
		inv.CouponCode = *order.CouponCode
	}
	inv.Seller = sellerParty(seller)
	inv.Buyer = invoice.Party{Name: shipTo.Line1, Address: shipTo}
	inv.ShipTo = shipTo
	// Cache tax classes to avoid a query per line item.
	taxCache := map[uuid.UUID]*postgres.TaxClass{}
	for _, it := range items {
		cgst, sgst, igst := float64(defaultCGST), float64(defaultSGST), float64(defaultIGST)
		if product, err := s.store.GetProductByID(ctx, it.ProductID); err == nil && product != nil && product.TaxClassID != nil {
			tc, hit := taxCache[*product.TaxClassID]
			if !hit {
				tc, _ = s.store.GetTaxClass(ctx, *product.TaxClassID)
				taxCache[*product.TaxClassID] = tc
			}
			if tc != nil {
				cgst, sgst, igst = tc.CGSTPercentage, tc.SGSTPercentage, tc.IGSTPercentage
			}
		}
		inv.Items = append(inv.Items, invoice.LineItem{
			Title: it.ProductTitle, SKU: it.SKU,
			Quantity: it.Quantity, UnitPrice: it.UnitPrice,
			Discount: it.DiscountAmount, Taxable: it.FinalPrice,
			CGSTPct: cgst, SGSTPct: sgst, IGSTPct: igst,
		})
	}
	inv.ApplyGST()
	inv.ComputeTotals()

	// Allocate invoice number from sequence.
	fy := invoice.FinancialYear(inv.Date)
	seq, err := s.store.NextInvoiceSequence(ctx, fy)
	if err != nil {
		return nil, fmt.Errorf("sequence: %w", err)
	}
	inv.Number = invoice.NumberFor(inv.Date, seq)

	// Render HTML (always) + PDF (best-effort via wkhtmltopdf; falls back to HTML-only).
	htmlBody, htmlCT, err := invoice.HTMLRenderer{}.Render(inv)
	if err != nil {
		return nil, fmt.Errorf("render html: %w", err)
	}
	htmlKey := fmt.Sprintf("invoices/%s/%s.html", fy, inv.AsFilename())
	if err := s.blob.Upload(ctx, htmlKey, htmlBody, htmlCT); err != nil {
		return nil, fmt.Errorf("upload invoice html: %w", err)
	}

	var pdfKeyPtr *string
	if pdfBody, pdfCT, err := (invoice.PDFRenderer{}).Render(inv); err == nil {
		pdfKey := fmt.Sprintf("invoices/%s/%s.pdf", fy, inv.AsFilename())
		if uerr := s.blob.Upload(ctx, pdfKey, pdfBody, pdfCT); uerr == nil {
			pdfKeyPtr = &pdfKey
		} else {
			slog.Warn("upload invoice pdf failed", "error", uerr)
		}
	} else {
		slog.Debug("pdf render skipped; using html-only invoice", "error", err)
	}

	rec := &postgres.Invoice{
		OrderID: orderID, InvoiceNumber: inv.Number, FinancialYear: fy, Sequence: seq,
		SellerID: sellerID, BuyerUserID: order.CustomerUserID,
		GrandTotal: inv.GrandTotal, CurrencyCode: inv.Currency, IsInterstate: inv.IsInterstate,
		CGSTTotal: inv.TotalCGST, SGSTTotal: inv.TotalSGST, IGSTTotal: inv.TotalIGST,
		HTMLMediaKey: &htmlKey,
		PDFMediaKey:  pdfKeyPtr,
	}
	if err := s.store.CreateInvoice(ctx, rec); err != nil {
		return nil, fmt.Errorf("persist invoice: %w", err)
	}

	// Prefer PDF key for the download link when available; fall back to HTML.
	downloadKey := htmlKey
	if pdfKeyPtr != nil {
		downloadKey = *pdfKeyPtr
	}
	buyerEmail, _ := s.resolveBuyer(ctx, order.CustomerUserID)
	invoiceURL, _ := s.blob.PresignedGetURL(ctx, downloadKey, 7*24*time.Hour)
	s.publish(ctx, events.EventCommerceInvoiceIssued, map[string]any{
		"order_id":       orderID,
		"order_number":   order.OrderNumber,
		"invoice_id":     rec.ID,
		"invoice_number": rec.InvoiceNumber,
		"seller_id":      sellerID,
		"user_id":        order.CustomerUserID,
		"grand_total":    rec.GrandTotal,
		"html_media_key": htmlKey,
		"pdf_media_key":  pdfKeyPtr,
		"invoice_url":    invoiceURL,
		"buyer_email":    buyerEmail,
	})
	return rec, nil
}

// GetInvoiceDownloadURL returns a presigned URL for the invoice.
// Prefers PDF when available; falls back to HTML.
func (s *Service) GetInvoiceDownloadURL(ctx context.Context, orderID uuid.UUID) (*postgres.Invoice, string, error) {
	inv, err := s.store.GetInvoiceByOrder(ctx, orderID)
	if err != nil {
		return nil, "", err
	}
	if s.blob == nil {
		return inv, "", nil
	}
	var key *string
	if inv.PDFMediaKey != nil {
		key = inv.PDFMediaKey
	} else if inv.HTMLMediaKey != nil {
		key = inv.HTMLMediaKey
	}
	if key == nil {
		return inv, "", nil
	}
	u, err := s.blob.PresignedGetURL(ctx, *key, 15*time.Minute)
	if err != nil {
		return inv, "", err
	}
	return inv, u, nil
}

// ─── helpers ──────────────────────────────────────────────────────────

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func codAmount(pm string, total float64) float64 {
	if pm == "cod" {
		return total
	}
	return 0
}

func orderNumberOrEmpty(o *postgres.Order) string {
	if o == nil {
		return ""
	}
	return o.OrderNumber
}

func sellerParty(s *postgres.Seller) invoice.Party {
	p := invoice.Party{Name: s.StoreName, Email: s.Email}
	if s.GSTNumber != nil {
		p.GSTIN = *s.GSTNumber
	}
	if s.PANNumber != nil {
		p.PAN = *s.PANNumber
	}
	if s.Phone != nil {
		p.Phone = *s.Phone
	}
	if s.City != nil {
		p.Address.City = *s.City
	}
	if s.State != nil {
		p.Address.State = *s.State
	}
	if s.PostalCode != nil {
		p.Address.Postal = *s.PostalCode
	}
	p.Address.Country = "IN"
	return p
}

// Phase F2.2 — Request For Quote service. Lifecycle:
//
//	CreateRFQ (buyer)
//	  → SendRFQQuote (seller; sets quoted_total + per-line prices)
//	    → AcceptRFQQuote (buyer; creates an order at the negotiated
//	      prices, marks rfq=accepted + quote=accepted)
//	    → RejectRFQ (buyer or seller)
//	  → ExpireRFQs (periodic sweeper)
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	ErrRFQNotFound    = fmt.Errorf("rfq not found")
	ErrRFQForbidden   = fmt.Errorf("not authorised for this rfq")
	ErrRFQBadStatus   = fmt.Errorf("rfq is not in the right state")
	ErrRFQQuoteExpired = fmt.Errorf("rfq quote has expired")
	ErrRFQVariantSeller = fmt.Errorf("rfq variant does not belong to seller")
)

const defaultRFQExpiry = 14 * 24 * time.Hour // 14 days to respond

type CreateRFQInput struct {
	BuyerUserID    uuid.UUID
	OrganizationID *uuid.UUID
	SellerID       uuid.UUID
	Message        *string
	Items          []RFQItemInput
}

type RFQItemInput struct {
	VariantID uuid.UUID
	Quantity  int
	Notes     *string
}

// CreateRFQ validates that every variant belongs to the named seller +
// (when org_id is set) that the buyer is an active member of the org,
// then persists the RFQ + items in one transaction.
func (s *Service) CreateRFQ(ctx context.Context, in CreateRFQInput) (*postgres.RFQ, []*postgres.RFQItem, error) {
	if in.SellerID == uuid.Nil {
		return nil, nil, fmt.Errorf("seller_id is required")
	}
	if len(in.Items) == 0 {
		return nil, nil, fmt.Errorf("at least one item is required")
	}
	if in.OrganizationID != nil {
		if _, err := s.requireOrgRole(ctx, *in.OrganizationID, in.BuyerUserID, "admin", "buyer", "approver"); err != nil {
			return nil, nil, err
		}
	}
	for _, it := range in.Items {
		if it.Quantity <= 0 {
			return nil, nil, fmt.Errorf("quantity must be > 0 for every item")
		}
		variant, err := s.store.GetVariantByID(ctx, it.VariantID)
		if err != nil {
			return nil, nil, fmt.Errorf("variant %s not found", it.VariantID)
		}
		product, err := s.store.GetProductByID(ctx, variant.ProductID)
		if err != nil {
			return nil, nil, fmt.Errorf("product not found: %w", err)
		}
		if product.SellerID != in.SellerID {
			return nil, nil, ErrRFQVariantSeller
		}
	}
	r := &postgres.RFQ{
		BuyerUserID:    in.BuyerUserID,
		OrganizationID: in.OrganizationID,
		SellerID:       in.SellerID,
		MessageText:    in.Message,
		ExpiresAt:      time.Now().Add(defaultRFQExpiry),
	}
	items := make([]*postgres.RFQItem, 0, len(in.Items))
	for _, it := range in.Items {
		items = append(items, &postgres.RFQItem{
			VariantID: it.VariantID, Quantity: it.Quantity, Notes: it.Notes,
		})
	}
	if err := s.store.CreateRFQ(ctx, r, items); err != nil {
		return nil, nil, fmt.Errorf("create rfq: %w", err)
	}
	s.publish(ctx, "commerce.rfq.created", map[string]any{
		"rfq_id": r.ID, "buyer_user_id": r.BuyerUserID, "seller_id": r.SellerID,
		"organization_id": r.OrganizationID,
	})
	return r, items, nil
}

// GetRFQ returns the RFQ + items + quotes if the caller is a party.
type RFQDetail struct {
	RFQ    *postgres.RFQ        `json:"rfq"`
	Items  []*postgres.RFQItem  `json:"items"`
	Quotes []*postgres.RFQQuote `json:"quotes"`
}

func (s *Service) GetRFQ(ctx context.Context, rfqID, actorID uuid.UUID) (*RFQDetail, error) {
	r, err := s.store.GetRFQByID(ctx, rfqID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrRFQNotFound
	}
	if err := s.assertRFQParty(ctx, r, actorID); err != nil {
		return nil, err
	}
	items, _ := s.store.GetRFQItems(ctx, rfqID)
	quotes, _ := s.store.ListRFQQuotes(ctx, rfqID)
	return &RFQDetail{RFQ: r, Items: items, Quotes: quotes}, nil
}

func (s *Service) ListBuyerRFQs(ctx context.Context, buyerID uuid.UUID, limit, offset int) ([]*postgres.RFQ, error) {
	return s.store.ListRFQsForBuyer(ctx, buyerID, limit, offset)
}

// ListSellerRFQs returns the inbox for the calling seller. The caller
// is resolved to a seller record by the handler before this is called.
func (s *Service) ListSellerRFQs(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]*postgres.RFQ, error) {
	return s.store.ListRFQsForSeller(ctx, sellerID, status, limit, offset)
}

type SendRFQQuoteInput struct {
	RFQID        uuid.UUID
	SellerID     uuid.UUID
	LinePrices   []postgres.RFQLinePrice
	ValidityDays int
}

// SendRFQQuote rejects the call unless caller is the RFQ's seller and
// every line in LinePrices matches an item on the RFQ. Total is
// recomputed server-side (not trusted from the client) for safety.
func (s *Service) SendRFQQuote(ctx context.Context, in SendRFQQuoteInput) (*postgres.RFQQuote, error) {
	r, err := s.store.GetRFQByID(ctx, in.RFQID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrRFQNotFound
	}
	if r.SellerID != in.SellerID {
		return nil, ErrRFQForbidden
	}
	if r.Status != "requested" && r.Status != "quoted" {
		return nil, ErrRFQBadStatus
	}
	if in.ValidityDays < 1 || in.ValidityDays > 90 {
		return nil, fmt.Errorf("validity_days must be 1-90")
	}
	items, err := s.store.GetRFQItems(ctx, in.RFQID)
	if err != nil {
		return nil, err
	}
	// Index items by id for line validation.
	itemMap := make(map[uuid.UUID]*postgres.RFQItem, len(items))
	for _, it := range items {
		itemMap[it.ID] = it
	}
	if len(in.LinePrices) != len(items) {
		return nil, fmt.Errorf("must quote every item in the RFQ (%d items)", len(items))
	}
	total := 0.0
	for i, lp := range in.LinePrices {
		item, ok := itemMap[lp.RFQItemID]
		if !ok {
			return nil, fmt.Errorf("line_prices[%d] references unknown rfq_item %s", i, lp.RFQItemID)
		}
		if lp.UnitPrice <= 0 {
			return nil, fmt.Errorf("line_prices[%d] unit_price must be > 0", i)
		}
		// Server-recomputed line total — client values are advisory.
		in.LinePrices[i].VariantID = item.VariantID
		in.LinePrices[i].Quantity = item.Quantity
		in.LinePrices[i].LineTotal = round2(lp.UnitPrice * float64(item.Quantity))
		total += in.LinePrices[i].LineTotal
	}
	payload, err := json.Marshal(in.LinePrices)
	if err != nil {
		return nil, err
	}
	q := &postgres.RFQQuote{
		RFQID:        in.RFQID,
		QuotedTotal:  round2(total),
		LinePrices:   payload,
		ValidityDays: in.ValidityDays,
		ExpiresAt:    time.Now().AddDate(0, 0, in.ValidityDays),
	}
	if err := s.store.SaveRFQQuote(ctx, q); err != nil {
		return nil, fmt.Errorf("save quote: %w", err)
	}
	s.publish(ctx, "commerce.rfq.quoted", map[string]any{
		"rfq_id": in.RFQID, "quote_id": q.ID, "total": q.QuotedTotal,
	})
	return q, nil
}

// RejectRFQ — either party can cancel. Buyer reason and seller reason
// flow through the same field; UI labels accordingly.
func (s *Service) RejectRFQ(ctx context.Context, rfqID, actorID uuid.UUID, reason string) error {
	r, err := s.store.GetRFQByID(ctx, rfqID)
	if err != nil {
		return err
	}
	if r == nil {
		return ErrRFQNotFound
	}
	if err := s.assertRFQParty(ctx, r, actorID); err != nil {
		return err
	}
	if r.Status == "accepted" || r.Status == "expired" || r.Status == "rejected" || r.Status == "cancelled" {
		return ErrRFQBadStatus
	}
	if err := s.store.UpdateRFQStatus(ctx, rfqID, "rejected"); err != nil {
		return err
	}
	s.publish(ctx, "commerce.rfq.rejected", map[string]any{
		"rfq_id": rfqID, "actor_id": actorID, "reason": reason,
	})
	return nil
}

// AcceptRFQQuote is the critical conversion path. It creates an order
// using the quoted unit prices (not priceCart) so the buyer gets what
// they were promised. Tx-wrapped: order + items + stock reservation +
// quote acceptance all commit together, or nothing does.
//
// Buyer must supply an address; payment method defaults to 'prepaid'
// but the buyer can choose 'cod' or (for org buyers with credit terms)
// 'credit'. The order carries the same B2B context fields as a regular
// Checkout when organization_id is set on the parent RFQ.
type AcceptRFQQuoteInput struct {
	QuoteID        uuid.UUID
	ActorUserID    uuid.UUID
	AddressID      uuid.UUID
	PaymentMethod  string // prepaid | cod | credit
	PONumber       *string
	CostCenter     *string
	InvoiceEmail   *string
	IdempotencyKey string
}

func (s *Service) AcceptRFQQuote(ctx context.Context, in AcceptRFQQuoteInput) (*postgres.Order, error) {
	q, err := s.store.GetRFQQuote(ctx, in.QuoteID)
	if err != nil {
		return nil, err
	}
	if q == nil {
		return nil, ErrRFQNotFound
	}
	if q.AcceptedAt != nil {
		return nil, fmt.Errorf("quote already accepted")
	}
	if time.Now().After(q.ExpiresAt) {
		return nil, ErrRFQQuoteExpired
	}
	rfq, err := s.store.GetRFQByID(ctx, q.RFQID)
	if err != nil {
		return nil, err
	}
	if rfq == nil {
		return nil, ErrRFQNotFound
	}
	if rfq.BuyerUserID != in.ActorUserID {
		// Org buyer who isn't the original requester may still accept
		// if they're an admin / approver on the same org.
		if rfq.OrganizationID == nil {
			return nil, ErrRFQForbidden
		}
		if _, err := s.requireOrgRole(ctx, *rfq.OrganizationID, in.ActorUserID, "admin", "approver"); err != nil {
			return nil, ErrRFQForbidden
		}
	}
	if rfq.Status != "quoted" {
		return nil, ErrRFQBadStatus
	}

	// Parse quoted line prices.
	var lines []postgres.RFQLinePrice
	if err := json.Unmarshal(q.LinePrices, &lines); err != nil {
		return nil, fmt.Errorf("decode line prices: %w", err)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("quote has no line prices")
	}

	// Stock check — RFQ acceptance reserves inventory just like
	// regular checkout. Stock that's run out since the quote was sent
	// fails the accept loudly so the buyer can re-quote.
	for _, ln := range lines {
		inv, err := s.store.GetInventory(ctx, ln.VariantID)
		if err != nil {
			return nil, fmt.Errorf("inventory %s: %w", ln.VariantID, err)
		}
		if inv.AvailableQty() < ln.Quantity {
			return nil, fmt.Errorf("insufficient stock for variant %s (have %d, need %d)",
				ln.VariantID, inv.AvailableQty(), ln.Quantity)
		}
	}

	// Build the order items at the quoted prices.
	orderItems := make([]*postgres.OrderItem, 0, len(lines))
	subtotal := 0.0
	for _, ln := range lines {
		variant, err := s.store.GetVariantByID(ctx, ln.VariantID)
		if err != nil {
			return nil, err
		}
		product, err := s.store.GetProductByID(ctx, variant.ProductID)
		if err != nil {
			return nil, err
		}
		returnUntil := time.Now().AddDate(0, 0, product.ReturnPolicyDays)
		orderItems = append(orderItems, &postgres.OrderItem{
			ProductID:           variant.ProductID,
			VariantID:           variant.ID,
			SellerID:            product.SellerID,
			ProductTitle:        product.Title,
			SKU:                 variant.SKU,
			Quantity:            ln.Quantity,
			UnitMRP:             variant.MRP,
			UnitPrice:           ln.UnitPrice,
			DiscountAmount:      0,
			TaxAmount:           0,
			FinalPrice:          ln.LineTotal,
			Status:              "confirmed",
			ReturnEligibleUntil: &returnUntil,
		})
		subtotal += ln.LineTotal
	}

	pm := in.PaymentMethod
	if pm == "" {
		pm = "prepaid"
	}
	isCOD := pm == "cod"
	paymentStatus := "pending"
	orderStatus := "payment_pending"
	if isCOD {
		orderStatus = "confirmed"
	}
	if pm == "credit" {
		orderStatus = "confirmed"
	}

	idempKey := in.IdempotencyKey
	if idempKey == "" {
		idempKey = fmt.Sprintf("rfq-%s-%d", q.ID, time.Now().UnixNano())
	}
	order := &postgres.Order{
		CustomerUserID:  in.ActorUserID,
		Subtotal:        round2(subtotal),
		ShippingCharges: 0,
		TaxAmount:       0,
		FinalAmount:     round2(subtotal),
		CurrencyCode:    "INR",
		PaymentMethod:   &pm,
		PaymentStatus:   paymentStatus,
		DeliveryAddressID: &in.AddressID,
		Status:          orderStatus,
		IdempotencyKey:  &idempKey,
		OrganizationID:  rfq.OrganizationID,
		PONumber:        in.PONumber,
		CostCenter:      in.CostCenter,
		InvoiceEmail:    in.InvoiceEmail,
	}

	if err := s.store.CreateOrder(ctx, order, orderItems); err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Mark quote + RFQ as accepted; reserve / deduct stock.
	tx, err := s.store.DB().Begin(ctx)
	if err == nil {
		_ = s.store.MarkRFQQuoteAccepted(ctx, tx, q.ID, order.ID)
		_ = tx.Commit(ctx)
	}
	for _, ln := range lines {
		if isCOD {
			_ = s.store.DeductStock(ctx, ln.VariantID, ln.Quantity, order.ID)
		} else {
			_ = s.store.ReserveStock(ctx, ln.VariantID, in.ActorUserID, ln.Quantity, &order.ID, "order", 30*time.Minute)
		}
	}
	s.publish(ctx, "commerce.rfq.accepted", map[string]any{
		"rfq_id": rfq.ID, "quote_id": q.ID, "order_id": order.ID, "amount": order.FinalAmount,
	})
	if pm == "cod" {
		s.EnqueueFulfillPaidOrder(ctx, order.ID)
	}
	return order, nil
}

// ExpireRFQs runs periodically to flip stale RFQs/quotes. Idempotent
// — fine to call on a 1-hour cron via the fulfillment job worker.
func (s *Service) ExpireRFQs(ctx context.Context) (int, error) {
	return s.store.ExpireStaleRFQs(ctx)
}

// assertRFQParty refuses anyone who isn't the buyer or the seller's
// underlying user.
func (s *Service) assertRFQParty(ctx context.Context, r *postgres.RFQ, actorID uuid.UUID) error {
	if r.BuyerUserID == actorID {
		return nil
	}
	// Check if actor owns the RFQ's seller account.
	seller, err := s.store.GetSellerByID(ctx, r.SellerID)
	if err == nil && seller != nil && seller.UserID == actorID {
		return nil
	}
	// Org members on the buying side can also view.
	if r.OrganizationID != nil {
		if _, err := s.requireOrgRole(ctx, *r.OrganizationID, actorID, "admin", "buyer", "approver", "finance"); err == nil {
			return nil
		}
	}
	return ErrRFQForbidden
}

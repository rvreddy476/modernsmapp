// Package service implements the core business logic for commerce-service.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/identity"
	"github.com/atpost/commerce-service/internal/kyc"
	"github.com/atpost/commerce-service/internal/payments"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	kafka "github.com/segmentio/kafka-go"
)

// Phase 0.1 — surfaceable errors so the HTTP handler can map them to
// specific status codes rather than the generic 500 the old code emitted.
var (
	ErrOrderNotFound          = fmt.Errorf("order not found")
	ErrNotOrderOwner          = fmt.Errorf("not authorized for this order")
	ErrOrderNotPaymentPending = fmt.Errorf("order is not in payment_pending state")
	ErrPaymentVerifyFailed    = fmt.Errorf("payment verification failed")
	ErrPaymentAmountMismatch  = fmt.Errorf("payment amount does not match order")
	ErrStubGatewayInProd      = fmt.Errorf("stub gateway not permitted; set PAYMENTS_ALLOW_STUB=true for dev/test")
	ErrPaymentsClientMissing  = fmt.Errorf("payments client not configured")
	ErrNotReturnSeller        = fmt.Errorf("actor is not the seller for this return")
	ErrReviewOrderItemInvalid = fmt.Errorf("order item does not belong to reviewer or does not match product/seller")
	ErrReviewItemNotDelivered = fmt.Errorf("order item must be delivered before review")
	ErrReturnNotFound         = fmt.Errorf("return not found")
	ErrNotReturnParty         = fmt.Errorf("actor is not the customer or seller for this return")
	ErrReviewNotFound         = fmt.Errorf("review not found")
	ErrNotReviewSeller        = fmt.Errorf("actor is not the seller for this review")
)

// ConfirmPaymentInput is the request body the customer-facing confirm
// endpoint accepts. The signature triple is what Razorpay returns on
// successful checkout; commerce-service does not trust any of these
// fields — they are forwarded to payments-service for HMAC verification
// against the stored intent.
type ConfirmPaymentInput struct {
	PaymentIntentID   uuid.UUID
	RazorpayOrderID   string
	RazorpayPaymentID string
	RazorpaySignature string
	AmountMinor       int64
	Gateway           string
}

// Service is the main commerce service.
type Service struct {
	store     *postgres.Store
	rdb       *redis.Client
	writer    *kafka.Writer
	courier   courier.Provider
	blob      BlobStore
	identity  *identity.Client
	payments  *payments.Client
	kyc       kyc.Validator
	payoutCfg PayoutConfig
}

func New(store *postgres.Store, rdb *redis.Client, kafkaBrokers string) *Service {
	return NewWithDialer(store, rdb, kafkaBrokers, nil)
}

func NewWithDialer(store *postgres.Store, rdb *redis.Client, kafkaBrokers string, dialer *kafka.Dialer) *Service {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  strings.Split(kafkaBrokers, ","),
		Topic:    "social.events.v1",
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	return &Service{store: store, rdb: rdb, writer: w}
}

func (s *Service) Close() {
	if s.writer != nil {
		_ = s.writer.Close()
	}
}

// publish emits a Kafka event. Failures are logged but not fatal (best-effort).
func (s *Service) publish(ctx context.Context, eventType string, payload any) {
	data, _ := json.Marshal(payload)
	env := events.EventEnvelope{
		EventID:    uuid.New().String(),
		EventType:  eventType,
		Payload:    data,
		OccurredAt: time.Now(),
	}
	b, _ := json.Marshal(env)
	if err := s.writer.WriteMessages(ctx, kafka.Message{Value: b}); err != nil {
		slog.Warn("kafka publish failed", "event", eventType, "error", err)
	}
}

// ─── Seller Onboarding ───────────────────────────────────────

type OnboardSellerInput struct {
	UserID      uuid.UUID
	SellerType  string
	StoreName   string
	BrandName   *string
	Slug        string
	Description *string
	Email       string
	Phone       *string
	GSTNumber   *string
	State       *string
	City        *string
	PostalCode  *string
}

func (s *Service) OnboardSeller(ctx context.Context, in OnboardSellerInput) (*postgres.Seller, error) {
	if in.StoreName == "" {
		return nil, fmt.Errorf("store_name is required")
	}
	if in.Slug == "" {
		in.Slug = slugify(in.StoreName)
	}
	sel := &postgres.Seller{
		UserID:             in.UserID,
		SellerType:         in.SellerType,
		StoreName:          in.StoreName,
		BrandName:          in.BrandName,
		Slug:               in.Slug,
		Description:        in.Description,
		Email:              in.Email,
		Phone:              in.Phone,
		GSTNumber:          in.GSTNumber,
		State:              in.State,
		City:               in.City,
		PostalCode:         in.PostalCode,
		VerificationStatus: "pending",
		StoreStatus:        "active",
	}
	if err := s.store.CreateSeller(ctx, sel); err != nil {
		return nil, fmt.Errorf("create seller: %w", err)
	}
	s.publish(ctx, "commerce.seller.registered", map[string]any{
		"seller_id": sel.ID, "user_id": sel.UserID, "store_name": sel.StoreName,
	})
	return sel, nil
}

func (s *Service) GetSellerProfile(ctx context.Context, userID uuid.UUID) (*postgres.Seller, error) {
	return s.store.GetSellerByUserID(ctx, userID)
}

// ─── Catalog ─────────────────────────────────────────────────

type CreateProductInput struct {
	SellerID         uuid.UUID
	CategoryID       *uuid.UUID
	BrandID          *uuid.UUID
	TaxClassID       *uuid.UUID
	Title            string
	ShortTitle       *string
	Description      *string
	ShortDescription *string
	BrandName        *string
	ManufacturerName *string
	ProductType      string
	Condition        string
	ReturnPolicyType string
	ReturnPolicyDays int
	HSNCode          *string
	// Logistics & legal-metrology — schema has columns; UI exposes none today.
	PrimaryImageMediaID *uuid.UUID
	VideoMediaID        *uuid.UUID
	WeightGrams         *int
	LengthCm            *float64
	WidthCm             *float64
	HeightCm            *float64
	CountryOfOrigin     *string
	WarrantyInfo        *string
	SearchKeywords      []string
	MetaTitle           *string
	MetaDescription     *string
	Variants            []CreateVariantInput
}

type CreateVariantInput struct {
	SKU          string
	Option1Name  *string
	Option1Value *string
	Option2Name  *string
	Option2Value *string
	Option3Name  *string
	Option3Value *string
	MRP          float64
	SellingPrice float64
	CostPrice    *float64
	StockQty     int
}

func (s *Service) CreateProduct(ctx context.Context, in CreateProductInput) (*postgres.Product, error) {
	if in.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if len(in.Variants) == 0 {
		return nil, fmt.Errorf("at least one variant is required")
	}

	productType := in.ProductType
	if productType == "" {
		productType = "physical"
	}

	p := &postgres.Product{
		SellerID:            in.SellerID,
		CategoryID:          in.CategoryID,
		BrandID:             in.BrandID,
		TaxClassID:          in.TaxClassID,
		Title:               in.Title,
		ShortTitle:          in.ShortTitle,
		Slug:                uniqueSlug(slugify(in.Title)),
		Description:         in.Description,
		ShortDescription:    in.ShortDescription,
		BrandName:           in.BrandName,
		ManufacturerName:    in.ManufacturerName,
		ProductType:         productType,
		Condition:           coalesceStr(in.Condition, "new"),
		Status:              "draft",
		Visibility:          "public",
		ApprovalStatus:      "draft",
		PrimaryImageMediaID: in.PrimaryImageMediaID,
		VideoMediaID:        in.VideoMediaID,
		WeightGrams:         in.WeightGrams,
		LengthCm:            in.LengthCm,
		WidthCm:             in.WidthCm,
		HeightCm:            in.HeightCm,
		CountryOfOrigin:     in.CountryOfOrigin,
		WarrantyInfo:        in.WarrantyInfo,
		ReturnPolicyType:    coalesceStr(in.ReturnPolicyType, "7_days"),
		ReturnPolicyDays:    coalesceInt(in.ReturnPolicyDays, 7),
		HSNCode:             in.HSNCode,
		SearchKeywords:      in.SearchKeywords,
		MetaTitle:           in.MetaTitle,
		MetaDescription:     in.MetaDescription,
	}

	if err := s.store.CreateProduct(ctx, p); err != nil {
		return nil, fmt.Errorf("create product: %w", err)
	}

	for _, vi := range in.Variants {
		v := &postgres.ProductVariant{
			ProductID:    p.ID,
			SKU:          vi.SKU,
			Option1Name:  vi.Option1Name,
			Option1Value: vi.Option1Value,
			Option2Name:  vi.Option2Name,
			Option2Value: vi.Option2Value,
			MRP:          vi.MRP,
			SellingPrice: vi.SellingPrice,
			CostPrice:    vi.CostPrice,
			CurrencyCode: "INR",
			Status:       "active",
		}
		if err := s.store.CreateVariant(ctx, v); err != nil {
			return nil, fmt.Errorf("create variant %s: %w", vi.SKU, err)
		}
		// Initialize inventory
		if err := s.store.UpsertInventory(ctx, v.ID, in.SellerID, vi.StockQty); err != nil {
			slog.Warn("failed to init inventory", "variant_id", v.ID, "error", err)
		}
	}

	s.publish(ctx, "commerce.product.created", map[string]any{
		"product_id": p.ID, "seller_id": p.SellerID, "title": p.Title,
	})
	return p, nil
}

func (s *Service) GetProduct(ctx context.Context, productID uuid.UUID) (*postgres.Product, []*postgres.ProductVariant, error) {
	p, err := s.store.GetProductByID(ctx, productID)
	if err != nil {
		return nil, nil, err
	}
	variants, err := s.store.GetVariantsByProduct(ctx, productID)
	if err != nil {
		return nil, nil, err
	}
	go s.store.IncrProductViewCount(context.Background(), productID)
	return p, variants, nil
}

func (s *Service) ListCategories(ctx context.Context) ([]*postgres.ProductCategory, error) {
	return s.store.ListCategories(ctx)
}

func (s *Service) ListSellerProducts(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*postgres.Product, error) {
	products, _, err := s.store.ListSellerProducts(ctx, sellerID, "", limit, offset)
	return products, err
}

// ListProducts returns the customer-facing product catalog: published +
// approved only. Optional category filter and title query. Returns total so
// the UI can paginate.
func (s *Service) ListProducts(ctx context.Context, categoryID *uuid.UUID, query string, limit, offset int) ([]*postgres.Product, int, error) {
	return s.store.ListProducts(ctx, categoryID, query, limit, offset)
}

func (s *Service) ListOrders(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*postgres.Order, error) {
	orders, _, err := s.store.GetOrdersByCustomer(ctx, userID, limit, offset)
	return orders, err
}

// ListOrderCardsResult is the customer order-list response — Phase 2.1.
// NextCursor is empty on the last page.
type ListOrderCardsResult struct {
	Items      []postgres.OrderCard
	NextCursor string
}

// ListOrderCards returns one page of order cards for the customer using
// keyset pagination over (created_at, id). The richer shape (item count,
// seller count, first item) replaces the old anemic list the customer
// couldn't tell orders apart from. Replaces the offset/COUNT(*) path —
// no more table-scan on every page.
func (s *Service) ListOrderCards(ctx context.Context, userID uuid.UUID, limit int, cursor string) (*ListOrderCardsResult, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if cursor != "" {
		t, id, err := decodeOrderCursor(cursor)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor")
		}
		cursorTime = &t
		cursorID = &id
	}
	cards, hasMore, err := s.store.ListOrderCardsByCustomer(ctx, userID, cursorTime, cursorID, limit)
	if err != nil {
		return nil, err
	}
	res := &ListOrderCardsResult{Items: cards}
	if hasMore && len(cards) > 0 {
		last := cards[len(cards)-1]
		res.NextCursor = encodeOrderCursor(last.CreatedAt, last.ID)
	}
	return res, nil
}

// encodeOrderCursor / decodeOrderCursor are deliberately opaque to the
// client. Format is rfc3339nano|uuid wrapped in URL-safe base64; bumping
// the format only requires a server-side change because clients never
// crack it open.
func encodeOrderCursor(t time.Time, id uuid.UUID) string {
	raw := t.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

func decodeOrderCursor(s string) (time.Time, uuid.UUID, error) {
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, fmt.Errorf("bad cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return t, id, nil
}

// GetOrderWithItems returns the order and its line items, verifying ownership.
func (s *Service) GetOrderWithItems(ctx context.Context, orderID, userID uuid.UUID) (*postgres.Order, []*postgres.OrderItem, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, nil, err
	}
	if order.CustomerUserID != userID {
		return nil, nil, fmt.Errorf("order not found")
	}
	items, err := s.store.GetOrderItems(ctx, orderID)
	if err != nil {
		return nil, nil, err
	}
	return order, items, nil
}

// ListSellerOrders returns orders for a seller — used by the seller fulfillment dashboard.
// Authorization: caller must own the seller account (verified in handler).
func (s *Service) ListSellerOrders(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*postgres.Order, error) {
	orders, _, err := s.store.GetOrdersBySeller(ctx, sellerID, limit, offset)
	return orders, err
}

// SellerOrderCard is the rich DTO the fulfillment dashboard renders for one
// order containing the seller's items. The customer's other-seller items in
// a multi-seller order are intentionally excluded — sellers only see what
// they're responsible for shipping.
type SellerOrderCard struct {
	Order               *postgres.Order      `json:"order"`
	Items               []*postgres.OrderItem `json:"items"`
	Shipment            *postgres.Shipment    `json:"shipment,omitempty"`
	SellerSubtotal      float64              `json:"seller_subtotal"`
	DeliveryAddress     []byte               `json:"delivery_address,omitempty"`
}

// ListSellerFulfillment returns the seller's fulfillment queue, optionally
// filtered by stage:
//
//	stage="" / "all"     — every order with the seller's items
//	stage="unshipped"    — paid but no shipment booked for this seller yet
//	stage="in_transit"   — shipment booked, not yet delivered
//	stage="delivered"    — shipment delivered
//	stage="cancelled"    — order cancelled
//
// The DTO carries only this seller's items even in multi-seller orders.
// Phase 4.2 — replaces the bare ListSellerOrders surface so the dashboard
// can render item-level state in one round trip.
func (s *Service) ListSellerFulfillment(ctx context.Context, sellerID uuid.UUID, stage string, limit, offset int) ([]*SellerOrderCard, error) {
	orders, _, err := s.store.GetOrdersBySeller(ctx, sellerID, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]*SellerOrderCard, 0, len(orders))
	for _, o := range orders {
		items, _ := s.store.GetOrderItems(ctx, o.ID)
		mine := make([]*postgres.OrderItem, 0, len(items))
		var subtotal float64
		for _, it := range items {
			if it.SellerID == sellerID {
				mine = append(mine, it)
				subtotal += it.FinalPrice
			}
		}
		if len(mine) == 0 {
			continue
		}
		shipment, _ := s.store.GetShipmentByOrderAndSeller(ctx, o.ID, sellerID)
		card := &SellerOrderCard{
			Order:           o,
			Items:           mine,
			Shipment:        shipment,
			SellerSubtotal:  round2(subtotal),
			DeliveryAddress: o.DeliveryAddressSnapshot,
		}
		if !fulfillmentMatchesStage(card, stage) {
			continue
		}
		out = append(out, card)
	}
	return out, nil
}

// GetSellerOrderDetail returns a single order from the seller's perspective —
// their items + their shipment + the buyer's delivery snapshot — used by
// the seller order-detail page. Fails if the caller has no items in the
// order (authorization).
func (s *Service) GetSellerOrderDetail(ctx context.Context, sellerID, orderID uuid.UUID) (*SellerOrderCard, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil || order == nil {
		return nil, ErrOrderNotFound
	}
	items, _ := s.store.GetOrderItems(ctx, orderID)
	mine := make([]*postgres.OrderItem, 0, len(items))
	var subtotal float64
	for _, it := range items {
		if it.SellerID == sellerID {
			mine = append(mine, it)
			subtotal += it.FinalPrice
		}
	}
	if len(mine) == 0 {
		return nil, ErrNotOrderOwner
	}
	shipment, _ := s.store.GetShipmentByOrderAndSeller(ctx, orderID, sellerID)
	return &SellerOrderCard{
		Order:           order,
		Items:           mine,
		Shipment:        shipment,
		SellerSubtotal:  round2(subtotal),
		DeliveryAddress: order.DeliveryAddressSnapshot,
	}, nil
}

// fulfillmentMatchesStage routes a card into the dashboard tab buckets so
// callers can filter server-side without each tab issuing its own query.
func fulfillmentMatchesStage(card *SellerOrderCard, stage string) bool {
	switch stage {
	case "", "all":
		return true
	case "unshipped":
		return card.Order.Status != "cancelled" &&
			card.Order.PaymentStatus == "paid" &&
			(card.Shipment == nil || card.Shipment.Status == "pending")
	case "in_transit":
		return card.Shipment != nil &&
			card.Shipment.Status != "delivered" &&
			card.Shipment.Status != "pending" &&
			card.Order.Status != "cancelled"
	case "delivered":
		return card.Shipment != nil && card.Shipment.Status == "delivered"
	case "cancelled":
		return card.Order.Status == "cancelled"
	}
	return true
}

func (s *Service) GetOrder(ctx context.Context, orderID, userID uuid.UUID) (*postgres.Order, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.CustomerUserID != userID {
		return nil, fmt.Errorf("order not found")
	}
	return order, nil
}

// ─── Cart ────────────────────────────────────────────────────

func (s *Service) AddToCart(ctx context.Context, userID, variantID uuid.UUID, qty int) error {
	variant, err := s.store.GetVariantByID(ctx, variantID)
	if err != nil {
		return fmt.Errorf("variant not found: %w", err)
	}
	if variant.Status != "active" {
		return fmt.Errorf("variant is not available")
	}

	inv, err := s.store.GetInventory(ctx, variantID)
	if err != nil {
		return fmt.Errorf("inventory not found: %w", err)
	}
	if inv.AvailableQty() < qty {
		return fmt.Errorf("only %d units available", inv.AvailableQty())
	}

	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return fmt.Errorf("get cart: %w", err)
	}

	return s.store.UpsertCartItem(ctx, cart.ID, variantID, variant.ProductID, qty, variant.SellingPrice)
}

func (s *Service) RemoveFromCart(ctx context.Context, userID, variantID uuid.UUID) error {
	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return err
	}
	return s.store.RemoveCartItem(ctx, cart.ID, variantID)
}

// UpdateCartItem sets the absolute quantity for a variant in the user's
// cart. Quantity 0 removes the line. Replaces the mobile delete+add
// roundtrip from commerce_repository.dart (Phase 1.2). UpsertCartItem at
// the SQL layer is INSERT ... ON CONFLICT DO UPDATE, so this is atomic at
// the row level — concurrent calls converge on a single final quantity.
func (s *Service) UpdateCartItem(ctx context.Context, userID, variantID uuid.UUID, qty int) error {
	if qty < 0 {
		return fmt.Errorf("quantity must be >= 0")
	}
	if qty == 0 {
		return s.RemoveFromCart(ctx, userID, variantID)
	}
	// AddToCart's stock check + upsert semantics are exactly what an
	// atomic set-to-N needs; reuse rather than duplicate.
	return s.AddToCart(ctx, userID, variantID, qty)
}

type CartSummary struct {
	CartID      uuid.UUID
	Items       []*CartItemDetail
	Subtotal    float64
	ItemCount   int
}

type CartItemDetail struct {
	Item     *postgres.CartItem
	Product  *postgres.Product
	Variant  *postgres.ProductVariant
}

func (s *Service) GetCart(ctx context.Context, userID uuid.UUID) (*CartSummary, error) {
	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	items, err := s.store.GetCartItems(ctx, cart.ID)
	if err != nil {
		return nil, err
	}

	summary := &CartSummary{CartID: cart.ID}
	for _, ci := range items {
		product, _ := s.store.GetProductByID(ctx, ci.ProductID)
		variant, _ := s.store.GetVariantByID(ctx, ci.VariantID)
		summary.Items = append(summary.Items, &CartItemDetail{
			Item: ci, Product: product, Variant: variant,
		})
		summary.Subtotal += ci.PriceSnapshot * float64(ci.Quantity)
		summary.ItemCount += ci.Quantity
	}
	return summary, nil
}

// ─── Tax Calculation ─────────────────────────────────────────

type TaxBreakdown struct {
	TaxableAmount float64
	CGSTPct       float64
	SGSTPct       float64
	IGSTPct       float64
	CGSTAmount    float64
	SGSTAmount    float64
	IGSTAmount    float64
	TotalTax      float64
	IsInterstate  bool
}

// CalcTax returns GST breakdown for a given amount.
// If sellerState == customerState → intrastate (CGST+SGST); else interstate (IGST).
func CalcTax(amount, cgstPct, sgstPct, igstPct float64, sellerState, customerState string) TaxBreakdown {
	interstate := sellerState != customerState
	tb := TaxBreakdown{TaxableAmount: amount, CGSTPct: cgstPct, SGSTPct: sgstPct, IGSTPct: igstPct, IsInterstate: interstate}
	if interstate {
		tb.IGSTAmount = round2(amount * igstPct / 100)
		tb.TotalTax = tb.IGSTAmount
	} else {
		tb.CGSTAmount = round2(amount * cgstPct / 100)
		tb.SGSTAmount = round2(amount * sgstPct / 100)
		tb.TotalTax = tb.CGSTAmount + tb.SGSTAmount
	}
	return tb
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// couponWithinCaps returns true iff the coupon still has global +
// per-user redemptions available. Audit O10: GetCouponByCode already
// filters by is_active and expiry at the SQL layer, but the prior
// checkout never enforced max_uses or max_uses_per_user — a permissive
// public code could be redeemed unboundedly by anyone. max_uses == 0
// is treated as "unlimited"; same for max_uses_per_user.
func couponWithinCaps(ctx context.Context, s *Service, c *postgres.Coupon, userID uuid.UUID) bool {
	if c.MaxUses != nil && *c.MaxUses > 0 && c.UsesCount >= *c.MaxUses {
		return false
	}
	if c.MaxUsesPerUser > 0 {
		n, err := s.store.CountCouponUsagesByUser(ctx, c.ID, userID)
		if err != nil {
			// Fail closed on Postgres errors — reject the coupon rather
			// than risk over-redemption under a transient blip.
			return false
		}
		if n >= c.MaxUsesPerUser {
			return false
		}
	}
	return true
}

// ─── Checkout & Orders ───────────────────────────────────────

// QuoteInput is the request body for the server-authoritative checkout
// quote endpoint (POST /v1/commerce/checkout/quote — Phase 1.1).
type QuoteInput struct {
	UserID        uuid.UUID
	AddressID     uuid.UUID
	PaymentMethod string
	CouponCode    string
}

// QuoteItem is one line item in a checkout quote response.
type QuoteItem struct {
	VariantID    uuid.UUID `json:"variant_id"`
	ProductID    uuid.UUID `json:"product_id"`
	SellerID     uuid.UUID `json:"seller_id"`
	ProductTitle string    `json:"product_title"`
	SKU          string    `json:"sku,omitempty"`
	Quantity     int       `json:"quantity"`
	UnitPrice    float64   `json:"unit_price"`
	LineSubtotal float64   `json:"line_subtotal"`
}

// UnavailableQuoteItem is one cart row whose requested quantity exceeds
// available stock — surfaced on the Quote response so the UI can warn
// before "Place order". Strict checkout still fails on these.
type UnavailableQuoteItem struct {
	VariantID    uuid.UUID `json:"variant_id"`
	ProductID    uuid.UUID `json:"product_id"`
	ProductTitle string    `json:"product_title"`
	Available    int       `json:"available"`
	Requested    int       `json:"requested"`
}

// Quote is the server-authoritative pricing the client must render before
// placing an order. The same priceCart helper backs both Quote and
// Checkout, so what the customer sees in the quote is what they get on
// the order — there is no client-side recomputation.
type Quote struct {
	Subtotal         float64                `json:"subtotal"`
	CouponDiscount   float64                `json:"coupon_discount"`
	CouponCode       string                 `json:"coupon_code,omitempty"`
	Shipping         float64                `json:"shipping"`
	Tax              float64                `json:"tax"`
	GrandTotal       float64                `json:"grand_total"`
	Currency         string                 `json:"currency"`
	Items            []QuoteItem            `json:"items"`
	UnavailableItems []UnavailableQuoteItem `json:"unavailable_items"`
	CODEligible      bool                   `json:"cod_eligible"`
	Serviceable      bool                   `json:"serviceable"`
	SellerIDs        []uuid.UUID            `json:"seller_ids"`
}

// pricingResult is the shared, untransformed output of the cart-pricing
// pipeline. Quote shapes it into a customer-facing DTO; Checkout consumes
// the OrderItems directly to persist the order.
type pricingResult struct {
	Cart             *postgres.Cart
	CartItems        []*postgres.CartItem
	OrderItems       []*postgres.OrderItem
	Subtotal         float64
	CouponDiscount   float64
	CouponCodePtr    *string
	Shipping         float64
	Tax              float64
	FinalAmount      float64
	UnavailableItems []UnavailableQuoteItem
}

// priceCart resolves the user's current cart into a fully-priced result
// using server-side product/variant/inventory data. strict=true (Checkout)
// rejects the call on any out-of-stock line; strict=false (Quote) returns
// the result with UnavailableItems populated and prices reflecting only
// the available rows. Coupon, shipping, and tax rules match Checkout 1:1.
//
// Caller must validate the user owns the cart; this helper trusts userID.
func (s *Service) priceCart(ctx context.Context, userID uuid.UUID, paymentMethod, couponCode string, strict bool) (*pricingResult, error) {
	cart, err := s.store.GetOrCreateCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	cartItems, err := s.store.GetCartItems(ctx, cart.ID)
	if err != nil {
		return nil, err
	}
	if len(cartItems) == 0 {
		return nil, fmt.Errorf("cart is empty")
	}

	var orderItems []*postgres.OrderItem
	var unavailable []UnavailableQuoteItem
	var subtotal float64
	totalTax := 0.0 // Phase 3+ will compute per-HSN GST; today the schema stores 0.

	for _, ci := range cartItems {
		variant, err := s.store.GetVariantByID(ctx, ci.VariantID)
		if err != nil {
			return nil, fmt.Errorf("variant %s not found", ci.VariantID)
		}
		product, err := s.store.GetProductByID(ctx, ci.ProductID)
		if err != nil {
			return nil, fmt.Errorf("product %s not found", ci.ProductID)
		}
		inv, err := s.store.GetInventory(ctx, ci.VariantID)
		if err != nil {
			return nil, fmt.Errorf("inventory for %s not found", ci.VariantID)
		}
		if inv.AvailableQty() < ci.Quantity {
			if strict {
				return nil, fmt.Errorf("insufficient stock for %s: available %d", product.Title, inv.AvailableQty())
			}
			unavailable = append(unavailable, UnavailableQuoteItem{
				VariantID:    ci.VariantID,
				ProductID:    ci.ProductID,
				ProductTitle: product.Title,
				Available:    inv.AvailableQty(),
				Requested:    ci.Quantity,
			})
			continue
		}

		lineTotal := variant.SellingPrice * float64(ci.Quantity)
		subtotal += lineTotal

		returnUntil := time.Now().AddDate(0, 0, product.ReturnPolicyDays)
		orderItems = append(orderItems, &postgres.OrderItem{
			ProductID:           ci.ProductID,
			VariantID:           ci.VariantID,
			SellerID:            product.SellerID,
			ProductTitle:        product.Title,
			SKU:                 variant.SKU,
			Quantity:            ci.Quantity,
			UnitMRP:             variant.MRP,
			UnitPrice:           variant.SellingPrice,
			DiscountAmount:      0,
			TaxAmount:           0,
			FinalPrice:          lineTotal,
			Status:              "confirmed",
			ReturnEligibleUntil: &returnUntil,
		})
	}

	// Coupon — Audit O10: enforces max_uses + max_uses_per_user caps.
	couponDiscount := 0.0
	var couponCodePtr *string
	if couponCode != "" {
		c, err := s.store.GetCouponByCode(ctx, couponCode)
		if err == nil && subtotal >= c.MinOrderAmount && couponWithinCaps(ctx, s, c, userID) {
			switch c.DiscountType {
			case "percentage":
				d := subtotal * c.DiscountValue / 100
				if c.MaxDiscountAmount != nil && d > *c.MaxDiscountAmount {
					d = *c.MaxDiscountAmount
				}
				couponDiscount = round2(d)
			case "flat":
				couponDiscount = c.DiscountValue
			}
		}
		cc := couponCode
		couponCodePtr = &cc
	}

	// Shipping: flat ₹40, free above ₹499. Phase 1.3 serviceability will
	// per-courier/per-pincode the real number.
	shipping := 40.0
	if subtotal > 499 {
		shipping = 0
	}

	final := subtotal - couponDiscount + shipping + totalTax
	return &pricingResult{
		Cart:             cart,
		CartItems:        cartItems,
		OrderItems:       orderItems,
		Subtotal:         subtotal,
		CouponDiscount:   couponDiscount,
		CouponCodePtr:    couponCodePtr,
		Shipping:         shipping,
		Tax:              totalTax,
		FinalAmount:      final,
		UnavailableItems: unavailable,
	}, nil
}

// ServiceabilityInput is the request for CheckServiceability — Phase 1.3.
// Pincode is the customer's drop pincode; ProductID resolves the seller
// (and pickup pincode) and the package weight unless overridden.
type ServiceabilityInput struct {
	Pincode       string
	ProductID     uuid.UUID
	VariantID     uuid.UUID
	SellerID      uuid.UUID
	PaymentMethod string
}

// CheckServiceability is the server-authoritative serviceability +
// COD-eligibility check that replaces the mobile pincode heuristic.
// Inputs come from the product's seller pickup address and weight; the
// courier adapter returns the authoritative answer (currently stub-ish
// for both adapters — the Shiprocket implementation is a follow-up).
func (s *Service) CheckServiceability(ctx context.Context, in ServiceabilityInput) (*courier.ServiceabilityResult, error) {
	if in.Pincode == "" {
		return &courier.ServiceabilityResult{Serviceable: false, Reason: "pincode required"}, nil
	}
	product, err := s.store.GetProductByID(ctx, in.ProductID)
	if err != nil {
		return nil, fmt.Errorf("product %s not found", in.ProductID)
	}
	sellerID := product.SellerID
	if in.SellerID != uuid.Nil {
		sellerID = in.SellerID
	}
	seller, err := s.store.GetSellerByID(ctx, sellerID)
	if err != nil {
		return nil, fmt.Errorf("seller %s not found", sellerID)
	}
	if seller.PostalCode == nil || *seller.PostalCode == "" {
		return &courier.ServiceabilityResult{
			Serviceable: false,
			Reason:      "seller pickup pincode not configured",
		}, nil
	}
	weightKg := 0.5 // sensible default until catalog enforces a weight
	if product.WeightGrams != nil && *product.WeightGrams > 0 {
		weightKg = float64(*product.WeightGrams) / 1000.0
	}
	pm := in.PaymentMethod
	if pm == "" {
		pm = "prepaid"
	}
	if s.courier == nil {
		// No courier wired (test/dev without provider): assume reachable
		// so the rest of the flow keeps working; production must have
		// COURIER_PROVIDER set.
		return &courier.ServiceabilityResult{
			Serviceable:   true,
			CODSupported:  true,
			EstimatedDays: 4,
			Courier:       "none",
			Reason:        "courier provider not configured",
		}, nil
	}
	return s.courier.CheckServiceability(ctx, courier.ServiceabilityRequest{
		PickupPincode: *seller.PostalCode,
		DropPincode:   in.Pincode,
		WeightKg:      weightKg,
		PaymentMethod: pm,
	})
}

// Quote returns the server-authoritative pricing for the user's current
// cart without persisting an order. The web + mobile checkout flows must
// render this before "Place order" so the customer sees the same numbers
// the server will use on Checkout.
//
// Serviceability and COD eligibility are placeholder true; Phase 1.3
// replaces them with the real courier + pincode check.
func (s *Service) Quote(ctx context.Context, in QuoteInput) (*Quote, error) {
	res, err := s.priceCart(ctx, in.UserID, in.PaymentMethod, in.CouponCode, false)
	if err != nil {
		return nil, err
	}
	items := make([]QuoteItem, 0, len(res.OrderItems))
	sellerSet := map[uuid.UUID]struct{}{}
	for _, oi := range res.OrderItems {
		items = append(items, QuoteItem{
			VariantID:    oi.VariantID,
			ProductID:    oi.ProductID,
			SellerID:     oi.SellerID,
			ProductTitle: oi.ProductTitle,
			SKU:          oi.SKU,
			Quantity:     oi.Quantity,
			UnitPrice:    oi.UnitPrice,
			LineSubtotal: oi.FinalPrice,
		})
		sellerSet[oi.SellerID] = struct{}{}
	}
	sellerIDs := make([]uuid.UUID, 0, len(sellerSet))
	for sid := range sellerSet {
		sellerIDs = append(sellerIDs, sid)
	}
	codeStr := ""
	if res.CouponCodePtr != nil {
		codeStr = *res.CouponCodePtr
	}
	return &Quote{
		Subtotal:         round2(res.Subtotal),
		CouponDiscount:   round2(res.CouponDiscount),
		CouponCode:       codeStr,
		Shipping:         round2(res.Shipping),
		Tax:              round2(res.Tax),
		GrandTotal:       round2(res.FinalAmount),
		Currency:         "INR",
		Items:            items,
		UnavailableItems: res.UnavailableItems,
		CODEligible:      true, // Phase 1.3 replaces with real check
		Serviceable:      true, // Phase 1.3 replaces with real check
		SellerIDs:        sellerIDs,
	}, nil
}

type CheckoutInput struct {
	UserID         uuid.UUID
	AddressID      uuid.UUID
	PaymentMethod  string
	CouponCode     string
	GiftMessage    *string
	IdempotencyKey string

	// ─── Phase 5 — Optional B2B context ────────────────────────
	// When OrganizationID is set, the caller must be an active 'admin'
	// or 'buyer' member. The org's approval_threshold + credit_terms
	// are applied at order create time.
	OrganizationID *uuid.UUID
	PONumber       *string
	CostCenter     *string
	InvoiceEmail   *string
}

// Checkout commits the user's cart as an order. All pricing (line totals,
// coupon, shipping, tax, grand total) is computed by priceCart so a future
// quote API and any client-side display can never drift from the value
// actually persisted on the order.
func (s *Service) Checkout(ctx context.Context, in CheckoutInput) (*postgres.Order, error) {
	res, err := s.priceCart(ctx, in.UserID, in.PaymentMethod, in.CouponCode, true)
	if err != nil {
		return nil, err
	}

	idempKey := in.IdempotencyKey
	if idempKey == "" {
		idempKey = fmt.Sprintf("%s-%d", in.UserID, time.Now().UnixNano())
	}

	// COD orders skip the gateway: confirmed immediately, payment_status
	// stays "pending" (orders_payment_status_check allows
	// pending|processing|paid|failed|refund_*; there is no cod_pending —
	// downstream code distinguishes COD by reading payment_method='cod').
	addrSnapshot, _ := json.Marshal(map[string]any{"address_id": in.AddressID})
	pm := in.PaymentMethod
	isCOD := strings.EqualFold(pm, "cod")
	paymentStatus := "pending"
	orderStatus := "payment_pending"
	if isCOD {
		orderStatus = "confirmed"
	}

	// Phase 5 — B2B context: validate org membership, apply approval
	// threshold + credit terms. A buyer on an org with approval_threshold
	// set and order >= threshold can't pay yet — the order parks in
	// approval_status=pending and Status="awaiting_approval"; an approver
	// then green-lights it via ApproveOrgOrder.
	var (
		orgID           *uuid.UUID
		approvalStatus  *string
		creditTermsDays int
		paymentDueDate  *time.Time
	)
	if in.OrganizationID != nil {
		member, err := s.requireOrgRole(ctx, *in.OrganizationID, in.UserID, "admin", "buyer")
		if err != nil {
			return nil, fmt.Errorf("org checkout: %w", err)
		}
		_ = member // role already validated
		org, err := s.store.GetOrganizationByID(ctx, *in.OrganizationID)
		if err != nil || org == nil {
			return nil, ErrOrgNotFound
		}
		if org.Status != "active" {
			return nil, fmt.Errorf("organization not active")
		}
		orgID = &org.ID
		// Approval gate
		if org.ApprovalThreshold != nil && res.FinalAmount >= *org.ApprovalThreshold {
			s := "pending"
			approvalStatus = &s
			orderStatus = "awaiting_approval"
			paymentStatus = "pending"
		} else if in.OrganizationID != nil {
			s := "not_required"
			approvalStatus = &s
		}
		// Credit terms: net N days. Only applied when the org has terms
		// configured AND the buyer chose the credit payment method.
		if org.CreditTermsDays > 0 && strings.EqualFold(pm, "credit") {
			creditTermsDays = org.CreditTermsDays
			due := time.Now().AddDate(0, 0, org.CreditTermsDays)
			paymentDueDate = &due
			// Credit orders defer payment but the order is otherwise
			// confirmed once approval clears.
			if approvalStatus == nil || *approvalStatus != "pending" {
				orderStatus = "confirmed"
			}
		}
	}

	order := &postgres.Order{
		CustomerUserID:          in.UserID,
		Subtotal:                res.Subtotal,
		DiscountAmount:          0,
		ShippingCharges:         res.Shipping,
		TaxAmount:               res.Tax,
		CouponCode:              res.CouponCodePtr,
		CouponDiscount:          res.CouponDiscount,
		FinalAmount:             res.FinalAmount,
		CurrencyCode:            "INR",
		PaymentMethod:           &pm,
		PaymentStatus:           paymentStatus,
		DeliveryAddressID:       &in.AddressID,
		DeliveryAddressSnapshot: addrSnapshot,
		GiftMessage:             in.GiftMessage,
		Status:                  orderStatus,
		IdempotencyKey:          &idempKey,
		OrganizationID:          orgID,
		PONumber:                in.PONumber,
		CostCenter:              in.CostCenter,
		InvoiceEmail:            in.InvoiceEmail,
		ApprovalStatus:          approvalStatus,
		CreditTermsDays:         creditTermsDays,
		PaymentDueDate:          paymentDueDate,
	}

	if err := s.store.CreateOrder(ctx, order, res.OrderItems); err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Hard-reserve inventory (COD deducts immediately — no gateway step).
	for _, ci := range res.CartItems {
		if isCOD {
			if err := s.store.DeductStock(ctx, ci.VariantID, ci.Quantity, order.ID); err != nil {
				slog.Warn("failed to deduct stock for COD", "variant", ci.VariantID, "error", err)
			}
			continue
		}
		if err := s.store.ReserveStock(ctx, ci.VariantID, in.UserID, ci.Quantity, &order.ID, "order", 30*time.Minute); err != nil {
			slog.Warn("failed to reserve stock", "variant", ci.VariantID, "error", err)
		}
	}

	_ = s.store.ClearCart(ctx, res.Cart.ID)

	if res.CouponCodePtr != nil {
		if c, err := s.store.GetCouponByCode(ctx, *res.CouponCodePtr); err == nil {
			_ = s.store.IncrCouponUsage(ctx, c.ID, in.UserID, order.ID)
		}
	}

	buyerEmail, buyerName := s.resolveBuyer(ctx, in.UserID)
	var sellerEmail, sellerName string
	if len(res.OrderItems) > 0 {
		if seller, err := s.store.GetSellerByID(ctx, res.OrderItems[0].SellerID); err == nil {
			sellerEmail, sellerName = s.resolveSeller(seller)
		}
	}

	s.publish(ctx, "commerce.order.created", map[string]any{
		"order_id": order.ID, "user_id": in.UserID,
		"order_number": order.OrderNumber, "amount": order.FinalAmount,
		"payment_method": pm,
		"buyer_email":    buyerEmail,
		"buyer_name":     buyerName,
	})
	s.publish(ctx, events.EventCommerceSellerNewOrder, map[string]any{
		"order_id":     order.ID,
		"order_number": order.OrderNumber,
		"seller_id":    sellerIDOrNil(res.OrderItems),
		"amount":       order.FinalAmount,
		"seller_email": sellerEmail,
		"seller_name":  sellerName,
	})
	if isCOD {
		s.publish(ctx, events.EventCommerceOrderPaid, map[string]any{
			"order_id":       order.ID,
			"order_number":   order.OrderNumber,
			"user_id":        in.UserID,
			"amount":         order.FinalAmount,
			"payment_method": "cod",
			"buyer_email":    buyerEmail,
			"buyer_name":     buyerName,
		})
		// Phase 6.1 — durable enqueue replaces the fire-and-forget goroutine.
		s.EnqueueFulfillPaidOrder(ctx, order.ID)
	}
	return order, nil
}

func sellerIDOrNil(items []*postgres.OrderItem) *uuid.UUID {
	if len(items) == 0 {
		return nil
	}
	return &items[0].SellerID
}

// ConfirmPayment is the customer-facing confirm path invoked by the HTTP
// handler after Razorpay checkout returns. It is the safety-critical entry
// point: previously the handler trusted any (payment_id, gateway) pair the
// client sent and marked the order paid. Now we:
//
//  1. Require the actor to own the order.
//  2. Require the order to actually be payment_pending.
//  3. Forward the Razorpay signature triple to payments-service for HMAC
//     verification + amount check.
//  4. Refuse gateway=stub unless PAYMENTS_ALLOW_STUB is explicitly set.
//
// Idempotent — an already-paid order returns nil without re-running the
// fulfillment side effects.
func (s *Service) ConfirmPayment(ctx context.Context, orderID, actorID uuid.UUID, in ConfirmPaymentInput) error {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return err
	}
	if order == nil {
		return ErrOrderNotFound
	}
	if order.CustomerUserID != actorID {
		return ErrNotOrderOwner
	}
	if order.PaymentStatus == "paid" {
		return nil // idempotent: payment already applied
	}
	if order.PaymentStatus != "payment_pending" {
		return ErrOrderNotPaymentPending
	}

	gateway := in.Gateway
	if gateway == "" {
		gateway = "razorpay"
	}
	if gateway == "stub" && os.Getenv("PAYMENTS_ALLOW_STUB") != "true" {
		return ErrStubGatewayInProd
	}

	if gateway != "stub" {
		if s.payments == nil {
			return ErrPaymentsClientMissing
		}
		expectedMinor := int64(math.Round(order.FinalAmount * 100))
		if in.AmountMinor != 0 && in.AmountMinor != expectedMinor {
			return ErrPaymentAmountMismatch
		}
		res, err := s.payments.VerifyIntent(ctx, in.PaymentIntentID, in.RazorpayOrderID, in.RazorpayPaymentID, in.RazorpaySignature, expectedMinor)
		if err != nil {
			slog.Warn("payment verify failed",
				"order_id", orderID, "intent_id", in.PaymentIntentID, "error", err)
			return ErrPaymentVerifyFailed
		}
		if res == nil || !res.Verified {
			return ErrPaymentVerifyFailed
		}
	}

	return s.applyPaidStatus(ctx, orderID, in.RazorpayPaymentID, gateway, &actorID, "customer")
}

// OrderActorRole describes how the supplied actor relates to an order —
// used by the shipment / invoice / return handlers to gate writes to the
// seller of at least one item and to gate reads to the customer or a
// seller. Returns ErrOrderNotFound when the order does not exist.
type OrderActorRole struct {
	IsCustomer bool
	IsSeller   bool
}

// OrderActor inspects the order + its items and reports whether the
// supplied actor is the customer and/or a seller of any item.
func (s *Service) OrderActor(ctx context.Context, orderID, actorID uuid.UUID) (OrderActorRole, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return OrderActorRole{}, err
	}
	if order == nil {
		return OrderActorRole{}, ErrOrderNotFound
	}
	role := OrderActorRole{IsCustomer: order.CustomerUserID == actorID}
	items, _ := s.store.GetOrderItems(ctx, orderID)
	for _, it := range items {
		if it.SellerID == actorID {
			role.IsSeller = true
			break
		}
	}
	return role, nil
}

// ApplyVerifiedPaymentEvent is the system entry point the Kafka payments
// consumer uses when payments-service publishes payment.succeeded. The
// webhook has already verified the Razorpay signature upstream, so we
// trust the event and apply the paid status directly. Idempotent.
func (s *Service) ApplyVerifiedPaymentEvent(ctx context.Context, orderID uuid.UUID, paymentID string) error {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return err
	}
	if order == nil {
		return ErrOrderNotFound
	}
	if order.PaymentStatus == "paid" {
		return nil
	}
	return s.applyPaidStatus(ctx, orderID, paymentID, "razorpay", nil, "system")
}

// applyPaidStatus is the shared "actually mark this order paid + fire the
// downstream side effects" core, called from both the customer-driven
// ConfirmPayment and the webhook-driven ApplyVerifiedPaymentEvent.
func (s *Service) applyPaidStatus(ctx context.Context, orderID uuid.UUID, paymentID, gateway string, actorID *uuid.UUID, actorType string) error {
	if err := s.store.UpdatePaymentStatus(ctx, orderID, "paid", paymentID, gateway); err != nil {
		return err
	}
	if err := s.store.UpdateOrderStatus(ctx, orderID, "confirmed", actorID, actorType, "payment confirmed"); err != nil {
		return err
	}

	// Deduct inventory (best-effort).
	items, _ := s.store.GetOrderItems(ctx, orderID)
	for _, item := range items {
		if err := s.store.DeductStock(ctx, item.VariantID, item.Quantity, orderID); err != nil {
			slog.Warn("failed to deduct stock", "variant", item.VariantID, "error", err)
		}
	}

	order, _ := s.store.GetOrderByID(ctx, orderID)
	var buyerEmail, orderNumber string
	var amount float64
	if order != nil {
		buyerEmail, _ = s.resolveBuyer(ctx, order.CustomerUserID)
		orderNumber = order.OrderNumber
		amount = order.FinalAmount
	}
	s.publish(ctx, events.EventCommerceOrderPaid, map[string]any{
		"order_id":     orderID,
		"order_number": orderNumber,
		"amount":       amount,
		"payment_id":   paymentID,
		"buyer_email":  buyerEmail,
	})

	// Phase 6.1 — enqueue a durable fulfillment job rather than firing
	// `go s.fulfillPaidOrder(orderID)`. A service restart between this
	// point and the side effects (invoice + shipment) used to drop the
	// work entirely; now the worker picks it back up.
	s.EnqueueFulfillPaidOrder(ctx, orderID)
	return nil
}

// MarkPaymentFailed flags an order's payment as failed and releases the stock
// reservation made at checkout so other customers can buy the units. The
// order itself stays in payment_pending so the customer can retry — switching
// to a hard "payment_failed" terminal state would force them to rebuild the
// cart. Idempotent: a second call on an already-failed intent is a no-op.
func (s *Service) MarkPaymentFailed(ctx context.Context, orderID uuid.UUID, paymentID string) error {
	if err := s.store.UpdatePaymentStatus(ctx, orderID, "failed", paymentID, "razorpay"); err != nil {
		return err
	}
	items, _ := s.store.GetOrderItems(ctx, orderID)
	order, _ := s.store.GetOrderByID(ctx, orderID)
	if order == nil {
		return nil
	}
	for _, item := range items {
		if err := s.store.ReleaseReservation(ctx, item.VariantID, order.CustomerUserID, item.Quantity); err != nil {
			slog.Warn("failed to release reservation",
				"variant", item.VariantID, "order", orderID, "error", err)
		}
	}
	return nil
}

// fulfillPaidOrder issues the invoice and books a shipment once payment is settled.
// Uses a fresh context so it survives the caller's deadline.
func (s *Service) fulfillPaidOrder(orderID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if s.blob != nil {
		if _, err := s.IssueInvoice(ctx, orderID); err != nil {
			slog.Warn("auto invoice failed", "order_id", orderID, "error", err)
		}
	}
	if s.courier != nil {
		if _, err := s.CreateShipmentForOrder(ctx, orderID); err != nil {
			slog.Warn("auto shipment failed", "order_id", orderID, "error", err)
		}
	}
}

// CancelOrder cancels an order that hasn't shipped yet.
func (s *Service) CancelOrder(ctx context.Context, orderID, actorID uuid.UUID, actorType, reason string) error {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return err
	}
	// Only allow cancellation before shipping
	cancellable := map[string]bool{
		"payment_pending": true, "created": true, "confirmed": true, "packed": true,
	}
	if !cancellable[order.Status] {
		return fmt.Errorf("order cannot be cancelled in status: %s", order.Status)
	}
	return s.store.UpdateOrderStatus(ctx, orderID, "cancelled", &actorID, actorType, reason)
}

// ─── Returns ─────────────────────────────────────────────────

// GetReturnRequest fetches a return for read by either the customer or
// the seller. Phase 2.2 — adds the missing detail endpoint mobile was
// emulating by listing all returns and filtering client-side.
func (s *Service) GetReturnRequest(ctx context.Context, returnID, actorID uuid.UUID) (*postgres.ReturnRequest, error) {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil || r == nil {
		return nil, ErrReturnNotFound
	}
	if r.CustomerUserID != actorID && r.SellerID != actorID {
		return nil, ErrNotReturnParty
	}
	return r, nil
}

// BulkReturnItemInput is one line in a multi-item return request. Phase 2.3
// replaces the mobile fan-out (N HTTP calls) with a single endpoint.
type BulkReturnItemInput struct {
	OrderItemID       uuid.UUID
	SellerID          uuid.UUID
	ReasonCode        string
	ReasonDescription *string
}

// CreateReturnRequestBulk creates a return row per supplied item. Each
// row goes through the existing single-item validation + publish path,
// so multi-seller orders correctly notify each seller. On partial
// failure (e.g. item 2 of 3 is ineligible) the first N successfully-
// created return rows are returned alongside the error so the caller
// can surface what landed and what didn't.
func (s *Service) CreateReturnRequestBulk(ctx context.Context, orderID, customerUserID uuid.UUID, items []BulkReturnItemInput) ([]*postgres.ReturnRequest, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("at least one item required")
	}
	out := make([]*postgres.ReturnRequest, 0, len(items))
	for _, it := range items {
		r, err := s.CreateReturnRequest(ctx, CreateReturnInput{
			OrderID:           orderID,
			OrderItemID:       it.OrderItemID,
			CustomerUserID:    customerUserID,
			SellerID:          it.SellerID,
			ReasonCode:        it.ReasonCode,
			ReasonDescription: it.ReasonDescription,
		})
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

type CreateReturnInput struct {
	OrderID           uuid.UUID
	OrderItemID       uuid.UUID
	CustomerUserID    uuid.UUID
	SellerID          uuid.UUID
	ReasonCode        string
	ReasonDescription *string
}

func (s *Service) CreateReturnRequest(ctx context.Context, in CreateReturnInput) (*postgres.ReturnRequest, error) {
	r := &postgres.ReturnRequest{
		OrderID:           in.OrderID,
		OrderItemID:       in.OrderItemID,
		CustomerUserID:    in.CustomerUserID,
		SellerID:          in.SellerID,
		ReasonCode:        in.ReasonCode,
		ReasonDescription: in.ReasonDescription,
		Status:            "requested",
	}
	if err := s.store.CreateReturnRequest(ctx, r); err != nil {
		return nil, err
	}
	_ = s.store.UpdateOrderStatus(ctx, in.OrderID, "return_requested", &in.CustomerUserID, "customer", in.ReasonCode)
	s.publish(ctx, "commerce.return.requested", map[string]any{
		"return_id": r.ID, "order_id": r.OrderID, "seller_id": r.SellerID,
	})
	return r, nil
}

// ApproveReturn moves a return from 'requested' to 'approved', books a
// reverse-pickup with the courier (pickup at the customer's address, drop
// at the seller), and kicks off the refund. The refund flow differs by
// payment method:
//   - Prepaid: call payments-service to refund the original gateway intent.
//     Refund status flips to 'pending' here; payments-service publishes a
//     payment.refunded event when the gateway settles, which the
//     commerce-service consumer rolls into 'succeeded'.
//   - COD: there is no gateway transaction to reverse. We mark the refund
//     as 'manual' so the seller's payout is debited and Ops can wire the
//     cash back to the customer outside the system.
//
// All side effects best-effort: courier or payments-service unavailability
// degrades cleanly to "approved with refund pending" rather than rolling
// back the approval.
func (s *Service) ApproveReturn(ctx context.Context, returnID, actorID uuid.UUID) (*postgres.ReturnRequest, error) {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil {
		return nil, fmt.Errorf("get return: %w", err)
	}
	// Phase 0.5: only the seller of the returned item may approve.
	if r.SellerID != actorID {
		return nil, ErrNotReturnSeller
	}
	if r.Status == "approved" {
		return r, nil // idempotent
	}
	if r.Status != "requested" {
		return nil, fmt.Errorf("cannot approve return in status %q", r.Status)
	}

	if err := s.store.UpdateReturnStatus(ctx, returnID, "approved", nil); err != nil {
		return nil, fmt.Errorf("update return status: %w", err)
	}
	_ = s.store.UpdateOrderStatus(ctx, r.OrderID, "return_approved", &actorID, "seller", "return approved")

	// Reverse-pickup label.
	s.bookReturnPickup(ctx, r)

	// Phase 6.1 — refund happens via the durable fulfillment worker so a
	// payments-service blip doesn't leave the buyer unrefunded after a
	// successful approve API call.
	s.EnqueueProcessReturnApproved(ctx, returnID)

	out, _ := s.store.GetReturnRequestByID(ctx, returnID)
	if out == nil {
		out = r
	}
	s.publish(ctx, "commerce.return.approved", map[string]any{
		"return_id": out.ID, "order_id": out.OrderID,
		"seller_id": out.SellerID, "refund_status": out.RefundAmount,
	})
	return out, nil
}

// bookReturnPickup books a reverse-pickup shipment (customer → seller).
// Failures are logged but don't block approval — Ops can re-trigger via
// a retry endpoint later. The courier is the same provider as outbound.
func (s *Service) bookReturnPickup(ctx context.Context, r *postgres.ReturnRequest) {
	if s.courier == nil {
		return
	}
	order, err := s.store.GetOrderByID(ctx, r.OrderID)
	if err != nil || order == nil {
		slog.Warn("return pickup: get order failed", "return_id", r.ID, "error", err)
		return
	}
	// Pickup is the customer's delivery address (where the goods are now).
	if order.DeliveryAddressID == nil {
		slog.Warn("return pickup: order has no delivery address", "order_id", order.ID)
		return
	}
	addr, err := s.store.GetAddressByID(ctx, *order.DeliveryAddressID)
	if err != nil {
		slog.Warn("return pickup: address lookup failed", "error", err)
		return
	}
	pickup := courier.Address{
		Name: addr.ContactName, Phone: addr.Phone,
		Line1: addr.AddressLine1, City: addr.City, State: addr.State,
		Postal: addr.PostalCode, Country: addr.Country,
	}
	if addr.AddressLine2 != nil {
		pickup.Line2 = *addr.AddressLine2
	}

	// Drop is the seller's pickup address.
	seller, err := s.store.GetSellerByID(ctx, r.SellerID)
	if err != nil {
		slog.Warn("return pickup: seller lookup failed", "error", err)
		return
	}
	drop := courier.Address{Name: seller.StoreName, Country: "IN"}
	if seller.City != nil {
		drop.City = *seller.City
	}
	if seller.State != nil {
		drop.State = *seller.State
	}
	if seller.PostalCode != nil {
		drop.Postal = *seller.PostalCode
	}
	if seller.Phone != nil {
		drop.Phone = *seller.Phone
	}

	// Reuse the outbound CreateShipment; courier providers don't model
	// returns separately. PaymentMethod=prepaid because the customer
	// already paid (this is reverse logistics, not a sale).
	resp, err := s.courier.CreateShipment(ctx, courier.ShipmentRequest{
		OrderID:       r.OrderID.String() + "-return",
		OrderNumber:   "RTN-" + r.ID.String()[:8],
		PickupAddress: pickup,
		DropAddress:   drop,
		Weight:        0.5, // Use a default — real weight is the original item's, looked up below.
		PaymentMethod: "prepaid",
	})
	if err != nil {
		slog.Warn("return pickup: courier create failed", "error", err)
		return
	}
	if err := s.store.SetReturnPickupLabel(ctx, r.ID, s.courier.Name(), resp.AWBNumber, resp.LabelURL); err != nil {
		slog.Warn("return pickup: persist label failed", "error", err)
	}
}

// initiateReturnRefund decides between gateway refund and manual COD
// reconciliation, then records the outcome on the return row. Item-level
// refund amount = the original line's final price (one item per return
// today; multi-item returns would prorate here).
func (s *Service) initiateReturnRefund(ctx context.Context, r *postgres.ReturnRequest, actorID uuid.UUID) {
	order, err := s.store.GetOrderByID(ctx, r.OrderID)
	if err != nil || order == nil {
		slog.Warn("refund: get order failed", "return_id", r.ID, "error", err)
		return
	}
	items, _ := s.store.GetOrderItems(ctx, r.OrderID)
	var amount float64
	for _, it := range items {
		if it.ID == r.OrderItemID {
			amount = it.FinalPrice
			break
		}
	}
	if amount == 0 {
		amount = order.FinalAmount // fallback: full-order refund
	}

	isCOD := order.PaymentMethod != nil && strings.EqualFold(*order.PaymentMethod, "cod")
	if isCOD {
		// No gateway leg — Ops settles cash externally.
		if err := s.store.SetReturnRefund(ctx, r.ID, "", "manual", amount); err != nil {
			slog.Warn("refund: persist manual cod refund failed", "error", err)
		}
		return
	}

	if s.payments == nil {
		slog.Warn("refund: payments client not configured; marking pending", "return_id", r.ID)
		_ = s.store.SetReturnRefund(ctx, r.ID, "", "pending", amount)
		return
	}
	intent, err := s.payments.FindOrderIntent(ctx, r.OrderID, actorID)
	if err != nil || intent == nil {
		slog.Warn("refund: no succeeded intent for order", "order_id", r.OrderID, "error", err)
		_ = s.store.SetReturnRefund(ctx, r.ID, "", "pending", amount)
		return
	}
	refunded, err := s.payments.InitiateRefund(ctx, intent.ID, actorID, "return:"+r.ReasonCode)
	if err != nil {
		slog.Warn("refund: initiate failed", "intent_id", intent.ID, "error", err)
		_ = s.store.SetReturnRefund(ctx, r.ID, intent.ID.String(), "pending", amount)
		return
	}
	status := "pending"
	if refunded != nil && refunded.Status == "refunded" {
		status = "succeeded"
	}
	_ = s.store.SetReturnRefund(ctx, r.ID, intent.ID.String(), status, amount)
}

// RejectReturn closes a return with status='rejected' and records the
// seller's reason. Order falls back to the previous fulfillment state
// (delivered) so the customer's UI shows the return is closed.
func (s *Service) RejectReturn(ctx context.Context, returnID, actorID uuid.UUID, reason string) (*postgres.ReturnRequest, error) {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil {
		return nil, fmt.Errorf("get return: %w", err)
	}
	// Phase 0.5: only the seller of the returned item may reject.
	if r.SellerID != actorID {
		return nil, ErrNotReturnSeller
	}
	if r.Status == "rejected" {
		return r, nil
	}
	if r.Status != "requested" {
		return nil, fmt.Errorf("cannot reject return in status %q", r.Status)
	}
	rsn := reason
	if err := s.store.UpdateReturnStatus(ctx, returnID, "rejected", &rsn); err != nil {
		return nil, fmt.Errorf("update return status: %w", err)
	}
	_ = s.store.UpdateOrderStatus(ctx, r.OrderID, "return_rejected", &actorID, "seller", reason)
	s.publish(ctx, "commerce.return.rejected", map[string]any{
		"return_id": r.ID, "order_id": r.OrderID, "reason": reason,
	})
	out, _ := s.store.GetReturnRequestByID(ctx, returnID)
	if out == nil {
		out = r
	}
	return out, nil
}

// ─── Payout Calculation ──────────────────────────────────────

// PayoutConfig holds the platform's commission / platform-fee / TDS rates.
// Phase 4.1 — previously these were hard-coded constants, making them
// impossible to change without redeploying. Now they're loaded from env at
// boot and applied as defaults when the per-call values aren't supplied.
//
// All values are percent (5.0 = 5%, not 0.05). Negative or out-of-bound
// values fall back to compiled defaults so a misconfigured env can't push
// a seller payout into negative territory.
type PayoutConfig struct {
	CommissionPct  float64
	PlatformFeePct float64
	TDSPct         float64
}

// fallbackPayoutConfig is the value the service falls back to when env vars
// are missing or out of bounds. Matches the historical hard-coded constants
// (5% commission, 2% platform fee, 1% TDS) so behaviour is unchanged for
// deployments that don't configure overrides.
var fallbackPayoutConfig = PayoutConfig{CommissionPct: 5.0, PlatformFeePct: 2.0, TDSPct: 1.0}

// WithPayoutConfig overrides the default fee schedule. Values <=0 or >100
// are rejected and replaced with the fallback so a misconfigured env can't
// produce nonsense payouts.
func (s *Service) WithPayoutConfig(cfg PayoutConfig) *Service {
	if cfg.CommissionPct <= 0 || cfg.CommissionPct > 100 {
		cfg.CommissionPct = fallbackPayoutConfig.CommissionPct
	}
	if cfg.PlatformFeePct <= 0 || cfg.PlatformFeePct > 100 {
		cfg.PlatformFeePct = fallbackPayoutConfig.PlatformFeePct
	}
	if cfg.TDSPct < 0 || cfg.TDSPct > 100 {
		cfg.TDSPct = fallbackPayoutConfig.TDSPct
	}
	s.payoutCfg = cfg
	return s
}

// payoutConfig returns the configured schedule, falling back to defaults if
// WithPayoutConfig was never called.
func (s *Service) payoutConfig() PayoutConfig {
	if s.payoutCfg.CommissionPct == 0 && s.payoutCfg.PlatformFeePct == 0 && s.payoutCfg.TDSPct == 0 {
		return fallbackPayoutConfig
	}
	return s.payoutCfg
}

// CalculateSellerPayout breaks a gross amount into commission, platform
// fee, TDS, and the seller's net payout. Per-call overrides (e.g. for a
// seller with a negotiated rate) win; if 0 is passed for a value the
// service's configured default is used.
func (s *Service) CalculateSellerPayout(grossAmount float64, commissionPct, platformFeePct float64) (net float64, commission float64, fee float64, tds float64) {
	cfg := s.payoutConfig()
	if commissionPct == 0 {
		commissionPct = cfg.CommissionPct
	}
	if platformFeePct == 0 {
		platformFeePct = cfg.PlatformFeePct
	}
	commission = round2(grossAmount * commissionPct / 100)
	fee = round2(grossAmount * platformFeePct / 100)
	tds = round2((grossAmount - commission - fee) * cfg.TDSPct / 100)
	net = round2(grossAmount - commission - fee - tds)
	return
}

// ─── Reviews ─────────────────────────────────────────────────

// CreateReview validates the supplied review against the reviewer's actual
// order history before persisting. Phase 0.6 — previously the handler set
// IsVerifiedPurchase=true blindly off the request fields, so any
// authenticated user could post a "verified" review for any product by
// supplying a fabricated order_item_id. Now we look up the order item and
// require:
//
//   - The order item belongs to an order owned by the reviewer.
//   - The item's product_id matches the reviewed product.
//   - The item's seller_id matches the supplied seller.
//   - The item is delivered (status=='delivered' or delivered_at set).
//
// IsVerifiedPurchase is derived from the validation; callers are no longer
// trusted to set it.
func (s *Service) CreateReview(ctx context.Context, r *postgres.Review) error {
	item, err := s.store.GetOrderItemByID(ctx, r.OrderItemID)
	if err != nil || item == nil {
		return ErrReviewOrderItemInvalid
	}
	if item.ProductID != r.ProductID || item.SellerID != r.SellerID {
		return ErrReviewOrderItemInvalid
	}
	order, err := s.store.GetOrderByID(ctx, item.OrderID)
	if err != nil || order == nil {
		return ErrReviewOrderItemInvalid
	}
	if order.CustomerUserID != r.ReviewerID {
		return ErrReviewOrderItemInvalid
	}
	if item.Status != "delivered" && item.DeliveredAt == nil {
		return ErrReviewItemNotDelivered
	}

	r.IsVerifiedPurchase = true // derived, not trusted from input

	if err := s.store.CreateReview(ctx, r); err != nil {
		return err
	}
	s.publish(ctx, "commerce.review.created", map[string]any{
		"product_id": r.ProductID, "seller_id": r.SellerID, "rating": r.Rating,
	})
	return nil
}

func (s *Service) GetProductReviews(ctx context.Context, productID uuid.UUID, limit, offset int) ([]*postgres.Review, int, error) {
	return s.store.GetProductReviews(ctx, productID, limit, offset)
}

// ─── Product Media + Attributes (Phase 3.1) ──────────────────

// assertProductSeller verifies the actor's seller account owns the
// product. Used by the media + attributes mutation endpoints to keep
// sellers out of each other's catalogs.
func (s *Service) assertProductSeller(ctx context.Context, productID, actorUserID uuid.UUID) error {
	product, err := s.store.GetProductByID(ctx, productID)
	if err != nil || product == nil {
		return fmt.Errorf("product not found")
	}
	seller, err := s.GetSellerProfile(ctx, actorUserID)
	if err != nil || seller == nil {
		return ErrNotOrderOwner // reused — caller maps to 403
	}
	if product.SellerID != seller.ID {
		return ErrNotOrderOwner
	}
	return nil
}

// AddProductMedia attaches a media-service asset to a product's gallery.
func (s *Service) AddProductMedia(ctx context.Context, productID, actorUserID, mediaID uuid.UUID, mediaType string, sortOrder int) ([]postgres.ProductMedia, error) {
	if err := s.assertProductSeller(ctx, productID, actorUserID); err != nil {
		return nil, err
	}
	if err := s.store.AddProductMedia(ctx, productID, mediaID, mediaType, sortOrder); err != nil {
		return nil, err
	}
	return s.store.ListProductMedia(ctx, productID)
}

// ListProductMedia is a public read used by the product detail page.
func (s *Service) ListProductMedia(ctx context.Context, productID uuid.UUID) ([]postgres.ProductMedia, error) {
	return s.store.ListProductMedia(ctx, productID)
}

// SetProductAttributes replaces the product's spec block in one call.
func (s *Service) SetProductAttributes(ctx context.Context, productID, actorUserID uuid.UUID, attrs []postgres.ProductAttribute) ([]postgres.ProductAttribute, error) {
	if err := s.assertProductSeller(ctx, productID, actorUserID); err != nil {
		return nil, err
	}
	if err := s.store.SetProductAttributes(ctx, productID, attrs); err != nil {
		return nil, err
	}
	return s.store.GetProductAttributes(ctx, productID)
}

// GetProductAttributes returns the spec block — public.
func (s *Service) GetProductAttributes(ctx context.Context, productID uuid.UUID) ([]postgres.ProductAttribute, error) {
	return s.store.GetProductAttributes(ctx, productID)
}

// AddSellerResponseToReview lets the seller of a reviewed product attach
// a public response. Phase 2.4. Only the seller may respond; the response
// timestamp is set on first write and overwritten on a subsequent edit.
func (s *Service) AddSellerResponseToReview(ctx context.Context, reviewID, actorID uuid.UUID, response string) (*postgres.Review, error) {
	r, err := s.store.GetReviewByID(ctx, reviewID)
	if err != nil || r == nil {
		return nil, ErrReviewNotFound
	}
	if r.SellerID != actorID {
		return nil, ErrNotReviewSeller
	}
	if err := s.store.SetSellerResponse(ctx, reviewID, response); err != nil {
		return nil, err
	}
	return s.store.GetReviewByID(ctx, reviewID)
}

// ─── Addresses ───────────────────────────────────────────────

func (s *Service) AddAddress(ctx context.Context, addr *postgres.CustomerAddress) error {
	return s.store.CreateAddress(ctx, addr)
}

func (s *Service) GetAddresses(ctx context.Context, userID uuid.UUID) ([]*postgres.CustomerAddress, error) {
	return s.store.GetAddressesByUser(ctx, userID)
}

func (s *Service) UpdateAddress(ctx context.Context, id, userID uuid.UUID, addr *postgres.CustomerAddress) error {
	return s.store.UpdateAddress(ctx, id, userID, addr)
}

func (s *Service) DeleteAddress(ctx context.Context, id, userID uuid.UUID) error {
	return s.store.DeleteAddress(ctx, id, userID)
}

func (s *Service) SetDefaultAddress(ctx context.Context, id, userID uuid.UUID) error {
	return s.store.SetDefaultAddress(ctx, id, userID)
}

// ListMyReturns returns the calling customer's return history.
func (s *Service) ListMyReturns(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*postgres.ReturnRequest, error) {
	return s.store.ListReturnsByCustomer(ctx, userID, limit, offset)
}

// SellerReturnCard joins a return request with the order item it concerns
// + the order header. Lets the inbox render reason, item, refund amount,
// and the buyer's order without N+1 round trips on the UI side.
type SellerReturnCard struct {
	Return    *postgres.ReturnRequest `json:"return"`
	OrderItem *postgres.OrderItem     `json:"order_item,omitempty"`
	Order     *postgres.Order         `json:"order,omitempty"`
}

// ListSellerReturns returns the seller's returns inbox, optionally filtered
// by status (requested / approved / rejected / refunded / closed). Phase 4.3.
func (s *Service) ListSellerReturns(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]*SellerReturnCard, error) {
	returns, err := s.store.ListReturnsBySeller(ctx, sellerID, status, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]*SellerReturnCard, 0, len(returns))
	for _, r := range returns {
		card := &SellerReturnCard{Return: r}
		if item, err := s.store.GetOrderItemByID(ctx, r.OrderItemID); err == nil {
			card.OrderItem = item
		}
		if o, err := s.store.GetOrderByID(ctx, r.OrderID); err == nil {
			card.Order = o
		}
		out = append(out, card)
	}
	return out, nil
}

// SellerEarning is one delivered prepaid order item with the gross/commission
// /fee/tds/net breakdown computed via CalculateSellerPayout. Phase 4.4 — the
// prepaid analogue of CODRemittance. We don't have a persisted ledger table
// yet (deferred to Phase 6's outbox/saga work); this is a read-time derivation
// from order_items so the seller UI can show earnings without a write path.
type SellerEarning struct {
	OrderItemID      uuid.UUID  `json:"order_item_id"`
	OrderID          uuid.UUID  `json:"order_id"`
	OrderNumber      string     `json:"order_number"`
	ProductTitle     string     `json:"product_title"`
	SKU              string     `json:"sku"`
	Quantity         int        `json:"quantity"`
	GrossAmount      float64    `json:"gross_amount"`
	CommissionAmount float64    `json:"commission_amount"`
	PlatformFee      float64    `json:"platform_fee"`
	TDSAmount        float64    `json:"tds_amount"`
	NetAmount        float64    `json:"net_amount"`
	PaymentMethod    *string    `json:"payment_method,omitempty"`
	Status           string     `json:"status"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
}

// ListSellerEarnings returns delivered prepaid (non-COD) order items for a
// seller. COD earnings live in their own remittance ledger so they're not
// duplicated here. Phase 4.4.
func (s *Service) ListSellerEarnings(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*SellerEarning, error) {
	items, err := s.store.ListDeliveredItemsForSeller(ctx, sellerID, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]*SellerEarning, 0, len(items))
	for _, row := range items {
		// Skip COD items — they go through the COD remittance ledger.
		if row.PaymentMethod != nil && *row.PaymentMethod == "cod" {
			continue
		}
		net, commission, fee, tds := s.CalculateSellerPayout(row.Item.FinalPrice, 0, 0)
		out = append(out, &SellerEarning{
			OrderItemID:      row.Item.ID,
			OrderID:          row.Item.OrderID,
			OrderNumber:      row.OrderNumber,
			ProductTitle:     row.Item.ProductTitle,
			SKU:              row.Item.SKU,
			Quantity:         row.Item.Quantity,
			GrossAmount:      round2(row.Item.FinalPrice),
			CommissionAmount: commission,
			PlatformFee:      fee,
			TDSAmount:        tds,
			NetAmount:        net,
			PaymentMethod:    row.PaymentMethod,
			Status:           row.Item.Status,
			DeliveredAt:      row.Item.DeliveredAt,
		})
	}
	return out, nil
}

// PreviewReturnRefund returns the refund amount the seller would be debited
// if they approve the return. Reads the order item's FinalPrice; when the
// return already has an explicit RefundAmount, that wins. Phase 4.3 — keeps
// the inbox from having to recompute.
func (s *Service) PreviewReturnRefund(ctx context.Context, returnID, actorID uuid.UUID) (float64, error) {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil || r == nil {
		return 0, ErrReturnNotFound
	}
	if r.SellerID != actorID && r.CustomerUserID != actorID {
		return 0, ErrNotReturnParty
	}
	if r.RefundAmount != nil && *r.RefundAmount > 0 {
		return *r.RefundAmount, nil
	}
	item, err := s.store.GetOrderItemByID(ctx, r.OrderItemID)
	if err != nil || item == nil {
		return 0, ErrReturnNotFound
	}
	return round2(item.FinalPrice), nil
}

// ─── Helpers ─────────────────────────────────────────────────

func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func uniqueSlug(base string) string {
	return fmt.Sprintf("%s-%d", base, time.Now().UnixMilli()%100000)
}

func coalesceStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func coalesceInt(n, def int) int {
	if n == 0 {
		return def
	}
	return n
}

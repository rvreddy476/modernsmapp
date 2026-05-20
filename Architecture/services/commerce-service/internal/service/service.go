// Package service implements the core business logic for commerce-service.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/identity"
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
	store    *postgres.Store
	rdb      *redis.Client
	writer   *kafka.Writer
	courier  courier.Provider
	blob     BlobStore
	identity *identity.Client
	payments *payments.Client
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
	TaxClassID       *uuid.UUID
	Title            string
	ShortTitle       *string
	Description      *string
	ShortDescription *string
	ProductType      string
	Condition        string
	ReturnPolicyType string
	ReturnPolicyDays int
	HSNCode          *string
	WeightGrams      *int
	Variants         []CreateVariantInput
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
		SellerID:         in.SellerID,
		CategoryID:       in.CategoryID,
		TaxClassID:       in.TaxClassID,
		Title:            in.Title,
		ShortTitle:       in.ShortTitle,
		Slug:             uniqueSlug(slugify(in.Title)),
		Description:      in.Description,
		ShortDescription: in.ShortDescription,
		ProductType:      productType,
		Condition:        coalesceStr(in.Condition, "new"),
		Status:           "draft",
		Visibility:       "public",
		ApprovalStatus:   "draft",
		ReturnPolicyType: coalesceStr(in.ReturnPolicyType, "7_days"),
		ReturnPolicyDays: coalesceInt(in.ReturnPolicyDays, 7),
		HSNCode:          in.HSNCode,
		WeightGrams:      in.WeightGrams,
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

type CheckoutInput struct {
	UserID         uuid.UUID
	AddressID      uuid.UUID
	PaymentMethod  string
	CouponCode     string
	GiftMessage    *string
	IdempotencyKey string
}

func (s *Service) Checkout(ctx context.Context, in CheckoutInput) (*postgres.Order, error) {
	// 1. Get cart
	cart, err := s.store.GetOrCreateCart(ctx, in.UserID)
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

	// 2. Validate stock + build order items
	var orderItems []*postgres.OrderItem
	var subtotal float64
	var totalTax float64

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
			return nil, fmt.Errorf("insufficient stock for %s: available %d", product.Title, inv.AvailableQty())
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

	// 3. Apply coupon if provided.
	//
	// Audit O10: GetCouponByCode already filters by is_active / starts_at /
	// expires_at at the SQL layer, but the prior implementation never
	// enforced max_uses (global cap) or max_uses_per_user (per-user cap).
	// A single permissive coupon could be redeemed unboundedly. Now we
	// require uses_count < max_uses and the per-user usage from
	// coupon_usages to be below max_uses_per_user before applying.
	couponDiscount := 0.0
	var couponCodePtr *string
	if in.CouponCode != "" {
		c, err := s.store.GetCouponByCode(ctx, in.CouponCode)
		if err == nil && subtotal >= c.MinOrderAmount && couponWithinCaps(ctx, s, c, in.UserID) {
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
		couponCodePtr = &in.CouponCode
	}

	// 4. Shipping (flat ₹40, free above ₹499)
	shipping := 40.0
	if subtotal > 499 {
		shipping = 0
	}

	finalAmount := subtotal - couponDiscount + shipping + totalTax
	idempKey := in.IdempotencyKey
	if idempKey == "" {
		idempKey = fmt.Sprintf("%s-%d", in.UserID, time.Now().UnixNano())
	}

	// 5. Create order (idempotent).
	// COD orders skip the gateway: confirmed immediately, payment_status stays
	// "pending" (the orders_payment_status_check constraint allows
	// pending|processing|paid|failed|refund_pending|refunded|partially_refunded
	// — there is no cod_pending). Downstream code distinguishes COD by reading
	// payment_method='cod' instead.
	addrSnapshot, _ := json.Marshal(map[string]any{"address_id": in.AddressID})
	pm := in.PaymentMethod
	isCOD := strings.EqualFold(pm, "cod")
	paymentStatus := "pending"
	orderStatus := "payment_pending"
	if isCOD {
		orderStatus = "confirmed"
	}
	order := &postgres.Order{
		CustomerUserID:          in.UserID,
		Subtotal:                subtotal,
		DiscountAmount:          0,
		ShippingCharges:         shipping,
		TaxAmount:               totalTax,
		CouponCode:              couponCodePtr,
		CouponDiscount:          couponDiscount,
		FinalAmount:             finalAmount,
		CurrencyCode:            "INR",
		PaymentMethod:           &pm,
		PaymentStatus:           paymentStatus,
		DeliveryAddressID:       &in.AddressID,
		DeliveryAddressSnapshot: addrSnapshot,
		GiftMessage:             in.GiftMessage,
		Status:                  orderStatus,
		IdempotencyKey:          &idempKey,
	}

	if err := s.store.CreateOrder(ctx, order, orderItems); err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	// 6. Hard-reserve inventory (COD: immediately deduct since there is no gateway step).
	for _, ci := range cartItems {
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

	// 7. Clear cart
	_ = s.store.ClearCart(ctx, cart.ID)

	// 8. Apply coupon usage
	if couponCodePtr != nil {
		if c, err := s.store.GetCouponByCode(ctx, *couponCodePtr); err == nil {
			_ = s.store.IncrCouponUsage(ctx, c.ID, in.UserID, order.ID)
		}
	}

	buyerEmail, buyerName := s.resolveBuyer(ctx, in.UserID)
	// Resolve seller contact for seller-side notifications.
	var sellerEmail, sellerName string
	if len(orderItems) > 0 {
		if seller, err := s.store.GetSellerByID(ctx, orderItems[0].SellerID); err == nil {
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
		"seller_id":    sellerIDOrNil(orderItems),
		"amount":       order.FinalAmount,
		"seller_email": sellerEmail,
		"seller_name":  sellerName,
	})
	if isCOD {
		// COD is a zero-step payment: signal downstream so invoice + shipment
		// and seller notifications fire the same way as paid orders.
		s.publish(ctx, events.EventCommerceOrderPaid, map[string]any{
			"order_id":       order.ID,
			"order_number":   order.OrderNumber,
			"user_id":        in.UserID,
			"amount":         order.FinalAmount,
			"payment_method": "cod",
			"buyer_email":    buyerEmail,
			"buyer_name":     buyerName,
		})
		go s.fulfillPaidOrder(order.ID)
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

	// Best-effort fulfillment automation. Run in a detached goroutine so a
	// slow courier API or invoice render doesn't stall the payment callback.
	go s.fulfillPaidOrder(orderID)
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

	// Refund.
	s.initiateReturnRefund(ctx, r, actorID)

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

const defaultCommissionPct = 5.0  // 5% platform commission
const defaultPlatformFeePct = 2.0 // 2% platform fee

func (s *Service) CalculateSellerPayout(grossAmount float64, commissionPct, platformFeePct float64) (net float64, commission float64, fee float64, tds float64) {
	if commissionPct == 0 {
		commissionPct = defaultCommissionPct
	}
	if platformFeePct == 0 {
		platformFeePct = defaultPlatformFeePct
	}
	commission = round2(grossAmount * commissionPct / 100)
	fee = round2(grossAmount * platformFeePct / 100)
	tds = round2((grossAmount - commission - fee) * 0.01) // 1% TDS on net
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

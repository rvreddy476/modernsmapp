// Package service implements the core business logic for commerce-service.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/identity"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	kafka "github.com/segmentio/kafka-go"
)

// Service is the main commerce service.
type Service struct {
	store    *postgres.Store
	rdb      *redis.Client
	writer   *kafka.Writer
	courier  courier.Provider
	blob     BlobStore
	identity *identity.Client
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
		ApprovalStatus:   "pending",
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

func (s *Service) ListOrders(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*postgres.Order, error) {
	orders, _, err := s.store.GetOrdersByCustomer(ctx, userID, limit, offset)
	return orders, err
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

	// 3. Apply coupon if provided
	couponDiscount := 0.0
	var couponCodePtr *string
	if in.CouponCode != "" {
		c, err := s.store.GetCouponByCode(ctx, in.CouponCode)
		if err == nil && subtotal >= c.MinOrderAmount {
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
	// COD orders skip the gateway: confirmed immediately, payment_status=cod_pending.
	addrSnapshot, _ := json.Marshal(map[string]any{"address_id": in.AddressID})
	pm := in.PaymentMethod
	isCOD := strings.EqualFold(pm, "cod")
	paymentStatus := "pending"
	orderStatus := "payment_pending"
	if isCOD {
		paymentStatus = "cod_pending"
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

// ConfirmPayment is called after successful payment gateway callback.
func (s *Service) ConfirmPayment(ctx context.Context, orderID uuid.UUID, paymentID, gateway string) error {
	if err := s.store.UpdatePaymentStatus(ctx, orderID, "paid", paymentID, gateway); err != nil {
		return err
	}
	if err := s.store.UpdateOrderStatus(ctx, orderID, "confirmed", nil, "system", "payment confirmed"); err != nil {
		return err
	}

	// Deduct inventory (best-effort)
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

	// Best-effort fulfillment automation. Run in a detached goroutine so a slow
	// courier API or invoice render doesn't stall the payment callback.
	go s.fulfillPaidOrder(orderID)
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

func (s *Service) CreateReview(ctx context.Context, r *postgres.Review) error {
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

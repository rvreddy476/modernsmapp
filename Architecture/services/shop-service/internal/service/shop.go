package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/shop-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Service struct {
	store  *postgres.Store
	writer *kafka.Writer
}

func New(store *postgres.Store, kafkaBrokers string) *Service {
	w := &kafka.Writer{
		Addr:     kafka.TCP(kafkaBrokers),
		Topic:    "social.events.v1",
		Balancer: &kafka.LeastBytes{},
	}
	return &Service{store: store, writer: w}
}

func (s *Service) Close() {
	if s.writer != nil {
		s.writer.Close()
	}
}

// --- Products ---

type CreateProductInput struct {
	SellerID    uuid.UUID
	Title       string
	Description string
	Price       float64
	Currency    string
	Category    string
	MediaIDs    []string
	Stock       int
}

func (s *Service) CreateProduct(ctx context.Context, input *CreateProductInput) (*postgres.Product, error) {
	if input.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if input.Price <= 0 {
		return nil, fmt.Errorf("price must be positive")
	}
	if input.Stock < 0 {
		return nil, fmt.Errorf("stock cannot be negative")
	}

	now := time.Now()
	p := &postgres.Product{
		ID:          uuid.New(),
		SellerID:    input.SellerID,
		Title:       input.Title,
		Description: input.Description,
		Price:       input.Price,
		Currency:    input.Currency,
		Category:    input.Category,
		MediaIDs:    input.MediaIDs,
		Stock:       input.Stock,
		Status:      "active",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if p.Currency == "" {
		p.Currency = "USD"
	}

	if err := s.store.CreateProduct(ctx, p); err != nil {
		return nil, err
	}

	// Publish ProductListed event
	go s.publishEvent(ctx, "ProductListed", &input.SellerID, events.ProductListedPayload{
		ProductID: p.ID.String(),
		SellerID:  p.SellerID.String(),
		Title:     p.Title,
		Price:     p.Price,
		Currency:  p.Currency,
		Category:  p.Category,
		CreatedAt: p.CreatedAt,
	})

	return p, nil
}

func (s *Service) GetProduct(ctx context.Context, id uuid.UUID) (*postgres.Product, error) {
	return s.store.GetProduct(ctx, id)
}

func (s *Service) ListProducts(ctx context.Context, category string, limit, offset int) ([]postgres.Product, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.store.ListProducts(ctx, category, limit, offset)
}

func (s *Service) ListSellerProducts(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]postgres.Product, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.store.ListSellerProducts(ctx, sellerID, limit, offset)
}

type UpdateProductInput struct {
	Title       string
	Description string
	Category    string
	Status      string
	Price       float64
	Stock       int
}

func (s *Service) UpdateProduct(ctx context.Context, productID, sellerID uuid.UUID, input *UpdateProductInput) error {
	// Verify ownership
	p, err := s.store.GetProduct(ctx, productID)
	if err != nil {
		return err
	}
	if p.SellerID != sellerID {
		return fmt.Errorf("not the product owner")
	}

	status := input.Status
	if status == "" {
		status = p.Status
	}

	return s.store.UpdateProduct(ctx, productID, input.Title, input.Description, input.Category, status, input.Price, input.Stock)
}

// --- Cart ---

func (s *Service) AddToCart(ctx context.Context, userID, productID uuid.UUID, quantity int) error {
	if quantity <= 0 {
		return fmt.Errorf("quantity must be positive")
	}
	// Verify product exists and is active
	p, err := s.store.GetProduct(ctx, productID)
	if err != nil {
		return fmt.Errorf("product not found")
	}
	if p.Status != "active" {
		return fmt.Errorf("product is not available")
	}
	if p.Stock < quantity {
		return fmt.Errorf("insufficient stock")
	}
	return s.store.AddToCart(ctx, userID, productID, quantity)
}

func (s *Service) GetCart(ctx context.Context, userID uuid.UUID) ([]postgres.CartItem, error) {
	return s.store.GetCart(ctx, userID)
}

func (s *Service) RemoveFromCart(ctx context.Context, userID, productID uuid.UUID) error {
	return s.store.RemoveFromCart(ctx, userID, productID)
}

func (s *Service) ClearCart(ctx context.Context, userID uuid.UUID) error {
	return s.store.ClearCart(ctx, userID)
}

// --- Orders ---

func (s *Service) Checkout(ctx context.Context, buyerID uuid.UUID) (*postgres.Order, error) {
	// Get cart items
	items, err := s.store.GetCart(ctx, buyerID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("cart is empty")
	}

	// Group by seller
	sellerItems := make(map[uuid.UUID][]postgres.CartItem)
	for _, item := range items {
		if item.Product == nil {
			continue
		}
		sellerItems[item.Product.SellerID] = append(sellerItems[item.Product.SellerID], item)
	}

	// For simplicity, create one order per seller
	var lastOrder *postgres.Order
	for sellerID, cartItems := range sellerItems {
		var total float64
		var orderItems []postgres.OrderItem
		for _, ci := range cartItems {
			if ci.Product.Stock < ci.Quantity {
				return nil, fmt.Errorf("insufficient stock for %s", ci.Product.Title)
			}
			lineTotal := ci.Product.Price * float64(ci.Quantity)
			total += lineTotal
			orderItems = append(orderItems, postgres.OrderItem{
				ID:              uuid.New(),
				ProductID:       ci.ProductID,
				Quantity:        ci.Quantity,
				PriceAtPurchase: ci.Product.Price,
			})
		}

		now := time.Now()
		order := &postgres.Order{
			ID:        uuid.New(),
			BuyerID:   buyerID,
			SellerID:  sellerID,
			Status:    "pending",
			Total:     total,
			Currency:  cartItems[0].Product.Currency,
			CreatedAt: now,
			UpdatedAt: now,
		}

		if err := s.store.CreateOrder(ctx, order, orderItems); err != nil {
			return nil, err
		}

		// Publish OrderCreated event
		go s.publishEvent(ctx, "OrderCreated", &buyerID, events.OrderCreatedPayload{
			OrderID:   order.ID.String(),
			BuyerID:   order.BuyerID.String(),
			SellerID:  order.SellerID.String(),
			Total:     order.Total,
			Currency:  order.Currency,
			ItemCount: len(orderItems),
			CreatedAt: order.CreatedAt,
		})

		lastOrder = order
	}

	// Clear the cart
	_ = s.store.ClearCart(ctx, buyerID)

	return lastOrder, nil
}

func (s *Service) GetOrder(ctx context.Context, orderID, userID uuid.UUID) (*postgres.Order, error) {
	order, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	// Only buyer or seller can view order
	if order.BuyerID != userID && order.SellerID != userID {
		return nil, fmt.Errorf("not authorized to view this order")
	}
	return order, nil
}

func (s *Service) ListOrders(ctx context.Context, userID uuid.UUID, role string, limit, offset int) ([]postgres.Order, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.store.ListOrders(ctx, userID, role, limit, offset)
}

func (s *Service) UpdateOrderStatus(ctx context.Context, orderID, sellerID uuid.UUID, status string) error {
	order, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if order.SellerID != sellerID {
		return fmt.Errorf("not the order seller")
	}

	validTransitions := map[string][]string{
		"pending":    {"confirmed", "cancelled"},
		"confirmed":  {"shipped", "cancelled"},
		"shipped":    {"delivered"},
		"delivered":  {"completed"},
		"cancelled":  {},
		"completed":  {},
	}
	allowed := validTransitions[order.Status]
	valid := false
	for _, s := range allowed {
		if s == status {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid status transition from %s to %s", order.Status, status)
	}

	if err := s.store.UpdateOrderStatus(ctx, orderID, status); err != nil {
		return err
	}

	// Publish OrderStatusUpdated event
	go s.publishEvent(ctx, "OrderStatusUpdated", &sellerID, events.OrderStatusUpdatedPayload{
		OrderID:   orderID.String(),
		BuyerID:   order.BuyerID.String(),
		SellerID:  order.SellerID.String(),
		OldStatus: order.Status,
		NewStatus: status,
		UpdatedAt: time.Now(),
	})

	return nil
}

// --- Event Publishing ---

func (s *Service) publishEvent(ctx context.Context, eventType string, actorID *uuid.UUID, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Warning: failed to marshal %s payload: %v", eventType, err)
		return
	}

	var actorStr *string
	if actorID != nil {
		str := actorID.String()
		actorStr = &str
	}

	env := events.NewEnvelope(ctx, eventType, actorStr, data)

	envData, err := json.Marshal(env)
	if err != nil {
		log.Printf("Warning: failed to marshal %s envelope: %v", eventType, err)
		return
	}

	if err := s.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(eventType),
		Value: envData,
	}); err != nil {
		log.Printf("Warning: failed to publish %s event: %v", eventType, err)
	}
}

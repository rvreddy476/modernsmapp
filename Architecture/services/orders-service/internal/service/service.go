package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/orders-service/internal/store/postgres"
	"github.com/google/uuid"
	kafka "github.com/segmentio/kafka-go"
)

// ErrDisputeAlreadyOpen is returned when a dispute is already open for an order.
var ErrDisputeAlreadyOpen = errors.New("a dispute is already open for this order")

// ErrDisputeNotFound is returned when a dispute cannot be located.
var ErrDisputeNotFound = errors.New("dispute not found")

type Service struct {
	store              *postgres.Store
	writer             *kafka.Writer
	httpClient         *http.Client
	paymentsServiceURL string
	internalKey        string
}

func New(store *postgres.Store, kafkaBrokers string) *Service {
	return NewWithDialer(store, kafkaBrokers, nil)
}

func NewWithDialer(store *postgres.Store, kafkaBrokers string, dialer *kafka.Dialer) *Service {
	return &Service{
		store: store,
		writer: kafka.NewWriter(kafka.WriterConfig{
			Brokers:  strings.Split(kafkaBrokers, ","),
			Topic:    "social.events.v1",
			Balancer: &kafka.LeastBytes{},
			Dialer:   dialer,
		}),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// WithPaymentsService sets the payments service URL and internal key for cross-service calls.
func (s *Service) WithPaymentsService(url, internalKey string) *Service {
	s.paymentsServiceURL = url
	s.internalKey = internalKey
	return s
}

type CreateOrderInput struct {
	BuyerID         uuid.UUID
	SellerID        uuid.UUID
	ListingID       *uuid.UUID
	Items           []OrderItemInput
	Total           float64
	Currency        string
	ShippingAddress any
	Notes           string
}

type OrderItemInput struct {
	ListingID       uuid.UUID
	Title           string
	Quantity        int
	PriceAtPurchase float64
	Currency        string
}

func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (*postgres.Order, error) {
	if len(in.Items) == 0 {
		return nil, fmt.Errorf("order must have at least one item")
	}
	if in.Total <= 0 {
		return nil, fmt.Errorf("order total must be positive")
	}

	order := postgres.Order{
		BuyerID:         in.BuyerID,
		SellerID:        in.SellerID,
		ListingID:       in.ListingID,
		Total:           in.Total,
		Currency:        orDefault(in.Currency, "INR"),
		ShippingAddress: in.ShippingAddress,
		Notes:           in.Notes,
	}

	items := make([]postgres.OrderItem, len(in.Items))
	for i, it := range in.Items {
		items[i] = postgres.OrderItem{
			ListingID:       it.ListingID,
			Title:           it.Title,
			Quantity:        it.Quantity,
			PriceAtPurchase: it.PriceAtPurchase,
			Currency:        orDefault(it.Currency, "INR"),
		}
	}

	created, err := s.store.CreateOrder(ctx, order, items)
	if err != nil {
		return nil, err
	}

	s.publishEvent(ctx, "order.created", created.SellerID.String(), created)
	return created, nil
}

func (s *Service) GetOrder(ctx context.Context, orderID, requestorID uuid.UUID) (*postgres.Order, error) {
	order, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if order.BuyerID != requestorID && order.SellerID != requestorID {
		return nil, fmt.Errorf("forbidden")
	}
	return order, nil
}

func (s *Service) ListOrders(ctx context.Context, userID uuid.UUID, role string, limit, offset int) ([]postgres.Order, error) {
	return s.store.ListOrders(ctx, userID, role, limit, offset)
}

// ValidOrderTransitions defines allowed status transitions.
var ValidOrderTransitions = map[string][]string{
	"created":   {"confirmed", "cancelled"},
	"confirmed": {"shipped", "cancelled"},
	"shipped":   {"delivered"},
	"delivered": {"completed"},
}

func (s *Service) UpdateOrderStatus(ctx context.Context, orderID, actorID uuid.UUID, newStatus string) (*postgres.Order, error) {
	order, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}

	allowed := ValidOrderTransitions[order.Status]
	valid := false
	for _, a := range allowed {
		if a == newStatus {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("invalid transition: %s -> %s", order.Status, newStatus)
	}

	if err := s.store.UpdateOrderStatus(ctx, orderID, actorID, newStatus); err != nil {
		return nil, err
	}
	order.Status = newStatus
	s.publishEvent(ctx, "order.status_changed", actorID.String(), map[string]any{
		"order_id":   orderID,
		"new_status": newStatus,
	})
	return order, nil
}

func (s *Service) CreateBooking(ctx context.Context, b postgres.Booking) (*postgres.Booking, error) {
	if b.SlotStart.IsZero() || b.SlotEnd.IsZero() {
		return nil, fmt.Errorf("slot_start and slot_end are required")
	}
	if b.SlotEnd.Before(b.SlotStart) {
		return nil, fmt.Errorf("slot_end must be after slot_start")
	}
	created, err := s.store.CreateBooking(ctx, b)
	if err != nil {
		return nil, err
	}
	s.publishEvent(ctx, "booking.created", created.ProviderID.String(), created)
	return created, nil
}

func (s *Service) GetBooking(ctx context.Context, bookingID, requestorID uuid.UUID) (*postgres.Booking, error) {
	b, err := s.store.GetBooking(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if b.CustomerID != requestorID && b.ProviderID != requestorID {
		return nil, fmt.Errorf("forbidden")
	}
	return b, nil
}

func (s *Service) ListBookings(ctx context.Context, userID uuid.UUID, role string, limit, offset int) ([]postgres.Booking, error) {
	return s.store.ListBookings(ctx, userID, role, limit, offset)
}

func (s *Service) UpdateBookingStatus(ctx context.Context, bookingID, actorID uuid.UUID, newStatus string) (*postgres.Booking, error) {
	b, err := s.store.GetBooking(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if b.CustomerID != actorID && b.ProviderID != actorID {
		return nil, fmt.Errorf("forbidden")
	}
	if err := s.store.UpdateBookingStatus(ctx, bookingID, newStatus); err != nil {
		return nil, err
	}
	b.Status = newStatus
	s.publishEvent(ctx, "booking.status_changed", actorID.String(), map[string]any{
		"booking_id": bookingID,
		"new_status": newStatus,
	})
	return b, nil
}

func (s *Service) OpenDispute(ctx context.Context, orderID, openedBy uuid.UUID, reason string, evidenceURLs []string) (*postgres.Dispute, error) {
	opened, err := s.store.MarkDisputeOpened(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !opened {
		return nil, ErrDisputeAlreadyOpen
	}

	if err := s.store.UpdateOrderStatus(ctx, orderID, openedBy, "disputed"); err != nil {
		return nil, err
	}

	d, err := s.store.CreateDispute(ctx, postgres.Dispute{
		OrderID:      orderID,
		OpenedBy:     openedBy,
		Reason:       reason,
		EvidenceURLs: evidenceURLs,
	})
	if err != nil {
		return nil, err
	}
	s.publishEvent(ctx, "dispute.opened", openedBy.String(), d)
	return d, nil
}

func (s *Service) ResolveDispute(ctx context.Context, disputeID uuid.UUID, resolution string, refundAmount int64) error {
	dispute, err := s.store.GetDispute(ctx, disputeID)
	if err != nil {
		return ErrDisputeNotFound
	}

	if err := s.store.UpdateDisputeStatus(ctx, disputeID, "resolved", resolution); err != nil {
		return err
	}

	if refundAmount > 0 && dispute.PaymentIntentID != "" {
		refundURL := fmt.Sprintf("%s/v1/payments/intents/%s/refund", s.paymentsServiceURL, dispute.PaymentIntentID)
		body, _ := json.Marshal(map[string]interface{}{"amount": refundAmount})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, refundURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Service-Key", s.internalKey)
		resp, err := s.httpClient.Do(req)
		if err != nil {
			slog.Error("orders: failed to trigger refund", "dispute_id", disputeID, "error", err)
		} else {
			resp.Body.Close()
		}
	}

	s.publishEvent(ctx, "dispute.resolved", disputeID.String(), map[string]interface{}{
		"dispute_id":    disputeID,
		"resolution":    resolution,
		"refund_amount": refundAmount,
		"occurred_at":   time.Now(),
	})

	return nil
}

// UpdateDisputeStatus transitions a dispute to a new status.
func (s *Service) UpdateDisputeStatus(ctx context.Context, disputeID uuid.UUID, status, notes string) error {
	return s.store.UpdateDisputeStatus(ctx, disputeID, status, notes)
}

func (s *Service) publishEvent(ctx context.Context, eventType, key string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("failed to marshal event", "event_type", eventType, "error", err)
		return
	}
	if err := s.writer.WriteMessages(ctx, kafka.Message{
		Key:     []byte(key),
		Value:   data,
		Headers: []kafka.Header{{Key: "event_type", Value: []byte(eventType)}},
	}); err != nil {
		slog.Error("failed to publish event", "event_type", eventType, "error", err)
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

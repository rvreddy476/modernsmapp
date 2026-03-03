package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// Order represents an orders.orders row.
type Order struct {
	ID              uuid.UUID  `json:"id"`
	BuyerID         uuid.UUID  `json:"buyer_id"`
	SellerID        uuid.UUID  `json:"seller_id"`
	ListingID       *uuid.UUID `json:"listing_id,omitempty"`
	Status          string     `json:"status"`
	Total           float64    `json:"total"`
	Currency        string     `json:"currency"`
	ShippingAddress any        `json:"shipping_address,omitempty"`
	Notes           string     `json:"notes"`
	PaymentIntentID *uuid.UUID `json:"payment_intent_id,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type OrderItem struct {
	ID               uuid.UUID `json:"id"`
	OrderID          uuid.UUID `json:"order_id"`
	ListingID        uuid.UUID `json:"listing_id"`
	Title            string    `json:"title"`
	Quantity         int       `json:"quantity"`
	PriceAtPurchase  float64   `json:"price_at_purchase"`
	Currency         string    `json:"currency"`
}

type Booking struct {
	ID               uuid.UUID  `json:"id"`
	CustomerID       uuid.UUID  `json:"customer_id"`
	ProviderID       uuid.UUID  `json:"provider_id"`
	ServiceListingID uuid.UUID  `json:"service_listing_id"`
	SlotStart        time.Time  `json:"slot_start"`
	SlotEnd          time.Time  `json:"slot_end"`
	Status           string     `json:"status"`
	PaymentIntentID  *uuid.UUID `json:"payment_intent_id,omitempty"`
	Notes            string     `json:"notes"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type Dispute struct {
	ID           uuid.UUID  `json:"id"`
	OrderID      uuid.UUID  `json:"order_id"`
	OpenedBy     uuid.UUID  `json:"opened_by"`
	Reason       string     `json:"reason"`
	Status       string     `json:"status"`
	Resolution   string     `json:"resolution,omitempty"`
	EvidenceURLs []string   `json:"evidence_urls"`
	CreatedAt    time.Time  `json:"created_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
}

// CreateOrder inserts a new order with items in a single transaction.
func (s *Store) CreateOrder(ctx context.Context, order Order, items []OrderItem) (*Order, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	order.ID = uuid.New()
	err = tx.QueryRow(ctx,
		`INSERT INTO orders.orders (id, buyer_id, seller_id, listing_id, status, total, currency, shipping_address, notes)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING id, buyer_id, seller_id, listing_id, status, total, currency, notes, created_at, updated_at`,
		order.ID, order.BuyerID, order.SellerID, order.ListingID, "created",
		order.Total, order.Currency, order.ShippingAddress, order.Notes,
	).Scan(&order.ID, &order.BuyerID, &order.SellerID, &order.ListingID,
		&order.Status, &order.Total, &order.Currency, &order.Notes,
		&order.CreatedAt, &order.UpdatedAt)
	if err != nil {
		return nil, err
	}

	for i := range items {
		items[i].ID = uuid.New()
		items[i].OrderID = order.ID
		_, err = tx.Exec(ctx,
			`INSERT INTO orders.order_items (id, order_id, listing_id, title, quantity, price_at_purchase, currency)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			items[i].ID, items[i].OrderID, items[i].ListingID, items[i].Title,
			items[i].Quantity, items[i].PriceAtPurchase, items[i].Currency,
		)
		if err != nil {
			return nil, err
		}
	}

	return &order, tx.Commit(ctx)
}

// GetOrder fetches an order by ID.
func (s *Store) GetOrder(ctx context.Context, orderID uuid.UUID) (*Order, error) {
	var o Order
	err := s.db.QueryRow(ctx,
		`SELECT id, buyer_id, seller_id, listing_id, status, total, currency, notes, payment_intent_id, created_at, updated_at
		 FROM orders.orders WHERE id = $1`,
		orderID,
	).Scan(&o.ID, &o.BuyerID, &o.SellerID, &o.ListingID, &o.Status, &o.Total, &o.Currency,
		&o.Notes, &o.PaymentIntentID, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// ListOrders lists orders for a buyer or seller with pagination.
func (s *Store) ListOrders(ctx context.Context, userID uuid.UUID, role string, limit, offset int) ([]Order, error) {
	col := "buyer_id"
	if role == "seller" {
		col = "seller_id"
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, buyer_id, seller_id, listing_id, status, total, currency, notes, payment_intent_id, created_at, updated_at
		 FROM orders.orders WHERE `+col+` = $1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.BuyerID, &o.SellerID, &o.ListingID, &o.Status, &o.Total, &o.Currency,
			&o.Notes, &o.PaymentIntentID, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// UpdateOrderStatus transitions an order to a new status.
func (s *Store) UpdateOrderStatus(ctx context.Context, orderID, actorID uuid.UUID, newStatus string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE orders.orders SET status = $1, updated_at = NOW() WHERE id = $2`,
		newStatus, orderID,
	)
	return err
}

// CreateBooking inserts a new service booking.
func (s *Store) CreateBooking(ctx context.Context, b Booking) (*Booking, error) {
	b.ID = uuid.New()
	err := s.db.QueryRow(ctx,
		`INSERT INTO orders.bookings (id, customer_id, provider_id, service_listing_id, slot_start, slot_end, status, notes)
		 VALUES ($1,$2,$3,$4,$5,$6,'pending',$7)
		 RETURNING id, customer_id, provider_id, service_listing_id, slot_start, slot_end, status, notes, created_at, updated_at`,
		b.ID, b.CustomerID, b.ProviderID, b.ServiceListingID, b.SlotStart, b.SlotEnd, b.Notes,
	).Scan(&b.ID, &b.CustomerID, &b.ProviderID, &b.ServiceListingID,
		&b.SlotStart, &b.SlotEnd, &b.Status, &b.Notes, &b.CreatedAt, &b.UpdatedAt)
	return &b, err
}

// GetBooking fetches a booking by ID.
func (s *Store) GetBooking(ctx context.Context, bookingID uuid.UUID) (*Booking, error) {
	var b Booking
	err := s.db.QueryRow(ctx,
		`SELECT id, customer_id, provider_id, service_listing_id, slot_start, slot_end, status, notes, payment_intent_id, created_at, updated_at
		 FROM orders.bookings WHERE id = $1`,
		bookingID,
	).Scan(&b.ID, &b.CustomerID, &b.ProviderID, &b.ServiceListingID,
		&b.SlotStart, &b.SlotEnd, &b.Status, &b.Notes, &b.PaymentIntentID, &b.CreatedAt, &b.UpdatedAt)
	return &b, err
}

// ListBookings lists bookings for a customer or provider.
func (s *Store) ListBookings(ctx context.Context, userID uuid.UUID, role string, limit, offset int) ([]Booking, error) {
	col := "customer_id"
	if role == "provider" {
		col = "provider_id"
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, customer_id, provider_id, service_listing_id, slot_start, slot_end, status, notes, payment_intent_id, created_at, updated_at
		 FROM orders.bookings WHERE `+col+` = $1
		 ORDER BY slot_start ASC LIMIT $2 OFFSET $3`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var bookings []Booking
	for rows.Next() {
		var b Booking
		if err := rows.Scan(&b.ID, &b.CustomerID, &b.ProviderID, &b.ServiceListingID,
			&b.SlotStart, &b.SlotEnd, &b.Status, &b.Notes, &b.PaymentIntentID, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		bookings = append(bookings, b)
	}
	return bookings, rows.Err()
}

// UpdateBookingStatus transitions a booking status.
func (s *Store) UpdateBookingStatus(ctx context.Context, bookingID uuid.UUID, newStatus string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE orders.bookings SET status = $1, updated_at = NOW() WHERE id = $2`,
		newStatus, bookingID,
	)
	return err
}

// CreateDispute opens a dispute for an order.
func (s *Store) CreateDispute(ctx context.Context, d Dispute) (*Dispute, error) {
	d.ID = uuid.New()
	err := s.db.QueryRow(ctx,
		`INSERT INTO orders.disputes (id, order_id, opened_by, reason, status, evidence_urls)
		 VALUES ($1,$2,$3,$4,'open',$5)
		 RETURNING id, order_id, opened_by, reason, status, evidence_urls, created_at`,
		d.ID, d.OrderID, d.OpenedBy, d.Reason, d.EvidenceURLs,
	).Scan(&d.ID, &d.OrderID, &d.OpenedBy, &d.Reason, &d.Status, &d.EvidenceURLs, &d.CreatedAt)
	return &d, err
}

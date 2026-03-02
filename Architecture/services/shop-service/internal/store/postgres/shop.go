package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Product struct {
	ID          uuid.UUID `json:"id"`
	SellerID    uuid.UUID `json:"seller_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Price       float64   `json:"price"`
	Currency    string    `json:"currency"`
	Category    string    `json:"category"`
	MediaIDs    []string  `json:"media_ids"`
	Stock       int       `json:"stock"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CartItem struct {
	UserID    uuid.UUID `json:"user_id"`
	ProductID uuid.UUID `json:"product_id"`
	Quantity  int       `json:"quantity"`
	AddedAt   time.Time `json:"added_at"`
	Product   *Product  `json:"product,omitempty"`
}

type Order struct {
	ID        uuid.UUID   `json:"id"`
	BuyerID   uuid.UUID   `json:"buyer_id"`
	SellerID  uuid.UUID   `json:"seller_id"`
	Status    string      `json:"status"`
	Total     float64     `json:"total"`
	Currency  string      `json:"currency"`
	Items     []OrderItem `json:"items,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

type OrderItem struct {
	ID              uuid.UUID `json:"id"`
	OrderID         uuid.UUID `json:"order_id"`
	ProductID       uuid.UUID `json:"product_id"`
	Quantity        int       `json:"quantity"`
	PriceAtPurchase float64   `json:"price_at_purchase"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// --- Products ---

func (s *Store) CreateProduct(ctx context.Context, p *Product) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO shop.products (id, seller_id, title, description, price, currency, category, stock, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
	`, p.ID, p.SellerID, p.Title, p.Description, p.Price, p.Currency, p.Category, p.Stock, p.Status, p.CreatedAt)
	return err
}

func (s *Store) GetProduct(ctx context.Context, id uuid.UUID) (*Product, error) {
	var p Product
	err := s.db.QueryRow(ctx, `
		SELECT id, seller_id, title, description, price, currency, category, stock, status, created_at, updated_at
		FROM shop.products WHERE id = $1
	`, id).Scan(&p.ID, &p.SellerID, &p.Title, &p.Description, &p.Price, &p.Currency, &p.Category, &p.Stock, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) ListProducts(ctx context.Context, category string, limit, offset int) ([]Product, int, error) {
	var total int
	countQ := `SELECT COUNT(*) FROM shop.products WHERE status = 'active'`
	args := []interface{}{}
	if category != "" {
		countQ += ` AND category = $1`
		args = append(args, category)
	}
	_ = s.db.QueryRow(ctx, countQ, args...).Scan(&total)

	query := `SELECT id, seller_id, title, description, price, currency, category, stock, status, created_at, updated_at
		FROM shop.products WHERE status = 'active'`
	if category != "" {
		query += ` AND category = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		args = append(args, limit, offset)
	} else {
		query += ` ORDER BY created_at DESC LIMIT $1 OFFSET $2`
		args = append(args, limit, offset)
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.SellerID, &p.Title, &p.Description, &p.Price, &p.Currency, &p.Category, &p.Stock, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, 0, err
		}
		products = append(products, p)
	}
	return products, total, nil
}

func (s *Store) ListSellerProducts(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]Product, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, seller_id, title, description, price, currency, category, stock, status, created_at, updated_at
		FROM shop.products WHERE seller_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, sellerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.SellerID, &p.Title, &p.Description, &p.Price, &p.Currency, &p.Category, &p.Stock, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, nil
}

func (s *Store) UpdateProduct(ctx context.Context, id uuid.UUID, title, description, category, status string, price float64, stock int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE shop.products SET title=$2, description=$3, category=$4, status=$5, price=$6, stock=$7, updated_at=NOW()
		WHERE id=$1
	`, id, title, description, category, status, price, stock)
	return err
}

// --- Cart ---

func (s *Store) AddToCart(ctx context.Context, userID, productID uuid.UUID, quantity int) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO shop.cart_items (user_id, product_id, quantity, added_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id, product_id) DO UPDATE SET quantity = shop.cart_items.quantity + EXCLUDED.quantity
	`, userID, productID, quantity)
	return err
}

func (s *Store) GetCart(ctx context.Context, userID uuid.UUID) ([]CartItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.user_id, c.product_id, c.quantity, c.added_at,
		       p.id, p.seller_id, p.title, p.description, p.price, p.currency, p.category, p.stock, p.status, p.created_at, p.updated_at
		FROM shop.cart_items c JOIN shop.products p ON c.product_id = p.id
		WHERE c.user_id = $1 ORDER BY c.added_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []CartItem
	for rows.Next() {
		var ci CartItem
		var p Product
		if err := rows.Scan(&ci.UserID, &ci.ProductID, &ci.Quantity, &ci.AddedAt,
			&p.ID, &p.SellerID, &p.Title, &p.Description, &p.Price, &p.Currency, &p.Category, &p.Stock, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		ci.Product = &p
		items = append(items, ci)
	}
	return items, nil
}

func (s *Store) RemoveFromCart(ctx context.Context, userID, productID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM shop.cart_items WHERE user_id = $1 AND product_id = $2`, userID, productID)
	return err
}

func (s *Store) ClearCart(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM shop.cart_items WHERE user_id = $1`, userID)
	return err
}

// --- Orders ---

func (s *Store) CreateOrder(ctx context.Context, order *Order, items []OrderItem) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO shop.orders (id, buyer_id, seller_id, status, total, currency, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
	`, order.ID, order.BuyerID, order.SellerID, order.Status, order.Total, order.Currency, order.CreatedAt)
	if err != nil {
		return err
	}

	for _, item := range items {
		_, err = tx.Exec(ctx, `
			INSERT INTO shop.order_items (id, order_id, product_id, quantity, price_at_purchase)
			VALUES ($1, $2, $3, $4, $5)
		`, item.ID, order.ID, item.ProductID, item.Quantity, item.PriceAtPurchase)
		if err != nil {
			return err
		}
		// Decrement stock
		_, err = tx.Exec(ctx, `UPDATE shop.products SET stock = stock - $1 WHERE id = $2`, item.Quantity, item.ProductID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) GetOrder(ctx context.Context, orderID uuid.UUID) (*Order, error) {
	var o Order
	err := s.db.QueryRow(ctx, `
		SELECT id, buyer_id, seller_id, status, total, currency, created_at, updated_at
		FROM shop.orders WHERE id = $1
	`, orderID).Scan(&o.ID, &o.BuyerID, &o.SellerID, &o.Status, &o.Total, &o.Currency, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, order_id, product_id, quantity, price_at_purchase FROM shop.order_items WHERE order_id = $1
	`, orderID)
	if err != nil {
		return &o, nil
	}
	defer rows.Close()

	for rows.Next() {
		var item OrderItem
		if err := rows.Scan(&item.ID, &item.OrderID, &item.ProductID, &item.Quantity, &item.PriceAtPurchase); err != nil {
			continue
		}
		o.Items = append(o.Items, item)
	}
	return &o, nil
}

func (s *Store) ListOrders(ctx context.Context, userID uuid.UUID, role string, limit, offset int) ([]Order, error) {
	var col string
	if role == "seller" {
		col = "seller_id"
	} else {
		col = "buyer_id"
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, buyer_id, seller_id, status, total, currency, created_at, updated_at
		FROM shop.orders WHERE `+col+` = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.BuyerID, &o.SellerID, &o.Status, &o.Total, &o.Currency, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, nil
}

func (s *Store) UpdateOrderStatus(ctx context.Context, orderID uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `UPDATE shop.orders SET status = $2, updated_at = NOW() WHERE id = $1`, orderID, status)
	return err
}

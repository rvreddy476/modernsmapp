// Orders: creating an order, reading it by id or idempotency key, the
// customer cancel rule, order cards and lists for buyers and sellers, status
// updates and order items. Moved out of store.go unchanged.

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ─── Orders ──────────────────────────────────────────────────

func (s *Store) CreateOrder(ctx context.Context, o *Order, items []*OrderItem) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	o.ID = uuid.New()
	o.CreatedAt = time.Now()
	o.UpdatedAt = time.Now()

	// Generate human-readable order number
	var orderNum string
	if err = tx.QueryRow(ctx, `SELECT generate_order_number()`).Scan(&orderNum); err != nil {
		return fmt.Errorf("generate order number: %w", err)
	}
	o.OrderNumber = orderNum

	addrSnapshot, _ := json.Marshal(o.DeliveryAddressSnapshot)

	if _, err = tx.Exec(ctx, `
		INSERT INTO orders (id,customer_user_id,order_number,subtotal,discount_amount,shipping_charges,
		  tax_amount,coupon_code,coupon_discount,final_amount,currency_code,payment_method,payment_status,
		  delivery_address_id,delivery_address_snapshot,gift_message,status,idempotency_key,created_at,updated_at,
		  organization_id,po_number,cost_center,billing_address_snapshot,invoice_email,
		  approval_status,credit_terms_days,payment_due_date)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,
		        $21,$22,$23,$24,$25,$26,$27,$28)`,
		o.ID, o.CustomerUserID, o.OrderNumber, o.Subtotal, o.DiscountAmount, o.ShippingCharges,
		o.TaxAmount, o.CouponCode, o.CouponDiscount, o.FinalAmount, o.CurrencyCode,
		o.PaymentMethod, o.PaymentStatus, o.DeliveryAddressID, addrSnapshot, o.GiftMessage,
		o.Status, o.IdempotencyKey, o.CreatedAt, o.UpdatedAt,
		o.OrganizationID, o.PONumber, o.CostCenter, o.BillingAddressSnapshot, o.InvoiceEmail,
		o.ApprovalStatus, o.CreditTermsDays, o.PaymentDueDate,
	); err != nil {
		return fmt.Errorf("insert order: %w", err)
	}

	for _, item := range items {
		item.ID = uuid.New()
		item.OrderID = o.ID
		item.CreatedAt = time.Now()
		varDetails, _ := json.Marshal(item.VariantDetails)
		if _, err = tx.Exec(ctx, `
			INSERT INTO order_items (id,order_id,product_id,variant_id,seller_id,product_title,
			  variant_details,sku,quantity,unit_mrp,unit_price,discount_amount,tax_amount,
			  final_price,status,return_eligible_until,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
			item.ID, item.OrderID, item.ProductID, item.VariantID, item.SellerID,
			item.ProductTitle, varDetails, item.SKU, item.Quantity,
			item.UnitMRP, item.UnitPrice, item.DiscountAmount, item.TaxAmount,
			item.FinalPrice, item.Status, item.ReturnEligibleUntil, item.CreatedAt,
		); err != nil {
			return fmt.Errorf("insert order item: %w", err)
		}
	}

	// Record initial status
	if _, err = tx.Exec(ctx, `
		INSERT INTO order_status_history (id,order_id,to_status,actor_type,created_at)
		VALUES (gen_random_uuid(),$1,$2,'system',NOW())`, o.ID, o.Status,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetOrderByIdempotencyKey returns the order (if any) created by `userID`
// against `key`. Underlies H4 — Checkout uses this to short-circuit a
// retried/double-tap checkout into the original order. Returns (nil, nil)
// when no row matches.
func (s *Store) GetOrderByIdempotencyKey(ctx context.Context, userID uuid.UUID, key string) (*Order, error) {
	var o Order
	err := s.db.QueryRow(ctx, `SELECT id,customer_user_id,order_number,subtotal,discount_amount,
		shipping_charges,tax_amount,coupon_code,coupon_discount,final_amount,currency_code,
		payment_method,payment_status,payment_id,payment_gateway,delivery_address_id,
		delivery_address_snapshot,gift_message,status,cancellation_reason,cancelled_by,
		idempotency_key,created_at,updated_at,
		organization_id,po_number,cost_center,billing_address_snapshot,invoice_email,
		approval_status,approved_by_user_id,approved_at,approval_notes,credit_terms_days,payment_due_date
		FROM orders WHERE customer_user_id=$1 AND idempotency_key=$2`, userID, key).Scan(
		&o.ID, &o.CustomerUserID, &o.OrderNumber, &o.Subtotal, &o.DiscountAmount,
		&o.ShippingCharges, &o.TaxAmount, &o.CouponCode, &o.CouponDiscount, &o.FinalAmount,
		&o.CurrencyCode, &o.PaymentMethod, &o.PaymentStatus, &o.PaymentID, &o.PaymentGateway,
		&o.DeliveryAddressID, &o.DeliveryAddressSnapshot, &o.GiftMessage, &o.Status,
		&o.CancellationReason, &o.CancelledBy, &o.IdempotencyKey, &o.CreatedAt, &o.UpdatedAt,
		&o.OrganizationID, &o.PONumber, &o.CostCenter, &o.BillingAddressSnapshot, &o.InvoiceEmail,
		&o.ApprovalStatus, &o.ApprovedByUserID, &o.ApprovedAt, &o.ApprovalNotes,
		&o.CreditTermsDays, &o.PaymentDueDate,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

func (s *Store) GetOrderByID(ctx context.Context, id uuid.UUID) (*Order, error) {
	var o Order
	err := s.db.QueryRow(ctx, `SELECT id,customer_user_id,order_number,subtotal,discount_amount,
		shipping_charges,tax_amount,coupon_code,coupon_discount,final_amount,currency_code,
		payment_method,payment_status,payment_id,payment_gateway,delivery_address_id,
		delivery_address_snapshot,gift_message,status,cancellation_reason,cancelled_by,
		idempotency_key,created_at,updated_at,
		organization_id,po_number,cost_center,billing_address_snapshot,invoice_email,
		approval_status,approved_by_user_id,approved_at,approval_notes,credit_terms_days,payment_due_date,
		-- The columns migration 007 made authoritative. Reading the order
		-- without them is what left ConfirmPayment expecting ₹0 and the
		-- seller's order detail summing a zero subtotal.
		COALESCE(subtotal_minor,0),COALESCE(discount_amount_minor,0),
		COALESCE(shipping_charges_minor,0),COALESCE(tax_amount_minor,0),
		COALESCE(coupon_discount_minor,0),COALESCE(final_amount_minor,0),
		delivery_address_snapshot_enc
		FROM orders WHERE id=$1`, id).Scan(
		&o.ID, &o.CustomerUserID, &o.OrderNumber, &o.Subtotal, &o.DiscountAmount,
		&o.ShippingCharges, &o.TaxAmount, &o.CouponCode, &o.CouponDiscount, &o.FinalAmount,
		&o.CurrencyCode, &o.PaymentMethod, &o.PaymentStatus, &o.PaymentID, &o.PaymentGateway,
		&o.DeliveryAddressID, &o.DeliveryAddressSnapshot, &o.GiftMessage, &o.Status,
		&o.CancellationReason, &o.CancelledBy, &o.IdempotencyKey, &o.CreatedAt, &o.UpdatedAt,
		&o.OrganizationID, &o.PONumber, &o.CostCenter, &o.BillingAddressSnapshot, &o.InvoiceEmail,
		&o.ApprovalStatus, &o.ApprovedByUserID, &o.ApprovedAt, &o.ApprovalNotes,
		&o.CreditTermsDays, &o.PaymentDueDate,
		&o.SubtotalMinor, &o.DiscountAmountMinor, &o.ShippingChargesMinor,
		&o.TaxAmountMinor, &o.CouponDiscountMinor, &o.FinalAmountMinor,
		&o.DeliveryAddressSnapshotEnc,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOrderNotFound
	}
	return &o, err
}

// customerCancellableStatuses are the order statuses a CUSTOMER may cancel
// from.
//
// It mirrors the ('<status>','cancelled','customer') rows of the D6
// cancellation matrix installed by migration 010 — the same matrix
// Store.CancelOrder is refused by. Kept as data next to a named function so
// `can_cancel` on the order detail is the button's actual precondition and
// not a second, drifting opinion. Note what is absent: `shipped` and beyond,
// which only an admin may cancel.
var customerCancellableStatuses = []string{"payment_pending", "payment_failed", "confirmed", "packed"}

// CustomerCanCancel reports whether a customer may still cancel an order in
// this status.
func CustomerCanCancel(status string) bool { return contains(customerCancellableStatuses, status) }

// OrderCard is the customer order-list row — Phase 2.1. Adds item +
// seller counts and the first item's product so the customer can tell
// orders apart without opening every one. Aggregates come from a single
// LATERAL subquery so the list query is O(page-size) instead of N+1.
// OrderCard is one row of the buyer's order list.
//
// ─── WHY THE MONEY IS IN MINOR UNITS ────────────────────────────────────
//
// This carried `final_amount`, a float64 read from `orders.final_amount`.
// Migration 007 made the minor-unit columns authoritative and stopped
// maintaining the rupee ones, so `final_amount` is 0.00 on every order the
// P0 checkout has ever written — the list rendered every order as ₹0 while
// `final_amount_minor` held the real total.
//
// The client was already right: Android's OrderDto reads `total_minor` and
// friends as Paise and never referenced `final_amount`. This is the server
// catching up to the contract the rest of P0 uses, not a new one.
type OrderCard struct {
	ID          uuid.UUID `json:"id"`
	OrderNumber string    `json:"order_number"`

	SubtotalMinor money.Paise `json:"subtotal_minor"`
	DiscountMinor money.Paise `json:"discount_minor"`
	ShippingMinor money.Paise `json:"shipping_minor"`
	TaxMinor      money.Paise `json:"tax_minor"`
	TotalMinor    money.Paise `json:"total_minor"`
	Currency      string      `json:"currency"`

	PaymentMethod     *string    `json:"payment_method,omitempty"`
	PaymentStatus     string     `json:"payment_status"`
	Status            string     `json:"status"`
	ItemCount         int        `json:"item_count"`
	SellerCount       int        `json:"seller_count"`
	FirstProductID    *uuid.UUID `json:"first_product_id,omitempty"`
	FirstProductTitle string     `json:"first_product_title,omitempty"`
	// FirstItemMediaID is the first line's product cover (primary image,
	// else the first gallery image), resolved into the two URLs below by
	// the service in one media batch for the page. The list drew no
	// picture at all before these: a row was a number and a title.
	FirstItemMediaID      *uuid.UUID `json:"-"`
	FirstItemImageURL     string     `json:"first_item_image_url,omitempty"`
	FirstItemThumbnailURL string     `json:"first_item_thumbnail_url,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	// CreatedAtEpoch is what the client actually parses; the RFC3339 form
	// above stays for anything reading the API by hand.
	CreatedAtEpoch int64 `json:"created_at_epoch"`
}

// ListOrderCardsByCustomer returns one page of order cards for the
// customer using keyset pagination on (created_at, id). cursorTime nil
// means first page. Returns the page (up to limit) plus a flag whether
// more rows exist (so the caller can mint a next cursor).
func (s *Store) ListOrderCardsByCustomer(ctx context.Context, userID uuid.UUID, cursorTime *time.Time, cursorID *uuid.UUID, limit int) ([]OrderCard, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	// Fetch limit+1 so we can detect whether a next page exists without a
	// separate COUNT(*) — count would index-scan the full set per request.
	rows, err := s.db.Query(ctx, `
		SELECT o.id, o.order_number, o.currency_code,
		       o.subtotal_minor, o.discount_amount_minor, o.shipping_charges_minor,
		       o.tax_amount_minor, o.final_amount_minor,
		       o.payment_method, o.payment_status, o.status, o.created_at,
		       COALESCE(items.item_count, 0),
		       COALESCE(items.seller_count, 0),
		       items.first_product_id,
		       items.first_product_title,
		       (SELECT COALESCE(pr.primary_image_media_id,
		               (SELECT pm.media_id FROM product_media pm
		                 WHERE pm.product_id = pr.id AND pm.media_type = 'image'
		                 ORDER BY pm.sort_order ASC, pm.created_at ASC LIMIT 1))
		          FROM products pr WHERE pr.id = items.first_product_id) AS first_item_media_id
		FROM orders o
		LEFT JOIN LATERAL (
			SELECT
				COUNT(*) AS item_count,
				COUNT(DISTINCT seller_id) AS seller_count,
				(ARRAY_AGG(product_id ORDER BY created_at ASC))[1] AS first_product_id,
				(ARRAY_AGG(product_title ORDER BY created_at ASC))[1] AS first_product_title
			FROM order_items oi
			WHERE oi.order_id = o.id
		) items ON TRUE
		WHERE o.customer_user_id = $1
		  AND ($2::TIMESTAMPTZ IS NULL OR (o.created_at, o.id) < ($2, $3::UUID))
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $4
	`, userID, cursorTime, cursorID, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]OrderCard, 0, limit+1)
	for rows.Next() {
		var c OrderCard
		var firstProductID *uuid.UUID
		var firstProductTitle *string
		if err := rows.Scan(
			&c.ID, &c.OrderNumber, &c.Currency,
			&c.SubtotalMinor, &c.DiscountMinor, &c.ShippingMinor,
			&c.TaxMinor, &c.TotalMinor,
			&c.PaymentMethod, &c.PaymentStatus, &c.Status, &c.CreatedAt,
			&c.ItemCount, &c.SellerCount,
			&firstProductID, &firstProductTitle, &c.FirstItemMediaID,
		); err != nil {
			return nil, false, err
		}
		c.FirstProductID = firstProductID
		if firstProductTitle != nil {
			c.FirstProductTitle = *firstProductTitle
		}
		c.CreatedAtEpoch = c.CreatedAt.Unix()
		out = append(out, c)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

func (s *Store) GetOrdersByCustomer(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Order, int, error) {
	var total int
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM orders WHERE customer_user_id=$1`, userID).Scan(&total)

	rows, err := s.db.Query(ctx, `SELECT id,customer_user_id,order_number,final_amount,currency_code,
		payment_status,status,created_at,updated_at FROM orders
		WHERE customer_user_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var orders []*Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.CustomerUserID, &o.OrderNumber, &o.FinalAmount,
			&o.CurrencyCode, &o.PaymentStatus, &o.Status, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, 0, err
		}
		orders = append(orders, &o)
	}
	return orders, total, nil
}

// GetOrdersBySeller returns orders containing at least one item sold by the given seller.
func (s *Store) GetOrdersBySeller(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*Order, int, error) {
	var total int
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(DISTINCT o.id) FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		WHERE oi.seller_id = $1
	`, sellerID).Scan(&total)

	// The paise total and the payment method ride along. The row used to
	// carry only `final_amount`, the rupee column migration 007 stopped
	// maintaining, so every seller list row omitted total_minor and the
	// dashboard priced every order at nothing.
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT o.id, o.customer_user_id, o.order_number, o.final_amount,
			COALESCE(o.final_amount_minor, 0), o.payment_method, o.currency_code,
			o.payment_status, o.status, o.created_at, o.updated_at
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		WHERE oi.seller_id = $1
		ORDER BY o.created_at DESC
		LIMIT $2 OFFSET $3
	`, sellerID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var orders []*Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.CustomerUserID, &o.OrderNumber, &o.FinalAmount,
			&o.FinalAmountMinor, &o.PaymentMethod,
			&o.CurrencyCode, &o.PaymentStatus, &o.Status, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, 0, err
		}
		orders = append(orders, &o)
	}
	return orders, total, nil
}

func (s *Store) UpdateOrderStatus(ctx context.Context, orderID uuid.UUID, toStatus string, actorID *uuid.UUID, actorType, notes string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var fromStatus string
	if err = tx.QueryRow(ctx, `UPDATE orders SET status=$2,updated_at=NOW() WHERE id=$1 RETURNING (SELECT status FROM orders WHERE id=$1)`, orderID, toStatus).Scan(&fromStatus); err != nil {
		// Fallback: just update
		if _, err2 := tx.Exec(ctx, `UPDATE orders SET status=$2,updated_at=NOW() WHERE id=$1`, orderID, toStatus); err2 != nil {
			return err2
		}
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO order_status_history (id,order_id,from_status,to_status,changed_by,actor_type,notes,created_at)
		VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6,NOW())`,
		orderID, fromStatus, toStatus, actorID, actorType, notes,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetOrderItems(ctx context.Context, orderID uuid.UUID) ([]*OrderItem, error) {
	rows, err := s.db.Query(ctx, `SELECT id,order_id,product_id,variant_id,seller_id,product_title,
		variant_details,sku,quantity,unit_mrp,unit_price,discount_amount,tax_amount,final_price,
		status,shipment_id,tracking_number,return_eligible_until,delivered_at,created_at,
		COALESCE(unit_mrp_minor,0),COALESCE(unit_price_minor,0),
		COALESCE(discount_amount_minor,0),COALESCE(tax_amount_minor,0),COALESCE(final_price_minor,0),`+
		orderItemCoverSQL+`
		FROM order_items WHERE order_id=$1 ORDER BY created_at`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*OrderItem
	for rows.Next() {
		var item OrderItem
		if err := rows.Scan(&item.ID, &item.OrderID, &item.ProductID, &item.VariantID, &item.SellerID,
			&item.ProductTitle, &item.VariantDetails, &item.SKU, &item.Quantity,
			&item.UnitMRP, &item.UnitPrice, &item.DiscountAmount, &item.TaxAmount, &item.FinalPrice,
			&item.Status, &item.ShipmentID, &item.TrackingNumber, &item.ReturnEligibleUntil,
			&item.DeliveredAt, &item.CreatedAt,
			&item.UnitMRPMinor, &item.UnitPriceMinor, &item.DiscountAmountMinor,
			&item.TaxAmountMinor, &item.FinalPriceMinor, &item.ImageMediaID); err != nil {
			return nil, err
		}
		items = append(items, &item)
	}
	return items, rows.Err()
}

// GetOrderItemByID fetches a single order item. Used by the review-create
// path (Phase 0.6) to validate that the reviewer's order item actually
// matches the product + seller they're trying to review.
func (s *Store) GetOrderItemByID(ctx context.Context, id uuid.UUID) (*OrderItem, error) {
	var item OrderItem
	err := s.db.QueryRow(ctx, `SELECT id,order_id,product_id,variant_id,seller_id,product_title,
		variant_details,sku,quantity,unit_mrp,unit_price,discount_amount,tax_amount,final_price,
		status,shipment_id,tracking_number,return_eligible_until,delivered_at,created_at
		FROM order_items WHERE id=$1`, id).Scan(
		&item.ID, &item.OrderID, &item.ProductID, &item.VariantID, &item.SellerID,
		&item.ProductTitle, &item.VariantDetails, &item.SKU, &item.Quantity,
		&item.UnitMRP, &item.UnitPrice, &item.DiscountAmount, &item.TaxAmount, &item.FinalPrice,
		&item.Status, &item.ShipmentID, &item.TrackingNumber, &item.ReturnEligibleUntil,
		&item.DeliveredAt, &item.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// Payment-status writes live in order_transitions.go (MarkOrderPaid /
// TransitionPaymentStatus) — every transition is guarded by the allowed
// source states so concurrent callers converge on one applied write.

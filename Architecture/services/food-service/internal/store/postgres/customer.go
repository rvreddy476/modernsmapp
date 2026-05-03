package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrCartRestaurantConflict = errors.New("cart contains items from another restaurant")
	ErrCartEmpty              = errors.New("cart is empty")
	ErrCouponInvalid          = errors.New("coupon is invalid")
	ErrIdempotencyInProgress  = errors.New("idempotent request is already in progress")
)

type AddCartItemInput struct {
	MenuItemID      uuid.UUID
	VariantID       *uuid.UUID
	Quantity        int
	ItemInstruction string
	ClearExisting   bool
}

type UpdateCartItemInput struct {
	Quantity        int
	ItemInstruction string
}

type PlaceOrderInput struct {
	AddressID           uuid.UUID
	PaymentMethod       string
	CouponCode          string
	CustomerInstruction string
}

func (s *Store) GetCart(ctx context.Context, userID uuid.UUID) (*Cart, error) {
	return s.loadCart(ctx, s.db, userID)
}

func (s *Store) AddCartItem(ctx context.Context, userID uuid.UUID, in AddCartItemInput) (*Cart, error) {
	if in.Quantity <= 0 {
		in.Quantity = 1
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var restaurantID uuid.UUID
	var available bool
	if err := tx.QueryRow(ctx, `
		SELECT restaurant_id, is_available AND is_active
		FROM food.menu_items
		WHERE id = $1
	`, in.MenuItemID).Scan(&restaurantID, &available); err != nil {
		return nil, err
	}
	if !available {
		return nil, fmt.Errorf("menu item is unavailable")
	}

	cartID, currentRestaurantID, err := s.ensureCartForUpdate(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if currentRestaurantID != nil && *currentRestaurantID != restaurantID {
		if !in.ClearExisting {
			return nil, ErrCartRestaurantConflict
		}
		if _, err := tx.Exec(ctx, `DELETE FROM food.cart_items WHERE cart_id = $1`, cartID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.carts
		SET restaurant_id = $2
		WHERE id = $1
	`, cartID, restaurantID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.cart_items (
			cart_id, restaurant_id, menu_item_id, variant_id, quantity, item_instruction
		)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, cartID, restaurantID, in.MenuItemID, in.VariantID, in.Quantity, in.ItemInstruction); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetCart(ctx, userID)
}

func (s *Store) UpdateCartItem(ctx context.Context, userID, itemID uuid.UUID, in UpdateCartItemInput) (*Cart, error) {
	if in.Quantity <= 0 {
		if err := s.RemoveCartItem(ctx, userID, itemID); err != nil {
			return nil, err
		}
		return s.GetCart(ctx, userID)
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE food.cart_items ci
		SET quantity = $3, item_instruction = $4
		FROM food.carts c
		WHERE ci.cart_id = c.id AND c.user_id = $1 AND ci.id = $2
	`, userID, itemID, in.Quantity, in.ItemInstruction); err != nil {
		return nil, err
	}
	return s.GetCart(ctx, userID)
}

func (s *Store) RemoveCartItem(ctx context.Context, userID, itemID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM food.cart_items ci
		USING food.carts c
		WHERE ci.cart_id = c.id AND c.user_id = $1 AND ci.id = $2
	`, userID, itemID)
	return err
}

func (s *Store) ClearCart(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE food.carts
		SET restaurant_id = NULL, coupon_code = NULL
		WHERE user_id = $1
	`, userID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		DELETE FROM food.cart_items
		WHERE cart_id IN (SELECT id FROM food.carts WHERE user_id = $1)
	`, userID)
	return err
}

func (s *Store) ApplyCoupon(ctx context.Context, userID uuid.UUID, code string) (*Cart, error) {
	cart, err := s.GetCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(cart.Items) == 0 {
		return nil, ErrCartEmpty
	}
	discount, err := s.validateCoupon(ctx, code, cart.RestaurantID, cart.Totals.ItemSubtotal)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE food.carts
		SET coupon_code = $2
		WHERE user_id = $1
	`, userID, code); err != nil {
		return nil, err
	}
	cart, err = s.GetCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	cart.Totals.CouponDiscount = discount
	cart.Totals.FinalAmount = roundMoney(cart.Totals.FinalAmount - discount)
	return cart, nil
}

func (s *Store) ListAddresses(ctx context.Context, userID uuid.UUID) ([]Address, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, COALESCE(label, ''), COALESCE(receiver_name, ''),
			COALESCE(phone, ''), address_line1, COALESCE(address_line2, ''),
			COALESCE(landmark, ''), city, COALESCE(state, ''), country,
			COALESCE(postal_code, ''), COALESCE(latitude, 0)::float8,
			COALESCE(longitude, 0)::float8, is_default
		FROM food.customer_addresses
		WHERE user_id = $1 AND is_deleted = FALSE
		ORDER BY is_default DESC, created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var addresses []Address
	for rows.Next() {
		var a Address
		if err := rows.Scan(&a.ID, &a.UserID, &a.Label, &a.ReceiverName, &a.Phone,
			&a.AddressLine1, &a.AddressLine2, &a.Landmark, &a.City, &a.State,
			&a.Country, &a.PostalCode, &a.Latitude, &a.Longitude, &a.IsDefault); err != nil {
			return nil, err
		}
		addresses = append(addresses, a)
	}
	return addresses, rows.Err()
}

func (s *Store) CreateAddress(ctx context.Context, userID uuid.UUID, in AddressInput) (*Address, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if in.IsDefault {
		if _, err := tx.Exec(ctx, `UPDATE food.customer_addresses SET is_default = FALSE WHERE user_id = $1`, userID); err != nil {
			return nil, err
		}
	}
	if in.Country == "" {
		in.Country = "India"
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.customer_addresses (
			user_id, label, receiver_name, phone, address_line1, address_line2,
			landmark, city, state, country, postal_code, latitude, longitude, is_default
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id
	`, userID, in.Label, in.ReceiverName, in.Phone, in.AddressLine1, in.AddressLine2,
		in.Landmark, in.City, in.State, in.Country, in.PostalCode, in.Latitude, in.Longitude,
		in.IsDefault).Scan(&id); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetAddress(ctx, userID, id)
}

func (s *Store) UpdateAddress(ctx context.Context, userID, addressID uuid.UUID, in AddressInput) (*Address, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if in.IsDefault {
		if _, err := tx.Exec(ctx, `UPDATE food.customer_addresses SET is_default = FALSE WHERE user_id = $1`, userID); err != nil {
			return nil, err
		}
	}
	if in.Country == "" {
		in.Country = "India"
	}
	tag, err := tx.Exec(ctx, `
		UPDATE food.customer_addresses
		SET label = $3,
			receiver_name = $4,
			phone = $5,
			address_line1 = $6,
			address_line2 = $7,
			landmark = $8,
			city = $9,
			state = $10,
			country = $11,
			postal_code = $12,
			latitude = $13,
			longitude = $14,
			is_default = $15
		WHERE user_id = $1 AND id = $2 AND is_deleted = FALSE
	`, userID, addressID, in.Label, in.ReceiverName, in.Phone, in.AddressLine1,
		in.AddressLine2, in.Landmark, in.City, in.State, in.Country, in.PostalCode,
		in.Latitude, in.Longitude, in.IsDefault)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetAddress(ctx, userID, addressID)
}

func (s *Store) GetAddress(ctx context.Context, userID, addressID uuid.UUID) (*Address, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, COALESCE(label, ''), COALESCE(receiver_name, ''),
			COALESCE(phone, ''), address_line1, COALESCE(address_line2, ''),
			COALESCE(landmark, ''), city, COALESCE(state, ''), country,
			COALESCE(postal_code, ''), COALESCE(latitude, 0)::float8,
			COALESCE(longitude, 0)::float8, is_default
		FROM food.customer_addresses
		WHERE user_id = $1 AND id = $2 AND is_deleted = FALSE
	`, userID, addressID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	var a Address
	if err := rows.Scan(&a.ID, &a.UserID, &a.Label, &a.ReceiverName, &a.Phone,
		&a.AddressLine1, &a.AddressLine2, &a.Landmark, &a.City, &a.State,
		&a.Country, &a.PostalCode, &a.Latitude, &a.Longitude, &a.IsDefault); err != nil {
		return nil, err
	}
	return &a, rows.Err()
}

func (s *Store) DeleteAddress(ctx context.Context, userID, addressID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE food.customer_addresses
		SET is_deleted = TRUE
		WHERE user_id = $1 AND id = $2
	`, userID, addressID)
	return err
}

func (s *Store) PlaceOrder(ctx context.Context, userID uuid.UUID, in PlaceOrderInput, idempotencyKey string) (*Order, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if idempotencyKey != "" {
		existingOrderID, handled, err := s.lockIdempotency(ctx, tx, userID, idempotencyKey)
		if err != nil {
			return nil, err
		}
		if handled {
			if existingOrderID == uuid.Nil {
				return nil, ErrIdempotencyInProgress
			}
			return s.getOrderTx(ctx, tx, userID, existingOrderID, true)
		}
	}

	cart, err := s.loadCart(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if len(cart.Items) == 0 || cart.RestaurantID == nil {
		return nil, ErrCartEmpty
	}
	address, err := s.getAddressTx(ctx, tx, userID, in.AddressID)
	if err != nil {
		return nil, err
	}

	var restaurantName string
	var restaurantAddressJSON []byte
	var prepMins int
	var commissionPct float64
	var active bool
	if err := tx.QueryRow(ctx, `
		SELECT name, jsonb_build_object(
			'address_line1', address_line1,
			'address_line2', address_line2,
			'city', city,
			'state', state,
			'country', country,
			'postal_code', postal_code,
			'latitude', latitude,
			'longitude', longitude
		), avg_preparation_minutes, commission_percentage::float8,
		(status = 'ACTIVE' AND is_open = TRUE AND is_accepting_orders = TRUE)
		FROM food.restaurants
		WHERE id = $1
	`, *cart.RestaurantID).Scan(&restaurantName, &restaurantAddressJSON, &prepMins, &commissionPct, &active); err != nil {
		return nil, err
	}
	if !active {
		return nil, fmt.Errorf("restaurant is not accepting orders")
	}

	if in.CouponCode != "" {
		if _, err := s.validateCouponTx(ctx, tx, in.CouponCode, cart.RestaurantID, cart.Totals.ItemSubtotal); err != nil {
			return nil, err
		}
		cart.CouponCode = in.CouponCode
	}
	if cart.CouponCode != "" {
		discount, err := s.validateCouponTx(ctx, tx, cart.CouponCode, cart.RestaurantID, cart.Totals.ItemSubtotal)
		if err != nil {
			return nil, err
		}
		cart.Totals.CouponDiscount = discount
		cart.Totals.FinalAmount = roundMoney(cart.Totals.FinalAmount - discount)
	}

	paymentMethod := in.PaymentMethod
	if paymentMethod == "" {
		paymentMethod = "COD"
	}
	status := "CONFIRMED"
	paymentStatus := "NOT_REQUIRED"
	if paymentMethod == "ONLINE" || paymentMethod == "WALLET" {
		status = "PAYMENT_PENDING"
		paymentStatus = "PENDING"
	}
	commissionAmount := roundMoney(cart.Totals.ItemSubtotal * commissionPct / 100)
	orderNumber := fmt.Sprintf("FG%d", time.Now().UnixNano())

	deliveryAddress := map[string]any{
		"id":            address.ID,
		"label":         address.Label,
		"receiver_name": address.ReceiverName,
		"phone":         address.Phone,
		"address_line1": address.AddressLine1,
		"address_line2": address.AddressLine2,
		"landmark":      address.Landmark,
		"city":          address.City,
		"state":         address.State,
		"country":       address.Country,
		"postal_code":   address.PostalCode,
		"latitude":      address.Latitude,
		"longitude":     address.Longitude,
	}
	deliveryAddressJSON, _ := json.Marshal(deliveryAddress)

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.orders (
			order_number, user_id, restaurant_id, customer_address_id, status,
			payment_status, payment_method, restaurant_name_snapshot,
			restaurant_address_snapshot, delivery_address_snapshot,
			item_subtotal, addon_total, packaging_fee, tax_total, delivery_fee,
			platform_fee, restaurant_discount, coupon_discount, final_amount,
			coupon_code, commission_percentage_snapshot, commission_amount,
			estimated_preparation_minutes, estimated_delivery_minutes, customer_instruction
		)
		VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25
		)
		RETURNING id
	`, orderNumber, userID, *cart.RestaurantID, address.ID, status, paymentStatus, paymentMethod,
		restaurantName, restaurantAddressJSON, deliveryAddressJSON, cart.Totals.ItemSubtotal,
		cart.Totals.AddonTotal, cart.Totals.PackagingFee, cart.Totals.TaxTotal, cart.Totals.DeliveryFee,
		cart.Totals.PlatformFee, cart.Totals.RestaurantDiscount, cart.Totals.CouponDiscount,
		cart.Totals.FinalAmount, emptyToNil(cart.CouponCode), commissionPct, commissionAmount,
		prepMins, prepMins+25, in.CustomerInstruction).Scan(&orderID); err != nil {
		return nil, err
	}

	for _, item := range cart.Items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.order_items (
				order_id, menu_item_id, variant_id, item_name_snapshot,
				food_type_snapshot, unit_price_snapshot, quantity,
				tax_percentage_snapshot, tax_amount, line_total, item_instruction
			)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		`, orderID, item.MenuItemID, item.VariantID, item.Name, item.FoodType, item.UnitPrice,
			item.Quantity, item.TaxPercentage, item.TaxAmount, item.LineTotal, item.ItemInstruction); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.order_status_history (order_id, from_status, to_status, changed_by, reason)
		VALUES ($1, NULL, $2, $3, 'order placed')
	`, orderID, status, userID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.payments (order_id, payment_method, status, amount)
		VALUES ($1, $2, $3, $4)
	`, orderID, paymentMethod, paymentStatus, cart.Totals.FinalAmount); err != nil {
		return nil, err
	}
	if status == "CONFIRMED" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.delivery_assignments (order_id, status, delivery_fee)
			VALUES ($1, 'CREATED', $2)
		`, orderID, cart.Totals.DeliveryFee); err != nil {
			return nil, err
		}
	}
	if cart.CouponCode != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE food.coupons SET used_count = used_count + 1 WHERE code = $1
		`, cart.CouponCode); err != nil {
			return nil, err
		}
		var couponID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM food.coupons WHERE code = $1`, cart.CouponCode).Scan(&couponID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.coupon_redemptions (coupon_id, order_id, user_id, discount_amount)
			VALUES ($1, $2, $3, $4)
		`, couponID, orderID, userID, cart.Totals.CouponDiscount); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM food.cart_items WHERE cart_id = $1`, cart.ID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE food.carts SET restaurant_id = NULL, coupon_code = NULL WHERE id = $1`, cart.ID); err != nil {
		return nil, err
	}
	if idempotencyKey != "" {
		body, _ := json.Marshal(map[string]string{"order_id": orderID.String()})
		if _, err := tx.Exec(ctx, `
			UPDATE food.idempotency_keys
			SET response_status = 201, response_body = $3, completed_at = NOW()
			WHERE user_id = $1 AND key = $2
		`, userID, idempotencyKey, body); err != nil {
			return nil, err
		}
	}

	order, err := s.getOrderTx(ctx, tx, userID, orderID, true)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return order, nil
}

func (s *Store) ListOrders(ctx context.Context, userID uuid.UUID) ([]Order, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, order_number, user_id, restaurant_id, restaurant_name_snapshot,
			status::text, payment_status::text, payment_method::text,
			item_subtotal::float8, addon_total::float8, packaging_fee::float8,
			tax_total::float8, delivery_fee::float8, platform_fee::float8,
			restaurant_discount::float8, coupon_discount::float8, final_amount::float8,
			COALESCE(estimated_preparation_minutes, 0),
			COALESCE(estimated_delivery_minutes, 0),
			placed_at::text, COALESCE(delivered_at::text, '')
		FROM food.orders
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 50
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

func (s *Store) GetOrder(ctx context.Context, userID, orderID uuid.UUID) (*Order, error) {
	return s.getOrder(ctx, s.db, userID, orderID, true)
}

func (s *Store) CancelOrder(ctx context.Context, userID, orderID uuid.UUID, reason string) (*Order, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	order, err := s.getOrderTx(ctx, tx, userID, orderID, false)
	if err != nil {
		return nil, err
	}
	switch order.Status {
	case "PLACED", "CONFIRMED", "PREPARING":
	default:
		return nil, fmt.Errorf("order cannot be cancelled from %s", order.Status)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders
		SET status = 'CANCELLED_BY_CUSTOMER', cancellation_reason = $3,
			cancelled_by = $1, cancelled_at = NOW()
		WHERE user_id = $1 AND id = $2
	`, userID, orderID, reason); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.order_status_history (order_id, from_status, to_status, changed_by, reason)
		VALUES ($1, $2, 'CANCELLED_BY_CUSTOMER', $3, $4)
	`, orderID, order.Status, userID, reason); err != nil {
		return nil, err
	}
	updated, err := s.getOrderTx(ctx, tx, userID, orderID, true)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Store) RateRestaurant(ctx context.Context, userID, orderID uuid.UUID, rating int, review string) (map[string]any, error) {
	if rating < 1 || rating > 5 {
		return nil, fmt.Errorf("rating must be between 1 and 5")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var restaurantID uuid.UUID
	var status string
	if err := tx.QueryRow(ctx, `
		SELECT restaurant_id, status::text
		FROM food.orders
		WHERE id = $1 AND user_id = $2
	`, orderID, userID).Scan(&restaurantID, &status); err != nil {
		return nil, err
	}
	if status != "DELIVERED" {
		return nil, fmt.Errorf("restaurant can be rated after delivery")
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.restaurant_ratings (order_id, restaurant_id, user_id, rating, review)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (order_id) DO UPDATE SET rating = EXCLUDED.rating, review = EXCLUDED.review
		RETURNING id
	`, orderID, restaurantID, userID, rating, review).Scan(&id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.restaurants r
		SET avg_rating = stats.avg_rating,
			rating_count = stats.rating_count
		FROM (
			SELECT restaurant_id, AVG(rating)::numeric(3,2) AS avg_rating, COUNT(*)::int AS rating_count
			FROM food.restaurant_ratings
			WHERE restaurant_id = $1
			GROUP BY restaurant_id
		) stats
		WHERE r.id = stats.restaurant_id
	`, restaurantID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "order_id": orderID, "restaurant_id": restaurantID, "rating": rating, "review": review}, nil
}

func (s *Store) RateDelivery(ctx context.Context, userID, orderID uuid.UUID, rating int, review string) (map[string]any, error) {
	if rating < 1 || rating > 5 {
		return nil, fmt.Errorf("rating must be between 1 and 5")
	}
	var partnerID uuid.UUID
	var status string
	if err := s.db.QueryRow(ctx, `
		SELECT da.delivery_partner_id, o.status::text
		FROM food.orders o
		JOIN food.delivery_assignments da ON da.order_id = o.id
		WHERE o.id = $1 AND o.user_id = $2 AND da.delivery_partner_id IS NOT NULL
	`, orderID, userID).Scan(&partnerID, &status); err != nil {
		return nil, err
	}
	if status != "DELIVERED" {
		return nil, fmt.Errorf("delivery can be rated after delivery")
	}
	var id uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.delivery_ratings (order_id, delivery_partner_id, user_id, rating, review)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (order_id) DO UPDATE SET rating = EXCLUDED.rating, review = EXCLUDED.review
		RETURNING id
	`, orderID, partnerID, userID, rating, review).Scan(&id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "order_id": orderID, "delivery_partner_id": partnerID, "rating": rating, "review": review}, nil
}

func (s *Store) ensureCartForUpdate(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (uuid.UUID, *uuid.UUID, error) {
	var cartID uuid.UUID
	var restaurantID *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id, restaurant_id
		FROM food.carts
		WHERE user_id = $1
		FOR UPDATE
	`, userID).Scan(&cartID, &restaurantID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO food.carts (user_id)
			VALUES ($1)
			RETURNING id, restaurant_id
		`, userID).Scan(&cartID, &restaurantID)
	}
	return cartID, restaurantID, err
}

func (s *Store) loadCart(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID uuid.UUID) (*Cart, error) {
	var cart Cart
	var restaurantID *uuid.UUID
	err := q.QueryRow(ctx, `
		INSERT INTO food.carts (user_id)
		VALUES ($1)
		ON CONFLICT (user_id) DO UPDATE SET updated_at = food.carts.updated_at
		RETURNING id, user_id, restaurant_id, COALESCE(coupon_code, '')
	`, userID).Scan(&cart.ID, &cart.UserID, &restaurantID, &cart.CouponCode)
	if err != nil {
		return nil, err
	}
	cart.RestaurantID = restaurantID
	if restaurantID != nil {
		_ = q.QueryRow(ctx, `SELECT name FROM food.restaurants WHERE id = $1`, *restaurantID).Scan(&cart.Restaurant)
	}
	rows, err := q.Query(ctx, `
		SELECT ci.id, ci.restaurant_id, ci.menu_item_id, ci.variant_id,
			i.name, COALESCE(i.image_url, ''), i.food_type::text, ci.quantity,
			COALESCE(v.price, COALESCE(i.discount_price, i.base_price))::float8,
			i.tax_percentage::float8,
			ci.item_instruction
		FROM food.cart_items ci
		JOIN food.menu_items i ON i.id = ci.menu_item_id
		LEFT JOIN food.menu_item_variants v ON v.id = ci.variant_id
		WHERE ci.cart_id = $1
		ORDER BY ci.created_at
	`, cart.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item CartItem
		if err := rows.Scan(&item.ID, &item.RestaurantID, &item.MenuItemID, &item.VariantID,
			&item.Name, &item.ImageURL, &item.FoodType, &item.Quantity, &item.UnitPrice,
			&item.TaxPercentage, &item.ItemInstruction); err != nil {
			rows.Close()
			return nil, err
		}
		item.LineTotal = roundMoney(item.UnitPrice * float64(item.Quantity))
		item.TaxAmount = roundMoney(item.LineTotal * item.TaxPercentage / 100)
		cart.Totals.ItemSubtotal += item.LineTotal
		cart.Totals.TaxTotal += item.TaxAmount
		cart.Items = append(cart.Items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if cart.RestaurantID != nil && len(cart.Items) > 0 {
		_ = q.QueryRow(ctx, `SELECT packaging_fee::float8 FROM food.restaurants WHERE id = $1`, *cart.RestaurantID).Scan(&cart.Totals.PackagingFee)
		cart.Totals.DeliveryFee = 29
		cart.Totals.PlatformFee = 5
	}
	if cart.CouponCode != "" {
		if discount, err := s.validateCouponQuery(ctx, q, cart.CouponCode, cart.RestaurantID, cart.Totals.ItemSubtotal); err == nil {
			cart.Totals.CouponDiscount = discount
		}
	}
	cart.Totals.ItemSubtotal = roundMoney(cart.Totals.ItemSubtotal)
	cart.Totals.TaxTotal = roundMoney(cart.Totals.TaxTotal)
	cart.Totals.FinalAmount = roundMoney(cart.Totals.ItemSubtotal + cart.Totals.AddonTotal + cart.Totals.PackagingFee + cart.Totals.TaxTotal + cart.Totals.DeliveryFee + cart.Totals.PlatformFee - cart.Totals.RestaurantDiscount - cart.Totals.CouponDiscount)
	return &cart, nil
}

func (s *Store) validateCoupon(ctx context.Context, code string, restaurantID *uuid.UUID, subtotal float64) (float64, error) {
	return s.validateCouponQuery(ctx, s.db, code, restaurantID, subtotal)
}

func (s *Store) validateCouponTx(ctx context.Context, tx pgx.Tx, code string, restaurantID *uuid.UUID, subtotal float64) (float64, error) {
	return s.validateCouponQuery(ctx, tx, code, restaurantID, subtotal)
}

func (s *Store) validateCouponQuery(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, code string, restaurantID *uuid.UUID, subtotal float64) (float64, error) {
	var couponType string
	var value, maxDiscount, minOrder float64
	var couponRestaurant *uuid.UUID
	if err := q.QueryRow(ctx, `
		SELECT coupon_type::text, discount_value::float8,
			COALESCE(max_discount_amount, discount_value)::float8,
			min_order_amount::float8, restaurant_id
		FROM food.coupons
		WHERE UPPER(code) = UPPER($1)
			AND is_active = TRUE
			AND starts_at <= NOW()
			AND ends_at >= NOW()
			AND (total_usage_limit IS NULL OR used_count < total_usage_limit)
	`, code).Scan(&couponType, &value, &maxDiscount, &minOrder, &couponRestaurant); err != nil {
		return 0, ErrCouponInvalid
	}
	if subtotal < minOrder {
		return 0, ErrCouponInvalid
	}
	if couponRestaurant != nil && (restaurantID == nil || *couponRestaurant != *restaurantID) {
		return 0, ErrCouponInvalid
	}
	discount := value
	if couponType == "PERCENTAGE" {
		discount = subtotal * value / 100
	}
	if discount > maxDiscount {
		discount = maxDiscount
	}
	if discount > subtotal {
		discount = subtotal
	}
	return roundMoney(discount), nil
}

func (s *Store) getAddressTx(ctx context.Context, tx pgx.Tx, userID, addressID uuid.UUID) (*Address, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, user_id, COALESCE(label, ''), COALESCE(receiver_name, ''),
			COALESCE(phone, ''), address_line1, COALESCE(address_line2, ''),
			COALESCE(landmark, ''), city, COALESCE(state, ''), country,
			COALESCE(postal_code, ''), COALESCE(latitude, 0)::float8,
			COALESCE(longitude, 0)::float8, is_default
		FROM food.customer_addresses
		WHERE user_id = $1 AND id = $2 AND is_deleted = FALSE
	`, userID, addressID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	var a Address
	if err := rows.Scan(&a.ID, &a.UserID, &a.Label, &a.ReceiverName, &a.Phone,
		&a.AddressLine1, &a.AddressLine2, &a.Landmark, &a.City, &a.State,
		&a.Country, &a.PostalCode, &a.Latitude, &a.Longitude, &a.IsDefault); err != nil {
		return nil, err
	}
	return &a, rows.Err()
}

func (s *Store) lockIdempotency(ctx context.Context, tx pgx.Tx, userID uuid.UUID, key string) (uuid.UUID, bool, error) {
	var responseBody []byte
	var completedAt *time.Time
	err := tx.QueryRow(ctx, `
		SELECT response_body, completed_at
		FROM food.idempotency_keys
		WHERE user_id = $1 AND key = $2
		FOR UPDATE
	`, userID, key).Scan(&responseBody, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `
			INSERT INTO food.idempotency_keys (user_id, key, locked_until)
			VALUES ($1, $2, NOW() + INTERVAL '2 minutes')
		`, userID, key)
		return uuid.Nil, false, err
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if completedAt == nil {
		return uuid.Nil, true, nil
	}
	var body struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(responseBody, &body); err != nil {
		return uuid.Nil, true, nil
	}
	orderID, _ := uuid.Parse(body.OrderID)
	return orderID, true, nil
}

func (s *Store) lockGenericIdempotency(ctx context.Context, tx pgx.Tx, userID uuid.UUID, key string) (map[string]any, bool, error) {
	if key == "" {
		return nil, false, nil
	}
	var responseBody []byte
	var completedAt *time.Time
	err := tx.QueryRow(ctx, `
		SELECT response_body, completed_at
		FROM food.idempotency_keys
		WHERE user_id = $1 AND key = $2
		FOR UPDATE
	`, userID, key).Scan(&responseBody, &completedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `
			INSERT INTO food.idempotency_keys (user_id, key, locked_until)
			VALUES ($1, $2, NOW() + INTERVAL '2 minutes')
		`, userID, key)
		return nil, false, err
	}
	if err != nil {
		return nil, false, err
	}
	if completedAt == nil {
		return nil, true, ErrIdempotencyInProgress
	}
	var body map[string]any
	if err := json.Unmarshal(responseBody, &body); err != nil {
		return map[string]any{}, true, nil
	}
	return body, true, nil
}

func (s *Store) completeGenericIdempotency(ctx context.Context, tx pgx.Tx, userID uuid.UUID, key string, status int, body map[string]any) error {
	if key == "" {
		return nil
	}
	raw, _ := json.Marshal(body)
	_, err := tx.Exec(ctx, `
		UPDATE food.idempotency_keys
		SET response_status = $3, response_body = $4, completed_at = NOW()
		WHERE user_id = $1 AND key = $2
	`, userID, key, status, raw)
	return err
}

func (s *Store) getOrderTx(ctx context.Context, tx pgx.Tx, userID, orderID uuid.UUID, includeDetails bool) (*Order, error) {
	return s.getOrder(ctx, tx, userID, orderID, includeDetails)
}

func (s *Store) getOrder(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID, orderID uuid.UUID, includeDetails bool) (*Order, error) {
	rows, err := q.Query(ctx, `
		SELECT id, order_number, user_id, restaurant_id, restaurant_name_snapshot,
			status::text, payment_status::text, payment_method::text,
			item_subtotal::float8, addon_total::float8, packaging_fee::float8,
			tax_total::float8, delivery_fee::float8, platform_fee::float8,
			restaurant_discount::float8, coupon_discount::float8, final_amount::float8,
			COALESCE(estimated_preparation_minutes, 0),
			COALESCE(estimated_delivery_minutes, 0),
			placed_at::text, COALESCE(delivered_at::text, '')
		FROM food.orders
		WHERE user_id = $1 AND id = $2
	`, userID, orderID)
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		rows.Close()
		return nil, pgx.ErrNoRows
	}
	order, err := scanOrder(rows)
	if err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !includeDetails {
		return &order, nil
	}
	items, err := s.listOrderItems(ctx, q, orderID)
	if err != nil {
		return nil, err
	}
	history, err := s.listOrderHistory(ctx, q, orderID)
	if err != nil {
		return nil, err
	}
	order.Items = items
	order.History = history
	return &order, nil
}

func (s *Store) listOrderItems(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, orderID uuid.UUID) ([]OrderItem, error) {
	rows, err := q.Query(ctx, `
		SELECT id, item_name_snapshot, food_type_snapshot::text,
			unit_price_snapshot::float8, quantity, tax_amount::float8,
			line_total::float8, COALESCE(item_instruction, '')
		FROM food.order_items
		WHERE order_id = $1
		ORDER BY created_at
	`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []OrderItem
	for rows.Next() {
		var item OrderItem
		if err := rows.Scan(&item.ID, &item.Name, &item.FoodType, &item.UnitPrice,
			&item.Quantity, &item.TaxAmount, &item.LineTotal, &item.Instruction); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) listOrderHistory(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, orderID uuid.UUID) ([]OrderStatusHistory, error) {
	rows, err := q.Query(ctx, `
		SELECT COALESCE(from_status::text, ''), to_status::text,
			COALESCE(reason, ''), created_at::text
		FROM food.order_status_history
		WHERE order_id = $1
		ORDER BY created_at
	`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var history []OrderStatusHistory
	for rows.Next() {
		var item OrderStatusHistory
		if err := rows.Scan(&item.FromStatus, &item.ToStatus, &item.Reason, &item.CreatedAt); err != nil {
			return nil, err
		}
		history = append(history, item)
	}
	return history, rows.Err()
}

func scanOrder(rows pgx.Rows) (Order, error) {
	var order Order
	err := rows.Scan(
		&order.ID,
		&order.OrderNumber,
		&order.UserID,
		&order.RestaurantID,
		&order.RestaurantName,
		&order.Status,
		&order.PaymentStatus,
		&order.PaymentMethod,
		&order.Totals.ItemSubtotal,
		&order.Totals.AddonTotal,
		&order.Totals.PackagingFee,
		&order.Totals.TaxTotal,
		&order.Totals.DeliveryFee,
		&order.Totals.PlatformFee,
		&order.Totals.RestaurantDiscount,
		&order.Totals.CouponDiscount,
		&order.Totals.FinalAmount,
		&order.EstimatedPrepMins,
		&order.EstimatedDeliveryMins,
		&order.PlacedAt,
		&order.DeliveredAt,
	)
	return order, err
}

func roundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}

func emptyToNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

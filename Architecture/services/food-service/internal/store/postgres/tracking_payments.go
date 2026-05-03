package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) GetOrderTracking(ctx context.Context, userID, orderID uuid.UUID) (map[string]any, error) {
	var orderNumber, status string
	var restaurantSnapshot, deliverySnapshot []byte
	var etaMins int
	if err := s.db.QueryRow(ctx, `
		SELECT order_number, status::text, restaurant_address_snapshot,
			delivery_address_snapshot, COALESCE(estimated_delivery_minutes, 0)
		FROM food.orders
		WHERE id = $1 AND user_id = $2
	`, orderID, userID).Scan(&orderNumber, &status, &restaurantSnapshot, &deliverySnapshot, &etaMins); err != nil {
		return nil, err
	}

	assignment, _ := s.assignmentForOrder(ctx, orderID)
	events, err := s.orderTimeline(ctx, orderID)
	if err != nil {
		return nil, err
	}
	deliveryLocation, _ := s.latestDeliveryLocationForOrder(ctx, orderID)

	return map[string]any{
		"order_id":                   orderID,
		"order_number":               orderNumber,
		"status":                     status,
		"timeline":                   events,
		"assignment":                 assignment,
		"restaurant_location":        locationFromJSON(restaurantSnapshot),
		"delivery_location":          deliveryLocation,
		"customer_location":          locationFromJSON(deliverySnapshot),
		"estimated_delivery_minutes": etaMins,
	}, nil
}

func (s *Store) WalletPaymentChargeDetails(ctx context.Context, userID, orderID uuid.UUID) (*WalletPaymentChargeDetails, error) {
	var details WalletPaymentChargeDetails
	if err := s.db.QueryRow(ctx, `
		SELECT o.id, o.order_number, o.user_id, r.owner_user_id,
			o.payment_method::text, o.payment_status::text, o.final_amount::float8
		FROM food.orders o
		JOIN food.restaurants r ON r.id = o.restaurant_id
		WHERE o.id = $1 AND o.user_id = $2
	`, orderID, userID).Scan(
		&details.OrderID,
		&details.OrderNumber,
		&details.UserID,
		&details.RestaurantOwnerID,
		&details.PaymentMethod,
		&details.PaymentStatus,
		&details.Amount,
	); err != nil {
		return nil, err
	}
	return &details, nil
}

func (s *Store) PaymentIntegrationDetails(ctx context.Context, orderID uuid.UUID) (*PaymentIntegrationDetails, error) {
	var details PaymentIntegrationDetails
	if err := s.db.QueryRow(ctx, `
		SELECT o.id, o.order_number, o.user_id, r.owner_user_id,
			o.payment_method::text, o.payment_status::text,
			COALESCE(p.provider_payment_id, ''), COALESCE(p.provider_order_id, ''),
			o.final_amount::float8
		FROM food.orders o
		JOIN food.restaurants r ON r.id = o.restaurant_id
		LEFT JOIN LATERAL (
			SELECT provider_payment_id, provider_order_id
			FROM food.payments
			WHERE order_id = o.id
			ORDER BY created_at DESC
			LIMIT 1
		) p ON TRUE
		WHERE o.id = $1
	`, orderID).Scan(
		&details.OrderID,
		&details.OrderNumber,
		&details.UserID,
		&details.RestaurantOwnerID,
		&details.PaymentMethod,
		&details.PaymentStatus,
		&details.ProviderPaymentID,
		&details.ProviderOrderID,
		&details.Amount,
	); err != nil {
		return nil, err
	}
	return &details, nil
}

func (s *Store) CreatePaymentIntent(ctx context.Context, userID, orderID uuid.UUID, method, idempotencyKey string) (map[string]any, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = "ONLINE"
	}
	if method != "ONLINE" && method != "WALLET" && method != "COD" {
		return nil, fmt.Errorf("unsupported payment method")
	}

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
			if existingOrderID == uuid.Nil || existingOrderID != orderID {
				return nil, ErrIdempotencyInProgress
			}
			return s.paymentIntentTx(ctx, tx, userID, orderID)
		}
	}

	var status, paymentStatus, orderMethod, orderNumber string
	var amount, deliveryFee float64
	if err := tx.QueryRow(ctx, `
		SELECT status::text, payment_status::text, payment_method::text,
			order_number, final_amount::float8, delivery_fee::float8
		FROM food.orders
		WHERE id = $1 AND user_id = $2
		FOR UPDATE
	`, orderID, userID).Scan(&status, &paymentStatus, &orderMethod, &orderNumber, &amount, &deliveryFee); err != nil {
		return nil, err
	}

	oldStatus := status
	provider := "payments-service"
	intentStatus := "PENDING"
	paymentStatus = "PENDING"
	if method == "WALLET" {
		provider = "monetization-service"
	}
	if method == "COD" {
		provider = "cod"
		intentStatus = "NOT_REQUIRED"
		paymentStatus = "NOT_REQUIRED"
		status = "CONFIRMED"
	}

	providerOrderID := fmt.Sprintf("food_order_%s_%d", orderID.String(), time.Now().UnixNano())
	raw, _ := json.Marshal(map[string]any{
		"reference_type": "food_order",
		"reference_id":   orderID.String(),
		"order_number":   orderNumber,
		"method":         method,
		"provider":       provider,
	})

	tag, err := tx.Exec(ctx, `
		UPDATE food.payments
		SET payment_method = $2,
			status = $3,
			provider = $4,
			provider_order_id = $5,
			amount = $6,
			currency = 'INR',
			raw_response = $7
		WHERE id = (
			SELECT id FROM food.payments WHERE order_id = $1 ORDER BY created_at DESC LIMIT 1
		) AND order_id = $1
	`, orderID, method, paymentStatus, provider, providerOrderID, amount, raw)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.payments (
				order_id, payment_method, status, provider, provider_order_id, amount, currency, raw_response
			)
			VALUES ($1, $2, $3, $4, $5, $6, 'INR', $7)
		`, orderID, method, paymentStatus, provider, providerOrderID, amount, raw); err != nil {
			return nil, err
		}
	}

	if method == "COD" {
		if _, err := tx.Exec(ctx, `
			UPDATE food.orders
			SET status = 'CONFIRMED', payment_status = 'NOT_REQUIRED', payment_method = 'COD'
			WHERE id = $1
		`, orderID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.order_status_history (order_id, from_status, to_status, changed_by, reason)
			VALUES ($1, NULLIF($2, '')::food.order_status, 'CONFIRMED', $3, 'cash on delivery selected')
		`, orderID, oldStatus, userID); err != nil {
			return nil, err
		}
		if err := s.ensureDeliveryAssignmentTx(ctx, tx, orderID, deliveryFee); err != nil {
			return nil, err
		}
	} else if orderMethod != method || status == "PLACED" {
		if _, err := tx.Exec(ctx, `
			UPDATE food.orders
			SET status = 'PAYMENT_PENDING', payment_status = 'PENDING', payment_method = $2
			WHERE id = $1
		`, orderID, method); err != nil {
			return nil, err
		}
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

	intent, err := s.paymentIntentTx(ctx, tx, userID, orderID)
	if err != nil {
		return nil, err
	}
	intent["payment_intent"] = map[string]any{
		"id":             intent["id"],
		"reference_type": "food_order",
		"reference_id":   orderID,
		"amount":         amount,
		"currency":       "INR",
		"method":         method,
		"status":         intentStatus,
		"provider":       provider,
		"provider_ref":   providerOrderID,
	}
	return intent, tx.Commit(ctx)
}

func (s *Store) AttachPaymentProviderReference(ctx context.Context, userID, orderID uuid.UUID, providerPaymentID, providerOrderID string, raw map[string]any) error {
	rawJSON, _ := json.Marshal(raw)
	tag, err := s.db.Exec(ctx, `
		UPDATE food.payments p
		SET provider = 'payments-service',
			provider_payment_id = COALESCE(NULLIF($3, ''), provider_payment_id),
			provider_order_id = COALESCE(NULLIF($4, ''), provider_order_id),
			raw_response = raw_response || $5::jsonb
		FROM food.orders o
		WHERE p.order_id = o.id
			AND p.id = (
				SELECT id FROM food.payments WHERE order_id = $1 ORDER BY created_at DESC LIMIT 1
			)
			AND p.order_id = $1
			AND o.user_id = $2
	`, orderID, userID, providerPaymentID, providerOrderID, rawJSON)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) ConfirmPayment(ctx context.Context, userID, orderID uuid.UUID, providerPaymentID, providerReference string) (*Order, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var fromStatus, method string
	var deliveryFee float64
	if err := tx.QueryRow(ctx, `
		SELECT status::text, payment_method::text, delivery_fee::float8
		FROM food.orders
		WHERE id = $1 AND user_id = $2
		FOR UPDATE
	`, orderID, userID).Scan(&fromStatus, &method, &deliveryFee); err != nil {
		return nil, err
	}
	if fromStatus != "PAYMENT_PENDING" && fromStatus != "PLACED" && fromStatus != "CONFIRMED" {
		return nil, fmt.Errorf("payment cannot be confirmed from %s", fromStatus)
	}
	status := "CAPTURED"
	if method == "COD" {
		status = "NOT_REQUIRED"
	}
	raw, _ := json.Marshal(map[string]any{
		"provider_reference": providerReference,
		"confirmed_by":       userID.String(),
	})
	if _, err := tx.Exec(ctx, `
		UPDATE food.payments
		SET status = $2::food.payment_status,
			provider_payment_id = COALESCE(NULLIF($3, ''), provider_payment_id),
			provider_order_id = COALESCE(NULLIF($4, ''), provider_order_id),
			raw_response = raw_response || $5::jsonb,
			paid_at = CASE WHEN $2::food.payment_status = 'CAPTURED'::food.payment_status THEN NOW() ELSE paid_at END
		WHERE id = (
			SELECT id FROM food.payments WHERE order_id = $1 ORDER BY created_at DESC LIMIT 1
		) AND order_id = $1
	`, orderID, status, providerPaymentID, providerReference, raw); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders
		SET status = 'CONFIRMED', payment_status = $3::food.payment_status
		WHERE id = $1 AND user_id = $2
	`, orderID, userID, status); err != nil {
		return nil, err
	}
	if fromStatus != "CONFIRMED" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.order_status_history (order_id, from_status, to_status, changed_by, reason)
			VALUES ($1, $2::food.order_status, 'CONFIRMED', $3, 'payment confirmed')
		`, orderID, fromStatus, userID); err != nil {
			return nil, err
		}
	}
	if err := s.ensureDeliveryAssignmentTx(ctx, tx, orderID, deliveryFee); err != nil {
		return nil, err
	}
	order, err := s.getOrderTx(ctx, tx, userID, orderID, true)
	if err != nil {
		return nil, err
	}
	return order, tx.Commit(ctx)
}

func (s *Store) UpdateDeliveryLocation(ctx context.Context, userID uuid.UUID, latitude, longitude float64, accuracyMeters *float64) (map[string]any, error) {
	if latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		return nil, fmt.Errorf("invalid coordinates")
	}
	partner, err := s.GetDeliveryPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var locationID uuid.UUID
	var recordedAt string
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.delivery_partner_locations (
			delivery_partner_id, latitude, longitude, accuracy_meters
		)
		VALUES ($1, $2, $3, $4)
		RETURNING id, recorded_at::text
	`, partner.ID, latitude, longitude, accuracyMeters).Scan(&locationID, &recordedAt); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_partners
		SET current_latitude = $2, current_longitude = $3
		WHERE id = $1
	`, partner.ID, latitude, longitude); err != nil {
		return nil, err
	}

	var assignmentID uuid.UUID
	var assignmentStatus string
	err = tx.QueryRow(ctx, `
		SELECT id, status::text
		FROM food.delivery_assignments
		WHERE delivery_partner_id = $1
			AND status NOT IN ('DELIVERED', 'FAILED', 'CANCELLED', 'REJECTED')
		ORDER BY created_at DESC
		LIMIT 1
	`, partner.ID).Scan(&assignmentID, &assignmentStatus)
	if err != nil && err != pgx.ErrNoRows {
		return nil, err
	}
	if err == nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.delivery_tracking_events (
				assignment_id, delivery_partner_id, status, latitude, longitude, note
			)
			VALUES ($1, $2, $3, $4, $5, 'location update')
		`, assignmentID, partner.ID, assignmentStatus, latitude, longitude); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{
		"id":                  locationID,
		"delivery_partner_id": partner.ID,
		"assignment_id":       assignmentID,
		"latitude":            latitude,
		"longitude":           longitude,
		"accuracy_meters":     accuracyMeters,
		"recorded_at":         recordedAt,
	}, nil
}

func (s *Store) GetAssignmentTracking(ctx context.Context, userID, assignmentID uuid.UUID) (map[string]any, error) {
	partner, err := s.GetDeliveryPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT da.id, da.order_id, o.order_number, o.restaurant_name_snapshot,
			o.restaurant_id, da.delivery_partner_id, da.status::text, o.status::text,
			da.delivery_fee::float8, da.delivery_partner_payout::float8, da.created_at::text
		FROM food.delivery_assignments da
		JOIN food.orders o ON o.id = da.order_id
		WHERE da.id = $1 AND da.delivery_partner_id = $2
	`, assignmentID, partner.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	assignment, err := scanDeliveryAssignment(rows)
	if err != nil {
		return nil, err
	}
	events, err := s.assignmentTrackingEvents(ctx, assignmentID)
	if err != nil {
		return nil, err
	}
	lastLocation, _ := s.latestDeliveryLocationForOrder(ctx, assignment.OrderID)
	return map[string]any{
		"assignment":        assignment,
		"events":            events,
		"delivery_location": lastLocation,
	}, rows.Err()
}

func (s *Store) PartnerRestaurantSettlements(ctx context.Context, ownerID, restaurantID uuid.UUID) ([]map[string]any, error) {
	if err := s.requireRestaurantOwner(ctx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id::text, period_start::text, period_end::text,
			gross_order_amount::float8, commission_amount::float8,
			refund_adjustment::float8, penalty_amount::float8, payout_amount::float8,
			status::text, COALESCE(paid_reference, ''), COALESCE(paid_at::text, ''),
			created_at::text
		FROM food.restaurant_settlements
		WHERE restaurant_id = $1
		ORDER BY period_start DESC, created_at DESC
		LIMIT 100
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, start, end, status, ref, paidAt, createdAt string
		var gross, commission, refund, penalty, payout float64
		if err := rows.Scan(&id, &start, &end, &gross, &commission, &refund, &penalty, &payout, &status, &ref, &paidAt, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "restaurant_id": restaurantID, "period_start": start, "period_end": end,
			"gross_amount": gross, "commission": commission, "refund_adjustment": refund,
			"penalty_amount": penalty, "payout_amount": payout, "status": status,
			"paid_reference": ref, "paid_at": paidAt, "created_at": createdAt,
		})
	}
	return items, rows.Err()
}

func (s *Store) PartnerRestaurantSummary(ctx context.Context, ownerID, restaurantID uuid.UUID) (map[string]any, error) {
	if err := s.requireRestaurantOwner(ctx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	var orders, delivered, refunded int
	var gross, commission, refunds float64
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*)::int,
			COUNT(*) FILTER (WHERE status = 'DELIVERED')::int,
			COUNT(*) FILTER (WHERE status = 'REFUNDED')::int,
			COALESCE(SUM(final_amount), 0)::float8,
			COALESCE(SUM(commission_amount), 0)::float8,
			COALESCE((SELECT SUM(amount) FROM food.refunds rf JOIN food.orders ro ON ro.id = rf.order_id WHERE ro.restaurant_id = $1), 0)::float8
		FROM food.orders
		WHERE restaurant_id = $1
	`, restaurantID).Scan(&orders, &delivered, &refunded, &gross, &commission, &refunds); err != nil {
		return nil, err
	}
	return map[string]any{
		"restaurant_id": restaurantID,
		"orders":        orders,
		"delivered":     delivered,
		"refunded":      refunded,
		"gross_amount":  gross,
		"commission":    commission,
		"refunds":       refunds,
		"payout_amount": roundMoney(gross - commission - refunds),
	}, nil
}

func (s *Store) AdminListDeliverySettlements(ctx context.Context, page Pagination) ([]map[string]any, error) {
	page = normalizePagination(page)
	rows, err := s.db.Query(ctx, `
		SELECT ds.id::text, ds.delivery_partner_id::text, dp.full_name,
			ds.period_start::text, ds.period_end::text, ds.delivery_count,
			ds.gross_earning_amount::float8, ds.incentive_amount::float8,
			ds.penalty_amount::float8, ds.payout_amount::float8, ds.status::text,
			COALESCE(ds.paid_reference, ''), COALESCE(ds.paid_at::text, ''), ds.created_at::text
		FROM food.delivery_partner_settlements ds
		JOIN food.delivery_partners dp ON dp.id = ds.delivery_partner_id
		ORDER BY ds.created_at DESC
		LIMIT $1 OFFSET $2
	`, page.Limit, page.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, partnerID, name, start, end, status, ref, paidAt, createdAt string
		var count int
		var gross, incentive, penalty, payout float64
		if err := rows.Scan(&id, &partnerID, &name, &start, &end, &count, &gross, &incentive, &penalty, &payout, &status, &ref, &paidAt, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "delivery_partner_id": partnerID, "delivery_partner_name": name,
			"period_start": start, "period_end": end, "delivery_count": count,
			"gross_amount": gross, "incentive_amount": incentive, "penalty_amount": penalty,
			"payout_amount": payout, "status": status, "paid_reference": ref,
			"paid_at": paidAt, "created_at": createdAt,
		})
	}
	return items, rows.Err()
}

func (s *Store) AdminMarkDeliverySettlementPaid(ctx context.Context, adminID, settlementID uuid.UUID, reference string) (map[string]any, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE food.delivery_partner_settlements
		SET status = 'PAID', paid_reference = $3::text, paid_at = NOW(), created_by = COALESCE(created_by, $2)
		WHERE id = $1
	`, settlementID, adminID, reference)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, entity_id, new_value)
		VALUES ($1, 'delivery_settlement.mark_paid', 'delivery_partner_settlement', $2, jsonb_build_object('status', 'PAID', 'reference', $3::text))
	`, adminID, settlementID, reference); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"id": settlementID, "status": "PAID", "paid_reference": reference}, nil
}

func (s *Store) AdminAuditLogs(ctx context.Context, page Pagination) ([]map[string]any, error) {
	page = normalizePagination(page)
	rows, err := s.db.Query(ctx, `
		SELECT id::text, actor_user_id::text, action, entity_type,
			COALESCE(entity_id::text, ''), COALESCE(old_value::text, '{}'),
			COALESCE(new_value::text, '{}'), created_at::text
		FROM food.admin_audit_logs
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, page.Limit, page.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, actor, action, entityType, entityID, oldValue, newValue, createdAt string
		if err := rows.Scan(&id, &actor, &action, &entityType, &entityID, &oldValue, &newValue, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "actor_user_id": actor, "action": action,
			"entity_type": entityType, "entity_id": entityID,
			"old_value": decodeJSONObject(oldValue), "new_value": decodeJSONObject(newValue),
			"created_at": createdAt,
		})
	}
	return items, rows.Err()
}

func (s *Store) paymentIntentTx(ctx context.Context, tx pgx.Tx, userID, orderID uuid.UUID) (map[string]any, error) {
	var id, method, status, provider, providerPaymentID, providerOrderID, currency string
	var amount float64
	if err := tx.QueryRow(ctx, `
		SELECT p.id::text, p.payment_method::text, p.status::text, COALESCE(p.provider, ''),
			COALESCE(p.provider_payment_id, ''), COALESCE(p.provider_order_id, ''),
			p.amount::float8, p.currency
		FROM food.payments p
		JOIN food.orders o ON o.id = p.order_id
		WHERE p.order_id = $1 AND o.user_id = $2
		ORDER BY p.created_at DESC
		LIMIT 1
	`, orderID, userID).Scan(&id, &method, &status, &provider, &providerPaymentID, &providerOrderID, &amount, &currency); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "order_id": orderID, "method": method, "status": status,
		"provider": provider, "provider_payment_id": providerPaymentID,
		"provider_order_id": providerOrderID, "amount": amount, "currency": currency,
	}, nil
}

func (s *Store) ensureDeliveryAssignmentTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, deliveryFee float64) error {
	payout := roundMoney(deliveryFee * 0.8)
	_, err := tx.Exec(ctx, `
		INSERT INTO food.delivery_assignments (order_id, status, delivery_fee, delivery_partner_payout)
		VALUES ($1, 'CREATED', $2, $3)
		ON CONFLICT (order_id) DO NOTHING
	`, orderID, deliveryFee, payout)
	return err
}

func (s *Store) assignmentForOrder(ctx context.Context, orderID uuid.UUID) (map[string]any, error) {
	var id, partnerID, status, createdAt string
	err := s.db.QueryRow(ctx, `
		SELECT id::text, COALESCE(delivery_partner_id::text, ''), status::text, created_at::text
		FROM food.delivery_assignments
		WHERE order_id = $1
	`, orderID).Scan(&id, &partnerID, &status, &createdAt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "delivery_partner_id": partnerID, "status": status, "created_at": createdAt}, nil
}

func (s *Store) orderTimeline(ctx context.Context, orderID uuid.UUID) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx, `
		SELECT COALESCE(from_status::text, ''), to_status::text, COALESCE(reason, ''), created_at::text
		FROM food.order_status_history
		WHERE order_id = $1
		ORDER BY created_at
	`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var from, to, reason, createdAt string
		if err := rows.Scan(&from, &to, &reason, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"from_status": from,
			"to_status":   to,
			"label":       orderStatusLabel(to),
			"reason":      reason,
			"completed":   true,
			"created_at":  createdAt,
		})
	}
	return items, rows.Err()
}

func (s *Store) assignmentTrackingEvents(ctx context.Context, assignmentID uuid.UUID) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx, `
		SELECT status::text, COALESCE(latitude, 0)::float8, COALESCE(longitude, 0)::float8,
			COALESCE(note, ''), created_at::text
		FROM food.delivery_tracking_events
		WHERE assignment_id = $1
		ORDER BY created_at
	`, assignmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var status, note, createdAt string
		var lat, lng float64
		if err := rows.Scan(&status, &lat, &lng, &note, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"status": status, "label": deliveryStatusLabel(status),
			"latitude": lat, "longitude": lng, "note": note, "created_at": createdAt,
		})
	}
	return items, rows.Err()
}

func (s *Store) latestDeliveryLocationForOrder(ctx context.Context, orderID uuid.UUID) (map[string]any, error) {
	var partnerID string
	var lat, lng float64
	var recordedAt string
	err := s.db.QueryRow(ctx, `
		SELECT dp.id::text, dpl.latitude::float8, dpl.longitude::float8, dpl.recorded_at::text
		FROM food.delivery_assignments da
		JOIN food.delivery_partners dp ON dp.id = da.delivery_partner_id
		JOIN food.delivery_partner_locations dpl ON dpl.delivery_partner_id = dp.id
		WHERE da.order_id = $1
		ORDER BY dpl.recorded_at DESC
		LIMIT 1
	`, orderID).Scan(&partnerID, &lat, &lng, &recordedAt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"delivery_partner_id": partnerID, "latitude": lat, "longitude": lng, "recorded_at": recordedAt}, nil
}

func locationFromJSON(raw []byte) map[string]any {
	var input map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &input) != nil {
		return nil
	}
	lat, latOK := jsonNumber(input["latitude"])
	lng, lngOK := jsonNumber(input["longitude"])
	if !latOK || !lngOK || (lat == 0 && lng == 0) {
		return nil
	}
	return map[string]any{
		"latitude":      lat,
		"longitude":     lng,
		"address_line1": input["address_line1"],
		"city":          input["city"],
		"state":         input["state"],
	}
}

func jsonNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	default:
		return 0, false
	}
}

func decodeJSONObject(raw string) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func orderStatusLabel(status string) string {
	labels := map[string]string{
		"PAYMENT_PENDING":       "Payment pending",
		"CONFIRMED":             "Order confirmed",
		"PREPARING":             "Restaurant preparing",
		"READY_FOR_PICKUP":      "Ready for pickup",
		"DELIVERY_ASSIGNED":     "Rider assigned",
		"PICKED_UP":             "Picked up",
		"OUT_FOR_DELIVERY":      "Out for delivery",
		"DELIVERED":             "Delivered",
		"CANCELLED_BY_CUSTOMER": "Cancelled by customer",
		"CANCELLED_BY_ADMIN":    "Cancelled by admin",
		"REFUNDED":              "Refunded",
	}
	if label, ok := labels[status]; ok {
		return label
	}
	return strings.ReplaceAll(strings.Title(strings.ToLower(status)), "_", " ")
}

func deliveryStatusLabel(status string) string {
	labels := map[string]string{
		"CREATED":               "Assignment created",
		"ASSIGNED":              "Assignment offered",
		"ACCEPTED":              "Rider accepted",
		"ARRIVED_AT_RESTAURANT": "Arrived at restaurant",
		"PICKED_UP":             "Picked up",
		"ARRIVED_AT_CUSTOMER":   "Arrived at customer",
		"DELIVERED":             "Delivered",
	}
	if label, ok := labels[status]; ok {
		return label
	}
	return strings.ReplaceAll(strings.Title(strings.ToLower(status)), "_", " ")
}

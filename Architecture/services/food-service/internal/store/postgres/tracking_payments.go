package postgres

import (
	"context"
	"encoding/json"
	"errors"
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
	var etaAt *time.Time
	var etaSource string
	if err := s.db.QueryRow(ctx, `
		SELECT order_number, status::text, restaurant_address_snapshot,
			delivery_address_snapshot, COALESCE(estimated_delivery_minutes, 0),
			eta_at, COALESCE(eta_source, '')
		FROM food.orders
		WHERE id = $1 AND user_id = $2
	`, orderID, userID).Scan(&orderNumber, &status, &restaurantSnapshot, &deliverySnapshot, &etaMins, &etaAt, &etaSource); err != nil {
		return nil, err
	}
	// B6: eta_at / eta_source are null unless the order can still arrive.
	var etaAtOut, etaSourceOut any
	if etaAt != nil && validETASource(etaSource) && ETAVisible(status) {
		etaAtOut, etaSourceOut = FormatETA(*etaAt), etaSource
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
		"eta_at":                     etaAtOut,
		"eta_source":                 etaSourceOut,
	}, nil
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

// RiderLocationMinInterval is the throttle: at most one rider.location frame
// per order in this window, however often the rider pings.
const RiderLocationMinInterval = 5 * time.Second

// LocationUpdate is one rider ping.
type LocationUpdate struct {
	Latitude       float64
	Longitude      float64
	AccuracyMeters *float64
	// Heading is an optional compass heading in degrees, 0 to 360.
	Heading *float64
}

// RiderLocationFrame names one order a ping must reach live. The store adds
// one only when RiderLocationShareable and the per-order throttle allowed it.
type RiderLocationFrame struct {
	OrderID      uuid.UUID
	AssignmentID uuid.UUID
	// ETAAt / ETASource are the order's stored ETA when the ping was taken
	// (B6); the service replaces them with a fresh one when this ping
	// recomputed it.
	ETAAt     *time.Time
	ETASource string
}

// DeliveryLocationResult is the POST /v1/food/delivery/location response.
type DeliveryLocationResult struct {
	ID                uuid.UUID `json:"id"`
	DeliveryPartnerID uuid.UUID `json:"delivery_partner_id"`
	// AssignmentID is the newest active assignment (the zero UUID when the
	// rider holds none), kept for clients written against the one-assignment
	// response; AssignmentIDs lists every active assignment the ping updated.
	AssignmentID   uuid.UUID   `json:"assignment_id"`
	AssignmentIDs  []uuid.UUID `json:"assignment_ids"`
	Latitude       float64     `json:"latitude"`
	Longitude      float64     `json:"longitude"`
	AccuracyMeters *float64    `json:"accuracy_meters"`
	Heading        *float64    `json:"heading"`
	RecordedAt     string      `json:"recorded_at"`

	// For the service's live fan-out; never serialised.
	RecordedAtTime time.Time            `json:"-"`
	Frames         []RiderLocationFrame `json:"-"`
	// ETAJobs are the orders this ping claimed for ETA recomputation (B6).
	ETAJobs []ETAJob `json:"-"`
}

type activeAssignment struct {
	id, orderID                   uuid.UUID
	assignmentStatus, orderStatus string
	etaAt                         *time.Time
	etaSource                     string
}

// UpdateDeliveryLocation records a ping, writes a tracking event on EVERY
// active assignment the rider holds (a batch run holds several), and returns
// the orders whose customers should get a rider.location frame now.
func (s *Store) UpdateDeliveryLocation(ctx context.Context, userID uuid.UUID, in LocationUpdate) (*DeliveryLocationResult, error) {
	if in.Latitude < -90 || in.Latitude > 90 || in.Longitude < -180 || in.Longitude > 180 {
		return nil, fmt.Errorf("invalid coordinates")
	}
	if in.Heading != nil && (*in.Heading < 0 || *in.Heading > 360) {
		return nil, fmt.Errorf("invalid heading: must be between 0 and 360")
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

	res := &DeliveryLocationResult{
		DeliveryPartnerID: partner.ID, AssignmentIDs: []uuid.UUID{},
		Latitude: in.Latitude, Longitude: in.Longitude, AccuracyMeters: in.AccuracyMeters, Heading: in.Heading,
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.delivery_partner_locations (
			delivery_partner_id, latitude, longitude, accuracy_meters, heading
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, recorded_at, recorded_at::text
	`, partner.ID, in.Latitude, in.Longitude, in.AccuracyMeters, in.Heading).Scan(&res.ID, &res.RecordedAtTime, &res.RecordedAt); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_partners
		SET current_latitude = $2, current_longitude = $3
		WHERE id = $1
	`, partner.ID, in.Latitude, in.Longitude); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT da.id, da.order_id, da.status::text, o.status::text, o.eta_at, COALESCE(o.eta_source, '')
		FROM food.delivery_assignments da
		JOIN food.orders o ON o.id = da.order_id
		WHERE da.delivery_partner_id = $1
			AND da.status NOT IN ('DELIVERED', 'FAILED', 'CANCELLED', 'REJECTED')
		ORDER BY da.created_at DESC, da.id
	`, partner.ID)
	if err != nil {
		return nil, err
	}
	var active []activeAssignment
	for rows.Next() {
		var a activeAssignment
		if err := rows.Scan(&a.id, &a.orderID, &a.assignmentStatus, &a.orderStatus, &a.etaAt, &a.etaSource); err != nil {
			rows.Close()
			return nil, err
		}
		active = append(active, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i, a := range active {
		if i == 0 {
			res.AssignmentID = a.id
		}
		res.AssignmentIDs = append(res.AssignmentIDs, a.id)
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.delivery_tracking_events (
				assignment_id, delivery_partner_id, status, latitude, longitude, note
			)
			VALUES ($1, $2, $3::text::food.assignment_status, $4, $5, 'location update')
		`, a.id, partner.ID, a.assignmentStatus, in.Latitude, in.Longitude); err != nil {
			return nil, err
		}
		if !RiderLocationShareable(a.assignmentStatus, a.orderStatus) {
			continue
		}
		// B6: the ETA recompute claim, at most once per order per
		// ETARecomputeInterval on every replica. Independent of the frame
		// throttle below; a fresh ETA rides the next frame that goes out.
		job, err := claimOrderETATx(ctx, tx, a)
		if err != nil {
			return nil, err
		}
		if job != nil {
			res.ETAJobs = append(res.ETAJobs, *job)
		}
		// The throttle claim: succeeds for at most one ping per order per
		// RiderLocationMinInterval, on every replica.
		var claimed bool
		err = tx.QueryRow(ctx, `
			UPDATE food.delivery_assignments
			SET location_published_at = NOW()
			WHERE id = $1
				AND (location_published_at IS NULL
					OR location_published_at <= NOW() - make_interval(secs => $2::float8))
			RETURNING TRUE
		`, a.id, RiderLocationMinInterval.Seconds()).Scan(&claimed)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		res.Frames = append(res.Frames, RiderLocationFrame{OrderID: a.orderID, AssignmentID: a.id, ETAAt: a.etaAt, ETASource: a.etaSource})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Store) GetAssignmentTracking(ctx context.Context, userID, assignmentID uuid.UUID) (map[string]any, error) {
	partner, err := s.GetDeliveryPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, deliveryAssignmentSelect+`
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
		var r PartnerSettlementRow
		if err := rows.Scan(&r.ID, &r.PeriodStart, &r.PeriodEnd, &r.Gross, &r.Commission, &r.RefundAdjustment, &r.Penalty, &r.Payout,
			&r.Status, &r.PaidReference, &r.PaidAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, PartnerSettlementMap(restaurantID, r))
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
	return PartnerSummaryMap(restaurantID, orders, delivered, refunded, gross, commission, refunds), nil
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

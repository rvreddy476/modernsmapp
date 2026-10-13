package postgres

import (
	"context"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type BatchRouteStop struct {
	OrderID             uuid.UUID
	Latitude, Longitude float64
}
type BatchRouteInput struct {
	BatchID              uuid.UUID
	PickupLat, PickupLng float64
	Stops                []BatchRouteStop
}

// Route previews are only for ACTIVE partners who accepted every batch member,
// before pickup. Recheck each request, including cached previews. No names,
// phones, OTPs, instructions or address text are read or sent to Google.
func (s *Store) GetAcceptedBatchRouteInput(ctx context.Context, userID, orderID uuid.UUID) (*BatchRouteInput, error) {
	rows, err := s.db.Query(ctx, `
		SELECT b.id, da.order_id, COALESCE(da.delivery_partner_id = dp.id AND o.restaurant_id = b.restaurant_id, false), da.status, o.status::text,
			r.latitude::float8, r.longitude::float8,
			CASE WHEN jsonb_typeof(o.delivery_address_snapshot->'latitude') = 'number'
				THEN (o.delivery_address_snapshot->>'latitude')::float8 END,
			CASE WHEN jsonb_typeof(o.delivery_address_snapshot->'longitude') = 'number'
				THEN (o.delivery_address_snapshot->>'longitude')::float8 END
		FROM food.delivery_assignments da
		JOIN food.orders o ON o.id = da.order_id
		JOIN food.delivery_batches b ON b.id = da.batch_id AND b.status = 'assigned'
		JOIN food.restaurants r ON r.id = b.restaurant_id
		JOIN food.delivery_partners dp ON dp.user_id = $1 AND dp.status = 'ACTIVE'
		WHERE da.batch_id = (
			SELECT anchor.batch_id FROM food.delivery_assignments anchor
			WHERE anchor.order_id = $2 AND anchor.delivery_partner_id = dp.id
			  AND anchor.status IN ('ACCEPTED', 'ARRIVED_AT_RESTAURANT')
		)
		ORDER BY da.batch_sequence LIMIT 4
	`, userID, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	input := &BatchRouteInput{}
	for rows.Next() {
		var stop BatchRouteStop
		var owned bool
		var assignmentStatus, orderStatus string
		var lat, lng, dropLat, dropLng *float64
		if err := rows.Scan(&input.BatchID, &stop.OrderID, &owned, &assignmentStatus, &orderStatus, &lat, &lng, &dropLat, &dropLng); err != nil {
			return nil, err
		}
		if !routePreviewVisible(owned, assignmentStatus, orderStatus) || !validPin(lat, lng) || !validPin(dropLat, dropLng) {
			return nil, pgx.ErrNoRows
		}
		input.PickupLat, input.PickupLng = *lat, *lng
		stop.Latitude, stop.Longitude = *dropLat, *dropLng
		input.Stops = append(input.Stops, stop)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(input.Stops) < 2 || len(input.Stops) > 3 {
		return nil, pgx.ErrNoRows
	}
	return input, nil
}

func routePreviewVisible(owned bool, assignmentStatus, orderStatus string) bool {
	return owned && (assignmentStatus == "ACCEPTED" || assignmentStatus == "ARRIVED_AT_RESTAURANT") && orderStatus == orderstate.DeliveryAssigned && DropVisible(assignmentStatus, orderStatus)
}

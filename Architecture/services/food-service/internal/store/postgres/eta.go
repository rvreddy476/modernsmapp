package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/atpost/food-service/internal/routing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// B6: routing and ETA.
//
// orders.eta_at is when the food is expected at the door, eta_source says
// whether every leg behind it came from Google ("google") or any leg was the
// straight-line estimate ("haversine"), and eta_computed_at is the claim that
// throttles recomputation to ETARecomputeInterval per order on every replica.

// ETARecomputeInterval: rider pings recompute an order's ETA at most this often.
const ETARecomputeInterval = 60 * time.Second

// RouteEstimate is a restaurant-to-customer ride priced before the placing
// transaction, with the two points it was priced for.
type RouteEstimate struct {
	From  routing.LatLng
	To    routing.LatLng
	Route routing.Route
}

// WithRouter wires the routing chain PlaceOrder prices the delivery ride with.
// Unwired (or when the ride cannot be priced) PlaceOrder uses the ordering
// config's haversine estimate.
func (s *Store) WithRouter(r routing.Router) *Store {
	s.router = r
	return s
}

// pricePlacementLeg prices the restaurant-to-customer ride for the cart the
// user is about to check out, BEFORE the placing transaction opens: a Google
// call can take up to the routing timeout and no transaction (holding the
// idempotency row and the cart) may wait on it. Nil when there is no router or
// the points cannot be read; PlaceOrder then reports the real problem itself.
func (s *Store) pricePlacementLeg(ctx context.Context, userID, addressID uuid.UUID) *RouteEstimate {
	if s.router == nil {
		return nil
	}
	var rLat, rLng, aLat, aLng *float64
	if err := s.db.QueryRow(ctx, `
		SELECT r.latitude::float8, r.longitude::float8, a.latitude::float8, a.longitude::float8
		FROM food.carts c
		JOIN food.restaurants r ON r.id = c.restaurant_id
		JOIN food.customer_addresses a ON a.id = $2 AND a.user_id = c.user_id AND a.is_deleted = FALSE
		WHERE c.user_id = $1
	`, userID, addressID).Scan(&rLat, &rLng, &aLat, &aLng); err != nil {
		return nil
	}
	if rLat == nil || rLng == nil || aLat == nil || aLng == nil {
		return nil
	}
	from, to := routing.LatLng{Lat: *rLat, Lng: *rLng}, routing.LatLng{Lat: *aLat, Lng: *aLng}
	r, err := s.router.Route(ctx, from, to)
	if err != nil {
		return nil
	}
	return &RouteEstimate{From: from, To: to, Route: r}
}

// placementLeg is the ride PlaceOrder uses: the pre-priced estimate only when
// it was priced for exactly the restaurant and address the transaction loaded
// (the cart or the address could have changed in between), otherwise the
// haversine estimate for those points.
func (s *Store) placementLeg(priced *RouteEstimate, restaurant, customer routing.LatLng) routing.Route {
	if priced != nil && priced.From == restaurant && priced.To == customer &&
		validETASource(priced.Route.Source) && priced.Route.Duration >= 0 {
		return priced.Route
	}
	r, err := s.ordering.Haversine().Route(context.Background(), restaurant, customer)
	if err != nil {
		return routing.Route{Source: routing.SourceHaversine}
	}
	return r
}

// rideSeconds is a ride rounded to the second, so float noise in a duration
// cannot add a minute when it is rounded up.
func rideSeconds(ride time.Duration) float64 {
	secs := math.Round(ride.Seconds())
	if secs < 0 {
		return 0
	}
	return secs
}

// deliveryMinutesForRoute = preparation + the ride, rounded up to the minute.
// With the haversine defaults this is lane 0c's estimateDeliveryMinutes.
func deliveryMinutesForRoute(prepMinutes int, ride time.Duration) int {
	return prepMinutes + int(math.Ceil(rideSeconds(ride)/60))
}

// placementETASeconds is eta_at - placed_at at placement: preparation plus the
// restaurant-to-customer ride. No rider holds the order yet, so there is no
// rider-to-restaurant leg; dispatch runs while the kitchen cooks.
func placementETASeconds(prepMinutes int, ride time.Duration) float64 {
	return float64(prepMinutes*60) + rideSeconds(ride)
}

func validETASource(source string) bool {
	return source == routing.SourceGoogle || source == routing.SourceHaversine
}

// ETAJob is an order a rider ping claimed for ETA recomputation. The service
// prices the legs outside the ping's transaction and writes the result back
// with RecordOrderETA, guarded on ClaimedAt.
type ETAJob struct {
	OrderID          uuid.UUID
	AssignmentID     uuid.UUID
	OrderStatus      string
	AssignmentStatus string
	// Restaurant and Customer come from the order's address snapshots; nil when
	// the snapshot has no coordinates (no ETA can be priced).
	Restaurant *routing.LatLng
	Customer   *routing.LatLng
	// FoodReadyAt is when the kitchen is expected to have the food ready
	// (confirmed, or placed, plus the estimated preparation minutes); nil once
	// the order has reached READY_FOR_PICKUP.
	FoodReadyAt *time.Time
	// ClaimedAt is the eta_computed_at this claim wrote.
	ClaimedAt time.Time
}

// claimOrderETATx takes the per-order recompute slot inside the ping's
// transaction: a guarded UPDATE that succeeds at most once per
// ETARecomputeInterval however many replicas handle pings. Nil when the slot
// is taken.
func claimOrderETATx(ctx context.Context, tx pgx.Tx, a activeAssignment) (*ETAJob, error) {
	job := ETAJob{OrderID: a.orderID, AssignmentID: a.id, OrderStatus: a.orderStatus, AssignmentStatus: a.assignmentStatus}
	err := tx.QueryRow(ctx, `
		UPDATE food.orders
		SET eta_computed_at = NOW()
		WHERE id = $1
			AND (eta_computed_at IS NULL
				OR eta_computed_at <= NOW() - make_interval(secs => $2::float8))
		RETURNING eta_computed_at
	`, a.orderID, ETARecomputeInterval.Seconds()).Scan(&job.ClaimedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim eta recompute: %w", err)
	}
	if err := loadETAInputs(ctx, tx, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// loadETAInputs reads a claimed order's map points and when its food is
// expected ready; shared by the rider-ping claim and the pre-accept claim.
func loadETAInputs(ctx context.Context, q rowQuerier, job *ETAJob) error {
	var restaurantSnapshot, deliverySnapshot []byte
	if err := q.QueryRow(ctx, `
		SELECT o.restaurant_address_snapshot, o.delivery_address_snapshot,
			CASE WHEN EXISTS (
					SELECT 1 FROM food.order_status_history h
					WHERE h.order_id = o.id AND h.to_status = 'READY_FOR_PICKUP'
				) THEN NULL
				ELSE COALESCE(
					(SELECT MIN(h.created_at) FROM food.order_status_history h
					 WHERE h.order_id = o.id AND h.to_status = 'CONFIRMED'),
					o.placed_at
				) + make_interval(mins => COALESCE(o.estimated_preparation_minutes, 0))
			END
		FROM food.orders o
		WHERE o.id = $1
	`, job.OrderID).Scan(&restaurantSnapshot, &deliverySnapshot, &job.FoodReadyAt); err != nil {
		return fmt.Errorf("read eta inputs: %w", err)
	}
	job.Restaurant = latLngFromSnapshot(restaurantSnapshot)
	job.Customer = latLngFromSnapshot(deliverySnapshot)
	return nil
}

// RecordOrderETA writes a recomputed ETA, only if the claim it was computed
// under is still the latest (a slower replica cannot overwrite a newer
// estimate). Reports whether the write applied.
func (s *Store) RecordOrderETA(ctx context.Context, orderID uuid.UUID, claimedAt, etaAt time.Time, source string) (bool, error) {
	if !validETASource(source) {
		return false, fmt.Errorf("eta source %q is not google or haversine", source)
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE food.orders
		SET eta_at = $2, eta_source = $3
		WHERE id = $1 AND eta_computed_at = $4
	`, orderID, etaAt, source, claimedAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// latLngFromSnapshot reads the coordinates of an address snapshot.
func latLngFromSnapshot(raw []byte) *routing.LatLng {
	loc := locationFromJSON(raw)
	if loc == nil {
		return nil
	}
	lat, _ := loc["latitude"].(float64)
	lng, _ := loc["longitude"].(float64)
	p := routing.LatLng{Lat: lat, Lng: lng}
	if !p.Valid() {
		return nil
	}
	return &p
}

// FormatETA is the wire form of eta_at: RFC 3339 in UTC, to the second.
func FormatETA(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
	_ "time/tzdata" // the container image may have no zoneinfo

	"github.com/atpost/food-service/internal/geo"
	"github.com/atpost/food-service/internal/routing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// HTTP 422 FOOD_RESTAURANT_NOT_ACCEPTING.
	ErrRestaurantNotAccepting = errors.New("restaurant is not accepting orders")
	// HTTP 422 FOOD_RESTAURANT_OUTSIDE_HOURS.
	ErrRestaurantOutsideHours = errors.New("restaurant is closed at this time")
	// HTTP 422 FOOD_ADDRESS_OUT_OF_RANGE.
	ErrAddressOutOfRange = errors.New("delivery address is outside the restaurant's delivery range")
	// HTTP 422 FOOD_ADDRESS_LOCATION_REQUIRED.
	ErrAddressLocationRequired = errors.New("delivery address has no map location")
	// HTTP 422 FOOD_RESTAURANT_LOCATION_MISSING.
	ErrRestaurantLocationMissing = errors.New("restaurant has no location configured")
)

const (
	defaultRestaurantTimezone   = "Asia/Kolkata"
	defaultDeliveryRadiusKM     = 7.0
	defaultAvgRiderSpeedKmh     = 20.0
	riderPayoutShareOfDeliveryFee = 0.8
)

// OrderingConfig tunes PlaceOrder's serviceability and ETA.
type OrderingConfig struct {
	// Location evaluates restaurant_operating_hours (one zone for all
	// restaurants; the table stores wall-clock TIME without a zone).
	Location *time.Location
	// DefaultDeliveryRadiusKM applies when a restaurant has no active
	// restaurant_service_areas rows.
	DefaultDeliveryRadiusKM float64
	AvgRiderSpeedKmh        float64
	// RouteWindingFactor is road distance over straight-line distance for the
	// haversine estimate (routing.Haversine), >= 1.
	RouteWindingFactor float64
	// Now is injectable for tests.
	Now func() time.Time
}

// DefaultOrderingConfig: Asia/Kolkata, 7 km, 20 km/h, winding 1.0, time.Now.
func DefaultOrderingConfig() OrderingConfig {
	loc, err := time.LoadLocation(defaultRestaurantTimezone)
	if err != nil {
		loc = time.FixedZone("IST", 5*3600+1800)
	}
	return OrderingConfig{
		Location:                loc,
		DefaultDeliveryRadiusKM: defaultDeliveryRadiusKM,
		AvgRiderSpeedKmh:        defaultAvgRiderSpeedKmh,
		RouteWindingFactor:      routing.DefaultWindingFactor,
		Now:                     time.Now,
	}
}

// Haversine is the no-network ride estimate these settings describe: what
// PlaceOrder uses when the service priced no leg, and the routing chain's
// fallback.
func (c OrderingConfig) Haversine() routing.Haversine {
	return routing.Haversine{SpeedKmh: c.AvgRiderSpeedKmh, WindingFactor: c.RouteWindingFactor}
}

// OrderingConfigFromEnv reads FOOD_RESTAURANT_TIMEZONE,
// FOOD_DEFAULT_DELIVERY_RADIUS_KM, FOOD_AVG_RIDER_SPEED_KMH and
// FOOD_ROUTE_WINDING_FACTOR (>= 1, default 1.0) over the defaults. An invalid
// value is an error so a typo cannot silently widen the delivery radius.
func OrderingConfigFromEnv() (OrderingConfig, error) {
	cfg := DefaultOrderingConfig()
	if tz := os.Getenv("FOOD_RESTAURANT_TIMEZONE"); tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return cfg, fmt.Errorf("FOOD_RESTAURANT_TIMEZONE: %w", err)
		}
		cfg.Location = loc
	}
	for key, dst := range map[string]*float64{
		"FOOD_DEFAULT_DELIVERY_RADIUS_KM": &cfg.DefaultDeliveryRadiusKM,
		"FOOD_AVG_RIDER_SPEED_KMH":        &cfg.AvgRiderSpeedKmh,
	} {
		if raw := os.Getenv(key); raw != "" {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil || v <= 0 {
				return cfg, fmt.Errorf("%s must be a positive number", key)
			}
			*dst = v
		}
	}
	if raw := os.Getenv("FOOD_ROUTE_WINDING_FACTOR"); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 1 || v > 3 {
			return cfg, fmt.Errorf("FOOD_ROUTE_WINDING_FACTOR must be a number from 1 to 3")
		}
		cfg.RouteWindingFactor = v
	}
	return cfg, nil
}

// WithOrderingConfig replaces the ordering config; zero fields keep defaults.
func (s *Store) WithOrderingConfig(cfg OrderingConfig) *Store {
	def := DefaultOrderingConfig()
	if cfg.Location == nil {
		cfg.Location = def.Location
	}
	if cfg.DefaultDeliveryRadiusKM <= 0 {
		cfg.DefaultDeliveryRadiusKM = def.DefaultDeliveryRadiusKM
	}
	if cfg.AvgRiderSpeedKmh <= 0 {
		cfg.AvgRiderSpeedKmh = def.AvgRiderSpeedKmh
	}
	if cfg.RouteWindingFactor < 1 {
		cfg.RouteWindingFactor = def.RouteWindingFactor
	}
	if cfg.Now == nil {
		cfg.Now = def.Now
	}
	s.ordering = cfg
	return s
}

// hoursWindow is one restaurant_operating_hours row. Day uses Go's
// time.Weekday numbering (0 = Sunday), which is also Postgres EXTRACT(DOW).
type hoursWindow struct {
	Day    int
	Opens  int // seconds since midnight
	Closes int // seconds since midnight
	Closed bool
}

// openAt reports whether a restaurant with these windows is open at `now`
// (already converted to the restaurant zone).
//
//   - no rows at all: unrestricted;
//   - a day with an is_closed row is closed all day;
//   - several windows per day are allowed;
//   - closes <= opens spans midnight, so the previous day's overnight window
//     also counts before its closing time.
func openAt(now time.Time, windows []hoursWindow) bool {
	if len(windows) == 0 {
		return true
	}
	t := now.Hour()*3600 + now.Minute()*60 + now.Second()
	today := int(now.Weekday())
	yesterday := (today + 6) % 7
	closedDay := map[int]bool{}
	for _, w := range windows {
		if w.Closed {
			closedDay[w.Day] = true
		}
	}
	for _, w := range windows {
		if w.Closed {
			continue
		}
		overnight := w.Closes <= w.Opens
		if w.Day == today && !closedDay[today] {
			if overnight && t >= w.Opens {
				return true
			}
			if !overnight && t >= w.Opens && t < w.Closes {
				return true
			}
		}
		if w.Day == yesterday && !closedDay[yesterday] && overnight && t < w.Closes {
			return true
		}
	}
	return false
}

type serviceArea struct {
	CenterLat *float64
	CenterLng *float64
	RadiusKM  float64
}

// addressServiceable: any active area covering the address wins (an area
// without its own centre is centred on the restaurant); no areas means the
// default radius around the restaurant.
func addressServiceable(restLat, restLng, addrLat, addrLng float64, areas []serviceArea, defaultRadiusKM float64) bool {
	if len(areas) == 0 {
		return geo.HaversineKM(restLat, restLng, addrLat, addrLng) <= defaultRadiusKM
	}
	for _, a := range areas {
		lat, lng := restLat, restLng
		if a.CenterLat != nil && a.CenterLng != nil {
			lat, lng = *a.CenterLat, *a.CenterLng
		}
		if geo.HaversineKM(lat, lng, addrLat, addrLng) <= a.RadiusKM {
			return true
		}
	}
	return false
}

// riderPayoutForFee is the rider's share of the delivery fee. Same rule as
// ensureDeliveryAssignmentTx (tracking_payments.go, payments stream) uses.
func riderPayoutForFee(deliveryFee float64) float64 {
	return roundMoney(deliveryFee * riderPayoutShareOfDeliveryFee)
}

// GeoPoint is a customer-supplied map position (the list's, the detail's and
// add-to-cart's lat/lng), already validated by the handler.
type GeoPoint struct {
	Lat float64
	Lng float64
}

// deliveryPoint is where an order would go. A nil *deliveryPoint means no
// address is known yet (add-to-cart without one): locations and range are then
// not checked. Nil fields are an address saved without a map pin.
type deliveryPoint struct {
	Lat *float64
	Lng *float64
}

func pointOf(g *GeoPoint) *deliveryPoint {
	if g == nil {
		return nil
	}
	lat, lng := g.Lat, g.Lng
	return &deliveryPoint{Lat: &lat, Lng: &lng}
}

// serviceabilityFacts is everything the serviceability rule reads about one
// restaurant. loadServiceabilityFacts is the only thing that builds it, so
// PlaceOrder, add-to-cart, the restaurant list and the restaurant detail all
// read the same columns the same way.
type serviceabilityFacts struct {
	// Active is status ACTIVE and is_open and is_accepting_orders.
	Active  bool
	Lat     *float64
	Lng     *float64
	Windows []hoursWindow
	Areas   []serviceArea
}

// evaluateServiceability is THE serviceability rule, shared by PlaceOrder,
// AddCartItem, ListRestaurants and GetRestaurant so the list can never promise
// what placing the order refuses. Checks run in PlaceOrder's order: accepting,
// restaurant location, address location, hours, range. With to == nil only
// accepting and hours are checked. Missing coordinates fail closed.
//
// distanceKM is the straight-line restaurant->address distance whenever both
// points are known, including on a refusal (the list shows it).
func (c OrderingConfig) evaluateServiceability(now time.Time, f serviceabilityFacts, to *deliveryPoint) (distanceKM *float64, err error) {
	if to != nil && f.Lat != nil && f.Lng != nil && to.Lat != nil && to.Lng != nil {
		d := geo.HaversineKM(*f.Lat, *f.Lng, *to.Lat, *to.Lng)
		distanceKM = &d
	}
	if !f.Active {
		return distanceKM, ErrRestaurantNotAccepting
	}
	if to != nil {
		if f.Lat == nil || f.Lng == nil {
			return distanceKM, ErrRestaurantLocationMissing
		}
		if to.Lat == nil || to.Lng == nil {
			return distanceKM, ErrAddressLocationRequired
		}
	}
	if !openAt(now.In(c.Location), f.Windows) {
		return distanceKM, ErrRestaurantOutsideHours
	}
	if to != nil && !addressServiceable(*f.Lat, *f.Lng, *to.Lat, *to.Lng, f.Areas, c.DefaultDeliveryRadiusKM) {
		return distanceKM, ErrAddressOutOfRange
	}
	return distanceKM, nil
}

// IsServiceabilityRefusal reports whether err is one of the refusals
// evaluateServiceability returns (each is a 422 in handler_errors.go).
func IsServiceabilityRefusal(err error) bool {
	for _, target := range []error{ErrRestaurantNotAccepting, ErrRestaurantLocationMissing, ErrAddressLocationRequired,
		ErrRestaurantOutsideHours, ErrAddressOutOfRange} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// checkServiceabilityTx loads one restaurant's facts through q (a transaction
// or the pool) and applies the shared rule. A restaurant that does not exist
// is pgx.ErrNoRows.
func (s *Store) checkServiceabilityTx(ctx context.Context, q hoursQuerier, restaurantID uuid.UUID, to *deliveryPoint) (*serviceabilityFacts, *float64, error) {
	all, err := loadServiceabilityFacts(ctx, q, []uuid.UUID{restaurantID})
	if err != nil {
		return nil, nil, err
	}
	f, ok := all[restaurantID]
	if !ok {
		return nil, nil, pgx.ErrNoRows
	}
	distanceKM, err := s.ordering.evaluateServiceability(s.ordering.Now(), *f, to)
	return f, distanceKM, err
}

// nextOpenAt is the first moment after now (already in the restaurant zone)
// at which openAt becomes true, looking at most a week ahead. False when the
// schedule is unrestricted, open now, or never opens.
func nextOpenAt(now time.Time, windows []hoursWindow) (time.Time, bool) {
	if len(windows) == 0 || openAt(now, windows) {
		return time.Time{}, false
	}
	closedDay := map[int]bool{}
	for _, w := range windows {
		if w.Closed {
			closedDay[w.Day] = true
		}
	}
	y, m, d := now.Date()
	for offset := 0; offset <= 7; offset++ {
		weekday := int(time.Date(y, m, d+offset, 12, 0, 0, 0, now.Location()).Weekday())
		if closedDay[weekday] {
			continue
		}
		var best time.Time
		found := false
		for _, w := range windows {
			if w.Closed || w.Day != weekday {
				continue
			}
			at := time.Date(y, m, d+offset, w.Opens/3600, (w.Opens%3600)/60, w.Opens%60, 0, now.Location())
			if at.After(now) && (!found || at.Before(best)) {
				best, found = at, true
			}
		}
		if found {
			return best, true
		}
	}
	return time.Time{}, false
}

// hoursQuerier is a transaction or the pool.
type hoursQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func loadHours(ctx context.Context, q hoursQuerier, restaurantID uuid.UUID) ([]hoursWindow, error) {
	all, err := loadHoursFor(ctx, q, []uuid.UUID{restaurantID})
	if err != nil {
		return nil, err
	}
	return all[restaurantID], nil
}

func loadHoursFor(ctx context.Context, q hoursQuerier, restaurantIDs []uuid.UUID) (map[uuid.UUID][]hoursWindow, error) {
	rows, err := q.Query(ctx, `
		SELECT restaurant_id, day_of_week, EXTRACT(EPOCH FROM opens_at)::int, EXTRACT(EPOCH FROM closes_at)::int, is_closed
		FROM food.restaurant_operating_hours
		WHERE restaurant_id = ANY($1::uuid[])
	`, restaurantIDs)
	if err != nil {
		return nil, fmt.Errorf("load operating hours: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID][]hoursWindow{}
	for rows.Next() {
		var id uuid.UUID
		var w hoursWindow
		if err := rows.Scan(&id, &w.Day, &w.Opens, &w.Closes, &w.Closed); err != nil {
			return nil, err
		}
		out[id] = append(out[id], w)
	}
	return out, rows.Err()
}

// loadServiceabilityFacts reads the facts for each restaurant that exists;
// ids with no restaurant are absent from the map.
func loadServiceabilityFacts(ctx context.Context, q hoursQuerier, restaurantIDs []uuid.UUID) (map[uuid.UUID]*serviceabilityFacts, error) {
	out := make(map[uuid.UUID]*serviceabilityFacts, len(restaurantIDs))
	if len(restaurantIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT id, (status = 'ACTIVE' AND is_open = TRUE AND is_accepting_orders = TRUE),
			latitude::float8, longitude::float8
		FROM food.restaurants
		WHERE id = ANY($1::uuid[])
	`, restaurantIDs)
	if err != nil {
		return nil, fmt.Errorf("load restaurant serviceability: %w", err)
	}
	for rows.Next() {
		var id uuid.UUID
		f := &serviceabilityFacts{}
		if err := rows.Scan(&id, &f.Active, &f.Lat, &f.Lng); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = f
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	hours, err := loadHoursFor(ctx, q, restaurantIDs)
	if err != nil {
		return nil, err
	}
	areaRows, err := q.Query(ctx, `
		SELECT restaurant_id, center_latitude::float8, center_longitude::float8, radius_km::float8
		FROM food.restaurant_service_areas
		WHERE restaurant_id = ANY($1::uuid[]) AND is_active = TRUE
	`, restaurantIDs)
	if err != nil {
		return nil, fmt.Errorf("load service areas: %w", err)
	}
	defer areaRows.Close()
	for areaRows.Next() {
		var id uuid.UUID
		var a serviceArea
		if err := areaRows.Scan(&id, &a.CenterLat, &a.CenterLng, &a.RadiusKM); err != nil {
			return nil, err
		}
		if f, ok := out[id]; ok {
			f.Areas = append(f.Areas, a)
		}
	}
	if err := areaRows.Err(); err != nil {
		return nil, err
	}
	for id, f := range out {
		f.Windows = hours[id]
	}
	return out, nil
}

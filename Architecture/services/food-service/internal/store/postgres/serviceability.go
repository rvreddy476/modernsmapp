package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
	_ "time/tzdata" // the container image may have no zoneinfo

	"github.com/atpost/food-service/internal/geo"
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
	// Now is injectable for tests.
	Now func() time.Time
}

// DefaultOrderingConfig: Asia/Kolkata, 7 km, 20 km/h, time.Now.
func DefaultOrderingConfig() OrderingConfig {
	loc, err := time.LoadLocation(defaultRestaurantTimezone)
	if err != nil {
		loc = time.FixedZone("IST", 5*3600+1800)
	}
	return OrderingConfig{
		Location:                loc,
		DefaultDeliveryRadiusKM: defaultDeliveryRadiusKM,
		AvgRiderSpeedKmh:        defaultAvgRiderSpeedKmh,
		Now:                     time.Now,
	}
}

// OrderingConfigFromEnv reads FOOD_RESTAURANT_TIMEZONE,
// FOOD_DEFAULT_DELIVERY_RADIUS_KM and FOOD_AVG_RIDER_SPEED_KMH over the
// defaults. An invalid value is an error so a typo cannot silently widen the
// delivery radius.
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

// estimateDeliveryMinutes = preparation + ride time at the average speed,
// rounded up to the minute.
func estimateDeliveryMinutes(prepMinutes int, distanceKM, speedKmh float64) int {
	if speedKmh <= 0 {
		speedKmh = defaultAvgRiderSpeedKmh
	}
	if distanceKM < 0 {
		distanceKM = 0
	}
	return prepMinutes + int(math.Ceil(distanceKM/speedKmh*60))
}

// riderPayoutForFee is the rider's share of the delivery fee. Same rule as
// ensureDeliveryAssignmentTx (tracking_payments.go, payments stream) uses.
func riderPayoutForFee(deliveryFee float64) float64 {
	return roundMoney(deliveryFee * riderPayoutShareOfDeliveryFee)
}

// checkServiceabilityTx enforces location, hours and range for PlaceOrder and
// returns the restaurant->address distance. Missing coordinates fail closed.
func (s *Store) checkServiceabilityTx(ctx context.Context, tx pgx.Tx, restaurantID uuid.UUID, restLat, restLng, addrLat, addrLng *float64) (float64, error) {
	if restLat == nil || restLng == nil {
		return 0, ErrRestaurantLocationMissing
	}
	if addrLat == nil || addrLng == nil {
		return 0, ErrAddressLocationRequired
	}

	windows, err := loadHoursTx(ctx, tx, restaurantID)
	if err != nil {
		return 0, err
	}
	if !openAt(s.ordering.Now().In(s.ordering.Location), windows) {
		return 0, ErrRestaurantOutsideHours
	}

	areas, err := loadServiceAreasTx(ctx, tx, restaurantID)
	if err != nil {
		return 0, err
	}
	if !addressServiceable(*restLat, *restLng, *addrLat, *addrLng, areas, s.ordering.DefaultDeliveryRadiusKM) {
		return 0, ErrAddressOutOfRange
	}
	return geo.HaversineKM(*restLat, *restLng, *addrLat, *addrLng), nil
}

func loadHoursTx(ctx context.Context, tx pgx.Tx, restaurantID uuid.UUID) ([]hoursWindow, error) {
	rows, err := tx.Query(ctx, `
		SELECT day_of_week, EXTRACT(EPOCH FROM opens_at)::int, EXTRACT(EPOCH FROM closes_at)::int, is_closed
		FROM food.restaurant_operating_hours
		WHERE restaurant_id = $1
	`, restaurantID)
	if err != nil {
		return nil, fmt.Errorf("load operating hours: %w", err)
	}
	defer rows.Close()
	var out []hoursWindow
	for rows.Next() {
		var w hoursWindow
		if err := rows.Scan(&w.Day, &w.Opens, &w.Closes, &w.Closed); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func loadServiceAreasTx(ctx context.Context, tx pgx.Tx, restaurantID uuid.UUID) ([]serviceArea, error) {
	rows, err := tx.Query(ctx, `
		SELECT center_latitude::float8, center_longitude::float8, radius_km::float8
		FROM food.restaurant_service_areas
		WHERE restaurant_id = $1 AND is_active = TRUE
	`, restaurantID)
	if err != nil {
		return nil, fmt.Errorf("load service areas: %w", err)
	}
	defer rows.Close()
	var out []serviceArea
	for rows.Next() {
		var a serviceArea
		if err := rows.Scan(&a.CenterLat, &a.CenterLng, &a.RadiusKM); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

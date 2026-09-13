package routing

import (
	"fmt"
	"strconv"
	"time"
)

// Config is the routing environment.
//
//   - GOOGLE_MAPS_SERVER_KEY: optional. Set, ETAs come from the Routes API
//     (TWO_WHEELER, TRAFFIC_AWARE) with haversine as the fallback; empty,
//     haversine only. A server key: restrict it to the Routes API and the
//     service's egress IPs, never ship it to a client.
//   - FOOD_ROUTING_TIMEOUT_MS: optional, default 2000. One computeRoutes call
//     end to end; a positive integer no larger than 10000.
//
// The haversine side is tuned by FOOD_AVG_RIDER_SPEED_KMH (default 20) and
// FOOD_ROUTE_WINDING_FACTOR (default 1.0), both read with the ordering config
// (store/postgres.OrderingConfigFromEnv) because PlaceOrder uses them too.
type Config struct {
	GoogleKey string
	Timeout   time.Duration
}

// maxTimeout keeps a typo from parking a rider ping for a minute.
const maxTimeout = 10 * time.Second

// ConfigFromEnv reads the routing environment. An invalid timeout is an error
// so a typo stops startup rather than silently disabling the bound.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := Config{GoogleKey: getenv("GOOGLE_MAPS_SERVER_KEY"), Timeout: DefaultTimeout}
	if raw := getenv("FOOD_ROUTING_TIMEOUT_MS"); raw != "" {
		ms, err := strconv.Atoi(raw)
		if err != nil || ms <= 0 || time.Duration(ms)*time.Millisecond > maxTimeout {
			return cfg, fmt.Errorf("FOOD_ROUTING_TIMEOUT_MS must be a whole number of milliseconds between 1 and %d", maxTimeout.Milliseconds())
		}
		cfg.Timeout = time.Duration(ms) * time.Millisecond
	}
	return cfg, nil
}

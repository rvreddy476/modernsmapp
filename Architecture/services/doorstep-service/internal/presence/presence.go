// Package presence mirrors on-duty professionals' latest location fix into
// Redis GEO, one sorted set per city (rider-service's GoOnline/UpdateLocation
// pattern): doorstep:pros:geo:<CITY>, member = professional id. Postgres
// (professionals.last_point / last_fix_at) stays the record and the stale-fix
// worker takes a silent professional off duty there; the set expires on its
// own when a city goes quiet. A Redis error is returned for the caller to
// log; it never blocks a duty change or a fix.
package presence

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	keyPrefix = "doorstep:pros:geo:"
	// setTTL bounds a city's set: refreshed on every fix, so it only lapses
	// when no professional in the city has sent one for this long.
	setTTL    = 20 * time.Minute
	opTimeout = 250 * time.Millisecond
)

// Key is a city's GEO set.
func Key(city string) string { return keyPrefix + city }

// Redis is presence over a go-redis client.
type Redis struct{ rdb *redis.Client }

// New wraps a client (nil gives nil: presence off).
func New(rdb *redis.Client) *Redis {
	if rdb == nil {
		return nil
	}
	return &Redis{rdb: rdb}
}

// Upsert places the professional at the fix.
func (r *Redis) Upsert(ctx context.Context, city string, proID uuid.UUID, lat, lng float64) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	key := Key(city)
	if err := r.rdb.GeoAdd(ctx, key, &redis.GeoLocation{Name: proID.String(), Latitude: lat, Longitude: lng}).Err(); err != nil {
		return err
	}
	return r.rdb.Expire(ctx, key, setTTL).Err()
}

// Remove takes the professional off the city's map.
func (r *Redis) Remove(ctx context.Context, city string, proID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if err := r.rdb.ZRem(ctx, Key(city), proID.String()).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

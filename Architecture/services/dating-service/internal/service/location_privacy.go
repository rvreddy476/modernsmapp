// Lane D7 — location precision and privacy (service side): the configurable
// limits, the last-active bucket, and the helpers the profile and deck paths
// share. Snapping, the distance buckets and the limits' enforcement live in
// store/geo.go and store/location.go.
package service

import (
	"time"

	"github.com/atpost/dating-service/internal/store"
)

// LocationPrivacyConfig holds the lane D7 limits (DATING_LOCATION_*,
// DATING_EXPLAIN_DAILY_LIMIT; see http.ResolveLocationPrivacyConfig).
type LocationPrivacyConfig struct {
	// LocationChangeMinInterval: at most one location change per interval.
	LocationChangeMinInterval time.Duration
	// LocationChangesPerDay: at most this many location changes in 24 hours.
	LocationChangesPerDay int
	// ExplainDailyLimit: explain requests per viewer in 24 hours.
	ExplainDailyLimit int
}

// DefaultLocationPrivacyConfig: 1 location change per 15 minutes, 10 per
// day, 60 explain requests per day.
func DefaultLocationPrivacyConfig() LocationPrivacyConfig {
	l := store.DefaultLocationChangeLimits()
	return LocationPrivacyConfig{
		LocationChangeMinInterval: l.MinInterval,
		LocationChangesPerDay:     l.MaxPerDay,
		ExplainDailyLimit:         store.DefaultExplainDailyLimit,
	}
}

// SetLocationPrivacyConfig applies the limits (the store enforces them).
func (s *Service) SetLocationPrivacyConfig(cfg LocationPrivacyConfig) {
	s.store.SetLocationChangeLimits(store.LocationChangeLimits{
		MinInterval: cfg.LocationChangeMinInterval,
		MaxPerDay:   cfg.LocationChangesPerDay,
	})
	s.store.SetExplainDailyLimit(cfg.ExplainDailyLimit)
}

// LocationPrivacyConfig returns the limits in force.
func (s *Service) LocationPrivacyConfig() LocationPrivacyConfig {
	l := s.store.LocationLimits()
	return LocationPrivacyConfig{
		LocationChangeMinInterval: l.MinInterval,
		LocationChangesPerDay:     l.MaxPerDay,
		ExplainDailyLimit:         s.store.ExplainDailyLimit(),
	}
}

// Store sentinels, re-exported for handlers and tests.
var (
	ErrInvalidLocation           = store.ErrInvalidLocation
	ErrLocationChangeRateLimited = store.ErrLocationChangeRateLimited
	ErrExplainRateLimited        = store.ErrExplainRateLimited
)

// Last-active bucket codes. A card carries one of these (with its label) only
// when the candidate does not hide last active; never a timestamp.
const (
	LastActiveToday     = "today"
	LastActiveThisWeek  = "this_week"
	LastActiveAWhileAgo = "a_while_ago"
)

// LastActiveBand is a last-active bucket code and its display label.
type LastActiveBand struct {
	Code  string
	Label string
}

// LastActiveBucketFor buckets a last-active time: under 24 hours ago is
// today, under 7 days this_week, anything older (or unknown) a_while_ago. A
// time in the future (clock skew) counts as today.
func LastActiveBucketFor(lastActive, now time.Time) LastActiveBand {
	if lastActive.IsZero() {
		return LastActiveBand{Code: LastActiveAWhileAgo, Label: "Active a while ago"}
	}
	switch age := now.Sub(lastActive); {
	case age < 24*time.Hour:
		return LastActiveBand{Code: LastActiveToday, Label: "Active today"}
	case age < 7*24*time.Hour:
		return LastActiveBand{Code: LastActiveThisWeek, Label: "Active this week"}
	default:
		return LastActiveBand{Code: LastActiveAWhileAgo, Label: "Active a while ago"}
	}
}

// locationMoved reports whether the stored (snapped) point differs between
// two reads of a profile. before may be nil (a new profile).
func locationMoved(before, after *store.Profile) bool {
	if after == nil {
		return false
	}
	var lat, lng *float64
	if before != nil {
		lat, lng = before.Latitude, before.Longitude
	}
	return !sameCoordinate(lat, after.Latitude) || !sameCoordinate(lng, after.Longitude)
}

func sameCoordinate(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

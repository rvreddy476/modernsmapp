// Travel store — mechanic M8.
//
// dating_travel holds one row per user: the destination city (its public
// centre, snapped like every stored point) and the window. While a trip is
// active — inside its window, not ended, the traveller holding an unexpired
// pass, and the mechanic on — the traveller's EFFECTIVE location everywhere
// in discovery is the destination centre: in their own deck, and in other
// people's decks and person cards, which show the destination city and a
// travelling marker. Their real location is never what another user sees.
//
// Ending a trip stamps ended_at; nothing is deleted.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SetTravelEnabled switches the effect of trips on or off (the mechanic
// flag). Off, every query reads home locations and nobody is travelling.
func (s *Store) SetTravelEnabled(on bool) { s.travelEnabled = on }

// activeTravelWhere is the active-trip condition for the trip row aliased
// tr belonging to the profile column userCol.
func activeTravelWhere(userCol string) string {
	return `tr.user_id = ` + userCol + `
          AND tr.ended_at IS NULL AND tr.starts_at <= now() AND tr.ends_at > now()
          AND EXISTS (SELECT 1 FROM dating_premium_subscriptions ps
                      WHERE ps.user_id = tr.user_id AND ps.expires_at > now())`
}

// effectiveCol is the travel-aware value of a profile column: the active
// trip's column when there is one, else the profile's own.
func (s *Store) effectiveCol(alias, profileCol, travelCol string) string {
	if !s.travelEnabled {
		return alias + "." + profileCol
	}
	return `COALESCE((SELECT tr.` + travelCol + ` FROM dating_travel tr WHERE ` + activeTravelWhere(alias+".user_id") + `), ` + alias + `.` + profileCol + `)`
}

// travellingCol is true while the profile aliased alias has an active trip.
func (s *Store) travellingCol(alias string) string {
	if !s.travelEnabled {
		return "false"
	}
	return `EXISTS (SELECT 1 FROM dating_travel tr WHERE ` + activeTravelWhere(alias+".user_id") + `)`
}

// Trip is a user's trip row.
type Trip struct {
	CityCode  string
	CityLabel string
	Latitude  float64
	Longitude float64
	StartsAt  time.Time
	EndsAt    time.Time
	EndedAt   *time.Time
}

// ActiveTrip returns the user's active trip (nil when none, or when travel
// is off), applying the same rule as the discovery queries.
func (s *Store) ActiveTrip(ctx context.Context, userID uuid.UUID) (*Trip, error) {
	if !s.travelEnabled {
		return nil, nil
	}
	var t Trip
	err := s.db.QueryRow(ctx, `
        SELECT tr.city_code, tr.city_label, tr.latitude, tr.longitude, tr.starts_at, tr.ends_at, tr.ended_at
        FROM dating_travel tr WHERE `+activeTravelWhere("$1::uuid"), userID).Scan(
		&t.CityCode, &t.CityLabel, &t.Latitude, &t.Longitude, &t.StartsAt, &t.EndsAt, &t.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("active trip: %w", err)
	}
	return &t, nil
}

// StartTrip starts (or replaces) the user's trip: the destination is stored
// snapped to the location grid, for `days` from now.
func (s *Store) StartTrip(ctx context.Context, userID uuid.UUID, code, label string, lat, lng float64, days int) error {
	slat, slng := SnapCoordinate(lat), SnapCoordinate(lng)
	_, err := s.db.Exec(ctx, `
        INSERT INTO dating_travel (user_id, city_code, city_label, latitude, longitude, geohash, starts_at, ends_at)
        VALUES ($1, $2, $3, $4, $5, $6, now(), now() + make_interval(days => $7))
        ON CONFLICT (user_id) DO UPDATE
        SET city_code = EXCLUDED.city_code, city_label = EXCLUDED.city_label,
            latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude, geohash = EXCLUDED.geohash,
            starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at, ended_at = NULL, updated_at = now()`,
		userID, code, label, slat, slng, EncodeGeohash(slat, slng, 7), days)
	if err != nil {
		return fmt.Errorf("start trip: %w", err)
	}
	return nil
}

// EndTrip ends the user's trip, if any.
func (s *Store) EndTrip(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
        UPDATE dating_travel SET ended_at = now(), updated_at = now()
        WHERE user_id = $1 AND ended_at IS NULL`, userID)
	if err != nil {
		return fmt.Errorf("end trip: %w", err)
	}
	return nil
}

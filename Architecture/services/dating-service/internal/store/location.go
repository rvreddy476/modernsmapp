// Lane D7 — location precision and privacy (store side).
//
// A dating location is stored only as the point snapped to the 0.01 degree
// grid (SnapCoordinate in geo.go), with location_geohash derived from that
// point. UpsertProfile writes a change through setLocationTx, which applies
// the per-user change limits against dating_location_changes. The explain
// endpoint's daily allowance is ConsumeExplainQuota (dating_explain_ledger).
package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrInvalidLocation refuses a location that is not a latitude and longitude
// sent together, within [-90,90] / [-180,180], finite, and off null island
// (a point that snaps to 0,0).
var ErrInvalidLocation = errors.New("invalid: latitude and longitude must be sent together, within range, and not 0,0")

// ErrLocationChangeRateLimited is matched (errors.Is) by every
// *LocationRateLimitError.
var ErrLocationChangeRateLimited = errors.New("rate_limited: location change limit reached")

// LocationChangeWindow is the rolling window MaxPerDay counts over.
const LocationChangeWindow = 24 * time.Hour

// LocationChangeLimits bounds how often one user's stored location may move.
// A zero field disables that check.
type LocationChangeLimits struct {
	// MinInterval: no change within this long after the previous one.
	MinInterval time.Duration
	// MaxPerDay: at most this many changes in LocationChangeWindow.
	MaxPerDay int
}

// DefaultLocationChangeLimits: 1 per 15 minutes, 10 per 24 hours.
func DefaultLocationChangeLimits() LocationChangeLimits {
	return LocationChangeLimits{MinInterval: 15 * time.Minute, MaxPerDay: 10}
}

// LocationRateLimitError is a refused location change, carrying the limits in
// force so the handler can state them.
type LocationRateLimitError struct {
	Limits LocationChangeLimits
}

func (e *LocationRateLimitError) Error() string { return ErrLocationChangeRateLimited.Error() }

// Is makes errors.Is(err, ErrLocationChangeRateLimited) true.
func (e *LocationRateLimitError) Is(target error) bool { return target == ErrLocationChangeRateLimited }

// SetLocationChangeLimits overrides DefaultLocationChangeLimits (main, from
// DATING_LOCATION_*).
func (s *Store) SetLocationChangeLimits(l LocationChangeLimits) {
	s.locationLimits = l
}

// LocationLimits returns the location change limits in force.
func (s *Store) LocationLimits() LocationChangeLimits {
	return s.locationLimits
}

// ValidateLocation checks a client location and returns it snapped to the
// grid. Both coordinates are required together.
func ValidateLocation(lat, lng *float64) (float64, float64, error) {
	if lat == nil || lng == nil {
		return 0, 0, ErrInvalidLocation
	}
	la, lo := *lat, *lng
	if math.IsNaN(la) || math.IsNaN(lo) || math.IsInf(la, 0) || math.IsInf(lo, 0) ||
		la < -90 || la > 90 || lo < -180 || lo > 180 {
		return 0, 0, ErrInvalidLocation
	}
	sLat, sLng := SnapCoordinate(la), SnapCoordinate(lo)
	if sLat == 0 && sLng == 0 {
		return 0, 0, ErrInvalidLocation
	}
	return sLat, sLng, nil
}

// setLocationTx stores the already-snapped point (lat, lng) and its geohash
// on userID's profile inside tx, holding the row lock so one user's changes
// serialise. The same grid cell as the stored point is a no-op that uses no
// allowance. Any other change — the first set included — is refused with a
// *LocationRateLimitError when a change was recorded within MinInterval or
// MaxPerDay changes were recorded in LocationChangeWindow; otherwise it is
// written and recorded. Reports whether the stored point changed. Both limit
// windows use the database clock, the clock that stamps changed_at.
func (s *Store) setLocationTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, lat, lng float64) (bool, error) {
	var curLat, curLng *float64
	if err := tx.QueryRow(ctx, `
        SELECT latitude, longitude FROM dating_profiles
        WHERE user_id = $1
        FOR UPDATE`, userID).Scan(&curLat, &curLng); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrProfileNotFound
		}
		return false, fmt.Errorf("lock profile location: %w", err)
	}
	if curLat != nil && curLng != nil && SnapCoordinate(*curLat) == lat && SnapCoordinate(*curLng) == lng {
		return false, nil
	}

	limits := s.locationLimits
	if _, err := tx.Exec(ctx, `
        DELETE FROM dating_location_changes
        WHERE user_id = $1 AND changed_at <= now() - make_interval(secs => $2)`,
		userID, LocationChangeWindow.Seconds()); err != nil {
		return false, fmt.Errorf("trim location changes: %w", err)
	}
	var recent, daily int
	if err := tx.QueryRow(ctx, `
        SELECT COUNT(*) FILTER (WHERE changed_at > now() - make_interval(secs => $2))::int,
               COUNT(*)::int
        FROM dating_location_changes
        WHERE user_id = $1 AND changed_at > now() - make_interval(secs => $3)`,
		userID, limits.MinInterval.Seconds(), LocationChangeWindow.Seconds()).Scan(&recent, &daily); err != nil {
		return false, fmt.Errorf("count location changes: %w", err)
	}
	if (limits.MinInterval > 0 && recent > 0) || (limits.MaxPerDay > 0 && daily >= limits.MaxPerDay) {
		return false, &LocationRateLimitError{Limits: limits}
	}

	if _, err := tx.Exec(ctx, `
        UPDATE dating_profiles
        SET latitude = $2, longitude = $3, location_geohash = $4, updated_at = now()
        WHERE user_id = $1`,
		userID, lat, lng, EncodeGeohash(lat, lng, LocationGeohashPrecision)); err != nil {
		return false, fmt.Errorf("update profile location: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO dating_location_changes (user_id) VALUES ($1)`, userID); err != nil {
		return false, fmt.Errorf("record location change: %w", err)
	}
	return true, nil
}

// --- explain allowance -------------------------------------------------------

// ErrExplainRateLimited is matched (errors.Is) by every *ExplainRateLimitError.
var ErrExplainRateLimited = errors.New("rate_limited: explain limit reached")

// ExplainQuotaWindow is the rolling window the explain limit counts over.
const ExplainQuotaWindow = 24 * time.Hour

// DefaultExplainDailyLimit is how many explain requests one viewer may make
// in ExplainQuotaWindow.
const DefaultExplainDailyLimit = 60

// ExplainRateLimitError is a refused explain request, carrying the limit.
type ExplainRateLimitError struct {
	Limit int
}

func (e *ExplainRateLimitError) Error() string { return ErrExplainRateLimited.Error() }

// Is makes errors.Is(err, ErrExplainRateLimited) true.
func (e *ExplainRateLimitError) Is(target error) bool { return target == ErrExplainRateLimited }

// SetExplainDailyLimit overrides DefaultExplainDailyLimit (main, from
// DATING_EXPLAIN_DAILY_LIMIT). 0 disables the limit.
func (s *Store) SetExplainDailyLimit(n int) {
	s.explainDailyLimit = n
}

// ExplainDailyLimit returns the explain limit in force.
func (s *Store) ExplainDailyLimit() int {
	return s.explainDailyLimit
}

// ConsumeExplainQuota records one explain request by viewerID, or refuses it
// with an *ExplainRateLimitError when `limit` requests were already recorded
// in ExplainQuotaWindow. A per-viewer advisory lock serialises the
// count-then-insert. limit <= 0 disables the check.
func (s *Store) ConsumeExplainQuota(ctx context.Context, viewerID uuid.UUID, limit int) error {
	if limit <= 0 {
		return nil
	}
	if viewerID == uuid.Nil {
		return fmt.Errorf("invalid: viewer_id required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin explain quota: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 7041))`, viewerID.String()); err != nil {
		return fmt.Errorf("lock explain quota: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        DELETE FROM dating_explain_ledger
        WHERE viewer_id = $1 AND requested_at <= now() - make_interval(secs => $2)`,
		viewerID, ExplainQuotaWindow.Seconds()); err != nil {
		return fmt.Errorf("trim explain ledger: %w", err)
	}
	var used int
	if err := tx.QueryRow(ctx, `
        SELECT COUNT(*)::int FROM dating_explain_ledger
        WHERE viewer_id = $1 AND requested_at > now() - make_interval(secs => $2)`,
		viewerID, ExplainQuotaWindow.Seconds()).Scan(&used); err != nil {
		return fmt.Errorf("count explain ledger: %w", err)
	}
	if used >= limit {
		return &ExplainRateLimitError{Limit: limit}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO dating_explain_ledger (viewer_id) VALUES ($1)`, viewerID); err != nil {
		return fmt.Errorf("record explain request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit explain quota: %w", err)
	}
	return nil
}

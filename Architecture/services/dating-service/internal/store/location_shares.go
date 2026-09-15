// Live location shares (Dating plan lane D8).
//
// A share sends the sharer's exact point to ONE recipient the sharer chose —
// a trusted contact or a current match — for a bounded time. Exact rather
// than the D7 grid: the point exists so that person can find the sharer, and
// a 1 km cell defeats that. The exposure is bounded instead: only the
// recipient can read it, only until expires_at, never after the sharer stops
// (the point is cleared), and never while the pair is blocked either way.
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

// Share recipient kinds.
const (
	ShareRecipientTrustedContact = "trusted_contact"
	ShareRecipientMatch          = "match"
)

// ErrLocationShareNotFound is returned when a share is missing, not the
// caller's, expired or stopped. One answer for all of them.
var ErrLocationShareNotFound = errors.New("not_found: location share not found")

// LocationShare is one row of dating_location_shares.
type LocationShare struct {
	ShareID       uuid.UUID  `json:"share_id"`
	UserID        uuid.UUID  `json:"user_id"`
	RecipientID   uuid.UUID  `json:"recipient_id"`
	RecipientKind string     `json:"recipient_kind"`
	Latitude      *float64   `json:"latitude,omitempty"`
	Longitude     *float64   `json:"longitude,omitempty"`
	ExpiresAt     time.Time  `json:"expires_at"`
	StoppedAt     *time.Time `json:"stopped_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

const locationShareCols = `id, user_id, recipient_id, recipient_kind, latitude, longitude,
    expires_at, stopped_at, created_at`

func scanLocationShare(row pgx.Row) (*LocationShare, error) {
	l := &LocationShare{}
	if err := row.Scan(&l.ShareID, &l.UserID, &l.RecipientID, &l.RecipientKind, &l.Latitude, &l.Longitude,
		&l.ExpiresAt, &l.StoppedAt, &l.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrLocationShareNotFound
		}
		return nil, fmt.Errorf("scan location share: %w", err)
	}
	return l, nil
}

// ValidExactLocation reports whether lat/lng is a usable exact point: both
// set, finite, in range and not 0,0.
func ValidExactLocation(lat, lng *float64) bool {
	if lat == nil || lng == nil {
		return false
	}
	la, lo := *lat, *lng
	if math.IsNaN(la) || math.IsNaN(lo) || math.IsInf(la, 0) || math.IsInf(lo, 0) {
		return false
	}
	if la < -90 || la > 90 || lo < -180 || lo > 180 {
		return false
	}
	return !(la == 0 && lo == 0)
}

// CreateLocationShare stores a share expiring ttl from now (database clock).
func (s *Store) CreateLocationShare(ctx context.Context, userID, recipientID uuid.UUID, kind string, lat, lng float64, ttl time.Duration) (*LocationShare, error) {
	if userID == uuid.Nil || recipientID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user and recipient ids required")
	}
	if userID == recipientID {
		return nil, fmt.Errorf("invalid: cannot share your location with yourself")
	}
	if kind != ShareRecipientTrustedContact && kind != ShareRecipientMatch {
		return nil, fmt.Errorf("invalid: recipient kind %q", kind)
	}
	if !ValidExactLocation(&lat, &lng) {
		return nil, ErrInvalidLocation
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("invalid: share duration must be positive")
	}
	share, err := scanLocationShare(s.db.QueryRow(ctx, `
        INSERT INTO dating_location_shares (user_id, recipient_id, recipient_kind, latitude, longitude, expires_at)
        VALUES ($1, $2, $3, $4, $5, now() + make_interval(secs => $6))
        RETURNING `+locationShareCols, userID, recipientID, kind, lat, lng, ttl.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("create location share: %w", err)
	}
	// Bookkeeping row for the user's safety history; no coordinates.
	if err := s.RecordSafetyEvent(ctx, userID, "location_share_created", map[string]any{
		"share_id":       share.ShareID.String(),
		"recipient_id":   recipientID.String(),
		"recipient_kind": kind,
		"expires_at":     share.ExpiresAt.UTC(),
	}); err != nil {
		return nil, err
	}
	return share, nil
}

// GetLocationShareForRecipient returns the share with its point only to its
// recipient, only while unexpired and not stopped, and only while the pair
// is not blocked either way.
func (s *Store) GetLocationShareForRecipient(ctx context.Context, shareID, recipientID uuid.UUID) (*LocationShare, error) {
	if shareID == uuid.Nil || recipientID == uuid.Nil {
		return nil, ErrLocationShareNotFound
	}
	return scanLocationShare(s.db.QueryRow(ctx, `
        SELECT `+locationShareCols+`
        FROM dating_location_shares ls
        WHERE ls.id = $1 AND ls.recipient_id = $2
          AND ls.stopped_at IS NULL
          AND ls.expires_at > now()
          AND ls.latitude IS NOT NULL AND ls.longitude IS NOT NULL
          AND NOT `+blockedPairPredicate("ls.user_id", "ls.recipient_id"), shareID, recipientID))
}

// StopLocationShare stops the sharer's own share and clears its point.
// Idempotent for the sharer; anyone else gets ErrLocationShareNotFound.
func (s *Store) StopLocationShare(ctx context.Context, shareID, userID uuid.UUID) (*LocationShare, error) {
	if shareID == uuid.Nil || userID == uuid.Nil {
		return nil, ErrLocationShareNotFound
	}
	return scanLocationShare(s.db.QueryRow(ctx, `
        UPDATE dating_location_shares
        SET stopped_at = COALESCE(stopped_at, now()), latitude = NULL, longitude = NULL
        WHERE id = $1 AND user_id = $2
        RETURNING `+locationShareCols, shareID, userID))
}

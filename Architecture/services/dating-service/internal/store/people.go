// People — the compact person row behind a match, an incoming spark and the
// person card (lane D10).
//
// It carries only what a card renders: the first name, the birth date (for
// age), the person's own description and languages, the approved primary
// photo's id and visibility, the photo-privacy flags and the trust tier.
// Nothing sensitive: no religion, no community, no exact coordinates on the
// wire, no last_active_at, no sealed field.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PersonRow is the compact projection used to build a person card.
type PersonRow struct {
	UserID    uuid.UUID
	FirstName *string
	BirthDate *time.Time
	TrustTier string
	// Bio is the person's own description, and LanguagePrefs the languages
	// they listed. Both are profile text the owner wrote for other people
	// to read, so they are shown before a match.
	Bio           string
	LanguagePrefs []string
	// PrimaryPhotoID / PrimaryPhotoVisibility name the approved primary
	// photo; never the media id.
	PrimaryPhotoID         *uuid.UUID
	PrimaryPhotoVisibility string
	// SparkedViewer is true when this person has sparked the viewer
	// (the sparked_only photo audience).
	SparkedViewer bool
	// BlurPhotosUntilMatch / BlurMode are the owner's photo-privacy flags.
	BlurPhotosUntilMatch bool
	BlurMode             bool
	// Latitude / Longitude are the profile's snapped point, used only to
	// compute a distance bucket. Never serialised by the service layer.
	Latitude  *float64
	Longitude *float64
}

// Age returns whole years, or 0 when no birth date is known.
func (p *PersonRow) Age() int {
	if p == nil || p.BirthDate == nil {
		return 0
	}
	return AgeOn(*p.BirthDate, time.Now())
}

const personSelectCols = `
    p.user_id, p.first_name, p.birth_date, p.trust_tier, p.bio, p.language_prefs,
    (SELECT ph.id FROM dating_photos ph
        WHERE ph.user_id = p.user_id AND ph.is_primary = true
          AND ph.moderation_status = 'approved' LIMIT 1) AS primary_photo_id,
    COALESCE((SELECT ph.visibility FROM dating_photos ph
        WHERE ph.user_id = p.user_id AND ph.is_primary = true
          AND ph.moderation_status = 'approved' LIMIT 1), 'public') AS primary_photo_visibility,
    EXISTS (SELECT 1 FROM dating_sparks sv
        WHERE sv.from_user_id = p.user_id AND sv.to_user_id = $1::uuid) AS sparked_viewer,
    p.blur_photos_until_match, p.blur_mode, p.latitude, p.longitude`

func scanPersonRow(row pgx.Row) (*PersonRow, error) {
	p := &PersonRow{}
	if err := row.Scan(&p.UserID, &p.FirstName, &p.BirthDate, &p.TrustTier,
		&p.Bio, &p.LanguagePrefs,
		&p.PrimaryPhotoID, &p.PrimaryPhotoVisibility, &p.SparkedViewer,
		&p.BlurPhotosUntilMatch, &p.BlurMode, &p.Latitude, &p.Longitude); err != nil {
		return nil, err
	}
	return p, nil
}

// personVisibleWhere is the one visibility rule for a person card: the
// profile exists, is neither soft-deleted nor suspended nor deleted, and the
// pair is not blocked either way. Incognito is NOT applied here: a card is
// only ever built for someone the viewer already has a relationship with
// (match, incoming spark, or their own deck), and the deck query applies the
// incognito rule itself.
const personVisibleWhere = `
          p.deleted_at IS NULL
          AND p.profile_status NOT IN ('suspended','deleted')
          AND NOT ` + `EXISTS (SELECT 1 FROM dating_blocks blk
        WHERE (blk.user_id = $1::uuid AND blk.blocked_id = p.user_id)
           OR (blk.user_id = p.user_id AND blk.blocked_id = $1::uuid))`

// GetPersonForViewer returns one compact person as the viewer may see it, or
// ErrProfileNotFound when the profile is hidden from them.
func (s *Store) GetPersonForViewer(ctx context.Context, viewerID, userID uuid.UUID) (*PersonRow, error) {
	if viewerID == uuid.Nil || userID == uuid.Nil {
		return nil, ErrProfileNotFound
	}
	p, err := scanPersonRow(s.db.QueryRow(ctx, `
        SELECT `+personSelectCols+`
        FROM dating_profiles p
        WHERE p.user_id = $2 AND `+personVisibleWhere, viewerID, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProfileNotFound
		}
		return nil, fmt.Errorf("get person: %w", err)
	}
	return p, nil
}

// ListPeopleForViewer returns the compact people among ids the viewer may
// see, keyed by user id. Ids that are blocked, deleted or suspended are
// simply absent.
func (s *Store) ListPeopleForViewer(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*PersonRow, error) {
	out := make(map[uuid.UUID]*PersonRow, len(ids))
	if viewerID == uuid.Nil || len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
        SELECT `+personSelectCols+`
        FROM dating_profiles p
        WHERE p.user_id = ANY($2::uuid[]) AND `+personVisibleWhere, viewerID, ids)
	if err != nil {
		return nil, fmt.Errorf("list people: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanPersonRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan person: %w", err)
		}
		out[p.UserID] = p
	}
	return out, rows.Err()
}

// ListBlockedPeople returns the users the caller has blocked, newest first,
// with the name and birth date needed for a no-photo card. A block survives
// the other side's deletion, so no visibility filter applies here.
type BlockedPerson struct {
	UserID    uuid.UUID  `json:"user_id"`
	FirstName *string    `json:"-"`
	BirthDate *time.Time `json:"-"`
	BlockedAt time.Time  `json:"blocked_at"`
}

// ListBlockedPeople lists the caller's own blocks (max 500).
func (s *Store) ListBlockedPeople(ctx context.Context, userID uuid.UUID) ([]*BlockedPerson, error) {
	rows, err := s.db.Query(ctx, `
        SELECT b.blocked_id, p.first_name, p.birth_date, b.created_at
        FROM dating_blocks b
        LEFT JOIN dating_profiles p ON p.user_id = b.blocked_id
        WHERE b.user_id = $1
        ORDER BY b.created_at DESC
        LIMIT 500`, userID)
	if err != nil {
		return nil, fmt.Errorf("list blocks: %w", err)
	}
	defer rows.Close()
	out := make([]*BlockedPerson, 0, 16)
	for rows.Next() {
		b := &BlockedPerson{}
		if err := rows.Scan(&b.UserID, &b.FirstName, &b.BirthDate, &b.BlockedAt); err != nil {
			return nil, fmt.Errorf("scan block: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// UnblockUser removes the caller's block on targetUserID. Idempotent: an
// absent block is not an error. Nothing the block severed is restored — the
// closed match stays closed and the deleted sparks stay deleted.
func (s *Store) UnblockUser(ctx context.Context, userID, targetUserID uuid.UUID) (bool, error) {
	if userID == uuid.Nil || targetUserID == uuid.Nil {
		return false, fmt.Errorf("invalid: user ids required")
	}
	tag, err := s.db.Exec(ctx, `DELETE FROM dating_blocks WHERE user_id = $1 AND blocked_id = $2`, userID, targetUserID)
	if err != nil {
		return false, fmt.Errorf("unblock user: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// HasIncomingSparkFrom reports whether senderID has a live (not declined)
// spark aimed at viewerID that the viewer may see.
func (s *Store) HasIncomingSparkFrom(ctx context.Context, viewerID, senderID uuid.UUID) (bool, error) {
	if viewerID == uuid.Nil || senderID == uuid.Nil {
		return false, nil
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM dating_sparks sp
            WHERE sp.to_user_id = $1 AND sp.from_user_id = $2
              AND sp.declined_at IS NULL
              AND NOT `+blockedPairPredicate("sp.from_user_id", "sp.to_user_id")+`
              AND `+visibleProfilePredicate("sp.from_user_id")+`)`, viewerID, senderID).Scan(&exists); err != nil {
		return false, fmt.Errorf("has incoming spark: %w", err)
	}
	return exists, nil
}

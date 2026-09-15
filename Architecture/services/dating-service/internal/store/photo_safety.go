package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Dating plan lane D6 — dating photo safety, store side: attaching under a
// per-profile limit with the automated moderation decision, idempotent
// moderation writes, the media recheck cursor, and the audience facts the
// photo image route decides on.

// Photo moderation statuses.
const (
	PhotoStatusPending       = "pending"
	PhotoStatusPendingReview = "pending_review"
	PhotoStatusApproved      = "approved"
	PhotoStatusRejected      = "rejected"
)

// Who set a photo's moderation status.
const (
	PhotoSourceAuto  = "auto"
	PhotoSourceAdmin = "admin"
)

var (
	// ErrPhotoLimitReached: the profile already holds the maximum number of
	// photos that are not rejected.
	ErrPhotoLimitReached = errors.New("conflict: photo limit reached")
	// ErrPhotoAlreadyAttached: this media is already one of the user's
	// photos (rejected ones included).
	ErrPhotoAlreadyAttached = errors.New("conflict: this media is already attached")
)

const photoSelectCols = `id, user_id, media_id, sort_order, is_primary, visibility,
               moderation_status, moderation_reason, created_at`

// PhotoDecision is one moderation outcome.
type PhotoDecision struct {
	Status string
	// Reason is a stable code (EXPLICIT_CONTENT, NO_FACE...) or a
	// moderator's note. Empty clears it.
	Reason string
	Source string
	// Labels is the scanner labels as a JSON array; nil leaves them as they
	// are on an update.
	Labels    []byte
	FaceCount *int
}

func validPhotoDecision(d PhotoDecision) error {
	switch d.Status {
	case PhotoStatusPending, PhotoStatusPendingReview, PhotoStatusApproved, PhotoStatusRejected:
	default:
		return fmt.Errorf("invalid: moderation status %q", d.Status)
	}
	switch d.Source {
	case PhotoSourceAuto, PhotoSourceAdmin:
	default:
		return fmt.Errorf("invalid: moderation source %q", d.Source)
	}
	return nil
}

func labelsArg(raw []byte) any {
	if raw == nil {
		return nil
	}
	return string(raw)
}

// PhotoSlotUsage returns how many of the user's photos are not rejected and
// whether mediaID is already one of their photos. A cheap pre-check before
// media-service does the expensive work; CreateModeratedPhoto re-checks.
func (s *Store) PhotoSlotUsage(ctx context.Context, userID, mediaID uuid.UUID) (int, bool, error) {
	var active int
	var attached bool
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*) FILTER (WHERE moderation_status <> 'rejected'),
               COALESCE(bool_or(media_id = $2), false)
        FROM dating_photos WHERE user_id = $1`, userID, mediaID).Scan(&active, &attached)
	if err != nil {
		return 0, false, fmt.Errorf("photo slot usage: %w", err)
	}
	return active, attached, nil
}

// CreateModeratedPhoto inserts a photo with its automated moderation decision
// under the per-profile limit. The count, the duplicate check and the insert
// run under a per-user advisory lock, so concurrent attaches cannot exceed the
// limit. A rejected photo is never made primary (the current primary stays).
func (s *Store) CreateModeratedPhoto(ctx context.Context, userID uuid.UUID, p CreatePhotoParams, maxPhotos int, d PhotoDecision) (*Photo, error) {
	if maxPhotos <= 0 {
		return nil, fmt.Errorf("invalid: photo limit must be positive")
	}
	if err := validPhotoDecision(d); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('dating_photos:' || $1::text, 0))`, userID); err != nil {
		return nil, fmt.Errorf("lock user photos: %w", err)
	}
	var active int
	var attached bool
	if err := tx.QueryRow(ctx, `
        SELECT COUNT(*) FILTER (WHERE moderation_status <> 'rejected'),
               COALESCE(bool_or(media_id = $2), false)
        FROM dating_photos WHERE user_id = $1`, userID, p.MediaID).Scan(&active, &attached); err != nil {
		return nil, fmt.Errorf("count photos: %w", err)
	}
	if attached {
		return nil, ErrPhotoAlreadyAttached
	}
	if active >= maxPhotos {
		return nil, ErrPhotoLimitReached
	}

	primary := p.IsPrimary && d.Status != PhotoStatusRejected
	if primary {
		if _, err := tx.Exec(ctx, `UPDATE dating_photos SET is_primary = false WHERE user_id = $1`, userID); err != nil {
			return nil, fmt.Errorf("demote primaries: %w", err)
		}
	}
	visibility := p.Visibility
	if visibility == "" {
		visibility = "public"
	}
	row := tx.QueryRow(ctx, `
        INSERT INTO dating_photos (user_id, media_id, sort_order, is_primary, visibility,
                                   moderation_status, moderation_reason, moderation_source,
                                   moderation_labels, media_checked_at, face_count)
        VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9::jsonb, now(), $10)
        RETURNING `+photoSelectCols,
		userID, p.MediaID, p.SortOrder, primary, visibility,
		d.Status, d.Reason, d.Source, labelsArg(d.Labels), d.FaceCount)
	out, err := scanPhoto(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit photo insert: %w", err)
	}
	return out, nil
}

// SetPhotoModerationDecision writes a moderation decision. changed is false
// when the photo already carries exactly this status and reason (a duplicate
// result): nothing is written and the caller must emit nothing. An automated
// decision also advances the media recheck cursor.
func (s *Store) SetPhotoModerationDecision(ctx context.Context, photoID uuid.UUID, d PhotoDecision) (*Photo, bool, error) {
	if err := validPhotoDecision(d); err != nil {
		return nil, false, err
	}
	row := s.db.QueryRow(ctx, `
        UPDATE dating_photos
        SET moderation_status = $2,
            moderation_reason = NULLIF($3, ''),
            moderation_source = $4,
            moderation_labels = COALESCE($5::jsonb, moderation_labels),
            face_count        = COALESCE($6, face_count),
            media_checked_at  = CASE WHEN $4 = 'auto' THEN now() ELSE media_checked_at END
        WHERE id = $1
          AND (moderation_status IS DISTINCT FROM $2
               OR moderation_reason IS DISTINCT FROM NULLIF($3, ''))
        RETURNING `+photoSelectCols,
		photoID, d.Status, d.Reason, d.Source, labelsArg(d.Labels), d.FaceCount)
	p, err := scanPhoto(row)
	if errors.Is(err, ErrPhotoNotFound) {
		cur, gerr := s.GetPhotoByID(ctx, photoID)
		if gerr != nil {
			return nil, false, gerr
		}
		return cur, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return p, true, nil
}

// GetPhotoByID returns one photo, or ErrPhotoNotFound.
func (s *Store) GetPhotoByID(ctx context.Context, photoID uuid.UUID) (*Photo, error) {
	return scanPhoto(s.db.QueryRow(ctx, `
        SELECT `+photoSelectCols+` FROM dating_photos WHERE id = $1`, photoID))
}

// GetPhotoForUser returns the user's photo, or ErrPhotoNotFound.
func (s *Store) GetPhotoForUser(ctx context.Context, userID, photoID uuid.UUID) (*Photo, error) {
	return scanPhoto(s.db.QueryRow(ctx, `
        SELECT `+photoSelectCols+` FROM dating_photos WHERE id = $1 AND user_id = $2`, photoID, userID))
}

// CountPhotosUsingMedia counts the user's other photos on mediaID.
func (s *Store) CountPhotosUsingMedia(ctx context.Context, userID, mediaID, exceptPhotoID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*) FROM dating_photos
        WHERE user_id = $1 AND media_id = $2 AND id <> $3`, userID, mediaID, exceptPhotoID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count photos using media: %w", err)
	}
	return n, nil
}

// PhotoRecheck is a photo due for a media recheck.
type PhotoRecheck struct {
	Photo
	// ModerationSource is "" on rows written before lane D6.
	ModerationSource string
}

// ListPhotosForMediaRecheck returns photos that are not rejected and whose
// media was last confirmed before olderThan (never-checked rows first).
func (s *Store) ListPhotosForMediaRecheck(ctx context.Context, olderThan time.Time, limit int) ([]PhotoRecheck, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
        SELECT `+photoSelectCols+`, COALESCE(moderation_source, '')
        FROM dating_photos
        WHERE moderation_status IN ('pending', 'pending_review', 'approved')
          AND (media_checked_at IS NULL OR media_checked_at < $1)
        ORDER BY media_checked_at NULLS FIRST, created_at
        LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("list photos for recheck: %w", err)
	}
	defer rows.Close()
	var out []PhotoRecheck
	for rows.Next() {
		var r PhotoRecheck
		if err := rows.Scan(&r.ID, &r.UserID, &r.MediaID, &r.SortOrder, &r.IsPrimary, &r.Visibility,
			&r.ModerationStatus, &r.ModerationReason, &r.CreatedAt, &r.ModerationSource); err != nil {
			return nil, fmt.Errorf("scan photo recheck: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TouchPhotoMediaCheck records that media-service confirmed the photo's media.
func (s *Store) TouchPhotoMediaCheck(ctx context.Context, photoID uuid.UUID) error {
	if _, err := s.db.Exec(ctx, `UPDATE dating_photos SET media_checked_at = now() WHERE id = $1`, photoID); err != nil {
		return fmt.Errorf("touch photo media check: %w", err)
	}
	return nil
}

// PhotoAudienceFacts is what the photo image route decides on.
type PhotoAudienceFacts struct {
	// OwnerVisible: the owner's profile exists, is not deleted or suspended,
	// neither side has blocked the other, and it is not incognito toward the
	// viewer.
	OwnerVisible bool
	// Matched: an open match (matched, conversing, quiet) between the two.
	Matched bool
	// OwnerSparkedViewer: the owner has sparked the viewer (sparked_only).
	OwnerSparkedViewer bool
	// BlurUntilMatch: the owner's blur_photos_until_match (or blur_mode).
	BlurUntilMatch bool
}

// PhotoAudience reads the audience facts for viewer looking at owner.
func (s *Store) PhotoAudience(ctx context.Context, viewerID, ownerID uuid.UUID) (*PhotoAudienceFacts, error) {
	f := &PhotoAudienceFacts{}
	err := s.db.QueryRow(ctx, `
        SELECT
            EXISTS (SELECT 1 FROM dating_profiles p
                    WHERE p.user_id = $2::uuid
                      AND p.deleted_at IS NULL
                      AND p.profile_status NOT IN ('suspended','deleted')
                      AND NOT `+blockedPairPredicate("$1::uuid", "p.user_id")+`
                      AND `+incognitoVisiblePredicate("p", "$1::uuid")+`),
            EXISTS (SELECT 1 FROM dating_matches m
                    WHERE m.user_a = LEAST($1::uuid, $2::uuid)
                      AND m.user_b = GREATEST($1::uuid, $2::uuid)
                      AND m.status IN ('matched','conversing','quiet')),
            EXISTS (SELECT 1 FROM dating_sparks sp
                    WHERE sp.from_user_id = $2::uuid AND sp.to_user_id = $1::uuid),
            COALESCE((SELECT p.blur_photos_until_match OR p.blur_mode
                      FROM dating_profiles p WHERE p.user_id = $2::uuid), false)`,
		viewerID, ownerID).Scan(&f.OwnerVisible, &f.Matched, &f.OwnerSparkedViewer, &f.BlurUntilMatch)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return f, nil
		}
		return nil, fmt.Errorf("photo audience: %w", err)
	}
	return f, nil
}

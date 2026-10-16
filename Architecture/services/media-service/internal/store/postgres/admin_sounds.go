package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Admin sound library (2026-10-16, migration 029). The rules are
// service/admin_sounds.go; this is the audio_library table as the admin
// console sees it: every row (drafts, inactive and withdrawn ones included),
// with the licence columns and the derived status.

// Library sound statuses (AdminLibrarySound.Status).
const (
	LibraryStatusDraft     = "draft"
	LibraryStatusActive    = "active"
	LibraryStatusInactive  = "inactive"
	LibraryStatusWithdrawn = "withdrawn"
)

// Withdraw scopes.
const (
	WithdrawNewUses    = "new_uses"
	WithdrawEverywhere = "everywhere"
)

var (
	// ErrLibrarySoundWithdrawn: a withdrawn sound cannot be enabled again.
	ErrLibrarySoundWithdrawn = errors.New("library sound is withdrawn")
	// ErrLibraryLicenseExpired: a sound whose licence has lapsed cannot be
	// enabled.
	ErrLibraryLicenseExpired = errors.New("library sound licence has expired")
	// ErrLibraryKeyInUse: the uploaded object already backs a library row.
	ErrLibraryKeyInUse = errors.New("upload already backs a library sound")
)

// AdminLibrarySound is one audio_library row for the admin console. The
// storage key (AudioURL) and first activation never leave the service.
type AdminLibrarySound struct {
	ID               uuid.UUID  `json:"id"`
	Title            string     `json:"title"`
	Artist           string     `json:"artist"`
	Genre            *string    `json:"genre"`
	Mood             *string    `json:"mood"`
	DurationMs       int        `json:"duration_ms"`
	LicenseType      *string    `json:"license_type"`
	RightsHolder     *string    `json:"rights_holder"`
	LicenseReference *string    `json:"license_reference"`
	LicenseExpiresAt *time.Time `json:"license_expires_at"`
	Attribution      *string    `json:"attribution"`
	IsActive         bool       `json:"is_active"`
	Status           string     `json:"status"`
	// AudioTrackID is the audio_tracks row the sound was materialised as on
	// its first use, whatever that row's status (a sound withdrawn
	// everywhere keeps its link). Null until the first use.
	AudioTrackID   *uuid.UUID `json:"audio_track_id"`
	UsageCount     int64      `json:"usage_count"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CreatedBy      *uuid.UUID `json:"created_by"`
	WithdrawnAt    *time.Time `json:"withdrawn_at"`
	WithdrawScope  *string    `json:"withdraw_scope"`
	WithdrawReason *string    `json:"withdraw_reason"`

	AudioURL         string     `json:"-"`
	FirstActivatedAt *time.Time `json:"-"`
}

// DeriveStatus is the status the console reads: withdrawn, else active,
// else draft (never activated), else inactive.
func (s *AdminLibrarySound) DeriveStatus() string {
	switch {
	case s.WithdrawnAt != nil:
		return LibraryStatusWithdrawn
	case s.IsActive:
		return LibraryStatusActive
	case s.FirstActivatedAt == nil:
		return LibraryStatusDraft
	default:
		return LibraryStatusInactive
	}
}

// adminSoundUses mirrors audioLibraryUses over the admin join (which keeps
// a withdrawn track).
const adminSoundUses = `GREATEST(l.usage_count, COALESCE(t.usage_count, 0)::BIGINT, COALESCE(t.use_count, 0)::BIGINT)`

const adminSoundCols = `l.id, l.title, l.artist, l.genre, l.mood, l.duration_ms, l.license_type,
    l.rights_holder, l.license_reference, l.license_expires_at, l.attribution, l.is_active,
    t.id, ` + adminSoundUses + `, l.created_at, l.updated_at, l.created_by, l.withdrawn_at,
    l.withdraw_scope, l.withdraw_reason, l.audio_url, l.first_activated_at`

// adminSoundFrom joins each row to its materialised sound, at most one
// (uq_audio_tracks_library), in any status.
const adminSoundFrom = ` FROM audio_library l LEFT JOIN audio_tracks t ON t.library_id = l.id`

func scanAdminSound(row pgx.Row, s *AdminLibrarySound) error {
	if err := row.Scan(
		&s.ID, &s.Title, &s.Artist, &s.Genre, &s.Mood, &s.DurationMs, &s.LicenseType,
		&s.RightsHolder, &s.LicenseReference, &s.LicenseExpiresAt, &s.Attribution, &s.IsActive,
		&s.AudioTrackID, &s.UsageCount, &s.CreatedAt, &s.UpdatedAt, &s.CreatedBy, &s.WithdrawnAt,
		&s.WithdrawScope, &s.WithdrawReason, &s.AudioURL, &s.FirstActivatedAt,
	); err != nil {
		return err
	}
	s.Status = s.DeriveStatus()
	return nil
}

// AdminSoundFilter is the admin list query. Status is all (or ""), active,
// inactive (drafts included), draft or withdrawn. Q matches title or
// artist; Genre is exact, case-insensitive. The cursor is the (created_at,
// id) of the last row of the previous page.
type AdminSoundFilter struct {
	Status  string
	Q       string
	Genre   string
	Limit   int
	AfterAt *time.Time
	AfterID uuid.UUID
}

// ListAdminLibrarySounds returns up to f.Limit rows, newest first.
func (s *MediaAssetStore) ListAdminLibrarySounds(ctx context.Context, f AdminSoundFilter) ([]AdminLibrarySound, error) {
	where := []string{"TRUE"}
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	switch f.Status {
	case "", "all":
	case LibraryStatusActive:
		where = append(where, "l.is_active = TRUE")
	case LibraryStatusInactive:
		where = append(where, "l.is_active = FALSE AND l.withdrawn_at IS NULL")
	case LibraryStatusDraft:
		where = append(where, "l.is_active = FALSE AND l.withdrawn_at IS NULL AND l.first_activated_at IS NULL")
	case LibraryStatusWithdrawn:
		where = append(where, "l.withdrawn_at IS NOT NULL")
	default:
		return nil, fmt.Errorf("unknown status %q", f.Status)
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		p := arg("%" + likeEscape(q) + "%")
		where = append(where, "(l.title ILIKE "+p+" OR l.artist ILIKE "+p+")")
	}
	if g := strings.TrimSpace(f.Genre); g != "" {
		where = append(where, "LOWER(l.genre) = LOWER("+arg(g)+")")
	}
	if f.AfterAt != nil {
		where = append(where, "(l.created_at, l.id) < ("+arg(*f.AfterAt)+", "+arg(f.AfterID)+")")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `SELECT `+adminSoundCols+adminSoundFrom+`
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY l.created_at DESC, l.id DESC
		LIMIT `+arg(limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminLibrarySound{}
	for rows.Next() {
		var a AdminLibrarySound
		if err := scanAdminSound(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAdminLibrarySound returns any library row; (nil, nil) when none.
func (s *MediaAssetStore) GetAdminLibrarySound(ctx context.Context, id uuid.UUID) (*AdminLibrarySound, error) {
	return getAdminSound(ctx, s.db, id, false)
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func getAdminSound(ctx context.Context, q rowQuerier, id uuid.UUID, lock bool) (*AdminLibrarySound, error) {
	sql := `SELECT ` + adminSoundCols + adminSoundFrom + ` WHERE l.id = $1`
	if lock {
		sql += ` FOR UPDATE OF l`
	}
	var a AdminLibrarySound
	err := scanAdminSound(q.QueryRow(ctx, sql, id), &a)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// AdminLibraryKeyInUse reports whether a library row already names key.
func (s *MediaAssetStore) AdminLibraryKeyInUse(ctx context.Context, key string) (bool, error) {
	var used bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM audio_library WHERE audio_url = $1)`, key).Scan(&used)
	return used, err
}

// AdminLibrarySoundInsert is a new row from the admin console. The upload
// has been checked (it exists, is audio, DurationMs is measured).
type AdminLibrarySoundInsert struct {
	StorageKey       string
	Title            string
	Artist           string
	Genre            string
	Mood             *string
	DurationMs       int
	LicenseType      string
	RightsHolder     string
	LicenseReference *string
	LicenseExpiresAt *time.Time
	Attribution      *string
	IsActive         bool
	CreatedBy        uuid.UUID
}

// CreateAdminLibrarySound inserts the row. source and is_licensed are kept
// meaningful for the older readers: an original recording is 'original',
// every other licence 'licensed'; is_licensed marks a licensed one (which
// the materialised sound carries as license_type 'licensed').
// ErrLibraryKeyInUse when the upload already backs a row.
func (s *MediaAssetStore) CreateAdminLibrarySound(ctx context.Context, in AdminLibrarySoundInsert) (*AdminLibrarySound, error) {
	source := "licensed"
	if in.LicenseType == "original" {
		source = "original"
	}
	id := uuid.New()
	_, err := s.db.Exec(ctx, `
		INSERT INTO audio_library (id, title, artist, duration_ms, audio_url, source, is_licensed,
		    genre, mood, is_active, license_type, rights_holder, license_reference, license_expires_at,
		    attribution, created_by, first_activated_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
		    CASE WHEN $10 THEN NOW() END, NOW(), NOW())`,
		id, in.Title, in.Artist, in.DurationMs, in.StorageKey, source, in.LicenseType == "licensed",
		in.Genre, in.Mood, in.IsActive, in.LicenseType, in.RightsHolder, in.LicenseReference,
		in.LicenseExpiresAt, in.Attribution, in.CreatedBy)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return nil, ErrLibraryKeyInUse
		}
		return nil, err
	}
	a, err := s.GetAdminLibrarySound(ctx, id)
	if err == nil && a == nil {
		err = pgx.ErrNoRows
	}
	return a, err
}

// OptString is a PATCH field: Set says it was sent; a nil Value clears it.
type OptString struct {
	Set   bool
	Value *string
}

// OptTime is OptString for a timestamp.
type OptTime struct {
	Set   bool
	Value *time.Time
}

// AdminLibrarySoundPatch is the admin edit. Title, Artist, Genre and
// RightsHolder are never cleared (the service refuses a null).
type AdminLibrarySoundPatch struct {
	Title            OptString
	Artist           OptString
	Genre            OptString
	Mood             OptString
	RightsHolder     OptString
	LicenseReference OptString
	LicenseExpiresAt OptTime
	Attribution      OptString
	IsActive         *bool
}

// PatchAdminLibrarySound applies the edit in one transaction over the
// locked row and returns the row after it; (nil, nil) when there is none.
//
//   - Enabling a withdrawn row is ErrLibrarySoundWithdrawn, and enabling a
//     row whose licence (after this edit) has lapsed ErrLibraryLicenseExpired.
//   - The first enable stamps first_activated_at (the row stops being a
//     draft for good).
//   - A new title, artist or genre follows onto the materialised sound, so
//     posts already using it show the edit.
func (s *MediaAssetStore) PatchAdminLibrarySound(ctx context.Context, id uuid.UUID, p AdminLibrarySoundPatch, now time.Time) (*AdminLibrarySound, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cur, err := getAdminSound(ctx, tx, id, true)
	if err != nil || cur == nil {
		return nil, err
	}
	expires := cur.LicenseExpiresAt
	if p.LicenseExpiresAt.Set {
		expires = p.LicenseExpiresAt.Value
	}
	if p.IsActive != nil && *p.IsActive {
		if cur.WithdrawnAt != nil {
			return nil, ErrLibrarySoundWithdrawn
		}
		if expires != nil && !expires.After(now) {
			return nil, ErrLibraryLicenseExpired
		}
	}

	sets := []string{"updated_at = NOW()"}
	args := []any{id}
	set := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, col+" = $"+strconv.Itoa(len(args)))
	}
	for _, f := range []struct {
		col string
		v   OptString
	}{
		{"title", p.Title}, {"artist", p.Artist}, {"genre", p.Genre}, {"mood", p.Mood},
		{"rights_holder", p.RightsHolder}, {"license_reference", p.LicenseReference},
		{"attribution", p.Attribution},
	} {
		if f.v.Set {
			set(f.col, f.v.Value)
		}
	}
	if p.LicenseExpiresAt.Set {
		set("license_expires_at", p.LicenseExpiresAt.Value)
	}
	if p.IsActive != nil {
		set("is_active", *p.IsActive)
		if *p.IsActive {
			sets = append(sets, "first_activated_at = COALESCE(first_activated_at, NOW())")
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE audio_library SET `+strings.Join(sets, ", ")+` WHERE id = $1`, args...); err != nil {
		return nil, err
	}
	if p.Title.Set || p.Artist.Set || p.Genre.Set {
		// genre: post-service's shape of audio_tracks is NOT NULL ''.
		if _, err := tx.Exec(ctx, `
			UPDATE audio_tracks t
			   SET title = l.title, artist = l.artist, genre = COALESCE(l.genre, ''), updated_at = NOW()
			  FROM audio_library l
			 WHERE l.id = $1 AND t.library_id = l.id`, id); err != nil {
			return nil, err
		}
	}
	out, err := getAdminSound(ctx, tx, id, false)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// WithdrawAdminLibrarySound withdraws the row in one transaction: no new
// uses (is_active FALSE), and for scope everywhere the materialised sound
// is set status 'deleted', which every sound read refuses, silencing the
// posts already using it. A second withdraw keeps the first withdrawn_at,
// records the latest reason, and can widen new_uses to everywhere but never
// narrow everywhere back (the posts stay silenced). (nil, nil) when there
// is no such row.
func (s *MediaAssetStore) WithdrawAdminLibrarySound(ctx context.Context, id uuid.UUID, scope, reason string) (*AdminLibrarySound, error) {
	if scope != WithdrawNewUses && scope != WithdrawEverywhere {
		return nil, fmt.Errorf("unknown withdraw scope %q", scope)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var effective string
	err = tx.QueryRow(ctx, `
		UPDATE audio_library
		   SET is_active = FALSE,
		       withdrawn_at = COALESCE(withdrawn_at, NOW()),
		       withdraw_reason = $3,
		       withdraw_scope = CASE WHEN withdraw_scope = 'everywhere' OR $2 = 'everywhere'
		                             THEN 'everywhere' ELSE 'new_uses' END,
		       updated_at = NOW()
		 WHERE id = $1
		RETURNING withdraw_scope`, id, scope, reason).Scan(&effective)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if effective == WithdrawEverywhere {
		if _, err := tx.Exec(ctx, `
			UPDATE audio_tracks SET status = 'deleted', updated_at = NOW()
			 WHERE library_id = $1 AND status <> 'deleted'`, id); err != nil {
			return nil, err
		}
	}
	out, err := getAdminSound(ctx, tx, id, false)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

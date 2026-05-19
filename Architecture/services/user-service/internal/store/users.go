package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User represents a user profile.
type User struct {
	ID              uuid.UUID  `json:"id"`
	Username        *string    `json:"username,omitempty"`
	DisplayName     string     `json:"display_name"`
	FirstName       *string    `json:"first_name,omitempty"`
	LastName        *string    `json:"last_name,omitempty"`
	Bio             string     `json:"bio"`
	DoB             *time.Time `json:"dob,omitempty"`
	Gender          *string    `json:"gender,omitempty"`
	AvatarMediaID   *uuid.UUID `json:"avatar_media_id,omitempty"`
	CoverMediaID    *uuid.UUID `json:"cover_media_id,omitempty"`
	Category        *string    `json:"category,omitempty"`
	Profession      *string    `json:"profession,omitempty"`
	Website         *string    `json:"website,omitempty"`
	Location        *string    `json:"location,omitempty"`
	Pronouns        *string    `json:"pronouns,omitempty"`
	StatusText      string     `json:"status_text,omitempty"`
	StatusEmoji     string     `json:"status_emoji,omitempty"`
	StatusExpiresAt *time.Time `json:"status_expires_at,omitempty"`
	BadgeFlags      int        `json:"badge_flags"`
	IsVerified      bool       `json:"is_verified"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// UserLink represents a social/external link on a user's profile.
type UserLink struct {
	UserID       uuid.UUID `json:"user_id"`
	Platform     string    `json:"platform"`
	URL          string    `json:"url"`
	DisplayLabel string    `json:"display_label,omitempty"`
	SortOrder    int       `json:"sort_order"`
}

// UserSettings represents privacy settings.
type UserSettings struct {
	UserID            uuid.UUID `json:"user_id"`
	AccountVisibility string    `json:"account_visibility"`  // public, followers, private
	AllowMessagesFrom string    `json:"allow_messages_from"` // everyone, followers, none
	AllowCommentsFrom string    `json:"allow_comments_from"` // everyone, followers, none
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

const userColumns = `id, username, display_name, first_name, last_name, bio, dob, gender,
	avatar_media_id, cover_media_id, category, profession, website, location,
	pronouns, status_text, status_emoji, status_expires_at,
	badge_flags, is_verified, created_at, updated_at`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(
		&u.ID, &u.Username, &u.DisplayName, &u.FirstName, &u.LastName, &u.Bio, &u.DoB, &u.Gender,
		&u.AvatarMediaID, &u.CoverMediaID, &u.Category, &u.Profession, &u.Website, &u.Location,
		&u.Pronouns, &u.StatusText, &u.StatusEmoji, &u.StatusExpiresAt,
		&u.BadgeFlags, &u.IsVerified, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// CreateUser creates a basic user profile (called by event consumer).
func (s *Store) CreateUser(ctx context.Context, id uuid.UUID, displayName, firstName, lastName, dob, gender string) error {
	now := time.Now()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var firstNamePtr, lastNamePtr, genderPtr *string
	if firstName != "" {
		firstNamePtr = &firstName
	}
	if lastName != "" {
		lastNamePtr = &lastName
	}
	if gender != "" {
		genderPtr = &gender
	}

	var dobPtr *time.Time
	if dob != "" {
		if t, err := time.Parse("2006-01-02", dob); err == nil {
			dobPtr = &t
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO users (id, display_name, first_name, last_name, dob, gender, bio, is_verified, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, '', false, $7, $7)
		ON CONFLICT (id) DO NOTHING
	`, id, displayName, firstNamePtr, lastNamePtr, dobPtr, genderPtr, now)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO user_settings (user_id, account_visibility, allow_messages_from, allow_comments_from, created_at, updated_at)
		VALUES ($1, 'public', 'everyone', 'everyone', $2, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, id, now)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ProjectionInput carries the identity-sourced fields used to (re)build a row
// in the local app.users projection — by the read-through repair path and the
// background reconcile job.
type ProjectionInput struct {
	ID            uuid.UUID
	Username      *string
	DisplayName   string
	FirstName     *string
	LastName      *string
	Bio           string
	Category      string
	Profession    string
	Website       string
	Location      string
	BadgeFlags    int
	IsVerified    bool
	AvatarMediaID *uuid.UUID
	CoverMediaID  *uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// UpsertUserProjection inserts or refreshes a user's row in app.users from an
// identity-sourced record. It is idempotent: a re-applied or stale record only
// updates the row when its UpdatedAt is newer than the stored one, so an
// out-of-order event or replay never clobbers fresher data. user_settings is
// seeded with defaults on first insert only.
func (s *Store) UpsertUserProjection(ctx context.Context, p ProjectionInput) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, username, display_name, first_name, last_name, bio,
			avatar_media_id, cover_media_id, category, profession, website, location,
			badge_flags, is_verified, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (id) DO UPDATE SET
			username        = EXCLUDED.username,
			display_name    = EXCLUDED.display_name,
			first_name      = EXCLUDED.first_name,
			last_name       = EXCLUDED.last_name,
			bio             = EXCLUDED.bio,
			avatar_media_id = EXCLUDED.avatar_media_id,
			cover_media_id  = EXCLUDED.cover_media_id,
			category        = EXCLUDED.category,
			profession      = EXCLUDED.profession,
			website         = EXCLUDED.website,
			location        = EXCLUDED.location,
			badge_flags     = EXCLUDED.badge_flags,
			is_verified     = EXCLUDED.is_verified,
			updated_at      = EXCLUDED.updated_at
		WHERE users.updated_at < EXCLUDED.updated_at
	`, p.ID, p.Username, p.DisplayName, p.FirstName, p.LastName, p.Bio,
		p.AvatarMediaID, p.CoverMediaID, p.Category, p.Profession, p.Website, p.Location,
		p.BadgeFlags, p.IsVerified, p.CreatedAt, p.UpdatedAt); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_settings (user_id, account_visibility, allow_messages_from, allow_comments_from, created_at, updated_at)
		VALUES ($1, 'public', 'everyone', 'everyone', $2, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, p.ID, p.CreatedAt); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// CountUsers returns the number of rows in app.users — used by the projection
// health check to compare against the identity master count.
func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// GetUser returns a user profile by ID.
func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
	row := s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

// GetUserByUsername returns a user profile by username.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := s.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE username = $1`, username)
	return scanUser(row)
}

// UpdateUser updates editable profile fields.
func (s *Store) UpdateUser(ctx context.Context, id uuid.UUID, displayName, bio string, avatarMediaID, coverMediaID *uuid.UUID, firstName, lastName, gender, username, category, profession, website, location *string, dob *time.Time) (*User, error) {
	row := s.db.QueryRow(ctx, `
		UPDATE users
		SET display_name = $2, bio = $3, avatar_media_id = $4, cover_media_id = $5,
		    first_name = $6, last_name = $7, gender = $8, dob = $9,
		    username = $10, category = $11, profession = $12, website = $13, location = $14,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING `+userColumns,
		id, displayName, bio, avatarMediaID, coverMediaID,
		firstName, lastName, gender, dob,
		username, category, profession, website, location,
	)
	return scanUser(row)
}

// GetUserLinks returns all links for a user.
func (s *Store) GetUserLinks(ctx context.Context, userID uuid.UUID) ([]UserLink, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, platform, url, display_label, sort_order
		FROM user_links
		WHERE user_id = $1
		ORDER BY sort_order
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []UserLink
	for rows.Next() {
		var l UserLink
		if err := rows.Scan(&l.UserID, &l.Platform, &l.URL, &l.DisplayLabel, &l.SortOrder); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// UpsertUserLinks replaces all links for a user.
func (s *Store) UpsertUserLinks(ctx context.Context, userID uuid.UUID, links []UserLink) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `DELETE FROM user_links WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}

	for _, l := range links {
		_, err = tx.Exec(ctx, `
			INSERT INTO user_links (user_id, platform, url, display_label, sort_order)
			VALUES ($1, $2, $3, $4, $5)
		`, userID, l.Platform, l.URL, l.DisplayLabel, l.SortOrder)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// GetSettings returns user settings.
func (s *Store) GetSettings(ctx context.Context, userID uuid.UUID) (*UserSettings, error) {
	var us UserSettings
	err := s.db.QueryRow(ctx, `
		SELECT user_id, account_visibility, allow_messages_from, allow_comments_from, created_at, updated_at
		FROM user_settings
		WHERE user_id = $1
	`, userID).Scan(&us.UserID, &us.AccountVisibility, &us.AllowMessagesFrom, &us.AllowCommentsFrom, &us.CreatedAt, &us.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &us, nil
}

// UpdateSettings updates user privacy settings.
func (s *Store) UpdateSettings(ctx context.Context, settings *UserSettings) (*UserSettings, error) {
	var us UserSettings
	err := s.db.QueryRow(ctx, `
		UPDATE user_settings
		SET account_visibility = $2, allow_messages_from = $3, allow_comments_from = $4, updated_at = NOW()
		WHERE user_id = $1
		RETURNING user_id, account_visibility, allow_messages_from, allow_comments_from, created_at, updated_at
	`, settings.UserID, settings.AccountVisibility, settings.AllowMessagesFrom, settings.AllowCommentsFrom).
		Scan(&us.UserID, &us.AccountVisibility, &us.AllowMessagesFrom, &us.AllowCommentsFrom, &us.CreatedAt, &us.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &us, nil
}

// SoftDeleteUser marks the app-level user record as deleted.
func (s *Store) SoftDeleteUser(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE users SET deleted_at = NOW() WHERE id = $1`,
		id,
	)
	return err
}

package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User represents a core user record (no profile data).
type User struct {
	ID         uuid.UUID `json:"id"`
	Status     string    `json:"status"`
	IsVerified bool      `json:"is_verified"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// UserSettings represents privacy settings.
type UserSettings struct {
	UserID            uuid.UUID `json:"user_id"`
	AccountVisibility string    `json:"account_visibility"`
	AllowMessagesFrom string    `json:"allow_messages_from"`
	AllowCommentsFrom string    `json:"allow_comments_from"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// CreateUser creates a core user record with default settings (called by event consumer).
func (s *Store) CreateUser(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO usr.users (id, status, is_verified, created_at, updated_at)
		VALUES ($1, 'active', false, $2, $2)
		ON CONFLICT (id) DO NOTHING
	`, id, now)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO usr.user_settings (user_id, account_visibility, allow_messages_from, allow_comments_from, created_at, updated_at)
		VALUES ($1, 'public', 'everyone', 'everyone', $2, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, id, now)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetUser returns a core user record.
func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
	var u User
	err := s.db.QueryRow(ctx, `
		SELECT id, status, is_verified, created_at, updated_at
		FROM usr.users
		WHERE id = $1
	`, id).Scan(&u.ID, &u.Status, &u.IsVerified, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// GetSettings returns user privacy settings.
func (s *Store) GetSettings(ctx context.Context, userID uuid.UUID) (*UserSettings, error) {
	var us UserSettings
	err := s.db.QueryRow(ctx, `
		SELECT user_id, account_visibility, allow_messages_from, allow_comments_from, created_at, updated_at
		FROM usr.user_settings
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

// ListUsers returns all active users with pagination.
func (s *Store) ListUsers(ctx context.Context, limit, offset int) ([]User, int, error) {
	var total int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM usr.users WHERE status = 'active'`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, status, is_verified, created_at, updated_at
		FROM usr.users
		WHERE status = 'active'
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Status, &u.IsVerified, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

// UpdateSettings updates user privacy settings.
func (s *Store) UpdateSettings(ctx context.Context, settings *UserSettings) (*UserSettings, error) {
	var us UserSettings
	err := s.db.QueryRow(ctx, `
		UPDATE usr.user_settings
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

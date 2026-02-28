package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AboutItem represents a single item in a user's about section.
type AboutItem struct {
	UserID     uuid.UUID       `json:"user_id"`
	Section    string          `json:"section"`
	ItemID     uuid.UUID       `json:"item_id"`
	Data       json.RawMessage `json:"data"`
	Visibility string          `json:"visibility"` // public, followers, friends, private
	SortOrder  int             `json:"sort_order"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// GetAllAbout returns all about items for a user grouped by section.
func (s *Store) GetAllAbout(ctx context.Context, userID uuid.UUID) (map[string][]AboutItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, section, item_id, data, visibility, sort_order, created_at, updated_at
		FROM user_about
		WHERE user_id = $1
		ORDER BY section, sort_order, created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]AboutItem)
	for rows.Next() {
		var item AboutItem
		if err := rows.Scan(&item.UserID, &item.Section, &item.ItemID, &item.Data, &item.Visibility, &item.SortOrder, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result[item.Section] = append(result[item.Section], item)
	}
	return result, rows.Err()
}

// GetAboutSection returns about items for a specific section.
func (s *Store) GetAboutSection(ctx context.Context, userID uuid.UUID, section string) ([]AboutItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, section, item_id, data, visibility, sort_order, created_at, updated_at
		FROM user_about
		WHERE user_id = $1 AND section = $2
		ORDER BY sort_order, created_at
	`, userID, section)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []AboutItem
	for rows.Next() {
		var item AboutItem
		if err := rows.Scan(&item.UserID, &item.Section, &item.ItemID, &item.Data, &item.Visibility, &item.SortOrder, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// UpsertAboutItem creates or updates an about item.
func (s *Store) UpsertAboutItem(ctx context.Context, item *AboutItem) (*AboutItem, error) {
	now := time.Now()
	if item.ItemID == uuid.Nil {
		item.ItemID = uuid.New()
	}

	var out AboutItem
	err := s.db.QueryRow(ctx, `
		INSERT INTO user_about (user_id, section, item_id, data, visibility, sort_order, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT (user_id, section, item_id) DO UPDATE
		SET data = $4, visibility = $5, sort_order = $6, updated_at = $7
		RETURNING user_id, section, item_id, data, visibility, sort_order, created_at, updated_at
	`, item.UserID, item.Section, item.ItemID, item.Data, item.Visibility, item.SortOrder, now).
		Scan(&out.UserID, &out.Section, &out.ItemID, &out.Data, &out.Visibility, &out.SortOrder, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAboutItem removes a specific about item.
func (s *Store) DeleteAboutItem(ctx context.Context, userID uuid.UUID, section string, itemID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM user_about WHERE user_id = $1 AND section = $2 AND item_id = $3
	`, userID, section, itemID)
	return err
}

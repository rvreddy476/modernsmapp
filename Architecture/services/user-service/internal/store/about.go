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

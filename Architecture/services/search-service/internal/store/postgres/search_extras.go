package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SavedSearch represents a user-saved search query.
type SavedSearch struct {
	ID         uuid.UUID `json:"id"`
	UserID     uuid.UUID `json:"user_id"`
	Query      string    `json:"query"`
	SearchType string    `json:"search_type"`
	SavedAt    time.Time `json:"saved_at"`
}

// SearchHistoryItem represents a single entry in a user's search history.
type SearchHistoryItem struct {
	ID         uuid.UUID `json:"id"`
	UserID     uuid.UUID `json:"user_id"`
	Query      string    `json:"query"`
	SearchedAt time.Time `json:"searched_at"`
}

// SearchExtrasStore provides Postgres-backed saved searches and search history.
type SearchExtrasStore struct {
	db *pgxpool.Pool
}

// NewExtrasStore returns a new SearchExtrasStore backed by the given connection pool.
func NewExtrasStore(db *pgxpool.Pool) *SearchExtrasStore {
	return &SearchExtrasStore{db: db}
}

// SaveSearch saves a search query for a user. After inserting the new row,
// any rows beyond the most-recent 50 are deleted to enforce the per-user cap.
func (s *SearchExtrasStore) SaveSearch(ctx context.Context, userID uuid.UUID, query, searchType string) (*SavedSearch, error) {
	ss := &SavedSearch{
		ID:         uuid.New(),
		UserID:     userID,
		Query:      query,
		SearchType: searchType,
		SavedAt:    time.Now().UTC(),
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO saved_searches (id, user_id, query, search_type, saved_at)
		VALUES ($1, $2, $3, $4, $5)
	`, ss.ID, ss.UserID, ss.Query, ss.SearchType, ss.SavedAt)
	if err != nil {
		return nil, err
	}

	// Enforce max-50 saved searches per user: delete oldest beyond the limit.
	_, err = s.db.Exec(ctx, `
		DELETE FROM saved_searches
		WHERE user_id = $1
		  AND id NOT IN (
		      SELECT id FROM saved_searches
		      WHERE user_id = $1
		      ORDER BY saved_at DESC
		      LIMIT 50
		  )
	`, userID)
	if err != nil {
		// Non-fatal: log and continue.
		return ss, nil
	}
	return ss, nil
}

// GetSavedSearches returns all saved searches for a user, newest first.
func (s *SearchExtrasStore) GetSavedSearches(ctx context.Context, userID uuid.UUID) ([]SavedSearch, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, query, search_type, saved_at
		FROM saved_searches
		WHERE user_id = $1
		ORDER BY saved_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SavedSearch
	for rows.Next() {
		var ss SavedSearch
		if err := rows.Scan(&ss.ID, &ss.UserID, &ss.Query, &ss.SearchType, &ss.SavedAt); err != nil {
			return nil, err
		}
		results = append(results, ss)
	}
	return results, rows.Err()
}

// DeleteSavedSearch removes a saved search belonging to userID.
func (s *SearchExtrasStore) DeleteSavedSearch(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM saved_searches WHERE id = $1 AND user_id = $2
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordSearchHistory inserts a search_history row for the user.
// After insert, rows beyond the most-recent 20 are deleted.
func (s *SearchExtrasStore) RecordSearchHistory(ctx context.Context, userID uuid.UUID, query string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO search_history (id, user_id, query, searched_at)
		VALUES ($1, $2, $3, $4)
	`, uuid.New(), userID, query, time.Now().UTC())
	if err != nil {
		return err
	}

	// Enforce max-20 history rows per user.
	_, _ = s.db.Exec(ctx, `
		DELETE FROM search_history
		WHERE user_id = $1
		  AND id NOT IN (
		      SELECT id FROM search_history
		      WHERE user_id = $1
		      ORDER BY searched_at DESC
		      LIMIT 20
		  )
	`, userID)
	return nil
}

// GetSearchHistory returns recent search history for a user, newest first.
// limit is capped at 20.
func (s *SearchExtrasStore) GetSearchHistory(ctx context.Context, userID uuid.UUID, limit int) ([]SearchHistoryItem, error) {
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, query, searched_at
		FROM search_history
		WHERE user_id = $1
		ORDER BY searched_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchHistoryItem
	for rows.Next() {
		var h SearchHistoryItem
		if err := rows.Scan(&h.ID, &h.UserID, &h.Query, &h.SearchedAt); err != nil {
			return nil, err
		}
		results = append(results, h)
	}
	return results, rows.Err()
}

// ClearSearchHistory deletes all search history for a user.
func (s *SearchExtrasStore) ClearSearchHistory(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM search_history WHERE user_id = $1`, userID)
	return err
}

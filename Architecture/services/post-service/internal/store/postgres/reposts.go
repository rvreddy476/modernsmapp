package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Repost struct {
	ID                uuid.UUID  `json:"id"`
	UserID            uuid.UUID  `json:"user_id"`
	OriginalPostID    uuid.UUID  `json:"original_post_id"`
	RepostType        string     `json:"repost_type"`
	QuoteText         string     `json:"quote_text,omitempty"`
	Visibility        string     `json:"visibility"`
	Status            string     `json:"status"`
	SourceContextType string     `json:"source_context_type,omitempty"`
	SourceContextID   *uuid.UUID `json:"source_context_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	DeletedAt         *time.Time `json:"deleted_at,omitempty"`
}

const repostCols = `id, user_id, original_post_id, repost_type, quote_text,
	visibility, status, source_context_type, source_context_id,
	created_at, updated_at, deleted_at`

func scanRepost(row pgx.Row) (*Repost, error) {
	var r Repost
	var qt, sct *string
	var sci *uuid.UUID
	err := row.Scan(
		&r.ID, &r.UserID, &r.OriginalPostID, &r.RepostType, &qt,
		&r.Visibility, &r.Status, &sct, &sci,
		&r.CreatedAt, &r.UpdatedAt, &r.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	if qt != nil {
		r.QuoteText = *qt
	}
	if sct != nil {
		r.SourceContextType = *sct
	}
	r.SourceContextID = sci
	return &r, nil
}

func (s *Store) CreateRepost(ctx context.Context, r *Repost) error {
	r.ID = uuid.New()
	r.Status = "active"
	r.CreatedAt = time.Now()
	r.UpdatedAt = r.CreatedAt

	var qt *string
	if r.QuoteText != "" {
		qt = &r.QuoteText
	}
	var sct *string
	if r.SourceContextType != "" {
		sct = &r.SourceContextType
	}

	_, err := s.db.Exec(ctx,
		`INSERT INTO post_reposts (id, user_id, original_post_id, repost_type, quote_text,
			visibility, status, source_context_type, source_context_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		r.ID, r.UserID, r.OriginalPostID, r.RepostType, qt,
		r.Visibility, r.Status, sct, r.SourceContextID, r.CreatedAt, r.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("ALREADY_REPOSTED")
		}
		return err
	}
	return nil
}

func (s *Store) SoftDeleteRepost(ctx context.Context, userID, originalPostID uuid.UUID) error {
	now := time.Now()
	tag, err := s.db.Exec(ctx,
		`UPDATE post_reposts SET status = 'undone', deleted_at = $3, updated_at = $3
		WHERE user_id = $1 AND original_post_id = $2 AND status = 'active'`,
		userID, originalPostID, now,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("REPOST_NOT_FOUND")
	}
	return nil
}

func (s *Store) GetActiveRepost(ctx context.Context, userID, originalPostID uuid.UUID) (*Repost, error) {
	row := s.db.QueryRow(ctx,
		fmt.Sprintf(`SELECT %s FROM post_reposts WHERE user_id = $1 AND original_post_id = $2 AND status = 'active'`, repostCols),
		userID, originalPostID,
	)
	r, err := scanRepost(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func (s *Store) GetRepostByID(ctx context.Context, id uuid.UUID) (*Repost, error) {
	row := s.db.QueryRow(ctx,
		fmt.Sprintf(`SELECT %s FROM post_reposts WHERE id = $1`, repostCols),
		id,
	)
	r, err := scanRepost(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func (s *Store) BatchGetActiveReposts(ctx context.Context, userID uuid.UUID, postIDs []uuid.UUID) (map[uuid.UUID]*Repost, error) {
	if len(postIDs) == 0 {
		return map[uuid.UUID]*Repost{}, nil
	}
	rows, err := s.db.Query(ctx,
		fmt.Sprintf(`SELECT %s FROM post_reposts WHERE user_id = $1 AND original_post_id = ANY($2) AND status = 'active'`, repostCols),
		userID, postIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[uuid.UUID]*Repost, len(postIDs))
	for rows.Next() {
		r, err := scanRepost(rows)
		if err != nil {
			return nil, err
		}
		result[r.OriginalPostID] = r
	}
	return result, rows.Err()
}

func (s *Store) ListReposters(ctx context.Context, originalPostID uuid.UUID, limit int, cursor string) ([]Repost, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var rows pgx.Rows
	var err error
	if cursor != "" {
		cursorTime, parseErr := time.Parse(time.RFC3339Nano, cursor)
		if parseErr != nil {
			return nil, "", fmt.Errorf("invalid cursor")
		}
		rows, err = s.db.Query(ctx,
			fmt.Sprintf(`SELECT %s FROM post_reposts
				WHERE original_post_id = $1 AND status = 'active' AND created_at < $2
				ORDER BY created_at DESC LIMIT $3`, repostCols),
			originalPostID, cursorTime, limit,
		)
	} else {
		rows, err = s.db.Query(ctx,
			fmt.Sprintf(`SELECT %s FROM post_reposts
				WHERE original_post_id = $1 AND status = 'active'
				ORDER BY created_at DESC LIMIT $2`, repostCols),
			originalPostID, limit,
		)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var reposts []Repost
	for rows.Next() {
		r, err := scanRepost(rows)
		if err != nil {
			return nil, "", err
		}
		reposts = append(reposts, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(reposts) == limit {
		nextCursor = reposts[len(reposts)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	return reposts, nextCursor, nil
}

func (s *Store) ListUserReposts(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]Repost, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var rows pgx.Rows
	var err error
	if cursor != "" {
		cursorTime, parseErr := time.Parse(time.RFC3339Nano, cursor)
		if parseErr != nil {
			return nil, "", fmt.Errorf("invalid cursor")
		}
		rows, err = s.db.Query(ctx,
			fmt.Sprintf(`SELECT %s FROM post_reposts
				WHERE user_id = $1 AND status = 'active' AND created_at < $2
				ORDER BY created_at DESC LIMIT $3`, repostCols),
			userID, cursorTime, limit,
		)
	} else {
		rows, err = s.db.Query(ctx,
			fmt.Sprintf(`SELECT %s FROM post_reposts
				WHERE user_id = $1 AND status = 'active'
				ORDER BY created_at DESC LIMIT $2`, repostCols),
			userID, limit,
		)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var reposts []Repost
	for rows.Next() {
		r, err := scanRepost(rows)
		if err != nil {
			return nil, "", err
		}
		reposts = append(reposts, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(reposts) == limit {
		nextCursor = reposts[len(reposts)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	return reposts, nextCursor, nil
}

func (s *Store) SwitchRepostType(ctx context.Context, userID, originalPostID uuid.UUID, newType, quoteText, visibility string, sourceCtxType string, sourceCtxID *uuid.UUID) (*Repost, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	now := time.Now()

	// Soft-delete existing active repost
	_, err = tx.Exec(ctx,
		`UPDATE post_reposts SET status = 'undone', deleted_at = $3, updated_at = $3
		WHERE user_id = $1 AND original_post_id = $2 AND status = 'active'`,
		userID, originalPostID, now,
	)
	if err != nil {
		return nil, err
	}

	// Insert new repost
	newID := uuid.New()
	var qt *string
	if quoteText != "" {
		qt = &quoteText
	}
	var sct *string
	if sourceCtxType != "" {
		sct = &sourceCtxType
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO post_reposts (id, user_id, original_post_id, repost_type, quote_text,
			visibility, status, source_context_type, source_context_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'active', $7, $8, $9, $9)`,
		newID, userID, originalPostID, newType, qt, visibility, sct, sourceCtxID, now,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &Repost{
		ID:                newID,
		UserID:            userID,
		OriginalPostID:    originalPostID,
		RepostType:        newType,
		QuoteText:         quoteText,
		Visibility:        visibility,
		Status:            "active",
		SourceContextType: sourceCtxType,
		SourceContextID:   sourceCtxID,
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}

func (s *Store) BatchGetRepostsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*Repost, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]*Repost{}, nil
	}
	rows, err := s.db.Query(ctx,
		fmt.Sprintf(`SELECT %s FROM post_reposts WHERE id = ANY($1)`, repostCols),
		ids,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[uuid.UUID]*Repost, len(ids))
	for rows.Next() {
		r, err := scanRepost(rows)
		if err != nil {
			return nil, err
		}
		result[r.ID] = r
	}
	return result, rows.Err()
}

func (s *Store) IncrementRepostCount(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO post_engagement_counts (post_id, repost_count)
		VALUES ($1, 1)
		ON CONFLICT (post_id) DO UPDATE SET repost_count = post_engagement_counts.repost_count + 1, updated_at = now()`,
		postID,
	)
	return err
}

func (s *Store) DecrementRepostCount(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE post_engagement_counts SET repost_count = GREATEST(repost_count - 1, 0), updated_at = now()
		WHERE post_id = $1`,
		postID,
	)
	return err
}

func (s *Store) GetRepostCount(ctx context.Context, postID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(repost_count, 0) FROM post_engagement_counts WHERE post_id = $1`,
		postID,
	).Scan(&count)
	if err != nil {
		return 0, nil // no row = 0 reposts
	}
	return count, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique")
}

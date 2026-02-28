package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SavedItem represents a user's saved post/video/reel.
type SavedItem struct {
	ID             uuid.UUID `json:"id"`
	UserID         uuid.UUID `json:"user_id"`
	TargetType     string    `json:"target_type"`
	TargetID       uuid.UUID `json:"target_id"`
	CollectionName string    `json:"collection_name"`
	CreatedAt      time.Time `json:"created_at"`
}

// SavedCollection represents a named collection with item count.
type SavedCollection struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// SaveItem adds an item to a user's saved collection.
func (s *Store) SaveItem(ctx context.Context, userID uuid.UUID, targetType string, targetID uuid.UUID, collectionName string) (*SavedItem, error) {
	if collectionName == "" {
		collectionName = "All Saved"
	}

	item := &SavedItem{
		ID:             uuid.New(),
		UserID:         userID,
		TargetType:     targetType,
		TargetID:       targetID,
		CollectionName: collectionName,
		CreatedAt:      time.Now(),
	}

	_, err := s.db.Exec(ctx, `
		INSERT INTO saved_items (id, user_id, target_type, target_id, collection_name, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, target_type, target_id) DO UPDATE SET collection_name = $5
	`, item.ID, item.UserID, item.TargetType, item.TargetID, item.CollectionName, item.CreatedAt)
	if err != nil {
		return nil, err
	}

	return item, nil
}

// UnsaveItem removes a saved item by ID. Returns error if not found or not owned by user.
func (s *Store) UnsaveItem(ctx context.Context, savedID, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM saved_items WHERE id = $1 AND user_id = $2
	`, savedID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("SAVED_ITEM_NOT_FOUND")
	}
	return nil
}

// ListSavedItems returns paginated saved items for a user, optionally filtered by collection.
func (s *Store) ListSavedItems(ctx context.Context, userID uuid.UUID, collectionName string, limit int, cursor string) ([]SavedItem, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var args []interface{}
	args = append(args, userID, limit+1)

	query := `SELECT id, user_id, target_type, target_id, collection_name, created_at
		FROM saved_items
		WHERE user_id = $1`

	argIdx := 3
	if collectionName != "" {
		query += fmt.Sprintf(` AND collection_name = $%d`, argIdx)
		args = append(args, collectionName)
		argIdx++
	}

	if cursor != "" {
		cursorTime, err := time.Parse(time.RFC3339Nano, cursor)
		if err == nil {
			query += fmt.Sprintf(` AND created_at < $%d`, argIdx)
			args = append(args, cursorTime)
		}
	}

	query += ` ORDER BY created_at DESC LIMIT $2`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var items []SavedItem
	for rows.Next() {
		var item SavedItem
		if err := rows.Scan(&item.ID, &item.UserID, &item.TargetType, &item.TargetID,
			&item.CollectionName, &item.CreatedAt); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(items) > limit {
		nextCursor = items[limit-1].CreatedAt.Format(time.RFC3339Nano)
		items = items[:limit]
	}

	return items, nextCursor, nil
}

// ListCollections returns all collection names with item counts for a user.
func (s *Store) ListCollections(ctx context.Context, userID uuid.UUID) ([]SavedCollection, error) {
	rows, err := s.db.Query(ctx, `
		SELECT collection_name, COUNT(*) as count
		FROM saved_items
		WHERE user_id = $1
		GROUP BY collection_name
		ORDER BY count DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collections []SavedCollection
	for rows.Next() {
		var c SavedCollection
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return nil, err
		}
		collections = append(collections, c)
	}
	return collections, rows.Err()
}

// IsSaved checks if a specific target is saved by the user.
func (s *Store) IsSaved(ctx context.Context, userID uuid.UUID, targetType string, targetID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM saved_items WHERE user_id = $1 AND target_type = $2 AND target_id = $3)
	`, userID, targetType, targetID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return exists, err
}

// GetPostsByHashtag returns posts containing a specific hashtag, paginated.
func (s *Store) GetPostsByHashtag(ctx context.Context, hashtag string, limit int, cursor string) ([]Post, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var args []interface{}
	args = append(args, hashtag, limit+1)

	query := `SELECT ` + postCols + `
		FROM posts
		WHERE $1 = ANY(hashtags) AND deleted_at IS NULL AND visibility = 'public'`

	if cursor != "" {
		cursorTime, err := time.Parse(time.RFC3339Nano, cursor)
		if err == nil {
			query += ` AND created_at < $3`
			args = append(args, cursorTime)
		}
	}

	query += ` ORDER BY created_at DESC LIMIT $2`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	posts, err := scanPostRows(rows)
	if err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(posts) > limit {
		nextCursor = posts[limit-1].CreatedAt.Format(time.RFC3339Nano)
		posts = posts[:limit]
	}

	// Batch-fetch media
	if len(posts) > 0 {
		postIDs := make([]uuid.UUID, len(posts))
		for i, p := range posts {
			postIDs[i] = p.ID
		}
		mediaRows, err := s.db.Query(ctx, `
			SELECT post_id, media_id, kind FROM post_media WHERE post_id = ANY($1)
		`, postIDs)
		if err == nil {
			defer mediaRows.Close()
			mediaMap := make(map[uuid.UUID][]PostMedia)
			for mediaRows.Next() {
				var postID uuid.UUID
				var m PostMedia
				if err := mediaRows.Scan(&postID, &m.MediaID, &m.Kind); err == nil {
					mediaMap[postID] = append(mediaMap[postID], m)
				}
			}
			for i := range posts {
				posts[i].Media = mediaMap[posts[i].ID]
			}
		}
	}

	return posts, nextCursor, nil
}

package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EditorSession is an auto-saved creative studio draft.
type EditorSession struct {
	ID           uuid.UUID       `json:"id"`
	UserID       uuid.UUID       `json:"user_id"`
	Mode         string          `json:"mode"`
	StateJSON    json.RawMessage `json:"state_json"`
	ThumbnailURL *string         `json:"thumbnail_url,omitempty"`
	LastSavedAt  time.Time       `json:"last_saved_at"`
	CreatedAt    time.Time       `json:"created_at"`
}

// EditorSessionSummary is the lightweight list view of an editor session.
type EditorSessionSummary struct {
	ID           uuid.UUID `json:"id"`
	Mode         string    `json:"mode"`
	ThumbnailURL *string   `json:"thumbnail_url,omitempty"`
	LastSavedAt  time.Time `json:"last_saved_at"`
}

// StickerPack groups related stickers together.
type StickerPack struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Category string    `json:"category"`
	CoverURL string    `json:"cover_url"`
}

// Sticker is a single creative-studio sticker asset.
type Sticker struct {
	ID              uuid.UUID `json:"id"`
	PackID          uuid.UUID `json:"pack_id"`
	AssetURL        string    `json:"asset_url"`
	Type            string    `json:"type"`
	InteractiveType *string   `json:"interactive_type,omitempty"`
	Tags            []string  `json:"tags"`
}

// FlickTemplate is a pre-built video editing template.
type FlickTemplate struct {
	ID           uuid.UUID       `json:"id"`
	Title        string          `json:"title"`
	Category     string          `json:"category"`
	PreviewURL   string          `json:"preview_url"`
	CoverURL     string          `json:"cover_url"`
	TemplateJSON json.RawMessage `json:"template_json"`
}

// CreateEditorSession inserts a new draft and returns its ID and created_at.
func (s *MediaAssetStore) CreateEditorSession(ctx context.Context, userID uuid.UUID, mode string, stateJSON json.RawMessage) (*EditorSession, error) {
	if stateJSON == nil {
		stateJSON = json.RawMessage(`{}`)
	}
	sess := &EditorSession{}
	err := s.db.QueryRow(ctx,
		`INSERT INTO editor_sessions (user_id, mode, state_json)
		 VALUES ($1, $2, $3)
		 RETURNING id, user_id, mode, state_json, thumbnail_url, last_saved_at, created_at`,
		userID, mode, []byte(stateJSON),
	).Scan(&sess.ID, &sess.UserID, &sess.Mode, &sess.StateJSON,
		&sess.ThumbnailURL, &sess.LastSavedAt, &sess.CreatedAt)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// ListEditorSessions returns the most-recently-saved sessions for a user (max 10).
func (s *MediaAssetStore) ListEditorSessions(ctx context.Context, userID uuid.UUID) ([]EditorSessionSummary, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, mode, thumbnail_url, last_saved_at
		 FROM editor_sessions
		 WHERE user_id = $1
		 ORDER BY last_saved_at DESC
		 LIMIT 10`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []EditorSessionSummary
	for rows.Next() {
		var s EditorSessionSummary
		if err := rows.Scan(&s.ID, &s.Mode, &s.ThumbnailURL, &s.LastSavedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// UpdateEditorSession persists new state_json for an owned session.
func (s *MediaAssetStore) UpdateEditorSession(ctx context.Context, sessionID, userID uuid.UUID, stateJSON json.RawMessage) error {
	if stateJSON == nil {
		stateJSON = json.RawMessage(`{}`)
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE editor_sessions
		 SET state_json = $1, last_saved_at = NOW()
		 WHERE id = $2 AND user_id = $3`,
		[]byte(stateJSON), sessionID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteEditorSession removes an owned session.
func (s *MediaAssetStore) DeleteEditorSession(ctx context.Context, sessionID, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM editor_sessions WHERE id = $1 AND user_id = $2`,
		sessionID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListStickerPacks returns all active sticker packs ordered by sort_order.
func (s *MediaAssetStore) ListStickerPacks(ctx context.Context) ([]StickerPack, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, name, category, cover_url
		 FROM sticker_packs
		 WHERE is_active = TRUE
		 ORDER BY sort_order`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var packs []StickerPack
	for rows.Next() {
		var p StickerPack
		if err := rows.Scan(&p.ID, &p.Name, &p.Category, &p.CoverURL); err != nil {
			return nil, err
		}
		packs = append(packs, p)
	}
	return packs, rows.Err()
}

// ListStickers returns active stickers optionally filtered by pack category, ordered by popularity.
func (s *MediaAssetStore) ListStickers(ctx context.Context, category string, limit int) ([]Sticker, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, pack_id, asset_url, type, interactive_type, tags
		 FROM stickers
		 WHERE is_active = TRUE
		   AND ($1 = '' OR pack_id IN (SELECT id FROM sticker_packs WHERE category = $1))
		 ORDER BY use_count DESC
		 LIMIT $2`,
		category, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stickers []Sticker
	for rows.Next() {
		var st Sticker
		if err := rows.Scan(&st.ID, &st.PackID, &st.AssetURL, &st.Type,
			&st.InteractiveType, &st.Tags); err != nil {
			return nil, err
		}
		stickers = append(stickers, st)
	}
	return stickers, rows.Err()
}

// IncrementStickerUse bumps the use_count for a sticker (fire-and-forget).
func (s *MediaAssetStore) IncrementStickerUse(ctx context.Context, stickerID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE stickers SET use_count = use_count + 1 WHERE id = $1`,
		stickerID,
	)
	return err
}

// ListTemplates returns active flick templates ordered by popularity.
func (s *MediaAssetStore) ListTemplates(ctx context.Context, category string, limit int) ([]FlickTemplate, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, title, category, preview_url, cover_url, template_json
		 FROM flick_templates
		 WHERE is_active = TRUE
		   AND ($1 = '' OR category = $1)
		 ORDER BY use_count DESC
		 LIMIT $2`,
		category, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var templates []FlickTemplate
	for rows.Next() {
		var t FlickTemplate
		if err := rows.Scan(&t.ID, &t.Title, &t.Category, &t.PreviewURL, &t.CoverURL, &t.TemplateJSON); err != nil {
			return nil, err
		}
		templates = append(templates, t)
	}
	return templates, rows.Err()
}

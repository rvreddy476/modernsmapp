package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// CreateEditorSession creates a new auto-save draft for a user.
func (s *Service) CreateEditorSession(ctx context.Context, userID uuid.UUID, mode string, stateJSON json.RawMessage) (*postgres.EditorSession, error) {
	if mode == "" {
		return nil, fmt.Errorf("mode is required")
	}
	return s.pgStore.CreateEditorSession(ctx, userID, mode, stateJSON)
}

// ListEditorSessions returns up to 10 recent draft sessions for a user.
func (s *Service) ListEditorSessions(ctx context.Context, userID uuid.UUID) ([]postgres.EditorSessionSummary, error) {
	return s.pgStore.ListEditorSessions(ctx, userID)
}

// UpdateEditorSession persists new state_json for an owned session.
func (s *Service) UpdateEditorSession(ctx context.Context, sessionID, userID uuid.UUID, stateJSON json.RawMessage) error {
	return s.pgStore.UpdateEditorSession(ctx, sessionID, userID, stateJSON)
}

// DeleteEditorSession removes an owned session.
func (s *Service) DeleteEditorSession(ctx context.Context, sessionID, userID uuid.UUID) error {
	return s.pgStore.DeleteEditorSession(ctx, sessionID, userID)
}

// ListStickerPacks returns all active sticker packs.
func (s *Service) ListStickerPacks(ctx context.Context) ([]postgres.StickerPack, error) {
	return s.pgStore.ListStickerPacks(ctx)
}

// ListStickers returns active stickers, optionally filtered by category, ordered by popularity.
func (s *Service) ListStickers(ctx context.Context, category string, limit int) ([]postgres.Sticker, error) {
	return s.pgStore.ListStickers(ctx, category, limit)
}

// RecordStickerUse increments the use_count for a sticker (fire-and-forget).
func (s *Service) RecordStickerUse(ctx context.Context, stickerID uuid.UUID) {
	_ = s.pgStore.IncrementStickerUse(ctx, stickerID)
}

// ListTemplates returns active flick templates, optionally filtered by category.
func (s *Service) ListTemplates(ctx context.Context, category string, limit int) ([]postgres.FlickTemplate, error) {
	return s.pgStore.ListTemplates(ctx, category, limit)
}

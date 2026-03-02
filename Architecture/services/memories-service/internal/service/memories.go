package service

import (
	"context"
	"fmt"
	"time"

	"github.com/facebook-like/memories-service/internal/store/postgres"
	"github.com/google/uuid"
)

type Service struct {
	store *postgres.Store
}

func New(store *postgres.Store) *Service {
	return &Service{store: store}
}

// --- Collections ---

type CreateCollectionInput struct {
	UserID      uuid.UUID
	Title       string
	Description string
	CoverURL    *string
	Visibility  string
}

func (s *Service) CreateCollection(ctx context.Context, input *CreateCollectionInput) (*postgres.Collection, error) {
	if input.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	visibility := input.Visibility
	if visibility == "" {
		visibility = "private"
	}

	now := time.Now()
	c := &postgres.Collection{
		ID:          uuid.New(),
		UserID:      input.UserID,
		Title:       input.Title,
		Description: input.Description,
		CoverURL:    input.CoverURL,
		Visibility:  visibility,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.store.CreateCollection(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) GetCollection(ctx context.Context, id, viewerID uuid.UUID) (*postgres.Collection, error) {
	c, err := s.store.GetCollection(ctx, id)
	if err != nil {
		return nil, err
	}
	// Private collections only visible to owner
	if c.Visibility == "private" && c.UserID != viewerID {
		return nil, fmt.Errorf("not authorized")
	}
	return c, nil
}

func (s *Service) ListCollections(ctx context.Context, userID uuid.UUID, limit, offset int) ([]postgres.Collection, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.store.ListCollections(ctx, userID, limit, offset)
}

func (s *Service) UpdateCollection(ctx context.Context, id, userID uuid.UUID, title, description, visibility string) error {
	c, err := s.store.GetCollection(ctx, id)
	if err != nil {
		return err
	}
	if c.UserID != userID {
		return fmt.Errorf("not the collection owner")
	}
	return s.store.UpdateCollection(ctx, id, title, description, visibility)
}

func (s *Service) DeleteCollection(ctx context.Context, id, userID uuid.UUID) error {
	return s.store.DeleteCollection(ctx, id, userID)
}

// --- Collection Items ---

type AddItemInput struct {
	CollectionID uuid.UUID
	UserID       uuid.UUID
	PostID       *uuid.UUID
	MediaURL     *string
	Caption      string
}

func (s *Service) AddCollectionItem(ctx context.Context, input *AddItemInput) (*postgres.CollectionItem, error) {
	// Verify ownership
	c, err := s.store.GetCollection(ctx, input.CollectionID)
	if err != nil {
		return nil, fmt.Errorf("collection not found")
	}
	if c.UserID != input.UserID {
		return nil, fmt.Errorf("not the collection owner")
	}

	item := &postgres.CollectionItem{
		ID:           uuid.New(),
		CollectionID: input.CollectionID,
		PostID:       input.PostID,
		MediaURL:     input.MediaURL,
		Caption:      input.Caption,
		SortOrder:    c.ItemCount,
		CreatedAt:    time.Now(),
	}
	if err := s.store.AddCollectionItem(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) ListCollectionItems(ctx context.Context, collectionID, viewerID uuid.UUID, limit, offset int) ([]postgres.CollectionItem, error) {
	c, err := s.store.GetCollection(ctx, collectionID)
	if err != nil {
		return nil, err
	}
	if c.Visibility == "private" && c.UserID != viewerID {
		return nil, fmt.Errorf("not authorized")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.store.ListCollectionItems(ctx, collectionID, limit, offset)
}

func (s *Service) RemoveCollectionItem(ctx context.Context, itemID, collectionID, userID uuid.UUID) error {
	c, err := s.store.GetCollection(ctx, collectionID)
	if err != nil {
		return err
	}
	if c.UserID != userID {
		return fmt.Errorf("not the collection owner")
	}
	return s.store.RemoveCollectionItem(ctx, itemID, collectionID)
}

// --- On This Day ---

func (s *Service) GetOnThisDay(ctx context.Context, userID uuid.UUID) ([]postgres.OnThisDay, error) {
	today := time.Now()
	dateStr := today.Format("2006-01-02")

	// Try cached first
	memories, err := s.store.GetOnThisDay(ctx, userID, dateStr)
	if err != nil {
		return nil, err
	}
	if len(memories) > 0 {
		return s.filterByPreferences(ctx, userID, memories)
	}

	// Generate fresh
	memories, err = s.store.GenerateOnThisDay(ctx, userID, today)
	if err != nil {
		return nil, err
	}
	return s.filterByPreferences(ctx, userID, memories)
}

func (s *Service) filterByPreferences(ctx context.Context, userID uuid.UUID, memories []postgres.OnThisDay) ([]postgres.OnThisDay, error) {
	prefs, err := s.store.GetPreferences(ctx, userID)
	if err != nil {
		return memories, nil
	}
	if !prefs.Enabled {
		return nil, nil
	}

	hiddenYears := make(map[int]bool)
	for _, y := range prefs.HiddenYears {
		hiddenYears[y] = true
	}

	var filtered []postgres.OnThisDay
	for _, m := range memories {
		if hiddenYears[m.YearsAgo] {
			continue
		}
		filtered = append(filtered, m)
	}
	return filtered, nil
}

// --- Preferences ---

func (s *Service) GetPreferences(ctx context.Context, userID uuid.UUID) (*postgres.Preferences, error) {
	return s.store.GetPreferences(ctx, userID)
}

func (s *Service) UpdatePreferences(ctx context.Context, prefs *postgres.Preferences) error {
	return s.store.UpdatePreferences(ctx, prefs)
}

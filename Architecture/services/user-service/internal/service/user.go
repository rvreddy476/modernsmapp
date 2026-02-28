package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/facebook-like/user-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	store *store.Store
	rdb   *redis.Client
}

func New(s *store.Store, rdb *redis.Client) *Service {
	return &Service{store: s, rdb: rdb}
}

// CreateUser handles user creation from event.
func (s *Service) CreateUser(ctx context.Context, id uuid.UUID, phone, email, firstName, lastName, dob, gender string) error {
	displayName := firstName + " " + lastName
	if displayName == " " {
		displayName = "User " + id.String()[:8]
	}
	return s.store.CreateUser(ctx, id, displayName, firstName, lastName, dob, gender)
}

// GetUser returns user profile, trying cache first.
func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (*store.User, error) {
	cacheKey := fmt.Sprintf("user:card:%s", id)
	val, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var u store.User
		if err := json.Unmarshal([]byte(val), &u); err == nil {
			return &u, nil
		}
	}

	u, err := s.store.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}

	go func() {
		data, _ := json.Marshal(u)
		s.rdb.Set(context.Background(), cacheKey, data, 5*time.Minute)
	}()

	return u, nil
}

// GetUserByUsername returns user profile by username, trying cache first.
func (s *Service) GetUserByUsername(ctx context.Context, username string) (*store.User, error) {
	// Check username→UUID mapping cache
	nameKey := fmt.Sprintf("user:name:%s", username)
	val, err := s.rdb.Get(ctx, nameKey).Result()
	if err == nil {
		id, err := uuid.Parse(val)
		if err == nil {
			return s.GetUser(ctx, id)
		}
	}

	u, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}

	// Cache both username→UUID and full user card
	go func() {
		s.rdb.Set(context.Background(), nameKey, u.ID.String(), 5*time.Minute)
		data, _ := json.Marshal(u)
		cardKey := fmt.Sprintf("user:card:%s", u.ID)
		s.rdb.Set(context.Background(), cardKey, data, 5*time.Minute)
	}()

	return u, nil
}

// UpdateUser updates profile and invalidates cache.
func (s *Service) UpdateUser(ctx context.Context, id uuid.UUID, displayName, bio string, avatarMediaID, coverMediaID *uuid.UUID, firstName, lastName, gender, username, category, profession, website, location *string, dob *time.Time) (*store.User, error) {
	u, err := s.store.UpdateUser(ctx, id, displayName, bio, avatarMediaID, coverMediaID, firstName, lastName, gender, username, category, profession, website, location, dob)
	if err != nil {
		return nil, err
	}

	// Invalidate caches
	cardKey := fmt.Sprintf("user:card:%s", id)
	s.rdb.Del(ctx, cardKey)
	if username != nil {
		nameKey := fmt.Sprintf("user:name:%s", *username)
		s.rdb.Del(ctx, nameKey)
	}

	return u, nil
}

// GetUserLinks returns user's social/external links.
func (s *Service) GetUserLinks(ctx context.Context, userID uuid.UUID) ([]store.UserLink, error) {
	return s.store.GetUserLinks(ctx, userID)
}

// UpdateUserLinks replaces all links for a user.
func (s *Service) UpdateUserLinks(ctx context.Context, userID uuid.UUID, links []store.UserLink) error {
	return s.store.UpsertUserLinks(ctx, userID, links)
}

// --- About ---

// ViewerAccess represents the viewer's access level for privacy filtering.
type ViewerAccess struct {
	IsSelf     bool
	IsFollower bool
	IsFriend   bool
}

// GetAllAbout returns all about items, filtered by viewer access.
func (s *Service) GetAllAbout(ctx context.Context, userID uuid.UUID, access ViewerAccess) (map[string][]store.AboutItem, error) {
	all, err := s.store.GetAllAbout(ctx, userID)
	if err != nil {
		return nil, err
	}
	return filterAboutMap(all, access), nil
}

// GetAboutSection returns about items for a section, filtered by viewer access.
func (s *Service) GetAboutSection(ctx context.Context, userID uuid.UUID, section string, access ViewerAccess) ([]store.AboutItem, error) {
	items, err := s.store.GetAboutSection(ctx, userID, section)
	if err != nil {
		return nil, err
	}
	return filterAboutItems(items, access), nil
}

// UpsertAboutItem creates or updates an about item (owner only).
func (s *Service) UpsertAboutItem(ctx context.Context, item *store.AboutItem) (*store.AboutItem, error) {
	return s.store.UpsertAboutItem(ctx, item)
}

// DeleteAboutItem removes an about item (owner only).
func (s *Service) DeleteAboutItem(ctx context.Context, userID uuid.UUID, section string, itemID uuid.UUID) error {
	return s.store.DeleteAboutItem(ctx, userID, section, itemID)
}

func filterAboutMap(all map[string][]store.AboutItem, access ViewerAccess) map[string][]store.AboutItem {
	result := make(map[string][]store.AboutItem, len(all))
	for section, items := range all {
		filtered := filterAboutItems(items, access)
		if len(filtered) > 0 {
			result[section] = filtered
		}
	}
	return result
}

func filterAboutItems(items []store.AboutItem, access ViewerAccess) []store.AboutItem {
	if access.IsSelf {
		return items
	}
	var filtered []store.AboutItem
	for _, item := range items {
		if canView(item.Visibility, access) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func canView(visibility string, access ViewerAccess) bool {
	switch visibility {
	case "public":
		return true
	case "followers":
		return access.IsFollower || access.IsFriend
	case "friends":
		return access.IsFriend
	case "private":
		return false
	default:
		return true
	}
}

// GetSettings returns user privacy settings.
func (s *Service) GetSettings(ctx context.Context, id uuid.UUID) (*store.UserSettings, error) {
	return s.store.GetSettings(ctx, id)
}

// UpdateSettings updates user privacy settings.
func (s *Service) UpdateSettings(ctx context.Context, settings *store.UserSettings) (*store.UserSettings, error) {
	return s.store.UpdateSettings(ctx, settings)
}

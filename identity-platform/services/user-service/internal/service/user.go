package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/identity-platform/user-service/internal/config"
	"github.com/identity-platform/user-service/internal/store"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	store *store.Store
	rdb   *redis.Client
	cfg   *config.Config
	log   *slog.Logger
}

func New(s *store.Store, rdb *redis.Client, cfg *config.Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: s, rdb: rdb, cfg: cfg, log: logger}
}

// CreateUser handles user creation from UserRegistered event.
func (s *Service) CreateUser(ctx context.Context, id uuid.UUID) error {
	return s.store.CreateUser(ctx, id)
}

// GetUser returns core user record.
func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (*store.User, error) {
	cacheKey := fmt.Sprintf("user:core:%s", id)
	if cached, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil {
		var u store.User
		if jsonErr := json.Unmarshal([]byte(cached), &u); jsonErr == nil {
			return &u, nil
		} else {
			s.log.Warn("failed to unmarshal user cache", "err", jsonErr, "cache_key", cacheKey)
		}
	} else if err != nil && err != redis.Nil {
		s.log.Warn("failed to read user cache", "err", err, "cache_key", cacheKey)
	}

	u, err := s.store.GetUser(ctx, id)
	if err != nil || u == nil {
		return u, err
	}

	go func() {
		data, err := json.Marshal(u)
		if err != nil {
			s.log.Warn("failed to marshal user cache", "err", err, "cache_key", cacheKey)
			return
		}
		if err := s.rdb.Set(context.Background(), cacheKey, data, s.cfg.CacheTTL).Err(); err != nil {
			s.log.Warn("failed to set user cache", "err", err, "cache_key", cacheKey)
		}
	}()

	return u, nil
}

// ListUsers returns all active users with pagination.
func (s *Service) ListUsers(ctx context.Context, limit, offset int) ([]store.User, int, error) {
	return s.store.ListUsers(ctx, limit, offset)
}

// GetSettings returns user privacy settings.
func (s *Service) GetSettings(ctx context.Context, id uuid.UUID) (*store.UserSettings, error) {
	cacheKey := fmt.Sprintf("user:settings:%s", id)
	if cached, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil {
		var us store.UserSettings
		if jsonErr := json.Unmarshal([]byte(cached), &us); jsonErr == nil {
			return &us, nil
		} else {
			s.log.Warn("failed to unmarshal settings cache", "err", jsonErr, "cache_key", cacheKey)
		}
	} else if err != nil && err != redis.Nil {
		s.log.Warn("failed to read settings cache", "err", err, "cache_key", cacheKey)
	}

	us, err := s.store.GetSettings(ctx, id)
	if err != nil || us == nil {
		return us, err
	}

	go func() {
		data, err := json.Marshal(us)
		if err != nil {
			s.log.Warn("failed to marshal settings cache", "err", err, "cache_key", cacheKey)
			return
		}
		if err := s.rdb.Set(context.Background(), cacheKey, data, s.cfg.CacheTTL).Err(); err != nil {
			s.log.Warn("failed to set settings cache", "err", err, "cache_key", cacheKey)
		}
	}()

	return us, nil
}

// UpdateSettings updates user privacy settings.
func (s *Service) UpdateSettings(ctx context.Context, settings *store.UserSettings) (*store.UserSettings, error) {
	us, err := s.store.UpdateSettings(ctx, settings)
	if err != nil {
		return nil, err
	}

	cacheKey := fmt.Sprintf("user:settings:%s", settings.UserID)
	if err := s.rdb.Del(ctx, cacheKey).Err(); err != nil {
		s.log.Warn("failed to delete settings cache", "err", err, "cache_key", cacheKey)
	}

	return us, nil
}

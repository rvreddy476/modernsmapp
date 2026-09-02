package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/identity-user-service/internal/config"
	"github.com/atpost/identity-user-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ErrInvalidPrivacySetting is returned by UpdateSettings when a privacy enum
// field carries a value outside its allowed set (spec §5.2). The HTTP layer
// maps this to 400.
var ErrInvalidPrivacySetting = errors.New("invalid privacy setting value")

// allowedPrivacyValues lists the accepted enum values per spec §5.2 for each
// privacy field. account_visibility and allow_comments_from are now validated
// here too (Module 3 — private accounts are a real, enforced feature); the
// one remaining legacy column, allow_messages_from, is intentionally not
// validated (WhoCanMessage is the authoritative messaging field).
var allowedPrivacyValues = map[string]map[string]bool{
	// Module 3: "private" is accepted and enforced. graph-service consumes
	// the settings-changed event (which carries the new value) to gate
	// follows and auto-accept pending requests on private→public.
	"account_visibility": {
		"public": true, "private": true,
	},
	"allow_comments_from": {
		"everyone": true, "friends": true,
	},
	"who_can_message": {
		"no_one": true, "connections_only": true, "connections_and_mutual_followers": true,
		"followers_message_requests": true, "everyone_message_requests": true,
		// Production chat pass (directive §3.2): friends-of-friends may open a
		// MESSAGE REQUEST, never a direct thread. Resolved by graph-service
		// second-degree facts — the client never sees adjacency lists.
		"friends_of_friends_requests": true,
	},
	"who_can_send_connection_request": {
		"no_one": true, "friends_of_friends_or_contacts": true, "friends_of_friends": true,
		"contacts_only": true, "everyone": true,
	},
	"who_can_call": {
		"no_one": true, "connections_only": true, "accepted_chats_only": true,
	},
	"who_can_add_to_groups": {
		"no_one": true, "connections_only": true, "connections_and_contacts": true,
		"everyone_with_approval": true,
		// Production chat pass: friends-of-friends may INVITE (consent
		// required); direct add stays connections-only.
		"friends_of_friends_invites": true,
	},
	// Chat availability is a master switch, not a visibility value.
	"chat_availability": {
		"enabled": true, "paused": true,
	},
	// The four visibility fields share one enum set.
	"visibility": {
		"everyone": true, "connections_only": true, "no_one": true,
	},
}

// validatePrivacySettings rejects any enum field with an out-of-range value.
func validatePrivacySettings(s *store.UserSettings) error {
	checks := []struct{ field, value string }{
		{"account_visibility", s.AccountVisibility},
		{"allow_comments_from", s.AllowCommentsFrom},
		{"who_can_message", s.WhoCanMessage},
		{"who_can_send_connection_request", s.WhoCanSendConnectionRequest},
		{"who_can_call", s.WhoCanCall},
		{"who_can_add_to_groups", s.WhoCanAddToGroups},
		{"chat_availability", s.ChatAvailability},
		{"visibility", s.WhoCanSeeOnlineStatus},
		{"visibility", s.WhoCanSeeReadReceipts},
		{"visibility", s.WhoCanSeeLastSeen},
		{"visibility", s.WhoCanSeeProfilePhoto},
	}
	for _, c := range checks {
		if !allowedPrivacyValues[c.field][c.value] {
			return fmt.Errorf("%w: %q", ErrInvalidPrivacySetting, c.value)
		}
	}
	return nil
}

// applyStrictMode forces the strictest values across the matrix when the user
// is in Strict Privacy Mode or is a minor (spec §5.4 — under-18 is server-
// enforced and cannot be loosened by the user).
func applyStrictMode(s *store.UserSettings) {
	if !s.StrictPrivacyMode && !s.Under18Mode {
		return
	}
	s.WhoCanMessage = "connections_only"
	s.WhoCanCall = "no_one"
	s.WhoCanAddToGroups = "no_one"
	s.WhoCanSeeOnlineStatus = "connections_only"
	s.WhoCanSeeReadReceipts = "connections_only"
	s.WhoCanSeeLastSeen = "connections_only"
	s.WhoCanSeeProfilePhoto = "connections_only"
	s.BlockUnknownCalls = true
	s.AutoFilterAbusiveContent = true
	s.AllowPhoneDiscovery = false
	s.DiscoverableByPhoneToContacts = false
}

// SettingsChangedPublisher announces committed writes to downstream
// permission caches. Implemented by events.Producer; nil-safe by contract.
//
// PublishSettingsChanged carries accountVisibility (the NEW value) so
// graph-service can act on a private→public flip — auto-accepting pending
// follow requests — without a synchronous read-back.
type SettingsChangedPublisher interface {
	PublishSettingsChanged(ctx context.Context, userID uuid.UUID, privacyVersion int, accountVisibility string)
	PublishModulesChanged(ctx context.Context, userID uuid.UUID, modules []string, homeModule string)
}

type Service struct {
	store    *store.Store
	rdb      *redis.Client
	cfg      *config.Config
	log      *slog.Logger
	producer SettingsChangedPublisher
}

func New(s *store.Store, rdb *redis.Client, cfg *config.Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: s, rdb: rdb, cfg: cfg, log: logger}
}

// WithProducer wires the settings-changed publisher. Optional — without it,
// downstream caches rely on their TTLs alone. Call once from cmd/server.
func (s *Service) WithProducer(p SettingsChangedPublisher) *Service {
	s.producer = p
	return s
}

// CreateUser handles user creation from a UserRegistered event. under18 is
// derived from the registrant's date of birth and pins the account into the
// minor-safety profile (spec §5.4, §11.1).
func (s *Service) CreateUser(ctx context.Context, id uuid.UUID, under18 bool) error {
	return s.store.CreateUser(ctx, id, under18)
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

// UpdateSettings validates and persists privacy settings. Strict Privacy Mode
// and under-18 status clamp the matrix to strict values before the write.
func (s *Service) UpdateSettings(ctx context.Context, settings *store.UserSettings) (*store.UserSettings, error) {
	applyStrictMode(settings)
	if err := validatePrivacySettings(settings); err != nil {
		return nil, err
	}

	us, err := s.store.UpdateSettings(ctx, settings)
	if err != nil {
		return nil, err
	}

	cacheKey := fmt.Sprintf("user:settings:%s", settings.UserID)
	if err := s.rdb.Del(ctx, cacheKey).Err(); err != nil {
		s.log.Warn("failed to delete settings cache", "err", err, "cache_key", cacheKey)
	}

	// Invalidation signal for downstream permission caches (graph privacy
	// cache, chat policy projection). Best-effort AFTER the committed write;
	// consumers re-fetch the snapshot, so a duplicate or late event is
	// harmless and a lost one is bounded by their TTL fallbacks.
	if s.producer != nil && us != nil {
		s.producer.PublishSettingsChanged(ctx, us.UserID, us.PrivacyVersion, us.AccountVisibility)
	}

	return us, nil
}

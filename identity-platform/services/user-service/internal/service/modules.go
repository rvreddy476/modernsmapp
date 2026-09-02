package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/identity-user-service/internal/store"
	"github.com/google/uuid"
)

// Module 3 — module preferences (privacy-first, server-driven).
//
// The server owns the vocabulary: a client can only choose from the module
// names the platform ships, and the home surface must be either the feed or
// one of the modules the user actually chose. No row means "defaults" — all
// modules on, home is the feed, onboarding not completed — and GET returns
// that shape rather than 404, so a fresh account is indistinguishable from
// one that explicitly chose everything.

// ErrInvalidModule is returned when a requested module name is not one the
// platform ships. The HTTP layer maps it to 400 INVALID_MODULE.
var ErrInvalidModule = errors.New("unknown module")

// ErrInvalidHomeModule is returned when home_module is neither 'feed' nor one
// of the chosen modules. The HTTP layer maps it to 400 INVALID_HOME_MODULE.
var ErrInvalidHomeModule = errors.New("invalid home module")

// ErrInvalidRegion is returned when a region code is not two ASCII letters
// (ISO-3166-1 alpha-2 style). The HTTP layer maps it to 400 INVALID_REGION.
var ErrInvalidRegion = errors.New("invalid region code")

// HomeModuleFeed is always a valid home surface, chosen modules or not.
const HomeModuleFeed = "feed"

// knownModules is the closed set of optional modules, in the order the
// defaults are served. Must match the CHECK constraint on
// usr.module_preferences in database/setup.sql.
var knownModules = []string{"reels", "commerce", "chat", "dating", "food", "qa", "posttube"}

var knownModuleSet = func() map[string]bool {
	m := make(map[string]bool, len(knownModules))
	for _, name := range knownModules {
		m[name] = true
	}
	return m
}()

// defaultModulePreferences is the no-row shape: every module on, home is the
// feed, onboarding not completed. UpdatedAt is stamped at read time — there
// is no stored row to date it from.
func defaultModulePreferences(userID uuid.UUID) *store.ModulePreferences {
	modules := make([]string, len(knownModules))
	copy(modules, knownModules)
	return &store.ModulePreferences{
		UserID:                userID,
		Modules:               modules,
		HomeModule:            HomeModuleFeed,
		OnboardingCompletedAt: nil,
		UpdatedAt:             time.Now().UTC(),
	}
}

// normalizeModulePreferences trims, lowercases, dedupes (first occurrence
// wins, order preserved) and validates the selection.
//
//   - any module outside knownModules → ErrInvalidModule
//   - home must be HomeModuleFeed or one of the CHOSEN modules → else
//     ErrInvalidHomeModule (an empty home defaults to the feed)
func normalizeModulePreferences(modules []string, home string) ([]string, string, error) {
	seen := make(map[string]bool, len(modules))
	normalized := make([]string, 0, len(modules))
	for _, raw := range modules {
		name := strings.ToLower(strings.TrimSpace(raw))
		if !knownModuleSet[name] {
			return nil, "", fmt.Errorf("%w: %q", ErrInvalidModule, raw)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		normalized = append(normalized, name)
	}

	home = strings.ToLower(strings.TrimSpace(home))
	if home == "" {
		home = HomeModuleFeed
	}
	if home != HomeModuleFeed && !seen[home] {
		return nil, "", fmt.Errorf("%w: %q is not 'feed' or one of the chosen modules", ErrInvalidHomeModule, home)
	}
	return normalized, home, nil
}

// GetModulePreferences returns the stored selection, or the defaults when the
// user has never written one.
func (s *Service) GetModulePreferences(ctx context.Context, userID uuid.UUID) (*store.ModulePreferences, error) {
	p, err := s.store.GetModulePreferences(ctx, userID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return defaultModulePreferences(userID), nil
	}
	return p, nil
}

// UpdateModulePreferences validates and persists the selection, then emits
// user.modules_changed so downstream surfaces (home composition, gateways)
// can react without polling.
func (s *Service) UpdateModulePreferences(ctx context.Context, userID uuid.UUID, modules []string, homeModule string, completeOnboarding bool) (*store.ModulePreferences, error) {
	normalized, home, err := normalizeModulePreferences(modules, homeModule)
	if err != nil {
		return nil, err
	}

	p, err := s.store.UpsertModulePreferences(ctx, userID, normalized, home, completeOnboarding)
	if err != nil {
		return nil, err
	}

	if s.producer != nil && p != nil {
		s.producer.PublishModulesChanged(ctx, userID, p.Modules, p.HomeModule)
	}
	return p, nil
}

// validRegionCode reports whether code is two ASCII letters (ISO-3166-1
// alpha-2 style). Case-insensitive; the caller stores the uppercase form.
func validRegionCode(code string) bool {
	if len(code) != 2 {
		return false
	}
	for _, r := range code {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// SetRegion validates, normalises to uppercase, and stores the user's region.
// The settings cache is invalidated because region is embedded in settings
// reads.
func (s *Service) SetRegion(ctx context.Context, userID uuid.UUID, countryCode string) (string, error) {
	code := strings.TrimSpace(countryCode)
	if !validRegionCode(code) {
		return "", fmt.Errorf("%w: %q", ErrInvalidRegion, countryCode)
	}
	code = strings.ToUpper(code)

	region, err := s.store.SetRegion(ctx, userID, code)
	if err != nil {
		return "", err
	}

	cacheKey := fmt.Sprintf("user:settings:%s", userID)
	if err := s.rdb.Del(ctx, cacheKey).Err(); err != nil {
		s.log.Warn("failed to delete settings cache after region write", "err", err, "cache_key", cacheKey)
	}
	return region, nil
}

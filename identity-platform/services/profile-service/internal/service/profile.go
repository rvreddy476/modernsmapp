package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/atpost/identity-profile-service/internal/config"
	"github.com/atpost/identity-profile-service/internal/events"
	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	store    *store.Store
	rdb      *redis.Client
	producer *events.Producer
	cfg      *config.Config
	log      *slog.Logger
}

func New(s *store.Store, rdb *redis.Client, producer *events.Producer, cfg *config.Config, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: s, rdb: rdb, producer: producer, cfg: cfg, log: logger}
}

// CreateProfile handles profile creation from UserRegistered event.
func (s *Service) CreateProfile(ctx context.Context, userID uuid.UUID, firstName, lastName, dob, gender string) error {
	displayName := firstName + " " + lastName
	if displayName == " " {
		displayName = "User " + userID.String()[:8]
	}
	return s.store.CreateProfile(ctx, userID, displayName, firstName, lastName, dob, gender)
}

// ---------------------------------------------------------------
// Profile read / update
// ---------------------------------------------------------------

// GetProfile returns user profile, trying cache first.
func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (*store.Profile, error) {
	cacheKey := fmt.Sprintf("profile:card:%s", userID)
	val, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var p store.Profile
		if err := json.Unmarshal([]byte(val), &p); err == nil {
			return &p, nil
		}
		s.log.Warn("failed to unmarshal profile cache", "err", err, "cache_key", cacheKey)
	} else if err != nil && err != redis.Nil {
		s.log.Warn("failed to read profile cache", "err", err, "cache_key", cacheKey)
	}

	p, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}

	s.cacheProfile(cacheKey, p)
	return p, nil
}

// GetProfileByUsername looks up by username.
func (s *Service) GetProfileByUsername(ctx context.Context, username string) (*store.Profile, error) {
	cacheKey := fmt.Sprintf("profile:name:%s", username)
	val, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var p store.Profile
		if err := json.Unmarshal([]byte(val), &p); err == nil {
			return &p, nil
		}
	}

	p, err := s.store.GetProfileByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}

	s.cacheProfile(cacheKey, p)
	return p, nil
}

// ListProfiles returns a paginated list of all profiles.
func (s *Service) ListProfiles(ctx context.Context, limit, offset int) ([]store.Profile, int64, error) {
	return s.store.ListProfiles(ctx, limit, offset)
}

// ListProfilesChangedSince returns profiles updated at or after `since`,
// oldest-first — the feed for downstream projection reconcile jobs.
func (s *Service) ListProfilesChangedSince(ctx context.Context, since time.Time, limit int) ([]store.Profile, error) {
	return s.store.ListProfilesChangedSince(ctx, since, limit)
}

// UpdateProfile updates profile and invalidates cache.
func (s *Service) UpdateProfile(ctx context.Context, userID uuid.UUID, params store.UpdateProfileParams) (*store.Profile, error) {
	// Strip leading "@" from username — it's a display convention, not part of the stored value.
	if params.Username != nil && strings.HasPrefix(*params.Username, "@") {
		trimmed := strings.TrimPrefix(*params.Username, "@")
		params.Username = &trimmed
	}

	p, err := s.store.UpdateProfile(ctx, userID, params)
	if err != nil {
		return nil, err
	}

	// Invalidate caches
	s.rdb.Del(ctx, fmt.Sprintf("profile:card:%s", userID))
	if p.Username != nil {
		s.rdb.Del(ctx, fmt.Sprintf("profile:name:%s", *p.Username))
	}

	// Publish profile updated event
	var fnPtr, lnPtr string
	if params.FirstName != nil {
		fnPtr = *params.FirstName
	}
	if params.LastName != nil {
		lnPtr = *params.LastName
	}
	if err := s.producer.PublishUserProfileUpdated(ctx, userID, p.DisplayName, p.Bio, p.AvatarMediaID, fnPtr, lnPtr); err != nil {
		s.log.Warn("failed to publish profile updated event", "err", err, "user_id", userID)
	}

	return p, nil
}

// ---------------------------------------------------------------
// User Links
// ---------------------------------------------------------------

func (s *Service) GetUserLinks(ctx context.Context, userID uuid.UUID) ([]store.UserLink, error) {
	return s.store.GetUserLinks(ctx, userID)
}

func (s *Service) UpsertUserLinks(ctx context.Context, userID uuid.UUID, links []store.UserLink) error {
	return s.store.UpsertUserLinks(ctx, userID, links)
}

// ---------------------------------------------------------------
// User About
// ---------------------------------------------------------------

func (s *Service) GetAllAbout(ctx context.Context, userID uuid.UUID) ([]store.AboutItem, error) {
	return s.store.GetAllAbout(ctx, userID)
}

func (s *Service) GetAboutBySection(ctx context.Context, userID uuid.UUID, section string) ([]store.AboutItem, error) {
	return s.store.GetAboutBySection(ctx, userID, section)
}

func (s *Service) UpsertAboutItem(ctx context.Context, item *store.AboutItem) (*store.AboutItem, error) {
	return s.store.UpsertAboutItem(ctx, item)
}

func (s *Service) DeleteAboutItem(ctx context.Context, userID uuid.UUID, section string, itemID uuid.UUID) error {
	return s.store.DeleteAboutItem(ctx, userID, section, itemID)
}

// ---------------------------------------------------------------
// Avatar / Cover
// ---------------------------------------------------------------

// UpdateAvatar sets the avatar media ID and invalidates cache.
func (s *Service) UpdateAvatar(ctx context.Context, userID uuid.UUID, mediaID uuid.UUID) error {
	if err := s.store.UpdateAvatar(ctx, userID, mediaID); err != nil {
		return err
	}
	s.rdb.Del(ctx, fmt.Sprintf("profile:card:%s", userID))
	return nil
}

// UpdateCover sets the cover media ID and invalidates cache.
func (s *Service) UpdateCover(ctx context.Context, userID uuid.UUID, mediaID uuid.UUID) error {
	if err := s.store.UpdateCover(ctx, userID, mediaID); err != nil {
		return err
	}
	s.rdb.Del(ctx, fmt.Sprintf("profile:card:%s", userID))
	return nil
}

// ---------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------

func (s *Service) cacheProfile(key string, p *store.Profile) {
	go func() {
		data, err := json.Marshal(p)
		if err != nil {
			s.log.Warn("failed to marshal profile cache", "err", err, "cache_key", key)
			return
		}
		if err := s.rdb.Set(context.Background(), key, data, s.cfg.CacheTTL).Err(); err != nil {
			s.log.Warn("failed to set profile cache", "err", err, "cache_key", key)
		}
	}()
}

// GetProfilesBatch returns profiles for up to 100 user IDs as a map keyed by user ID.
func (s *Service) GetProfilesBatch(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]*store.Profile, error) {
	if len(userIDs) > 100 {
		userIDs = userIDs[:100]
	}
	profiles, err := s.store.GetProfilesByIDs(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	result := make(map[uuid.UUID]*store.Profile, len(profiles))
	for i := range profiles {
		result[profiles[i].UserID] = &profiles[i]
	}
	return result, nil
}

// ---------------------------------------------------------------
// Profile Links (new table)
// ---------------------------------------------------------------

func (s *Service) GetProfileLinks(ctx context.Context, profileID uuid.UUID) ([]store.ProfileLink, error) {
	return s.store.GetProfileLinks(ctx, profileID)
}

func (s *Service) CreateProfileLink(ctx context.Context, link *store.ProfileLink) (*store.ProfileLink, error) {
	return s.store.CreateProfileLink(ctx, link)
}

func (s *Service) UpdateProfileLink(ctx context.Context, linkID, profileID uuid.UUID, title, url string, icon, category *string, sortOrder int, isPinned bool, visibility string) (*store.ProfileLink, error) {
	return s.store.UpdateProfileLink(ctx, linkID, profileID, title, url, icon, category, sortOrder, isPinned, visibility)
}

func (s *Service) DeleteProfileLink(ctx context.Context, linkID, profileID uuid.UUID) error {
	return s.store.DeleteProfileLink(ctx, linkID, profileID)
}

func (s *Service) IncrementLinkClick(ctx context.Context, linkID uuid.UUID) error {
	return s.store.IncrementLinkClick(ctx, linkID)
}

// ---------------------------------------------------------------
// Follows
// ---------------------------------------------------------------

func (s *Service) FollowUser(ctx context.Context, followerID, followingID uuid.UUID) (*store.Follow, error) {
	if followerID == followingID {
		return nil, errors.New("cannot follow yourself")
	}

	// Check if either user has blocked the other
	blocked, err := s.store.GetBlockBidirectional(ctx, followerID, followingID)
	if err != nil {
		return nil, fmt.Errorf("failed to check block status: %w", err)
	}
	if blocked {
		return nil, errors.New("cannot follow this user")
	}

	f, changed, err := s.store.CreateFollow(ctx, followerID, followingID)
	if err != nil {
		return nil, err
	}

	// Audit UC2: only bump denormalized counters when the follow edge
	// actually changed state (new insert or re-activation). Previously
	// the increments fired unconditionally so a duplicate or re-follow
	// drifted counters upward.
	if changed {
		if err := s.store.IncrementFollowingCount(ctx, followerID); err != nil {
			s.log.Warn("failed to increment following_count", "err", err, "user_id", followerID)
		}
		if err := s.store.IncrementFollowerCount(ctx, followingID); err != nil {
			s.log.Warn("failed to increment follower_count", "err", err, "user_id", followingID)
		}
	}

	// Invalidate caches
	s.rdb.Del(ctx, fmt.Sprintf("profile:card:%s", followerID))
	s.rdb.Del(ctx, fmt.Sprintf("profile:card:%s", followingID))

	// Update Redis follow sets
	s.updateFollowSetsAdd(ctx, followerID, followingID)

	// Mutual follow detection: check if the followed user already follows back
	reverseFollow, _ := s.store.GetFollowStatus(ctx, followingID, followerID)
	isMutual := reverseFollow != nil && reverseFollow.Status == "active"

	// Publish event (best-effort)
	if err := s.producer.PublishFollowCreated(ctx, followerID, followingID); err != nil {
		s.log.Warn("failed to publish follow created event", "err", err, "follower_id", followerID, "following_id", followingID)
	}

	if isMutual {
		s.log.Info("mutual follow detected", "user_a", followerID, "user_b", followingID)
		// Mutual follows are a strong signal for circle suggestion boost.
		// The event consumers can use the follow.created event to detect this
		// by checking the reverse follow. Future: publish a dedicated mutual_follow event.
	}

	return f, nil
}

func (s *Service) UnfollowUser(ctx context.Context, followerID, followingID uuid.UUID) error {
	if err := s.store.DeleteFollow(ctx, followerID, followingID); err != nil {
		return err
	}

	if err := s.store.DecrementFollowingCount(ctx, followerID); err != nil {
		s.log.Warn("failed to decrement following_count", "err", err, "user_id", followerID)
	}
	if err := s.store.DecrementFollowerCount(ctx, followingID); err != nil {
		s.log.Warn("failed to decrement follower_count", "err", err, "user_id", followingID)
	}

	s.rdb.Del(ctx, fmt.Sprintf("profile:card:%s", followerID))
	s.rdb.Del(ctx, fmt.Sprintf("profile:card:%s", followingID))

	// Update Redis follow sets
	s.updateFollowSetsRemove(ctx, followerID, followingID)

	// Publish event (best-effort)
	if err := s.producer.PublishFollowDeleted(ctx, followerID, followingID); err != nil {
		s.log.Warn("failed to publish follow deleted event", "err", err, "follower_id", followerID, "following_id", followingID)
	}

	return nil
}

// Friend system retired — see graph-service connections; profile.friendships kept dormant for backfill

// ---------------------------------------------------------------
// Social Lists
// ---------------------------------------------------------------

// ListFollowers returns users who follow the given userID.
func (s *Service) ListFollowers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FollowerEntry, int64, error) {
	return s.store.ListFollowers(ctx, userID, limit, offset)
}

// ListFollowing returns users that the given userID follows.
func (s *Service) ListFollowing(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.FollowerEntry, int64, error) {
	return s.store.ListFollowing(ctx, userID, limit, offset)
}

// ListFollowersCursor / ListFollowingCursor — keyset pagination, used
// when the caller passes ?cursor= or ?paginate=cursor. Stays O(log n)
// at celebrity scale; the legacy offset path scans linearly past the
// offset, which dies past ~OFFSET 10000.
func (s *Service) ListFollowersCursor(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]store.FollowerEntry, string, error) {
	return s.store.ListFollowersCursor(ctx, userID, limit, cursor)
}

func (s *Service) ListFollowingCursor(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]store.FollowerEntry, string, error) {
	return s.store.ListFollowingCursor(ctx, userID, limit, cursor)
}

// Friend system retired — see graph-service connections; profile.friendships kept dormant for backfill

// ListBlocks returns users blocked by the given userID.
func (s *Service) ListBlocks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.Block, int64, error) {
	return s.store.ListBlocks(ctx, userID, limit, offset)
}

// ---------------------------------------------------------------
// Block / Unblock
// ---------------------------------------------------------------

// BlockUser creates a block and auto-unfollows in both directions.
func (s *Service) BlockUser(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	if blockerID == blockedID {
		return errors.New("cannot block yourself")
	}

	// Create block record
	if err := s.store.CreateBlock(ctx, blockerID, blockedID); err != nil {
		return fmt.Errorf("failed to create block: %w", err)
	}

	// Determine which follow directions exist before deleting, so we can adjust counts correctly
	blockerFollowsBlocked, blockedFollowsBlocker, err := s.store.CountFollowsBetween(ctx, blockerID, blockedID)
	if err != nil {
		s.log.Warn("failed to check follows between users", "err", err, "blocker_id", blockerID, "blocked_id", blockedID)
	}

	// Delete any follows in both directions
	if _, err := s.store.DeleteFollowsBetween(ctx, blockerID, blockedID); err != nil {
		s.log.Warn("failed to delete follows between users", "err", err, "blocker_id", blockerID, "blocked_id", blockedID)
	}

	// Adjust denormalized counts
	if blockerFollowsBlocked {
		if err := s.store.DecrementFollowingCount(ctx, blockerID); err != nil {
			s.log.Warn("failed to decrement following_count", "err", err, "user_id", blockerID)
		}
		if err := s.store.DecrementFollowerCount(ctx, blockedID); err != nil {
			s.log.Warn("failed to decrement follower_count", "err", err, "user_id", blockedID)
		}
	}
	if blockedFollowsBlocker {
		if err := s.store.DecrementFollowingCount(ctx, blockedID); err != nil {
			s.log.Warn("failed to decrement following_count", "err", err, "user_id", blockedID)
		}
		if err := s.store.DecrementFollowerCount(ctx, blockerID); err != nil {
			s.log.Warn("failed to decrement follower_count", "err", err, "user_id", blockerID)
		}
	}

	// Friend system retired — see graph-service connections; profile.friendships kept dormant for backfill

	// Invalidate caches
	s.rdb.Del(ctx, fmt.Sprintf("profile:card:%s", blockerID))
	s.rdb.Del(ctx, fmt.Sprintf("profile:card:%s", blockedID))

	// Clean up Redis follow sets
	s.updateFollowSetsRemove(ctx, blockerID, blockedID)
	s.updateFollowSetsRemove(ctx, blockedID, blockerID)

	// Publish event (best-effort)
	if err := s.producer.PublishUserBlocked(ctx, blockerID, blockedID); err != nil {
		s.log.Warn("failed to publish user blocked event", "err", err, "blocker_id", blockerID, "blocked_id", blockedID)
	}

	return nil
}

// UnblockUser removes a block relationship.
func (s *Service) UnblockUser(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	if err := s.store.DeleteBlock(ctx, blockerID, blockedID); err != nil {
		return fmt.Errorf("failed to delete block: %w", err)
	}

	// Publish event (best-effort)
	if err := s.producer.PublishUserUnblocked(ctx, blockerID, blockedID); err != nil {
		s.log.Warn("failed to publish user unblocked event", "err", err, "blocker_id", blockerID, "blocked_id", blockedID)
	}

	return nil
}

// ---------------------------------------------------------------
// Relationship
// ---------------------------------------------------------------

// GetRelationship returns the full relationship state between viewerID and targetID.
func (s *Service) GetRelationship(ctx context.Context, viewerID, targetID uuid.UUID) (*store.RelationshipStatus, error) {
	return s.store.GetRelationship(ctx, viewerID, targetID)
}

// ---------------------------------------------------------------
// Redis Follow / Circle Set Helpers
// ---------------------------------------------------------------

func (s *Service) updateFollowSetsAdd(ctx context.Context, followerID, followingID uuid.UUID) {
	go func() {
		bgCtx := context.Background()
		if err := s.rdb.SAdd(bgCtx, fmt.Sprintf("following:%s", followerID), followingID.String()).Err(); err != nil {
			s.log.Warn("failed to SADD following set", "err", err)
		}
		if err := s.rdb.SAdd(bgCtx, fmt.Sprintf("followers:%s", followingID), followerID.String()).Err(); err != nil {
			s.log.Warn("failed to SADD followers set", "err", err)
		}
	}()
}

func (s *Service) updateFollowSetsRemove(ctx context.Context, followerID, followingID uuid.UUID) {
	go func() {
		bgCtx := context.Background()
		if err := s.rdb.SRem(bgCtx, fmt.Sprintf("following:%s", followerID), followingID.String()).Err(); err != nil {
			s.log.Warn("failed to SREM following set", "err", err)
		}
		if err := s.rdb.SRem(bgCtx, fmt.Sprintf("followers:%s", followingID), followerID.String()).Err(); err != nil {
			s.log.Warn("failed to SREM followers set", "err", err)
		}
	}()
}

// Friend system retired — see graph-service connections; profile.friendships kept dormant for backfill

// ---------------------------------------------------------------
// Module Profiles
// ---------------------------------------------------------------

// GetModuleProfile returns a single module profile for a user+module.
func (s *Service) GetModuleProfile(ctx context.Context, userID uuid.UUID, module string) (*store.ModuleProfile, error) {
	if !isValidModule(module) {
		return nil, errors.New("invalid module: must be postbook, posttube, or postgram")
	}

	cacheKey := fmt.Sprintf("module_profile:%s:%s", userID, module)
	val, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var mp store.ModuleProfile
		if err := json.Unmarshal([]byte(val), &mp); err == nil {
			return &mp, nil
		}
	}

	mp, err := s.store.GetModuleProfile(ctx, userID, module)
	if err != nil {
		return nil, err
	}
	if mp == nil {
		return nil, nil
	}

	s.cacheModuleProfile(cacheKey, mp)
	return mp, nil
}

// GetModuleProfiles returns all module profiles for a user.
func (s *Service) GetModuleProfiles(ctx context.Context, userID uuid.UUID) ([]store.ModuleProfile, error) {
	return s.store.GetModuleProfiles(ctx, userID)
}

// UpsertModuleProfile creates or updates a module profile and publishes an event.
func (s *Service) UpsertModuleProfile(ctx context.Context, userID uuid.UUID, module string, params store.UpsertModuleProfileParams) (*store.ModuleProfile, error) {
	if !isValidModule(module) {
		return nil, errors.New("invalid module: must be postbook, posttube, or postgram")
	}

	mp, err := s.store.UpsertModuleProfile(ctx, userID, module, params)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert module profile: %w", err)
	}

	// Invalidate cache
	s.rdb.Del(ctx, fmt.Sprintf("module_profile:%s:%s", userID, module))

	// Publish event
	if err := s.producer.PublishModuleProfileUpdated(ctx, userID, module); err != nil {
		s.log.Warn("failed to publish module profile updated event", "err", err, "user_id", userID, "module", module)
	}

	return mp, nil
}

// DeleteModuleProfile removes a module profile.
func (s *Service) DeleteModuleProfile(ctx context.Context, userID uuid.UUID, module string) error {
	if !isValidModule(module) {
		return errors.New("invalid module: must be postbook, posttube, or postgram")
	}

	if err := s.store.DeleteModuleProfile(ctx, userID, module); err != nil {
		return fmt.Errorf("failed to delete module profile: %w", err)
	}

	s.rdb.Del(ctx, fmt.Sprintf("module_profile:%s:%s", userID, module))
	return nil
}

// ResolveModuleIdentity returns the display name for a user in a given module.
// If use_global_identity=true (default), reads from the global profile.
// Otherwise, uses the module-specific name override.
// Media (avatar, banner, watermark) is now resolved via owner_media_slots in the media service.
func (s *Service) ResolveModuleIdentity(ctx context.Context, userID uuid.UUID, module string) (displayName string, err error) {
	mp, err := s.GetModuleProfile(ctx, userID, module)
	if err != nil {
		return "", err
	}

	// If no module profile or use_global_identity=true, fall back to global
	if mp == nil || mp.UseGlobalIdentity {
		profile, err := s.GetProfile(ctx, userID)
		if err != nil {
			return "", err
		}
		if profile == nil {
			return "", errors.New("profile not found")
		}
		return profile.DisplayName, nil
	}

	return safeString(mp.NameOverride), nil
}

func (s *Service) cacheModuleProfile(key string, mp *store.ModuleProfile) {
	go func() {
		data, err := json.Marshal(mp)
		if err != nil {
			return
		}
		s.rdb.Set(context.Background(), key, data, s.cfg.CacheTTL)
	}()
}

// ---------------------------------------------------------------
// Handle Change
// ---------------------------------------------------------------

// ChangeHandle changes the user's username with cooldown enforcement (30 days between changes).
func (s *Service) ChangeHandle(ctx context.Context, userID uuid.UUID, newUsername string) (*store.Profile, error) {
	newUsername = strings.TrimPrefix(newUsername, "@")
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return nil, errors.New("username cannot be empty")
	}

	// Get current profile
	profile, err := s.store.GetProfile(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get profile: %w", err)
	}
	if profile == nil {
		return nil, errors.New("profile not found")
	}

	oldUsername := ""
	if profile.Username != nil {
		oldUsername = *profile.Username
	}

	if oldUsername == newUsername {
		return profile, nil
	}

	// Check cooldown
	latest, err := s.store.GetLatestHandleChange(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check handle cooldown: %w", err)
	}
	if latest != nil && latest.CooldownUntil.After(time.Now()) {
		return nil, fmt.Errorf("handle change on cooldown until %s", latest.CooldownUntil.Format(time.RFC3339))
	}

	// Check if new username is taken
	existing, err := s.store.GetProfileByUsername(ctx, newUsername)
	if err != nil {
		return nil, fmt.Errorf("failed to check username availability: %w", err)
	}
	if existing != nil && existing.UserID != userID {
		return nil, errors.New("username already taken")
	}

	// Update profile username
	params := store.UpdateProfileParams{
		DisplayName: profile.DisplayName,
		Bio:         profile.Bio,
		Username:    &newUsername,
		Category:    profile.Category,
		Profession:  profile.Profession,
		Website:     profile.Website,
		Location:    profile.Location,
		ProfileThemeColor: profile.ProfileThemeColor,
	}
	updated, err := s.store.UpdateProfile(ctx, userID, params)
	if err != nil {
		return nil, fmt.Errorf("failed to update username: %w", err)
	}

	// Record handle change history
	if oldUsername != "" {
		if _, err := s.store.InsertHandleHistory(ctx, userID, oldUsername, newUsername); err != nil {
			s.log.Warn("failed to record handle history", "err", err, "user_id", userID)
		}
	}

	// Invalidate caches
	s.rdb.Del(ctx, fmt.Sprintf("profile:card:%s", userID))
	if oldUsername != "" {
		s.rdb.Del(ctx, fmt.Sprintf("profile:name:%s", oldUsername))
	}
	s.rdb.Del(ctx, fmt.Sprintf("profile:name:%s", newUsername))

	// Publish event
	if err := s.producer.PublishHandleChanged(ctx, userID, oldUsername, newUsername); err != nil {
		s.log.Warn("failed to publish handle changed event", "err", err, "user_id", userID)
	}

	return updated, nil
}

// ResolveHandle looks up the current username for an old handle (redirect support, 90-day window).
func (s *Service) ResolveHandle(ctx context.Context, oldUsername string) (*uuid.UUID, *string, error) {
	return s.store.ResolveHandle(ctx, oldUsername)
}

// GetHandleHistory returns the handle change history for a user.
func (s *Service) GetHandleHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.HandleHistoryEntry, error) {
	return s.store.GetHandleHistory(ctx, userID, limit, offset)
}

// ---------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------

// ---------------------------------------------------------------
// Profile Stats
// ---------------------------------------------------------------

// GetProfileStats returns cached aggregated stats for a user's profile.
func (s *Service) GetProfileStats(ctx context.Context, userID uuid.UUID) (*store.ProfileStats, error) {
	cacheKey := fmt.Sprintf("profile:stats:%s", userID)
	val, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var ps store.ProfileStats
		if err := json.Unmarshal([]byte(val), &ps); err == nil {
			return &ps, nil
		}
	}

	ps, err := s.store.GetProfileStats(ctx, userID)
	if err != nil {
		return nil, err
	}
	if ps == nil {
		// If no stats row exists, recalculate from profile
		ps, err = s.store.RecalculateProfileStats(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to recalculate profile stats: %w", err)
		}
	}

	// Cache for 5 minutes
	go func() {
		data, err := json.Marshal(ps)
		if err != nil {
			return
		}
		s.rdb.Set(context.Background(), cacheKey, data, 5*time.Minute)
	}()

	return ps, nil
}

// IncrementProfileStat increments a stat field and invalidates cache.
func (s *Service) IncrementProfileStat(ctx context.Context, userID uuid.UUID, field string, delta int) error {
	if err := s.store.IncrementProfileStat(ctx, userID, field, delta); err != nil {
		return err
	}
	s.rdb.Del(ctx, fmt.Sprintf("profile:stats:%s", userID))
	return nil
}

// RecalculateProfileStats recounts from source tables and updates cache.
func (s *Service) RecalculateProfileStats(ctx context.Context, userID uuid.UUID) (*store.ProfileStats, error) {
	ps, err := s.store.RecalculateProfileStats(ctx, userID)
	if err != nil {
		return nil, err
	}
	s.rdb.Del(ctx, fmt.Sprintf("profile:stats:%s", userID))
	return ps, nil
}

func isValidModule(module string) bool {
	return module == "postbook" || module == "posttube" || module == "postgram"
}

func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

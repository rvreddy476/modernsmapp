package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/user-service/internal/handle"
	"github.com/atpost/user-service/internal/store"
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

// SoftDeleteUser marks the app-level user record as deleted.
func (s *Service) SoftDeleteUser(ctx context.Context, id uuid.UUID) error {
	return s.store.SoftDeleteUser(ctx, id)
}

// CreateUser handles user creation from event.
// After creating the user record it auto-provisions a handle and default
// channel so the user is ready to publish content immediately.
func (s *Service) CreateUser(ctx context.Context, id uuid.UUID, phone, email, firstName, lastName, dob, gender string) error {
	displayName := firstName + " " + lastName
	if displayName == " " {
		displayName = "User " + id.String()[:8]
	}
	if err := s.store.CreateUser(ctx, id, displayName, firstName, lastName, dob, gender); err != nil {
		return err
	}

	// Auto-create handle + default channel for the new user.
	if _, err := s.store.EnsurePublisher(ctx, id, handle.Generate); err != nil {
		// Log but don't fail user creation — channel can be provisioned later.
		fmt.Printf("warn: auto-provision publisher for %s failed: %v\n", id, err)
	}
	return nil
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

// --- Channels ---

// CreateChannel creates a new creator channel.
func (s *Service) CreateChannel(ctx context.Context, ch *store.Channel) error {
	return s.store.CreateChannel(ctx, ch)
}

// GetChannel returns a channel by handle with links and milestones.
func (s *Service) GetChannel(ctx context.Context, handle string) (*store.ChannelDetail, error) {
	ch, err := s.store.GetChannelByHandle(ctx, handle)
	if err != nil {
		return nil, err
	}
	links, err := s.store.GetChannelLinks(ctx, ch.ID)
	if err != nil {
		return nil, err
	}
	milestones, err := s.store.GetChannelMilestones(ctx, ch.ID, true)
	if err != nil {
		return nil, err
	}
	if links == nil {
		links = []store.ChannelLink{}
	}
	if milestones == nil {
		milestones = []store.ChannelMilestone{}
	}
	return &store.ChannelDetail{Channel: *ch, Links: links, Milestones: milestones}, nil
}

// UpdateChannel updates a channel's editable fields.
func (s *Service) UpdateChannel(ctx context.Context, upd *store.ChannelUpdate) error {
	return s.store.UpdateChannel(ctx, upd)
}

// DeleteChannel removes a channel.
func (s *Service) DeleteChannel(ctx context.Context, id, userID uuid.UUID) error {
	return s.store.DeleteChannel(ctx, id, userID)
}

// GetUserChannels returns all channels for a user.
func (s *Service) GetUserChannels(ctx context.Context, userID uuid.UUID) ([]store.Channel, error) {
	return s.store.GetUserChannels(ctx, userID)
}

// EnsurePublisher atomically ensures the user has a handle and a default channel.
// If both already exist, it returns them without modification.
func (s *Service) EnsurePublisher(ctx context.Context, userID uuid.UUID) (*store.EnsurePublisherResult, error) {
	return s.store.EnsurePublisher(ctx, userID, handle.Generate)
}

// --- Business Pages ---

// CreateBusinessPage creates a new business page.
func (s *Service) CreateBusinessPage(ctx context.Context, p *store.BusinessPage) error {
	return s.store.CreateBusinessPage(ctx, p)
}

// GetBusinessPage returns a business page by handle OR UUID, with optional viewer follow status.
// Accepting both forms keeps the public `:id` route flexible: clients can deep-link by handle
// while internal flows (e.g. onboarding wizard) can address pages by their stable UUID.
func (s *Service) GetBusinessPage(ctx context.Context, idOrHandle string, viewerID *uuid.UUID) (*store.BusinessPage, error) {
	if pageID, err := uuid.Parse(idOrHandle); err == nil {
		return s.store.GetBusinessPageByID(ctx, pageID, viewerID)
	}
	return s.store.GetBusinessPageByHandle(ctx, idOrHandle, viewerID)
}

// DeleteBusinessPage hard-deletes a page owned by the caller.
func (s *Service) DeleteBusinessPage(ctx context.Context, pageID, userID uuid.UUID) error {
	return s.store.DeleteBusinessPage(ctx, pageID, userID)
}

// DiscoverPages returns pages filtered by category/search with pagination.
func (s *Service) DiscoverPages(ctx context.Context, category, search string, limit, offset int) ([]store.BusinessPage, error) {
	return s.store.DiscoverPages(ctx, category, search, limit, offset)
}

// FollowPage follows a business page.
func (s *Service) FollowPage(ctx context.Context, pageID, userID uuid.UUID) error {
	return s.store.FollowPage(ctx, pageID, userID)
}

// UnfollowPage unfollows a business page.
func (s *Service) UnfollowPage(ctx context.Context, pageID, userID uuid.UUID) error {
	return s.store.UnfollowPage(ctx, pageID, userID)
}

// UpdateBusinessPage updates a business page.
func (s *Service) UpdateBusinessPage(ctx context.Context, p *store.BusinessPage) error {
	return s.store.UpdateBusinessPage(ctx, p)
}

// GetPageReviews returns reviews for a business page.
func (s *Service) GetPageReviews(ctx context.Context, pageID uuid.UUID, cursor time.Time, limit int) ([]store.BusinessReview, error) {
	return s.store.GetPageReviews(ctx, pageID, cursor, limit)
}

// SubmitReview adds a review for a business page.
func (s *Service) SubmitReview(ctx context.Context, r *store.BusinessReview) error {
	return s.store.SubmitReview(ctx, r)
}

// GetUserBusinessPages returns all business pages for a user.
func (s *Service) GetUserBusinessPages(ctx context.Context, userID uuid.UUID) ([]store.BusinessPage, error) {
	return s.store.GetUserBusinessPages(ctx, userID)
}

// SetBusinessPageSellerID links a seller to a business page.
func (s *Service) SetBusinessPageSellerID(ctx context.Context, pageID, sellerID uuid.UUID) error {
	return s.store.SetBusinessPageSellerID(ctx, pageID, sellerID)
}

// ActivateBusinessPage sets a business page status to active.
func (s *Service) ActivateBusinessPage(ctx context.Context, pageID uuid.UUID) error {
	return s.store.ActivateBusinessPage(ctx, pageID)
}

// --- Reputation & Endorsements ---

// GetReputation returns a user's reputation.
func (s *Service) GetReputation(ctx context.Context, userID uuid.UUID) (*store.UserReputation, error) {
	return s.store.GetReputation(ctx, userID)
}

// EndorseUser creates an endorsement.
func (s *Service) EndorseUser(ctx context.Context, e *store.Endorsement) error {
	if e.FromUserID == e.ToUserID {
		return fmt.Errorf("CANNOT_ENDORSE_SELF")
	}
	return s.store.CreateEndorsement(ctx, e)
}

// GetEndorsements returns all endorsements for a user.
func (s *Service) GetEndorsements(ctx context.Context, userID uuid.UUID) ([]store.Endorsement, error) {
	return s.store.GetEndorsements(ctx, userID)
}

// GetEndorsementSummary returns endorsement counts by skill.
func (s *Service) GetEndorsementSummary(ctx context.Context, userID uuid.UUID) ([]store.SkillEndorsementSummary, error) {
	return s.store.GetEndorsementSummary(ctx, userID)
}

// --- Status/Mood ---

// UpdateStatus sets a user's status/mood.
func (s *Service) UpdateStatus(ctx context.Context, userID uuid.UUID, statusText, statusEmoji string, expiresAt *time.Time) error {
	err := s.store.UpdateStatus(ctx, userID, statusText, statusEmoji, expiresAt)
	if err != nil {
		return err
	}
	// Invalidate user cache
	cardKey := fmt.Sprintf("user:card:%s", userID)
	s.rdb.Del(ctx, cardKey)
	return nil
}

// ClearExpiredStatuses clears statuses that have expired.
func (s *Service) ClearExpiredStatuses(ctx context.Context) (int64, error) {
	return s.store.ClearExpiredStatuses(ctx)
}

// --- Link Analytics ---

// TrackLinkClick increments click count for a user link.
func (s *Service) TrackLinkClick(ctx context.Context, userID uuid.UUID, platform string) error {
	return s.store.TrackLinkClick(ctx, userID, platform)
}

// GetLinkAnalytics returns click counts for a user's links.
func (s *Service) GetLinkAnalytics(ctx context.Context, userID uuid.UUID) ([]store.LinkAnalytics, error) {
	return s.store.GetLinkAnalytics(ctx, userID)
}

// GetCompatibility returns a compatibility score between two users.
func (s *Service) GetCompatibility(ctx context.Context, userID, otherID uuid.UUID) (float64, error) {
	myAbout, err := s.store.GetAllAbout(ctx, userID)
	if err != nil {
		return 0, err
	}
	otherAbout, err := s.store.GetAllAbout(ctx, otherID)
	if err != nil {
		return 0, err
	}

	sharedSections := 0
	totalSections := 0
	for section := range myAbout {
		totalSections++
		if _, ok := otherAbout[section]; ok {
			sharedSections++
		}
	}
	for section := range otherAbout {
		if _, ok := myAbout[section]; !ok {
			totalSections++
		}
	}

	if totalSections == 0 {
		return 0.5, nil
	}
	return float64(sharedSections) / float64(totalSections), nil
}

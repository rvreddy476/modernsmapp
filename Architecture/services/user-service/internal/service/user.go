package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/user-service/internal/handle"
	"github.com/atpost/user-service/internal/identityclient"
	"github.com/atpost/user-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	store    *store.Store
	rdb      *redis.Client
	identity *identityclient.Client

	// ms is store, seen through the calls the media-writing paths make
	// (media_claims.go); tests substitute an in-memory fake.
	ms mediaStore
	// media checks an attached asset is the actor's own finished upload;
	// claims registers the reference with media-service. Nil refuses every
	// write that ADDS media: fail closed (media_claims.go).
	media  MediaLookup
	claims MediaClaims
}

func New(s *store.Store, rdb *redis.Client) *Service {
	svc := &Service{store: s, rdb: rdb}
	if s != nil {
		svc.ms = s
	}
	return svc
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// WithIdentityClient wires the profile-service client used to repair the
// local app.users projection from the identity source of truth.
func (s *Service) WithIdentityClient(c *identityclient.Client) *Service {
	s.identity = c
	return s
}

// ErrUserNotInIdentity means the user has no row in the local projection AND
// profile-service has no such user — i.e. the user genuinely does not exist.
var ErrUserNotInIdentity = errors.New("user not found in identity source")

// EnsureUser guarantees the user has a row in the local app.users projection.
// If the row is missing — because a UserRegistered Kafka event was lost — it
// repairs the projection on-demand from profile-service. This read-through
// fallback keeps cross-service flows (e.g. graph close-friends, whose FK
// references app.users) from failing on a stale projection.
//
// Returns nil when the row exists or was just repaired; ErrUserNotInIdentity
// when the user does not exist anywhere; a transport error otherwise.
func (s *Service) EnsureUser(ctx context.Context, id uuid.UUID) error {
	u, err := s.store.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if u != nil {
		return nil // already projected — fast path
	}
	if s.identity == nil {
		return ErrUserNotInIdentity // no repair source wired
	}
	p, err := s.identity.GetProfile(ctx, id)
	if err != nil {
		return err // transport failure — caller decides whether to proceed
	}
	if p == nil {
		return ErrUserNotInIdentity
	}
	if err := s.store.UpsertUserProjection(ctx, ProjectionInputFromProfile(p)); err != nil {
		return err
	}
	// Best-effort: a repaired user should also have a handle + default channel.
	// Non-fatal — the projection row already exists, which is what unblocks callers.
	if _, perr := s.store.EnsurePublisher(ctx, id, handle.Generate); perr != nil {
		fmt.Printf("warn: ensure-publisher for repaired user %s failed: %v\n", id, perr)
	}
	return nil
}

// ProjectionInputFromProfile maps an identity profile to the projection upsert
// payload, guaranteeing a non-empty display_name (the column is NOT NULL).
// Shared by the read-through repair (EnsureUser) and the reconcile job.
func ProjectionInputFromProfile(p *identityclient.Profile) store.ProjectionInput {
	display := strings.TrimSpace(p.DisplayName)
	if display == "" {
		display = strings.TrimSpace(deref(p.FirstName) + " " + deref(p.LastName))
	}
	if display == "" {
		display = "User " + p.UserID.String()[:8]
	}
	category := p.Category
	if category == "" {
		category = "personal"
	}
	return store.ProjectionInput{
		ID:            p.UserID,
		Username:      p.Username,
		DisplayName:   display,
		FirstName:     p.FirstName,
		LastName:      p.LastName,
		Bio:           p.Bio,
		Category:      category,
		Profession:    p.Profession,
		Website:       p.Website,
		Location:      p.Location,
		BadgeFlags:    p.BadgeFlags,
		IsVerified:    p.IsVerified,
		AvatarMediaID: p.AvatarMediaID,
		CoverMediaID:  p.CoverMediaID,
		CreatedAt:     p.CreatedAt,
		UpdatedAt:     p.UpdatedAt,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ProjectionHealthReport summarises whether the app.users projection is
// converged with the identity master.
type ProjectionHealthReport struct {
	MasterCount     int64      `json:"master_count"`
	ProjectionCount int64      `json:"projection_count"`
	MissingCount    int64      `json:"missing_count"`
	DLQUnreplayed   int        `json:"dlq_unreplayed"`
	LastSyncedAt    *time.Time `json:"last_synced_at,omitempty"`
	LastSuccessAt   *time.Time `json:"last_success_at,omitempty"`
	LastError       *string    `json:"last_reconcile_error,omitempty"`
	Status          string     `json:"status"` // healthy | degraded | unknown
}

// ProjectionHealth reports the convergence of app.users with the identity
// master — backing the /internal/projection/health endpoint and metrics.
func (s *Service) ProjectionHealth(ctx context.Context) (*ProjectionHealthReport, error) {
	r := &ProjectionHealthReport{Status: "healthy"}

	localCount, err := s.store.CountUsers(ctx)
	if err != nil {
		return nil, err
	}
	r.ProjectionCount = localCount

	if dlq, err := s.store.CountDLQ(ctx); err == nil {
		r.DLQUnreplayed = dlq
	}

	if cp, err := s.store.GetCheckpoint(ctx, store.CheckpointAppUsers); err == nil && cp != nil {
		r.LastSyncedAt = &cp.LastSyncedAt
		r.LastSuccessAt = cp.LastSuccessAt
		r.LastError = cp.LastError
	}

	if s.identity != nil {
		if master, err := s.identity.CountProfiles(ctx); err == nil {
			r.MasterCount = master
			if master > localCount {
				r.MissingCount = master - localCount
			}
		} else {
			r.Status = "unknown" // cannot reach the master to compare
		}
	} else {
		r.Status = "unknown"
	}

	if r.MissingCount > 0 || r.DLQUnreplayed > 0 || r.LastError != nil {
		r.Status = "degraded"
	}
	return r, nil
}

// SoftDeleteUser marks the app-level user record as deleted.
func (s *Service) SoftDeleteUser(ctx context.Context, id uuid.UUID) error {
	if err := s.store.SoftDeleteUser(ctx, id); err != nil {
		return err
	}
	s.dropUserCard(ctx, id)
	return nil
}

// SetUserHidden is the purge.Hider for account control: it flags the row and
// drops the cached card, so a deactivated account stops answering profile
// reads now rather than when a five-minute cache entry expires.
func (s *Service) SetUserHidden(ctx context.Context, id uuid.UUID, hidden bool, reason string) error {
	if err := s.store.SetUserHidden(ctx, id, hidden, reason); err != nil {
		return err
	}
	s.dropUserCard(ctx, id)
	return nil
}

func (s *Service) dropUserCard(ctx context.Context, id uuid.UUID) {
	if s.rdb != nil {
		s.rdb.Del(ctx, userCardKey(id))
	}
}

func userCardKey(id uuid.UUID) string { return fmt.Sprintf("user:card:%s", id) }

// cachedCard is the Redis form of a user card. Unavailable travels beside
// the user because store.User never serializes it; an entry without "user"
// (the pre-envelope format) reads as a miss.
type cachedCard struct {
	User        *store.User `json:"user"`
	Unavailable bool        `json:"unavailable"`
}

func encodeCard(u *store.User) ([]byte, error) {
	return json.Marshal(cachedCard{User: u, Unavailable: u.Unavailable})
}

func decodeCard(raw string) (*store.User, bool) {
	var c cachedCard
	if json.Unmarshal([]byte(raw), &c) != nil || c.User == nil {
		return nil, false
	}
	c.User.Unavailable = c.Unavailable
	return c.User, true
}

// CreateUser handles user creation from event.
// After creating the user record it auto-provisions a handle and default
// channel so the user is ready to publish content immediately.
func (s *Service) CreateUser(ctx context.Context, id uuid.UUID, username, phone, email, firstName, lastName, dob, gender string) error {
	displayName := firstName + " " + lastName
	if displayName == " " {
		displayName = "User " + id.String()[:8]
	}
	if err := s.store.CreateUser(ctx, id, displayName, firstName, lastName, dob, gender); err != nil {
		return err
	}

	// Store the handle auth-service assigned, so /u/<handle> resolves here
	// too. An empty username means the event came from an auth-service older
	// than 22 Sep 2026, which left it NULL; nothing is invented in its place.
	if username != "" {
		assigned, err := s.store.SetUsernameIfAbsent(ctx, id, []string{
			username,
			username + "1",
			fmt.Sprintf("%s_%s", username, id.String()[:6]),
		})
		if err != nil {
			// Not fatal: the account exists and is usable, it just has no
			// profile address here yet. GetUser's read-through repair and a
			// rename both still work.
			fmt.Printf("warn: set username %q for %s failed: %v\n", username, id, err)
		} else if assigned != "" && assigned != username {
			fmt.Printf("warn: username %q was taken in app.users; %s got %q instead — identity and app now disagree\n",
				username, id, assigned)
		}
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
	cacheKey := userCardKey(id)
	val, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		if u, ok := decodeCard(val); ok {
			return u, nil
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
		data, _ := encodeCard(u)
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
		data, _ := encodeCard(u)
		s.rdb.Set(context.Background(), userCardKey(u.ID), data, 5*time.Minute)
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

// canView decides one About item for a viewer who is not its owner. An
// unknown visibility is private: the retired user-service write stored any
// string, and a typo or a value from a newer writer must hide an item, never
// publish it (US-L2).
func canView(visibility string, access ViewerAccess) bool {
	switch visibility {
	case "public":
		return true
	case "followers":
		return access.IsFollower || access.IsFriend
	case "friends":
		return access.IsFriend
	default: // "private", and anything unknown
		return false
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

// GetBusinessPage returns a business page by handle OR UUID, with optional viewer follow status.
// Accepting both forms keeps the public `:id` route flexible: clients can deep-link by handle
// while internal flows (e.g. onboarding wizard) can address pages by their stable UUID.
func (s *Service) GetBusinessPage(ctx context.Context, idOrHandle string, viewerID *uuid.UUID) (*store.BusinessPage, error) {
	if pageID, err := uuid.Parse(idOrHandle); err == nil {
		return s.store.GetBusinessPageByID(ctx, pageID, viewerID)
	}
	return s.store.GetBusinessPageByHandle(ctx, idOrHandle, viewerID)
}

// DiscoverPages returns pages filtered by category/search with pagination.
func (s *Service) DiscoverPages(ctx context.Context, category, search string, limit, offset int) ([]store.BusinessPage, error) {
	return s.store.DiscoverPages(ctx, category, search, limit, offset)
}

// FollowPage follows a business page; returns the new follower count.
func (s *Service) FollowPage(ctx context.Context, pageID, userID uuid.UUID) (int, error) {
	return s.store.FollowPage(ctx, pageID, userID)
}

// UnfollowPage unfollows a business page; returns the resulting follower count.
func (s *Service) UnfollowPage(ctx context.Context, pageID, userID uuid.UUID) (int, error) {
	return s.store.UnfollowPage(ctx, pageID, userID)
}

// UpdatePageStatus performs a lifecycle transition (caller checks legality/authz).
func (s *Service) UpdatePageStatus(ctx context.Context, pageID, actorID uuid.UUID, to, reason string) error {
	return s.store.UpdatePageStatus(ctx, pageID, to, actorID, reason)
}

// CountActivePagesOwned returns the count of non-disabled pages a user owns.
func (s *Service) CountActivePagesOwned(ctx context.Context, userID uuid.UUID) (int, error) {
	return s.store.CountActivePagesOwned(ctx, userID)
}

// ReconcileFollowerCounts recomputes page follower counts from active rows.
func (s *Service) ReconcileFollowerCounts(ctx context.Context) (int64, error) {
	return s.store.ReconcileFollowerCounts(ctx)
}

// GetPageRole returns a user's active role on a page ("" if none).
func (s *Service) GetPageRole(ctx context.Context, pageID, userID uuid.UUID) (string, error) {
	return s.store.GetPageRole(ctx, pageID, userID)
}

// IsPageOwnerOrAdmin reports whether the user holds owner/admin on the page.
func (s *Service) IsPageOwnerOrAdmin(ctx context.Context, pageID, userID uuid.UUID) (bool, error) {
	return s.store.IsPageOwnerOrAdmin(ctx, pageID, userID)
}

// AddPageDocument inserts a pending verification document.
func (s *Service) AddPageDocument(ctx context.Context, d *store.PageDocument) error {
	return s.store.AddPageDocument(ctx, d)
}

// ListPageDocuments lists a page's verification documents.
func (s *Service) ListPageDocuments(ctx context.Context, pageID uuid.UUID) ([]store.PageDocument, error) {
	return s.store.ListPageDocuments(ctx, pageID)
}

// UploadedDocTypes returns the set of uploaded doc types (pending|approved).
func (s *Service) UploadedDocTypes(ctx context.Context, pageID uuid.UUID) (map[string]bool, error) {
	return s.store.UploadedDocTypes(ctx, pageID)
}

// SetPageDocStatus reviews a single document.
func (s *Service) SetPageDocStatus(ctx context.Context, docID, reviewerID uuid.UUID, status, reason string) error {
	return s.store.SetPageDocStatus(ctx, docID, reviewerID, status, reason)
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

// ApplySellerApproval links an approved seller to their own business page and
// approves it only out of pending_review. See store/seller_approval.go.
func (s *Service) ApplySellerApproval(ctx context.Context, pageID, sellerID, sellerUserID, actorID uuid.UUID) (store.SellerApprovalOutcome, error) {
	return s.store.ApplySellerApproval(ctx, pageID, sellerID, sellerUserID, actorID)
}

// --- Reputation ---

// GetReputation returns a user's reputation. The endorsement routes that fed
// it are retired (internal/http/retired_social_routes.go); the user.endorsed
// consumer still reads it.
func (s *Service) GetReputation(ctx context.Context, userID uuid.UUID) (*store.UserReputation, error) {
	return s.store.GetReputation(ctx, userID)
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

// GetLinkAnalytics returns click counts for a user's links.
func (s *Service) GetLinkAnalytics(ctx context.Context, userID uuid.UUID) ([]store.LinkAnalytics, error) {
	return s.store.GetLinkAnalytics(ctx, userID)
}

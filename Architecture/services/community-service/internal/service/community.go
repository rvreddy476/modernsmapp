package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	communityevents "github.com/atpost/community-service/internal/events"
	"github.com/atpost/community-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var validCommunityTypes = map[string]bool{
	"public": true, "private": true, "invite": true, "education": true,
	"local": true, "professional": true, "fan": true, "brand": true,
}

var validJoinModes = map[string]bool{
	"open": true, "request": true, "invite_only": true, "email_domain": true,
}

var validMemberRoles = map[string]bool{
	"owner": true, "admin": true, "moderator": true, "space_manager": true,
	"expert": true, "member": true, "pending": true, "suspended": true, "banned": true,
}

var validSpaceTypes = map[string]bool{
	"group": true, "channel": true, "discussion": true, "events": true, "resources": true,
}

func roleLevel(role string) int {
	switch role {
	case "owner":
		return 7
	case "admin":
		return 6
	case "moderator":
		return 5
	case "space_manager":
		return 4
	case "expert":
		return 3
	case "member":
		return 2
	case "pending":
		return 1
	default:
		return 0
	}
}

func isAtLeast(userRole, requiredRole string) bool {
	return roleLevel(userRole) >= roleLevel(requiredRole)
}

// AuthError discriminates the two reasons we deny a request — useful for
// the handler to map to 404 vs 403 appropriately. Audit (CC2-CC7):
// previously the handlers called h.svc.Store() directly, bypassing
// every membership/role gate. These helpers + their sentinel errors
// give handlers a single chokepoint to enforce authorization.
type AuthError struct {
	Reason string // "not_member", "insufficient_role", "banned", "not_owner"
}

func (e *AuthError) Error() string { return "community auth: " + e.Reason }

var (
	ErrNotCommunityMember   = &AuthError{Reason: "not_member"}
	ErrInsufficientRole     = &AuthError{Reason: "insufficient_role"}
	ErrMemberBanned         = &AuthError{Reason: "banned"}
	ErrNotPostAuthor        = &AuthError{Reason: "not_owner"}
)

// AuthorizeMembership returns nil if userID is an active (non-banned)
// member of communityID; returns ErrNotCommunityMember / ErrMemberBanned
// otherwise. Use this on every write that should require being in the
// community (post creation, engagement on member-only content).
func (s *Service) AuthorizeMembership(ctx context.Context, communityID, userID uuid.UUID) (*store.CommunityMember, error) {
	m, err := s.store.GetMember(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrNotCommunityMember
	}
	if m.Role == "banned" || m.Role == "suspended" {
		return nil, ErrMemberBanned
	}
	return m, nil
}

// AuthorizeRole returns nil if userID is a member of communityID with a
// role at least `minRole`; otherwise returns ErrInsufficientRole (or
// ErrNotCommunityMember / ErrMemberBanned for the precedent failures).
// Used for moderation actions: pin, feature, ban, wiki edit, etc.
func (s *Service) AuthorizeRole(ctx context.Context, communityID, userID uuid.UUID, minRole string) (*store.CommunityMember, error) {
	m, err := s.AuthorizeMembership(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if !isAtLeast(m.Role, minRole) {
		return nil, ErrInsufficientRole
	}
	return m, nil
}

// AuthorizeViewer returns nil if userID may view content in communityID
// (anyone for public/education, members only for private). Pass empty
// userID for unauthenticated callers — only public communities pass.
func (s *Service) AuthorizeViewer(ctx context.Context, communityID uuid.UUID, userID *uuid.UUID) error {
	c, err := s.store.GetCommunityByID(ctx, communityID)
	if err != nil {
		return err
	}
	if c == nil {
		return ErrNotCommunityMember
	}
	// Public / discovery-friendly types: anyone can read.
	switch c.CommunityType {
	case "public", "education", "professional":
		return nil
	}
	// Private / invite-only / secret: require active membership.
	if userID == nil {
		return ErrNotCommunityMember
	}
	_, err = s.AuthorizeMembership(ctx, communityID, *userID)
	return err
}

type Service struct {
	store    *store.Store
	rdb      *redis.Client
	producer *communityevents.Producer
}

func New(s *store.Store, rdb *redis.Client) *Service {
	return &Service{store: s, rdb: rdb}
}

// memberCacheTTL is short on purpose — role/ban changes need to
// propagate fast (admin demoting a moderator can't wait 5 min). 60s
// absorbs burst load on hot communities (post create, comment, list
// posts all call GetMember) without making membership writes feel
// stale.
const memberCacheTTL = 60 * time.Second

// getMemberCached is the read-through wrapper for hot-path GetMember
// calls. Cache miss → store fetch → cache write (best-effort).
// Cache hit → unmarshal. Returns the same shape as store.GetMember
// (including not-found returning nil membership + nil error).
//
// Mediums note: addresses HC2-pattern (per-request DB call on hot
// path). Promotions / bans / unbans invalidate via invalidateMemberCache.
func (s *Service) getMemberCached(ctx context.Context, communityID, userID uuid.UUID) (*store.CommunityMember, error) {
	if s.rdb == nil {
		return s.store.GetMember(ctx, communityID, userID)
	}
	key := fmt.Sprintf("cm:%s:%s", communityID, userID)
	if raw, err := s.rdb.Get(ctx, key).Bytes(); err == nil && len(raw) > 0 {
		// Sentinel value for negative cache (user is not a member).
		if string(raw) == "_nil" {
			return nil, nil
		}
		var m store.CommunityMember
		if err := json.Unmarshal(raw, &m); err == nil {
			return &m, nil
		}
		// Fall through on bad cached payload.
	}
	m, err := s.store.GetMember(ctx, communityID, userID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		// Cache the not-a-member result too — common for public
		// browse, where a non-member viewer hits GetMember on every
		// post fetch.
		_ = s.rdb.Set(ctx, key, "_nil", memberCacheTTL).Err()
		return nil, nil
	}
	if raw, mErr := json.Marshal(m); mErr == nil {
		_ = s.rdb.Set(ctx, key, raw, memberCacheTTL).Err()
	}
	return m, nil
}

// invalidateMemberCache is called after any membership mutation
// (join, leave, role change, ban) so the cached row doesn't survive
// the change.
func (s *Service) invalidateMemberCache(ctx context.Context, communityID, userID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	key := fmt.Sprintf("cm:%s:%s", communityID, userID)
	_ = s.rdb.Del(ctx, key).Err()
}

func (s *Service) SetProducer(p *communityevents.Producer) {
	s.producer = p
}

func (s *Service) Store() *store.Store {
	return s.store
}

// --- Community CRUD ---

type CreateCommunityParams struct {
	Handle          string          `json:"handle"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	AvatarMediaID   *uuid.UUID      `json:"avatar_media_id"`
	BannerMediaID   *uuid.UUID      `json:"banner_media_id"`
	CommunityType   string          `json:"community_type"`
	Category        string          `json:"category"`
	Language        string          `json:"language"`
	JoinMode        string          `json:"join_mode"`
	EmailDomainGate *string         `json:"email_domain_gate"`
	JoinQuestions   json.RawMessage `json:"join_questions"`
	MemberDirectory *bool           `json:"member_directory"`
	CrossSpaceBans  *bool           `json:"cross_space_bans"`
	MaxSubSpaces    *int            `json:"max_sub_spaces"`
	Latitude        *float64        `json:"latitude"`
	Longitude       *float64        `json:"longitude"`
	LocationName    string          `json:"location_name"`
	Rules           []string        `json:"rules"`
	TopicTags       []string        `json:"topic_tags"`
}

func (s *Service) CreateCommunity(ctx context.Context, ownerID uuid.UUID, params CreateCommunityParams) (*store.Community, error) {
	if params.Name == "" {
		return nil, fmt.Errorf("invalid: name is required")
	}
	if params.Handle == "" {
		return nil, fmt.Errorf("invalid: handle is required")
	}
	if len(params.Handle) < 3 || len(params.Handle) > 30 {
		return nil, fmt.Errorf("invalid: handle must be between 3 and 30 characters")
	}

	ct := params.CommunityType
	if ct == "" {
		ct = "public"
	}
	if !validCommunityTypes[ct] {
		return nil, fmt.Errorf("invalid: community_type is not valid")
	}

	jm := params.JoinMode
	if jm == "" {
		jm = "open"
	}
	if !validJoinModes[jm] {
		return nil, fmt.Errorf("invalid: join_mode is not valid")
	}

	// Rate limit: 3 communities per day
	since := time.Now().Add(-24 * time.Hour)
	count, err := s.store.CountCommunitiesByOwner(ctx, ownerID, since)
	if err != nil {
		return nil, fmt.Errorf("failed to check rate limit: %w", err)
	}
	if count >= 3 {
		return nil, fmt.Errorf("rate_limited: you can create at most 3 communities per day")
	}

	memberDir := true
	if params.MemberDirectory != nil {
		memberDir = *params.MemberDirectory
	}

	crossBans := true
	if params.CrossSpaceBans != nil {
		crossBans = *params.CrossSpaceBans
	}

	maxSpaces := 50
	if params.MaxSubSpaces != nil && *params.MaxSubSpaces > 0 {
		maxSpaces = *params.MaxSubSpaces
	}

	rules := params.Rules
	if rules == nil {
		rules = []string{}
	}
	tags := params.TopicTags
	if tags == nil {
		tags = []string{}
	}

	joinQuestions := params.JoinQuestions
	if joinQuestions == nil {
		joinQuestions = json.RawMessage("[]")
	}

	c := &store.Community{
		ID:              uuid.New(),
		OwnerID:         ownerID,
		Handle:          params.Handle,
		Name:            params.Name,
		Description:     params.Description,
		AvatarMediaID:   params.AvatarMediaID,
		BannerMediaID:   params.BannerMediaID,
		CommunityType:   ct,
		Category:        params.Category,
		Language:        params.Language,
		JoinMode:        jm,
		EmailDomainGate: params.EmailDomainGate,
		JoinQuestions:   joinQuestions,
		MemberDirectory: memberDir,
		CrossSpaceBans:  crossBans,
		MaxSubSpaces:    maxSpaces,
		Latitude:        params.Latitude,
		Longitude:       params.Longitude,
		LocationName:    params.LocationName,
		Rules:           rules,
		TopicTags:       tags,
		MemberCount:     1, // owner counts
		Status:          "active",
	}

	if err := s.store.CreateCommunity(ctx, c); err != nil {
		return nil, fmt.Errorf("failed to create community: %w", err)
	}

	// Add owner as member
	member := &store.CommunityMember{
		CommunityID: c.ID,
		UserID:      ownerID,
		Role:        "owner",
	}
	if _, err := s.store.AddMember(ctx, member); err != nil {
		slog.Warn("failed to add owner as member", "community_id", c.ID, "error", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishCommunityCreated(ctx, c.ID, ownerID, c.Name, c.CommunityType); err != nil {
			slog.Warn("failed to publish community.created event", "error", err)
		}
	}

	return c, nil
}

type CommunityWithMembership struct {
	*store.Community
	ViewerRole string `json:"viewer_role,omitempty"`
}

func (s *Service) GetCommunity(ctx context.Context, communityID uuid.UUID, viewerID *uuid.UUID) (*CommunityWithMembership, error) {
	c, err := s.store.GetCommunityByID(ctx, communityID)
	if err != nil {
		return nil, err
	}

	result := &CommunityWithMembership{Community: c}

	if viewerID != nil {
		member, err := s.store.GetMember(ctx, communityID, *viewerID)
		if err != nil {
			slog.Warn("failed to get member state", "error", err)
		}
		if member != nil {
			result.ViewerRole = member.Role
		}
	}

	return result, nil
}

type UpdateCommunityParams struct {
	Name            *string         `json:"name"`
	Description     *string         `json:"description"`
	AvatarMediaID   *uuid.UUID      `json:"avatar_media_id"`
	BannerMediaID   *uuid.UUID      `json:"banner_media_id"`
	CommunityType   *string         `json:"community_type"`
	Category        *string         `json:"category"`
	Language        *string         `json:"language"`
	JoinMode        *string         `json:"join_mode"`
	EmailDomainGate *string         `json:"email_domain_gate"`
	JoinQuestions   json.RawMessage `json:"join_questions"`
	MemberDirectory *bool           `json:"member_directory"`
	CrossSpaceBans  *bool           `json:"cross_space_bans"`
	MaxSubSpaces    *int            `json:"max_sub_spaces"`
	Latitude        *float64        `json:"latitude"`
	Longitude       *float64        `json:"longitude"`
	LocationName    *string         `json:"location_name"`
	Rules           []string        `json:"rules"`
	TopicTags       []string        `json:"topic_tags"`
}

func (s *Service) UpdateCommunity(ctx context.Context, communityID, actorID uuid.UUID, params UpdateCommunityParams) (*store.Community, error) {
	member, err := s.store.GetMember(ctx, communityID, actorID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "admin") {
		return nil, fmt.Errorf("forbidden: only admins and above can update the community")
	}

	c, err := s.store.GetCommunityByID(ctx, communityID)
	if err != nil {
		return nil, err
	}

	if params.Name != nil {
		c.Name = *params.Name
	}
	if params.Description != nil {
		c.Description = *params.Description
	}
	if params.AvatarMediaID != nil {
		c.AvatarMediaID = params.AvatarMediaID
	}
	if params.BannerMediaID != nil {
		c.BannerMediaID = params.BannerMediaID
	}
	if params.CommunityType != nil {
		if !validCommunityTypes[*params.CommunityType] {
			return nil, fmt.Errorf("invalid: community_type is not valid")
		}
		c.CommunityType = *params.CommunityType
	}
	if params.Category != nil {
		c.Category = *params.Category
	}
	if params.Language != nil {
		c.Language = *params.Language
	}
	if params.JoinMode != nil {
		if !validJoinModes[*params.JoinMode] {
			return nil, fmt.Errorf("invalid: join_mode is not valid")
		}
		c.JoinMode = *params.JoinMode
	}
	if params.EmailDomainGate != nil {
		c.EmailDomainGate = params.EmailDomainGate
	}
	if params.JoinQuestions != nil {
		c.JoinQuestions = params.JoinQuestions
	}
	if params.MemberDirectory != nil {
		c.MemberDirectory = *params.MemberDirectory
	}
	if params.CrossSpaceBans != nil {
		c.CrossSpaceBans = *params.CrossSpaceBans
	}
	if params.MaxSubSpaces != nil {
		c.MaxSubSpaces = *params.MaxSubSpaces
	}
	if params.Latitude != nil {
		c.Latitude = params.Latitude
	}
	if params.Longitude != nil {
		c.Longitude = params.Longitude
	}
	if params.LocationName != nil {
		c.LocationName = *params.LocationName
	}
	if params.Rules != nil {
		c.Rules = params.Rules
	}
	if params.TopicTags != nil {
		c.TopicTags = params.TopicTags
	}

	if err := s.store.UpdateCommunity(ctx, c); err != nil {
		return nil, fmt.Errorf("failed to update community: %w", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishCommunityUpdated(ctx, communityID, actorID, c.Name, c.CommunityType); err != nil {
			slog.Warn("failed to publish community.updated event", "error", err)
		}
	}

	return c, nil
}

func (s *Service) DeleteCommunity(ctx context.Context, communityID, actorID uuid.UUID) error {
	member, err := s.store.GetMember(ctx, communityID, actorID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || member.Role != "owner" {
		return fmt.Errorf("forbidden: only the community owner can delete the community")
	}

	if err := s.store.DeleteCommunity(ctx, communityID); err != nil {
		return fmt.Errorf("failed to delete community: %w", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishCommunityDeleted(ctx, communityID, actorID); err != nil {
			slog.Warn("failed to publish community.deleted event", "error", err)
		}
	}

	return nil
}

// --- Join / Leave ---

func (s *Service) JoinCommunity(ctx context.Context, communityID, userID uuid.UUID, answers json.RawMessage) (*store.CommunityMember, *store.CommunityJoinRequest, error) {
	c, err := s.store.GetCommunityByID(ctx, communityID)
	if err != nil {
		return nil, nil, err
	}

	existing, err := s.store.GetMember(ctx, communityID, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if existing != nil {
		if existing.Role == "banned" {
			return nil, nil, fmt.Errorf("forbidden: you are banned from this community")
		}
		return nil, nil, fmt.Errorf("already a member of this community")
	}

	switch c.JoinMode {
	case "open":
		member := &store.CommunityMember{
			CommunityID: communityID,
			UserID:      userID,
			Role:        "member",
		}
		inserted, err := s.store.AddMember(ctx, member)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to join: %w", err)
		}
		// Audit HC1: only increment member_count on a genuinely new row
		// — concurrent duplicate joins previously each ran
		// IncrementMemberCount and drifted the counter upward.
		if inserted {
			if err := s.store.IncrementMemberCount(ctx, communityID, 1); err != nil {
				slog.Warn("failed to increment member count", "error", err)
			}
		}
		// Auto-follow default spaces (announcements, welcome, is_default)
		if err := s.store.AutoFollowDefaultSpaces(ctx, communityID, userID); err != nil {
			slog.Warn("failed to auto-follow default spaces", "community_id", communityID, "user_id", userID, "error", err)
		}
		if s.producer != nil {
			if err := s.producer.PublishMemberJoined(ctx, communityID, userID); err != nil {
				slog.Warn("failed to publish community.member.joined event", "error", err)
			}
		}
		return member, nil, nil

	case "request", "invite_only", "email_domain":
		jr := &store.CommunityJoinRequest{
			ID:          uuid.New(),
			CommunityID: communityID,
			UserID:      userID,
			Answers:     answers,
			Status:      "pending",
		}
		if err := s.store.CreateJoinRequest(ctx, jr); err != nil {
			return nil, nil, fmt.Errorf("failed to create join request: %w", err)
		}
		return nil, jr, nil

	default:
		return nil, nil, fmt.Errorf("invalid: unknown join mode")
	}
}

func (s *Service) LeaveCommunity(ctx context.Context, communityID, userID uuid.UUID) error {
	member, err := s.store.GetMember(ctx, communityID, userID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil {
		return fmt.Errorf("not a member of this community")
	}
	if member.Role == "owner" {
		return fmt.Errorf("forbidden: the owner cannot leave; delete the community instead")
	}

	if err := s.store.RemoveMember(ctx, communityID, userID); err != nil {
		return fmt.Errorf("failed to leave: %w", err)
	}

	if err := s.store.IncrementMemberCount(ctx, communityID, -1); err != nil {
		slog.Warn("failed to decrement member count", "error", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishMemberLeft(ctx, communityID, userID); err != nil {
			slog.Warn("failed to publish community.member.left event", "error", err)
		}
	}

	return nil
}

// --- Members ---

func (s *Service) ListMembers(ctx context.Context, communityID uuid.UUID, limit, offset int) ([]store.CommunityMember, error) {
	return s.store.ListMembers(ctx, communityID, limit, offset)
}

func (s *Service) UpdateMemberRole(ctx context.Context, communityID, targetUserID, actorID uuid.UUID, newRole string) error {
	if !validMemberRoles[newRole] {
		return fmt.Errorf("invalid: role is not valid")
	}

	actor, err := s.store.GetMember(ctx, communityID, actorID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if actor == nil || !isAtLeast(actor.Role, "admin") {
		return fmt.Errorf("forbidden: only admins and above can change roles")
	}

	target, err := s.store.GetMember(ctx, communityID, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to check target membership: %w", err)
	}
	if target == nil {
		return fmt.Errorf("member not found")
	}

	// Cannot change role of someone at same or higher level
	if roleLevel(target.Role) >= roleLevel(actor.Role) {
		return fmt.Errorf("forbidden: cannot change role of someone at your level or above")
	}
	// Cannot promote above own level
	if roleLevel(newRole) >= roleLevel(actor.Role) {
		return fmt.Errorf("forbidden: cannot promote to your level or above")
	}

	if err := s.store.UpdateMemberRole(ctx, communityID, targetUserID, newRole); err != nil {
		return err
	}

	if s.producer != nil {
		if err := s.producer.PublishMemberRoleChanged(ctx, communityID, targetUserID, actorID, newRole); err != nil {
			slog.Warn("failed to publish community.member.role_changed event", "error", err)
		}
	}

	return nil
}

func (s *Service) BanMember(ctx context.Context, communityID, targetUserID, actorID uuid.UUID, reason string) error {
	actor, err := s.store.GetMember(ctx, communityID, actorID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if actor == nil || !isAtLeast(actor.Role, "moderator") {
		return fmt.Errorf("forbidden: only moderators and above can ban members")
	}

	target, err := s.store.GetMember(ctx, communityID, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to check target membership: %w", err)
	}
	if target == nil {
		return fmt.Errorf("member not found")
	}
	if roleLevel(target.Role) >= roleLevel(actor.Role) {
		return fmt.Errorf("forbidden: cannot ban someone at your level or above")
	}

	if err := s.store.BanMember(ctx, communityID, targetUserID, actorID, reason); err != nil {
		return fmt.Errorf("failed to ban member: %w", err)
	}

	if err := s.store.IncrementMemberCount(ctx, communityID, -1); err != nil {
		slog.Warn("failed to decrement member count", "error", err)
	}

	// Log modlog entry
	entry := &store.CommunityModlogEntry{
		ID:          uuid.New(),
		CommunityID: communityID,
		ActorID:     actorID,
		Action:      "ban_member",
		TargetType:  "user",
		TargetID:    targetUserID,
		Reason:      &reason,
	}
	// Audit HC6: previously this swallowed the error with a Warn log so
	// a ban/unban/role-change without an audit trail looked successful.
	// The audit log is safety-critical — fail the action so the actor
	// can retry and operators see the gap in metrics.
	if err := s.store.AddModlogEntry(ctx, entry); err != nil {
		return fmt.Errorf("failed to record moderation audit log: %w", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishMemberBanned(ctx, communityID, targetUserID, actorID); err != nil {
			slog.Warn("failed to publish community.member.banned event", "error", err)
		}
	}

	return nil
}

func (s *Service) UnbanMember(ctx context.Context, communityID, targetUserID, actorID uuid.UUID) error {
	actor, err := s.store.GetMember(ctx, communityID, actorID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if actor == nil || !isAtLeast(actor.Role, "moderator") {
		return fmt.Errorf("forbidden: only moderators and above can unban members")
	}

	if err := s.store.UnbanMember(ctx, communityID, targetUserID); err != nil {
		return err
	}

	// Log modlog entry
	entry := &store.CommunityModlogEntry{
		ID:          uuid.New(),
		CommunityID: communityID,
		ActorID:     actorID,
		Action:      "unban_member",
		TargetType:  "user",
		TargetID:    targetUserID,
	}
	// Audit HC6: previously this swallowed the error with a Warn log so
	// a ban/unban/role-change without an audit trail looked successful.
	// The audit log is safety-critical — fail the action so the actor
	// can retry and operators see the gap in metrics.
	if err := s.store.AddModlogEntry(ctx, entry); err != nil {
		return fmt.Errorf("failed to record moderation audit log: %w", err)
	}

	return nil
}

// --- Spaces ---

type CreateSpaceParams struct {
	SpaceType       string     `json:"space_type"`
	LinkedGroupID   *uuid.UUID `json:"linked_group_id"`
	LinkedChannelID *uuid.UUID `json:"linked_channel_id"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	SortOrder       int        `json:"sort_order"`
}

func (s *Service) CreateSpace(ctx context.Context, communityID, creatorID uuid.UUID, params CreateSpaceParams) (*store.CommunitySpace, error) {
	member, err := s.store.GetMember(ctx, communityID, creatorID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "space_manager") {
		return nil, fmt.Errorf("forbidden: only space managers and above can create spaces")
	}

	if params.Name == "" {
		return nil, fmt.Errorf("invalid: name is required")
	}

	st := params.SpaceType
	if st == "" {
		st = "discussion"
	}
	if !validSpaceTypes[st] {
		return nil, fmt.Errorf("invalid: space_type is not valid")
	}

	// Check max spaces
	c, err := s.store.GetCommunityByID(ctx, communityID)
	if err != nil {
		return nil, err
	}
	spaceCount, err := s.store.CountSpaces(ctx, communityID)
	if err != nil {
		return nil, fmt.Errorf("failed to count spaces: %w", err)
	}
	if spaceCount >= c.MaxSubSpaces {
		return nil, fmt.Errorf("rate_limited: community has reached its maximum number of spaces (%d)", c.MaxSubSpaces)
	}

	sp := &store.CommunitySpace{
		ID:              uuid.New(),
		CommunityID:     communityID,
		SpaceType:       st,
		LinkedGroupID:   params.LinkedGroupID,
		LinkedChannelID: params.LinkedChannelID,
		Name:            params.Name,
		Description:     params.Description,
		SortOrder:       params.SortOrder,
		CreatedBy:       creatorID,
	}

	if err := s.store.CreateSpace(ctx, sp); err != nil {
		return nil, fmt.Errorf("failed to create space: %w", err)
	}

	if err := s.store.IncrementSpaceCount(ctx, communityID, 1); err != nil {
		slog.Warn("failed to increment space count", "error", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishSpaceCreated(ctx, communityID, sp.ID, creatorID, sp.Name, sp.SpaceType); err != nil {
			slog.Warn("failed to publish community.space.created event", "error", err)
		}
	}

	return sp, nil
}

func (s *Service) ListSpaces(ctx context.Context, communityID uuid.UUID) ([]store.CommunitySpace, error) {
	return s.store.ListSpaces(ctx, communityID)
}

type UpdateSpaceParams struct {
	Name            *string    `json:"name"`
	Description     *string    `json:"description"`
	SortOrder       *int       `json:"sort_order"`
	LinkedGroupID   *uuid.UUID `json:"linked_group_id"`
	LinkedChannelID *uuid.UUID `json:"linked_channel_id"`
}

func (s *Service) UpdateSpace(ctx context.Context, communityID, spaceID, actorID uuid.UUID, params UpdateSpaceParams) (*store.CommunitySpace, error) {
	member, err := s.store.GetMember(ctx, communityID, actorID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "space_manager") {
		return nil, fmt.Errorf("forbidden: only space managers and above can update spaces")
	}

	sp, err := s.store.GetSpace(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	if sp.CommunityID != communityID {
		return nil, fmt.Errorf("space not found")
	}

	if params.Name != nil {
		sp.Name = *params.Name
	}
	if params.Description != nil {
		sp.Description = *params.Description
	}
	if params.SortOrder != nil {
		sp.SortOrder = *params.SortOrder
	}
	if params.LinkedGroupID != nil {
		sp.LinkedGroupID = params.LinkedGroupID
	}
	if params.LinkedChannelID != nil {
		sp.LinkedChannelID = params.LinkedChannelID
	}

	if err := s.store.UpdateSpace(ctx, sp); err != nil {
		return nil, fmt.Errorf("failed to update space: %w", err)
	}

	return sp, nil
}

func (s *Service) DeleteSpace(ctx context.Context, communityID, spaceID, actorID uuid.UUID) error {
	member, err := s.store.GetMember(ctx, communityID, actorID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "admin") {
		return fmt.Errorf("forbidden: only admins and above can delete spaces")
	}

	sp, err := s.store.GetSpace(ctx, spaceID)
	if err != nil {
		return err
	}
	if sp.CommunityID != communityID {
		return fmt.Errorf("space not found")
	}

	if err := s.store.DeleteSpace(ctx, spaceID); err != nil {
		return fmt.Errorf("failed to delete space: %w", err)
	}

	if err := s.store.IncrementSpaceCount(ctx, communityID, -1); err != nil {
		slog.Warn("failed to decrement space count", "error", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishSpaceRemoved(ctx, communityID, spaceID, actorID); err != nil {
			slog.Warn("failed to publish community.space.removed event", "error", err)
		}
	}

	return nil
}

func (s *Service) QuarantineSpace(ctx context.Context, communityID, spaceID, actorID uuid.UUID, reason string) error {
	member, err := s.store.GetMember(ctx, communityID, actorID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "moderator") {
		return fmt.Errorf("forbidden: only moderators and above can quarantine spaces")
	}

	sp, err := s.store.GetSpace(ctx, spaceID)
	if err != nil {
		return err
	}
	if sp.CommunityID != communityID {
		return fmt.Errorf("space not found")
	}

	if err := s.store.QuarantineSpace(ctx, spaceID, true); err != nil {
		return fmt.Errorf("failed to quarantine space: %w", err)
	}

	// Log modlog entry
	entry := &store.CommunityModlogEntry{
		ID:          uuid.New(),
		CommunityID: communityID,
		ActorID:     actorID,
		Action:      "quarantine_space",
		TargetType:  "space",
		TargetID:    spaceID,
		Reason:      &reason,
	}
	// Audit HC6: previously this swallowed the error with a Warn log so
	// a ban/unban/role-change without an audit trail looked successful.
	// The audit log is safety-critical — fail the action so the actor
	// can retry and operators see the gap in metrics.
	if err := s.store.AddModlogEntry(ctx, entry); err != nil {
		return fmt.Errorf("failed to record moderation audit log: %w", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishSpaceQuarantined(ctx, communityID, spaceID, actorID); err != nil {
			slog.Warn("failed to publish community.space.quarantined event", "error", err)
		}
	}

	return nil
}

// --- Join Requests ---

func (s *Service) ListJoinRequests(ctx context.Context, communityID, actorID uuid.UUID, limit, offset int) ([]store.CommunityJoinRequest, error) {
	member, err := s.store.GetMember(ctx, communityID, actorID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "moderator") {
		return nil, fmt.Errorf("forbidden: only moderators and above can view join requests")
	}

	return s.store.ListJoinRequests(ctx, communityID, limit, offset)
}

func (s *Service) ApproveRequest(ctx context.Context, communityID, requestID, actorID uuid.UUID) error {
	member, err := s.store.GetMember(ctx, communityID, actorID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "moderator") {
		return fmt.Errorf("forbidden: only moderators and above can approve requests")
	}

	jr, err := s.store.GetJoinRequest(ctx, requestID)
	if err != nil {
		return err
	}
	if jr.CommunityID != communityID {
		return fmt.Errorf("join request not found")
	}
	// Audit HC3: join requests previously had no expiration or
	// single-use semantics. A stale "pending" row from months ago
	// could be approved into a fresh membership. Reject anything
	// older than 30 days, and refuse to approve non-pending rows
	// so a malicious moderator can't double-approve the same
	// request to undo a prior rejection.
	if jr.Status != "pending" {
		return fmt.Errorf("join request is not pending (status=%s)", jr.Status)
	}
	if time.Since(jr.CreatedAt) > 30*24*time.Hour {
		return fmt.Errorf("join request expired")
	}

	if err := s.store.ApproveRequest(ctx, requestID, actorID); err != nil {
		return err
	}

	// Add user as member
	newMember := &store.CommunityMember{
		CommunityID: communityID,
		UserID:      jr.UserID,
		Role:        "member",
	}
	inserted, err := s.store.AddMember(ctx, newMember)
	if err != nil {
		return fmt.Errorf("failed to add member after approval: %w", err)
	}
	if inserted {
		if err := s.store.IncrementMemberCount(ctx, communityID, 1); err != nil {
			slog.Warn("failed to increment member count", "error", err)
		}
	}

	// Auto-follow default spaces for approved member
	if err := s.store.AutoFollowDefaultSpaces(ctx, communityID, jr.UserID); err != nil {
		slog.Warn("failed to auto-follow default spaces after approval", "community_id", communityID, "user_id", jr.UserID, "error", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishMemberJoined(ctx, communityID, jr.UserID); err != nil {
			slog.Warn("failed to publish community.member.joined event", "error", err)
		}
	}

	return nil
}

func (s *Service) RejectRequest(ctx context.Context, communityID, requestID, actorID uuid.UUID) error {
	member, err := s.store.GetMember(ctx, communityID, actorID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "moderator") {
		return fmt.Errorf("forbidden: only moderators and above can reject requests")
	}

	jr, err := s.store.GetJoinRequest(ctx, requestID)
	if err != nil {
		return err
	}
	if jr.CommunityID != communityID {
		return fmt.Errorf("join request not found")
	}
	// Audit HC3: same single-use gate as ApproveRequest.
	if jr.Status != "pending" {
		return fmt.Errorf("join request is not pending (status=%s)", jr.Status)
	}

	return s.store.RejectRequest(ctx, requestID, actorID)
}

// --- Modlog ---

func (s *Service) GetModLog(ctx context.Context, communityID, actorID uuid.UUID, limit, offset int) ([]store.CommunityModlogEntry, error) {
	member, err := s.store.GetMember(ctx, communityID, actorID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "moderator") {
		return nil, fmt.Errorf("forbidden: only moderators and above can view the modlog")
	}

	return s.store.ListModlog(ctx, communityID, limit, offset)
}

// --- Queries ---

func (s *Service) GetMyCommunities(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.Community, error) {
	return s.store.GetMyCommunities(ctx, userID, limit, offset)
}

func (s *Service) DiscoverCommunities(ctx context.Context, limit, offset int) ([]store.Community, error) {
	return s.store.DiscoverCommunities(ctx, limit, offset)
}

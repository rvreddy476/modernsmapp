package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	communityevents "github.com/atpost/community-service/internal/events"
	"github.com/atpost/community-service/internal/mediaclaims"
	"github.com/atpost/community-service/internal/store"
	"github.com/atpost/shared/counters"
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

// AuthorizeMembership returns the caller's row if userID is a member of
// communityID (not pending, banned or suspended); ErrNotCommunityMember /
// ErrMemberBanned / ErrCommunityNotFound otherwise. It is the access gate in
// write mode (access.go).
func (s *Service) AuthorizeMembership(ctx context.Context, communityID, userID uuid.UUID) (*store.CommunityMember, error) {
	acc, err := s.authorize(ctx, communityID, &userID, accessWrite)
	if err != nil {
		return nil, err
	}
	if acc.member == nil {
		// The owner with no member row (older databases): still the owner.
		return &store.CommunityMember{CommunityID: communityID, UserID: userID, Role: acc.role}, nil
	}
	m := *acc.member
	m.Role = acc.role
	return &m, nil
}

// AuthorizeRole returns nil if userID is a member of communityID with a
// role at least `minRole`; otherwise returns ErrInsufficientRole (or the
// gate's refusal). Used for moderation actions: pin, feature, ban, wiki
// edit, etc.
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

// AuthorizeViewer returns nil if userID may view content in communityID:
// anyone for a public type (store.PublicCommunityTypes), members only
// otherwise. Pass nil for unauthenticated callers. It is the access gate in
// read mode (access.go).
func (s *Service) AuthorizeViewer(ctx context.Context, communityID uuid.UUID, userID *uuid.UUID) error {
	_, err := s.authorize(ctx, communityID, userID, accessRead)
	return err
}

type Service struct {
	store    *store.Store
	rdb      *redis.Client
	producer *communityevents.Producer
	// memberCounter shards community member counts across Redis so a
	// 10M-member community no longer contends on a single
	// communities.member_count UPDATE row per join. Nil-safe — if no
	// Redis is configured the service falls back to per-event PG
	// UPDATE (the legacy hot-row path) so the dev loop still works.
	memberCounter *counters.Counter
	// media is the attach gate and claim protocol for a community's avatar
	// and banner (media_claims.go). Nil refuses every write that adds media:
	// fail closed.
	media *mediaclaims.Gate
}

func New(s *store.Store, rdb *redis.Client) *Service {
	svc := &Service{store: s, rdb: rdb}
	if rdb != nil {
		svc.memberCounter = counters.New(rdb, counters.Config{
			EntityKind: "community_member_count",
			Shards:     32,
		})
	}
	return svc
}

// MemberCounter exposes the sharded counter so cmd/server can attach a
// flush worker. Returns nil when Redis isn't configured.
func (s *Service) MemberCounter() *counters.Counter {
	return s.memberCounter
}

// adjustMemberCount fans an increment/decrement to the sharded Redis
// counter when available, otherwise falls back to the legacy per-event
// PG UPDATE. Failure inside the Redis path is logged but not fatal —
// the hourly reconciler (internal/reconcile) backfills any drift from
// community_members the next tick.
func (s *Service) adjustMemberCount(ctx context.Context, communityID uuid.UUID, delta int64) error {
	if s.memberCounter != nil {
		if err := s.memberCounter.Inc(ctx, communityID.String(), delta); err != nil {
			slog.Warn("sharded member counter inc failed; falling back to PG",
				"community_id", communityID, "delta", delta, "err", err)
			return s.store.IncrementMemberCount(ctx, communityID, int(delta))
		}
		return nil
	}
	return s.store.IncrementMemberCount(ctx, communityID, int(delta))
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

	// A private community defaults to invite-only and is never open
	// (access.go, joinModeFor).
	jm, err := joinModeFor(ct, params.JoinMode)
	if err != nil {
		return nil, err
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

	// The avatar and banner must be the owner's own finished uploads, and
	// are claimed before the row names them (media_claims.go).
	if err := s.media.Attach(ctx, ownerID, communityMediaChanges(c.ID, nil, nil, c.AvatarMediaID, c.BannerMediaID), func() error {
		if err := s.store.CreateCommunity(ctx, c); err != nil {
			return fmt.Errorf("failed to create community: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
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
		if err := s.producer.PublishCommunityCreated(ctx, c); err != nil {
			slog.Warn("failed to publish community.created event", "error", err)
		}
	}

	return c, nil
}

type CommunityWithMembership struct {
	*store.Community
	ViewerRole string `json:"viewer_role,omitempty"`
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
	if _, err := s.authorizeRoleOr(ctx, communityID, actorID, "admin", errors.New("forbidden: only admins and above can update the community")); err != nil {
		return nil, err
	}

	// Every save rewrites the avatar and banner from this read, so every save
	// is serialised with the other writers of this row's media
	// (media_claims.go), not only the ones that change them.
	unlock, err := s.lockMediaHolder(ctx, communityID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	c, err := s.store.GetCommunityByID(ctx, communityID)
	if err != nil {
		return nil, err
	}
	prevAvatar, prevBanner := c.AvatarMediaID, c.BannerMediaID

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
	if err := settleJoinMode(c, params.JoinMode != nil); err != nil {
		return nil, err
	}

	// A new avatar or banner must be the actor's own finished upload and is
	// claimed before the row names it; the one it replaces is released only
	// after the row stops naming it.
	if err := s.media.Attach(ctx, actorID, communityMediaChanges(c.ID, prevAvatar, prevBanner, c.AvatarMediaID, c.BannerMediaID), func() error {
		if err := s.store.UpdateCommunity(ctx, c); err != nil {
			return fmt.Errorf("failed to update community: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if s.producer != nil {
		if err := s.producer.PublishCommunityUpdated(ctx, c, actorID); err != nil {
			slog.Warn("failed to publish community.updated event", "error", err)
		}
	}

	return c, nil
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
	if _, err := s.authorizeRoleOr(ctx, communityID, creatorID, "space_manager", errors.New("forbidden: only space managers and above can create spaces")); err != nil {
		return nil, err
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

type UpdateSpaceParams struct {
	Name            *string    `json:"name"`
	Description     *string    `json:"description"`
	SortOrder       *int       `json:"sort_order"`
	LinkedGroupID   *uuid.UUID `json:"linked_group_id"`
	LinkedChannelID *uuid.UUID `json:"linked_channel_id"`
}

func (s *Service) UpdateSpace(ctx context.Context, communityID, spaceID, actorID uuid.UUID, params UpdateSpaceParams) (*store.CommunitySpace, error) {
	if _, err := s.authorizeRoleOr(ctx, communityID, actorID, "space_manager", errors.New("forbidden: only space managers and above can update spaces")); err != nil {
		return nil, err
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
	if _, err := s.authorizeRoleOr(ctx, communityID, actorID, "admin", errors.New("forbidden: only admins and above can delete spaces")); err != nil {
		return err
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
	if _, err := s.authorizeRoleOr(ctx, communityID, actorID, "moderator", errors.New("forbidden: only moderators and above can quarantine spaces")); err != nil {
		return err
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
	if _, err := s.authorizeRoleOr(ctx, communityID, actorID, "moderator", errors.New("forbidden: only moderators and above can view join requests")); err != nil {
		return nil, err
	}

	return s.store.ListJoinRequests(ctx, communityID, limit, offset)
}

func (s *Service) ApproveRequest(ctx context.Context, communityID, requestID, actorID uuid.UUID) error {
	if _, err := s.authorizeRoleOr(ctx, communityID, actorID, "moderator", errors.New("forbidden: only moderators and above can approve requests")); err != nil {
		return err
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
		if err := s.adjustMemberCount(ctx, communityID, 1); err != nil {
			slog.Warn("failed to increment member count", "error", err)
		}
	}
	s.invalidateMemberCache(ctx, communityID, jr.UserID)

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
	if _, err := s.authorizeRoleOr(ctx, communityID, actorID, "moderator", errors.New("forbidden: only moderators and above can reject requests")); err != nil {
		return err
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
	if _, err := s.authorizeRoleOr(ctx, communityID, actorID, "moderator", errors.New("forbidden: only moderators and above can view the modlog")); err != nil {
		return nil, err
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

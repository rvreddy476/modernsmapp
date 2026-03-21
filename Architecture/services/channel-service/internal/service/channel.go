package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	channelevents "github.com/atpost/channel-service/internal/events"
	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var validChannelTypes = map[string]bool{
	"public": true, "private": true, "creator": true, "brand": true,
	"education": true, "official": true, "topic": true, "paid": true,
}

var validCommentModes = map[string]bool{
	"enabled": true, "moderated": true, "subscribers_only": true, "disabled": true,
}

var validReactionModes = map[string]bool{
	"enabled": true, "disabled": true,
}

var validMemberRoles = map[string]bool{
	"owner": true, "admin": true, "editor": true, "moderator": true, "subscriber": true, "banned": true,
}

var validUpdateTypes = map[string]bool{
	"announcement": true, "image": true, "video": true, "audio": true,
	"poll": true, "event": true, "commerce": true, "alert": true, "digest": true,
}

func roleLevel(role string) int {
	switch role {
	case "owner":
		return 5
	case "admin":
		return 4
	case "editor":
		return 3
	case "moderator":
		return 2
	case "subscriber":
		return 1
	default:
		return 0
	}
}

func isAtLeast(userRole, requiredRole string) bool {
	return roleLevel(userRole) >= roleLevel(requiredRole)
}

type Service struct {
	store    *store.Store
	rdb      *redis.Client
	producer *channelevents.Producer
}

func New(s *store.Store, rdb *redis.Client) *Service {
	return &Service{store: s, rdb: rdb}
}

func (s *Service) SetProducer(p *channelevents.Producer) {
	s.producer = p
}

// --- Channel CRUD ---

type CreateChannelParams struct {
	Handle                string     `json:"handle"`
	Name                  string     `json:"name"`
	Description           string     `json:"description"`
	AvatarMediaID         *uuid.UUID `json:"avatar_media_id"`
	BannerMediaID         *uuid.UUID `json:"banner_media_id"`
	ChannelType           string     `json:"channel_type"`
	Category              string     `json:"category"`
	Language              string     `json:"language"`
	CommentMode           string     `json:"comment_mode"`
	ReactionMode          string     `json:"reaction_mode"`
	ForwardAllowed        *bool      `json:"forward_allowed"`
	PaidAccess            bool       `json:"paid_access"`
	SubscriptionPriceCents int       `json:"subscription_price_cents"`
}

func (s *Service) CreateChannel(ctx context.Context, ownerID uuid.UUID, params CreateChannelParams) (*store.BroadcastChannel, error) {
	// Validate required fields
	if params.Name == "" {
		return nil, fmt.Errorf("invalid: name is required")
	}
	if params.Handle == "" {
		return nil, fmt.Errorf("invalid: handle is required")
	}
	if len(params.Handle) < 3 || len(params.Handle) > 30 {
		return nil, fmt.Errorf("invalid: handle must be between 3 and 30 characters")
	}

	// Validate channel type
	ct := params.ChannelType
	if ct == "" {
		ct = "public"
	}
	if !validChannelTypes[ct] {
		return nil, fmt.Errorf("invalid: channel_type is not valid")
	}

	cm := params.CommentMode
	if cm == "" {
		cm = "enabled"
	}
	if !validCommentModes[cm] {
		return nil, fmt.Errorf("invalid: comment_mode is not valid")
	}

	rm := params.ReactionMode
	if rm == "" {
		rm = "enabled"
	}
	if !validReactionModes[rm] {
		return nil, fmt.Errorf("invalid: reaction_mode is not valid")
	}

	// Rate limit: 5 channels per day
	since := time.Now().Add(-24 * time.Hour)
	count, err := s.store.CountChannelsByOwner(ctx, ownerID, since)
	if err != nil {
		return nil, fmt.Errorf("failed to check rate limit: %w", err)
	}
	if count >= 5 {
		return nil, fmt.Errorf("rate_limited: you can create at most 5 channels per day")
	}

	forwardAllowed := true
	if params.ForwardAllowed != nil {
		forwardAllowed = *params.ForwardAllowed
	}

	ch := &store.BroadcastChannel{
		ID:                     uuid.New(),
		OwnerID:                ownerID,
		Handle:                 params.Handle,
		Name:                   params.Name,
		Description:            params.Description,
		AvatarMediaID:          params.AvatarMediaID,
		BannerMediaID:          params.BannerMediaID,
		ChannelType:            ct,
		Category:               params.Category,
		Language:               params.Language,
		CommentMode:            cm,
		ReactionMode:           rm,
		ForwardAllowed:         forwardAllowed,
		PaidAccess:             params.PaidAccess,
		SubscriptionPriceCents: params.SubscriptionPriceCents,
		PostScheduleEnabled:    true,
		SubscriberCountVisible: true,
		AllowPreviewPosts:      3,
		SubscriberCount:        1, // owner counts
		Status:                 "active",
	}

	if err := s.store.CreateChannel(ctx, ch); err != nil {
		return nil, fmt.Errorf("failed to create channel: %w", err)
	}

	// Add owner as member
	member := &store.ChannelMember{
		ChannelID: ch.ID,
		UserID:    ownerID,
		Role:      "owner",
		NotifyOn:  "all",
	}
	if err := s.store.AddMember(ctx, member); err != nil {
		slog.Warn("failed to add owner as member", "channel_id", ch.ID, "error", err)
	}

	// Publish event
	if s.producer != nil {
		if err := s.producer.PublishChannelCreated(ctx, ch.ID, ownerID, ch.Name, ch.ChannelType); err != nil {
			slog.Warn("failed to publish channel.created event", "error", err)
		}
	}

	return ch, nil
}

type ChannelWithMembership struct {
	*store.BroadcastChannel
	ViewerRole string `json:"viewer_role,omitempty"`
}

func (s *Service) GetChannel(ctx context.Context, channelID uuid.UUID, viewerID *uuid.UUID) (*ChannelWithMembership, error) {
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, err
	}

	result := &ChannelWithMembership{BroadcastChannel: ch}

	if viewerID != nil {
		// Check owner_id first as authoritative source
		if ch.OwnerID == *viewerID {
			result.ViewerRole = "admin"
		} else {
			member, err := s.store.GetMember(ctx, channelID, *viewerID)
			if err != nil {
				slog.Warn("failed to get member state", "error", err)
			}
			if member != nil {
				result.ViewerRole = member.Role
			}
		}
	}

	return result, nil
}

type UpdateChannelParams struct {
	Name                   *string    `json:"name"`
	Description            *string    `json:"description"`
	AvatarMediaID          *uuid.UUID `json:"avatar_media_id"`
	BannerMediaID          *uuid.UUID `json:"banner_media_id"`
	ChannelType            *string    `json:"channel_type"`
	Category               *string    `json:"category"`
	Language               *string    `json:"language"`
	CommentMode            *string    `json:"comment_mode"`
	ReactionMode           *string    `json:"reaction_mode"`
	ForwardAllowed         *bool      `json:"forward_allowed"`
	PaidAccess             *bool      `json:"paid_access"`
	SubscriptionPriceCents *int       `json:"subscription_price_cents"`
	PostScheduleEnabled    *bool      `json:"post_schedule_enabled"`
	SubscriberCountVisible *bool      `json:"subscriber_count_visible"`
	AllowPreviewPosts      *int       `json:"allow_preview_posts"`
}

func (s *Service) UpdateChannel(ctx context.Context, channelID, actorID uuid.UUID, params UpdateChannelParams) (*store.BroadcastChannel, error) {
	// Verify role
	member, err := s.store.GetMember(ctx, channelID, actorID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "admin") {
		return nil, fmt.Errorf("forbidden: only admins and above can update the channel")
	}

	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, err
	}

	// Apply updates
	if params.Name != nil {
		ch.Name = *params.Name
	}
	if params.Description != nil {
		ch.Description = *params.Description
	}
	if params.AvatarMediaID != nil {
		ch.AvatarMediaID = params.AvatarMediaID
	}
	if params.BannerMediaID != nil {
		ch.BannerMediaID = params.BannerMediaID
	}
	if params.ChannelType != nil {
		if !validChannelTypes[*params.ChannelType] {
			return nil, fmt.Errorf("invalid: channel_type is not valid")
		}
		ch.ChannelType = *params.ChannelType
	}
	if params.Category != nil {
		ch.Category = *params.Category
	}
	if params.Language != nil {
		ch.Language = *params.Language
	}
	if params.CommentMode != nil {
		if !validCommentModes[*params.CommentMode] {
			return nil, fmt.Errorf("invalid: comment_mode is not valid")
		}
		ch.CommentMode = *params.CommentMode
	}
	if params.ReactionMode != nil {
		if !validReactionModes[*params.ReactionMode] {
			return nil, fmt.Errorf("invalid: reaction_mode is not valid")
		}
		ch.ReactionMode = *params.ReactionMode
	}
	if params.ForwardAllowed != nil {
		ch.ForwardAllowed = *params.ForwardAllowed
	}
	if params.PaidAccess != nil {
		ch.PaidAccess = *params.PaidAccess
	}
	if params.SubscriptionPriceCents != nil {
		ch.SubscriptionPriceCents = *params.SubscriptionPriceCents
	}
	if params.PostScheduleEnabled != nil {
		ch.PostScheduleEnabled = *params.PostScheduleEnabled
	}
	if params.SubscriberCountVisible != nil {
		ch.SubscriberCountVisible = *params.SubscriberCountVisible
	}
	if params.AllowPreviewPosts != nil {
		ch.AllowPreviewPosts = *params.AllowPreviewPosts
	}

	if err := s.store.UpdateChannel(ctx, ch); err != nil {
		return nil, fmt.Errorf("failed to update channel: %w", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishChannelUpdated(ctx, channelID, actorID); err != nil {
			slog.Warn("failed to publish channel.updated event", "error", err)
		}
	}

	return ch, nil
}

func (s *Service) DeleteChannel(ctx context.Context, channelID, actorID uuid.UUID) error {
	member, err := s.store.GetMember(ctx, channelID, actorID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || member.Role != "owner" {
		return fmt.Errorf("forbidden: only the channel owner can delete the channel")
	}

	if err := s.store.DeleteChannel(ctx, channelID); err != nil {
		return fmt.Errorf("failed to delete channel: %w", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishChannelDeleted(ctx, channelID, actorID); err != nil {
			slog.Warn("failed to publish channel.deleted event", "error", err)
		}
	}

	return nil
}

// --- Subscription ---

func (s *Service) Subscribe(ctx context.Context, channelID, userID uuid.UUID) error {
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return err
	}

	// Check if already a member
	existing, err := s.store.GetMember(ctx, channelID, userID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if existing != nil {
		if existing.Role == "banned" {
			return fmt.Errorf("forbidden: you are banned from this channel")
		}
		return fmt.Errorf("already subscribed to this channel")
	}

	// Private channels could require approval, but for now allow direct subscribe
	_ = ch

	member := &store.ChannelMember{
		ChannelID: channelID,
		UserID:    userID,
		Role:      "subscriber",
		NotifyOn:  "all",
	}
	if err := s.store.AddMember(ctx, member); err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	if err := s.store.IncrementSubscriberCount(ctx, channelID, 1); err != nil {
		slog.Warn("failed to increment subscriber count", "error", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishChannelSubscribed(ctx, channelID, userID); err != nil {
			slog.Warn("failed to publish channel.subscribed event", "error", err)
		}
	}

	return nil
}

func (s *Service) Unsubscribe(ctx context.Context, channelID, userID uuid.UUID) error {
	member, err := s.store.GetMember(ctx, channelID, userID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil {
		return fmt.Errorf("not a member of this channel")
	}
	if member.Role == "owner" {
		return fmt.Errorf("forbidden: the owner cannot unsubscribe; delete the channel instead")
	}

	if err := s.store.RemoveMember(ctx, channelID, userID); err != nil {
		return fmt.Errorf("failed to unsubscribe: %w", err)
	}

	if err := s.store.IncrementSubscriberCount(ctx, channelID, -1); err != nil {
		slog.Warn("failed to decrement subscriber count", "error", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishChannelUnsubscribed(ctx, channelID, userID); err != nil {
			slog.Warn("failed to publish channel.unsubscribed event", "error", err)
		}
	}

	return nil
}

func (s *Service) MuteChannel(ctx context.Context, channelID, userID uuid.UUID, mutedUntil *time.Time) error {
	member, err := s.store.GetMember(ctx, channelID, userID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil {
		return fmt.Errorf("not a member of this channel")
	}

	return s.store.SetMutedUntil(ctx, channelID, userID, mutedUntil)
}

func (s *Service) ListSubscribers(ctx context.Context, channelID, actorID uuid.UUID, limit, offset int) ([]store.ChannelMember, error) {
	member, err := s.store.GetMember(ctx, channelID, actorID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "admin") {
		return nil, fmt.Errorf("forbidden: only admins and above can list subscribers")
	}

	return s.store.ListSubscribers(ctx, channelID, limit, offset)
}

// --- Updates ---

type CreateUpdateParams struct {
	UpdateType  string          `json:"update_type"`
	Title       *string         `json:"title"`
	Body        string          `json:"body"`
	MediaIDs    []uuid.UUID     `json:"media_ids"`
	Metadata    json.RawMessage `json:"metadata"`
	ScheduledAt *time.Time      `json:"scheduled_at"`
}

func (s *Service) CreateUpdate(ctx context.Context, channelID, authorID uuid.UUID, params CreateUpdateParams) (*store.ChannelUpdate, error) {
	member, err := s.store.GetMember(ctx, channelID, authorID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "editor") {
		return nil, fmt.Errorf("forbidden: only editors and above can create updates")
	}

	ut := params.UpdateType
	if ut == "" {
		ut = "announcement"
	}
	if !validUpdateTypes[ut] {
		return nil, fmt.Errorf("invalid: update_type is not valid")
	}

	status := "published"
	var publishedAt *time.Time
	if params.ScheduledAt != nil && params.ScheduledAt.After(time.Now()) {
		status = "scheduled"
	} else {
		now := time.Now()
		publishedAt = &now
	}

	mediaIDs := params.MediaIDs
	if mediaIDs == nil {
		mediaIDs = []uuid.UUID{}
	}

	u := &store.ChannelUpdate{
		ID:          uuid.New(),
		ChannelID:   channelID,
		AuthorID:    authorID,
		UpdateType:  ut,
		Title:       params.Title,
		Body:        params.Body,
		MediaIDs:    mediaIDs,
		Metadata:    params.Metadata,
		ScheduledAt: params.ScheduledAt,
		PublishedAt: publishedAt,
		Status:      status,
	}

	if err := s.store.CreateUpdate(ctx, u); err != nil {
		return nil, fmt.Errorf("failed to create update: %w", err)
	}

	if err := s.store.IncrementUpdateCount(ctx, channelID, 1); err != nil {
		slog.Warn("failed to increment update count", "error", err)
	}

	if status == "published" && s.producer != nil {
		if err := s.producer.PublishChannelUpdatePublished(ctx, channelID, u.ID, authorID); err != nil {
			slog.Warn("failed to publish channel.update.published event", "error", err)
		}
	}

	return u, nil
}

func (s *Service) ListUpdates(ctx context.Context, channelID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]store.ChannelUpdate, error) {
	statusFilter := "published"

	// Admins+ can see all statuses
	if viewerID != nil {
		member, err := s.store.GetMember(ctx, channelID, *viewerID)
		if err == nil && member != nil && isAtLeast(member.Role, "admin") {
			// For admins, still default to published but they could request others
			_ = member
		}
	}

	return s.store.ListUpdates(ctx, channelID, statusFilter, limit, offset)
}

func (s *Service) EditUpdate(ctx context.Context, channelID, updateID, actorID uuid.UUID, params CreateUpdateParams) (*store.ChannelUpdate, error) {
	member, err := s.store.GetMember(ctx, channelID, actorID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "editor") {
		return nil, fmt.Errorf("forbidden: only editors and above can edit updates")
	}

	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return nil, err
	}
	if u.ChannelID != channelID {
		return nil, fmt.Errorf("update not found")
	}

	if params.UpdateType != "" {
		if !validUpdateTypes[params.UpdateType] {
			return nil, fmt.Errorf("invalid: update_type is not valid")
		}
		u.UpdateType = params.UpdateType
	}
	if params.Title != nil {
		u.Title = params.Title
	}
	if params.Body != "" {
		u.Body = params.Body
	}
	if params.MediaIDs != nil {
		u.MediaIDs = params.MediaIDs
	}
	if params.Metadata != nil {
		u.Metadata = params.Metadata
	}
	if params.ScheduledAt != nil {
		u.ScheduledAt = params.ScheduledAt
	}

	if err := s.store.UpdateUpdate(ctx, u); err != nil {
		return nil, fmt.Errorf("failed to edit update: %w", err)
	}

	return u, nil
}

func (s *Service) DeleteUpdate(ctx context.Context, channelID, updateID, actorID uuid.UUID) error {
	member, err := s.store.GetMember(ctx, channelID, actorID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "admin") {
		return fmt.Errorf("forbidden: only admins and above can delete updates")
	}

	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return err
	}
	if u.ChannelID != channelID {
		return fmt.Errorf("update not found")
	}

	if err := s.store.DeleteUpdate(ctx, updateID); err != nil {
		return fmt.Errorf("failed to delete update: %w", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishChannelUpdateDeleted(ctx, channelID, updateID, actorID); err != nil {
			slog.Warn("failed to publish channel.update.deleted event", "error", err)
		}
	}

	return nil
}

// --- Queries ---

// GetMyChannels returns channels owned by the user AND channels they are subscribed to, with viewer_role.
func (s *Service) GetMyChannels(ctx context.Context, userID uuid.UUID, limit, offset int) ([]ChannelWithMembership, error) {
	// 1. Get owned channels
	owned, err := s.store.GetMyChannels(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	// 2. Get subscribed channels
	subscribed, err := s.store.GetSubscribedChannels(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}

	seen := make(map[uuid.UUID]bool)
	var result []ChannelWithMembership
	for i := range owned {
		seen[owned[i].ID] = true
		result = append(result, ChannelWithMembership{BroadcastChannel: &owned[i], ViewerRole: "admin"})
	}
	for i := range subscribed {
		if seen[subscribed[i].ID] {
			continue
		}
		role := s.store.GetMemberRole(ctx, subscribed[i].ID, userID)
		if role == "" {
			role = "subscriber"
		}
		result = append(result, ChannelWithMembership{BroadcastChannel: &subscribed[i], ViewerRole: role})
	}
	return result, nil
}

// DiscoverChannels returns discoverable channels with viewer_role for the given user.
func (s *Service) DiscoverChannels(ctx context.Context, viewerID *uuid.UUID, limit, offset int) ([]ChannelWithMembership, error) {
	channels, err := s.store.DiscoverChannels(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	result := make([]ChannelWithMembership, len(channels))
	for i := range channels {
		result[i] = ChannelWithMembership{BroadcastChannel: &channels[i]}
		if viewerID != nil {
			if channels[i].OwnerID == *viewerID {
				result[i].ViewerRole = "admin"
			} else {
				role := s.store.GetMemberRole(ctx, channels[i].ID, *viewerID)
				if role != "" {
					result[i].ViewerRole = role
				}
			}
		}
	}
	return result, nil
}

// --- Engagement ---

func (s *Service) SparkUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID, isSupernova bool) error {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return err
	}
	if u.ChannelID != channelID {
		return fmt.Errorf("update not found")
	}
	return s.store.SparkUpdate(ctx, updateID, userID, isSupernova)
}

func (s *Service) UnsparkUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID) error {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return err
	}
	if u.ChannelID != channelID {
		return fmt.Errorf("update not found")
	}
	return s.store.UnsparkUpdate(ctx, updateID, userID)
}

func (s *Service) StashUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID) error {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return err
	}
	if u.ChannelID != channelID {
		return fmt.Errorf("update not found")
	}
	return s.store.StashUpdate(ctx, updateID, userID)
}

func (s *Service) UnstashUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID) error {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return err
	}
	if u.ChannelID != channelID {
		return fmt.Errorf("update not found")
	}
	return s.store.UnstashUpdate(ctx, updateID, userID)
}

func (s *Service) EchoUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID, echoType string) error {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return err
	}
	if u.ChannelID != channelID {
		return fmt.Errorf("update not found")
	}

	// Enforce forward_allowed flag
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return err
	}
	if !ch.ForwardAllowed {
		return fmt.Errorf("forbidden: forwarding disabled")
	}

	if echoType == "" {
		echoType = "share"
	}
	if err := s.store.EchoUpdate(ctx, updateID, userID, echoType); err != nil {
		return err
	}

	// Fire-and-forget: publish Kafka event
	go func() {
		if s.producer != nil {
			if pubErr := s.producer.PublishChannelUpdateEchoed(context.Background(), channelID, updateID, userID, echoType); pubErr != nil {
				slog.Warn("failed to publish channel.update.echoed", "error", pubErr)
			}
		}
	}()

	return nil
}

func (s *Service) UnechoUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID) error {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return err
	}
	if u.ChannelID != channelID {
		return fmt.Errorf("update not found")
	}
	return s.store.UnechoUpdate(ctx, updateID, userID)
}

func (s *Service) RecordView(ctx context.Context, channelID, updateID, userID uuid.UUID) error {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return err
	}
	if u.ChannelID != channelID {
		return fmt.Errorf("update not found")
	}
	return s.store.RecordView(ctx, updateID, userID)
}

func (s *Service) ListComments(ctx context.Context, channelID, updateID uuid.UUID, sort string, limit, offset int) ([]store.UpdateComment, error) {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return nil, err
	}
	if u.ChannelID != channelID {
		return nil, fmt.Errorf("update not found")
	}
	return s.store.ListComments(ctx, updateID, sort, limit, offset)
}

func (s *Service) ListCommentsSince(ctx context.Context, channelID, updateID uuid.UUID, since time.Time, limit int) ([]store.UpdateComment, error) {
	_, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	return s.store.ListCommentsSince(ctx, updateID, since, limit)
}

func (s *Service) AddComment(ctx context.Context, channelID, updateID, userID uuid.UUID, body string, parentID *uuid.UUID) (*store.UpdateComment, error) {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return nil, err
	}
	if u.ChannelID != channelID {
		return nil, fmt.Errorf("update not found")
	}
	if body == "" {
		return nil, fmt.Errorf("invalid: comment body is required")
	}
	comment, err := s.store.AddComment(ctx, updateID, userID, body, parentID)
	if err != nil {
		return nil, err
	}

	// Fire-and-forget: publish realtime + Kafka event
	go func() {
		if s.producer != nil {
			parentStr := ""
			if parentID != nil {
				parentStr = parentID.String()
			}
			if pubErr := s.producer.PublishCommentCreated(context.Background(), channelID, updateID, comment.ID, userID, body, parentStr); pubErr != nil {
				slog.Warn("failed to publish channel.comment.created", "error", pubErr)
			}
		}
	}()

	return comment, nil
}

func (s *Service) DeleteComment(ctx context.Context, channelID, updateID, commentID, userID uuid.UUID) error {
	// Check if user is the channel owner (can delete any comment)
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return err
	}
	isOwner := ch.OwnerID == userID

	// Also check admin/moderator role
	if !isOwner {
		member, err := s.store.GetMember(ctx, channelID, userID)
		if err != nil {
			return fmt.Errorf("failed to check membership: %w", err)
		}
		if member != nil && isAtLeast(member.Role, "moderator") {
			isOwner = true
		}
	}

	if err := s.store.DeleteComment(ctx, commentID, userID, isOwner); err != nil {
		return err
	}

	// Fire-and-forget: publish realtime + Kafka event
	go func() {
		if s.producer != nil {
			if pubErr := s.producer.PublishCommentDeleted(context.Background(), channelID, updateID, commentID, userID); pubErr != nil {
				slog.Warn("failed to publish channel.comment.deleted", "error", pubErr)
			}
		}
	}()

	return nil
}

func (s *Service) PinComment(ctx context.Context, channelID, updateID, commentID, userID uuid.UUID) error {
	// Only admins+ can pin
	member, err := s.store.GetMember(ctx, channelID, userID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil || !isAtLeast(member.Role, "admin") {
		return fmt.Errorf("forbidden: only admins and above can pin comments")
	}
	return s.store.PinComment(ctx, commentID)
}

func (s *Service) VoteOnPoll(ctx context.Context, channelID, updateID, userID uuid.UUID, optionIndexes []int) error {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return err
	}
	if u.ChannelID != channelID {
		return fmt.Errorf("update not found")
	}
	if u.UpdateType != "poll" {
		return fmt.Errorf("invalid: this update is not a poll")
	}

	// Check if poll is expired via metadata
	if u.Metadata != nil {
		var meta map[string]interface{}
		if err := json.Unmarshal(u.Metadata, &meta); err == nil {
			if expiresStr, ok := meta["expires_at"].(string); ok {
				if expiresAt, err := time.Parse(time.RFC3339, expiresStr); err == nil {
					if time.Now().After(expiresAt) {
						return fmt.Errorf("invalid: poll has expired")
					}
				}
			}
			// Check single-select constraint
			if multiSelect, ok := meta["multi_select"].(bool); ok && !multiSelect {
				voted, err := s.store.HasUserVoted(ctx, updateID, userID)
				if err != nil {
					return fmt.Errorf("failed to check vote status: %w", err)
				}
				if voted {
					return fmt.Errorf("already voted on this poll")
				}
				if len(optionIndexes) > 1 {
					return fmt.Errorf("invalid: only one option allowed for single-select polls")
				}
			}
		}
	}

	if len(optionIndexes) == 0 {
		return fmt.Errorf("invalid: at least one option is required")
	}

	return s.store.VoteOnPoll(ctx, updateID, userID, optionIndexes)
}

func (s *Service) GetPollResults(ctx context.Context, channelID, updateID uuid.UUID, viewerID *uuid.UUID) (*store.PollResultsResponse, error) {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return nil, err
	}
	if u.ChannelID != channelID {
		return nil, fmt.Errorf("update not found")
	}

	results, err := s.store.GetPollResults(ctx, updateID)
	if err != nil {
		return nil, err
	}

	resp := &store.PollResultsResponse{Results: results}
	if viewerID != nil {
		voted, err := s.store.HasUserVoted(ctx, updateID, *viewerID)
		if err == nil {
			resp.UserVoted = voted
		}
	}
	return resp, nil
}

func (s *Service) RSVPEvent(ctx context.Context, channelID, updateID, userID uuid.UUID, status string) error {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return err
	}
	if u.ChannelID != channelID {
		return fmt.Errorf("update not found")
	}
	if u.UpdateType != "event" {
		return fmt.Errorf("invalid: this update is not an event")
	}

	validStatuses := map[string]bool{"going": true, "interested": true, "not_going": true}
	if !validStatuses[status] {
		return fmt.Errorf("invalid: status must be going, interested, or not_going")
	}

	return s.store.RSVPEvent(ctx, updateID, userID, status)
}

func (s *Service) ListAttendees(ctx context.Context, channelID, updateID uuid.UUID, status string, limit, offset int) ([]store.EventAttendee, error) {
	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return nil, err
	}
	if u.ChannelID != channelID {
		return nil, fmt.Errorf("update not found")
	}
	return s.store.ListAttendees(ctx, updateID, status, limit, offset)
}

// --- Schedule Worker ---

func (s *Service) RunScheduleWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	slog.Info("schedule worker started")
	for {
		select {
		case <-ctx.Done():
			slog.Info("schedule worker shutting down")
			return
		case <-ticker.C:
			published, err := s.store.PublishScheduledUpdates(ctx)
			if err != nil {
				slog.Warn("schedule worker: failed to publish scheduled updates", "error", err)
				continue
			}
			for _, u := range published {
				slog.Info("schedule worker: published update", "update_id", u.ID, "channel_id", u.ChannelID)
				if s.producer != nil {
					if err := s.producer.PublishChannelUpdatePublished(ctx, u.ChannelID, u.ID, u.AuthorID); err != nil {
						slog.Warn("schedule worker: failed to publish event", "update_id", u.ID, "error", err)
					}
				}
			}
		}
	}
}

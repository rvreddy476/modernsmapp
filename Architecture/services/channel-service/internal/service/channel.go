package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	channelevents "github.com/atpost/channel-service/internal/events"
	"github.com/atpost/channel-service/internal/store"
	"github.com/atpost/shared/counters"
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

// AuthError + helpers — audit CCh1-CCh4.
// Engagement endpoints (spark/stash/echo/view) and read endpoints
// (GetChannel, ListUpdates) previously trusted that the caller had a
// right to see/touch the channel. They didn't check banned roles or
// non-public channel types.
type AuthError struct{ Reason string }

func (e *AuthError) Error() string { return "channel auth: " + e.Reason }

var (
	ErrChannelNotFound     = &AuthError{Reason: "not_found"}
	ErrChannelMemberBanned = &AuthError{Reason: "banned"}
	ErrChannelNotSubscriber = &AuthError{Reason: "not_subscriber"}
)

// The read and engagement gates that used to live here (authorizeViewer,
// authorizeEngagement) are one gate now: authorize and gateUpdate in
// access.go (audit 2026-11-07, CH-M1).

type Service struct {
	store    *store.Store
	rdb      *redis.Client
	producer *channelevents.Producer
	// subscriberCounter is used ONLY for its dirty set: a join or leave
	// marks the channel, and the flush worker recounts the roster at most
	// once per interval (refreshSubscriberCount). That keeps a celebrity
	// channel's singleton row from being written on every join. Nil when
	// Redis isn't configured: every change then recounts at once.
	subscriberCounter *counters.Counter
	// stateSeam replaces the producer for channel.updated in tests.
	stateSeam channelStatePublisher
	// policy is the communities launch-safety configuration (invite-only
	// pilot, creator allowlist, kill switch). See policy.go; New() starts
	// from DefaultCommunityPolicy so a caller that never calls
	// WithCommunityPolicy still gets the pilot, not the open product.
	policy CommunityPolicy
	// media answers whether a media id an update attaches is the actor's own
	// confirmed upload (update_media.go). Nil refuses every update that
	// carries media: fail closed.
	media MediaLookup
	// claims registers the media a channel row holds with media-service, so
	// the uploader cannot delete it from under the row (media_claims.go).
	// Nil refuses every write that ADDS media: fail closed.
	claims MediaClaims
}

func New(s *store.Store, rdb *redis.Client) *Service {
	svc := &Service{store: s, rdb: rdb, policy: DefaultCommunityPolicy()}
	if rdb != nil {
		svc.subscriberCounter = counters.New(rdb, counters.Config{
			EntityKind: "channel_subscriber_count",
			Shards:     32,
		})
	}
	return svc
}

// SubscriberCounter exposes the dirty set so cmd/server can attach the
// flush worker (which recounts; see refreshSubscriberCount). Returns nil
// when Redis isn't configured.
func (s *Service) SubscriberCounter() *counters.Counter {
	return s.subscriberCounter
}

// refreshSubscriberCount brings broadcast_channels.subscriber_count back in
// line with the roster after a join, leave, ban or unban.
//
// The roster is the truth: subscriber_count is the number of channel_members
// rows that are not banned, the same number every read and the fan-out count.
// It used to be the SUM of Redis shard deltas, written over the column by the
// flush worker. Nothing ever seeded the shards from Postgres, so a channel's
// first join after creation wrote 1 over the owner's 1 (the owner vanished
// from the count), every channel with members from before the counter showed
// only the joins since, a Redis flush reset every count to zero, and an
// increment taken by the Postgres fallback was overwritten by the next flush.
//
// Now the delta is not counted anywhere. The channel is marked dirty and the
// flush worker recounts it (store.RecountSubscriberCounts) — one indexed count
// per dirty channel per interval, however many joins arrived, so the hot-row
// protection stays. Without Redis, or when marking fails, it recounts at once.
// Either way a wrong count heals on the channel's next change.
func (s *Service) refreshSubscriberCount(ctx context.Context, channelID uuid.UUID) error {
	if s.subscriberCounter != nil {
		err := s.subscriberCounter.MarkDirty(ctx, channelID.String())
		if err == nil {
			return nil
		}
		slog.Warn("subscriber count: marking the channel dirty failed; recounting now",
			"channel_id", channelID, "err", err)
	}
	return s.store.RecountSubscriberCounts(ctx, []uuid.UUID{channelID})
}

func (s *Service) SetProducer(p *channelevents.Producer) {
	s.producer = p
}

// --- Channel CRUD ---

type CreateChannelParams struct {
	Handle        string     `json:"handle"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
	BannerMediaID *uuid.UUID `json:"banner_media_id"`
	// Visibility (public|private) is the community wire field; it wins over
	// ChannelType when both are sent.
	Visibility            string     `json:"visibility"`
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
	// Chat-app pass: name <= 60, about <= 300, handle ^[a-z0-9_]{3,30}$
	// (unique), visibility public|private.
	name, err := validateCommunityName(params.Name)
	if err != nil {
		return nil, err
	}
	about, err := validateCommunityAbout(params.Description)
	if err != nil {
		return nil, err
	}
	handle, err := normalizeHandle(params.Handle)
	if err != nil {
		return nil, err
	}
	params.Name, params.Description, params.Handle = name, about, handle

	// Invite-only pilot (2026-09-12): creation is restricted to the
	// allowlist — and an EMPTY allowlist permits NOBODY, not everyone — an
	// omitted visibility defaults to PRIVATE (it used to default to
	// public), any publicly visible channel_type is refused outright, and
	// paid communities are out of the pilot entirely.
	if !s.policy.CreatorAllowed(ownerID) {
		return nil, ErrCreatorNotAllowlisted
	}
	// Paid is checked BEFORE the type resolution, because `paid` resolves
	// to private visibility and would otherwise sail through the
	// public-community refusal — the one type with money attached being the
	// one type the pilot gate could not see.
	if err := s.policy.GuardPaidCommunity(
		params.ChannelType, params.PaidAccess, params.SubscriptionPriceCents); err != nil {
		return nil, err
	}
	ct, err := s.policy.ResolveCreateChannelType(params.Visibility, params.ChannelType)
	if err != nil {
		return nil, err
	}
	// `visibility: "paid"` is not a thing, but a caller could still reach a
	// paid type through the resolution above if the vocabulary ever grows.
	// Re-check the RESOLVED type so the refusal cannot be bypassed by the
	// spelling used to request it.
	if err := s.policy.GuardPaidCommunity(ct, false, 0); err != nil {
		return nil, err
	}

	// Communities are broadcast-only: members cannot reply. Comments are
	// DISABLED unless the creator opts in explicitly.
	cm := params.CommentMode
	if cm == "" {
		cm = "disabled"
	}
	if !validCommentModes[cm] {
		return nil, fmt.Errorf("invalid: comment_mode is not valid")
	}
	if existing, err := s.store.GetChannelByHandle(ctx, handle); err == nil && existing != nil {
		return nil, fmt.Errorf("already taken: handle @%s is in use", handle)
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

	// Check, then claim, the picture and banner before the row exists
	// (update_media.go, media_claims.go).
	avatarAdd, _ := optionalMediaDiff(nil, ch.AvatarMediaID)
	bannerAdd, _ := optionalMediaDiff(nil, ch.BannerMediaID)
	if err := s.verifyChannelMedia(ctx, ownerID, avatarAdd, bannerAdd); err != nil {
		return nil, err
	}
	if err := s.writeWithClaims(ctx, []claimChange{
		{kind: claimKindAvatar, ref: ch.ID, add: avatarAdd},
		{kind: claimKindBanner, ref: ch.ID, add: bannerAdd},
	}, func() error {
		if err := s.store.CreateChannel(ctx, ch); err != nil {
			if strings.Contains(err.Error(), "broadcast_channels_handle_key") || strings.Contains(err.Error(), "duplicate key") {
				return fmt.Errorf("already taken: handle @%s is in use", handle)
			}
			return fmt.Errorf("failed to create channel: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// The owner's membership row is written inside CreateChannel's transaction,
	// so there is nothing to do here. It used to be a second call whose error
	// was only logged, which is how a channel could end up owned by nobody.

	// Publish event
	if s.producer != nil {
		if err := s.producer.PublishChannelCreated(ctx, channelSnapshotOf(ch)); err != nil {
			slog.Warn("failed to publish channel.created event", "error", err)
		}
	}

	return ch, nil
}

type ChannelWithMembership struct {
	*store.BroadcastChannel
	// ViewerRole: owner | admin | subscriber | banned | "" (not joined).
	ViewerRole string `json:"viewer_role,omitempty"`
	// Community wire fields (chat-app pass).
	Visibility  string `json:"visibility"`
	MemberCount int64  `json:"member_count"`
	ViewerMuted bool   `json:"viewer_muted"`
	// CanPost is true for the owner/admins; members never post.
	CanPost bool `json:"can_post"`
}

// withMembership decorates one channel row for the viewer. It is the
// one-row case of withMemberships.
func (s *Service) withMembership(ctx context.Context, ch *store.BroadcastChannel, viewerID *uuid.UUID) ChannelWithMembership {
	return s.withMemberships(ctx, []store.BroadcastChannel{*ch}, viewerID)[0]
}

// withMemberships decorates a page of channels for the viewer in TWO
// queries whatever the page size (audit 2026-11-07, CH-L1): one grouped
// roster count and one lookup of the viewer's rows. It used to run a
// COUNT(*) and a member lookup per row — about 40 queries for a 20-row
// /my or /discover page.
//
// member_count stays the LIVE roster rather than subscriber_count:
// subscriber_count is recounted from the same roster but only once per
// flush interval (refreshSubscriberCount), and the grouped count is exact
// at no extra cost per row. When the count query fails the stored value is
// shown rather than nothing.
func (s *Service) withMemberships(ctx context.Context, chs []store.BroadcastChannel, viewerID *uuid.UUID) []ChannelWithMembership {
	out := make([]ChannelWithMembership, len(chs))
	if len(chs) == 0 {
		return out
	}
	ids := make([]uuid.UUID, len(chs))
	for i := range chs {
		ids[i] = chs[i].ID
	}
	counts, err := s.store.CountSubscribersBatch(ctx, ids)
	if err != nil {
		slog.Warn("failed to count members", "error", err)
		counts = nil
	}
	var members map[uuid.UUID]store.ChannelMember
	if viewerID != nil {
		if members, err = s.store.MembersOf(ctx, *viewerID, ids); err != nil {
			slog.Warn("failed to get member state", "error", err)
		}
	}
	for i := range chs {
		var member *store.ChannelMember
		if m, ok := members[chs[i].ID]; ok {
			member = &m
		}
		live, counted := counts[chs[i].ID]
		if counts != nil && !counted {
			counted = true // the grouped count omits a channel with no rows
		}
		out[i] = decorateChannel(&chs[i], viewerID, member, live, counted)
	}
	return out
}

// decorateChannel is the pure part of withMemberships: role (the owner
// outranks the member row), mute state and the community wire fields.
func decorateChannel(ch *store.BroadcastChannel, viewerID *uuid.UUID, member *store.ChannelMember, liveCount int64, counted bool) ChannelWithMembership {
	out := ChannelWithMembership{
		BroadcastChannel: ch,
		Visibility:       VisibilityOf(ch.ChannelType),
		MemberCount:      ch.SubscriberCount,
	}
	if counted {
		out.MemberCount = liveCount
	}
	if viewerID == nil {
		return out
	}
	switch {
	case ch.OwnerID == *viewerID:
		out.ViewerRole = "owner"
	case member != nil:
		out.ViewerRole = member.Role
	}
	out.ViewerMuted = memberIsMuted(member)
	out.CanPost = out.ViewerRole == "owner" || out.ViewerRole == "admin"
	return out
}

// channelMetaCacheTTL bounds how stale a cached channel record may be.
// 60s is well within tolerance for channel metadata (name, bio, avatar)
// while absorbing the read load on a hot channel page; no explicit
// invalidation is needed — an edit shows within the window.
const channelMetaCacheTTL = 60 * time.Second

// cachedChannelByID is a Redis cache-aside over store.GetChannelByID. The
// channel record is viewer-independent, so it caches cleanly; the
// per-viewer role in GetChannel stays a live lookup.
func (s *Service) cachedChannelByID(ctx context.Context, channelID uuid.UUID) (*store.BroadcastChannel, error) {
	key := "channel:meta:" + channelID.String()
	if s.rdb != nil {
		if raw, err := s.rdb.Get(ctx, key).Bytes(); err == nil {
			var ch store.BroadcastChannel
			if json.Unmarshal(raw, &ch) == nil {
				return &ch, nil
			}
		}
	}
	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil || ch == nil {
		return ch, err
	}
	if s.rdb != nil {
		if data, mErr := json.Marshal(ch); mErr == nil {
			_ = s.rdb.Set(ctx, key, data, channelMetaCacheTTL).Err()
		}
	}
	return ch, nil
}

func (s *Service) GetChannel(ctx context.Context, channelID uuid.UUID, viewerID *uuid.UUID) (*ChannelWithMembership, error) {
	ch, err := s.cachedChannelByID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, ErrChannelNotFound
	}

	// The whole read decision lives in channelReadable (policy.go): the
	// suspension gate plus the CCh3 private-metadata gate. Only look the
	// viewer's role up when the channel is private and the viewer is not
	// the owner — the same shape as before, one query at most.
	isOwner := viewerID != nil && ch.OwnerID == *viewerID
	viewerRole := ""
	if viewerID != nil && !isOwner && VisibilityOf(ch.ChannelType) == "private" {
		viewerRole = s.store.GetMemberRole(ctx, channelID, *viewerID)
	}
	if err := channelReadable(ch, isOwner, viewerRole); err != nil {
		return nil, err
	}

	result := s.withMembership(ctx, ch, viewerID)
	return &result, nil
}

type UpdateChannelParams struct {
	Name          *string    `json:"name"`
	// The @handle. Settable after creation: the settings screen has always
	// shown it as an editable field, and it was the only one there that the
	// update endpoint did not accept — so it was silently discarded on every
	// save, which reads as "saving does not work".
	Handle        *string    `json:"handle"`
	Description   *string    `json:"description"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
	BannerMediaID *uuid.UUID `json:"banner_media_id"`
	// Visibility (public|private) wins over ChannelType when both are sent.
	Visibility             *string    `json:"visibility"`
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

	// Every save rewrites the picture and banner from this read, so every
	// save is serialised with the other writers of this row's media
	// (media_claims.go), not only the ones that change them.
	unlock, err := s.lockMediaHolder(ctx, channelID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	ch, err := s.store.GetChannelByID(ctx, channelID)
	if err != nil {
		return nil, err
	}

	// Apply updates
	if params.Name != nil {
		name, err := validateCommunityName(*params.Name)
		if err != nil {
			return nil, err
		}
		ch.Name = name
	}
	if params.Handle != nil {
		handle, err := normalizeHandle(*params.Handle)
		if err != nil {
			return nil, err
		}
		// Only when it actually changes: re-saving the settings form sends the
		// current handle back, and that must not collide with itself.
		if handle != ch.Handle {
			/*
				A FREE handle comes back as an ERROR here, not as (nil, nil):
				GetChannelByHandle reports "channel not found" when nothing
				matches. Treating that error as a failure made every available
				handle a 404 — the check refused exactly the handles it should
				have allowed. The create path has always ignored the error for
				this reason; this matches it.

				So only a successful lookup that actually returns a channel
				means taken.
			*/
			if existing, err := s.store.GetChannelByHandle(ctx, handle); err == nil && existing != nil {
				return nil, fmt.Errorf("already taken: handle @%s is in use", handle)
			}
			ch.Handle = handle
		}
	}
	if params.Description != nil {
		about, err := validateCommunityAbout(*params.Description)
		if err != nil {
			return nil, err
		}
		ch.Description = about
	}
	prevAvatar, prevBanner := ch.AvatarMediaID, ch.BannerMediaID
	if params.AvatarMediaID != nil {
		ch.AvatarMediaID = params.AvatarMediaID
	}
	if params.BannerMediaID != nil {
		ch.BannerMediaID = params.BannerMediaID
	}
	// Invite-only pilot (2026-09-12): the create path refuses public
	// communities, so the update path must refuse the same flip — the
	// audit found the asymmetry, where a raw `channel_type` on PUT could
	// take a private community public. Guard the RESOLVED type, so it
	// closes whether the caller sends `channel_type` or `visibility`.
	originalType := ch.ChannelType
	if params.ChannelType != nil {
		if !validChannelTypes[*params.ChannelType] {
			return nil, fmt.Errorf("invalid: channel_type is not valid")
		}
		ch.ChannelType = *params.ChannelType
	}
	if params.Visibility != nil {
		v, err := channelTypeForVisibility(*params.Visibility)
		if err != nil {
			return nil, err
		}
		if v != "" {
			ch.ChannelType = v
		}
	}
	if err := s.policy.GuardVisibilityChange(originalType, ch.ChannelType); err != nil {
		return nil, err
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

	// Paid is out of the pilot, so the UPDATE path must refuse it as firmly
	// as the create path. Checked on the RESOLVED row rather than on the
	// request, so turning a community paid is refused whether it arrives as
	// channel_type, as paid_access, or as a subscription price — and so
	// that an update touching only the name cannot carry an existing paid
	// community along (nothing paid exists to carry, and if one is found
	// later it is a scoped decision, not something this path should
	// silently keep alive).
	if err := s.policy.GuardPaidCommunity(
		ch.ChannelType, ch.PaidAccess, ch.SubscriptionPriceCents); err != nil {
		return nil, err
	}

	// A new picture or banner is claimed before the row names it; the one it
	// replaces is released only after the row stops naming it.
	avatarAdd, avatarDrop := optionalMediaDiff(prevAvatar, ch.AvatarMediaID)
	bannerAdd, bannerDrop := optionalMediaDiff(prevBanner, ch.BannerMediaID)
	if err := s.verifyChannelMedia(ctx, actorID, avatarAdd, bannerAdd); err != nil {
		return nil, err
	}
	if err := s.writeWithClaims(ctx, []claimChange{
		{kind: claimKindAvatar, ref: ch.ID, add: avatarAdd, drop: avatarDrop},
		{kind: claimKindBanner, ref: ch.ID, add: bannerAdd, drop: bannerDrop},
	}, func() error {
		if err := s.store.UpdateChannel(ctx, ch); err != nil {
			return fmt.Errorf("failed to update channel: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	s.announceChannelState(ctx, ch, &actorID)

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
	// The channel is gone for good (no restore path): its picture, banner
	// and every update's media stop being held (media_claims.go).
	s.releaseChannelClaims(ctx, channelID)

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
	if ch == nil || !channelIsServable(ch.Status) {
		return ErrChannelNotFound
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

	// Internal-only pilot (2026-09-12), two separate gates.
	//
	// First: participation itself is allowlisted, for EVERY channel type
	// including the legacy public ones. The creator allowlist governs
	// creation only, which leaves joining as the open door — and while the
	// product is internal-only, an unapproved user joining a legacy public
	// community is unapproved participation just the same. Existing members
	// are untouched; this refuses NEW joins only.
	if !s.policy.ParticipantAllowed(userID) {
		return ErrParticipantNotAllowlisted
	}
	// Second: a direct subscribe on a PRIVATE community is refused even for
	// an approved participant — joining goes through POST
	// /v1/broadcast-channels/invites/{code}/join. This replaces the
	// "private channels could require approval, but for now allow direct
	// subscribe" hole, which let anyone with the id join any private
	// channel.
	if err := s.policy.GuardDirectSubscribe(ch.ChannelType); err != nil {
		return err
	}

	member := &store.ChannelMember{
		ChannelID: channelID,
		UserID:    userID,
		Role:      "subscriber",
		NotifyOn:  "all",
	}
	inserted, err := s.store.AddMember(ctx, member)
	if errors.Is(err, store.ErrMemberBanned) {
		return ErrChannelMemberBanned // banned between the read above and the insert
	}
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	// Audit HCh1: only bump the counter when a NEW row landed. Concurrent
	// duplicate subscribes previously each ran the increment and drifted
	// the counter forever.
	if inserted {
		if err := s.refreshSubscriberCount(ctx, channelID); err != nil {
			slog.Warn("failed to refresh subscriber count", "error", err)
		}
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
	remove, err := guardLeave(member)
	if err != nil || !remove {
		return err
	}

	// RemoveMember never deletes a banned row (store), so a ban landing
	// between the read above and this delete still holds; nothing removed
	// means nothing to count or announce.
	removed, err := s.store.RemoveMember(ctx, channelID, userID)
	if err != nil {
		return fmt.Errorf("failed to unsubscribe: %w", err)
	}
	if !removed {
		return nil
	}

	if err := s.refreshSubscriberCount(ctx, channelID); err != nil {
		slog.Warn("failed to refresh subscriber count", "error", err)
	}

	if s.producer != nil {
		if err := s.producer.PublishChannelUnsubscribed(ctx, channelID, userID); err != nil {
			slog.Warn("failed to publish channel.unsubscribed event", "error", err)
		}
	}

	return nil
}

// guardLeave decides what leaving does to a member row (audit 2026-11-07,
// CH-M3). A BANNED row is never deleted by leaving: the ban lives on that
// row, so deleting it erased the ban, and the user rejoined through any
// live invite or a plain subscribe (and each round drifted
// subscriber_count down by one more, since BanMember already decremented).
// Leaving while banned answers success and keeps the row — the user is
// already out (hidden from /my, never notified), and the ban stays.
func guardLeave(member *store.ChannelMember) (remove bool, err error) {
	switch {
	case member == nil:
		return false, fmt.Errorf("not a member of this channel")
	case member.Role == "owner":
		return false, fmt.Errorf("forbidden: the owner cannot unsubscribe; delete the channel instead")
	case member.Role == "banned":
		return false, nil
	}
	return true, nil
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

// ListSubscribers is the non-banned roster. Superseded by ListMembers
// (moderation.go), which takes the same owner/admin gate and adds the
// `role` filter a moderator needs to see who is banned; kept as the
// unfiltered shorthand.
func (s *Service) ListSubscribers(ctx context.Context, channelID, actorID uuid.UUID, limit, offset int) ([]store.ChannelMember, error) {
	return s.ListMembers(ctx, channelID, actorID, "", limit, offset)
}

// --- Updates ---

type CreateUpdateParams struct {
	UpdateType  string          `json:"update_type"`
	Title       *string         `json:"title"`
	Body        string          `json:"body"`
	MediaIDs    []uuid.UUID     `json:"media_ids"`
	Metadata    json.RawMessage `json:"metadata"`
	ScheduledAt *time.Time      `json:"scheduled_at"`
	// Event (optional) makes this an event update; stored under
	// metadata.event and echoed back as `event` on every read.
	Event *EventInfo `json:"event"`
}

// CreateUpdate — owner/admin only (community rule: members never post).
func (s *Service) CreateUpdate(ctx context.Context, channelID, authorID uuid.UUID, params CreateUpdateParams) (*store.ChannelUpdate, error) {
	ch, err := s.requireAdmin(ctx, channelID, authorID)
	if err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(params.Body) > updateBodyMaxRunes {
		return nil, fmt.Errorf("invalid: body must be at most %d characters", updateBodyMaxRunes)
	}
	if params.Event != nil {
		if err := validateEvent(params.Event); err != nil {
			return nil, err
		}
		merged, err := mergeEventMetadata(params.Metadata, params.Event)
		if err != nil {
			return nil, err
		}
		params.Metadata = merged
		if params.UpdateType == "" {
			params.UpdateType = "event"
		}
	}
	if strings.TrimSpace(params.Body) == "" && len(params.MediaIDs) == 0 && params.Event == nil && (params.Title == nil || strings.TrimSpace(*params.Title) == "") {
		return nil, fmt.Errorf("invalid: an update needs text, media or an event")
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

	if err := s.verifyUpdateMedia(ctx, authorID, params.MediaIDs, nil); err != nil {
		return nil, err
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

	if err := s.writeWithClaims(ctx, []claimChange{
		{kind: claimKindUpdate, ref: u.ID, add: uniqueMedia(mediaIDs)},
	}, func() error {
		if err := s.store.CreateUpdate(ctx, u); err != nil {
			return fmt.Errorf("failed to create update: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if err := s.store.IncrementUpdateCount(ctx, channelID, 1); err != nil {
		slog.Warn("failed to increment update count", "error", err)
	}

	if status == "published" {
		if s.producer != nil {
			if err := s.producer.PublishChannelUpdatePublished(ctx, channelID, u.ID, authorID); err != nil {
				slog.Warn("failed to publish channel.update.published event", "error", err)
			}
		}
		// Subscribers are notified through the fan-out worker.
		s.emitFanout(ctx, ch, u)
	}

	return u, nil
}

// ListUpdates returns published updates, newest first, decorated for the
// viewer (event, reaction tallies, the viewer's own reaction). Returns
// limit+1 rows' worth of knowledge via hasMore so the handler can page.
func (s *Service) ListUpdates(ctx context.Context, channelID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]UpdateView, bool, error) {
	// Audit CCh2: previously returned every published update regardless
	// of channel type — private/paid channel posts were readable by any
	// caller. Run the read-side viewer gate before the listing.
	_, role, err := s.authorize(ctx, channelID, viewerID, accessRead)
	if err != nil {
		return nil, false, err
	}
	updates, err := s.store.ListUpdates(ctx, channelID, "published", limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(updates) > limit
	if hasMore {
		updates = updates[:limit]
	}
	views := s.decorateUpdates(ctx, viewerID, isAtLeast(role, "admin"), updates)
	return views, hasMore, nil
}

// EditUpdate — owner/admin only.
func (s *Service) EditUpdate(ctx context.Context, channelID, updateID, actorID uuid.UUID, params CreateUpdateParams) (*store.ChannelUpdate, error) {
	if _, err := s.requireAdmin(ctx, channelID, actorID); err != nil {
		return nil, err
	}

	// The edit rewrites media_ids from this read: serialise it with the
	// other writers of this update's media (media_claims.go).
	unlock, err := s.lockMediaHolder(ctx, updateID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	u, err := s.store.GetUpdate(ctx, updateID)
	if err != nil {
		return nil, err
	}
	if u.ChannelID != channelID {
		return nil, fmt.Errorf("update not found")
	}
	if utf8.RuneCountInString(params.Body) > updateBodyMaxRunes {
		return nil, fmt.Errorf("invalid: body must be at most %d characters", updateBodyMaxRunes)
	}
	if params.Event != nil {
		if err := validateEvent(params.Event); err != nil {
			return nil, err
		}
		base := u.Metadata
		if params.Metadata != nil {
			base = params.Metadata
		}
		merged, err := mergeEventMetadata(base, params.Event)
		if err != nil {
			return nil, err
		}
		params.Metadata = merged
	}
	if params.MediaIDs != nil {
		if err := s.verifyUpdateMedia(ctx, actorID, params.MediaIDs, u.MediaIDs); err != nil {
			return nil, err
		}
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
	prevMedia := u.MediaIDs
	if params.MediaIDs != nil {
		u.MediaIDs = params.MediaIDs
	}
	if params.Metadata != nil {
		u.Metadata = params.Metadata
	}
	if params.ScheduledAt != nil {
		u.ScheduledAt = params.ScheduledAt
	}

	// Added media is claimed before the row names it; removed media is
	// released only after the row stops naming it (media_claims.go).
	added, removed := mediaDiff(prevMedia, u.MediaIDs)
	if err := s.writeWithClaims(ctx, []claimChange{
		{kind: claimKindUpdate, ref: u.ID, add: added, drop: removed},
	}, func() error {
		if err := s.store.UpdateUpdate(ctx, u); err != nil {
			return fmt.Errorf("failed to edit update: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
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

	unlock, err := s.lockMediaHolder(ctx, updateID)
	if err != nil {
		return err
	}
	defer unlock()

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
	// Deleted updates are never shown again: release their media
	// (media_claims.go). Best effort; nil releases all the update holds.
	if len(u.MediaIDs) > 0 {
		s.release(ctx, claimKindUpdate, updateID, nil)
	}

	if s.producer != nil {
		if err := s.producer.PublishChannelUpdateDeleted(ctx, channelID, updateID, actorID); err != nil {
			slog.Warn("failed to publish channel.update.deleted event", "error", err)
		}
	}

	return nil
}

// --- Queries ---

// GetMyChannels returns the channels the user owns or belongs to (never one
// they are banned from), newest first, decorated with viewer_role.
//
// Audit 2026-11-07, CH-L4: this used to page owned and subscribed channels
// separately with the same limit/offset and concatenate them, so a page held
// up to 2×limit rows and page 2 skipped or repeated; and the subscribed read
// had no ban filter, so a banned user still saw the community. It is one
// query now (store.MyChannels) with a single ORDER BY and LIMIT.
func (s *Service) GetMyChannels(ctx context.Context, userID uuid.UUID, limit, offset int) ([]ChannelWithMembership, error) {
	channels, err := s.store.MyChannels(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	// Emergency disable (2026-09-12): the query lists active channels only;
	// this stays as the in-process backstop so a suspended community can
	// never reach the list, for its owner either.
	channels = servableChannels(channels)
	return s.withMemberships(ctx, channels, &userID), nil
}

// DiscoverChannels returns public communities ordered by member count then
// recency, optionally filtered by q (name/handle substring), decorated with
// the viewer's role. hasMore tells the handler whether to emit a cursor.
func (s *Service) DiscoverChannels(ctx context.Context, viewerID *uuid.UUID, q string, limit, offset int) ([]ChannelWithMembership, bool, error) {
	channels, err := s.store.DiscoverChannelsFiltered(ctx, q, limit, offset)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(channels) > limit
	if hasMore {
		channels = channels[:limit]
	}
	return s.withMemberships(ctx, channels, viewerID), hasMore, nil
}

// --- Engagement ---

// Every route below acts on one update and starts with gateUpdate
// (access.go): the caller may see the channel and is not banned, and the
// update belongs to the channel in the path. Reads pass accessRead, member
// writes accessWrite. Audit CCh1/CCh4 gated spark/stash/echo/view; audit
// 2026-11-07 CH-M1 found comments, the comment delta, polls, RSVPs and
// attendees outside any gate, so every route here now shares the one gate.

// SparkUpdate is DEPRECATED on channels: an update carries an emoji reaction
// and a share, nothing else. The route stays only until we know no shipped
// client calls it, and a spark no longer contributes to reaction_count —
// see the note on store.SparkUpdate.
func (s *Service) SparkUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID, isSupernova bool) error {
	if _, err := s.gateUpdate(ctx, channelID, updateID, &userID, accessWrite); err != nil {
		return err
	}
	return s.store.SparkUpdate(ctx, updateID, userID, isSupernova)
}

// UnsparkUpdate is DEPRECATED with SparkUpdate, and likewise leaves
// reaction_count alone.
func (s *Service) UnsparkUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID) error {
	if _, err := s.gateUpdate(ctx, channelID, updateID, &userID, accessWrite); err != nil {
		return err
	}
	return s.store.UnsparkUpdate(ctx, updateID, userID)
}

func (s *Service) StashUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID) error {
	if _, err := s.gateUpdate(ctx, channelID, updateID, &userID, accessWrite); err != nil {
		return err
	}
	return s.store.StashUpdate(ctx, updateID, userID)
}

func (s *Service) UnstashUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID) error {
	if _, err := s.gateUpdate(ctx, channelID, updateID, &userID, accessWrite); err != nil {
		return err
	}
	return s.store.UnstashUpdate(ctx, updateID, userID)
}

func (s *Service) EchoUpdate(ctx context.Context, channelID, updateID, userID uuid.UUID, echoType string) error {
	ga, err := s.gateUpdate(ctx, channelID, updateID, &userID, accessWrite)
	if err != nil {
		return err
	}
	// Enforce forward_allowed flag
	if !ga.ch.ForwardAllowed {
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
	if _, err := s.gateUpdate(ctx, channelID, updateID, &userID, accessWrite); err != nil {
		return err
	}
	return s.store.UnechoUpdate(ctx, updateID, userID)
}

func (s *Service) RecordView(ctx context.Context, channelID, updateID, userID uuid.UUID) error {
	if _, err := s.gateUpdate(ctx, channelID, updateID, &userID, accessWrite); err != nil {
		return err
	}
	return s.store.RecordView(ctx, updateID, userID)
}

func (s *Service) ListComments(ctx context.Context, channelID, updateID uuid.UUID, viewerID *uuid.UUID, sort string, limit, offset int) ([]store.UpdateComment, error) {
	if _, err := s.gateUpdate(ctx, channelID, updateID, viewerID, accessRead); err != nil {
		return nil, err
	}
	return s.store.ListComments(ctx, updateID, sort, limit, offset)
}

// ListCommentsSince is the /comments/delta route. It used to load the
// channel, discard it, and read the comments of whatever update id it was
// given, so any public channel id plus any update id returned that update's
// thread. The gate now ties the update to the channel in the path.
func (s *Service) ListCommentsSince(ctx context.Context, channelID, updateID uuid.UUID, viewerID *uuid.UUID, since time.Time, limit int) ([]store.UpdateComment, error) {
	if _, err := s.gateUpdate(ctx, channelID, updateID, viewerID, accessRead); err != nil {
		return nil, err
	}
	return s.store.ListCommentsSince(ctx, updateID, since, limit)
}

func (s *Service) AddComment(ctx context.Context, channelID, updateID, userID uuid.UUID, body string, parentID *uuid.UUID) (*store.UpdateComment, error) {
	ga, err := s.gateUpdate(ctx, channelID, updateID, &userID, accessWrite)
	if err != nil {
		return nil, err
	}
	if body == "" {
		return nil, fmt.Errorf("invalid: comment body is required")
	}
	if err := commentAllowed(ga.ch.CommentMode, ga.role); err != nil {
		return nil, err
	}
	// The store refuses a parent that is not a comment on this same update.
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

// commentAllowed applies comment_mode. Community rule: members cannot
// reply, so comment_mode=disabled (the default for new channels) refuses
// every comment, admins included. role is the gate's effectiveRole, so the
// owner counts as a member even without a member row.
func commentAllowed(commentMode, role string) error {
	switch commentMode {
	case "disabled":
		return fmt.Errorf("forbidden: comments are disabled on this channel")
	case "subscribers_only":
		if role == "" {
			return fmt.Errorf("forbidden: only subscribers can comment on this channel")
		}
	}
	return nil
}

// DeleteComment removes a comment of THIS update (audit 2026-11-07, CH-M2).
// Moderators and above of this channel may delete any comment on its
// updates; anyone else only their own. The authority used to come from the
// path channel while the DELETE matched the comment id alone, so the owner
// of any channel could delete any comment platform-wide. The store now
// matches the comment on (id, update_id), and the gate ties the update to
// the channel the authority came from.
func (s *Service) DeleteComment(ctx context.Context, channelID, updateID, commentID, userID uuid.UUID) error {
	ga, err := s.gateUpdate(ctx, channelID, updateID, &userID, accessRead)
	if err != nil {
		return err
	}
	moderator := isAtLeast(ga.role, "moderator")
	if err := s.store.DeleteComment(ctx, commentID, updateID, userID, moderator); err != nil {
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

// PinComment pins one comment of THIS update (admins and above of this
// channel). Scoped like DeleteComment (CH-M2): the store pins only a comment
// whose update_id is the update in the path.
func (s *Service) PinComment(ctx context.Context, channelID, updateID, commentID, userID uuid.UUID) error {
	ga, err := s.gateUpdate(ctx, channelID, updateID, &userID, accessRead)
	if err != nil {
		return err
	}
	if !isAtLeast(ga.role, "admin") {
		return fmt.Errorf("forbidden: only admins and above can pin comments")
	}
	return s.store.PinComment(ctx, commentID, updateID)
}

func (s *Service) VoteOnPoll(ctx context.Context, channelID, updateID, userID uuid.UUID, optionIndexes []int) error {
	ga, err := s.gateUpdate(ctx, channelID, updateID, &userID, accessWrite)
	if err != nil {
		return err
	}
	u := ga.u
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
	if _, err := s.gateUpdate(ctx, channelID, updateID, viewerID, accessRead); err != nil {
		return nil, err
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
	ga, err := s.gateUpdate(ctx, channelID, updateID, &userID, accessWrite)
	if err != nil {
		return err
	}
	if ga.u.UpdateType != "event" {
		return fmt.Errorf("invalid: this update is not an event")
	}

	validStatuses := map[string]bool{"going": true, "interested": true, "not_going": true}
	if !validStatuses[status] {
		return fmt.Errorf("invalid: status must be going, interested, or not_going")
	}

	return s.store.RSVPEvent(ctx, updateID, userID, status)
}

func (s *Service) ListAttendees(ctx context.Context, channelID, updateID uuid.UUID, viewerID *uuid.UUID, status string, limit, offset int) ([]store.EventAttendee, error) {
	if _, err := s.gateUpdate(ctx, channelID, updateID, viewerID, accessRead); err != nil {
		return nil, err
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

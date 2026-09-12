package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
)

// Communities invite-only pilot (2026-09-12): invite links for broadcast
// channels. Built natively here (channel-service does not import
// message-service) but deliberately the same shape and error vocabulary as
// the group links in chat-service, so the two products behave alike:
//
//	POST   /v1/broadcast-channels/{id}/invite-link   owner/admin → mint (rotates)
//	GET    /v1/broadcast-channels/{id}/invite-link   owner/admin → current live link
//	DELETE /v1/broadcast-channels/{id}/invite-link   owner/admin → revoke
//	GET    /v1/broadcast-channels/invites/{code}     anyone      → preview
//	POST   /v1/broadcast-channels/invites/{code}/join  any user  → join
//
// Error vocabulary: INVITE_NOT_FOUND 404, INVITE_NOT_LIVE 410,
// INVITE_REQUIRED 403 (a direct subscribe on a private community).

const (
	// InviteCodeLength is the code size in characters.
	InviteCodeLength = 10
	// inviteCodeAlphabet is RFC 4648 base32 (A-Z, 2-7): URL-safe, and no
	// look-alike digits 0/1/8.
	inviteCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	// DefaultInviteTTL is the expiry applied when the minter gives none.
	DefaultInviteTTL = 7 * 24 * time.Hour
	// maxInviteTTL bounds a caller-supplied expiry.
	maxInviteTTL = 90 * 24 * time.Hour
	// invitesPerHour bounds join attempts per user, mirroring the group
	// link's 20/hour quota.
	inviteJoinsPerHour = 20
)

// GenerateInviteCode returns InviteCodeLength base32 characters from
// crypto/rand.
func GenerateInviteCode() (string, error) {
	buf := make([]byte, InviteCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, InviteCodeLength)
	for i, b := range buf {
		out[i] = inviteCodeAlphabet[int(b)%len(inviteCodeAlphabet)]
	}
	return string(out), nil
}

// ValidInviteCode reports whether s has the shape of a code (length +
// alphabet), so the join path never hits the database for junk.
func ValidInviteCode(code string) bool {
	if len(code) != InviteCodeLength {
		return false
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		if !((c >= 'A' && c <= 'Z') || (c >= '2' && c <= '7')) {
			return false
		}
	}
	return true
}

// inviteIsLive reports whether an invite may still be consumed.
func inviteIsLive(inv *store.ChannelInvite, now time.Time) bool {
	if inv == nil {
		return false
	}
	if inv.RevokedAt != nil {
		return false
	}
	if inv.ExpiresAt != nil && !inv.ExpiresAt.After(now) {
		return false
	}
	if inv.MaxUses != nil && inv.Uses >= *inv.MaxUses {
		return false
	}
	return true
}

// InviteResponse is the wire shape of a minted/current invite.
type InviteResponse struct {
	Code      string     `json:"code"`
	URL       string     `json:"url,omitempty"`
	ChannelID uuid.UUID  `json:"channel_id"`
	CreatedBy uuid.UUID  `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	MaxUses   *int       `json:"max_uses"`
	Uses      int        `json:"uses"`
}

// InvitePreview is what a non-member sees before joining. It is deliberately
// the ONLY thing a private community discloses to an outsider: name, about,
// avatar, member count, and whether the caller is already in. No handle, no
// owner, no channel_type, no update counts.
type InvitePreview struct {
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	AvatarURL   string     `json:"avatar_url,omitempty"`
	MemberCount int64      `json:"member_count"`
	IsMember    bool       `json:"is_member"`
	IsLive      bool       `json:"is_live"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

func (s *Service) inviteResponse(inv *store.ChannelInvite) *InviteResponse {
	resp := &InviteResponse{
		Code:      inv.Code,
		ChannelID: inv.ChannelID,
		CreatedBy: inv.CreatedBy,
		CreatedAt: inv.CreatedAt,
		ExpiresAt: inv.ExpiresAt,
		MaxUses:   inv.MaxUses,
		Uses:      inv.Uses,
	}
	if s.policy.InviteBaseURL != "" {
		resp.URL = s.policy.InviteBaseURL + inv.Code
	}
	return resp
}

// CreateInvite mints a fresh invite, rotating any live one (owner/admin).
// ttl <= 0 uses the 7-day default; maxUses nil/0 means unlimited.
func (s *Service) CreateInvite(ctx context.Context, channelID, actorID uuid.UUID, ttl time.Duration, maxUses *int) (*InviteResponse, error) {
	if _, err := s.requireAdmin(ctx, channelID, actorID); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = DefaultInviteTTL
	}
	if ttl > maxInviteTTL {
		return nil, fmt.Errorf("invalid: expires_in_seconds cannot exceed %d days", int(maxInviteTTL.Hours()/24))
	}
	if maxUses != nil {
		if *maxUses < 0 {
			return nil, fmt.Errorf("invalid: max_uses cannot be negative")
		}
		if *maxUses == 0 {
			maxUses = nil
		}
	}
	code, err := GenerateInviteCode()
	if err != nil {
		return nil, fmt.Errorf("invite code generation failed: %w", err)
	}
	expiresAt := time.Now().Add(ttl)
	inv, err := s.store.CreateChannelInvite(ctx, channelID, actorID, code, &expiresAt, maxUses)
	if err != nil {
		return nil, err
	}
	return s.inviteResponse(inv), nil
}

// GetInvite returns the channel's live invite (owner/admin).
func (s *Service) GetInvite(ctx context.Context, channelID, actorID uuid.UUID) (*InviteResponse, error) {
	if _, err := s.requireAdmin(ctx, channelID, actorID); err != nil {
		return nil, err
	}
	inv, err := s.store.GetLiveChannelInvite(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if inv == nil || !inviteIsLive(inv, time.Now()) {
		return nil, ErrInviteNotFound
	}
	return s.inviteResponse(inv), nil
}

// RevokeInvite revokes the live invite (owner/admin). Idempotent.
func (s *Service) RevokeInvite(ctx context.Context, channelID, actorID uuid.UUID) error {
	if _, err := s.requireAdmin(ctx, channelID, actorID); err != nil {
		return err
	}
	_, err := s.store.RevokeChannelInvite(ctx, channelID)
	return err
}

// PreviewInvite describes the community behind a code without joining.
// Unauthenticated-safe: viewerID may be nil, in which case is_member is
// false. Never discloses more than InvitePreview carries.
func (s *Service) PreviewInvite(ctx context.Context, code string, viewerID *uuid.UUID) (*InvitePreview, error) {
	inv, err := s.lookupInvite(ctx, code)
	if err != nil {
		return nil, err
	}
	ch, err := s.store.GetChannelByID(ctx, inv.ChannelID)
	if err != nil || ch == nil {
		// A code whose channel is gone reads as an unknown code rather
		// than confirming that a community once existed.
		return nil, ErrInviteNotFound
	}
	if !channelIsServable(ch.Status) {
		return nil, ErrInviteNotFound
	}
	preview := &InvitePreview{
		Code:        inv.Code,
		Name:        ch.Name,
		Description: ch.Description,
		IsLive:      inviteIsLive(inv, time.Now()),
		ExpiresAt:   inv.ExpiresAt,
	}
	if n, err := s.store.CountSubscribers(ctx, ch.ID); err == nil {
		preview.MemberCount = n
	} else {
		preview.MemberCount = ch.SubscriberCount
	}
	if ch.AvatarMediaID != nil {
		preview.AvatarURL = ch.AvatarMediaID.String()
	}
	if viewerID != nil {
		role := s.store.GetMemberRole(ctx, ch.ID, *viewerID)
		preview.IsMember = role != "" && role != "banned"
	}
	return preview, nil
}

// JoinByInvite consumes one use and subscribes the caller. Idempotent for an
// existing member (no use is consumed). A banned user is refused.
func (s *Service) JoinByInvite(ctx context.Context, code string, userID uuid.UUID) (*ChannelWithMembership, error) {
	inv, err := s.lookupInvite(ctx, code)
	if err != nil {
		return nil, err
	}
	if !inviteIsLive(inv, time.Now()) {
		return nil, ErrInviteNotLive
	}
	if err := s.checkInviteJoinRate(ctx, userID); err != nil {
		return nil, err
	}
	ch, err := s.store.GetChannelByID(ctx, inv.ChannelID)
	if err != nil || ch == nil {
		return nil, ErrInviteNotFound
	}
	if !channelIsServable(ch.Status) {
		return nil, ErrInviteNotFound
	}

	existing, err := s.store.GetMember(ctx, ch.ID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if existing != nil {
		// A ban outranks any invite: an invite link must never be a way
		// back in for someone a moderator removed.
		if existing.Role == "banned" {
			return nil, ErrChannelMemberBanned
		}
		out := s.withMembership(ctx, ch, &userID)
		return &out, nil
	}

	// Consume FIRST (atomic against revoke/expiry/max_uses), then add. A
	// crash between the two burns one use — acceptable; the reverse order
	// could admit past max_uses.
	if _, err := s.store.ConsumeChannelInvite(ctx, code); err != nil {
		if errors.Is(err, store.ErrInviteLinkNotLive) {
			return nil, ErrInviteNotLive
		}
		return nil, err
	}
	inserted, err := s.store.AddMember(ctx, &store.ChannelMember{
		ChannelID: ch.ID, UserID: userID, Role: "subscriber", NotifyOn: "all",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to join: %w", err)
	}
	if inserted {
		if err := s.adjustSubscriberCount(ctx, ch.ID, 1); err != nil {
			slog.Warn("failed to increment subscriber count", "error", err)
		}
	}
	if s.producer != nil {
		if err := s.producer.PublishChannelSubscribed(ctx, ch.ID, userID); err != nil {
			slog.Warn("failed to publish channel.subscribed event", "error", err)
		}
	}
	out := s.withMembership(ctx, ch, &userID)
	return &out, nil
}

// lookupInvite resolves a code to a row, refusing junk without a query.
func (s *Service) lookupInvite(ctx context.Context, code string) (*store.ChannelInvite, error) {
	if !ValidInviteCode(code) {
		return nil, ErrInviteNotFound
	}
	inv, err := s.store.GetChannelInviteByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return nil, ErrInviteNotFound
	}
	return inv, nil
}

// checkInviteJoinRate bounds invite joins per user per hour. Redis-less
// deployments (dev loops) skip the quota rather than fail the join.
func (s *Service) checkInviteJoinRate(ctx context.Context, userID uuid.UUID) error {
	if s.rdb == nil {
		return nil
	}
	key := "channel:invite_join:" + userID.String()
	n, err := s.rdb.Incr(ctx, key).Result()
	if err != nil {
		slog.Warn("invite join rate limit check failed; allowing", "error", err)
		return nil
	}
	if n == 1 {
		_ = s.rdb.Expire(ctx, key, time.Hour).Err()
	}
	if n > inviteJoinsPerHour {
		return fmt.Errorf("rate_limited: too many invite joins, try again later")
	}
	return nil
}

// The status gate that used to live here as requireActiveAdmin now lives
// inside requireAdmin and requireOwner themselves (see community.go), so
// every owner/admin write path is gated rather than only the invite routes.

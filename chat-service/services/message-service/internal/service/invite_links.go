package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/chat-message-service/internal/ratelimit"
	"github.com/atpost/chat-message-service/internal/store/postgres"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// Chat-app pass (2026-09-05): shareable group invite links.
//
//   POST   /v1/chat/conversations/{id}/invite-link   owner/admin → mint (rotates)
//   GET    /v1/chat/conversations/{id}/invite-link   owner/admin → current live link
//   DELETE /v1/chat/conversations/{id}/invite-link   owner/admin → revoke
//   GET    /v1/chat/invites/{code}                   any user    → preview
//   POST   /v1/chat/invites/{code}/join              any user    → join
//
// Joining honours the same roster block check add-member does (a link never
// co-locates two users who block each other), the 1,024 cap, and a 20/hour
// per-user join quota.

const (
	// InviteCodeLength is the code size in characters.
	InviteCodeLength = 10
	// inviteCodeAlphabet is RFC 4648 base32 (A-Z, 2-7): URL-safe, no
	// look-alike digits 0/1/8.
	inviteCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	// DefaultInviteLinkTTL is the expiry applied when the minter gives none.
	DefaultInviteLinkTTL = 7 * 24 * time.Hour
	// maxInviteLinkTTL bounds a caller-supplied expiry.
	maxInviteLinkTTL = 90 * 24 * time.Hour
)

var (
	// ErrInviteLinkNotFound is returned for an unknown code.
	ErrInviteLinkNotFound = errors.New("invite link not found")
	// ErrInviteLinkNotLive is returned for a revoked, expired or exhausted link.
	ErrInviteLinkNotLive = postgres.ErrInviteLinkNotLive
	// ErrInviteJoinBlocked is returned when a block anywhere in the roster
	// prevents the join. Deliberately vague — block state is never disclosed.
	ErrInviteJoinBlocked = errors.New("this group cannot be joined")

	inviteJoinRateLimit = ratelimit.Limit{Action: "invite_join", Max: 20, Window: time.Hour}
)

type inviteLinkStore interface {
	CreateInviteLink(ctx context.Context, conversationID, createdBy uuid.UUID, code string, expiresAt *time.Time, maxUses *int) (*postgres.GroupInviteLink, error)
	GetLiveInviteLink(ctx context.Context, conversationID uuid.UUID) (*postgres.GroupInviteLink, error)
	GetInviteLinkByCode(ctx context.Context, code string) (*postgres.GroupInviteLink, error)
	RevokeInviteLink(ctx context.Context, conversationID uuid.UUID) (bool, error)
	ConsumeInviteLink(ctx context.Context, code string) (*postgres.GroupInviteLink, error)
}

func (s *Service) inviteLinkStore() inviteLinkStore {
	return s.convStore.(inviteLinkStore)
}

// InviteLinkResponse is the wire shape of a minted/current link.
type InviteLinkResponse struct {
	Code           string     `json:"code"`
	URL            string     `json:"url,omitempty"`
	ConversationID uuid.UUID  `json:"conversation_id"`
	CreatedBy      uuid.UUID  `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	MaxUses        *int       `json:"max_uses"`
	Uses           int        `json:"uses"`
}

// InvitePreview is what a non-member sees before joining.
type InvitePreview struct {
	Code           string     `json:"code"`
	ConversationID uuid.UUID  `json:"conversation_id"`
	Title          string     `json:"title"`
	Description    string     `json:"description,omitempty"`
	AvatarMediaID  *uuid.UUID `json:"avatar_media_id,omitempty"`
	AvatarURL      string     `json:"avatar_url,omitempty"`
	MemberCount    int        `json:"member_count"`
	ExpiresAt      *time.Time `json:"expires_at"`
	// IsLive is false for a revoked/expired/exhausted link; join will 410.
	IsLive bool `json:"is_live"`
	// IsMember is true when the caller already belongs to the group.
	IsMember bool `json:"is_member"`
}

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

func (s *Service) inviteLinkResponse(l *postgres.GroupInviteLink) *InviteLinkResponse {
	resp := &InviteLinkResponse{
		Code:           l.Code,
		ConversationID: l.ConversationID,
		CreatedBy:      l.CreatedBy,
		CreatedAt:      l.CreatedAt,
		ExpiresAt:      l.ExpiresAt,
		MaxUses:        l.MaxUses,
		Uses:           l.Uses,
	}
	if s.inviteLinkBaseURL != "" {
		resp.URL = s.inviteLinkBaseURL + l.Code
	}
	return resp
}

// CreateInviteLink mints a fresh link (rotating any live one). ttl <= 0 uses
// the 7-day default; maxUses nil/0 means unlimited.
func (s *Service) CreateInviteLink(ctx context.Context, actorID, conversationID uuid.UUID, ttl time.Duration, maxUses *int) (*InviteLinkResponse, error) {
	if _, _, err := s.requireGroupRole(ctx, conversationID, actorID, "owner", "admin"); err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = DefaultInviteLinkTTL
	}
	if ttl > maxInviteLinkTTL {
		return nil, fmt.Errorf("expires_in cannot exceed %d days", int(maxInviteLinkTTL.Hours()/24))
	}
	if maxUses != nil {
		if *maxUses < 0 {
			return nil, errors.New("max_uses cannot be negative")
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
	link, err := s.inviteLinkStore().CreateInviteLink(ctx, conversationID, actorID, code, &expiresAt, maxUses)
	if err != nil {
		return nil, err
	}
	return s.inviteLinkResponse(link), nil
}

// GetInviteLink returns the group's live link (nil, ErrInviteLinkNotFound
// when none).
func (s *Service) GetInviteLink(ctx context.Context, actorID, conversationID uuid.UUID) (*InviteLinkResponse, error) {
	if _, _, err := s.requireGroupRole(ctx, conversationID, actorID, "owner", "admin"); err != nil {
		return nil, err
	}
	link, err := s.inviteLinkStore().GetLiveInviteLink(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, ErrInviteLinkNotFound
	}
	return s.inviteLinkResponse(link), nil
}

// RevokeInviteLink revokes the live link. Idempotent.
func (s *Service) RevokeInviteLink(ctx context.Context, actorID, conversationID uuid.UUID) error {
	if _, _, err := s.requireGroupRole(ctx, conversationID, actorID, "owner", "admin"); err != nil {
		return err
	}
	_, err := s.inviteLinkStore().RevokeInviteLink(ctx, conversationID)
	return err
}

func inviteLinkIsLive(l *postgres.GroupInviteLink, now time.Time) bool {
	if l.RevokedAt != nil {
		return false
	}
	if l.ExpiresAt != nil && !l.ExpiresAt.After(now) {
		return false
	}
	if l.MaxUses != nil && l.Uses >= *l.MaxUses {
		return false
	}
	return true
}

// PreviewInvite describes the group behind a code without joining.
func (s *Service) PreviewInvite(ctx context.Context, viewerID uuid.UUID, code string) (*InvitePreview, error) {
	if !ValidInviteCode(code) {
		return nil, ErrInviteLinkNotFound
	}
	link, err := s.inviteLinkStore().GetInviteLinkByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, ErrInviteLinkNotFound
	}
	conv, err := s.convStore.GetConversation(ctx, link.ConversationID)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return nil, ErrInviteLinkNotFound
	}
	count, err := s.groupStore().CountActiveMembers(ctx, conv.ID)
	if err != nil {
		return nil, err
	}
	isMember, err := s.convStore.CheckMembership(ctx, conv.ID, viewerID)
	if err != nil {
		return nil, err
	}
	preview := &InvitePreview{
		Code:           link.Code,
		ConversationID: conv.ID,
		Description:    conv.Description,
		AvatarMediaID:  conv.AvatarMediaID,
		MemberCount:    count,
		ExpiresAt:      link.ExpiresAt,
		IsLive:         inviteLinkIsLive(link, time.Now()),
		IsMember:       isMember,
	}
	if conv.Title != nil {
		preview.Title = *conv.Title
	}
	if conv.AvatarMediaID != nil {
		// The invitee is not a member yet, so the roster-scoped delivery
		// authority denies them; the group's own admin is the viewer that
		// can vouch for the avatar on a preview.
		urls := s.fetchMediaURLs(ctx, link.CreatedBy, []string{conv.AvatarMediaID.String()})
		preview.AvatarURL = urls[conv.AvatarMediaID.String()]
	}
	return preview, nil
}

// JoinByInvite adds the caller through a live link. Idempotent for existing
// members (no use is consumed). Rate limited 20/hour/user.
func (s *Service) JoinByInvite(ctx context.Context, userID uuid.UUID, code string) (*ConversationResponse, error) {
	if !ValidInviteCode(code) {
		return nil, ErrInviteLinkNotFound
	}
	if err := s.checkRateLimit(ctx, inviteJoinRateLimit, userID); err != nil {
		return nil, err
	}
	link, err := s.inviteLinkStore().GetInviteLinkByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, ErrInviteLinkNotFound
	}
	if !inviteLinkIsLive(link, time.Now()) {
		return nil, ErrInviteLinkNotLive
	}
	conv, err := s.convStore.GetConversation(ctx, link.ConversationID)
	if err != nil {
		return nil, err
	}
	if conv == nil || conv.Type != "group" {
		return nil, ErrInviteLinkNotFound
	}
	alreadyMember, err := s.convStore.CheckMembership(ctx, conv.ID, userID)
	if err != nil {
		return nil, err
	}
	if alreadyMember {
		return s.getConversationResponseFor(ctx, userID, conv.ID)
	}
	count, err := s.groupStore().CountActiveMembers(ctx, conv.ID)
	if err != nil {
		return nil, err
	}
	if count >= GroupMemberCap {
		return nil, postgres.ErrGroupFull
	}
	// Roster block check (P0-5 rule carried over from add-member): a link
	// must never co-locate two users who block each other.
	blocked, err := s.blockedWithAnyMember(ctx, conv.ID, userID)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, ErrInviteJoinBlocked
	}
	// Consume FIRST (atomic against revoke/expiry/max_uses), then the capped
	// add. A crash between the two burns one use — acceptable; the reverse
	// order could admit past max_uses.
	if _, err := s.inviteLinkStore().ConsumeInviteLink(ctx, code); err != nil {
		return nil, err
	}
	if err := s.groupStore().AddMemberCapped(ctx, conv.ID, userID, "member", GroupMemberCap); err != nil {
		return nil, err
	}
	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MemberAdded, sharedEvents.MemberAddedPayload{
		ConversationID: conv.ID.String(),
		UserID:         userID.String(),
		AddedBy:        link.CreatedBy.String(),
		Role:           "member",
		AddedAt:        time.Now(),
	})
	s.publishControlFrame(ctx, userID, map[string]any{
		"type": "conversation_joined", "conversation_id": conv.ID.String(),
	})
	return s.getConversationResponseFor(ctx, userID, conv.ID)
}

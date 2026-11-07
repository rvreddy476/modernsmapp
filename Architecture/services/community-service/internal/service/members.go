package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/community-service/internal/store"
	"github.com/google/uuid"
)

// Membership and the community card, on the access gate (access.go). Bans
// stick (K-L1): a banned member cannot leave, a ban cannot be re-placed to
// change who owns it, only a role at or above the banner lifts it, and a role
// change cannot be used as an unban.

// GetCommunity is the community's card. A private community the viewer is
// not in, and a community that is not active, answer exactly as a missing one
// (K-M1: there was no visibility check at all).
func (s *Service) GetCommunity(ctx context.Context, communityID uuid.UUID, viewerID *uuid.UUID) (*CommunityWithMembership, error) {
	acc, err := s.authorize(ctx, communityID, viewerID, accessCard)
	if err != nil {
		return nil, err
	}
	return &CommunityWithMembership{Community: acc.c, ViewerRole: acc.role}, nil
}

func (s *Service) DeleteCommunity(ctx context.Context, communityID, actorID uuid.UUID) error {
	if _, err := s.authorizeRoleOr(ctx, communityID, actorID, "owner",
		errors.New("forbidden: only the community owner can delete the community")); err != nil {
		return err
	}

	unlock, err := s.lockMediaHolder(ctx, communityID)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.store.DeleteCommunity(ctx, communityID); err != nil {
		return fmt.Errorf("failed to delete community: %w", err)
	}
	// A deleted community is never shown again (no restore path): its avatar
	// and banner stop being held (media_claims.go).
	s.releaseCommunityMedia(ctx, communityID)

	if s.producer != nil {
		if err := s.producer.PublishCommunityDeleted(ctx, communityID, actorID); err != nil {
			slog.Warn("failed to publish community.deleted event", "error", err)
		}
	}

	return nil
}

// --- Join / Leave ---

// JoinCommunity joins or asks to join. It is not hidden behind the private
// rule: a join request is how someone who was given a private community's id
// gets in, as in channel-service. A private community is never joined
// directly, even when its row says "open" (effectiveJoinMode): the caller
// gets a pending request. A community that is not active cannot be joined,
// and a banned row is refused.
func (s *Service) JoinCommunity(ctx context.Context, communityID, userID uuid.UUID, answers json.RawMessage) (*store.CommunityMember, *store.CommunityJoinRequest, error) {
	c, err := s.store.GetCommunityByID(ctx, communityID)
	if errors.Is(err, store.ErrCommunityNotFound) {
		return nil, nil, ErrCommunityNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if c.Status != "active" {
		return nil, nil, ErrCommunityNotFound
	}

	existing, err := s.store.GetMember(ctx, communityID, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if existing != nil {
		if isBannedRole(existing.Role) {
			return nil, nil, fmt.Errorf("forbidden: you are banned from this community")
		}
		return nil, nil, fmt.Errorf("already a member of this community")
	}

	switch effectiveJoinMode(c) {
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
			if err := s.adjustMemberCount(ctx, communityID, 1); err != nil {
				slog.Warn("failed to increment member count", "error", err)
			}
		}
		// Bust the negative cache entry written by any prior viewer-side
		// GetMember call, so the new member's next request sees their
		// real role instead of the cached "_nil".
		s.invalidateMemberCache(ctx, communityID, userID)
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

// LeaveCommunity removes the caller's own row. A banned or suspended row is
// refused: deleting it erased the ban, so leaving and rejoining was a way
// round every ban (K-L1).
func (s *Service) LeaveCommunity(ctx context.Context, communityID, userID uuid.UUID) error {
	member, err := s.store.GetMember(ctx, communityID, userID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if member == nil {
		return fmt.Errorf("not a member of this community")
	}
	if isBannedRole(member.Role) {
		return ErrBannedCannotLeave
	}
	if member.Role == "owner" {
		return fmt.Errorf("forbidden: the owner cannot leave; delete the community instead")
	}

	if err := s.store.RemoveMember(ctx, communityID, userID); err != nil {
		return fmt.Errorf("failed to leave: %w", err)
	}

	// A pending row was never counted.
	if isMemberRole(member.Role) {
		if err := s.adjustMemberCount(ctx, communityID, -1); err != nil {
			slog.Warn("failed to decrement member count", "error", err)
		}
	}
	s.invalidateMemberCache(ctx, communityID, userID)

	if s.producer != nil {
		if err := s.producer.PublishMemberLeft(ctx, communityID, userID); err != nil {
			slog.Warn("failed to publish community.member.left event", "error", err)
		}
	}

	return nil
}

// --- Members ---

// ListMembers lists the members of a community the viewer may read. With
// member_directory off, only its moderators and above see the list.
func (s *Service) ListMembers(ctx context.Context, communityID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]store.CommunityMember, error) {
	acc, err := s.authorize(ctx, communityID, viewerID, accessRead)
	if err != nil {
		return nil, err
	}
	if !memberDirectoryAllowed(acc.c, acc.role) {
		return nil, ErrMemberDirectoryHidden
	}
	return s.store.ListMembers(ctx, communityID, limit, offset)
}

func (s *Service) UpdateMemberRole(ctx context.Context, communityID, targetUserID, actorID uuid.UUID, newRole string) error {
	if !assignableRoles[newRole] {
		return fmt.Errorf("invalid: role is not valid")
	}

	acc, err := s.authorizeRoleOr(ctx, communityID, actorID, "admin",
		errors.New("forbidden: only admins and above can change roles"))
	if err != nil {
		return err
	}

	target, err := s.store.GetMember(ctx, communityID, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to check target membership: %w", err)
	}
	if target == nil {
		return fmt.Errorf("member not found")
	}
	if isBannedRole(target.Role) {
		return ErrTargetBanned
	}

	targetRole := effectiveRole(acc.c, &targetUserID, target.Role)
	// Cannot change role of someone at same or higher level
	if roleLevel(targetRole) >= roleLevel(acc.role) {
		return fmt.Errorf("forbidden: cannot change role of someone at your level or above")
	}
	// Cannot promote above own level
	if roleLevel(newRole) >= roleLevel(acc.role) {
		return fmt.Errorf("forbidden: cannot promote to your level or above")
	}

	if err := s.store.UpdateMemberRole(ctx, communityID, targetUserID, newRole); err != nil {
		return err
	}
	s.invalidateMemberCache(ctx, communityID, targetUserID)

	if s.producer != nil {
		if err := s.producer.PublishMemberRoleChanged(ctx, communityID, targetUserID, actorID, newRole); err != nil {
			slog.Warn("failed to publish community.member.role_changed event", "error", err)
		}
	}

	return nil
}

func (s *Service) BanMember(ctx context.Context, communityID, targetUserID, actorID uuid.UUID, reason string) error {
	acc, err := s.authorizeRoleOr(ctx, communityID, actorID, "moderator",
		errors.New("forbidden: only moderators and above can ban members"))
	if err != nil {
		return err
	}

	target, err := s.store.GetMember(ctx, communityID, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to check target membership: %w", err)
	}
	if target == nil {
		return fmt.Errorf("member not found")
	}
	// A second ban would rewrite banned_by (handing the ban to the
	// re-banner, who could then lift an admin's ban) and decrement the
	// member count again.
	if isBannedRole(target.Role) {
		return ErrAlreadyBanned
	}
	targetRole := effectiveRole(acc.c, &targetUserID, target.Role)
	if roleLevel(targetRole) >= roleLevel(acc.role) {
		return fmt.Errorf("forbidden: cannot ban someone at your level or above")
	}

	if err := s.store.BanMember(ctx, communityID, targetUserID, actorID, reason); err != nil {
		return fmt.Errorf("failed to ban member: %w", err)
	}
	s.invalidateMemberCache(ctx, communityID, targetUserID)

	if isMemberRole(target.Role) {
		if err := s.adjustMemberCount(ctx, communityID, -1); err != nil {
			slog.Warn("failed to decrement member count", "error", err)
		}
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

// UnbanMember lifts a ban. Moderators and above, and only a ban placed by
// someone at the actor's level or below: before, any moderator lifted a ban
// an admin or the owner had placed (K-L1).
func (s *Service) UnbanMember(ctx context.Context, communityID, targetUserID, actorID uuid.UUID) error {
	acc, err := s.authorizeRoleOr(ctx, communityID, actorID, "moderator",
		errors.New("forbidden: only moderators and above can unban members"))
	if err != nil {
		return err
	}

	ban, err := s.store.GetMember(ctx, communityID, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to check target membership: %w", err)
	}
	if ban == nil || ban.Role != "banned" {
		return fmt.Errorf("member not found or not banned")
	}
	placedBy, err := s.bannerRole(ctx, acc.c, ban)
	if err != nil {
		return err
	}
	if err := guardUnban(acc.role, placedBy); err != nil {
		return err
	}

	if err := s.store.UnbanMember(ctx, communityID, targetUserID); err != nil {
		return err
	}
	s.invalidateMemberCache(ctx, communityID, targetUserID)

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

// ListSpaces lists the spaces of a community the viewer may read (K-M1: it
// was ungated).
func (s *Service) ListSpaces(ctx context.Context, communityID uuid.UUID, viewerID *uuid.UUID) ([]store.CommunitySpace, error) {
	if _, err := s.authorize(ctx, communityID, viewerID, accessRead); err != nil {
		return nil, err
	}
	return s.store.ListSpaces(ctx, communityID)
}
